package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/coder/websocket"
)

type fakePTY struct {
	input        io.Writer
	outputRead   *io.PipeReader
	outputWrite  *io.PipeWriter
	waitDone     chan struct{}
	waitReturned chan struct{}
	closed       chan struct{}
	waitOnce     sync.Once
	closeOnce    sync.Once

	mu         sync.Mutex
	resizes    [][2]uint16
	waitError  error
	inputPanic any
}

func newFakePTY(input io.Writer) *fakePTY {
	outputRead, outputWrite := io.Pipe()
	return &fakePTY{
		input: input, outputRead: outputRead, outputWrite: outputWrite,
		waitDone: make(chan struct{}), waitReturned: make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (pty *fakePTY) Input() io.Writer {
	if pty.inputPanic != nil {
		panic(pty.inputPanic)
	}
	return pty.input
}
func (pty *fakePTY) Output() io.Reader { return pty.outputRead }

func (pty *fakePTY) Resize(cols, rows uint16) error {
	pty.mu.Lock()
	pty.resizes = append(pty.resizes, [2]uint16{cols, rows})
	pty.mu.Unlock()
	return nil
}

func (pty *fakePTY) Wait() error {
	<-pty.waitDone
	pty.mu.Lock()
	err := pty.waitError
	pty.mu.Unlock()
	close(pty.waitReturned)
	return err
}

func (pty *fakePTY) Close() error {
	pty.closeOnce.Do(func() {
		_ = pty.outputRead.Close()
		_ = pty.outputWrite.Close()
		pty.waitOnce.Do(func() { close(pty.waitDone) })
		close(pty.closed)
	})
	return nil
}

func (pty *fakePTY) finishWait(err error) {
	pty.mu.Lock()
	pty.waitError = err
	pty.mu.Unlock()
	pty.waitOnce.Do(func() { close(pty.waitDone) })
}

func (pty *fakePTY) resizeSnapshot() [][2]uint16 {
	pty.mu.Lock()
	defer pty.mu.Unlock()
	return append([][2]uint16(nil), pty.resizes...)
}

type serverFixture struct {
	handler *Handler
	server  *httptest.Server
	origin  string
	logger  *bytes.Buffer
	mount   func(http.Handler)

	mu           sync.Mutex
	ptys         []*fakePTY
	openRecords  []openRecord
	openError    error
	openPanic    any
	openGate     <-chan struct{}
	openSeen     chan<- struct{}
	input        func() io.Writer
	configurePTY func(*fakePTY)
}

type openRecord struct {
	deviceID string
	cols     uint16
	rows     uint16
}

func newServerFixture(t *testing.T) *serverFixture {
	t.Helper()
	fixture := &serverFixture{logger: &bytes.Buffer{}, input: func() io.Writer { return &bytes.Buffer{} }}
	var mounted atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := mounted.Load()
		if value == nil {
			http.Error(writer, "handler unavailable", http.StatusServiceUnavailable)
			return
		}
		value.(mountedHandler).handler.ServeHTTP(writer, request)
	}))
	server.Start()
	fixture.server = server
	fixture.origin = server.URL
	fixture.mount = func(handler http.Handler) { mounted.Store(mountedHandler{handler: handler}) }
	t.Cleanup(server.Close)
	handler, err := New(Options{
		Auth: authFunc(func(_ context.Context, token string, _ time.Time) (store.User, error) {
			if token != testToken {
				return store.User{}, auth.ErrInvalidCredentials
			}
			return store.User{ID: testUserID}, nil
		}),
		Devices: deviceFunc(func(_ context.Context, userID, deviceID string) (store.Device, error) {
			if userID != testUserID || !strings.HasPrefix(deviceID, "dev_") {
				return store.Device{}, store.ErrNotFound
			}
			return store.Device{ID: deviceID}, nil
		}),
		Online: onlineFunc(func(string) bool { return true }),
		OpenPTY: func(ctx context.Context, deviceID string, cols, rows uint16) (PTY, error) {
			fixture.mu.Lock()
			fixture.openRecords = append(fixture.openRecords, openRecord{deviceID: deviceID, cols: cols, rows: rows})
			openSeen, openGate := fixture.openSeen, fixture.openGate
			openError, openPanic := fixture.openError, fixture.openPanic
			input, configurePTY := fixture.input, fixture.configurePTY
			fixture.mu.Unlock()
			if openSeen != nil {
				select {
				case openSeen <- struct{}{}:
				default:
				}
			}
			if openGate != nil {
				select {
				case <-openGate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if openPanic != nil {
				panic(openPanic)
			}
			if openError != nil {
				return nil, openError
			}
			pty := newFakePTY(input())
			if configurePTY != nil {
				configurePTY(pty)
			}
			fixture.mu.Lock()
			fixture.ptys = append(fixture.ptys, pty)
			fixture.mu.Unlock()
			return pty, nil
		},
		AllowedOrigin: fixture.origin,
		Logger:        slog.New(slog.NewJSONHandler(fixture.logger, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	fixture.mount(handler.Handler())
	t.Cleanup(func() {
		fixture.handler.Close()
	})
	return fixture
}

type mountedHandler struct {
	handler http.Handler
}

func (fixture *serverFixture) dial(t *testing.T, deviceID string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	headers.Set("Origin", fixture.origin)
	headers.Set("Cookie", (&http.Cookie{Name: sessionCookieName, Value: testToken}).String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return websocket.Dial(ctx, fixture.server.URL+terminalPath(deviceID), &websocket.DialOptions{HTTPHeader: headers})
}

func (fixture *serverFixture) latestPTY(t *testing.T) *fakePTY {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fixture.mu.Lock()
		if len(fixture.ptys) > 0 {
			pty := fixture.ptys[len(fixture.ptys)-1]
			fixture.mu.Unlock()
			return pty
		}
		fixture.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("PTY was not opened")
	return nil
}

func (fixture *serverFixture) setInput(input func() io.Writer) {
	fixture.mu.Lock()
	fixture.input = input
	fixture.mu.Unlock()
}

func (fixture *serverFixture) setOpenFailure(openError error, openPanic any) {
	fixture.mu.Lock()
	fixture.openError = openError
	fixture.openPanic = openPanic
	fixture.mu.Unlock()
}

func (fixture *serverFixture) setOpenBarrier(gate <-chan struct{}, seen chan<- struct{}) {
	fixture.mu.Lock()
	fixture.openGate = gate
	fixture.openSeen = seen
	fixture.mu.Unlock()
}

func (fixture *serverFixture) setPTYConfiguration(configure func(*fakePTY)) {
	fixture.mu.Lock()
	fixture.configurePTY = configure
	fixture.mu.Unlock()
}

func (fixture *serverFixture) latestOpenRecord(t *testing.T) openRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fixture.mu.Lock()
		if len(fixture.openRecords) > 0 {
			record := fixture.openRecords[len(fixture.openRecords)-1]
			fixture.mu.Unlock()
			return record
		}
		fixture.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("PTY open was not recorded")
	return openRecord{}
}

func (fixture *serverFixture) handshakeRequest(t *testing.T, deviceID string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, fixture.server.URL+terminalPath(deviceID), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", fixture.origin)
	request.Header.Set("Cookie", (&http.Cookie{Name: sessionCookieName, Value: testToken}).String())
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return request
}

func (fixture *serverFixture) silentRawPeer(t *testing.T, deviceID string) net.Conn {
	t.Helper()
	serverURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", serverURL.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n",
		terminalPath(deviceID), serverURL.Host, fixture.origin,
		(&http.Cookie{Name: sessionCookieName, Value: testToken}).String(),
	)
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, request); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		contents, _ := io.ReadAll(response.Body)
		response.Body.Close()
		connection.Close()
		t.Fatalf("raw WebSocket status = %d body=%s", response.StatusCode, contents)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	return connection
}

func TestPredictableHandshakeFailuresUseJSONBeforeAdmission(t *testing.T) {
	tests := []struct {
		name       string
		wantStatus int
		wantCode   string
		mutate     func(*http.Request)
	}{
		{
			name: "missing upgrade headers", wantStatus: http.StatusUpgradeRequired, wantCode: "WEBSOCKET_UPGRADE_REQUIRED",
			mutate: func(request *http.Request) { request.Header.Del("Connection"); request.Header.Del("Upgrade") },
		},
		{
			name: "malformed connection token", wantStatus: http.StatusUpgradeRequired, wantCode: "WEBSOCKET_UPGRADE_REQUIRED",
			mutate: func(request *http.Request) { request.Header.Set("Connection", "keep-alive-private-sentinel") },
		},
		{
			name: "malformed upgrade token", wantStatus: http.StatusUpgradeRequired, wantCode: "WEBSOCKET_UPGRADE_REQUIRED",
			mutate: func(request *http.Request) { request.Header.Set("Upgrade", "not-websocket-private-sentinel") },
		},
		{
			name: "missing version", wantStatus: http.StatusBadRequest, wantCode: "INVALID_WEBSOCKET_HANDSHAKE",
			mutate: func(request *http.Request) { request.Header.Del("Sec-WebSocket-Version") },
		},
		{
			name: "invalid version", wantStatus: http.StatusBadRequest, wantCode: "INVALID_WEBSOCKET_HANDSHAKE",
			mutate: func(request *http.Request) { request.Header.Set("Sec-WebSocket-Version", "12-private-sentinel") },
		},
		{
			name: "missing key", wantStatus: http.StatusBadRequest, wantCode: "INVALID_WEBSOCKET_HANDSHAKE",
			mutate: func(request *http.Request) { request.Header.Del("Sec-WebSocket-Key") },
		},
		{
			name: "invalid key", wantStatus: http.StatusBadRequest, wantCode: "INVALID_WEBSOCKET_HANDSHAKE",
			mutate: func(request *http.Request) { request.Header.Set("Sec-WebSocket-Key", "key-private-sentinel") },
		},
		{
			name: "duplicate key", wantStatus: http.StatusBadRequest, wantCode: "INVALID_WEBSOCKET_HANDSHAKE",
			mutate: func(request *http.Request) {
				request.Header["Sec-WebSocket-Key"] = []string{"dGhlIHNhbXBsZSBub25jZQ==", "duplicate-private-sentinel"}
			},
		},
		{
			name: "origin host mismatch", wantStatus: http.StatusForbidden, wantCode: "ORIGIN_FORBIDDEN",
			mutate: func(request *http.Request) { request.Host = "wrong-host-private-sentinel.example" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServerFixture(t)
			request := fixture.handshakeRequest(t, testDeviceID)
			test.mutate(request)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			assertHandshakeAPIError(t, response, test.wantStatus, test.wantCode)
			assertNoPTYOrSession(t, fixture)
		})
	}
}

func TestHTTP10HandshakeFailureUsesJSONBeforeAdmission(t *testing.T) {
	fixture := newServerFixture(t)
	serverURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", serverURL.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	requestText := fmt.Sprintf(
		"GET %s HTTP/1.0\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n",
		terminalPath(testDeviceID), serverURL.Host, fixture.origin,
		(&http.Cookie{Name: sessionCookieName, Value: testToken}).String(),
	)
	if _, err := io.WriteString(connection, requestText); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	assertHandshakeAPIError(t, response, http.StatusUpgradeRequired, "WEBSOCKET_UPGRADE_REQUIRED")
	assertNoPTYOrSession(t, fixture)
}

type opaqueResponseWriter struct {
	http.ResponseWriter
}

func TestNonHijackerHandshakeFailureUsesJSONBeforeAdmission(t *testing.T) {
	fixture := newServerFixture(t)
	fixture.mount(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.handler.ServeHTTP(opaqueResponseWriter{ResponseWriter: writer}, request)
	}))
	response, err := http.DefaultClient.Do(fixture.handshakeRequest(t, testDeviceID))
	if err != nil {
		t.Fatal(err)
	}
	assertHandshakeAPIError(t, response, http.StatusNotImplemented, "WEBSOCKET_UNAVAILABLE")
	assertNoPTYOrSession(t, fixture)
}

