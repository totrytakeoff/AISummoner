package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const testAPIKey = "test-deepseek-api-key"

type recordingSink struct {
	reasoning string
	text      string
	states    []string
}

func (sink *recordingSink) ReasoningDelta(_ context.Context, delta string) error {
	sink.reasoning += delta
	return nil
}

func (sink *recordingSink) TextDelta(_ context.Context, delta string) error {
	sink.text += delta
	return nil
}

func (sink *recordingSink) ProviderState(_ context.Context, state string) error {
	sink.states = append(sink.states, state)
	return nil
}

func (*recordingSink) SetExternalSessionID(context.Context, string) error { return nil }

type rejectingSink struct {
	recordingSink
	reasoningError error
	textError      error
}

func (sink *rejectingSink) ReasoningDelta(ctx context.Context, delta string) error {
	if sink.reasoningError != nil {
		return sink.reasoningError
	}
	return sink.recordingSink.ReasoningDelta(ctx, delta)
}

func (sink *rejectingSink) TextDelta(ctx context.Context, delta string) error {
	if sink.textError != nil {
		return sink.textError
	}
	return sink.recordingSink.TextDelta(ctx, delta)
}

type invokerFunc func(context.Context, agent.ToolRequest) (agent.ToolResult, error)

func (function invokerFunc) Invoke(ctx context.Context, request agent.ToolRequest) (agent.ToolResult, error) {
	return function(ctx, request)
}

func unusedInvoker(t *testing.T) agent.RemoteExecInvoker {
	t.Helper()
	return invokerFunc(func(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
		t.Fatal("unexpected remote tool invocation")
		return agent.ToolResult{}, nil
	})
}

func newTestAdapter(t *testing.T, server *httptest.Server, options ...func(*Options)) *Adapter {
	t.Helper()
	configuration := Options{
		BaseURL: server.URL, APIKey: testAPIKey, ModelID: "deepseek-v4-flash",
		HTTPClient: server.Client(), StreamIdleTimeout: time.Second,
	}
	for _, apply := range options {
		apply(&configuration)
	}
	adapter, err := NewAdapter(configuration)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func writeSSE(t *testing.T, writer http.ResponseWriter, records ...any) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for _, record := range records {
		if text, ok := record.(string); ok {
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", text)
			continue
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	}
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{
		"id": "response-id", "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

func requireAdapterCode(t *testing.T, err error, code string) {
	t.Helper()
	var adapterError *agent.AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != code {
		t.Fatalf("error = %v, want AdapterError code %q", err, code)
	}
}

func TestAdapterStreamsReasoningAndFinalText(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("provider authorization missing")
		}
		var payload chatRequest
		var encoded map[string]json.RawMessage
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&encoded); err != nil {
			t.Fatal(err)
		}
		serialized, err := json.Marshal(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(serialized, &payload); err != nil {
			t.Fatal(err)
		}
		if _, present := encoded["tool_choice"]; present {
			t.Error("thinking-mode request must not include tool_choice")
		}
		if payload.Model != "deepseek-v4-flash" || !payload.Stream || !payload.StreamOptions.IncludeUsage || payload.Thinking.Type != "enabled" || payload.ReasoningEffort != "high" {
			t.Errorf("unexpected request controls: %#v", payload)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].Function.Name != agent.ToolRemoteExec {
			t.Fatalf("tools = %#v", payload.Tools)
		}
		writeSSE(t, writer,
			chunk(map[string]any{"reasoning_content": "inspect "}, nil),
			chunk(map[string]any{"reasoning_content": "request"}, nil),
			chunk(map[string]any{"content": "Final "}, nil),
			chunk(map[string]any{"content": "answer."}, "stop"),
			"[DONE]",
		)
	}))
	defer server.Close()

	sink := &recordingSink{}
	err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
		SessionID: "ags_direct", UserText: "hello", RemoteExec: unusedInvoker(t),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if sink.reasoning != "inspect request" || sink.text != "Final answer." {
		t.Fatalf("reasoning=%q text=%q", sink.reasoning, sink.text)
	}
}

