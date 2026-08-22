package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/store"
)

const apiTestOrigin = "https://aisummoner.test"

type apiOnline struct{}

func (apiOnline) IsOnline(string) bool { return true }

type apiExecutor struct{}

func (apiExecutor) Exec(context.Context, string, string, string) (agent.RemoteExecution, error) {
	return agent.RemoteExecution{Stdout: []byte("lzr-host\n"), ExitCode: 0}, nil
}

type providerConfiguratorProbe struct {
	mu    sync.Mutex
	calls int
	key   string
	model string
	err   error
}

func (probe *providerConfiguratorProbe) ConfigureDeepSeek(_ context.Context, key, model string) error {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.calls++
	probe.key = key
	probe.model = model
	return probe.err
}

func (probe *providerConfiguratorProbe) snapshot() (int, string, string) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.calls, probe.key, probe.model
}

type apiFixture struct {
	t       *testing.T
	store   *store.Store
	service *agent.Service
	handler http.Handler
	cookie  *http.Cookie
	ownerID string
	device  string
	now     time.Time
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	passwordHash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := database.BootstrapAdmin(ctx, "usr_agent_api", "admin", passwordHash, now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_agent_api"
	_, err = database.RegisterDevice(ctx, store.Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x51}, 32), OwnerUserID: &ownerID,
		Name: "agent api host", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database)
	login, err := authService.Login(ctx, "admin", "test-password", now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := agent.NewService(agent.ServiceOptions{
		Store: database, Adapter: &agent.FakeAdapter{}, Executor: apiExecutor{}, Online: apiOnline{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), TurnTimeout: time.Second, ApprovalTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	api, err := New(Options{
		Auth: authService, Agent: service, AllowedOrigin: apiTestOrigin,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }, Keepalive: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &apiFixture{
		t: t, store: database, service: service, handler: api.Handler(),
		cookie: &http.Cookie{Name: sessionCookieName, Value: login.Token}, ownerID: ownerID, device: deviceID, now: now,
	}
}

func (fixture *apiFixture) request(method, path, body string, cookie *http.Cookie, origin bool) *httptest.ResponseRecorder {
	fixture.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if origin {
		request.Header.Set("Origin", apiTestOrigin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

func TestAPIAuthenticationOriginMalformedOversizedAndOwnerHiding(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", `{}`, nil, true)
	assertError(t, response, http.StatusUnauthorized, "UNAUTHENTICATED")
	response = fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", `{}`, fixture.cookie, false)
	assertError(t, response, http.StatusForbidden, "ORIGIN_FORBIDDEN")
	response = fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", `{"approval_mode":"per_command","extra":true}`, fixture.cookie, true)
	assertError(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", strings.Repeat("x", maxJSONBodyBytes+1), fixture.cookie, true)
	assertError(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = fixture.request(http.MethodPost, "/api/v1/devices/dev_not_owned/agent-sessions", `{}`, fixture.cookie, true)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")
	response = fixture.request(http.MethodGet, "/api/v1/agent-sessions/ags_not_owned", "", fixture.cookie, false)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")
}

func TestDeepSeekConfigurationRequiresAuthExactOriginAndBoundedJSON(t *testing.T) {
	fixture := newAPIFixture(t)
	probe := &providerConfiguratorProbe{}
	api, err := New(Options{
		Auth: auth.NewService(fixture.store), Agent: fixture.service, ProviderConfigurator: probe,
		AllowedOrigin: apiTestOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := api.Handler()
	request := func(cookie *http.Cookie, origins []string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-provider/deepseek", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, origin := range origins {
			req.Header.Add("Origin", origin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	assertError(t, request(nil, []string{apiTestOrigin}, `{}`), http.StatusUnauthorized, "UNAUTHENTICATED")
	assertError(t, request(fixture.cookie, nil, `{}`), http.StatusForbidden, "ORIGIN_FORBIDDEN")
	assertError(t, request(fixture.cookie, []string{apiTestOrigin, apiTestOrigin}, `{}`), http.StatusForbidden, "ORIGIN_FORBIDDEN")
	assertError(t, request(fixture.cookie, []string{apiTestOrigin}, `{"api_key":"key","model":"model","extra":true}`), http.StatusBadRequest, "INVALID_REQUEST")
	assertError(t, request(fixture.cookie, []string{apiTestOrigin}, strings.Repeat("x", maxDeepSeekConfigJSONBodyBytes+1)), http.StatusBadRequest, "INVALID_REQUEST")
	if calls, _, _ := probe.snapshot(); calls != 0 {
		t.Fatalf("invalid requests reached provider configurator %d times", calls)
	}

	response := fixture.request(http.MethodPost, "/api/v1/agent-provider/deepseek", `{}`, fixture.cookie, true)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")
}

func TestDeepSeekConfigurationDoesNotEchoOrLogCredentials(t *testing.T) {
	fixture := newAPIFixture(t)
	secret := "sk-browser-provider-secret-sentinel"
	model := "deepseek-v4-flash"
	probe := &providerConfiguratorProbe{}
	var logs bytes.Buffer
	api, err := New(Options{
		Auth: auth.NewService(fixture.store), Agent: fixture.service, ProviderConfigurator: probe,
		AllowedOrigin: apiTestOrigin, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"api_key": secret, "model": model})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent-provider/deepseek", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", apiTestOrigin)
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("configuration response=%d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if calls, key, configuredModel := probe.snapshot(); calls != 1 || key != secret || configuredModel != model {
		t.Fatalf("configurator calls=%d key-match=%v model=%q", calls, key == secret, configuredModel)
	}
	if strings.Contains(response.Body.String(), secret) || strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), model) {
		t.Fatalf("provider credential/model leaked through response or logs")
	}

	probe.err = errors.New("provider rejected " + secret)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agent-provider/deepseek", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", apiTestOrigin)
	request.AddCookie(fixture.cookie)
	response = httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	assertError(t, response, http.StatusInternalServerError, "INTERNAL")
	if strings.Contains(response.Body.String(), secret) || strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), model) {
		t.Fatal("provider configuration error leaked credential/model")
	}
}

func TestAPILatestDeviceSessionRestoresNewestTranscript(t *testing.T) {
	fixture := newAPIFixture(t)
	first, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalPerCommand)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("session IDs unexpectedly match")
	}
	if err := fixture.store.CreateAgentMessage(context.Background(), fixture.ownerID, store.AgentMessage{
		ID: "msg_resume_reasoning", SessionID: second.ID, Role: "reasoning", Content: "inspect", CreatedAt: fixture.now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CreateAgentMessage(context.Background(), fixture.ownerID, store.AgentMessage{
		ID: "msg_resume_answer", SessionID: second.ID, Role: "assistant", Content: "ready", CreatedAt: fixture.now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	response := fixture.request(http.MethodGet, "/api/v1/devices/"+fixture.device+"/agent-sessions", "", fixture.cookie, false)
	if response.Code != http.StatusOK {
		t.Fatalf("latest status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot struct {
		Session  sessionJSON   `json:"session"`
		Messages []messageJSON `json:"messages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.ID != second.ID || snapshot.Session.ApprovalMode != store.AgentApprovalFullAccess ||
		len(snapshot.Messages) != 2 || snapshot.Messages[0].Role != "reasoning" || snapshot.Messages[1].Content != "ready" {
		t.Fatalf("unexpected latest response: %#v", snapshot)
	}
	response = fixture.request(http.MethodGet, "/api/v1/devices/dev_unknown/agent-sessions", "", fixture.cookie, false)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")
}

func TestDuplicateOriginHeadersFailBeforeAuthentication(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, origins := range [][]string{
		{apiTestOrigin, "https://evil.example"},
		{apiTestOrigin, apiTestOrigin},
	} {
		request := httptest.NewRequest(http.MethodPost,
			"/api/v1/devices/"+fixture.device+"/agent-sessions", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		for _, origin := range origins {
			request.Header.Add("Origin", origin)
		}
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origins=%q status=%d body=%s", origins, response.Code, response.Body.String())
		}
		assertError(t, response, http.StatusForbidden, "ORIGIN_FORBIDDEN")
	}
}

func TestAPIExactMaximumMessageAndOversizeContent(t *testing.T) {
	fixture := newAPIFixture(t)
	session, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	exactBody, _ := json.Marshal(map[string]string{"content": strings.Repeat("x", agent.MaxMessageBytes)})
	response := fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/messages", string(exactBody), fixture.cookie, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("exact-max message status=%d body=%s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
		if snapshot.Session.State == store.AgentSessionIdle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	oversizeBody, _ := json.Marshal(map[string]string{"content": strings.Repeat("x", agent.MaxMessageBytes+1)})
	response = fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+session.ID+"/messages", string(oversizeBody), fixture.cookie, true)
	assertError(t, response, http.StatusBadRequest, "INVALID_REQUEST")

	escapedSession, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.Repeat("\x01", agent.MaxMessageBytes)
	escapedBody, _ := json.Marshal(map[string]string{"content": escaped})
	response = fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+escapedSession.ID+"/messages", string(escapedBody), fixture.cookie, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("escaped exact-max message status=%d raw=%d body=%s", response.Code, len(escapedBody), response.Body.String())
	}
	escapedOversizeBody, _ := json.Marshal(map[string]string{"content": escaped + "\x01"})
	response = fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+escapedSession.ID+"/messages", string(escapedOversizeBody), fixture.cookie, true)
	assertError(t, response, http.StatusBadRequest, "INVALID_REQUEST")
}

func TestAPIOfflineSessionCreationReturnsStableError(t *testing.T) {
	fixture := newAPIFixture(t)
	offline := &apiMutableOnline{}
	service, err := agent.NewService(agent.ServiceOptions{
		Store: fixture.store, Adapter: &agent.FakeAdapter{}, Executor: apiExecutor{}, Online: offline,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authService := auth.NewService(fixture.store)
	api, _ := New(Options{
		Auth: authService, Agent: service, AllowedOrigin: apiTestOrigin,
		Now: func() time.Time { return fixture.now },
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", apiTestOrigin)
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	assertError(t, response, http.StatusConflict, "DEVICE_OFFLINE")
}

type apiMutableOnline struct{}

func (*apiMutableOnline) IsOnline(string) bool { return false }

func TestAPISessionMessageConflictAndDecisionOwner(t *testing.T) {
	fixture := newAPIFixture(t)
	response := fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.device+"/agent-sessions", `{"approval_mode":"per_command"}`, fixture.cookie, true)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Session sessionJSON `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	response = fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+created.Session.ID+"/messages", `{"content":"inspect"}`, fixture.cookie, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("message status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+created.Session.ID+"/messages", `{"content":"second"}`, fixture.cookie, true)
	assertError(t, response, http.StatusConflict, "TURN_IN_PROGRESS")

	deadline := time.Now().Add(time.Second)
	var pending store.ToolCall
	for pending.ID == "" && time.Now().Before(deadline) {
		snapshot, _ := fixture.service.Snapshot(context.Background(), fixture.ownerID, created.Session.ID)
		for _, toolCall := range snapshot.ToolCalls {
			if toolCall.Status == store.ToolCallPending {
				pending = toolCall
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("pending tool call not observed")
	}
	response = fixture.request(http.MethodPost, "/api/v1/tool-calls/"+pending.ID+"/decision", `{"decision":"invalid"}`, fixture.cookie, true)
	assertError(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = fixture.request(http.MethodPost, "/api/v1/tool-calls/tool_not_owned/decision", `{"decision":"deny"}`, fixture.cookie, true)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")
	response = fixture.request(http.MethodPost, "/api/v1/tool-calls/"+pending.ID+"/decision", `{"decision":"deny"}`, fixture.cookie, true)
	if response.Code != http.StatusOK {
		t.Fatalf("decision status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.request(http.MethodPost, "/api/v1/tool-calls/"+pending.ID+"/decision", `{"decision":"approve_once"}`, fixture.cookie, true)
	assertError(t, response, http.StatusConflict, "INVALID_STATE")
}

func TestSSEAuthenticatesBeforeHeadersAndFramesCanonicalEvents(t *testing.T) {
	fixture := newAPIFixture(t)
	session, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := fixture.request(http.MethodGet, "/api/v1/agent-sessions/"+session.ID+"/events", "", nil, false)
	assertError(t, unauthenticated, http.StatusUnauthorized, "UNAUTHENTICATED")
	if strings.Contains(unauthenticated.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatal("SSE headers committed before authentication")
	}

	server := httptest.NewServer(fixture.handler)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/agent-sessions/"+session.ID+"/events", nil)
	request.AddCookie(fixture.cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response status=%d headers=%v", response.StatusCode, response.Header)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "inspect"); err != nil {
		t.Fatal(err)
	}
	contents := make(chan string, 1)
	go func() {
		value, _ := io.ReadAll(io.LimitReader(response.Body, 128*1024))
		contents <- string(value)
	}()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case value := <-contents:
			if !strings.Contains(value, "event: session.state\n") || !strings.Contains(value, "event: tool_call.started\n") ||
				!strings.Contains(value, "event: tool_call.output\n") || !strings.Contains(value, "event: turn.completed\n") ||
				!strings.Contains(value, `"event_id":"evt_`) || strings.Contains(value, "\ndata: {\n") {
				t.Fatalf("unexpected SSE frames: %s", value)
			}
			return
		case <-deadline:
			response.Body.Close()
			value := <-contents
			if !strings.Contains(value, "event: turn.completed\n") {
				t.Fatalf("SSE did not flush canonical sequence: %s", value)
			}
			return
		}
	}
}

func TestSSECanonicalOrder(t *testing.T) {
	service := &staticAgentService{events: make(chan agent.Event, 16)}
	fixture := newAPIFixture(t)
	session, _ := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	api, err := New(Options{
		Auth: auth.NewService(fixture.store), Agent: service, AllowedOrigin: apiTestOrigin,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Keepalive: time.Second,
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/agent-sessions/"+session.ID+"/events", nil)
	request.AddCookie(fixture.cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	eventTypes := []string{
		agent.EventSessionState, agent.EventToolStarted, agent.EventToolOutput,
		agent.EventToolCompleted, agent.EventTextDelta, agent.EventTextDone, agent.EventTurnCompleted,
	}
	for index, eventType := range eventTypes {
		service.events <- agent.Event{
			ID: "evt_order" + string(rune('a'+index)), SessionID: session.ID,
			CreatedAt: time.Now().UTC(), Type: eventType, Payload: json.RawMessage(`{"ok":true}`),
		}
	}
	close(service.events)
	contents, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	lastIndex := -1
	for _, eventType := range eventTypes {
		index := strings.Index(text, "event: "+eventType+"\n")
		if index <= lastIndex {
			t.Fatalf("SSE event order incorrect for %s: %s", eventType, text)
		}
		lastIndex = index
	}
}

func TestSSEWrongOwnerAndCanceledSubscriberCleanup(t *testing.T) {
	fixture := newAPIFixture(t)
	session, _ := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.device, store.AgentApprovalFullAccess)
	wrongAuth := &staticAgentService{subscribeErr: agent.ErrNotFound}
	api, _ := New(Options{
		Auth: auth.NewService(fixture.store), Agent: wrongAuth, AllowedOrigin: apiTestOrigin,
		Now: func() time.Time { return fixture.now },
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent-sessions/"+session.ID+"/events", nil)
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	assertError(t, response, http.StatusNotFound, "NOT_FOUND")

	server := httptest.NewServer(fixture.handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	streamRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		server.URL+"/api/v1/agent-sessions/"+session.ID+"/events", nil)
	streamRequest.AddCookie(fixture.cookie)
	streamResponse, err := server.Client().Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for fixture.service.SubscriberCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fixture.service.SubscriberCount() != 1 {
		t.Fatalf("subscriber was not registered: %d", fixture.service.SubscriberCount())
	}
	cancel()
	streamResponse.Body.Close()
	deadline = time.Now().Add(time.Second)
	for fixture.service.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fixture.service.SubscriberCount() != 0 {
		t.Fatalf("canceled SSE retained subscriber: %d", fixture.service.SubscriberCount())
	}
}

type blockingDeadlineWriter struct {
	header      http.Header
	deadlineSet chan struct{}
	wrote       chan struct{}
	once        sync.Once
	status      int
}

func newBlockingDeadlineWriter() *blockingDeadlineWriter {
	return &blockingDeadlineWriter{header: make(http.Header), deadlineSet: make(chan struct{}), wrote: make(chan struct{})}
}

func (writer *blockingDeadlineWriter) Header() http.Header { return writer.header }

func (writer *blockingDeadlineWriter) WriteHeader(status int) { writer.status = status }

func (writer *blockingDeadlineWriter) Write(contents []byte) (int, error) {
	writer.once.Do(func() { close(writer.wrote) })
	<-writer.deadlineSet
	return 0, context.DeadlineExceeded
}

func (writer *blockingDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	time.AfterFunc(delay, func() {
		select {
		case <-writer.deadlineSet:
		default:
			close(writer.deadlineSet)
		}
	})
	return nil
}

func (writer *blockingDeadlineWriter) FlushError() error { return nil }

func TestSSEBlockedInitialWriteIsBoundedAndUnsubscribes(t *testing.T) {
	fixture := newAPIFixture(t)
	service := &staticAgentService{events: make(chan agent.Event), unsubscribed: make(chan struct{})}
	api, err := New(Options{
		Auth: auth.NewService(fixture.store), Agent: service, AllowedOrigin: apiTestOrigin,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), WriteTimeout: 20 * time.Millisecond,
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent-sessions/ags_slow_writer/events", nil).WithContext(ctx)
	request.AddCookie(fixture.cookie)
	writer := newBlockingDeadlineWriter()
	done := make(chan struct{})
	go func() {
		api.Handler().ServeHTTP(writer, request)
		close(done)
	}()
	select {
	case <-writer.wrote:
	case <-time.After(time.Second):
		t.Fatal("SSE handler never reached the blocking writer")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("SSE handler outlived its write deadline")
	}
	select {
	case <-service.unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("bounded SSE write did not unsubscribe")
	}
}

type staticAgentService struct {
	mu           sync.Mutex
	subscribeErr error
	events       chan agent.Event
	unsubscribed chan struct{}
}

type panicAgentService struct{ staticAgentService }

func (*panicAgentService) Snapshot(context.Context, string, string) (store.AgentSnapshot, error) {
	panic("dependency panic sentinel")
}

func TestAPIPanicPreservesRequestIDAndLogsStack(t *testing.T) {
	fixture := newAPIFixture(t)
	var logs bytes.Buffer
	api, err := New(Options{
		Auth: auth.NewService(fixture.store), Agent: &panicAgentService{}, AllowedOrigin: apiTestOrigin,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Now:    func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent-sessions/ags_panics", nil)
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	assertError(t, response, http.StatusInternalServerError, "INTERNAL")
	if !strings.Contains(logs.String(), "stack=") || !strings.Contains(logs.String(), "runtime/debug.Stack") {
		t.Fatalf("panic stack not logged: %s", logs.String())
	}
}

func (*staticAgentService) CreateSession(context.Context, string, string, string) (store.AgentSession, error) {
	return store.AgentSession{}, errors.New("unused")
}
func (*staticAgentService) Snapshot(context.Context, string, string) (store.AgentSnapshot, error) {
	return store.AgentSnapshot{}, errors.New("unused")
}
func (*staticAgentService) LatestSnapshot(context.Context, string, string) (store.AgentSnapshot, error) {
	return store.AgentSnapshot{}, errors.New("unused")
}
func (*staticAgentService) StartTurn(context.Context, string, string, string) (store.AgentMessage, error) {
	return store.AgentMessage{}, errors.New("unused")
}
func (*staticAgentService) Decide(context.Context, string, string, string) (store.ToolCall, error) {
	return store.ToolCall{}, errors.New("unused")
}
func (service *staticAgentService) Subscribe(context.Context, string, string) (<-chan agent.Event, func(), error) {
	if service.events == nil && service.subscribeErr == nil {
		service.events = make(chan agent.Event)
	}
	return service.events, func() {
		if service.unsubscribed != nil {
			select {
			case <-service.unsubscribed:
			default:
				close(service.unsubscribed)
			}
		}
	}, service.subscribeErr
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, response.Body.String())
	}
	if envelope.Error.Code != code || !strings.HasPrefix(envelope.Error.RequestID, "req_") ||
		response.Header().Get("X-Request-ID") != envelope.Error.RequestID {
		t.Fatalf("unexpected error envelope: %#v headers=%v", envelope, response.Header())
	}
}
