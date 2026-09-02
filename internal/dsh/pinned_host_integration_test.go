//go:build linux

package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
)

// TestPinnedHostLoadsBothReviewedExecutionPresets crosses the real pinned DSH
// Host boundary when AISUMMONER_TEST_DSH_RUNTIME_ROOT names an extracted
// runtime bundle. Ordinary unit runs skip it; release/contract gates provide
// the immutable bundle explicitly.
func TestPinnedHostLoadsBothReviewedExecutionPresets(t *testing.T) {
	runtimeRoot := os.Getenv("AISUMMONER_TEST_DSH_RUNTIME_ROOT")
	if runtimeRoot == "" {
		t.Skip("AISUMMONER_TEST_DSH_RUNTIME_ROOT is not configured")
	}
	runtimeRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := unusedLoopbackOrigin(t)
	bridgeURL := unusedLoopbackOrigin(t) + CallbackPath
	secret := []byte("pinned-host-preset-contract-secret-0001")
	bridge, err := opencodebridge.New(opencodebridge.Options{
		Secret: secret, CallbackPath: CallbackPath, ProofDomain: ProofDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := bridge.Close(closeCtx); err != nil {
			t.Errorf("close capability bridge: %v", err)
		}
	})
	adapter, err := NewAdapter(Options{BaseURL: baseURL, Bridge: bridge})
	if err != nil {
		t.Fatal(err)
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelStartup()
	host, err := StartHost(startupCtx, HostOptions{
		NodePath: filepath.Join(runtimeRoot, "node", "bin", "node"),
		CLIPath:  filepath.Join(runtimeRoot, "runtime", "lib", "bin.js"),
		Home:     filepath.Join(t.TempDir(), "dsh-home"), BaseURL: baseURL,
		BridgeURL: bridgeURL, BridgeSecret: secret, Probe: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Close(closeCtx); err != nil {
			t.Errorf("close pinned DSH Host: %v", err)
		}
	})

	windowsTarget := agent.ExecutionTarget{
		Platform: "windows", Arch: "amd64", Shell: agent.ExecutionShellWindowsPowerShell,
		PathFlavor: agent.PathFlavorWindows, DefaultCWDPolicy: agent.DefaultCWDUserProfile,
	}
	const windowsSession = "ses_windows_preset_contract"
	if got, err := adapter.PrepareSessionForTarget(context.Background(), windowsSession, windowsTarget); err != nil || got != windowsSession {
		t.Fatalf("create Windows preset Session=%q err=%v", got, err)
	}
	if got, err := adapter.PrepareSessionForTarget(context.Background(), windowsSession, windowsTarget); err != nil || got != windowsSession {
		t.Fatalf("resume Windows preset Session=%q err=%v", got, err)
	}

	linuxTarget := agent.ExecutionTarget{
		Platform: "linux", Arch: "amd64", Shell: agent.ExecutionShellPOSIXUser,
		PathFlavor: agent.PathFlavorPOSIX, DefaultCWDPolicy: agent.DefaultCWDInherit,
	}
	const linuxSession = "ses_linux_preset_contract"
	if got, err := adapter.PrepareSessionForTarget(context.Background(), linuxSession, linuxTarget); err != nil || got != linuxSession {
		t.Fatalf("create Linux preset Session=%q err=%v", got, err)
	}
	if _, err := adapter.PrepareSessionForTarget(context.Background(), windowsSession, linuxTarget); err == nil {
		t.Fatal("pinned DSH Host resumed a live Windows Session under the Linux preset")
	} else {
		var adapterError *agent.AdapterError
		if !errors.As(err, &adapterError) || adapterError.Code != "provider_rejected" {
			t.Fatalf("preset conflict error=%v", err)
		}
	}
}

// TestPinnedHostWindowsPresetRunsRemoteToolTurn exercises the pinned runtime,
// the reviewed Windows preset, DSH's OpenAI-compatible provider adapter, the
// private authenticated bridge, and the final streamed answer in one process.
// The invoker is deterministic here; the Windows CI job separately proves that
// the same RemoteExec seam reaches native Windows PowerShell 5.1 over SSH.
func TestPinnedHostWindowsPresetRunsRemoteToolTurn(t *testing.T) {
	runtimeRoot := os.Getenv("AISUMMONER_TEST_DSH_RUNTIME_ROOT")
	if runtimeRoot == "" {
		t.Skip("AISUMMONER_TEST_DSH_RUNTIME_ROOT is not configured")
	}
	runtimeRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}

	provider := &pinnedProviderContract{}
	providerServer := httptest.NewServer(provider)
	t.Cleanup(providerServer.Close)

	secret := []byte("pinned-host-windows-turn-secret-0001")
	bridge, err := opencodebridge.New(opencodebridge.Options{
		Secret: secret, CallbackPath: CallbackPath, ProofDomain: ProofDomain,
	})
	if err != nil {
		t.Fatal(err)
	}
	bridgeServer := httptest.NewServer(bridge.Handler())
	t.Cleanup(bridgeServer.Close)
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := bridge.Close(closeCtx); err != nil {
			t.Errorf("close capability bridge: %v", err)
		}
	})

	baseURL := unusedLoopbackOrigin(t)
	adapter, err := NewAdapter(Options{BaseURL: baseURL, Bridge: bridge})
	if err != nil {
		t.Fatal(err)
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelStartup()
	host, err := StartHost(startupCtx, HostOptions{
		NodePath: filepath.Join(runtimeRoot, "node", "bin", "node"),
		CLIPath:  filepath.Join(runtimeRoot, "runtime", "lib", "bin.js"),
		Home:     filepath.Join(t.TempDir(), "dsh-home"), BaseURL: baseURL,
		BridgeURL: bridgeServer.URL + CallbackPath, BridgeSecret: secret, Probe: adapter,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Close(closeCtx); err != nil {
			t.Errorf("close pinned DSH Host: %v", err)
		}
	})

	testCtx, cancelTest := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTest()
	directory, err := adapter.ProviderDirectory(testCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !directory.Writable {
		t.Fatal("pinned DSH provider directory is not writable")
	}
	const (
		providerID = "contract-mock"
		modelID    = "contract-model"
		apiKey     = "pinned-contract-key"
	)
	if err := adapter.ConfigureProvider(testCtx, agent.RuntimeProviderMutation{
		Provider: providerID, ExpectedRevision: directory.CustomProviderRevision,
		DisplayName: "Pinned Contract Mock", BaseURL: providerServer.URL + "/v1",
		API: "openai-completions", APIKey: apiKey, ModelsOverridden: true,
		Models: []agent.RuntimeProviderModel{{
			ID: modelID, Name: "Pinned Contract Model", ContextWindow: 32768, MaxTokens: 4096,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	provider.setExpectedAPIKey(apiKey)

	windowsTarget := agent.ExecutionTarget{
		Platform: "windows", Arch: "amd64", Shell: agent.ExecutionShellWindowsPowerShell,
		PathFlavor: agent.PathFlavorWindows, DefaultCWDPolicy: agent.DefaultCWDUserProfile,
	}
	const externalSessionID = "ses_windows_tool_contract"
	if got, err := adapter.PrepareSessionForTarget(testCtx, externalSessionID, windowsTarget); err != nil || got != externalSessionID {
		t.Fatalf("create Windows tool Session=%q err=%v", got, err)
	}
	selected, err := adapter.SelectModel(testCtx, externalSessionID, agent.ModelSelection{Provider: providerID, Model: modelID})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Provider != providerID || selected.Model != modelID {
		t.Fatalf("selected model=%+v", selected)
	}

	invoker := &pinnedWindowsInvoker{}
	sink := &recordingSink{}
	request := agent.RunRequest{
		SessionID: "ags_pinned_windows_turn", ExternalSessionID: externalSessionID,
		UserText:   "Read the current directory on the selected Windows device, then report completion.",
		RemoteExec: invoker, Target: windowsTarget,
	}
	if err := adapter.PreflightTurn(testCtx, request); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Run(testCtx, request, sink); err != nil {
		t.Fatal(err)
	}

	_, text, external, _ := sink.snapshot()
	if external != "" {
		t.Fatalf("existing external Session was unexpectedly replaced with %q", external)
	}
	if text != "WINDOWS_DSH_CHAIN_OK" {
		t.Fatalf("streamed text=%q", text)
	}
	if err := invoker.assertSingleCall(); err != nil {
		t.Fatal(err)
	}
	if err := provider.assertComplete(); err != nil {
		t.Fatal(err)
	}
}

type pinnedWindowsInvoker struct {
	mu    sync.Mutex
	calls []agent.RemoteExecArguments
}

func (invoker *pinnedWindowsInvoker) Invoke(_ context.Context, request agent.ToolRequest) (agent.ToolResult, error) {
	if request.Name != agent.ToolRemoteExec {
		return agent.ToolResult{}, fmt.Errorf("unexpected tool %q", request.Name)
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Arguments))
	decoder.DisallowUnknownFields()
	var arguments agent.RemoteExecArguments
	if err := decoder.Decode(&arguments); err != nil {
		return agent.ToolResult{}, fmt.Errorf("decode remote_exec arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return agent.ToolResult{}, errors.New("remote_exec arguments contain trailing data")
	}
	invoker.mu.Lock()
	invoker.calls = append(invoker.calls, arguments)
	invoker.mu.Unlock()
	return agent.ToolResult{
		ToolCallID: "tool_contract", Stdout: "AIS_CWD=C:\\Users\\Alice\nAIS_POWERSHELL=5.1\n", ExitCode: 0,
	}, nil
}

func (invoker *pinnedWindowsInvoker) assertSingleCall() error {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	if len(invoker.calls) != 1 {
		return fmt.Errorf("remote_exec call count=%d", len(invoker.calls))
	}
	call := invoker.calls[0]
	if call.Command != "[Console]::Out.WriteLine((Get-Location).Path)" {
		return fmt.Errorf("remote_exec command=%q", call.Command)
	}
	if call.CWD != "" {
		return fmt.Errorf("remote_exec default cwd was not omitted: %q", call.CWD)
	}
	if call.TimeoutMS != 30000 {
		return fmt.Errorf("remote_exec timeout_ms=%d", call.TimeoutMS)
	}
	return nil
}

type pinnedProviderRequest struct {
	Authorization string
	Path          string
	Body          map[string]any
}

type pinnedProviderContract struct {
	mu             sync.Mutex
	expectedAPIKey string
	requests       []pinnedProviderRequest
	failures       []string
}

func (provider *pinnedProviderContract) setExpectedAPIKey(value string) {
	provider.mu.Lock()
	provider.expectedAPIKey = value
	provider.mu.Unlock()
}

func (provider *pinnedProviderContract) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 2*1024*1024+1))
	if err != nil || len(body) == 0 || len(body) > 2*1024*1024 {
		provider.reject(response, "invalid request body")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		provider.reject(response, "request body is not JSON")
		return
	}

	provider.mu.Lock()
	provider.requests = append(provider.requests, pinnedProviderRequest{
		Authorization: request.Header.Get("Authorization"), Path: request.URL.Path, Body: payload,
	})
	step := len(provider.requests)
	expectedAPIKey := provider.expectedAPIKey
	provider.mu.Unlock()

	if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
		provider.reject(response, "unexpected provider endpoint")
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+expectedAPIKey {
		provider.reject(response, "unexpected provider authorization")
		return
	}
	if payload["model"] != "contract-model" || payload["stream"] != true {
		provider.reject(response, "unexpected provider request options")
		return
	}

	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	response.WriteHeader(http.StatusOK)
	switch step {
	case 1:
		if _, ok := payload["tools"]; !ok {
			provider.recordFailure("initial request did not advertise tools")
		}
		arguments := `{"command":"[Console]::Out.WriteLine((Get-Location).Path)","description":"Read the Windows working directory","timeoutMs":30000}`
		midpoint := len(arguments) / 2
		provider.writeSSE(response, map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_contract", "type": "function",
				"function": map[string]any{"name": "bash", "arguments": arguments[:midpoint]},
			}}}, "finish_reason": nil,
		}}})
		provider.writeSSE(response, map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": arguments[midpoint:]},
			}}}, "finish_reason": nil,
		}}})
		provider.writeSSE(response, pinnedTerminalChunk("tool_calls", 2))
	case 2:
		if !containsToolResult(payload["messages"], "AIS_POWERSHELL=5.1") {
			provider.recordFailure("follow-up request did not contain the remote tool result")
		}
		provider.writeSSE(response, map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"content": "WINDOWS_DSH_CHAIN_OK"}, "finish_reason": nil,
		}}})
		provider.writeSSE(response, pinnedTerminalChunk("stop", 20))
	default:
		provider.recordFailure(fmt.Sprintf("unexpected provider request %d", step))
	}
	provider.writeSSE(response, "[DONE]")
}

