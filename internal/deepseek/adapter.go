// Package deepseek implements a direct DeepSeek Chat-Completions Agent
// Adapter. It has no local execution authority: the only exposed tool invokes
// the AISummoner RemoteExec boundary supplied for the selected Device.
package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const (
	ProviderName   = agent.ProviderDeepSeek
	DefaultBaseURL = "https://api.deepseek.com"
)

const (
	maximumModelIDBytes      = 256
	maximumAPIKeyBytes       = 4096
	maximumRequestBytes      = 4 * 1024 * 1024
	maximumSSERecordBytes    = 256 * 1024
	maximumSSELineBytes      = maximumSSERecordBytes + len("data: ")
	maximumStreamRecords     = 4096
	maximumToolCallsPerReply = 64
	maximumToolArgumentBytes = 128 * 1024
	defaultStreamIdleTimeout = 60 * time.Second
	defaultMaximumTokens     = 8192
)

var errStreamIdle = errors.New("DeepSeek stream idle timeout")

const systemPrompt = `You are an agent operating on one selected remote device. Use remote_exec for every device fact or action; never claim that a command ran unless its tool result proves it. The server may require the user to approve a command. Keep the final answer concise and distinguish observed command output from inference.`

// Options configures a direct provider Adapter. APIKey is secret and must
// never be logged or returned in errors.
type Options struct {
	BaseURL           string
	APIKey            string
	ModelID           string
	HTTPClient        *http.Client
	StreamIdleTimeout time.Duration
}

type Adapter struct {
	endpoint          *url.URL
	apiKey            string
	modelID           string
	client            *http.Client
	streamIdleTimeout time.Duration
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []wireMessage `json:"messages"`
	Tools           []wireTool    `json:"tools"`
	Stream          bool          `json:"stream"`
	StreamOptions   streamOptions `json:"stream_options"`
	MaximumTokens   int           `json:"max_tokens"`
	Thinking        thinkingMode  `json:"thinking"`
	ReasoningEffort string        `json:"reasoning_effort"`
}