type unwrappingResponseWriter struct {
	http.ResponseWriter
}

func (writer unwrappingResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func TestResponseWriterUnwrapPreservesRealUpgrade(t *testing.T) {
	fixture := newServerFixture(t)
	fixture.mount(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.handler.ServeHTTP(unwrappingResponseWriter{ResponseWriter: writer}, request)
	}))
	connection, response, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatalf("upgrade through Unwrap: %v response=%v", err, response)
	}
	pty := fixture.latestPTY(t)
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	waitSignal(t, pty.closed, "unwrapped writer Terminal did not close")
	waitNoSessions(t, fixture.handler)
}

func TestRealWebSocketBridgesBinaryOutputAndResize(t *testing.T) {
	fixture := newServerFixture(t)
	input := &lockedBuffer{maximum: 2}
	fixture.setInput(func() io.Writer { return input })
	connection, response, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatalf("dial terminal: %v response=%v", err, response)
	}
	pty := fixture.latestPTY(t)
	open := fixture.latestOpenRecord(t)
	if open.deviceID != testDeviceID || open.cols != defaultCols || open.rows != defaultRows {
		t.Fatalf("initial PTY open = %+v", open)
	}

	browserInput := []byte("terminal-input-sentinel\x00\xff")
	writeContext, cancelWrite := context.WithTimeout(context.Background(), time.Second)
	err = connection.Write(writeContext, websocket.MessageBinary, browserInput)
	cancelWrite()
	if err != nil {
		t.Fatal(err)
	}
	waitBytes(t, input, browserInput, "browser binary did not reach PTY")

	resize := []byte(`{"type":"terminal.resize","cols":123,"rows":45}`)
	writeContext, cancelWrite = context.WithTimeout(context.Background(), time.Second)
	err = connection.Write(writeContext, websocket.MessageText, resize)
	cancelWrite()
	if err != nil {
		t.Fatal(err)
	}
	waitResize(t, pty, [2]uint16{123, 45})

	remoteOutput := []byte("terminal-output-sentinel\x00\xff")
	if _, err := pty.outputWrite.Write(remoteOutput); err != nil {
		t.Fatal(err)
	}
	readContext, cancelRead := context.WithTimeout(context.Background(), time.Second)
	messageType, contents, err := connection.Read(readContext)
	cancelRead()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary || !bytes.Equal(contents, remoteOutput) {
		t.Fatalf("browser output type=%v contents=%v", messageType, contents)
	}

	_ = connection.Close(websocket.StatusNormalClosure, "page closed")
	waitSignal(t, pty.closed, "browser close did not close PTY")
	waitNoSessions(t, fixture.handler)
	logs := fixture.logger.String()
	if strings.Contains(logs, "terminal-input-sentinel") || strings.Contains(logs, "terminal-output-sentinel") || strings.Contains(logs, testToken) {
		t.Fatal("terminal input/output or session token leaked to logs")
	}
}

