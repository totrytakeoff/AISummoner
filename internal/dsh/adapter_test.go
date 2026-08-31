package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
	"github.com/coder/websocket"
)

type recordingSink struct {
	mu        sync.Mutex
	reasoning []string
	text      []string
	states    []string
	external  string
}

func (sink *recordingSink) ReasoningDelta(_ context.Context, value string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.reasoning = append(sink.reasoning, value)
	return nil
}

func (sink *recordingSink) TextDelta(_ context.Context, value string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.text = append(sink.text, value)
	return nil
}

func (sink *recordingSink) ProviderState(_ context.Context, value string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.states = append(sink.states, value)
	return nil
}

func (sink *recordingSink) SetExternalSessionID(_ context.Context, value string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.external = value
	return nil
}

func (sink *recordingSink) snapshot() (reasoning, text, external string, states []string) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return strings.Join(sink.reasoning, ""), strings.Join(sink.text, ""), sink.external, append([]string(nil), sink.states...)
}

type noopInvoker struct{}

func (noopInvoker) Invoke(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
	return agent.ToolResult{}, nil
}

type testActivation struct {
	failures chan error
	closed   chan struct{}
	once     sync.Once
}

func newTestActivation() *testActivation {
	return &testActivation{failures: make(chan error, 1), closed: make(chan struct{})}
}

func (activation *testActivation) Failures() <-chan error { return activation.failures }
func (activation *testActivation) Close() {
	activation.once.Do(func() { close(activation.closed) })
}

type recordingActivator struct {
	mu          sync.Mutex
	activation  *testActivation
	productID   string
	externalID  string
	invoker     agent.RemoteExecInvoker
	activations int
}

func (bridge *recordingActivator) Activate(_ context.Context, productID, externalID string, invoker agent.RemoteExecInvoker) (opencodebridge.Activation, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.productID = productID
	bridge.externalID = externalID
	bridge.invoker = invoker
	bridge.activations++
	return bridge.activation, nil
}

func (bridge *recordingActivator) snapshot() (string, string, agent.RemoteExecInvoker, int) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.productID, bridge.externalID, bridge.invoker, bridge.activations
}

type dshFixture struct {
	t             *testing.T
	server        *httptest.Server
	externalID    string
	sendTurn      bool
	promptSeen    chan struct{}
	subscribed    chan struct{}
	websocketDone chan struct{}
	promptOnce    sync.Once
	subscribeOnce sync.Once
	websocketOnce sync.Once

	mu         sync.Mutex
	created    []map[string]any
	prompts    []map[string]any
	cancels    int
	credential string
}

