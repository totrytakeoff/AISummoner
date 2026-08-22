package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/devicegate"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/requestsource"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/wsstream"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/ssh"
)

const (
	authDomain              = "aisummoner-device-auth-v1\x00"
	maxSourceLimiterEntries = 4096
)

type DeviceStore interface {
	RegisterDevice(context.Context, store.Device) (store.Device, error)
	UpdateDeviceLastSeen(context.Context, string, time.Time) error
}

type PairingOfferer interface {
	Offer(context.Context, string, time.Time) (pairing.Offer, error)
}

type TunnelAuditor interface {
	CreateAuditEvent(context.Context, store.AuditEvent) error
}

type DeviceLifecycleGate interface {
	LockDevice(context.Context, string) (func(), error)
}

type SourceResolver interface {
	Resolve(*http.Request) (string, error)
}

type GatewayOptions struct {
	Store             DeviceStore
	Pairing           PairingOfferer
	Auditor           TunnelAuditor
	Manager           *Manager
	DeviceGate        DeviceLifecycleGate
	Logger            *slog.Logger
	Now               func() time.Time
	Random            io.Reader
	AuthTimeout       time.Duration
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	MaxPreAuth        int
	PreAuthPerMinute  int
	SourceResolver    SourceResolver
}

// Gateway accepts and authenticates outbound Device tunnels.
type Gateway struct {
	store             DeviceStore
	pairing           PairingOfferer
	auditor           TunnelAuditor
	manager           *Manager
	deviceGate        DeviceLifecycleGate
	logger            *slog.Logger
	now               func() time.Time
	random            io.Reader
	authTimeout       time.Duration
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	preAuth           chan struct{}
	limiter           *sourceLimiter
	sourceResolver    SourceResolver
	ctx               context.Context
	cancel            context.CancelFunc
	handlerMu         sync.Mutex
	handlerWG         sync.WaitGroup
	closed            bool
	closeOnce         sync.Once
	closeDone         chan struct{}

	// beforePublish is a package-private deterministic test barrier at the
	// final Manager publication boundary. Production Gateways leave it nil.
	beforePublish          func()
	afterClosed            func()
	beforeExtraWatcherExit func()
}