func TestRealWebSocketZeroProgressPTYInputFailsClosed(t *testing.T) {
	fixture := newServerFixture(t)
	input := &lockedBuffer{zeroOnce: true}
	fixture.setInput(func() io.Writer { return input })
	connection, _, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	pty := fixture.latestPTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = connection.Write(ctx, websocket.MessageBinary, []byte("not-written"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	_, _, err = connection.Read(ctx)
	cancel()
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("zero-progress close status = %v error=%v", websocket.CloseStatus(err), err)
	}
	waitSignal(t, pty.closed, "zero-progress input did not close PTY")
	waitNoSessions(t, fixture.handler)
}

func TestRealWebSocketMalformedControlsFailClosed(t *testing.T) {
	invalid := []string{
		`{"type":"terminal.resize","cols":1.5,"rows":24}`,
		`{"type":"terminal.resize","cols":80,"rows":24,"unknown":true}`,
		`{"type":"terminal.resize","cols":501,"rows":24}`,
		`{"type":"terminal.resize","cols":80,"rows":24} {}`,
	}
	for _, control := range invalid {
		t.Run(control, func(t *testing.T) {
			fixture := newServerFixture(t)
			connection, _, err := fixture.dial(t, testDeviceID)
			if err != nil {
				t.Fatal(err)
			}
			pty := fixture.latestPTY(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if err := connection.Write(ctx, websocket.MessageText, []byte(control)); err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			_, _, err = connection.Read(ctx)
			cancel()
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("close error = %v status=%v", err, websocket.CloseStatus(err))
			}
			waitSignal(t, pty.closed, "protocol failure did not close PTY")
		})
	}
}

func TestRealWebSocketFrameLimitClosesAndReleases(t *testing.T) {
	fixture := newServerFixture(t)
	connection, _, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	pty := fixture.latestPTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = connection.Write(ctx, websocket.MessageBinary, bytes.Repeat([]byte("x"), maxTerminalFrame+1))
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	_, _, err = connection.Read(ctx)
	cancel()
	if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversized frame status = %v error=%v", websocket.CloseStatus(err), err)
	}
	waitSignal(t, pty.closed, "oversized frame did not close PTY")
	waitNoSessions(t, fixture.handler)
}

