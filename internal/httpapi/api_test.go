package httpapi

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
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/device"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/requestsource"
	"github.com/aisummoner/aisummoner/internal/store"
)

const testOrigin = "https://aisummoner.test"

type apiFixture struct {
	t       *testing.T
	store   *store.Store
	pairing *pairing.Service
	api     *API
	handler http.Handler
	now     time.Time
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	passwordHash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatalf("auth.HashPassword: %v", err)
	}
	if _, _, err := database.BootstrapAdmin(ctx, "usr_admin", "admin", passwordHash, now); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	authService := auth.NewService(database)
	pairingService, err := pairing.NewService(database, bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatalf("pairing.NewService: %v", err)
	}
	fixture := &apiFixture{t: t, store: database, pairing: pairingService, now: now}
	api, err := New(Options{
		Auth: authService, Pairing: pairingService, Devices: device.NewService(database, device.OfflineState{}),
		Auditor: database, AllowedOrigin: testOrigin,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatalf("New API: %v", err)
	}
	fixture.api = api
	fixture.handler = api.Handler()
	return fixture
}

func (fixture *apiFixture) request(method, path, body string, cookie *http.Cookie, origin bool) *httptest.ResponseRecorder {
	return fixture.requestFrom(method, path, body, cookie, origin, "192.0.2.1:1234", "")
}

func (fixture *apiFixture) requestFrom(method, path, body string, cookie *http.Cookie, origin bool, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
	headers := make(http.Header)
	if forwardedFor != "" {
		headers.Set("X-Forwarded-For", forwardedFor)
	}
	return fixture.requestFromHeaders(method, path, body, cookie, origin, remoteAddr, headers)
}

func (fixture *apiFixture) requestFromHeaders(method, path, body string, cookie *http.Cookie, origin bool, remoteAddr string, headers http.Header) *httptest.ResponseRecorder {
	fixture.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	request.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if origin {
		request.Header.Set("Origin", testOrigin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

func (fixture *apiFixture) loginCookie() *http.Cookie {
	fixture.t.Helper()
	response := fixture.request(http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"test-password"}`, nil, true)
	if response.Code != http.StatusOK {
		fixture.t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				fixture.t.Fatalf("unsafe session cookie: %#v", cookie)
			}
			return cookie
		}
	}
	fixture.t.Fatal("login did not set session cookie")
	return nil
}

func TestOriginRejectionUsesErrorEnvelope(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.request(http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"test-password"}`, nil, false)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "ORIGIN_FORBIDDEN")
}

func TestDuplicateOriginHeadersFailBeforeAuthentication(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, origins := range [][]string{
		{testOrigin, "https://evil.example"},
		{testOrigin, testOrigin},
	} {
		headers := make(http.Header)
		for _, origin := range origins {
			headers.Add("Origin", origin)
		}
		response := fixture.requestFromHeaders(http.MethodPost, "/api/v1/auth/login",
			`{"username":"admin","password":"test-password"}`, nil, false, "192.0.2.1:1234", headers)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origins=%q status=%d body=%s", origins, response.Code, response.Body.String())
		}
		assertErrorEnvelope(t, response, "ORIGIN_FORBIDDEN")
	}
}