func (provider *pinnedProviderContract) reject(response http.ResponseWriter, message string) {
	provider.recordFailure(message)
	http.Error(response, `{"error":{"message":"contract rejected","type":"invalid_request_error"}}`, http.StatusBadRequest)
}

func (provider *pinnedProviderContract) recordFailure(message string) {
	provider.mu.Lock()
	provider.failures = append(provider.failures, message)
	provider.mu.Unlock()
}

func (provider *pinnedProviderContract) writeSSE(response http.ResponseWriter, payload any) {
	var encoded []byte
	if value, ok := payload.(string); ok {
		encoded = []byte(value)
	} else {
		var err error
		encoded, err = json.Marshal(payload)
		if err != nil {
			provider.recordFailure("encode provider response")
			return
		}
	}
	_, _ = response.Write(append(append([]byte("data: "), encoded...), '\n', '\n'))
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (provider *pinnedProviderContract) assertComplete() error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.failures) != 0 {
		return fmt.Errorf("provider contract failures: %s", strings.Join(provider.failures, "; "))
	}
	if len(provider.requests) != 2 {
		return fmt.Errorf("provider request count=%d", len(provider.requests))
	}
	return nil
}

func pinnedTerminalChunk(reason string, outputTokens int) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"content": ""}, "finish_reason": reason,
		}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": outputTokens},
	}
}

func containsToolResult(value any, marker string) bool {
	encoded, err := json.Marshal(value)
	return err == nil && strings.Contains(string(encoded), marker)
}

func unusedLoopbackOrigin(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + address
}