func newDSHFixture(t *testing.T, externalID string, sendTurn bool) *dshFixture {
	t.Helper()
	fixture := &dshFixture{
		t: t, externalID: externalID, sendTurn: sendTurn,
		promptSeen: make(chan struct{}), subscribed: make(chan struct{}), websocketDone: make(chan struct{}),
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(func() { fixture.server.Close() })
	return fixture
}

func (fixture *dshFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/events.mux" {
		fixture.serveEvents(writer, request)
		return
	}
	if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
		writer.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	var envelope struct {
		Type    string          `json:"type"`
		RPCID   string          `json:"rpcId"`
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil || envelope.Type != "client-request" || envelope.RPCID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var value any
	switch envelope.Method {
	case "host.describe":
		value = map[string]any{"version": "0.1.0-rc.5", "cwd": "/", "attachedSessions": 0, "canOpenPath": false}
	case "credentials.set":
		var payload struct {
			Ref   string `json:"ref"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Ref != CredentialReference {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.credential = payload.Value
		fixture.mu.Unlock()
		value = map[string]any{}
	case "credentials.describe":
		var payload struct {
			Refs []string `json:"refs"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || len(payload.Refs) != 1 || payload.Refs[0] != CredentialReference {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		configured := fixture.credential != ""
		fixture.mu.Unlock()
		credential := map[string]any{"configured": configured, "writable": true}
		if configured {
			credential["source"] = "file"
		}
		value = map[string]any{"credentials": map[string]any{CredentialReference: credential}}
	case "llm.providers":
		value = map[string]any{"providers": []map[string]any{{
			"provider": deepSeekProviderRoute, "displayName": "DeepSeek",
			"settingsNs": deepSeekSettingsNamespace, "settingsPath": []string{}, "active": true,
		}}}
	case "settings.describe":
		value = map[string]any{
			"writable": true, "hasDocument": true,
			"namespaces": []map[string]any{{
				"ns": deepSeekSettingsNamespace, "schema": map[string]any{},
				"value": map[string]any{"apiKeyEnv": CredentialReference},
				"base":  map[string]any{}, "user": map[string]any{}, "applies": "live",
				"secrets": []any{}, "revision": 0,
			}},
		}
	case "session.models":
		value = map[string]any{
			"current":  map[string]any{"provider": deepSeekProviderRoute, "model": "deepseek-v4-flash"},
			"routable": true,
			"groups": []map[string]any{{
				"id": deepSeekProviderRoute, "name": "DeepSeek",
				"models": []map[string]any{{"id": "deepseek-v4-flash", "name": "DeepSeek V4 Flash"}},
			}},
			"failures": []any{},
		}
	case "session.create":
		var payload map[string]any
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.created = append(fixture.created, payload)
		fixture.mu.Unlock()
		preset, _ := payload["agentPreset"].(string)
		value = map[string]any{"sessionId": fixture.externalID, "agentPreset": preset}
	case "session.prompt":
		var payload map[string]any
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.prompts = append(fixture.prompts, payload)
		fixture.mu.Unlock()
		value = map[string]any{"accepted": true}
		defer fixture.promptOnce.Do(func() { close(fixture.promptSeen) })
	case "session.cancel":
		fixture.mu.Lock()
		fixture.cancels++
		fixture.mu.Unlock()
		value = map[string]any{"accepted": true}
	default:
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"type": "server-response", "rpcId": envelope.RPCID,
		"result": map[string]any{"ok": true, "value": value},
	})
}

func TestCredentialStatusAndTurnPreflightAreValueFreeAndRecoverable(t *testing.T) {
	fixture := newDSHFixture(t, "ses_credential_status", false)
	adapter, err := NewAdapter(Options{
		BaseURL: fixture.server.URL,
		Bridge:  &recordingActivator{activation: newTestActivation()},
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := adapter.DescribeCredential(context.Background())
	if err != nil || status.Configured || !status.Writable {
		t.Fatalf("missing credential status=%#v err=%v", status, err)
	}
	var adapterError *agent.AdapterError
	request := agent.RunRequest{ExternalSessionID: "ses_credential_status"}
	if err := adapter.PreflightTurn(context.Background(), request); !errors.As(err, &adapterError) || adapterError.Code != "credential_required" {
		t.Fatalf("missing credential preflight=%v", err)
	}
	if err := adapter.ConfigureCredential(context.Background(), "sk-private-test"); err != nil {
		t.Fatal(err)
	}
	status, err = adapter.DescribeCredential(context.Background())
	if err != nil || !status.Configured || !status.Writable {
		t.Fatalf("configured credential status=%#v err=%v", status, err)
	}
	if err := adapter.PreflightTurn(context.Background(), request); err != nil {
		t.Fatalf("configured credential preflight=%v", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sk-private-test") || strings.Contains(string(encoded), "source") {
		t.Fatalf("credential status leaked private fields: %s", encoded)
	}
}

func (fixture *dshFixture) serveEvents(writer http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(writer, request, nil)
	if err != nil {
		fixture.t.Errorf("accept DSH events: %v", err)
		return
	}
	defer connection.CloseNow()
	defer fixture.websocketOnce.Do(func() { close(fixture.websocketDone) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := writeMuxFrame(ctx, connection, "session/subscribed", map[string]any{
		"type": "session/subscribed", "sessionId": "ses_unrelated", "lastSeq": 9,
	}); err != nil {
		return
	}
	if err := writeMuxFrame(ctx, connection, "session/subscribed", map[string]any{
		"type": "session/subscribed", "sessionId": fixture.externalID, "lastSeq": 4,
	}); err != nil {
		return
	}
	fixture.subscribeOnce.Do(func() { close(fixture.subscribed) })
	select {
	case <-fixture.promptSeen:
	case <-ctx.Done():
		return
	}
	if fixture.sendTurn {
		frames := []struct {
			method  string
			payload map[string]any
		}{
			{method: "session/event", payload: sessionFrame(fixture.externalID, 5, "turn/start", map[string]any{"turn": 1, "trigger": map[string]any{"kind": "message"}})},
			{method: "session/event", payload: sessionFrame("ses_unrelated", 99, "turn/start", map[string]any{"turn": 7})},
			{method: "session/event", payload: sessionFrame(fixture.externalID, 6, "assistant/chunk", map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{"type": "reasoning-delta", "index": 0, "text": "inspect "}})},
			{method: "session/event", payload: sessionFrame(fixture.externalID, 7, "assistant/chunk", map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{"type": "block-start", "index": 0}})},
			{method: "session/event", payload: sessionFrame(fixture.externalID, 8, "assistant/chunk", map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{"type": "text-delta", "index": 0, "text": "remote result"}})},
			{method: "session/event", payload: sessionFrame(fixture.externalID, 9, "turn/end", map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}})},
		}
		for _, frame := range frames {
			if err := writeMuxFrame(ctx, connection, frame.method, frame.payload); err != nil {
				return
			}
		}
	}
	_, _, _ = connection.Read(ctx)
}

func writeMuxFrame(ctx context.Context, connection *websocket.Conn, method string, payload any) error {
	encoded, err := json.Marshal(map[string]any{
		"type": "server-request", "rpcId": "evt_" + method, "method": method, "payload": payload,
	})
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, encoded)
}

func sessionFrame(sessionID string, seq int64, eventType string, data any) map[string]any {
	return map[string]any{
		"type": "session/event", "sessionId": sessionID,
		"event": map[string]any{"type": eventType, "seq": seq, "time": seq, "data": data},
	}
}

func (fixture *dshFixture) snapshot() (created, prompts []map[string]any, cancels int, credential string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]map[string]any(nil), fixture.created...), append([]map[string]any(nil), fixture.prompts...), fixture.cancels, fixture.credential
}