func TestAdapterToolLoopEchoesRequiredReasoningAndResult(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := requests.Add(1)
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if call == 1 {
			if len(payload.Messages) != 4 || payload.Messages[1].Role != "user" || payload.Messages[2].Role != "assistant" || payload.Messages[3].Content != "inspect the remote" {
				t.Fatalf("initial history = %#v", payload.Messages)
			}
			index := 0
			writeSSE(t, writer,
				chunk(map[string]any{"reasoning_content": "Need remote evidence."}, nil),
				chunk(map[string]any{"content": "Checking. "}, nil),
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "id": "provider-call-1", "type": "function",
					"function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"host`},
				}}}, nil),
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "function": map[string]any{"arguments": `name","timeout_ms":30000}`},
				}}}, "tool_calls"),
				"[DONE]",
			)
			return
		}
		if call != 2 {
			t.Fatalf("provider requests = %d", call)
		}
		if len(payload.Messages) != 6 {
			t.Fatalf("tool continuation messages = %d", len(payload.Messages))
		}
		assistantMessage := payload.Messages[4]
		if assistantMessage.Role != "assistant" || assistantMessage.ReasoningContent != "Need remote evidence." || len(assistantMessage.ToolCalls) != 1 || assistantMessage.ToolCalls[0].ID != "provider-call-1" {
			t.Fatalf("assistant tool context = %#v", assistantMessage)
		}
		toolMessage := payload.Messages[5]
		toolContent, ok := toolMessage.Content.(string)
		if !ok || toolMessage.Role != "tool" || toolMessage.ToolCallID != "provider-call-1" || !strings.Contains(toolContent, "remote-host") {
			t.Fatalf("tool result context = %#v", toolMessage)
		}
		writeSSE(t, writer,
			chunk(map[string]any{"reasoning_content": "Evidence received."}, nil),
			chunk(map[string]any{"content": "Remote hostname is remote-host."}, "stop"),
			"[DONE]",
		)
	}))
	defer server.Close()

	var invocations atomic.Int32
	sink := &recordingSink{}
	err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
		SessionID: "ags_tool", UserText: "inspect the remote",
		History: []agent.ConversationMessage{
			{Role: "user", Content: "previous question"},
			{Role: "assistant", Content: "previous answer"},
			{Role: "user", Content: "inspect the remote"},
		},
		RemoteExec: invokerFunc(func(_ context.Context, request agent.ToolRequest) (agent.ToolResult, error) {
			invocations.Add(1)
			if request.Name != agent.ToolRemoteExec || string(request.Arguments) != `{"command":"hostname","timeout_ms":30000}` {
				t.Fatalf("tool request = %s %s", request.Name, request.Arguments)
			}
			return agent.ToolResult{Stdout: "remote-host\n", ExitCode: 0}, nil
		}),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || invocations.Load() != 1 {
		t.Fatalf("requests=%d invocations=%d", requests.Load(), invocations.Load())
	}
	if sink.reasoning != "Need remote evidence.Evidence received." || sink.text != "Checking. Remote hostname is remote-host." {
		t.Fatalf("reasoning=%q text=%q", sink.reasoning, sink.text)
	}
	if len(sink.states) != 3 || sink.states[0] != "streaming" || sink.states[1] != "tool_calls" || sink.states[2] != "streaming" {
		t.Fatalf("provider states = %v", sink.states)
	}
}