func NewGateway(options GatewayOptions) (*Gateway, error) {
	if options.Store == nil || options.Pairing == nil || options.Manager == nil {
		return nil, errors.New("tunnel store, pairing service, and manager are required")
	}
	if options.DeviceGate == nil {
		// Standalone package users still get safe per-Gateway serialization.
		// Production composition injects the same Gate into Gateway and Device
		// Service so unpair participates in this order as well.
		options.DeviceGate = devicegate.New()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.AuthTimeout <= 0 {
		options.AuthTimeout = 10 * time.Second
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 5 * time.Second
	}
	if options.HeartbeatTimeout <= 0 {
		options.HeartbeatTimeout = 15 * time.Second
	}
	if options.HeartbeatTimeout <= options.HeartbeatInterval {
		return nil, errors.New("heartbeat timeout must exceed heartbeat interval")
	}
	if options.MaxPreAuth <= 0 {
		options.MaxPreAuth = 64
	}
	if options.PreAuthPerMinute <= 0 {
		options.PreAuthPerMinute = 20
	}
	if options.SourceResolver == nil {
		options.SourceResolver = requestsource.New(nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{
		store: options.Store, pairing: options.Pairing, auditor: options.Auditor,
		manager: options.Manager, deviceGate: options.DeviceGate,
		logger: options.Logger, now: options.Now, random: options.Random,
		authTimeout: options.AuthTimeout, heartbeatInterval: options.HeartbeatInterval,
		heartbeatTimeout: options.HeartbeatTimeout, preAuth: make(chan struct{}, options.MaxPreAuth),
		limiter: newSourceLimiter(options.PreAuthPerMinute, time.Minute), sourceResolver: options.SourceResolver,
		ctx: ctx, cancel: cancel,
		closeDone: make(chan struct{}),
	}, nil
}

func (g *Gateway) Close() {
	g.closeOnce.Do(func() {
		g.handlerMu.Lock()
		g.closed = true
		g.cancel()
		if g.afterClosed != nil {
			g.afterClosed()
		}
		g.handlerMu.Unlock()
		g.manager.CloseAll()
		g.handlerWG.Wait()
		close(g.closeDone)
	})
	<-g.closeDone
}

func (g *Gateway) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	g.handlerMu.Lock()
	if g.closed {
		g.handlerMu.Unlock()
		http.Error(writer, "tunnel gateway is closed", http.StatusServiceUnavailable)
		return
	}
	g.handlerWG.Add(1)
	g.handlerMu.Unlock()
	defer g.handlerWG.Done()

	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	source, err := g.sourceResolver.Resolve(request)
	if err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	if !g.limiter.allow(source, g.now()) {
		http.Error(writer, "too many tunnel attempts", http.StatusTooManyRequests)
		return
	}
	select {
	case g.preAuth <- struct{}{}:
	default:
		http.Error(writer, "too many unauthenticated tunnels", http.StatusServiceUnavailable)
		return
	}
	var releaseOnce sync.Once
	releasePreAuth := func() { releaseOnce.Do(func() { <-g.preAuth }) }
	defer releasePreAuth()

	websocketConn, err := websocket.Accept(writer, request, nil)
	if err != nil {
		return
	}
	websocketConn.SetReadLimit(1024 * 1024)
	ctx, cancel := context.WithCancel(g.ctx)
	defer cancel()
	transport := wsstream.New(ctx, websocketConn)
	if err := g.serveTransport(ctx, transport, source, releasePreAuth); err != nil && !errors.Is(err, context.Canceled) {
		g.logger.Warn("device tunnel closed", "error", publicTunnelError(err))
	}
	_ = transport.Close()
}

func (g *Gateway) serveTransport(ctx context.Context, transport net.Conn, source string, authenticated func()) error {
	authDeadline := g.now().Add(g.authTimeout)
	if err := transport.SetDeadline(authDeadline); err != nil {
		return err
	}
	session, err := yamux.Server(transport, yamuxConfig())
	if err != nil {
		return fmt.Errorf("start yamux server: %w", err)
	}
	defer session.Close()
	control, err := session.AcceptStream()
	if err != nil {
		return fmt.Errorf("accept control stream: %w", err)
	}
	defer control.Close()
	if err := control.SetDeadline(authDeadline); err != nil {
		return err
	}
	codec := protocol.NewCodec(control)
	header, err := codec.ReadHeader()
	if err != nil {
		return fmt.Errorf("read control header: %w", err)
	}
	if header.Kind != protocol.StreamControl {
		return errors.New("first stream is not control")
	}

	// Any further Client-opened stream violates the protocol. Server-opened
	// streams do not appear in AcceptStream on this side.
	extraDone := make(chan struct{})
	go func() {
		defer func() {
			if g.beforeExtraWatcherExit != nil {
				g.beforeExtraWatcherExit()
			}
			close(extraDone)
		}()
		extra, acceptErr := session.AcceptStream()
		if acceptErr == nil {
			_ = extra.Close()
			_ = session.Close()
		}
	}()
	defer func() {
		_ = session.Close()
		<-extraDone
	}()

	helloMessage, err := expectMessage(codec, protocol.TypeClientHello)
	if err != nil {
		return err
	}
	var hello protocol.ClientHello
	if err := protocol.DecodePayload(helloMessage, &hello); err != nil {
		return err
	}
	publicKey, err := validateHello(hello)
	if err != nil {
		return err
	}
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(g.random, nonce); err != nil {
		return fmt.Errorf("generate device challenge: %w", err)
	}
	if err := codec.WriteMessage(protocol.TypeServerChallenge, helloMessage.RequestID, protocol.ServerChallenge{
		Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	}); err != nil {
		return err
	}
	proofMessage, err := expectMessage(codec, protocol.TypeDeviceProof)
	if err != nil {
		return err
	}
	if proofMessage.RequestID != helloMessage.RequestID {
		return errors.New("device proof request id does not match challenge")
	}
	var proof protocol.DeviceProof
	if err := protocol.DecodePayload(proofMessage, &proof); err != nil {
		return err
	}
	signature, err := base64.RawURLEncoding.DecodeString(proof.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid device proof encoding")
	}
	if !ed25519.Verify(publicKey, AuthenticationTranscript(nonce, publicKey), signature) {
		return errors.New("device proof verification failed")
	}

	_, ephemeralPrivateKey, err := ed25519.GenerateKey(g.random)
	if err != nil {
		return fmt.Errorf("generate connection SSH key: %w", err)
	}
	sshSigner, err := ssh.NewSignerFromKey(ephemeralPrivateKey)
	if err != nil {
		return fmt.Errorf("create connection SSH signer: %w", err)
	}
	connectionID, err := id.New("conn")
	if err != nil {
		return err
	}
	authCtx, cancelAuth := context.WithDeadline(ctx, authDeadline)
	unlock, err := g.deviceGate.LockDevice(authCtx, hello.DeviceID)
	if err != nil {
		cancelAuth()
		return fmt.Errorf("wait for device lifecycle gate: %w", err)
	}
	now := g.now().UTC()
	lastSeen := now
	registered, err := g.store.RegisterDevice(authCtx, store.Device{
		ID: hello.DeviceID, PublicKey: append([]byte(nil), publicKey...), Name: hello.DeviceName,
		Platform: hello.Platform, Arch: hello.Arch, ClientVersion: hello.ClientVersion,
		CreatedAt: now, LastSeenAt: &lastSeen,
	})
	if err != nil {
		unlock()
		cancelAuth()
		return fmt.Errorf("register authenticated device: %w", err)
	}
	connection := newConnection(connectionID, registered.ID, session, transport, control, sshSigner, now)
	defer connection.Close()
	err = g.publishAuthenticated(authCtx, registered, connection, codec, helloMessage.RequestID, source, now, authenticated)
	unlock()
	cancelAuth()
	if err != nil {
		return err
	}
	defer func() {
		g.manager.Remove(connection)
	}()
	g.audit(ctx, registered.ID, "tunnel.authenticated")
	g.logger.Info("device tunnel authenticated", "device_id", registered.ID, "connection_id", connectionID)
	return g.heartbeatLoop(ctx, connection, codec, control)
}

// publishAuthenticated crosses the ready boundary for a Tunnel. The response
// must reach the peer before Manager publication, because Register is also the
// newest-wins point that closes an existing healthy connection.
func (g *Gateway) publishAuthenticated(ctx context.Context, registered store.Device, connection *Connection, codec *protocol.Codec, requestID, source string, now time.Time, authenticated func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := codec.WriteMessage(protocol.TypeServerAuthenticated, requestID, protocol.ServerAuthenticated{
		ConnectionID:       connection.ID,
		SSHClientPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(connection.sshSigner.PublicKey()))),
		HeartbeatInterval:  g.heartbeatInterval.Milliseconds(),
	}); err != nil {
		return err
	}
	if registered.OwnerUserID == nil {
		offer, err := g.pairing.Offer(ctx, registered.ID, now)
		if err != nil {
			return fmt.Errorf("offer device pairing: %w", err)
		}
		offerRequestID, err := id.New("req")
		if err != nil {
			return err
		}
		if err := codec.WriteMessage(protocol.TypePairingOffered, offerRequestID, protocol.PairingOffered{
			Code: offer.Code, ExpiresAt: offer.ExpiresAt,
		}); err != nil {
			return err
		}
	}
	if err := connection.transport.SetDeadline(time.Time{}); err != nil {
		return err
	}
	if err := connection.control.SetDeadline(time.Time{}); err != nil {
		return err
	}
	// Clearing a socket deadline does not prove the authentication budget is
	// still live. Recheck the authoritative context at the exact publication
	// boundary so an expired candidate cannot replace a healthy connection.
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.beforePublish != nil {
		g.beforePublish()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, accepted := g.manager.Register(connection); !accepted {
		return errors.New("tunnel manager is closed")
	}
	authenticated()
	g.limiter.succeeded(source)
	return nil
}

func (g *Gateway) heartbeatLoop(ctx context.Context, connection *Connection, codec *protocol.Codec, control net.Conn) error {
	for {
		if err := control.SetReadDeadline(g.now().Add(g.heartbeatTimeout)); err != nil {
			return err
		}
		message, err := expectMessage(codec, protocol.TypeDeviceHeartbeat)
		if err != nil {
			return fmt.Errorf("read device heartbeat: %w", err)
		}
		var heartbeat protocol.DeviceHeartbeat
		if err := protocol.DecodePayload(message, &heartbeat); err != nil || heartbeat.SentAt.IsZero() {
			if err == nil {
				err = errors.New("heartbeat sent_at is required")
			}
			return err
		}
		now := g.now().UTC()
		connection.touch(now)
		if err := g.store.UpdateDeviceLastSeen(ctx, connection.DeviceID, now); err != nil {
			return fmt.Errorf("persist device heartbeat: %w", err)
		}
		if err := control.SetWriteDeadline(now.Add(g.heartbeatTimeout)); err != nil {
			return err
		}
		if err := codec.WriteMessage(protocol.TypeHeartbeatAck, message.RequestID, protocol.HeartbeatAck{ReceivedAt: now}); err != nil {
			return fmt.Errorf("write heartbeat acknowledgement: %w", err)
		}
	}
}

func expectMessage(codec *protocol.Codec, expectedType string) (protocol.Message, error) {
	message, err := codec.ReadMessage()
	if err != nil {
		return protocol.Message{}, err
	}
	if message.Type != expectedType {
		return protocol.Message{}, fmt.Errorf("expected %s, got %s: %w", expectedType, message.Type, protocol.ErrUnsupported)
	}
	return message, nil
}

func validateHello(hello protocol.ClientHello) (ed25519.PublicKey, error) {
	if len(hello.DeviceID) > 128 || len(hello.DeviceName) == 0 || len(hello.DeviceName) > 128 ||
		hello.Platform != "linux" || len(hello.Arch) == 0 || len(hello.Arch) > 64 ||
		len(hello.ClientVersion) == 0 || len(hello.ClientVersion) > 64 {
		return nil, errors.New("invalid client hello metadata")
	}
	for _, value := range []string{hello.DeviceID, hello.DeviceName, hello.Platform, hello.Arch, hello.ClientVersion} {
		if strings.ContainsRune(value, '\x00') {
			return nil, errors.New("client hello contains NUL")
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(hello.DevicePublicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid device public key")
	}
	publicKey := ed25519.PublicKey(decoded)
	derivedID, err := id.Device(publicKey)
	if err != nil || derivedID != hello.DeviceID {
		return nil, errors.New("device id does not match public key")
	}
	return publicKey, nil
}

// AuthenticationTranscript is deliberately exported for the Client and test
// peers; it returns a fresh slice to prevent mutation of caller-owned keys.
func AuthenticationTranscript(nonce []byte, publicKey ed25519.PublicKey) []byte {
	message := make([]byte, 0, len(authDomain)+len(nonce)+len(publicKey))
	message = append(message, authDomain...)
	message = append(message, nonce...)
	message = append(message, publicKey...)
	return message
}

func yamuxConfig() *yamux.Config {
	configuration := yamux.DefaultConfig()
	configuration.EnableKeepAlive = false
	configuration.LogOutput = io.Discard
	configuration.AcceptBacklog = 32
	return configuration
}

func publicTunnelError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if netError, ok := err.(net.Error); ok && netError.Timeout() {
		return "timeout"
	}
	return "protocol_or_transport_error"
}

func (g *Gateway) audit(ctx context.Context, deviceID, eventType string) {
	if g.auditor == nil {
		return
	}
	eventID, err := id.New("audit")
	if err != nil {
		return
	}
	if err := g.auditor.CreateAuditEvent(ctx, store.AuditEvent{
		ID: eventID, DeviceID: &deviceID, EventType: eventType, MetadataJSON: "{}", CreatedAt: g.now().UTC(),
	}); err != nil {
		g.logger.Error("write tunnel audit event", "device_id", deviceID, "event_type", eventType, "error", err)
	}
}

type sourceWindow struct {
	started          time.Time
	lastObserved     time.Time
	observationOrder uint64
	count            int
}

type sourceLimiter struct {
	mu                   sync.Mutex
	entries              map[string]sourceWindow
	maximum              int
	duration             time.Duration
	latestObservation    time.Time
	nextObservationOrder uint64
}

func newSourceLimiter(maximum int, duration time.Duration) *sourceLimiter {
	return &sourceLimiter{entries: make(map[string]sourceWindow), maximum: maximum, duration: duration}
}

func (limiter *sourceLimiter) allow(source string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	observedAt, order := limiter.observeLocked(now)
	entry, ok := limiter.entries[source]
	if ok {
		entry.lastObserved = observedAt
		entry.observationOrder = order
		if now.Sub(entry.started) >= limiter.duration {
			entry.started = now
			entry.count = 1
			limiter.entries[source] = entry
			return true
		}
		if entry.count >= limiter.maximum {
			limiter.entries[source] = entry
			return false
		}
		entry.count++
		limiter.entries[source] = entry
		return true
	}

	// A new source is the only operation that can grow the map. Reclaim every
	// expired fixed-window entry first, then evict exactly one LRU entry if the
	// hard cap is still full. Both scans are synchronously bounded by the cap;
	// the limiter never creates a per-source cleanup goroutine.
	limiter.reclaimExpiredLocked(now)
	if len(limiter.entries) >= maxSourceLimiterEntries {
		limiter.evictLeastRecentlyObservedLocked()
	}
	limiter.entries[source] = sourceWindow{
		started: now, lastObserved: observedAt, observationOrder: order, count: 1,
	}
	return true
}

func (limiter *sourceLimiter) observeLocked(now time.Time) (time.Time, uint64) {
	// Do not move observation time backwards if the wall clock is adjusted.
	// The strictly increasing order preserves deterministic LRU ordering when
	// multiple observations share one timestamp.
	if limiter.latestObservation.IsZero() || now.After(limiter.latestObservation) {
		limiter.latestObservation = now
	}
	limiter.nextObservationOrder++
	return limiter.latestObservation, limiter.nextObservationOrder
}

func (limiter *sourceLimiter) reclaimExpiredLocked(now time.Time) {
	for source, entry := range limiter.entries {
		// A clock rollback produces a negative delta and must not expire state.
		if now.Sub(entry.started) >= limiter.duration {
			delete(limiter.entries, source)
		}
	}
}

func (limiter *sourceLimiter) evictLeastRecentlyObservedLocked() {
	var oldestSource string
	var oldest sourceWindow
	found := false
	for source, entry := range limiter.entries {
		if !found || entry.lastObserved.Before(oldest.lastObserved) ||
			(entry.lastObserved.Equal(oldest.lastObserved) &&
				(entry.observationOrder < oldest.observationOrder ||
					(entry.observationOrder == oldest.observationOrder && source < oldestSource))) {
			oldestSource = source
			oldest = entry
			found = true
		}
	}
	if found {
		delete(limiter.entries, oldestSource)
	}
}

func (limiter *sourceLimiter) succeeded(source string) {
	limiter.mu.Lock()
	delete(limiter.entries, source)
	limiter.mu.Unlock()
}