func TestLoginVerificationBusyUsesStableEnvelopeWithoutCountingFailure(t *testing.T) {
	fixture := newAPIFixture(t)
	busyAuth := &authServiceFake{loginErr: auth.ErrVerificationBusy}
	fixture.api.auth = busyAuth

	for attempt := 0; attempt < 6; attempt++ {
		response := fixture.requestFrom(http.MethodPost, "/api/v1/auth/login",
			`{"username":"admin","password":"not-recorded"}`, nil, true,
			"198.51.100.10:6000", "203.0.113.250")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("busy attempt %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
		assertErrorEnvelope(t, response, "SERVICE_UNAVAILABLE")
		if strings.Contains(response.Body.String(), "not-recorded") || strings.Contains(response.Body.String(), auth.ErrVerificationBusy.Error()) {
			t.Fatalf("busy response exposed credential or internal error: %s", response.Body.String())
		}
	}
	if got := busyAuth.loginCalls; got != 6 {
		t.Fatalf("busy attempts were rate limited: auth calls=%d want=6", got)
	}
	if got := len(fixture.api.loginLimiter.entries); got != 0 {
		t.Fatalf("busy attempts created failure state: entries=%d", got)
	}
}

func TestLoginFailureLimitDirectAddressExpiryAndSuccess(t *testing.T) {
	fixture := newAPIFixture(t)
	authFake := &authServiceFake{loginErr: auth.ErrInvalidCredentials}
	fixture.api.auth = authFake
	const remoteAddr = "198.51.100.20:6001"
	for attempt := 0; attempt < 5; attempt++ {
		response := fixture.requestFrom(http.MethodPost, "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`, nil, true,
			remoteAddr, fmt.Sprintf("203.0.113.%d", attempt+1))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
		assertErrorEnvelope(t, response, "INVALID_CREDENTIALS")
	}
	response := fixture.requestFrom(http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"test-password"}`, nil, true,
		remoteAddr, "192.0.2.200")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "RATE_LIMITED")
	if got := authFake.loginCalls; got != 5 {
		t.Fatalf("forwarded headers changed direct-address limit: auth calls=%d want=5", got)
	}

	fixture.now = fixture.now.Add(time.Minute)
	authFake.loginErr = nil
	authFake.loginResult = auth.LoginResult{
		Token: "successful-login-token", ExpiresAt: fixture.now.Add(auth.SessionDuration),
		User: store.User{ID: "usr_admin", Username: "admin"},
	}
	response = fixture.requestFrom(http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"test-password"}`, nil, true,
		remoteAddr, "192.0.2.201")
	if response.Code != http.StatusOK {
		t.Fatalf("expired limiter status=%d body=%s", response.Code, response.Body.String())
	}
	if got := len(fixture.api.loginLimiter.entries); got != 0 {
		t.Fatalf("successful login did not remove key: entries=%d", got)
	}

	response = fixture.requestFrom(http.MethodPost, "/api/v1/auth/login", `{`, nil, true, remoteAddr, "192.0.2.202")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d body=%s", response.Code, response.Body.String())
	}
	fixture.api.loginLimiter.mu.Lock()
	entry := fixture.api.loginLimiter.entries["198.51.100.20"]
	fixture.api.loginLimiter.mu.Unlock()
	if entry.count != 1 {
		t.Fatalf("invalid JSON failure count=%d want=1", entry.count)
	}
	response = fixture.requestFrom(http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"test-password"}`, nil, true,
		remoteAddr, "192.0.2.203")
	if response.Code != http.StatusOK {
		t.Fatalf("success recovery status=%d body=%s", response.Code, response.Body.String())
	}
	if got := len(fixture.api.loginLimiter.entries); got != 0 {
		t.Fatalf("success recovery retained limiter key: entries=%d", got)
	}
}

func TestTrustedProxyLoginLimitSeparatesClientSources(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.sourceResolver = requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")})
	authFake := &authServiceFake{loginErr: auth.ErrInvalidCredentials}
	fixture.api.auth = authFake
	const (
		proxyAddress = "10.0.0.2:8443"
		sourceA      = "198.51.100.21"
		sourceB      = "198.51.100.22"
	)

	login := func(source string) *httptest.ResponseRecorder {
		headers := make(http.Header)
		headers.Set(requestsource.HeaderName, source)
		return fixture.requestFromHeaders(http.MethodPost, "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`, nil, true, proxyAddress, headers)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if response := login(sourceA); response.Code != http.StatusUnauthorized {
			t.Fatalf("source A failure %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
	}
	if response := login(sourceA); response.Code != http.StatusTooManyRequests {
		t.Fatalf("source A blocked status=%d body=%s", response.Code, response.Body.String())
	}
	if response := login(sourceB); response.Code != http.StatusUnauthorized {
		t.Fatalf("source B was collapsed into source A: status=%d body=%s", response.Code, response.Body.String())
	}
	if authFake.loginCalls != 6 {
		t.Fatalf("auth calls=%d want=6", authFake.loginCalls)
	}

	fixture.api.loginLimiter.mu.Lock()
	entryA := fixture.api.loginLimiter.entries[sourceA]
	entryB := fixture.api.loginLimiter.entries[sourceB]
	entryCount := len(fixture.api.loginLimiter.entries)
	fixture.api.loginLimiter.mu.Unlock()
	if entryCount != 2 || entryA.count != 5 || entryB.count != 1 {
		t.Fatalf("trusted source windows collapsed: count=%d A=%#v B=%#v", entryCount, entryA, entryB)
	}
}

