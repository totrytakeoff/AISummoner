package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/hashicorp/yamux"
)

func TestResolveTunnelURLRequiresExplicitDevModeAndLiteralLoopbackForPlaintext(t *testing.T) {
	tests := []struct {
		name          string
		serverURL     string
		development   bool
		want          string
		wantPlaintext bool
	}{
		{name: "HTTP needs dev", serverURL: "http://127.0.0.1:8080", wantPlaintext: true},
		{name: "WS needs dev", serverURL: "ws://127.0.0.1:8080", wantPlaintext: true},
		{name: "HTTP IPv4 loopback", serverURL: "http://127.42.0.1:8080", development: true, want: "ws://127.42.0.1:8080/api/v1/tunnel"},
		{name: "HTTP IPv6 loopback", serverURL: "http://[::1]:8080", development: true, want: "ws://[::1]:8080/api/v1/tunnel"},
		{name: "WS IPv4 loopback", serverURL: "ws://127.0.0.1:8080", development: true, want: "ws://127.0.0.1:8080/api/v1/tunnel"},
		{name: "HTTP hostname alias", serverURL: "http://localhost:8080", development: true, wantPlaintext: true},
		{name: "HTTP non-loopback", serverURL: "http://192.0.2.10:8080", development: true, wantPlaintext: true},
		{name: "WS non-loopback", serverURL: "ws://192.0.2.10:8080", development: true, wantPlaintext: true},
		{name: "HTTP IPv4 wildcard", serverURL: "http://0.0.0.0:8080", development: true, wantPlaintext: true},
		{name: "WS IPv6 wildcard", serverURL: "ws://[::]:8080", development: true, wantPlaintext: true},
		{name: "HTTPS remote", serverURL: "https://example.com", want: "wss://example.com/api/v1/tunnel"},
		{name: "WSS remote", serverURL: "wss://example.com", want: "wss://example.com/api/v1/tunnel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveTunnelURL(test.serverURL, test.development)
			if test.wantPlaintext {
				if !errors.Is(err, ErrPlaintextTunnel) {
					t.Fatalf("error = %v, want ErrPlaintextTunnel", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientStateCallbacksReportRetryAndJoinedStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	states := make([]StateNotification, 0, 3)
	client, err := NewClient(ClientOptions{
		ServerURL: server.URL, DevMode: true, Identity: testIdentity(t),
		DeviceName: "state-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Jitter: func(time.Duration) time.Duration { return time.Hour },
		OnState: func(notification StateNotification) {
			states = append(states, notification)
			if notification.Phase == ClientPhaseRetrying {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(states) != 3 || states[0].Phase != ClientPhaseConnecting ||
		states[1].Phase != ClientPhaseRetrying || states[2].Phase != ClientPhaseStopped {
		t.Fatalf("state callbacks = %+v", states)
	}
	if states[1].ErrorCategory != "protocol_or_transport_error" || states[1].RetryIn != time.Hour {
		t.Fatalf("retry callback = %+v", states[1])
	}
}

func TestNewClientClonesHTTPClientAndDisablesRedirectsWithoutMutation(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{MaxIdleConns: 7}
	t.Cleanup(transport.CloseIdleConnections)
	var callerRedirects atomic.Int32
	caller := &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   37 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			callerRedirects.Add(1)
			return nil
		},
	}
	client, err := NewClient(ClientOptions{
		ServerURL: "https://example.test", Identity: testIdentity(t),
		DeviceName: "clone-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		HTTPClient: caller,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient == caller {
		t.Fatal("client retained the caller's mutable HTTP client")
	}
	if client.httpClient.Transport != transport || client.httpClient.Jar != jar || client.httpClient.Timeout != caller.Timeout {
		t.Fatalf("HTTP client fields were not preserved: transport=%v jar=%v timeout=%v", client.httpClient.Transport, client.httpClient.Jar, client.httpClient.Timeout)
	}
	request, err := http.NewRequest(http.MethodGet, "https://redirect.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.httpClient.CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("cloned CheckRedirect error = %v, want http.ErrUseLastResponse", err)
	}
	if callerRedirects.Load() != 0 {
		t.Fatal("cloned client invoked the caller's redirect policy")
	}
	if err := caller.CheckRedirect(request, nil); err != nil {
		t.Fatalf("caller CheckRedirect was mutated: %v", err)
	}
	if callerRedirects.Load() != 1 {
		t.Fatalf("caller redirect invocations = %d, want 1", callerRedirects.Load())
	}
}

func TestClientRejectsHTTPAndTLSWebSocketRedirects(t *testing.T) {
	tests := []struct {
		name string
		tls  bool
	}{
		{name: "HTTP loopback development"},
		{name: "trusted TLS", tls: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var targetRequests atomic.Int32
			targetHandler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				targetRequests.Add(1)
				http.Error(writer, "redirect target must not receive tunnel traffic", http.StatusForbidden)
			})

			var target *httptest.Server
			if test.tls {
				target = httptest.NewUnstartedServer(targetHandler)
				target.StartTLS()
			} else {
				target = httptest.NewServer(targetHandler)
			}
			defer target.Close()

			var originRequests atomic.Int32
			var websocketUpgrade atomic.Bool
			originHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				originRequests.Add(1)
				websocketUpgrade.Store(request.Header.Get("Upgrade") == "websocket")
				http.Redirect(writer, request, target.URL+"/captured", http.StatusTemporaryRedirect)
			})
			var origin *httptest.Server
			if test.tls {
				origin = httptest.NewUnstartedServer(originHandler)
				origin.StartTLS()
			} else {
				origin = httptest.NewServer(originHandler)
			}
			defer origin.Close()

			httpClient := &http.Client{Timeout: 2 * time.Second}
			if test.tls {
				httpClient = trustedTLSHTTPClient(t, origin, target)
			}
			client, err := NewClient(ClientOptions{
				ServerURL: origin.URL, DevMode: !test.tls, Identity: testIdentity(t),
				DeviceName: "redirect-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
				HTTPClient: httpClient, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ConnectTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := client.runOnce(ctx); err == nil || err.Error() != "dial tunnel: HTTP 307" {
				t.Fatalf("runOnce error = %v, want dial tunnel: HTTP 307", err)
			}
			if originRequests.Load() != 1 || !websocketUpgrade.Load() {
				t.Fatalf("origin requests = %d, websocket upgrade = %v", originRequests.Load(), websocketUpgrade.Load())
			}
			if targetRequests.Load() != 0 {
				t.Fatalf("redirect target received %d request(s), want none", targetRequests.Load())
			}
		})
	}
}

func TestClientDirectTrustedTLSConnectionStillAuthenticates(t *testing.T) {
	deviceIdentity := testIdentity(t)
	deviceStore := &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)}
	manager := NewManager()
	gateway, err := NewGateway(GatewayOptions{
		Store: deviceStore, Pairing: &pairingFake{code: "K7HF-92PQ"}, Manager: manager,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HeartbeatInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	server := httptest.NewTLSServer(gateway)
	defer server.Close()

	httpClient := trustedTLSHTTPClient(t, server)
	online := make(chan ClientSession, 1)
	client, err := NewClient(ClientOptions{
		ServerURL: server.URL, Identity: deviceIdentity,
		DeviceName: "direct-tls-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		HTTPClient: httpClient, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnOnline: func(session ClientSession) {
			select {
			case online <- session:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if httpClient.CheckRedirect != nil {
		t.Fatal("NewClient mutated the caller's nil redirect policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case session := <-online:
		if session.ConnectionID == "" || session.SSHClientPublicKey == nil {
			cancel()
			t.Fatalf("invalid authenticated session: %+v", session)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("trusted TLS client did not authenticate directly")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not stop after direct TLS authentication")
	}
}

func trustedTLSHTTPClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		certificate := server.Certificate()
		if certificate == nil {
			t.Fatal("TLS test server has no certificate")
		}
		roots.AddCert(certificate)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func TestReconnectDelayIsBounded(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second}
	for index, expected := range want {
		if got := reconnectDelay(index); got != expected {
			t.Fatalf("delay %d = %v, want %v", index, got, expected)
		}
	}
}

func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	base := 10 * time.Second
	for index := 0; index < 100; index++ {
		got := jitterDuration(base)
		if got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("jitter = %v", got)
		}
	}
}

func TestClientReconnectsAndResetsBackoffAfterStableOnline(t *testing.T) {
	deviceIdentity := testIdentity(t)
	deviceStore := &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)}
	manager := NewManager()
	gateway, err := NewGateway(GatewayOptions{
		Store: deviceStore, Pairing: &pairingFake{code: "K7HF-92PQ"}, Manager: manager,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 10 * time.Millisecond, HeartbeatTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	server := httptest.NewServer(gateway)
	defer server.Close()

	baseDelays := make(chan time.Duration, 8)
	var onlineCount atomic.Int32
	client, err := NewClient(ClientOptions{
		ServerURL: server.URL, DevMode: true, Identity: deviceIdentity,
		DeviceName: "reconnect-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), StableOnline: 200 * time.Millisecond,
		Jitter: func(base time.Duration) time.Duration {
			baseDelays <- base
			return time.Millisecond
		},
		OnOnline: func(session ClientSession) {
			attempt := onlineCount.Add(1)
			go func() {
				deadline := time.Now().Add(time.Second)
				var connection *Connection
				for time.Now().Before(deadline) {
					candidate, ok := manager.Get(deviceIdentity.DeviceID)
					if ok && candidate.ID == session.ConnectionID {
						connection = candidate
						break
					}
					time.Sleep(time.Millisecond)
				}
				if connection == nil {
					return
				}
				// Two short sessions advance the backoff index from zero to two.
				// The third session stays online long enough to require a reset.
				if attempt == 3 {
					time.Sleep(300 * time.Millisecond)
				} else {
					time.Sleep(5 * time.Millisecond)
				}
				_ = connection.Close()
			}()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	var got []time.Duration
	for len(got) < 3 {
		select {
		case delay := <-baseDelays:
			got = append(got, delay)
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("client did not complete three reconnect cycles")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not stop after cancellation")
	}
	if onlineCount.Load() < 3 {
		t.Fatalf("online sessions = %d, want at least 3", onlineCount.Load())
	}
	if got[0] != time.Second || got[1] != 2*time.Second || got[2] != time.Second {
		t.Fatalf("base reconnect delays = %v, want [1s 2s 1s] after two short sessions then a stable reset", got)
	}
}

func TestClientCancellationInterruptsBackoff(t *testing.T) {
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case requestSeen <- struct{}{}:
		default:
		}
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	backoffStarted := make(chan struct{}, 1)
	client, err := NewClient(ClientOptions{
		ServerURL: server.URL, DevMode: true, Identity: testIdentity(t),
		DeviceName: "cancel-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Jitter: func(time.Duration) time.Duration {
			backoffStarted <- struct{}{}
			return time.Hour
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("client did not attempt a connection")
	}
	select {
	case <-backoffStarted:
	case <-time.After(time.Second):
		t.Fatal("client did not enter reconnect backoff")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancellation did not interrupt reconnect backoff")
	}
}

func TestReadStreamHeaderDoesNotConsumeSSHBytes(t *testing.T) {
	var stream bytes.Buffer
	if err := protocol.NewCodec(&stream).WriteHeader(protocol.StreamHeader{
		Version: protocol.Version, Kind: protocol.StreamSSH, RequestID: "req_header",
	}); err != nil {
		t.Fatal(err)
	}
	wantSSH := []byte("SSH-2.0-test\r\n")
	_, _ = stream.Write(wantSSH)
	header, err := readStreamHeader(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != protocol.StreamSSH || header.RequestID != "req_header" {
		t.Fatalf("header = %+v", header)
	}
	if got := stream.Bytes(); !bytes.Equal(got, wantSSH) {
		t.Fatalf("bytes after header = %q, want untouched SSH version %q", got, wantSSH)
	}
}

func TestAuthenticatedRunJoinsStreamHandlersOnEveryExit(t *testing.T) {
	tests := []struct {
		name string
		stop func(context.CancelFunc, *yamux.Session)
	}{
		{name: "root cancellation", stop: func(cancel context.CancelFunc, _ *yamux.Session) { cancel() }},
		{name: "tunnel disconnect", stop: func(_ context.CancelFunc, session *yamux.Session) { _ = session.Close() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverSession, remoteSession := yamuxPair(t)
			control, controlPeer := net.Pipe()
			defer controlPeer.Close()
			handlerStarted := make(chan struct{})
			releaseHandler := make(chan struct{})
			handlerExited := make(chan struct{})
			streamEvents := make(chan StreamNotification, 2)
			client := &Client{
				connectTimeout: time.Second,
				onStream:       func(notification StreamNotification) { streamEvents <- notification },
				streamHandler: func(ctx context.Context, stream net.Conn, _ protocol.StreamHeader, _ ClientSession) {
					defer close(handlerExited)
					defer stream.Close()
					close(handlerStarted)
					<-ctx.Done()
					<-releaseHandler
				},
			}
			runContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			runDone := make(chan error, 1)
			go func() {
				runDone <- client.runAuthenticated(
					runContext, remoteSession, control, protocol.NewCodec(control),
					ClientSession{ConnectionID: "conn_test"}, time.Hour,
				)
			}()
			stream, err := serverSession.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.NewCodec(stream).WriteHeader(protocol.StreamHeader{
				Version: protocol.Version, Kind: protocol.StreamSSH, RequestID: "req_join",
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-handlerStarted:
			case <-time.After(time.Second):
				t.Fatal("stream handler did not start")
			}
			if notification := <-streamEvents; !notification.Opened {
				t.Fatalf("first stream notification = %+v", notification)
			}
			test.stop(cancel, serverSession)
			select {
			case err := <-runDone:
				t.Fatalf("authenticated run returned before handler cleanup: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(releaseHandler)
			select {
			case <-handlerExited:
			case <-time.After(time.Second):
				t.Fatal("stream handler did not exit")
			}
			if notification := <-streamEvents; notification.Opened {
				t.Fatalf("final stream notification = %+v", notification)
			}
			select {
			case <-runDone:
			case <-time.After(time.Second):
				t.Fatal("authenticated run did not return after handler cleanup")
			}
		})
	}
}
