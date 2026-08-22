package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/store"
)

const (
	testOrigin   = "https://controller.example"
	testDeviceID = "dev_terminal_test"
	testUserID   = "usr_terminal_test"
	testToken    = "test-session-token-long-enough"
)

type authFunc func(context.Context, string, time.Time) (store.User, error)

func (function authFunc) Authenticate(ctx context.Context, token string, now time.Time) (store.User, error) {
	return function(ctx, token, now)
}

type deviceFunc func(context.Context, string, string) (store.Device, error)

func (function deviceFunc) DeviceByOwner(ctx context.Context, userID, deviceID string) (store.Device, error) {
	return function(ctx, userID, deviceID)
}

type onlineFunc func(string) bool

func (function onlineFunc) IsOnline(deviceID string) bool { return function(deviceID) }

type handlerFixture struct {
	handler     *Handler
	openCalls   atomic.Int32
	deviceCalls atomic.Int32
	logger      *bytes.Buffer
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	fixture := &handlerFixture{logger: &bytes.Buffer{}}
	handler, err := New(Options{
		Auth: authFunc(func(_ context.Context, token string, _ time.Time) (store.User, error) {
			if token != testToken {
				return store.User{}, auth.ErrInvalidCredentials
			}
			return store.User{ID: testUserID, Username: "admin"}, nil
		}),
		Devices: deviceFunc(func(_ context.Context, userID, deviceID string) (store.Device, error) {
			fixture.deviceCalls.Add(1)
			if userID != testUserID || deviceID != testDeviceID {
				return store.Device{}, store.ErrNotFound
			}
			return store.Device{ID: deviceID}, nil
		}),
		Online: onlineFunc(func(string) bool { return true }),
		OpenPTY: func(context.Context, string, uint16, uint16) (PTY, error) {
			fixture.openCalls.Add(1)
			return nil, errors.New("unused test opener")
		},
		AllowedOrigin: testOrigin,
		Logger:        slog.New(slog.NewJSONHandler(fixture.logger, nil)),
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	return fixture
}

func TestPreflightOrderFailsBeforeUpgradeAndPTYOpen(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		cookie     bool
		mutate     func(*handlerFixture)
		wantStatus int
		wantCode   string
		wantDevice int32
	}{
		{name: "unrelated route", method: http.MethodGet, path: "/api/v1/devices/" + testDeviceID, origin: testOrigin, cookie: true, wantStatus: 404, wantCode: "NOT_FOUND"},
		{name: "wrong method", method: http.MethodPost, path: terminalPath(testDeviceID), origin: testOrigin, cookie: true, wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED"},
		{name: "missing origin", method: http.MethodGet, path: terminalPath(testDeviceID), cookie: true, wantStatus: 403, wantCode: "ORIGIN_FORBIDDEN"},
		{name: "mismatched origin", method: http.MethodGet, path: terminalPath(testDeviceID), origin: "https://evil.example", cookie: true, wantStatus: 403, wantCode: "ORIGIN_FORBIDDEN"},
		{name: "unauthenticated", method: http.MethodGet, path: terminalPath(testDeviceID), origin: testOrigin, wantStatus: 401, wantCode: "UNAUTHENTICATED"},
		{name: "wrong owner", method: http.MethodGet, path: terminalPath(testDeviceID), origin: testOrigin, cookie: true, mutate: func(f *handlerFixture) {
			f.handler.devices = deviceFunc(func(context.Context, string, string) (store.Device, error) {
				f.deviceCalls.Add(1)
				return store.Device{}, store.ErrNotFound
			})
		}, wantStatus: 404, wantCode: "DEVICE_NOT_FOUND", wantDevice: 1},
		{name: "offline", method: http.MethodGet, path: terminalPath(testDeviceID), origin: testOrigin, cookie: true, mutate: func(f *handlerFixture) {
			f.handler.online = onlineFunc(func(string) bool { return false })
		}, wantStatus: 409, wantCode: "DEVICE_OFFLINE", wantDevice: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHandlerFixture(t)
			if test.mutate != nil {
				test.mutate(fixture)
			}
			upgrades := atomic.Int32{}
			fixture.handler.accept = func(http.ResponseWriter, *http.Request) (websocketConnection, error) {
				upgrades.Add(1)
				return nil, errors.New("unexpected upgrade")
			}
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testToken})
			}
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			assertAPIError(t, response.Result(), test.wantStatus, test.wantCode)
			if got := fixture.deviceCalls.Load(); got != test.wantDevice {
				t.Fatalf("device lookup calls = %d, want %d", got, test.wantDevice)
			}
			if upgrades.Load() != 0 || fixture.openCalls.Load() != 0 {
				t.Fatalf("preflight crossed upgrade/opener: upgrades=%d opens=%d", upgrades.Load(), fixture.openCalls.Load())
			}
		})
	}
}