func TestRealWebSocketRemoteEndCleansUpAndReleases(t *testing.T) {
	tests := []struct {
		name       string
		waitError  error
		wantStatus websocket.StatusCode
	}{
		{name: "EOF", wantStatus: websocket.StatusNormalClosure},
		{name: "Tunnel loss", waitError: errors.New("tunnel-private-sentinel"), wantStatus: websocket.StatusGoingAway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServerFixture(t)
			connection, _, err := fixture.dial(t, testDeviceID)
			if err != nil {
				t.Fatal(err)
			}
			pty := fixture.latestPTY(t)
			pty.finishWait(test.waitError)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, err = connection.Read(ctx)
			cancel()
			if websocket.CloseStatus(err) != test.wantStatus {
				t.Fatalf("Remote end status = %v error=%v", websocket.CloseStatus(err), err)
			}
			waitSignal(t, pty.closed, "Remote end did not close PTY")
			waitSignal(t, pty.waitReturned, "Remote end Wait worker did not return")
			waitNoSessions(t, fixture.handler)
			if strings.Contains(err.Error(), "tunnel-private-sentinel") || strings.Contains(fixture.logger.String(), "tunnel-private-sentinel") {
				t.Fatal("raw Tunnel error leaked to close reason or logs")
			}
		})
	}
}

