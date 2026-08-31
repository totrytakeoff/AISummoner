package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/wsstream"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/ssh"
)

var ErrPlaintextTunnel = errors.New("plaintext tunnel requires explicit development mode")

type PairingNotification struct {
	Code      string
	ExpiresAt time.Time
}

type ClientPhase string

const (
	ClientPhaseConnecting ClientPhase = "connecting"
	ClientPhaseOnline     ClientPhase = "online"
	ClientPhaseRetrying   ClientPhase = "retrying"
	ClientPhaseStopped    ClientPhase = "stopped"
)

type StateNotification struct {
	Phase         ClientPhase
	RetryIn       time.Duration
	ErrorCategory string
}

type StreamNotification struct {
	Opened bool
}

type ClientSession struct {
	ConnectionID       string
	SSHClientPublicKey ssh.PublicKey
}

type StreamHandler func(context.Context, net.Conn, protocol.StreamHeader, ClientSession)

type ClientOptions struct {
	ServerURL      string
	DevMode        bool
	Identity       *identity.Identity
	DeviceName     string
	Platform       string
	Arch           string
	ClientVersion  string
	HTTPClient     *http.Client
	Logger         *slog.Logger
	OnPairing      func(PairingNotification)
	OnOnline       func(ClientSession)
	OnState        func(StateNotification)
	OnStream       func(StreamNotification)
	StreamHandler  StreamHandler
	StableOnline   time.Duration
	ConnectTimeout time.Duration
	Jitter         func(time.Duration) time.Duration
}

type Client struct {
	endpoint       string
	identity       *identity.Identity
	deviceName     string
	platform       string
	arch           string
	clientVersion  string
	httpClient     *http.Client
	logger         *slog.Logger
	onPairing      func(PairingNotification)
	onOnline       func(ClientSession)
	onState        func(StateNotification)
	onStream       func(StreamNotification)
	streamHandler  StreamHandler
	stableOnline   time.Duration
	connectTimeout time.Duration
	jitter         func(time.Duration) time.Duration
}

func NewClient(options ClientOptions) (*Client, error) {
	if options.Identity == nil {
		return nil, errors.New("device identity is required")
	}
	endpoint, err := ResolveTunnelURL(options.ServerURL, options.DevMode)
	if err != nil {
		return nil, err
	}
	if len(options.DeviceName) == 0 || len(options.DeviceName) > 128 || !protocol.SupportedPlatform(options.Platform) ||
		len(options.Arch) == 0 || len(options.Arch) > 64 || len(options.ClientVersion) == 0 || len(options.ClientVersion) > 64 {
		return nil, errors.New("invalid client metadata")
	}
	sourceHTTPClient := options.HTTPClient
	if sourceHTTPClient == nil {
		sourceHTTPClient = http.DefaultClient
	}
	httpClient := *sourceHTTPClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.OnPairing == nil {
		options.OnPairing = func(PairingNotification) {}
	}
	if options.OnOnline == nil {
		options.OnOnline = func(ClientSession) {}
	}
	if options.OnState == nil {
		options.OnState = func(StateNotification) {}
	}
	if options.OnStream == nil {
		options.OnStream = func(StreamNotification) {}
	}
	if options.StableOnline <= 0 {
		options.StableOnline = 30 * time.Second
	}
	if options.ConnectTimeout <= 0 {
		options.ConnectTimeout = 10 * time.Second
	}
	if options.Jitter == nil {
		options.Jitter = jitterDuration
	}
	return &Client{
		endpoint: endpoint, identity: options.Identity, deviceName: options.DeviceName,
		platform: options.Platform, arch: options.Arch, clientVersion: options.ClientVersion,
		httpClient: &httpClient, logger: options.Logger, onPairing: options.OnPairing,
		onOnline: options.OnOnline, onState: options.OnState, onStream: options.OnStream,
		streamHandler: options.StreamHandler,
		stableOnline:  options.StableOnline, connectTimeout: options.ConnectTimeout, jitter: options.Jitter,
	}, nil
}