func TestMultipleOriginHeadersFailBeforeAuthentication(t *testing.T) {
	fixture := newHandlerFixture(t)
	request := authenticatedRequest(http.MethodGet, terminalPath(testDeviceID))
	request.Header["Origin"] = []string{testOrigin, testOrigin}
	response := newHijackRecorder()
	fixture.handler.ServeHTTP(response, request)
	assertAPIError(t, response.Result(), http.StatusForbidden, "ORIGIN_FORBIDDEN")
	if fixture.deviceCalls.Load() != 0 || fixture.openCalls.Load() != 0 {
		t.Fatal("duplicate Origin crossed the preflight gate")
	}
}

func TestFifthTerminalRejectedAndUsersIsolated(t *testing.T) {
	fixture := newHandlerFixture(t)
	var admitted []*activeSession
	for index := 0; index < maxTerminalsPerUser; index++ {
		session, err := fixture.handler.admit(context.Background(), testUserID, "dev_"+string(rune('a'+index)), 0)
		if err != nil {
			t.Fatal(err)
		}
		admitted = append(admitted, session)
	}
	if _, err := fixture.handler.admit(context.Background(), testUserID, "dev_limit", 0); !errors.Is(err, errTerminalLimit) {
		t.Fatalf("fifth admission error = %v", err)
	}
	other, err := fixture.handler.admit(context.Background(), "usr_other", "dev_other", 0)
	if err != nil {
		t.Fatalf("different user admission failed: %v", err)
	}
	fixture.handler.release(other)

	releaseOne := admitted[0]
	fixture.handler.release(releaseOne)
	replacement, err := fixture.handler.admit(context.Background(), testUserID, "dev_replacement", 0)
	if err != nil {
		t.Fatalf("capacity did not return: %v", err)
	}
	fixture.handler.release(replacement)
	for _, session := range admitted[1:] {
		fixture.handler.release(session)
	}
}

func TestOwnerLookupCannotCrossConcurrentDeviceInvalidation(t *testing.T) {
	fixture := newHandlerFixture(t)
	lookupEntered := make(chan struct{})
	releaseLookup := make(chan struct{})
	fixture.handler.devices = deviceFunc(func(context.Context, string, string) (store.Device, error) {
		close(lookupEntered)
		<-releaseLookup
		return store.Device{ID: testDeviceID}, nil
	})
	upgrades := atomic.Int32{}
	fixture.handler.accept = func(http.ResponseWriter, *http.Request) (websocketConnection, error) {
		upgrades.Add(1)
		return nil, errors.New("unexpected upgrade")
	}
	request := authenticatedRequest(http.MethodGet, terminalPath(testDeviceID))
	response := newHijackRecorder()
	requestDone := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(response, request)
		close(requestDone)
	}()
	waitSignal(t, lookupEntered, "owner lookup did not block")
	cancelDone := make(chan struct{})
	go func() {
		fixture.handler.CancelDevice(testDeviceID)
		close(cancelDone)
	}()
	waitSignal(t, cancelDone, "CancelDevice did not finish with no admitted sessions")
	close(releaseLookup)
	waitSignal(t, requestDone, "old owner request did not finish")
	assertAPIError(t, response.Result(), http.StatusNotFound, "DEVICE_NOT_FOUND")
	if upgrades.Load() != 0 || fixture.openCalls.Load() != 0 {
		t.Fatalf("invalidated request crossed admission: upgrades=%d opens=%d", upgrades.Load(), fixture.openCalls.Load())
	}

	// A newly paired request snapshots the incremented generation and may admit.
	fixture.handler.devices = deviceFunc(func(context.Context, string, string) (store.Device, error) {
		return store.Device{ID: testDeviceID}, nil
	})
	fixture.handler.accept = func(http.ResponseWriter, *http.Request) (websocketConnection, error) {
		upgrades.Add(1)
		return nil, errors.New("intentional handshake stop")
	}
	second := newHijackRecorder()
	fixture.handler.ServeHTTP(second, authenticatedRequest(http.MethodGet, terminalPath(testDeviceID)))
	if upgrades.Load() != 1 {
		t.Fatalf("new generation did not reach upgrade: %d", upgrades.Load())
	}
}