type thinkingMode struct {
	Type string `json:"type"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content          *string               `json:"content"`
	ReasoningContent *string               `json:"reasoning_content"`
	ToolCalls        []streamToolCallDelta `json:"tool_calls"`
}

type streamToolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type streamResult struct {
	content    string
	reasoning  string
	finish     string
	toolCalls  []wireToolCall
	doneMarker bool
}

type toolResultMessage struct {
	Stdout    string             `json:"stdout"`
	Stderr    string             `json:"stderr"`
	ExitCode  int                `json:"exit_code"`
	Truncated bool               `json:"truncated"`
	Denied    bool               `json:"denied"`
	Failure   *agent.ToolFailure `json:"failure,omitempty"`
}

// NewAdapter validates an HTTPS provider origin and clones the supplied HTTP
// client before installing a no-redirect credential boundary.
func NewAdapter(options Options) (*Adapter, error) {
	endpoint, err := providerEndpoint(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if !validVisibleASCII(options.APIKey, maximumAPIKeyBytes) {
		return nil, errors.New("DeepSeek API key is required")
	}
	if !validModelID(options.ModelID) {
		return nil, errors.New("DeepSeek model is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	idleTimeout := options.StreamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultStreamIdleTimeout
	}
	return &Adapter{
		endpoint: endpoint, apiKey: options.APIKey, modelID: options.ModelID,
		client: client, streamIdleTimeout: idleTimeout,
	}, nil
}

func providerEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("DeepSeek base URL must be an HTTPS origin")
	}
	parsed.Path = "/chat/completions"
	return parsed, nil
}

func validModelID(value string) bool {
	return validVisibleASCII(value, maximumModelIDBytes)
}

func validVisibleASCII(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func remoteExecTool() wireTool {
	return wireTool{
		Type: "function",
		Function: wireToolFunction{
			Name:        agent.ToolRemoteExec,
			Description: "Execute a command on the selected remote device and return bounded stdout, stderr, exit status, and failure information.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":    map[string]any{"type": "string", "description": "Command to execute on the remote device."},
					"cwd":        map[string]any{"type": "string", "description": "Optional absolute working directory on the remote device."},
					"timeout_ms": map[string]any{"type": "integer", "minimum": int(agent.MinimumExecTimeout / time.Millisecond), "maximum": int(agent.MaximumExecTimeout / time.Millisecond)},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}

// Run reconstructs bounded Server-owned dialogue and drives DeepSeek tool
// steps through the supplied RemoteExec invoker until a final answer arrives.
func (adapter *Adapter) Run(ctx context.Context, request agent.RunRequest, sink agent.EventSink) error {
	if request.SessionID == "" || request.RemoteExec == nil || sink == nil || !validUserText(request.UserText) {
		return protocolError("invalid DeepSeek run request")
	}
	messages, err := conversationMessages(request)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sink.ProviderState(ctx, "streaming"); err != nil {
			return err
		}
		result, err := adapter.complete(ctx, messages, sink)
		if err != nil {
			return err
		}
		assistantMessage := wireMessage{
			Role: "assistant", Content: result.content, ReasoningContent: result.reasoning,
			ToolCalls: result.toolCalls,
		}
		messages = append(messages, assistantMessage)
		switch result.finish {
		case "stop":
			if len(result.toolCalls) != 0 || strings.TrimSpace(result.content) == "" {
				return protocolError("DeepSeek stopped without a final answer")
			}
			return nil
		case "tool_calls":
			if len(result.toolCalls) == 0 {
				return protocolError("DeepSeek declared tool calls without a tool")
			}
			if err := sink.ProviderState(ctx, "tool_calls"); err != nil {
				return err
			}
			for _, call := range result.toolCalls {
				toolResult, invokeErr := request.RemoteExec.Invoke(ctx, agent.ToolRequest{
					Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
				})
				if invokeErr != nil {
					if errors.Is(invokeErr, agent.ErrInvalidTool) {
						return protocolError("DeepSeek returned invalid remote tool arguments")
					}
					return invokeErr
				}
				encoded, encodeErr := json.Marshal(toolResultMessage{
					Stdout: toolResult.Stdout, Stderr: toolResult.Stderr, ExitCode: toolResult.ExitCode,
					Truncated: toolResult.Truncated, Denied: toolResult.Denied, Failure: toolResult.Failure,
				})
				if encodeErr != nil {
					return protocolError("encode remote tool result")
				}
				messages = append(messages, wireMessage{Role: "tool", Content: string(encoded), ToolCallID: call.ID})
			}
		case "insufficient_system_resource":
			return providerError("provider_unavailable", "DeepSeek lacked inference capacity")
		case "length", "content_filter":
			return protocolError("DeepSeek returned an incomplete answer")
		default:
			return protocolError("DeepSeek returned an unknown finish reason")
		}
	}
}

func validUserText(value string) bool {
	return value != "" && len([]byte(value)) <= agent.MaxMessageBytes && utf8.ValidString(value) && strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

func conversationMessages(request agent.RunRequest) ([]wireMessage, error) {
	messages := []wireMessage{{Role: "system", Content: systemPrompt}}
	totalBytes := 0
	for _, message := range request.History {
		if (message.Role != "user" && message.Role != "assistant") || !validUserText(message.Content) {
			return nil, protocolError("invalid DeepSeek conversation history")
		}
		totalBytes += len([]byte(message.Content))
		if len(messages) > agent.MaxConversationHistoryMessages || totalBytes > agent.MaxConversationHistoryBytes {
			return nil, protocolError("DeepSeek conversation history exceeds limit")
		}
		messages = append(messages, wireMessage{Role: message.Role, Content: message.Content})
	}
	if len(request.History) == 0 || request.History[len(request.History)-1].Role != "user" || request.History[len(request.History)-1].Content != request.UserText {
		messages = append(messages, wireMessage{Role: "user", Content: request.UserText})
	}
	return messages, nil
}

func (adapter *Adapter) complete(ctx context.Context, messages []wireMessage, sink agent.EventSink) (streamResult, error) {
	payload := chatRequest{
		Model: adapter.modelID, Messages: messages, Tools: []wireTool{remoteExecTool()},
		Stream: true, StreamOptions: streamOptions{IncludeUsage: true}, MaximumTokens: defaultMaximumTokens,
		Thinking: thinkingMode{Type: "enabled"}, ReasoningEffort: "high",
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > maximumRequestBytes {
		return streamResult{}, protocolError("DeepSeek request exceeds limit")
	}
	streamCtx, cancelStream := context.WithCancelCause(ctx)
	activity := make(chan struct{}, 1)
	stopWatchdog := make(chan struct{})
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		timer := time.NewTimer(adapter.streamIdleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(adapter.streamIdleTimeout)
			case <-timer.C:
				cancelStream(errStreamIdle)
				return
			case <-stopWatchdog:
				return
			}
		}
	}()
	defer func() {
		close(stopWatchdog)
		<-watchdogDone
		cancelStream(nil)
	}()
	pulse := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}

	httpRequest, err := http.NewRequestWithContext(streamCtx, http.MethodPost, adapter.endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return streamResult{}, protocolError("build DeepSeek request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+adapter.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("User-Agent", "AISummoner/0")
	response, err := adapter.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		if errors.Is(context.Cause(streamCtx), errStreamIdle) {
			return streamResult{}, providerError("provider_unavailable", "DeepSeek stream timed out")
		}
		return streamResult{}, providerError("provider_unavailable", "DeepSeek transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return streamResult{}, classifyHTTPStatus(response.StatusCode)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return streamResult{}, protocolError("DeepSeek response is not SSE")
	}
	result, err := consumeStream(streamCtx, response.Body, sink, pulse)
	if err != nil {
		if ctx.Err() != nil {
			return streamResult{}, ctx.Err()
		}
		if errors.Is(context.Cause(streamCtx), errStreamIdle) {
			return streamResult{}, providerError("provider_unavailable", "DeepSeek stream timed out")
		}
		return streamResult{}, err
	}
	return result, nil
}

func consumeStream(ctx context.Context, reader io.Reader, sink agent.EventSink, pulse func()) (streamResult, error) {
	var result streamResult
	toolCalls := make(map[int]*wireToolCall)
	finishSeen := false
	records := 0
	oversizedLine := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maximumSSELineBytes+2)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline > maximumSSELineBytes {
			oversizedLine = true
			return newline + 1, []byte{}, nil
		}
		if len(data) > maximumSSELineBytes {
			oversizedLine = true
			return len(data), []byte{}, nil
		}
		return bufio.ScanLines(data, atEOF)
	})
	var record bytes.Buffer
	consumeRecord := func(encoded []byte) error {
		if bytes.Equal(encoded, []byte("[DONE]")) {
			result.doneMarker = true
			return nil
		}
		records++
		if records > maximumStreamRecords {
			return protocolError("too many DeepSeek stream records")
		}
		var chunk streamChunk
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		if err := decoder.Decode(&chunk); err != nil {
			return protocolError("malformed DeepSeek stream record")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
			return protocolError("malformed DeepSeek stream record")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return protocolError("unexpected DeepSeek stream choice")
			}
			if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
				delta := *choice.Delta.ReasoningContent
				if !utf8.ValidString(delta) || len(result.reasoning) > agent.MaxMessageBytes-len(delta) {
					return protocolError("DeepSeek reasoning exceeds limit")
				}
				if err := sink.ReasoningDelta(ctx, delta); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return protocolError("DeepSeek reasoning was rejected")
				}
				result.reasoning += delta
			}
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				delta := *choice.Delta.Content
				if !utf8.ValidString(delta) || len(result.content) > agent.MaxMessageBytes-len(delta) {
					return protocolError("DeepSeek answer exceeds limit")
				}
				if err := sink.TextDelta(ctx, delta); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return protocolError("DeepSeek answer was rejected")
				}
				result.content += delta
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index == nil || *delta.Index < 0 || *delta.Index >= maximumToolCallsPerReply {
					return protocolError("invalid DeepSeek tool-call index")
				}
				call := toolCalls[*delta.Index]
				if call == nil {
					if len(toolCalls) >= maximumToolCallsPerReply {
						return protocolError("too many DeepSeek tool calls")
					}
					call = &wireToolCall{Type: "function"}
					toolCalls[*delta.Index] = call
				}
				if delta.ID != "" {
					if call.ID != "" && call.ID != delta.ID {
						return protocolError("DeepSeek tool-call identity changed")
					}
					call.ID = delta.ID
				}
				if delta.Type != "" && delta.Type != "function" {
					return protocolError("unsupported DeepSeek tool-call type")
				}
				if delta.Function.Name != "" {
					if call.Function.Name != "" && call.Function.Name != delta.Function.Name {
						return protocolError("DeepSeek tool-call name changed")
					}
					call.Function.Name = delta.Function.Name
				}
				if len(call.Function.Arguments) > maximumToolArgumentBytes-len(delta.Function.Arguments) {
					return protocolError("DeepSeek tool arguments exceed limit")
				}
				call.Function.Arguments += delta.Function.Arguments
			}
			if choice.FinishReason != nil {
				if finishSeen || *choice.FinishReason == "" || len(*choice.FinishReason) > 64 {
					return protocolError("invalid DeepSeek finish reason")
				}
				finishSeen = true
				result.finish = *choice.FinishReason
			}
		}
		return nil
	}
	for scanner.Scan() {
		if oversizedLine {
			return streamResult{}, protocolError("DeepSeek SSE line exceeds limit")
		}
		if err := ctx.Err(); err != nil {
			return streamResult{}, err
		}
		pulse()
		line := scanner.Bytes()
		if len(line) == 0 {
			if record.Len() == 0 {
				continue
			}
			if err := consumeRecord(record.Bytes()); err != nil {
				return streamResult{}, err
			}
			record.Reset()
			if result.doneMarker {
				break
			}
			continue
		}
		if line[0] == ':' || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := line[len("data:"):]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		if record.Len() > 0 {
			_ = record.WriteByte('\n')
		}
		if record.Len()+len(data) > maximumSSERecordBytes {
			return streamResult{}, protocolError("DeepSeek SSE record exceeds limit")
		}
		_, _ = record.Write(data)
	}
	if err := scanner.Err(); err != nil {
		return streamResult{}, err
	}
	if record.Len() > 0 {
		if err := consumeRecord(record.Bytes()); err != nil {
			return streamResult{}, err
		}
	}
	if !finishSeen || !result.doneMarker {
		return streamResult{}, protocolError("DeepSeek stream ended before completion")
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	identities := make(map[string]struct{}, len(indexes))
	for expected, index := range indexes {
		if index != expected {
			return streamResult{}, protocolError("DeepSeek tool-call indexes are not contiguous")
		}
		call := toolCalls[index]
		if !validVisibleASCII(call.ID, 512) || call.Function.Name != agent.ToolRemoteExec || !json.Valid([]byte(call.Function.Arguments)) {
			return streamResult{}, protocolError("invalid DeepSeek remote tool call")
		}
		if _, duplicate := identities[call.ID]; duplicate {
			return streamResult{}, protocolError("duplicate DeepSeek tool-call identity")
		}
		identities[call.ID] = struct{}{}
		result.toolCalls = append(result.toolCalls, *call)
	}
	return result, nil
}

func classifyHTTPStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return providerError("unauthorized", "DeepSeek authentication failed")
	case http.StatusTooManyRequests:
		return providerError("rate_limited", "DeepSeek rate limited the request")
	case http.StatusRequestTimeout:
		return providerError("provider_unavailable", "DeepSeek request timed out")
	default:
		if status >= 500 {
			return providerError("provider_unavailable", "DeepSeek service was unavailable")
		}
		if status >= 400 {
			return providerError("provider_rejected", "DeepSeek rejected the request or model configuration")
		}
		return protocolError("DeepSeek rejected the request")
	}
}

func providerError(code, detail string) error {
	return &agent.AdapterError{Code: code, Err: errors.New(detail)}
}

func protocolError(detail string) error {
	return providerError("protocol_error", detail)
}