func TestAdapterExecutesMultipleToolCallsInOrderAndReturnsFailuresToProvider(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := requests.Add(1)
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if call == 1 {
			firstIndex, secondIndex := 0, 1
			writeSSE(t, writer,
				chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": firstIndex, "id": "provider-call-denied", "type": "function",
					"function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"hostname"}`},
				}, map[string]any{
					"index": secondIndex, "id": "provider-call-failed", "type": "function",
					"function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"uname -a"}`},
				}}}, "tool_calls"),
				"[DONE]",
			)
			return
		}
		if call != 2 || len(payload.Messages) != 5 {
			t.Fatalf("provider request %d messages = %d", call, len(payload.Messages))
		}
		if assistantContent, ok := payload.Messages[2].Content.(string); !ok || assistantContent != "" {
			t.Fatalf("tool-call assistant content = %#v, want empty string", payload.Messages[2].Content)
		}
		var denied, failed toolResultMessage
		for index, destination := range []*toolResultMessage{&denied, &failed} {
			message := payload.Messages[index+3]
			encoded, ok := message.Content.(string)
			if !ok || message.Role != "tool" {
				t.Fatalf("tool message %d = %#v", index, message)
			}
			if err := json.Unmarshal([]byte(encoded), destination); err != nil {
				t.Fatal(err)
			}
		}
		if !denied.Denied || denied.Failure == nil || denied.Failure.Code != agent.FailureCommandDenied {
			t.Fatalf("denied tool result = %#v", denied)
		}
		if failed.Failure == nil || failed.Failure.Code != agent.FailureExecTransport {
			t.Fatalf("failed tool result = %#v", failed)
		}
		writeSSE(t, writer,
			chunk(map[string]any{"content": "Both tool results were handled safely."}, "stop"),
			"[DONE]",
		)
	}))
	defer server.Close()

	commands := make([]string, 0, 2)
	sink := &recordingSink{}
	err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
		SessionID: "ags_multiple", UserText: "inspect safely",
		RemoteExec: invokerFunc(func(_ context.Context, request agent.ToolRequest) (agent.ToolResult, error) {
			var arguments agent.RemoteExecArguments
			if err := json.Unmarshal(request.Arguments, &arguments); err != nil {
				t.Fatal(err)
			}
			commands = append(commands, arguments.Command)
			if arguments.Command == "hostname" {
				return agent.ToolResult{Denied: true, Failure: &agent.ToolFailure{Code: agent.FailureCommandDenied, Message: "Command was denied."}}, nil
			}
			return agent.ToolResult{Failure: &agent.ToolFailure{Code: agent.FailureExecTransport, Message: "Remote command failed."}}, nil
		}),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(commands, ",") != "hostname,uname -a" || sink.text != "Both tool results were handled safely." {
		t.Fatalf("commands=%v text=%q", commands, sink.text)
	}
}

func TestAdapterContinuesBeyondLegacyTwelveToolCallsUntilFinalAnswer(t *testing.T) {
	const toolSteps = 20
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := int(requests.Add(1))
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if call <= toolSteps {
			index := 0
			writeSSE(t, writer, chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": index, "id": fmt.Sprintf("provider-call-%d", call), "type": "function",
				"function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"hostname"}`},
			}}}, "tool_calls"), "[DONE]")
			return
		}
		if call != toolSteps+1 {
			t.Fatalf("provider requests = %d", call)
		}
		writeSSE(t, writer, chunk(map[string]any{"content": "Finished after all remote steps."}, "stop"), "[DONE]")
	}))
	defer server.Close()

	var invocations atomic.Int32
	sink := &recordingSink{}
	err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
		SessionID: "ags_many_tools", UserText: "complete a multi-step task",
		RemoteExec: invokerFunc(func(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
			invocations.Add(1)
			return agent.ToolResult{Stdout: "remote-host\n", ExitCode: 0}, nil
		}),
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != toolSteps+1 || invocations.Load() != toolSteps || sink.text != "Finished after all remote steps." {
		t.Fatalf("requests=%d invocations=%d text=%q", requests.Load(), invocations.Load(), sink.text)
	}
}

func TestAdapterStillBoundsToolCallsInOneProviderReply(t *testing.T) {
	calls := make([]any, 0, maximumToolCallsPerReply+1)
	for index := 0; index <= maximumToolCallsPerReply; index++ {
		calls = append(calls, map[string]any{
			"index": index, "id": fmt.Sprintf("provider-call-%d", index), "type": "function",
			"function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"hostname"}`},
		})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(t, writer, chunk(map[string]any{"tool_calls": calls}, "tool_calls"), "[DONE]")
	}))
	defer server.Close()
	err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
		SessionID: "ags_reply_bound", UserText: "inspect", RemoteExec: unusedInvoker(t),
	}, &recordingSink{})
	requireAdapterCode(t, err, "protocol_error")
}