func TestCancelDeviceAndCloseJoinPreOpenSessions(t *testing.T) {
	fixture := newHandlerFixture(t)
	session, err := fixture.handler.admit(context.Background(), testUserID, testDeviceID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cancelReturned := make(chan struct{})
	go func() {
		fixture.handler.CancelDevice(testDeviceID)
		close(cancelReturned)
	}()
	select {
	case <-cancelReturned:
		t.Fatal("CancelDevice returned before admitted session released")
	case <-time.After(20 * time.Millisecond):
	}
	if !errors.Is(context.Cause(session.ctx), errDeviceInvalidated) {
		t.Fatalf("session cancel cause = %v", context.Cause(session.ctx))
	}
	fixture.handler.release(session)
	waitSignal(t, cancelReturned, "CancelDevice did not join released session")

	second, err := fixture.handler.admit(context.Background(), testUserID, "dev_second", 0)
	if err != nil {
		t.Fatal(err)
	}
	closeOne := make(chan struct{})
	closeTwo := make(chan struct{})
	go func() { fixture.handler.Close(); close(closeOne) }()
	go func() { fixture.handler.Close(); close(closeTwo) }()
	waitContextDone(t, second.ctx, "Close did not cancel admitted session")
	if !errors.Is(context.Cause(second.ctx), errHandlerClosed) {
		t.Fatalf("Close cause = %v", context.Cause(second.ctx))
	}
	fixture.handler.release(second)
	waitSignal(t, closeOne, "first Close did not join")
	waitSignal(t, closeTwo, "second Close did not join")
	if _, err := fixture.handler.admit(context.Background(), testUserID, "dev_closed", 0); !errors.Is(err, errHandlerClosed) {
		t.Fatalf("closed handler admission error = %v", err)
	}
}

func TestReleasePublishesCompletionBeforeRegistryRemoval(t *testing.T) {
	tests := []struct {
		name      string
		lifecycle func(*Handler)
	}{
		{name: "CancelDevice", lifecycle: func(handler *Handler) { handler.CancelDevice(testDeviceID) }},
		{name: "Close", lifecycle: func(handler *Handler) { handler.Close() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHandlerFixture(t)
			session, err := fixture.handler.admit(context.Background(), testUserID, testDeviceID, 0)
			if err != nil {
				t.Fatal(err)
			}

			finishEntered := make(chan struct{})
			allowRemoval := make(chan struct{})
			session.afterFinish = func() {
				close(finishEntered)
				<-allowRemoval
			}
			releaseReturned := make(chan struct{})
			go func() {
				fixture.handler.release(session)
				close(releaseReturned)
			}()
			waitSignal(t, finishEntered, "release did not publish handler completion")
			select {
			case <-session.done:
			default:
				t.Fatal("session completion was not published before removal barrier")
			}
			select {
			case <-releaseReturned:
				t.Fatal("release crossed the deterministic removal barrier")
			default:
			}

			lifecycleStarted := make(chan struct{})
			lifecycleReturned := make(chan struct{})
			go func() {
				close(lifecycleStarted)
				test.lifecycle(fixture.handler)
				close(lifecycleReturned)
			}()
			waitSignal(t, lifecycleStarted, "lifecycle call did not start")
			close(allowRemoval)
			waitSignal(t, releaseReturned, "release did not remove completed session")
			waitSignal(t, lifecycleReturned, "lifecycle call missed completed handler")

			fixture.handler.mu.Lock()
			_, registered := fixture.handler.byDevice[testDeviceID]
			count := fixture.handler.userCounts[testUserID]
			fixture.handler.mu.Unlock()
			if registered || count != 0 {
				t.Fatalf("completed session remained registered: device=%v user_count=%d", registered, count)
			}
		})
	}
}

func TestLifecycleCancellationInterruptsAndJoinsBlockedUpgrade(t *testing.T) {
	tests := []struct {
		name      string
		cancel    func(*Handler)
		wantCause error
	}{
		{name: "CancelDevice", cancel: func(handler *Handler) { handler.CancelDevice(testDeviceID) }, wantCause: errDeviceInvalidated},
		{name: "Close", cancel: func(handler *Handler) { handler.Close() }, wantCause: errHandlerClosed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHandlerFixture(t)
			acceptEntered := make(chan struct{})
			acceptCause := make(chan error, 1)
			fixture.handler.accept = func(_ http.ResponseWriter, request *http.Request) (websocketConnection, error) {
				close(acceptEntered)
				<-request.Context().Done()
				acceptCause <- context.Cause(request.Context())
				return nil, request.Context().Err()
			}
			requestDone := make(chan struct{})
			go func() {
				fixture.handler.ServeHTTP(newHijackRecorder(), authenticatedRequest(http.MethodGet, terminalPath(testDeviceID)))
				close(requestDone)
			}()
			waitSignal(t, acceptEntered, "upgrade acceptor did not block")
			cancelDone := make(chan struct{})
			go func() {
				test.cancel(fixture.handler)
				close(cancelDone)
			}()
			waitSignal(t, cancelDone, "lifecycle cancellation did not join blocked upgrade")
			waitSignal(t, requestDone, "blocked-upgrade request did not return")
			select {
			case cause := <-acceptCause:
				if !errors.Is(cause, test.wantCause) {
					t.Fatalf("upgrade context cause = %v, want %v", cause, test.wantCause)
				}
			default:
				t.Fatal("upgrade acceptor did not observe cancellation cause")
			}
			if fixture.openCalls.Load() != 0 {
				t.Fatalf("PTY opened while upgrade was blocked: %d", fixture.openCalls.Load())
			}
			fixture.handler.mu.Lock()
			active := len(fixture.handler.byDevice)
			fixture.handler.mu.Unlock()
			if active != 0 {
				t.Fatalf("blocked-upgrade sessions remained registered: %d", active)
			}
		})
	}
}