func TestRealWebSocketConcurrentFifthConnectionRejectedAndCapacityReturns(t *testing.T) {
	fixture := newServerFixture(t)
	type dialResult struct {
		connection *websocket.Conn
		response   *http.Response
		err        error
	}
	start := make(chan struct{})
	results := make(chan dialResult, maxTerminalsPerUser+1)
	var attempts sync.WaitGroup
	for index := 0; index < maxTerminalsPerUser+1; index++ {
		deviceID := "dev_limit_" + string(rune('a'+index))
		attempts.Add(1)
		go func() {
			defer attempts.Done()
			<-start
			connection, response, err := fixture.dial(t, deviceID)
			results <- dialResult{connection: connection, response: response, err: err}
		}()
	}
	close(start)
	attempts.Wait()
	close(results)

	connections := make([]*websocket.Conn, 0, maxTerminalsPerUser)
	rejections := 0
	for result := range results {
		if result.err == nil {
			connections = append(connections, result.connection)
			continue
		}
		if result.response == nil {
			t.Fatalf("terminal attempt failed without HTTP response: %v", result.err)
		}
		assertAPIError(t, result.response, http.StatusTooManyRequests, "TERMINAL_LIMIT")
		rejections++
	}
	if len(connections) != maxTerminalsPerUser || rejections != 1 {
		t.Fatalf("concurrent attempts: successes=%d rejections=%d", len(connections), rejections)
	}

	_ = connections[0].Close(websocket.StatusNormalClosure, "done")
	waitUserCount(t, fixture.handler, testUserID, maxTerminalsPerUser-1)
	replacement, response, err := fixture.dial(t, "dev_limit_replacement")
	if err != nil {
		t.Fatalf("capacity did not return: %v response=%v", err, response)
	}
	_ = replacement.CloseNow()
	for _, connection := range connections[1:] {
		_ = connection.CloseNow()
	}
}

func TestOpenerErrorAndCancelDeviceJoin(t *testing.T) {
	t.Run("opener error", func(t *testing.T) {
		fixture := newServerFixture(t)
		fixture.setOpenFailure(errors.New("ssh-private-sentinel"), nil)
		connection, response, err := fixture.dial(t, testDeviceID)
		if err != nil {
			t.Fatal(err)
		}
		if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("opener ran without a completed 101 response: %v", response)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, err = connection.Read(ctx)
		cancel()
		if websocket.CloseStatus(err) != websocket.StatusInternalError {
			t.Fatalf("opener close = %v status=%v", err, websocket.CloseStatus(err))
		}
		waitNoSessions(t, fixture.handler)
		if strings.Contains(err.Error(), "ssh-private-sentinel") || strings.Contains(fixture.logger.String(), "ssh-private-sentinel") {
			t.Fatal("raw opener error leaked to close reason or logs")
		}
	})

	t.Run("open session", func(t *testing.T) {
		fixture := newServerFixture(t)
		connection, _, err := fixture.dial(t, testDeviceID)
		if err != nil {
			t.Fatal(err)
		}
		pty := fixture.latestPTY(t)
		readResult := readTerminalClose(connection)
		cancelOne := make(chan struct{})
		cancelTwo := make(chan struct{})
		go func() { fixture.handler.CancelDevice(testDeviceID); close(cancelOne) }()
		go func() { fixture.handler.CancelDevice(testDeviceID); close(cancelTwo) }()
		waitSignal(t, cancelOne, "first CancelDevice did not join open Terminal")
		waitSignal(t, cancelTwo, "repeated CancelDevice did not join open Terminal")
		waitSignal(t, pty.closed, "CancelDevice did not close PTY")
		err = waitReadResult(t, readResult, "browser did not receive CancelDevice close")
		if websocket.CloseStatus(err) != websocket.StatusGoingAway {
			t.Fatalf("CancelDevice close status = %v err=%v", websocket.CloseStatus(err), err)
		}
	})

	t.Run("pre-open", func(t *testing.T) {
		fixture := newServerFixture(t)
		gate := make(chan struct{})
		seen := make(chan struct{}, 1)
		fixture.setOpenBarrier(gate, seen)
		connection, response, err := fixture.dial(t, testDeviceID)
		if err != nil {
			t.Fatal(err)
		}
		if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("blocked opener did not follow status 101: %v", response)
		}
		waitSignal(t, seen, "opener did not enter")
		readResult := readTerminalClose(connection)
		cancelReturned := make(chan struct{})
		go func() { fixture.handler.CancelDevice(testDeviceID); close(cancelReturned) }()
		waitSignal(t, cancelReturned, "CancelDevice did not join canceled opener")
		err = waitReadResult(t, readResult, "browser did not receive pre-open cancellation")
		if websocket.CloseStatus(err) != websocket.StatusGoingAway {
			t.Fatalf("pre-open cancel close = %v status=%v", err, websocket.CloseStatus(err))
		}
	})
}