// Run maintains the tunnel until ctx is canceled, using bounded jittered
// exponential reconnect. Cancellation is a normal shutdown and returns nil.
func (c *Client) Run(ctx context.Context) error {
	defer c.notifyState(StateNotification{Phase: ClientPhaseStopped})
	backoffIndex := 0
	for {
		c.notifyState(StateNotification{Phase: ClientPhaseConnecting})
		onlineFor, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if onlineFor >= c.stableOnline {
			backoffIndex = 0
		}
		delay := c.jitter(reconnectDelay(backoffIndex))
		if backoffIndex < 4 {
			backoffIndex++
		}
		errorCategory := publicTunnelError(err)
		c.notifyState(StateNotification{Phase: ClientPhaseRetrying, RetryIn: delay, ErrorCategory: errorCategory})
		c.logger.Warn("device tunnel disconnected", "retry_in", delay, "error", errorCategory)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (c *Client) runOnce(ctx context.Context) (time.Duration, error) {
	dialContext, cancelDial := context.WithTimeout(ctx, c.connectTimeout)
	websocketConn, response, err := websocket.Dial(dialContext, c.endpoint, &websocket.DialOptions{HTTPClient: c.httpClient})
	cancelDial()
	if err != nil {
		if response != nil {
			return 0, fmt.Errorf("dial tunnel: HTTP %d", response.StatusCode)
		}
		return 0, fmt.Errorf("dial tunnel: %w", err)
	}
	websocketConn.SetReadLimit(1024 * 1024)
	transport := wsstream.New(ctx, websocketConn)
	defer transport.Close()
	session, err := yamux.Client(transport, yamuxConfig())
	if err != nil {
		return 0, fmt.Errorf("start yamux client: %w", err)
	}
	defer session.Close()
	control, err := session.OpenStream()
	if err != nil {
		return 0, fmt.Errorf("open control stream: %w", err)
	}
	defer control.Close()
	deadline := time.Now().Add(c.connectTimeout)
	if err := control.SetDeadline(deadline); err != nil {
		return 0, err
	}
	codec := protocol.NewCodec(control)
	requestID, err := id.New("req")
	if err != nil {
		return 0, err
	}
	if err := codec.WriteHeader(protocol.StreamHeader{
		Version: protocol.Version, Kind: protocol.StreamControl, RequestID: requestID,
	}); err != nil {
		return 0, err
	}
	if err := codec.WriteMessage(protocol.TypeClientHello, requestID, protocol.ClientHello{
		DeviceID:        c.identity.DeviceID,
		DevicePublicKey: base64.RawURLEncoding.EncodeToString(c.identity.PublicKey),
		DeviceName:      c.deviceName, Platform: c.platform, Arch: c.arch, ClientVersion: c.clientVersion,
	}); err != nil {
		return 0, err
	}
	challengeMessage, err := expectMessage(codec, protocol.TypeServerChallenge)
	if err != nil {
		return 0, err
	}
	if challengeMessage.RequestID != requestID {
		return 0, errors.New("challenge request id mismatch")
	}
	var challenge protocol.ServerChallenge
	if err := protocol.DecodePayload(challengeMessage, &challenge); err != nil {
		return 0, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Nonce)
	if err != nil || len(nonce) != 32 {
		return 0, errors.New("invalid server challenge")
	}
	signature, err := c.identity.Sign(AuthenticationTranscript(nonce, c.identity.PublicKey))
	if err != nil {
		return 0, err
	}
	if err := codec.WriteMessage(protocol.TypeDeviceProof, requestID, protocol.DeviceProof{
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		return 0, err
	}
	authenticatedMessage, err := expectMessage(codec, protocol.TypeServerAuthenticated)
	if err != nil {
		return 0, err
	}
	if authenticatedMessage.RequestID != requestID {
		return 0, errors.New("authenticated request id mismatch")
	}
	var authenticated protocol.ServerAuthenticated
	if err := protocol.DecodePayload(authenticatedMessage, &authenticated); err != nil {
		return 0, err
	}
	clientKey, err := parseConnectionPublicKey(authenticated.SSHClientPublicKey)
	if err != nil || len(authenticated.ConnectionID) < 6 || authenticated.HeartbeatInterval <= 0 || authenticated.HeartbeatInterval > 60000 {
		return 0, errors.New("invalid authenticated response")
	}
	clientSession := ClientSession{ConnectionID: authenticated.ConnectionID, SSHClientPublicKey: clientKey}
	onlineAt := time.Now()
	if err := control.SetDeadline(time.Time{}); err != nil {
		return 0, err
	}
	c.onOnline(clientSession)
	c.notifyState(StateNotification{Phase: ClientPhaseOnline})
	err = c.runAuthenticated(ctx, session, control, codec, clientSession, time.Duration(authenticated.HeartbeatInterval)*time.Millisecond)
	return time.Since(onlineAt), err
}

func (c *Client) runAuthenticated(ctx context.Context, session *yamux.Session, control net.Conn, codec *protocol.Codec, clientSession ClientSession, heartbeatInterval time.Duration) error {
	runContext, cancelRun := context.WithCancel(ctx)
	heartbeatErrors := make(chan error, 1)
	acceptErrors := make(chan error, 1)
	done := make(chan struct{})
	acceptDone := make(chan struct{})
	var workers sync.WaitGroup
	defer func() {
		// Stop every producer and make AcceptStream/stream I/O return before
		// waiting. acceptStreams owns every handler Add and does not publish
		// acceptDone until no more handlers can be added and all have exited.
		close(done)
		cancelRun()
		_ = session.Close()
		_ = control.Close()
		<-acceptDone
		workers.Wait()
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		c.sendHeartbeats(done, codec, control, heartbeatInterval, heartbeatErrors)
	}()
	go func() {
		defer close(acceptDone)
		c.acceptStreams(runContext, done, session, clientSession, acceptErrors)
	}()

	controlMessages := make(chan protocol.Message, 1)
	controlErrors := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			message, err := codec.ReadMessage()
			if err != nil {
				select {
				case controlErrors <- err:
				case <-done:
				}
				return
			}
			select {
			case controlMessages <- message:
			case <-done:
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-heartbeatErrors:
			return err
		case err := <-acceptErrors:
			return err
		case err := <-controlErrors:
			return err
		case message := <-controlMessages:
			switch message.Type {
			case protocol.TypePairingOffered:
				var offer protocol.PairingOffered
				if err := protocol.DecodePayload(message, &offer); err != nil || len(offer.Code) == 0 || len(offer.Code) > 16 || offer.ExpiresAt.IsZero() {
					if err == nil {
						err = errors.New("invalid pairing offer")
					}
					return err
				}
				c.onPairing(PairingNotification{Code: offer.Code, ExpiresAt: offer.ExpiresAt})
			case protocol.TypeHeartbeatAck:
				var acknowledgement protocol.HeartbeatAck
				if err := protocol.DecodePayload(message, &acknowledgement); err != nil || acknowledgement.ReceivedAt.IsZero() {
					if err == nil {
						err = errors.New("invalid heartbeat acknowledgement")
					}
					return err
				}
			default:
				return protocol.ErrUnsupported
			}
		}
	}
}

func (c *Client) sendHeartbeats(done <-chan struct{}, codec *protocol.Codec, control net.Conn, interval time.Duration, errorsOut chan<- error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case sentAt := <-ticker.C:
			requestID, err := id.New("req")
			if err == nil {
				err = control.SetWriteDeadline(time.Now().Add(interval))
			}
			if err == nil {
				err = codec.WriteMessage(protocol.TypeDeviceHeartbeat, requestID, protocol.DeviceHeartbeat{SentAt: sentAt.UTC()})
			}
			if err != nil {
				select {
				case errorsOut <- err:
				case <-done:
				}
				return
			}
		}
	}
}