func TestRequestPanicUsesStandardEnvelopeWithoutSecret(t *testing.T) {
	fixture := newHandlerFixture(t)
	sentinel := "terminal-secret-sentinel"
	fixture.handler.devices = deviceFunc(func(context.Context, string, string) (store.Device, error) { panic(sentinel) })
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, terminalPath(testDeviceID)))
	assertAPIError(t, response.Result(), http.StatusInternalServerError, "INTERNAL")
	if strings.Contains(response.Body.String(), sentinel) || strings.Contains(fixture.logger.String(), sentinel) {
		t.Fatal("panic sentinel leaked to response or logs")
	}
}

func authenticatedRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Host = "controller.example"
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: testToken})
	return request
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
}

func newHijackRecorder() *hijackRecorder {
	return &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (*hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test recorder cannot actually hijack")
}

func terminalPath(deviceID string) string { return terminalPrefix + deviceID + terminalSuffix }

func assertAPIError(t *testing.T, response *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d body=%s", response.StatusCode, wantStatus, contents)
	}
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != wantCode || payload.Error.Message == "" || !strings.HasPrefix(payload.Error.RequestID, "req_") {
		t.Fatalf("error payload = %+v", payload.Error)
	}
}

func waitSignal(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

func waitContextDone(t *testing.T, ctx context.Context, message string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

type shortWriter struct {
	mu       sync.Mutex
	maximum  int
	zeroOnce bool
	contents bytes.Buffer
}

func (writer *shortWriter) Write(contents []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.zeroOnce {
		writer.zeroOnce = false
		return 0, nil
	}
	maximum := writer.maximum
	if maximum <= 0 || maximum > len(contents) {
		maximum = len(contents)
	}
	return writer.contents.Write(contents[:maximum])
}

func TestWriteAllHandlesPartialAndZeroProgress(t *testing.T) {
	partial := &shortWriter{maximum: 2}
	if err := writeAll(partial, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if partial.contents.String() != "abcdef" {
		t.Fatalf("partial writer contents = %q", partial.contents.String())
	}
	zero := &shortWriter{zeroOnce: true}
	if err := writeAll(zero, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-progress error = %v", err)
	}
}

func TestStrictResizeValidation(t *testing.T) {
	valid := []byte(`{"type":"terminal.resize","cols":120,"rows":36}`)
	if cols, rows, err := decodeResize(valid); err != nil || cols != 120 || rows != 36 {
		t.Fatalf("valid resize = %dx%d err=%v", cols, rows, err)
	}
	invalid := []string{
		`{"type":"terminal.resize","cols":1.5,"rows":24}`,
		`{"type":"terminal.resize","cols":80}`,
		`{"type":"terminal.resize","cols":80,"rows":24,"extra":true}`,
		`{"type":"terminal.resize","cols":80,"rows":24} {}`,
		`{"type":"other","cols":80,"rows":24}`,
		`{"type":"terminal.resize","cols":0,"rows":24}`,
		`{"type":"terminal.resize","cols":501,"rows":24}`,
		`{"type":"terminal.resize","cols":80,"rows":0}`,
		`{"type":"terminal.resize","cols":80,"rows":301}`,
		`[]`,
	}
	for _, contents := range invalid {
		if _, _, err := decodeResize([]byte(contents)); err == nil {
			t.Fatalf("invalid resize accepted: %s", contents)
		}
	}
}