func TestUntrustedLoginPeerCannotForgeLimiterSource(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.sourceResolver = requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")})
	authFake := &authServiceFake{loginErr: auth.ErrInvalidCredentials}
	fixture.api.auth = authFake
	const directSource = "203.0.113.17"

	for attempt := 0; attempt < 6; attempt++ {
		headers := make(http.Header)
		headers.Set(requestsource.HeaderName, fmt.Sprintf("198.51.100.%d", attempt+1))
		headers.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", attempt+1))
		headers.Set("Forwarded", fmt.Sprintf("for=192.0.2.%d", attempt+1))
		response := fixture.requestFromHeaders(http.MethodPost, "/api/v1/auth/login",
			`{"username":"admin","password":"wrong-password"}`, nil, true,
			directSource+":9443", headers)
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status=%d want=%d body=%s", attempt+1, response.Code, want, response.Body.String())
		}
	}
	if authFake.loginCalls != 5 {
		t.Fatalf("forged headers changed admission key: auth calls=%d want=5", authFake.loginCalls)
	}
	fixture.api.loginLimiter.mu.Lock()
	entry := fixture.api.loginLimiter.entries[directSource]
	entryCount := len(fixture.api.loginLimiter.entries)
	fixture.api.loginLimiter.mu.Unlock()
	if entryCount != 1 || entry.count != 5 {
		t.Fatalf("untrusted source state=%d direct=%#v", entryCount, entry)
	}
}

func TestMalformedTrustedSourceFailsBeforeBrowserLimiters(t *testing.T) {
	fixture := newAPIFixture(t)
	cookie := fixture.loginCookie()
	fixture.api.sourceResolver = requestsource.New([]netip.Addr{netip.MustParseAddr("10.0.0.2")})
	authFake := &authServiceFake{loginErr: auth.ErrInvalidCredentials}
	fixture.api.auth = authFake
	var logs bytes.Buffer
	fixture.api.logger = slog.New(slog.NewTextHandler(&logs, nil))
	const trustedPeer = "10.0.0.2:8080"

	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "missing", headers: make(http.Header)},
		{name: "repeated", headers: http.Header{requestsource.HeaderName: {"198.51.100.31", "198.51.100.32"}}},
		{name: "comma separated", headers: http.Header{requestsource.HeaderName: {"198.51.100.31,198.51.100.32"}}},
	}
	for _, testCase := range cases {
		t.Run("login "+testCase.name, func(t *testing.T) {
			response := fixture.requestFromHeaders(http.MethodPost, "/api/v1/auth/login",
				`{"username":"admin","password":"must-not-be-checked"}`, nil, true,
				trustedPeer, testCase.headers)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorEnvelope(t, response, "INVALID_REQUEST")
			if strings.Contains(response.Body.String(), requestsource.HeaderName) ||
				strings.Contains(response.Body.String(), "198.51.100.31") ||
				strings.Contains(response.Body.String(), requestsource.ErrInvalidSource.Error()) {
				t.Fatalf("source error exposed request details: %s", response.Body.String())
			}
		})
	}
	if authFake.loginCalls != 0 {
		t.Fatalf("malformed trusted source reached login: calls=%d", authFake.loginCalls)
	}
	if got := len(fixture.api.loginLimiter.entries); got != 0 {
		t.Fatalf("malformed trusted source mutated login limiter: entries=%d", got)
	}

	// Pairing authenticates the browser first, then must still resolve the
	// source before parsing a claim or mutating claim limiter state.
	fixture.api.auth = auth.NewService(fixture.store)
	response := fixture.requestFromHeaders(http.MethodPost, "/api/v1/pairings/claim",
		`{"code":"must-not-be-consumed"}`, cookie, true, trustedPeer, make(http.Header))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("pairing missing source status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "INVALID_REQUEST")
	if got := len(fixture.api.claimLimiter.entries); got != 0 {
		t.Fatalf("malformed trusted source mutated claim limiter: entries=%d", got)
	}
	for _, forbidden := range []string{
		requestsource.HeaderName, "198.51.100.31", "must-not-be-checked", requestsource.ErrInvalidSource.Error(),
	} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("source failure log exposed %q: %s", forbidden, logs.String())
		}
	}
}