func (c *Client) acceptStreams(ctx context.Context, done <-chan struct{}, session *yamux.Session, clientSession ClientSession, errorsOut chan<- error) {
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			select {
			case errorsOut <- err:
			case <-done:
			}
			return
		}
		if err := stream.SetReadDeadline(time.Now().Add(c.connectTimeout)); err != nil {
			_ = stream.Close()
			select {
			case errorsOut <- err:
			case <-done:
			}
			return
		}
		header, err := readStreamHeader(stream)
		if err != nil || header.Kind != protocol.StreamSSH {
			_ = stream.Close()
			if err == nil {
				err = protocol.ErrUnsupported
			}
			select {
			case errorsOut <- err:
			case <-done:
			}
			return
		}
		if err := stream.SetReadDeadline(time.Time{}); err != nil {
			_ = stream.Close()
			select {
			case errorsOut <- err:
			case <-done:
			}
			return
		}
		if c.streamHandler == nil {
			_ = stream.Close()
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			c.notifyStream(StreamNotification{Opened: true})
			defer c.notifyStream(StreamNotification{Opened: false})
			c.streamHandler(ctx, stream, header, clientSession)
		}()
	}
}

// readStreamHeader consumes exactly one length-prefixed frame. Constructing a
// temporary protocol.Codec directly on stream would let its bufio.Reader
// prefetch SSH version bytes that cannot be handed back to the SSH server.
func readStreamHeader(stream io.Reader) (protocol.StreamHeader, error) {
	var length [4]byte
	if _, err := io.ReadFull(stream, length[:]); err != nil {
		return protocol.StreamHeader{}, fmt.Errorf("read stream header length: %w", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 {
		return protocol.StreamHeader{}, protocol.ErrMalformedFrame
	}
	if size > protocol.MaxControlFrameBytes {
		return protocol.StreamHeader{}, protocol.ErrFrameTooLarge
	}
	frame := make([]byte, 4+int(size))
	copy(frame, length[:])
	if _, err := io.ReadFull(stream, frame[4:]); err != nil {
		return protocol.StreamHeader{}, fmt.Errorf("read stream header: %w", err)
	}
	return protocol.NewCodec(bytes.NewBuffer(frame)).ReadHeader()
}

func ResolveTunnelURL(serverURL string, development bool) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("server URL must be absolute and contain no user info")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("server URL must not contain a path, query, or fragment")
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "wss":
	case "http":
		if !development {
			return "", ErrPlaintextTunnel
		}
		if !literalLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("%w: server address must be literal loopback", ErrPlaintextTunnel)
		}
		parsed.Scheme = "ws"
	case "ws":
		if !development {
			return "", ErrPlaintextTunnel
		}
		if !literalLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("%w: server address must be literal loopback", ErrPlaintextTunnel)
		}
	default:
		return "", errors.New("server URL must use https or wss")
	}
	parsed.Path = "/api/v1/tunnel"
	return parsed.String(), nil
}

func literalLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) notifyState(notification StateNotification) {
	if c.onState != nil {
		c.onState(notification)
	}
}

func (c *Client) notifyStream(notification StreamNotification) {
	if c.onStream != nil {
		c.onStream(notification)
	}
}

func parseConnectionPublicKey(encoded string) (ssh.PublicKey, error) {
	publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(encoded)))
	if err != nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid SSH client public key")
	}
	if publicKey.Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("SSH client public key is not Ed25519")
	}
	return publicKey, nil
}

func reconnectDelay(index int) time.Duration {
	delays := [...]time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	return delays[index]
}

func jitterDuration(base time.Duration) time.Duration {
	var randomByte [1]byte
	if _, err := io.ReadFull(rand.Reader, randomByte[:]); err != nil {
		return base
	}
	// Uniformly choose approximately [-20%, +20%].
	factorPermille := 800 + int64(randomByte[0])*400/255
	return time.Duration(int64(base) * factorPermille / 1000)
}