func TestSilentRawPeerOpenerFailureIsForceClosedAfterGrace(t *testing.T) {
	fixture := newServerFixture(t)
	fixture.handler.closeGrace = 80 * time.Millisecond
	gate := make(chan struct{})
	seen := make(chan struct{}, 1)
	fixture.setOpenFailure(errors.New("silent-opener-private-sentinel"), nil)
	fixture.setOpenBarrier(gate, seen)

	peer := fixture.silentRawPeer(t, testDeviceID)
	defer peer.Close()
	waitSignal(t, seen, "silent-peer opener did not enter")
	started := time.Now()
	close(gate)
	waitNoSessions(t, fixture.handler)
	assertGraceBound(t, time.Since(started), fixture.handler.closeGrace)
	assertNoRegisteredSession(t, fixture.handler)
	fixture.mu.Lock()
	ptyCount := len(fixture.ptys)
	fixture.mu.Unlock()
	if ptyCount != 0 {
		t.Fatalf("opener failure unexpectedly returned %d PTYs", ptyCount)
	}
	if strings.Contains(fixture.logger.String(), "silent-opener-private-sentinel") {
		t.Fatal("silent-peer opener error leaked to logs")
	}
}

func TestSilentRawPeerCancelDeviceForceClosesAndJoinsAfterGrace(t *testing.T) {
	fixture := newServerFixture(t)
	fixture.handler.closeGrace = 80 * time.Millisecond
	peer := fixture.silentRawPeer(t, testDeviceID)
	defer peer.Close()
	pty := fixture.latestPTY(t)

	started := time.Now()
	fixture.handler.CancelDevice(testDeviceID)
	assertGraceBound(t, time.Since(started), fixture.handler.closeGrace)
	waitSignal(t, pty.closed, "silent-peer cancellation did not close PTY")
	waitSignal(t, pty.waitReturned, "silent-peer cancellation did not join PTY Wait")
	assertNoRegisteredSession(t, fixture.handler)
}

func TestPostUpgradeBoundaryPanicClosesPTYWebSocketAndSlot(t *testing.T) {
	fixture := newServerFixture(t)
	sentinel := "post-upgrade-private-sentinel"
	fixture.setPTYConfiguration(func(pty *fakePTY) { pty.inputPanic = sentinel })
	connection, response, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("panic test did not upgrade: %v", response)
	}
	pty := fixture.latestPTY(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, _, err = connection.Read(ctx)
	cancel()
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Fatalf("post-upgrade panic close = %v status=%v", err, websocket.CloseStatus(err))
	}
	waitSignal(t, pty.closed, "post-upgrade panic did not close PTY")
	waitNoSessions(t, fixture.handler)
	if strings.Contains(err.Error(), sentinel) || strings.Contains(fixture.logger.String(), sentinel) {
		t.Fatal("post-upgrade panic value leaked to close reason or logs")
	}
}