func TestAdapterRejectsInvalidOrAmbiguousProviderToolCalls(t *testing.T) {
	tests := []struct {
		name    string
		calls   []any
		invoker agent.RemoteExecInvoker
	}{
		{
			name: "duplicate provider identity",
			calls: []any{
				map[string]any{"index": 0, "id": "duplicate-call", "type": "function", "function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"hostname"}`}},
				map[string]any{"index": 1, "id": "duplicate-call", "type": "function", "function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"uname -a"}`}},
			},
		},
		{
			name: "authoritative invoker rejects arguments",
			calls: []any{
				map[string]any{"index": 0, "id": "invalid-arguments", "type": "function", "function": map[string]any{"name": agent.ToolRemoteExec, "arguments": `{"command":"hostname","unexpected":true}`}},
			},
			invoker: invokerFunc(func(context.Context, agent.ToolRequest) (agent.ToolResult, error) {
				return agent.ToolResult{}, agent.ErrInvalidTool
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(t, writer, chunk(map[string]any{"tool_calls": test.calls}, "tool_calls"), "[DONE]")
			}))
			defer server.Close()
			invoker := test.invoker
			if invoker == nil {
				invoker = unusedInvoker(t)
			}
			err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
				SessionID: "ags_invalid_tool", UserText: "inspect", RemoteExec: invoker,
			}, &recordingSink{})
			requireAdapterCode(t, err, "protocol_error")
		})
	}
}

func TestAdapterHTTPAndFinishFailureMatrix(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		records     []any
		wantCode    string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "rate limited", status: http.StatusTooManyRequests, wantCode: "rate_limited"},
		{name: "provider unavailable", status: http.StatusServiceUnavailable, wantCode: "provider_unavailable"},
		{name: "rejected", status: http.StatusBadRequest, wantCode: "provider_rejected"},
		{name: "wrong media", status: http.StatusOK, contentType: "application/json", wantCode: "protocol_error"},
		{name: "empty stop", status: http.StatusOK, records: []any{chunk(map[string]any{}, "stop"), "[DONE]"}, wantCode: "protocol_error"},
		{name: "length", status: http.StatusOK, records: []any{chunk(map[string]any{"content": "partial"}, "length"), "[DONE]"}, wantCode: "protocol_error"},
		{name: "resource", status: http.StatusOK, records: []any{chunk(map[string]any{}, "insufficient_system_resource"), "[DONE]"}, wantCode: "provider_unavailable"},
		{name: "missing done", status: http.StatusOK, records: []any{chunk(map[string]any{"content": "answer"}, "stop")}, wantCode: "protocol_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				status := test.status
				if status == 0 {
					status = http.StatusOK
				}
				if status != http.StatusOK {
					writer.WriteHeader(status)
					_, _ = io.WriteString(writer, "provider-detail-must-not-be-read")
					return
				}
				if test.contentType != "" {
					writer.Header().Set("Content-Type", test.contentType)
					writer.WriteHeader(status)
					return
				}
				writeSSE(t, writer, test.records...)
			}))
			defer server.Close()
			err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
				SessionID: "ags_failure", UserText: "hello", RemoteExec: unusedInvoker(t),
			}, &recordingSink{})
			requireAdapterCode(t, err, test.wantCode)
			if strings.Contains(err.Error(), "provider-detail") || strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("error leaked provider material: %v", err)
			}
		})
	}
}

func TestAdapterRejectsRedirectWithoutMutatingCallerClient(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	caller := origin.Client()
	caller.Timeout = 137 * time.Millisecond
	originalRedirect := func(*http.Request, []*http.Request) error { return errors.New("caller redirect sentinel") }
	caller.CheckRedirect = originalRedirect
	adapter := newTestAdapter(t, origin, func(options *Options) { options.HTTPClient = caller })
	err := adapter.Run(context.Background(), agent.RunRequest{
		SessionID: "ags_redirect", UserText: "hello", RemoteExec: unusedInvoker(t),
	}, &recordingSink{})
	requireAdapterCode(t, err, "protocol_error")
	if targetRequests.Load() != 0 {
		t.Fatalf("redirect target requests = %d", targetRequests.Load())
	}
	if err := caller.CheckRedirect(nil, nil); err == nil || err.Error() != "caller redirect sentinel" {
		t.Fatalf("caller redirect policy mutated: %v", err)
	}
	if adapter.client.Timeout != caller.Timeout {
		t.Fatalf("cloned client timeout = %s, want %s", adapter.client.Timeout, caller.Timeout)
	}
}