func TestAdapterCreatesOrResumesAndMapsNativeTurn(t *testing.T) {
	for _, test := range []struct {
		name       string
		existingID string
		target     agent.ExecutionTarget
		preset     string
	}{
		{name: "create", preset: AgentPreset},
		{name: "resume", existingID: "ses_existing", preset: AgentPreset},
		{name: "windows", target: agent.ExecutionTarget{Platform: "windows", Arch: "amd64"}, preset: WindowsAgentPreset},
	} {
		t.Run(test.name, func(t *testing.T) {
			externalID := test.existingID
			if externalID == "" {
				externalID = "ses_generated"
			}
			fixture := newDSHFixture(t, externalID, true)
			activation := newTestActivation()
			bridge := &recordingActivator{activation: activation}
			var counter atomic.Int64
			adapter, err := NewAdapter(Options{
				BaseURL: fixture.server.URL, Bridge: bridge,
				NewID: func(prefix string) (string, error) {
					if prefix == "ses" {
						return "ses_generated", nil
					}
					return fmt.Sprintf("req_%d", counter.Add(1)), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if health := adapter.Health(context.Background()); health.Status != HealthAvailable || health.Version != "0.1.0-rc.5" {
				t.Fatalf("health=%#v", health)
			}
			if err := adapter.ConfigureCredential(context.Background(), "sk-private-test"); err != nil {
				t.Fatal(err)
			}
			sink := &recordingSink{}
			invoker := noopInvoker{}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = adapter.Run(ctx, agent.RunRequest{
				SessionID: "ags_product", ExternalSessionID: test.existingID,
				UserText: "inspect the selected device", RemoteExec: invoker, Target: test.target,
			}, sink)
			if err != nil {
				t.Fatal(err)
			}
			reasoning, text, persisted, states := sink.snapshot()
			if reasoning != "inspect " || text != "remote result" || len(states) != 1 || states[0] != "streaming" {
				t.Fatalf("reasoning=%q text=%q states=%v", reasoning, text, states)
			}
			if test.existingID == "" && persisted != externalID {
				t.Fatalf("new external id=%q", persisted)
			}
			if test.existingID != "" && persisted != "" {
				t.Fatalf("resumed session rewrote external id=%q", persisted)
			}
			productID, activatedExternalID, activatedInvoker, activations := bridge.snapshot()
			if productID != "ags_product" || activatedExternalID != externalID || activatedInvoker != invoker || activations != 1 {
				t.Fatalf("activation product=%q external=%q invoker=%T count=%d", productID, activatedExternalID, activatedInvoker, activations)
			}
			select {
			case <-activation.closed:
			default:
				t.Fatal("capability activation was not joined")
			}
			select {
			case <-fixture.websocketDone:
			case <-time.After(time.Second):
				t.Fatal("event stream worker did not close")
			}
			created, prompts, cancels, credential := fixture.snapshot()
			if len(created) != 1 || created[0]["cwd"] != "/" || created[0]["sessionId"] != externalID || created[0]["agentPreset"] != test.preset {
				t.Fatalf("session.create=%#v", created)
			}
			if len(prompts) != 1 || prompts[0]["sessionId"] != externalID || prompts[0]["mode"] != "queue" || cancels != 0 || credential != "sk-private-test" {
				t.Fatalf("prompts=%#v cancels=%d credential matched=%v", prompts, cancels, credential == "sk-private-test")
			}
		})
	}
}

func TestAdapterCancellationCancelsRuntimeAndJoinsStreamAndCapability(t *testing.T) {
	fixture := newDSHFixture(t, "ses_cancel", false)
	activation := newTestActivation()
	bridge := &recordingActivator{activation: activation}
	var counter atomic.Int64
	adapter, err := NewAdapter(Options{
		BaseURL: fixture.server.URL, Bridge: bridge,
		NewID: func(prefix string) (string, error) { return fmt.Sprintf("%s_%d", prefix, counter.Add(1)), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- adapter.Run(ctx, agent.RunRequest{
			SessionID: "ags_cancel", ExternalSessionID: "ses_cancel", UserText: "wait", RemoteExec: noopInvoker{},
		}, &recordingSink{})
	}()
	select {
	case <-fixture.promptSeen:
	case <-time.After(time.Second):
		t.Fatal("prompt was not accepted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled DSH Turn did not join")
	}
	_, _, cancels, _ := fixture.snapshot()
	if cancels != 1 {
		t.Fatalf("session.cancel calls=%d", cancels)
	}
	select {
	case <-activation.closed:
	default:
		t.Fatal("canceled capability was not joined")
	}
	select {
	case <-fixture.websocketDone:
	case <-time.After(time.Second):
		t.Fatal("canceled event stream was not joined")
	}
}

func TestConsumeTurnRejectsSequenceRegressionAndClassifiesTerminalReasons(t *testing.T) {
	stream := make(chan streamItem, 2)
	stream <- streamItem{frame: muxFrame{
		Type: "session/event", SessionID: "ses_order",
		Event: sessionEvent{Type: "turn/start", Seq: 4, Data: json.RawMessage(`{"turn":1}`)},
	}}
	stream <- streamItem{frame: muxFrame{
		Type: "session/event", SessionID: "ses_order",
		Event: sessionEvent{Type: "assistant/chunk", Seq: 4, Data: json.RawMessage(`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"duplicate"}}`)},
	}}
	close(stream)
	err := consumeTurn(context.Background(), "ses_order", 3, stream, nil, &recordingSink{})
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "protocol_error" {
		t.Fatalf("sequence regression error=%v", err)
	}

	tests := []struct {
		reason turnEndReason
		code   string
	}{
		{reason: turnEndReason{Kind: "completed"}},
		{reason: turnEndReason{Kind: "error", Error: struct {
			Code string `json:"code"`
		}{Code: "AUTH"}}, code: "unauthorized"},
		{reason: turnEndReason{Kind: "error", Error: struct {
			Code string `json:"code"`
		}{Code: "RATE_LIMIT"}}, code: "rate_limited"},
		{reason: turnEndReason{Kind: "error", Error: struct {
			Code string `json:"code"`
		}{Code: "SERVER"}}, code: "provider_unavailable"},
		{reason: turnEndReason{Kind: "max-tokens"}, code: "provider_rejected"},
	}
	for _, test := range tests {
		err := classifyTurnEnd(test.reason)
		if test.code == "" {
			if err != nil {
				t.Fatalf("completed reason error=%v", err)
			}
			continue
		}
		var safe *agent.AdapterError
		if !errors.As(err, &safe) || safe.Code != test.code {
			t.Fatalf("reason=%#v error=%v", test.reason, err)
		}
	}
}

func TestNewAdapterClonesHTTPClientAndRejectsUnsafeInputs(t *testing.T) {
	originalRedirect := func(*http.Request, []*http.Request) error { return nil }
	original := &http.Client{Timeout: time.Minute, CheckRedirect: originalRedirect}
	adapter, err := NewAdapter(Options{
		BaseURL: "http://127.0.0.1:14096", HTTPClient: original,
		Bridge: &recordingActivator{activation: newTestActivation()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.client == original || adapter.client.Timeout != 0 || original.Timeout != time.Minute {
		t.Fatal("HTTP client was not safely cloned")
	}
	if err := adapter.client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy=%v", err)
	}
	if err := original.CheckRedirect(nil, nil); err != nil {
		t.Fatalf("caller redirect policy mutated: %v", err)
	}
	for _, baseURL := range []string{"http://localhost:14096", "http://0.0.0.0:14096", "https://127.0.0.1:14096", "http://127.0.0.1:14096/path"} {
		if _, err := NewAdapter(Options{BaseURL: baseURL, Bridge: &recordingActivator{activation: newTestActivation()}}); err == nil {
			t.Fatalf("unsafe DSH URL accepted: %s", baseURL)
		}
	}
	if err := adapter.ConfigureCredential(context.Background(), " key-with-space"); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("invalid credential error=%v", err)
	}
}