func TestFailureLimiterBoundsCapacityExpiryAndLRU(t *testing.T) {
	start := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	limiter := newFailureLimiter(5, time.Minute)
	for index := 0; index < failureLimiterCapacity; index++ {
		limiter.failed(fmt.Sprintf("source-%04d", index), start)
	}
	if got := len(limiter.entries); got != failureLimiterCapacity {
		t.Fatalf("initial limiter size=%d want=%d", got, failureLimiterCapacity)
	}
	if !limiter.allow("source-0000", start.Add(time.Second)) {
		t.Fatal("observed LRU source was unexpectedly blocked")
	}
	limiter.failed("replacement", start.Add(2*time.Second))
	if got := len(limiter.entries); got != failureLimiterCapacity {
		t.Fatalf("replacement limiter size=%d want=%d", got, failureLimiterCapacity)
	}
	if _, ok := limiter.entries["source-0000"]; !ok {
		t.Fatal("most recently observed source was evicted")
	}
	if _, ok := limiter.entries["source-0001"]; ok {
		t.Fatal("least recently observed source was not evicted")
	}
	if _, ok := limiter.entries["replacement"]; !ok {
		t.Fatal("replacement source was not inserted")
	}

	limiter.failed("after-expiry", start.Add(time.Minute+2*time.Second))
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("expired entries were not reclaimed: size=%d", got)
	}
	if _, ok := limiter.entries["after-expiry"]; !ok {
		t.Fatal("new source missing after expiry reclamation")
	}

	api := &API{
		loginLimiter: newFailureLimiter(5, time.Minute),
		claimLimiter: newFailureLimiter(5, time.Minute),
	}
	for index := 0; index < failureLimiterCapacity+257; index++ {
		key := fmt.Sprintf("rotating-%05d", index)
		api.loginLimiter.failed(key, start)
		api.claimLimiter.failed(key, start)
	}
	if got := len(api.loginLimiter.entries); got > failureLimiterCapacity {
		t.Fatalf("login limiter exceeded capacity: %d", got)
	}
	if got := len(api.claimLimiter.entries); got > failureLimiterCapacity {
		t.Fatalf("pairing limiter exceeded capacity: %d", got)
	}
}

func TestFailureLimiterConcurrentOperationsStayBounded(t *testing.T) {
	limiter := newFailureLimiter(5, time.Minute)
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	const workers = 16
	const operationsPerWorker = 384
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer workersDone.Done()
			<-start
			sharedKey := fmt.Sprintf("shared-%02d", worker%4)
			for operation := 0; operation < operationsPerWorker; operation++ {
				uniqueKey := fmt.Sprintf("unique-%02d-%03d", worker, operation)
				_ = limiter.allow(sharedKey, now)
				limiter.failed(uniqueKey, now)
				limiter.failed(sharedKey, now)
				if operation%4 == 0 {
					limiter.succeeded(uniqueKey)
				}
				if operation%17 == 0 {
					limiter.succeeded(sharedKey)
				}
			}
		}()
	}
	close(start)
	workersDone.Wait()

	limiter.mu.Lock()
	size := len(limiter.entries)
	var invalidKey string
	var invalidEntry failureWindow
	for key, entry := range limiter.entries {
		if entry.count <= 0 || entry.started.IsZero() || entry.lastSeen.IsZero() || entry.lastObserved == 0 {
			invalidKey = key
			invalidEntry = entry
			break
		}
	}
	limiter.mu.Unlock()
	if invalidKey != "" {
		t.Fatalf("invalid retained limiter entry %q: %#v", invalidKey, invalidEntry)
	}
	if size == 0 || size > failureLimiterCapacity {
		t.Fatalf("concurrent limiter size=%d want 1..%d", size, failureLimiterCapacity)
	}
}

func TestUnauthenticatedRequestUsesErrorEnvelope(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.request(http.MethodGet, "/api/v1/devices", "", nil, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "UNAUTHENTICATED")
}