func TestAdapterStreamIdleTimeoutIsBounded(t *testing.T) {
	handlerDone := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(handlerDone)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()
	adapter := newTestAdapter(t, server, func(options *Options) { options.StreamIdleTimeout = 20 * time.Millisecond })
	started := time.Now()
	err := adapter.Run(context.Background(), agent.RunRequest{
		SessionID: "ags_idle", UserText: "hello", RemoteExec: unusedInvoker(t),
	}, &recordingSink{})
	requireAdapterCode(t, err, "provider_unavailable")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("idle timeout elapsed = %s", elapsed)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("provider handler did not observe cancellation")
	}
}

func TestAdapterCancellationStopsProviderRequestAndJoins(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(handlerStarted)
		defer close(handlerDone)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	errorResult := make(chan error, 1)
	adapter := newTestAdapter(t, server)
	invoker := unusedInvoker(t)
	go func() {
		errorResult <- adapter.Run(ctx, agent.RunRequest{
			SessionID: "ags_cancel", UserText: "hello", RemoteExec: invoker,
		}, &recordingSink{})
	}()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	select {
	case err := <-errorResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Adapter did not return after cancellation")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("provider handler did not observe cancellation")
	}
}

func TestAdapterClassifiesNonContextSinkRejectionAsProtocolFailure(t *testing.T) {
	tests := []struct {
		name      string
		delta     map[string]any
		configure func(*rejectingSink)
	}{
		{name: "reasoning", delta: map[string]any{"reasoning_content": "private reasoning"}, configure: func(sink *rejectingSink) { sink.reasoningError = errors.New("sink detail") }},
		{name: "answer", delta: map[string]any{"content": "answer"}, configure: func(sink *rejectingSink) { sink.textError = errors.New("sink detail") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(t, writer, chunk(test.delta, "stop"), "[DONE]")
			}))
			defer server.Close()
			sink := &rejectingSink{}
			test.configure(sink)
			err := newTestAdapter(t, server).Run(context.Background(), agent.RunRequest{
				SessionID: "ags_sink", UserText: "hello", RemoteExec: unusedInvoker(t),
			}, sink)
			requireAdapterCode(t, err, "protocol_error")
			if strings.Contains(err.Error(), "sink detail") {
				t.Fatalf("sink detail escaped safe Adapter error: %v", err)
			}
		})
	}
}

func TestAdapterRejectsOversizedSSERecordAndHistory(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", strings.Repeat("x", maximumSSERecordBytes+1))
	}))
	defer server.Close()
	adapter := newTestAdapter(t, server)
	err := adapter.Run(context.Background(), agent.RunRequest{
		SessionID: "ags_large", UserText: "hello", RemoteExec: unusedInvoker(t),
	}, &recordingSink{})
	requireAdapterCode(t, err, "protocol_error")

	history := make([]agent.ConversationMessage, agent.MaxConversationHistoryMessages+1)
	for index := range history {
		history[index] = agent.ConversationMessage{Role: "user", Content: "bounded"}
	}
	err = adapter.Run(context.Background(), agent.RunRequest{
		SessionID: "ags_history", UserText: "hello", History: history, RemoteExec: unusedInvoker(t),
	}, &recordingSink{})
	requireAdapterCode(t, err, "protocol_error")
}

func TestNewAdapterRejectsUnsafeConfiguration(t *testing.T) {
	tests := []Options{
		{BaseURL: "http://provider.invalid", APIKey: testAPIKey, ModelID: "model"},
		{BaseURL: "https://provider.invalid/path", APIKey: testAPIKey, ModelID: "model"},
		{BaseURL: "https://user@provider.invalid", APIKey: testAPIKey, ModelID: "model"},
		{BaseURL: "https://provider.invalid", APIKey: "", ModelID: "model"},
		{BaseURL: "https://provider.invalid", APIKey: testAPIKey + "\n", ModelID: "model"},
		{BaseURL: "https://provider.invalid", APIKey: "test\nkey", ModelID: "model"},
		{BaseURL: "https://provider.invalid", APIKey: testAPIKey, ModelID: " bad model"},
		{BaseURL: "https://provider.invalid", APIKey: testAPIKey, ModelID: "model\tvariant"},
	}
	for index, options := range tests {
		if _, err := NewAdapter(options); err == nil {
			t.Fatalf("unsafe configuration %d accepted", index)
		}
	}
}