func TestCloseCancelsOpenSessionsAndIsIdempotent(t *testing.T) {
	fixture := newServerFixture(t)
	connection, _, err := fixture.dial(t, testDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	pty := fixture.latestPTY(t)
	readResult := readTerminalClose(connection)
	doneOne := make(chan struct{})
	doneTwo := make(chan struct{})
	go func() { fixture.handler.Close(); close(doneOne) }()
	go func() { fixture.handler.Close(); close(doneTwo) }()
	waitSignal(t, doneOne, "first Close did not return")
	waitSignal(t, doneTwo, "second Close did not return")
	waitSignal(t, pty.closed, "Close did not close PTY")
	err = waitReadResult(t, readResult, "browser did not receive shutdown close")
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("shutdown close = %v status=%v", err, websocket.CloseStatus(err))
	}
	_, response, err := fixture.dial(t, "dev_after_close")
	if err == nil || response == nil {
		t.Fatal("closed handler accepted a new Terminal")
	}
	assertAPIError(t, response, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
}

type blockingWebSocket struct {
	readStarted  chan struct{}
	readDone     chan struct{}
	writeStarted chan struct{}
	writeDone    chan struct{}
	closeCalled  chan struct{}

	readOnce  sync.Once
	writeOnce sync.Once
	closeOnce sync.Once

	mu        sync.Mutex
	readLimit int64
	written   []byte
	closeCode websocket.StatusCode
}

func newBlockingWebSocket() *blockingWebSocket {
	return &blockingWebSocket{
		readStarted: make(chan struct{}), readDone: make(chan struct{}),
		writeStarted: make(chan struct{}), writeDone: make(chan struct{}),
		closeCalled: make(chan struct{}),
	}
}

func (connection *blockingWebSocket) SetReadLimit(limit int64) {
	connection.mu.Lock()
	connection.readLimit = limit
	connection.mu.Unlock()
}

func (connection *blockingWebSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	connection.readOnce.Do(func() { close(connection.readStarted) })
	<-ctx.Done()
	close(connection.readDone)
	return 0, nil, ctx.Err()
}

func (connection *blockingWebSocket) Write(ctx context.Context, messageType websocket.MessageType, contents []byte) error {
	if messageType != websocket.MessageBinary {
		return errors.New("unexpected non-binary terminal output")
	}
	connection.mu.Lock()
	connection.written = append([]byte(nil), contents...)
	connection.mu.Unlock()
	connection.writeOnce.Do(func() { close(connection.writeStarted) })
	<-ctx.Done()
	close(connection.writeDone)
	return ctx.Err()
}

func (connection *blockingWebSocket) Close(code websocket.StatusCode, _ string) error {
	// The test fake deliberately does not unblock Write. Cleanup must cancel the
	// write context before attempting the WebSocket close.
	select {
	case <-connection.writeStarted:
		<-connection.writeDone
	default:
	}
	connection.mu.Lock()
	connection.closeCode = code
	connection.mu.Unlock()
	connection.closeOnce.Do(func() { close(connection.closeCalled) })
	return nil
}

func (connection *blockingWebSocket) ForceClose() error {
	connection.closeOnce.Do(func() { close(connection.closeCalled) })
	return nil
}

func TestRequestContextCancellationClosesPTYAndJoinsWorkers(t *testing.T) {
	fixture := newHandlerFixture(t)
	connection := newBlockingWebSocket()
	pty := newFakePTY(&lockedBuffer{})
	fixture.handler.accept = func(http.ResponseWriter, *http.Request) (websocketConnection, error) {
		return connection, nil
	}
	fixture.handler.openPTY = func(context.Context, string, uint16, uint16) (PTY, error) {
		return pty, nil
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := authenticatedRequest(http.MethodGet, terminalPath(testDeviceID)).WithContext(requestContext)
	requestDone := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(newHijackRecorder(), request)
		close(requestDone)
	}()
	waitSignal(t, connection.readStarted, "context-cancellation WebSocket reader did not start")
	cancelRequest()
	waitSignal(t, requestDone, "request cancellation did not join Terminal workers")
	waitSignal(t, connection.readDone, "request cancellation did not join reader")
	waitSignal(t, connection.closeCalled, "request cancellation did not close WebSocket")
	waitSignal(t, pty.closed, "request cancellation did not close PTY")
	waitSignal(t, pty.waitReturned, "request cancellation did not join PTY Wait")
	waitNoSessions(t, fixture.handler)
	connection.mu.Lock()
	closeCode := connection.closeCode
	connection.mu.Unlock()
	if closeCode != websocket.StatusGoingAway {
		t.Fatalf("request cancellation close code = %v", closeCode)
	}
}

func TestBlockedWebSocketWriterIsDirectCanceledAndJoined(t *testing.T) {
	fixture := newHandlerFixture(t)
	fixture.handler.closeGrace = 20 * time.Millisecond
	connection := newBlockingWebSocket()
	pty := newFakePTY(&lockedBuffer{})
	fixture.handler.accept = func(http.ResponseWriter, *http.Request) (websocketConnection, error) {
		return connection, nil
	}
	fixture.handler.openPTY = func(context.Context, string, uint16, uint16) (PTY, error) {
		return pty, nil
	}
	requestDone := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(newHijackRecorder(), authenticatedRequest(http.MethodGet, terminalPath(testDeviceID)))
		close(requestDone)
	}()
	waitSignal(t, connection.readStarted, "WebSocket reader did not start")

	firstOutputDone := make(chan struct{})
	go func() {
		_, _ = pty.outputWrite.Write([]byte("slow-output-private-sentinel"))
		close(firstOutputDone)
	}()
	waitSignal(t, connection.writeStarted, "WebSocket writer did not block")
	waitSignal(t, firstOutputDone, "direct output buffer did not consume the first chunk")

	secondOutputDone := make(chan struct{})
	go func() {
		_, _ = pty.outputWrite.Write([]byte("must-not-queue"))
		close(secondOutputDone)
	}()
	select {
	case <-secondOutputDone:
		t.Fatal("PTY output was read into a queue while the direct WebSocket write was blocked")
	case <-time.After(20 * time.Millisecond):
	}

	cancelDone := make(chan struct{})
	go func() {
		fixture.handler.CancelDevice(testDeviceID)
		close(cancelDone)
	}()
	waitSignal(t, cancelDone, "CancelDevice did not join blocked writer")
	waitSignal(t, requestDone, "ServeHTTP did not join Terminal workers")
	waitSignal(t, connection.writeDone, "write context was not canceled")
	waitSignal(t, connection.readDone, "read worker was not joined")
	waitSignal(t, connection.closeCalled, "WebSocket was not closed")
	waitSignal(t, secondOutputDone, "blocked PTY output producer did not unblock")
	waitSignal(t, pty.waitReturned, "PTY Wait worker was not joined")
	waitNoSessions(t, fixture.handler)

	connection.mu.Lock()
	readLimit, written, closeCode := connection.readLimit, append([]byte(nil), connection.written...), connection.closeCode
	connection.mu.Unlock()
	if readLimit != maxTerminalFrame || string(written) != "slow-output-private-sentinel" || closeCode != websocket.StatusGoingAway {
		t.Fatalf("blocked writer state: limit=%d written=%q close=%v", readLimit, written, closeCode)
	}
	if strings.Contains(fixture.logger.String(), "slow-output-private-sentinel") {
		t.Fatal("slow terminal output leaked to logs")
	}
}

