package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aisummoner/aisummoner/internal/agent"
)

type configurationRPCCall struct {
	method  string
	payload json.RawMessage
}

type configurationRPCFixture struct {
	t      *testing.T
	server *httptest.Server

	mu    sync.Mutex
	calls []configurationRPCCall
}

func newConfigurationRPCFixture(t *testing.T) *configurationRPCFixture {
	t.Helper()
	fixture := &configurationRPCFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *configurationRPCFixture) adapter(t *testing.T) *Adapter {
	t.Helper()
	adapter, err := NewAdapter(Options{
		BaseURL: fixture.server.URL,
		Bridge:  &recordingActivator{activation: newTestActivation()},
		NewID: func(string) (string, error) {
			return "req_configuration", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func (fixture *configurationRPCFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	var envelope struct {
		Type    string          `json:"type"`
		RPCID   string          `json:"rpcId"`
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&envelope) != nil ||
		envelope.Type != "client-request" || envelope.RPCID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	fixture.mu.Lock()
	fixture.calls = append(fixture.calls, configurationRPCCall{method: envelope.Method, payload: append(json.RawMessage(nil), envelope.Payload...)})
	fixture.mu.Unlock()

	var value any
	switch envelope.Method {
	case "llm.providers":
		value = map[string]any{"providers": []map[string]any{
			{"provider": deepSeekProviderRoute, "displayName": "DeepSeek", "settingsNs": deepSeekSettingsNamespace, "settingsPath": []string{}, "active": true},
			{"provider": "openai", "displayName": "OpenAI", "settingsNs": piAISettingsNamespace, "settingsPath": []string{"providers", "openai"}, "active": false, "declared": false},
			{"provider": "acme-gateway", "displayName": "Acme", "settingsNs": piAISettingsNamespace, "settingsPath": []string{"providers", "acme-gateway"}, "active": true, "declared": true},
		}}
	case "settings.describe":
		value = fixture.settingsDescription()
	case "credentials.describe":
		var payload struct {
			Refs []string `json:"refs"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		credentials := make(map[string]any, len(payload.Refs))
		for _, ref := range payload.Refs {
			credentials[ref] = map[string]any{"configured": true, "source": "file", "writable": true}
		}
		value = map[string]any{"credentials": credentials}
	case "settings.mutate":
		var payload struct {
			NS string `json:"ns"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		value = map[string]any{
			"ns": payload.NS, "schema": map[string]any{}, "value": map[string]any{},
			"base": map[string]any{}, "user": map[string]any{}, "applies": "live",
			"secrets": []any{}, "revision": 8,
		}
	case "credentials.set", "credentials.unset":
		value = map[string]any{}
	case "session.models":
		value = map[string]any{
			"current":  map[string]any{"provider": "acme-gateway", "model": "acme-pro", "reasoningEffort": "high"},
			"routable": true,
			"groups": []map[string]any{{
				"id": "acme-gateway", "name": "Acme", "models": []map[string]any{{
					"id": "acme-pro", "name": "Acme Pro", "description": "Primary model",
					"reasoning": map[string]any{
						"efforts":       []map[string]any{{"id": "off", "name": "关闭"}, {"id": "high", "name": "高"}},
						"defaultEffort": "high",
					},
				}},
			}},
			"failures": []map[string]any{{"id": "broken", "name": "Broken", "message": "offline"}},
		}
	case "session.selectModel":
		var payload modelSelectionWire
		var requestPayload struct {
			SessionID string `json:"sessionId"`
			modelSelectionWire
		}
		if json.Unmarshal(envelope.Payload, &requestPayload) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		payload = requestPayload.modelSelectionWire
		if payload.ReasoningEffort == "" {
			payload.ReasoningEffort = "high"
		}
		value = map[string]any{"selected": payload}
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

func (fixture *configurationRPCFixture) settingsDescription() map[string]any {
	return map[string]any{
		"writable": true, "hasDocument": true,
		"namespaces": []map[string]any{
			{
				"ns": deepSeekSettingsNamespace, "schema": map[string]any{},
				"value": map[string]any{
					"apiKeyEnv": CredentialReference, "baseURL": "https://api.deepseek.com",
					"models": []map[string]any{{"id": "deepseek-v4-flash", "name": "V4 Flash", "contextWindow": 1000000}},
				},
				"base": map[string]any{}, "user": map[string]any{"retryPolicy": map[string]any{"mode": "normal"}},
				"applies": "live", "secrets": []any{}, "revision": 2,
			},
			{
				"ns": piAISettingsNamespace, "schema": map[string]any{},
				"value": map[string]any{"providers": map[string]any{
					"acme-gateway": map[string]any{
						"displayName": "Acme Gateway", "apiKeyEnv": "ACME_GATEWAY_API_KEY",
						"baseURL": "https://acme.example/v1", "api": "openai-responses",
						"models": []map[string]any{{"id": "acme-pro", "name": "Acme Pro"}},
					},
				}},
				"base": map[string]any{"providers": map[string]any{}},
				"user": map[string]any{"providers": map[string]any{
					"acme-gateway": map[string]any{
						"apiKeyEnv": "ACME_GATEWAY_API_KEY", "baseURL": "https://acme.example/v1",
					},
				}},
				"applies": "live", "secrets": []any{}, "revision": 7,
			},
		},
	}
}

func (fixture *configurationRPCFixture) takeCalls() []configurationRPCCall {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	calls := append([]configurationRPCCall(nil), fixture.calls...)
	fixture.calls = nil
	return calls
}

func TestProviderDirectoryJoinsNativeFactsWithoutCredentialLeaks(t *testing.T) {
	fixture := newConfigurationRPCFixture(t)
	directory, err := fixture.adapter(t).ProviderDirectory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if directory.Runtime != ProviderName || !directory.Writable || directory.CustomProviderRevision != 7 || len(directory.Providers) != 3 {
		t.Fatalf("directory=%#v", directory)
	}
	if directory.Providers[0].ID != deepSeekProviderRoute || directory.Providers[1].ID != "openai" || directory.Providers[2].ID != "acme-gateway" {
		t.Fatalf("provider order=%#v", directory.Providers)
	}
	deepSeek := directory.Providers[0]
	if !deepSeek.Configured || !deepSeek.Active || deepSeek.Credential == nil || !deepSeek.Credential.Configured || len(deepSeek.Models) != 1 {
		t.Fatalf("deepseek=%#v", deepSeek)
	}
	openAI := directory.Providers[1]
	if openAI.Configured || openAI.Active || openAI.Credential != nil || openAI.Custom {
		t.Fatalf("openai=%#v", openAI)
	}
	custom := directory.Providers[2]
	if !custom.Custom || !custom.Removable || !custom.Active || custom.DisplayName != "Acme Gateway" || custom.API != "openai-responses" {
		t.Fatalf("custom=%#v", custom)
	}
	encoded, err := json.Marshal(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"DEEPSEEK_API_KEY", "ACME_GATEWAY_API_KEY", "source", "private-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("provider directory leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestConfigureProviderUsesMinimalSettingsOpsThenWriteOnlyCredential(t *testing.T) {
	fixture := newConfigurationRPCFixture(t)
	adapter := fixture.adapter(t)
	secret := "sk-private-provider-test"
	err := adapter.ConfigureProvider(context.Background(), agent.RuntimeProviderMutation{
		Provider: "openai", ExpectedRevision: 7, BaseURL: "https://proxy.example/v1",
		ModelsOverridden: true, Models: []agent.RuntimeProviderModel{{ID: "gpt-test", Name: "GPT Test"}},
		APIKey: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := fixture.takeCalls()
	if len(calls) < 5 || calls[len(calls)-2].method != "settings.mutate" || calls[len(calls)-1].method != "credentials.set" {
		t.Fatalf("calls=%#v", calls)
	}
	mutate := calls[len(calls)-2].payload
	if strings.Contains(string(mutate), secret) || !strings.Contains(string(mutate), `"expectedRevision":7`) ||
		!strings.Contains(string(mutate), `"path":["providers","openai","apiKeyEnv"]`) ||
		!strings.Contains(string(mutate), `"value":[{"id":"gpt-test","name":"GPT Test"}]`) ||
		strings.Contains(string(mutate), `"ID"`) || strings.Contains(string(mutate), `"ContextWindow"`) {
		t.Fatalf("settings mutation=%s", mutate)
	}
	credential := calls[len(calls)-1].payload
	if !strings.Contains(string(credential), `"ref":"OPENAI_API_KEY"`) || !strings.Contains(string(credential), secret) {
		t.Fatalf("credential mutation=%s", credential)
	}

	if err := adapter.ConfigureProvider(context.Background(), agent.RuntimeProviderMutation{
		Provider: "unsafe", ExpectedRevision: 7, BaseURL: "http://10.0.0.8/v1", API: "openai-responses",
		ModelsOverridden: true, Models: []agent.RuntimeProviderModel{{ID: "model"}},
	}); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("unsafe provider URL error=%v", err)
	}
}

func TestCustomProviderCreateAndRemovalUseNativeRevisionAndOwnedCredential(t *testing.T) {
	fixture := newConfigurationRPCFixture(t)
	adapter := fixture.adapter(t)
	if err := adapter.ConfigureProvider(context.Background(), agent.RuntimeProviderMutation{
		Provider: "my-gateway", ExpectedRevision: 7, DisplayName: "My Gateway",
		BaseURL: "https://gateway.example/v1", API: "anthropic-messages",
		ModelsOverridden: true, Models: []agent.RuntimeProviderModel{{ID: "sonnet"}}, APIKey: "sk-custom-test",
	}); err != nil {
		t.Fatal(err)
	}
	calls := fixture.takeCalls()
	if calls[len(calls)-2].method != "settings.mutate" || calls[len(calls)-1].method != "credentials.set" ||
		!strings.Contains(string(calls[len(calls)-2].payload), `"path":["providers","my-gateway"]`) {
		t.Fatalf("custom create calls=%#v", calls)
	}

	if err := adapter.RemoveProvider(context.Background(), "acme-gateway", 7); err != nil {
		t.Fatal(err)
	}
	calls = fixture.takeCalls()
	if calls[len(calls)-2].method != "settings.mutate" || calls[len(calls)-1].method != "credentials.unset" ||
		!strings.Contains(string(calls[len(calls)-2].payload), `"op":"unset"`) {
		t.Fatalf("remove calls=%#v", calls)
	}
}

func TestSessionModelsAndSelectionUseNativeDSHRoute(t *testing.T) {
	fixture := newConfigurationRPCFixture(t)
	adapter := fixture.adapter(t)
	directory, err := adapter.Models(context.Background(), "ses_models")
	if err != nil {
		t.Fatal(err)
	}
	if !directory.Routable || directory.Current.Provider != "acme-gateway" || directory.Current.ReasoningEffort != "high" ||
		directory.CurrentCredential == nil || !directory.CurrentCredential.Configured || len(directory.Groups) != 1 ||
		len(directory.Groups[0].Models[0].ReasoningEfforts) != 2 || len(directory.Failures) != 1 {
		t.Fatalf("models=%#v", directory)
	}
	selection := agent.ModelSelection{Provider: "acme-gateway", Model: "acme-pro", ReasoningEffort: "off"}
	selected, err := adapter.SelectModel(context.Background(), "ses_models", selection)
	if err != nil || selected != selection {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}
	normalized, err := adapter.SelectModel(context.Background(), "ses_models", agent.ModelSelection{Provider: "acme-gateway", Model: "acme-pro"})
	if err != nil || normalized.ReasoningEffort != "high" {
		t.Fatalf("normalized selection=%#v err=%v", normalized, err)
	}
}

func TestLiveProviderDirectoryContract(t *testing.T) {
	baseURL := os.Getenv("AISUMMONER_TEST_DSH_URL")
	if baseURL == "" {
		t.Skip("AISUMMONER_TEST_DSH_URL is not set")
	}
	adapter, err := NewAdapter(Options{BaseURL: baseURL, Bridge: &recordingActivator{activation: newTestActivation()}})
	if err != nil {
		t.Fatal(err)
	}
	directory, err := adapter.ProviderDirectory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if directory.Runtime != ProviderName || directory.DisplayName == "" || len(directory.Providers) == 0 {
		t.Fatal("live DSH provider directory did not satisfy the pinned contract")
	}
	encoded, err := json.Marshal(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"credentialRef"`, `"credential_ref"`, `"apiKeyEnv"`, `"source"`, `"secret"`, `"value"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("live provider directory exposed forbidden field %q", forbidden)
		}
	}
}
