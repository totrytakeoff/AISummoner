package opencode

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
)

type noopInvoker struct{}

func (noopInvoker) Invoke(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
	return agent.ToolResult{}, nil
}

type adapterFixture struct {
	t         *testing.T
	username  string
	password  string
	server    *httptest.Server
	bridge    *opencodebridge.Bridge
	adapter   *Adapter
	workspace string

	mu           sync.Mutex
	requests     []string
	prompt       promptRequest
	promptHadID  bool
	event        func(http.ResponseWriter, *http.Request)
	promptSeen   chan struct{}
	promptOnce   sync.Once
	eventSeen    chan struct{}
	eventOnce    sync.Once
	promptStatus int
	promptBody   string
	afterPrompt  func(promptRequest)
	aborts       int
}

func newAdapterFixture(t *testing.T) *adapterFixture {
	t.Helper()
	fixture := &adapterFixture{t: t, username: "opencode-user", password: "opencode-secret", promptStatus: http.StatusNoContent, promptSeen: make(chan struct{}), eventSeen: make(chan struct{})}
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	bridge, err := opencodebridge.New(opencodebridge.Options{Secret: []byte(strings.Repeat("s", 32)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge = bridge
	adapter, err := NewAdapter(Options{
		BaseURL: fixture.server.URL, Username: fixture.username, Password: fixture.password,
		ModelID: "opencode/free-model", WorkspaceRoot: filepath.Join(t.TempDir(), "workspace root + # ?"),
		Bridge: bridge, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.adapter = adapter
	t.Cleanup(func() {
		fixture.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.Close(ctx); err != nil {
			t.Errorf("close bridge: %v", err)
		}
	})
	return fixture
}

func (fixture *adapterFixture) serveHTTP(response http.ResponseWriter, request *http.Request) {
	username, password, ok := request.BasicAuth()
	if !ok || username != fixture.username || password != fixture.password {
		fixture.t.Errorf("missing Basic Auth on %s", request.URL.Path)
		response.WriteHeader(http.StatusUnauthorized)
		return
	}
	fixture.mu.Lock()
	fixture.requests = append(fixture.requests, request.Method+" "+request.URL.RequestURI())
	fixture.mu.Unlock()
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/global/health":
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"healthy":true,"version":"1.18.11"}`)
	case request.Method == http.MethodPost && request.URL.Path == "/session":
		fixture.captureDirectory(request)
		var create createSessionRequest
		if err := json.NewDecoder(request.Body).Decode(&create); err != nil {
			fixture.t.Error(err)
		}
		if create.Model.ID != "free-model" || create.Model.ProviderID != ProviderName || create.Model.Variant != "default" {
			fixture.t.Errorf("create model=%#v", create.Model)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, `{"id":"ses_fake_http"}`)
	case request.Method == http.MethodGet && request.URL.Path == "/event":
		fixture.captureDirectory(request)
		fixture.eventOnce.Do(func() { close(fixture.eventSeen) })
		fixture.event(response, request)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/prompt_async"):
		fixture.captureDirectory(request)
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			fixture.t.Error(err)
		}
		var prompt promptRequest
		if err := json.Unmarshal(encoded, &prompt); err != nil {
			fixture.t.Error(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &wire); err != nil {
			fixture.t.Error(err)
		}
		fixture.mu.Lock()
		fixture.prompt = prompt
		_, fixture.promptHadID = wire["messageID"]
		status, body, afterPrompt := fixture.promptStatus, fixture.promptBody, fixture.afterPrompt
		fixture.mu.Unlock()
		fixture.promptOnce.Do(func() { close(fixture.promptSeen) })
		if afterPrompt != nil {
			afterPrompt(prompt)
		}
		response.WriteHeader(status)
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = io.WriteString(response, body)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/abort"):
		fixture.captureDirectory(request)
		fixture.mu.Lock()
		fixture.aborts++
		fixture.mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, "true")
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *adapterFixture) captureDirectory(request *http.Request) {
	directory := request.URL.Query().Get("directory")
	if directory == "" || !filepath.IsAbs(directory) {
		fixture.t.Errorf("invalid directory query %q", directory)
	}
	fixture.mu.Lock()
	if fixture.workspace == "" {
		fixture.workspace = directory
	} else if fixture.workspace != directory {
		fixture.t.Errorf("workspace changed: %q != %q", fixture.workspace, directory)
	}
	fixture.mu.Unlock()
}

func (fixture *adapterFixture) snapshot() (promptRequest, int) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.prompt, fixture.aborts
}

func TestAdapterFullHTTPAndSSETurnContract(t *testing.T) {
	fixture := newAdapterFixture(t)
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-fixture.promptSeen
		fixture.mu.Lock()
		promptHadID := fixture.promptHadID
		fixture.mu.Unlock()
		promptID := "msg_generated_user"
		assistantID := "msg_assistant"
		_, _ = io.WriteString(response,
			sseEvent(t, "user", "message.updated", map[string]any{"info": map[string]any{"id": promptID, "sessionID": "ses_fake_http", "role": "user"}})+
				sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": assistantID, "sessionID": "ses_fake_http", "role": "assistant", "parentID": promptID, "finish": "stop"}})+
				sseEvent(t, "delta", "session.next.text.delta", map[string]any{"sessionID": "ses_fake_http", "assistantMessageID": assistantID, "textID": "part_result", "delta": "remote result"})+
				sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": "ses_fake_http"}))
		response.(http.Flusher).Flush()
		if promptHadID {
			fixture.t.Error("production prompt supplied a caller-generated messageID")
		}
	}
	sink := &recordingSink{}
	err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_http", UserText: "inspect lzr", RemoteExec: noopInvoker{}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	prompt, aborts := fixture.snapshot()
	if sink.external != "ses_fake_http" || sink.text() != "remote result" || aborts != 0 {
		t.Fatalf("external=%q text=%q aborts=%d", sink.external, sink.text(), aborts)
	}
	if prompt.MessageID != "" || prompt.Model.ProviderID != ProviderName || prompt.Model.ModelID != "free-model" || prompt.Agent != "build" || len(prompt.Parts) != 1 || prompt.Parts[0].Text != "inspect lzr" {
		t.Fatalf("prompt=%#v", prompt)
	}
	if len(prompt.Tools) != len(builtInTools)+1 || !prompt.Tools[agent.ToolRemoteExec] {
		t.Fatalf("tool map=%v", prompt.Tools)
	}
	for _, builtIn := range builtInTools {
		if prompt.Tools[builtIn] {
			t.Fatalf("built-in %q enabled", builtIn)
		}
	}
	if health := fixture.adapter.Health(context.Background()); health.Status != HealthAvailable || health.Version != "1.18.11" {
		t.Fatalf("health=%#v", health)
	}
}

func TestAdapter204AloneDoesNotCompleteAndCancelAborts(t *testing.T) {
	fixture := newAdapterFixture(t)
	promptBodyClosed := make(chan struct{})
	baseTransport := http.DefaultTransport
	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, err := baseTransport.RoundTrip(request)
		if err == nil && strings.HasSuffix(request.URL.Path, "/prompt_async") {
			response.Body = &signalReadCloser{ReadCloser: response.Body, closed: promptBodyClosed}
		}
		return response, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- fixture.adapter.Run(ctx, agent.RunRequest{SessionID: "ags_wait", ExternalSessionID: "ses_fake_http", UserText: "wait", RemoteExec: noopInvoker{}}, &recordingSink{})
	}()
	select {
	case <-fixture.eventSeen:
	case <-time.After(time.Second):
		t.Fatal("event subscription not established")
	}
	select {
	case <-promptBodyClosed:
	case <-time.After(time.Second):
		t.Fatal("Adapter prompt did not close the 204 response body")
	}
	select {
	case err := <-done:
		t.Fatalf("204 completed Turn: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Turn did not close blocking SSE")
	}
	_, aborts := fixture.snapshot()
	if aborts != 1 || fixture.bridge.ActiveCount() != 0 {
		t.Fatalf("aborts=%d active mappings=%d", aborts, fixture.bridge.ActiveCount())
	}
}

func TestAdapterPromptFailureCleansMappingBeforeAbort(t *testing.T) {
	fixture := newAdapterFixture(t)
	fixture.promptStatus = http.StatusInternalServerError
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-request.Context().Done()
	}
	err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_prompt_fail", ExternalSessionID: "ses_fake_http", UserText: "fail", RemoteExec: noopInvoker{}}, &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "provider_unavailable" {
		t.Fatalf("prompt error=%v", err)
	}
	if fixture.bridge.ActiveCount() != 0 {
		t.Fatalf("mapping remained active=%d", fixture.bridge.ActiveCount())
	}
}

func TestAdapterReusesPersistedSessionWithoutCreate(t *testing.T) {
	fixture := newAdapterFixture(t)
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-fixture.promptSeen
		promptID := "msg_generated_reuse"
		_, _ = io.WriteString(response, sseEvent(t, "user", "message.updated", map[string]any{"info": map[string]any{"id": promptID, "sessionID": "ses_existing", "role": "user"}})+sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_reuse", "sessionID": "ses_existing", "role": "assistant", "parentID": promptID, "finish": "stop"}})+sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": "ses_existing"}))
	}
	if err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_reuse", ExternalSessionID: "ses_existing", UserText: "again", RemoteExec: noopInvoker{}}, &recordingSink{}); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, request := range fixture.requests {
		if request == "POST /session" || strings.HasPrefix(request, "POST /session?") {
			t.Fatalf("persisted session was recreated: %v", fixture.requests)
		}
	}
}

func TestAdapterMalformedStreamIsTypedAndAborted(t *testing.T) {
	fixture := newAdapterFixture(t)
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		_, _ = io.WriteString(response, "data: {bad}\n\n")
	}
	err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_bad_sse", ExternalSessionID: "ses_fake_http", UserText: "bad", RemoteExec: noopInvoker{}}, &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
		t.Fatalf("malformed stream=%v", err)
	}
	_, aborts := fixture.snapshot()
	if aborts != 1 || fixture.bridge.ActiveCount() != 0 {
		t.Fatalf("aborts=%d active=%d", aborts, fixture.bridge.ActiveCount())
	}
}

type errorInvoker struct{ err error }

func (invoker errorInvoker) Invoke(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
	return agent.ToolResult{}, invoker.err
}

func TestBridgeFatalOriginalErrorPropagatesThroughAdapterRun(t *testing.T) {
	fixture := newAdapterFixture(t)
	sentinel := errors.New("sentinel bound invoker failure")
	fixture.afterPrompt = func(prompt promptRequest) {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
		_, _ = mac.Write([]byte("AISummoner.OpenCodeBridge.v1\x00ses_fake_http\x00" + timestamp))
		body, err := json.Marshal(map[string]any{"session_id": "ses_fake_http", "command": "hostname", "timeout_seconds": 30})
		if err != nil {
			t.Error(err)
			return
		}
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+opencodebridge.CallbackPath, bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:43001"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(opencodebridge.HeaderTimestamp, timestamp)
		request.Header.Set("Authorization", opencodebridge.Authorization+" "+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		fixture.bridge.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Errorf("callback status=%d", response.Code)
		}
	}
	err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_fatal", ExternalSessionID: "ses_fake_http", UserText: "invoke", RemoteExec: errorInvoker{err: sentinel}}, &recordingSink{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error=%v", err)
	}
	_, aborts := fixture.snapshot()
	if aborts != 1 || fixture.bridge.ActiveCount() != 0 {
		t.Fatalf("aborts=%d active=%d", aborts, fixture.bridge.ActiveCount())
	}
}

func TestAdapterRejectsWrongSSEMediaType(t *testing.T) {
	fixture := newAdapterFixture(t)
	fixture.event = func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, "{}")
	}
	err := fixture.adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_media", ExternalSessionID: "ses_fake_http", UserText: "bad", RemoteExec: noopInvoker{}}, &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
		t.Fatalf("wrong media type=%v", err)
	}
}

func TestAdapterStatusErrorsAndRedirectRejection(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{{http.StatusUnauthorized, "unauthorized"}, {http.StatusTooManyRequests, "rate_limited"}, {http.StatusBadGateway, "provider_unavailable"}} {
		t.Run(test.code, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
			})
			bridge, _ := opencodebridge.New(opencodebridge.Options{Secret: []byte(strings.Repeat("s", 32))})
			adapter, err := NewAdapter(Options{BaseURL: "http://127.0.0.1:4096", Username: "u", Password: "p", ModelID: "free", WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), HTTPClient: &http.Client{Transport: transport}, Bridge: bridge})
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_status", RemoteExec: noopInvoker{}}, &recordingSink{})
			var adapterError *agent.AdapterError
			if !errors.As(err, &adapterError) || adapterError.Code != test.code {
				t.Fatalf("status %d error=%v", test.status, err)
			}
		})
	}

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Error("Basic Auth followed redirect")
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, redirectTarget.URL, http.StatusFound)
	}))
	defer source.Close()
	bridge, _ := opencodebridge.New(opencodebridge.Options{Secret: []byte(strings.Repeat("s", 32))})
	adapter, err := NewAdapter(Options{BaseURL: source.URL, Username: "u", Password: "redirect-secret", ModelID: "free", WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), Bridge: bridge})
	if err != nil {
		t.Fatal(err)
	}
	if status := adapter.Health(context.Background()).Status; status != HealthUnavailable {
		t.Fatalf("redirect health=%s", status)
	}
}

func TestAdapterPromptResponseBodyBound(t *testing.T) {
	bridge, _ := opencodebridge.New(opencodebridge.Options{Secret: []byte(strings.Repeat("s", 32))})
	for _, test := range []struct {
		name      string
		status    int
		body      int
		wantError bool
	}{
		{name: "legal 204 body bound", status: http.StatusNoContent, body: maximumAbortBytes},
		{name: "oversized 204 body", status: http.StatusNoContent, body: maximumAbortBytes + 1, wantError: true},
		{name: "200 rejected", status: http.StatusOK, wantError: true},
		{name: "201 rejected", status: http.StatusCreated, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", test.body))), Request: request}, nil
			})
			adapter, err := NewAdapter(Options{BaseURL: "http://127.0.0.1:4096", Username: "u", Password: "p", ModelID: "free", WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), HTTPClient: &http.Client{Transport: transport}, Bridge: bridge})
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.prompt(context.Background(), "/tmp/work space", "ses_body", "body", func() {})
			var adapterError *agent.AdapterError
			if test.wantError && (!errors.As(err, &adapterError) || adapterError.Code != "protocol_error") {
				t.Fatalf("status=%d body=%d error=%v", test.status, test.body, err)
			}
			if !test.wantError && err != nil {
				t.Fatal(err)
			}
		})
	}
}

type lateFatalActivation struct {
	failures  chan error
	release   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func (activation *lateFatalActivation) Failures() <-chan error { return activation.failures }

func (activation *lateFatalActivation) Close() {
	activation.closeOnce.Do(func() { close(activation.release) })
	<-activation.done
}

type lateFatalActivator struct {
	activation *lateFatalActivation
}

func (activator *lateFatalActivator) Activate(context.Context, string, string, agent.RemoteExecInvoker) (opencodebridge.Activation, error) {
	return activator.activation, nil
}

func TestAdapterTerminalRacingLateCallbackFatalCannotReturnSuccess(t *testing.T) {
	sentinel := errors.New("late remote exec fatal")
	activation := &lateFatalActivation{failures: make(chan error, 1), release: make(chan struct{}), done: make(chan struct{})}
	go func() {
		<-activation.release
		activation.failures <- sentinel
		close(activation.done)
	}()
	promptDispatched := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/event":
			response.Header().Set("Content-Type", "text/event-stream")
			response.WriteHeader(http.StatusOK)
			response.(http.Flusher).Flush()
			<-promptDispatched
			currentPrompt := "msg_generated_late"
			_, _ = io.WriteString(response, sseEvent(t, "user", "message.updated", map[string]any{"info": map[string]any{"id": currentPrompt, "sessionID": "ses_late_fatal", "role": "user"}})+sseEvent(t, "assistant", "message.updated", map[string]any{"info": map[string]any{"id": "msg_assistant", "sessionID": "ses_late_fatal", "role": "assistant", "parentID": currentPrompt, "finish": "stop"}})+sseEvent(t, "idle", "session.idle", map[string]any{"sessionID": "ses_late_fatal"}))
			response.(http.Flusher).Flush()
		case strings.HasSuffix(request.URL.Path, "/prompt_async"):
			var prompt promptRequest
			if err := json.NewDecoder(request.Body).Decode(&prompt); err != nil {
				t.Error(err)
			}
			if prompt.MessageID != "" {
				t.Errorf("prompt message ID=%q", prompt.MessageID)
			}
			promptDispatched <- struct{}{}
			response.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(request.URL.Path, "/abort"):
			_, _ = io.WriteString(response, "true")
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	adapter, err := NewAdapter(Options{BaseURL: server.URL, Username: "u", Password: "p", ModelID: "free", WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), Bridge: &lateFatalActivator{activation: activation}})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Run(context.Background(), agent.RunRequest{SessionID: "ags_late_fatal", ExternalSessionID: "ses_late_fatal", UserText: "late", RemoteExec: noopInvoker{}}, &recordingSink{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run returned %v instead of late fatal", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type closeTrackingBody struct {
	io.Reader
	mu     sync.Mutex
	closed bool
}

func (body *closeTrackingBody) Close() error {
	body.mu.Lock()
	body.closed = true
	body.mu.Unlock()
	return nil
}

func (body *closeTrackingBody) isClosed() bool {
	body.mu.Lock()
	defer body.mu.Unlock()
	return body.closed
}

func TestAdapterClosesBoundedHTTPResponseBodies(t *testing.T) {
	bridge, _ := opencodebridge.New(opencodebridge.Options{Secret: []byte(strings.Repeat("s", 32))})
	for _, test := range []struct {
		name   string
		status int
		body   string
		call   func(*Adapter)
	}{
		{name: "health", status: http.StatusOK, body: `{"healthy":true,"version":"1.18.11"}`, call: func(adapter *Adapter) { _ = adapter.Health(context.Background()) }},
		{name: "create", status: http.StatusCreated, body: `{"id":"ses_body_close"}`, call: func(adapter *Adapter) { _, _ = adapter.createSession(context.Background(), "/tmp/work", "ags_body") }},
		{name: "prompt", status: http.StatusNoContent, call: func(adapter *Adapter) {
			_ = adapter.prompt(context.Background(), "/tmp/work", "ses_body", "hello", func() {})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &closeTrackingBody{Reader: strings.NewReader(test.body)}
			adapter, err := NewAdapter(Options{
				BaseURL: "http://127.0.0.1:4096", Username: "u", Password: "p", ModelID: "free",
				WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"), Bridge: bridge,
				HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: body, Request: request}, nil
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			test.call(adapter)
			if !body.isClosed() {
				t.Fatal("HTTP response body was not closed")
			}
		})
	}
}

type signalReadCloser struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (body *signalReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(func() { close(body.closed) })
	return err
}

func TestPromptDTOUsesDistinctModelFieldsAndDirectoryEscaping(t *testing.T) {
	create, _ := json.Marshal(createSessionRequest{Model: createSessionModel{ID: "model", ProviderID: ProviderName, Variant: "default"}})
	prompt, _ := json.Marshal(promptRequest{Model: promptModel{ModelID: "model", ProviderID: ProviderName}})
	if bytes.Contains(create, []byte("modelID")) || !bytes.Contains(create, []byte(`"id":"model"`)) || bytes.Contains(prompt, []byte(`"id":"model"`)) || !bytes.Contains(prompt, []byte(`"modelID":"model"`)) {
		t.Fatalf("create=%s prompt=%s", create, prompt)
	}
	if bytes.Contains(prompt, []byte("messageID")) {
		t.Fatalf("prompt must let OpenCode allocate its monotonic message ID: %s", prompt)
	}
	value := "/tmp/path with spaces/+?#"
	query := url.Values{"directory": []string{value}}.Encode()
	parsed, _ := url.Parse("http://localhost/event?" + query)
	if parsed.Query().Get("directory") != value {
		t.Fatalf("directory round trip=%q", parsed.Query().Get("directory"))
	}
	proof := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if strings.ContainsAny(proof, "+/=") {
		t.Fatalf("proof is not raw URL encoding: %q", proof)
	}
}