type lockedBuffer struct {
	mu       sync.Mutex
	maximum  int
	zeroOnce bool
	b        bytes.Buffer
}

func (buffer *lockedBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.zeroOnce {
		buffer.zeroOnce = false
		return 0, nil
	}
	maximum := buffer.maximum
	if maximum <= 0 || maximum > len(contents) {
		maximum = len(contents)
	}
	return buffer.b.Write(contents[:maximum])
}

func (buffer *lockedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.b.Bytes()...)
}

func waitBytes(t *testing.T, buffer *lockedBuffer, want []byte, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Equal(buffer.Bytes(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s: got %v want %v", message, buffer.Bytes(), want)
}

func waitResize(t *testing.T, pty *fakePTY, want [2]uint16) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		values := pty.resizeSnapshot()
		if len(values) > 0 && values[len(values)-1] == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("resize = %v, want %v", pty.resizeSnapshot(), want)
}

func waitNoSessions(t *testing.T, handler *Handler) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		handler.mu.Lock()
		count := len(handler.byDevice)
		handler.mu.Unlock()
		if count == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Terminal sessions did not release")
}

func assertNoPTYOrSession(t *testing.T, fixture *serverFixture) {
	t.Helper()
	fixture.mu.Lock()
	openCount := len(fixture.openRecords)
	fixture.mu.Unlock()
	fixture.handler.mu.Lock()
	devices := len(fixture.handler.byDevice)
	users := len(fixture.handler.userCounts)
	fixture.handler.mu.Unlock()
	if openCount != 0 || devices != 0 || users != 0 {
		t.Fatalf("handshake crossed admission/opener or leaked capacity: opens=%d devices=%d users=%d", openCount, devices, users)
	}
}

func assertNoRegisteredSession(t *testing.T, handler *Handler) {
	t.Helper()
	handler.mu.Lock()
	devices := len(handler.byDevice)
	users := len(handler.userCounts)
	handler.mu.Unlock()
	if devices != 0 || users != 0 {
		t.Fatalf("Terminal registry did not release: devices=%d users=%d", devices, users)
	}
}

func assertHandshakeAPIError(t *testing.T, response *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("handshake status = %d, want %d body=%s", response.StatusCode, wantStatus, contents)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("handshake content type = %q", contentType)
	}
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(contents, &payload); err != nil {
		t.Fatalf("decode handshake envelope: %v body=%s", err, contents)
	}
	responseRequestID := response.Header.Get("X-Request-ID")
	if payload.Error.Code != wantCode || payload.Error.Message == "" ||
		!strings.HasPrefix(payload.Error.RequestID, "req_") || payload.Error.RequestID != responseRequestID {
		t.Fatalf("handshake error = %+v response_request_id=%q", payload.Error, responseRequestID)
	}
	if strings.Contains(string(contents), "private-sentinel") {
		t.Fatalf("raw handshake header reflected in response: %s", contents)
	}
}

func assertGraceBound(t *testing.T, elapsed, grace time.Duration) {
	t.Helper()
	if elapsed < grace/2 {
		t.Fatalf("silent-peer lifecycle returned before close grace: elapsed=%s grace=%s", elapsed, grace)
	}
	if elapsed > time.Second {
		t.Fatalf("silent-peer lifecycle exceeded bounded force-close window: elapsed=%s grace=%s", elapsed, grace)
	}
}

func waitUserCount(t *testing.T, handler *Handler, userID string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		handler.mu.Lock()
		got := handler.userCounts[userID]
		handler.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("user terminal count did not become %d", want)
}

func readTerminalClose(connection *websocket.Conn) <-chan error {
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, err := connection.Read(ctx)
		result <- err
	}()
	return result
}

func waitReadResult(t *testing.T, result <-chan error, message string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(4 * time.Second):
		t.Fatal(message)
		return nil
	}
}