func TestPairingIsOneTimeAndOwnerChecked(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := context.Background()
	_, err := fixture.store.RegisterDevice(ctx, store.Device{
		ID: "dev_api", PublicKey: bytes.Repeat([]byte{0x71}, 32), Name: "API host",
		Platform: "linux", Arch: "amd64", ClientVersion: "0.1.0", CreatedAt: fixture.now,
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	response := fixture.request(http.MethodGet, "/api/v1/devices/dev_api", "", fixture.loginCookie(), false)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unowned device leaked: status=%d body=%s", response.Code, response.Body.String())
	}
	offer, err := fixture.pairing.Offer(ctx, "dev_api", fixture.now)
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"code": offer.Code})
	cookie := fixture.loginCookie()
	response = fixture.request(http.MethodPost, "/api/v1/pairings/claim", string(body), cookie, true)
	if response.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.request(http.MethodPost, "/api/v1/pairings/claim", string(body), cookie, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("second claim status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "PAIRING_CODE_INVALID")
	response = fixture.request(http.MethodGet, "/api/v1/devices/dev_api", "", cookie, false)
	if response.Code != http.StatusOK {
		t.Fatalf("owner detail status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	fixture := newAPIFixture(t)
	cookie := fixture.loginCookie()
	response := fixture.request(http.MethodPost, "/api/v1/auth/logout", `{}`, cookie, true)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.request(http.MethodGet, "/api/v1/me", "", cookie, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("logged out session status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFullMiddlewarePreservesSSEFlusher(t *testing.T) {
	fixture := newAPIFixture(t)
	downstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Error("full middleware removed http.Flusher")
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: ready\n\n"))
		flusher.Flush()
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	fixture.api.withMiddleware(downstream).ServeHTTP(response, request)
	if !response.Flushed {
		t.Fatal("SSE flush did not reach the underlying response writer")
	}
}

func TestFullMiddlewarePreservesConnectionUpgrade(t *testing.T) {
	fixture := newAPIFixture(t)
	wantError := errors.New("underlying hijack result")
	underlying := &hijackResponseWriter{header: make(http.Header), err: wantError}
	var gotError error
	downstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("full middleware removed http.Hijacker")
			return
		}
		_, _, gotError = hijacker.Hijack()
	})
	request := httptest.NewRequest(http.MethodGet, "/websocket", nil)
	fixture.api.withMiddleware(downstream).ServeHTTP(underlying, request)
	if !underlying.hijacked {
		t.Fatal("connection upgrade did not reach the underlying response writer")
	}
	if !errors.Is(gotError, wantError) {
		t.Fatalf("hijack error was not preserved: got %v want %v", gotError, wantError)
	}
}

func TestFullMiddlewareRecoversPanicWithErrorEnvelope(t *testing.T) {
	fixture := newAPIFixture(t)
	downstream := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("regression panic")
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	fixture.api.withMiddleware(downstream).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertErrorEnvelope(t, response, "INTERNAL")
}

type hijackResponseWriter struct {
	header   http.Header
	hijacked bool
	err      error
}

func (writer *hijackResponseWriter) Header() http.Header { return writer.header }
func (writer *hijackResponseWriter) Write(contents []byte) (int, error) {
	return len(contents), nil
}
func (writer *hijackResponseWriter) WriteHeader(int) {}
func (writer *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	writer.hijacked = true
	return nil, nil, writer.err
}

type authServiceFake struct {
	loginResult auth.LoginResult
	loginErr    error
	loginCalls  int
}

func (fake *authServiceFake) Login(context.Context, string, string, time.Time) (auth.LoginResult, error) {
	fake.loginCalls++
	return fake.loginResult, fake.loginErr
}

func (fake *authServiceFake) Authenticate(context.Context, string, time.Time) (store.User, error) {
	return store.User{}, auth.ErrInvalidCredentials
}

func (fake *authServiceFake) Logout(context.Context, string) error {
	return nil
}

func assertErrorEnvelope(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, response.Body.String())
	}
	if envelope.Error.Code != code || !strings.HasPrefix(envelope.Error.RequestID, "req_") {
		t.Fatalf("unexpected error envelope: %#v", envelope)
	}
	if response.Header().Get("X-Request-ID") != envelope.Error.RequestID {
		t.Fatalf("request id header mismatch: %q != %q", response.Header().Get("X-Request-ID"), envelope.Error.RequestID)
	}
}
