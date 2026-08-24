// Package dsh implements the first-class DeepSeek Harness Runtime Adapter.
// It speaks only to a private loopback DSH Host and exposes no Server-local
// command capability.
package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
	"github.com/coder/websocket"
)

const (
	ProviderName = agent.ProviderDSH

	CallbackPath = "/internal/dsh/remote-exec"
	ProofDomain  = "AISummoner.DSHBridge.v1"

	CredentialReference = "DEEPSEEK_API_KEY"
	AgentPreset         = "aisummoner"
)

const (
	maximumRPCResponseBytes              = 128 * 1024
	maximumCatalogRPCResponseBytes       = 512 * 1024
	maximumConfigurationRPCResponseBytes = 2 * 1024 * 1024
	maximumEventFrameBytes               = 512 * 1024
	maximumExternalIDBytes               = 512
	maximumCredentialBytes               = 4096
	requestTimeout                       = 15 * time.Second
	cancelTimeout                        = 3 * time.Second
	subscribeTimeout                     = 10 * time.Second
)

type HealthStatus string

const (
	HealthAvailable   HealthStatus = "available"
	HealthUnavailable HealthStatus = "unavailable"
)

type HealthResult struct {
	Status  HealthStatus
	Version string
}

// CredentialStatus is retained as the DSH package's value-free public alias.
type CredentialStatus = agent.CredentialStatus

// Options contains only the private Host transport and the already reviewed
// AISummoner capability activator.
type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	Bridge     opencodebridge.Activator
	NewID      func(string) (string, error)
}

type Adapter struct {
	baseURL *url.URL
	client  *http.Client
	bridge  opencodebridge.Activator
	newID   func(string) (string, error)
}

type rpcRequest struct {
	Type    string `json:"type"`
	RPCID   string `json:"rpcId"`
	Method  string `json:"method"`
	Payload any    `json:"payload"`
}

type rpcError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

type rpcResult struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *rpcError       `json:"error,omitempty"`
}

type rpcResponse struct {
	Type   string    `json:"type"`
	RPCID  string    `json:"rpcId"`
	Result rpcResult `json:"result"`
}

type serverRequest struct {
	Type    string          `json:"type"`
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

type muxFrame struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId,omitempty"`
	LastSeq   int64           `json:"lastSeq,omitempty"`
	Event     sessionEvent    `json:"event,omitempty"`
	Error     *rpcError       `json:"error,omitempty"`
	View      json.RawMessage `json:"view,omitempty"`
}

type sessionEvent struct {
	Type            string          `json:"type"`
	Seq             int64           `json:"seq"`
	Data            json.RawMessage `json:"data"`
	SourceEventSeqs []int64         `json:"sourceEventSeqs,omitempty"`
	SurfaceOp       json.RawMessage `json:"surfaceOp,omitempty"`
	Ignorable       bool            `json:"ignorable,omitempty"`
}

type turnData struct {
	Turn    int64           `json:"turn"`
	Step    int64           `json:"step,omitempty"`
	Reason  json.RawMessage `json:"reason,omitempty"`
	Chunk   json.RawMessage `json:"chunk,omitempty"`
	Trigger json.RawMessage `json:"trigger,omitempty"`
}

type turnEndReason struct {
	Kind  string `json:"kind"`
	Error struct {
		Code string `json:"code"`
	} `json:"error,omitempty"`
}

type eventChunk struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Text  string `json:"text,omitempty"`
}

type streamItem struct {
	frame muxFrame
	err   error
}

func NewAdapter(options Options) (*Adapter, error) {
	baseURL, err := validateBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.Bridge == nil {
		return nil, errors.New("DSH capability bridge is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copyClient := *client
		client = &copyClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 0
	if options.NewID == nil {
		options.NewID = id.New
	}
	return &Adapter{baseURL: baseURL, client: client, bridge: options.Bridge, newID: options.NewID}, nil
}

// Health proves the pinned Host RPC carrier is reachable and structurally
// usable. Provider credentials are deliberately not required at startup.
func (adapter *Adapter) Health(ctx context.Context) HealthResult {
	var value struct {
		Version          string `json:"version"`
		CWD              string `json:"cwd"`
		Provider         string `json:"provider"`
		Model            string `json:"model"`
		AttachedSessions int    `json:"attachedSessions"`
		CanOpenPath      bool   `json:"canOpenPath"`
	}
	if err := adapter.call(ctx, "host.describe", struct{}{}, &value); err != nil ||
		value.Version == "" || len(value.Version) > 128 {
		return HealthResult{Status: HealthUnavailable}
	}
	return HealthResult{Status: HealthAvailable, Version: value.Version}
}

// ConfigureCredential writes a DeepSeek key through DSH's value-write-only
// credential RPC. The value is never retained by this Adapter.
func (adapter *Adapter) ConfigureCredential(ctx context.Context, apiKey string) error {
	if !validCredential(apiKey) {
		return agent.ErrInvalidRequest
	}
	return adapter.call(ctx, "credentials.set", struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}{Ref: CredentialReference, Value: apiKey}, nil)
}

// DescribeCredential reads only DSH's value-free credential metadata.
func (adapter *Adapter) DescribeCredential(ctx context.Context) (CredentialStatus, error) {
	type credentialView struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source,omitempty"`
		Writable   bool   `json:"writable"`
	}
	var value struct {
		Credentials map[string]credentialView `json:"credentials"`
	}
	if err := adapter.call(ctx, "credentials.describe", struct {
		Refs []string `json:"refs"`
	}{Refs: []string{CredentialReference}}, &value); err != nil {
		return CredentialStatus{}, err
	}
	view, ok := value.Credentials[CredentialReference]
	if !ok || len(value.Credentials) != 1 {
		return CredentialStatus{}, protocolError("DSH credential description is invalid")
	}
	return CredentialStatus{Configured: view.Configured, Writable: view.Writable}, nil
}

// PreflightTurn evaluates the selected DSH provider instead of assuming the
// official DeepSeek route. It prevents a named missing credential or removed
// route from creating a failed product Turn.
func (adapter *Adapter) PreflightTurn(ctx context.Context, request agent.RunRequest) error {
	if request.ExternalSessionID == "" {
		return protocolError("DSH Session was not prepared before preflight")
	}
	directory, err := adapter.Models(ctx, request.ExternalSessionID)
	if err != nil {
		return err
	}
	if !directory.Routable {
		return &agent.AdapterError{Code: "provider_unavailable", Err: errors.New("selected DSH provider route is unavailable")}
	}
	if directory.CurrentCredential != nil && !directory.CurrentCredential.Configured {
		return &agent.AdapterError{Code: "credential_required", Err: errors.New("selected DSH provider credential is not configured")}
	}
	return nil
}

// PrepareSession creates or resumes the private DSH Session identified by an
// opaque ID. A new ID is returned to the product Service for owner-scoped
// persistence before a Turn or model mutation proceeds.
func (adapter *Adapter) PrepareSession(ctx context.Context, externalID string) (string, error) {
	if externalID == "" {
		var err error
		externalID, err = adapter.newID("ses")
		if err != nil {
			return "", protocolError("create DSH session id")
		}
	}
	if !validExternalSessionID(externalID) {
		return "", protocolError("invalid persisted DSH session id")
	}
	if err := adapter.createOrResume(ctx, externalID); err != nil {
		return "", err
	}
	return externalID, nil
}

func (adapter *Adapter) Run(ctx context.Context, request agent.RunRequest, sink agent.EventSink) error {
	if ctx == nil || request.SessionID == "" || request.RemoteExec == nil || sink == nil || request.UserText == "" {
		return protocolError("invalid DSH run request")
	}
	newExternalID := request.ExternalSessionID == ""
	externalID, err := adapter.PrepareSession(ctx, request.ExternalSessionID)
	if err != nil {
		return err
	}
	if newExternalID {
		if err := sink.SetExternalSessionID(ctx, externalID); err != nil {
			return err
		}
	}

	connection, baseline, err := adapter.subscribe(ctx, externalID)
	if err != nil {
		return err
	}
	activation, err := adapter.bridge.Activate(ctx, request.SessionID, externalID, request.RemoteExec)
	if err != nil {
		connection.CloseNow()
		return protocolError("activate DSH capability")
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	stream := make(chan streamItem, 16)
	streamDone := make(chan struct{})
	go adapter.readStream(streamCtx, connection, stream, streamDone)
	cleanup := func(runErr error, cancelRuntime bool) error {
		if cancelRuntime {
			adapter.cancel(externalID)
		}
		activation.Close()
		cancelStream()
		connection.CloseNow()
		<-streamDone
		select {
		case fatal := <-activation.Failures():
			if fatal != nil {
				return fatal
			}
		default:
		}
		return runErr
	}

	if err := adapter.prompt(ctx, externalID, request.UserText); err != nil {
		return cleanup(err, true)
	}
	err = consumeTurn(ctx, externalID, baseline, stream, activation.Failures(), sink)
	return cleanup(err, err != nil)
}

func (adapter *Adapter) createOrResume(ctx context.Context, externalID string) error {
	var value struct {
		SessionID   string `json:"sessionId"`
		AgentPreset string `json:"agentPreset"`
	}
	err := adapter.call(ctx, "session.create", struct {
		CWD         string `json:"cwd"`
		SessionID   string `json:"sessionId"`
		AgentPreset string `json:"agentPreset"`
	}{CWD: "/", SessionID: externalID, AgentPreset: AgentPreset}, &value)
	if err != nil {
		return err
	}
	if value.SessionID != externalID || value.AgentPreset != AgentPreset {
		return protocolError("DSH resumed an unexpected session")
	}
	return nil
}

func (adapter *Adapter) prompt(ctx context.Context, externalID, userText string) error {
	var value struct {
		Accepted bool `json:"accepted"`
		Command  *struct {
			Kind string `json:"kind"`
			Text string `json:"text,omitempty"`
		} `json:"command,omitempty"`
	}
	err := adapter.call(ctx, "session.prompt", struct {
		SessionID string `json:"sessionId"`
		Mode      string `json:"mode"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}{SessionID: externalID, Mode: "queue", Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: userText}}}, &value)
	if err != nil {
		return err
	}
	if !value.Accepted || value.Command != nil {
		return protocolError("DSH prompt was not accepted")
	}
	return nil
}

func (adapter *Adapter) cancel(externalID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	var value struct {
		Accepted bool `json:"accepted"`
	}
	_ = adapter.call(ctx, "session.cancel", struct {
		SessionID string `json:"sessionId"`
	}{SessionID: externalID}, &value)
}

func (adapter *Adapter) subscribe(ctx context.Context, externalID string) (*websocket.Conn, int64, error) {
	subscribeCtx, cancel := context.WithTimeout(ctx, subscribeTimeout)
	defer cancel()
	target := *adapter.baseURL
	if target.Scheme == "http" {
		target.Scheme = "ws"
	} else {
		target.Scheme = "wss"
	}
	target.Path = "/api/events.mux"
	connection, response, err := websocket.Dial(subscribeCtx, target.String(), &websocket.DialOptions{HTTPClient: adapter.client})
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, 0, classifyHTTPStatus(response.StatusCode)
		}
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, providerUnavailable()
	}
	connection.SetReadLimit(maximumEventFrameBytes)
	for {
		frame, err := readMuxFrame(subscribeCtx, connection)
		if err != nil {
			connection.CloseNow()
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			return nil, 0, providerUnavailable()
		}
		switch frame.Type {
		case "session/subscribed":
			if frame.SessionID == externalID {
				if frame.LastSeq < -1 {
					connection.CloseNow()
					return nil, 0, protocolError("invalid DSH subscription baseline")
				}
				return connection, frame.LastSeq, nil
			}
		case "stream/error":
			connection.CloseNow()
			return nil, 0, providerUnavailable()
		}
	}
}

func (adapter *Adapter) readStream(ctx context.Context, connection *websocket.Conn, output chan<- streamItem, done chan<- struct{}) {
	defer close(done)
	defer close(output)
	for {
		frame, err := readMuxFrame(ctx, connection)
		item := streamItem{frame: frame, err: err}
		select {
		case output <- item:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func readMuxFrame(ctx context.Context, connection *websocket.Conn) (muxFrame, error) {
	messageType, encoded, err := connection.Read(ctx)
	if err != nil {
		return muxFrame{}, err
	}
	if messageType != websocket.MessageText || len(encoded) == 0 || len(encoded) > maximumEventFrameBytes {
		return muxFrame{}, errors.New("invalid DSH event frame")
	}
	var envelope serverRequest
	if err := decodeStrict(encoded, &envelope); err != nil || envelope.Type != "server-request" || envelope.RPCID == "" || envelope.Method == "" {
		return muxFrame{}, errors.New("invalid DSH event envelope")
	}
	var frame muxFrame
	if err := json.Unmarshal(envelope.Payload, &frame); err != nil || frame.Type == "" || envelope.Method != frame.Type {
		return muxFrame{}, errors.New("invalid DSH event payload")
	}
	return frame, nil
}

func consumeTurn(ctx context.Context, externalID string, baseline int64, stream <-chan streamItem, failures <-chan error, sink agent.EventSink) error {
	lastSeq := baseline
	var activeTurn int64
	for {
		select {
		case fatal := <-failures:
			if fatal != nil {
				return fatal
			}
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-stream:
			if !ok {
				return providerUnavailable()
			}
			if item.err != nil {
				return classifyTransport(item.err)
			}
			frame := item.frame
			if frame.Type == "stream/error" {
				return providerUnavailable()
			}
			if frame.Type != "session/event" || frame.SessionID != externalID {
				continue
			}
			event := frame.Event
			if event.Type == "" || event.Seq <= lastSeq {
				return protocolError("non-monotonic DSH session event")
			}
			lastSeq = event.Seq
			var data turnData
			switch event.Type {
			case "turn/start":
				if err := decodeStrict(event.Data, &data); err != nil || data.Turn <= 0 || activeTurn != 0 {
					return protocolError("invalid DSH turn start")
				}
				activeTurn = data.Turn
				if err := sink.ProviderState(ctx, "streaming"); err != nil {
					return err
				}
			case "assistant/chunk":
				if err := decodeStrict(event.Data, &data); err != nil || data.Turn <= 0 {
					return protocolError("invalid DSH assistant chunk")
				}
				if activeTurn == 0 || data.Turn != activeTurn {
					continue
				}
				var chunk eventChunk
				if err := json.Unmarshal(data.Chunk, &chunk); err != nil || chunk.Type == "" || chunk.Index < 0 || !utf8.ValidString(chunk.Text) {
					return protocolError("invalid DSH text encoding")
				}
				switch chunk.Type {
				case "reasoning-delta":
					if chunk.Text != "" {
						if err := sink.ReasoningDelta(ctx, chunk.Text); err != nil {
							return err
						}
					}
				case "text-delta":
					if chunk.Text != "" {
						if err := sink.TextDelta(ctx, chunk.Text); err != nil {
							return err
						}
					}
				}
			case "turn/end":
				if err := decodeStrict(event.Data, &data); err != nil || data.Turn <= 0 || activeTurn == 0 || data.Turn != activeTurn {
					return protocolError("invalid DSH turn end")
				}
				var reason turnEndReason
				if err := json.Unmarshal(data.Reason, &reason); err != nil || reason.Kind == "" {
					return protocolError("invalid DSH turn end reason")
				}
				return classifyTurnEnd(reason)
			}
		}
	}
}

func classifyTurnEnd(reason turnEndReason) error {
	switch reason.Kind {
	case "completed":
		return nil
	case "aborted":
		return context.Canceled
	case "error":
		switch strings.ToUpper(reason.Error.Code) {
		case "AUTH", "UNAUTHORIZED":
			return &agent.AdapterError{Code: "unauthorized", Err: errors.New("DSH provider authentication failed")}
		case "RATE_LIMIT", "QUOTA":
			return &agent.AdapterError{Code: "rate_limited", Err: errors.New("DSH provider rate limited")}
		case "SERVER", "TRANSPORT", "STREAM_CLOSED":
			return providerUnavailable()
		default:
			return &agent.AdapterError{Code: "provider_rejected", Err: errors.New("DSH provider rejected the turn")}
		}
	case "interrupted":
		return providerUnavailable()
	case "blocked", "max-tokens":
		return &agent.AdapterError{Code: "provider_rejected", Err: errors.New("DSH turn did not complete")}
	default:
		return protocolError("unknown DSH turn outcome")
	}
}

func (adapter *Adapter) call(ctx context.Context, method string, payload, destination any) error {
	return adapter.callBounded(ctx, method, payload, destination, maximumRPCResponseBytes)
}

func (adapter *Adapter) callBounded(ctx context.Context, method string, payload, destination any, maximumBytes int64) error {
	if ctx == nil {
		return errors.New("DSH request context is required")
	}
	if maximumBytes <= 0 {
		return protocolError("invalid DSH response limit")
	}
	rpcID, err := adapter.newID("req")
	if err != nil {
		return protocolError("create DSH request id")
	}
	encoded, err := json.Marshal(rpcRequest{Type: "client-request", RPCID: rpcID, Method: method, Payload: payload})
	if err != nil {
		return protocolError("encode DSH request")
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	target := *adapter.baseURL
	target.Path = "/api/" + method
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return protocolError("construct DSH request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	response, err := adapter.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerUnavailable()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return classifyHTTPStatus(response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return protocolError("DSH response content type is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maximumBytes {
		return protocolError("DSH response exceeds the safe limit")
	}
	var envelope rpcResponse
	if err := decodeStrict(body, &envelope); err != nil || envelope.Type != "server-response" || envelope.RPCID != rpcID {
		return protocolError("DSH response envelope is invalid")
	}
	if !envelope.Result.OK {
		if envelope.Result.Error == nil || envelope.Result.Error.Code == "" {
			return protocolError("DSH response error is invalid")
		}
		return classifyRPCError(envelope.Result.Error.Code)
	}
	if envelope.Result.Error != nil {
		return protocolError("DSH response has conflicting result branches")
	}
	if destination == nil {
		return nil
	}
	if len(envelope.Result.Value) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Result.Value), []byte("null")) {
		return protocolError("DSH response value is missing")
	}
	if err := decodeStrict(envelope.Result.Value, destination); err != nil {
		return protocolError("DSH response value is invalid")
	}
	return nil
}

func classifyRPCError(code string) error {
	switch code {
	case "cancelled":
		return context.Canceled
	case "settings-conflict":
		return &agent.AdapterError{Code: "configuration_conflict", Err: errors.New("DSH settings changed concurrently")}
	case "model-unavailable":
		return &agent.AdapterError{Code: "model_unavailable", Err: errors.New("DSH model is unavailable")}
	case "agent-busy", "session-conflict", "agent-preset-conflict", "credential-rejected", "settings-rejected", "bad-request":
		return &agent.AdapterError{Code: "provider_rejected", Err: errors.New("DSH request was rejected")}
	case "internal", "settings-not-exposed":
		return providerUnavailable()
	case "session-not-found":
		return &agent.AdapterError{Code: "provider_unavailable", Err: errors.New("DSH session is unavailable")}
	default:
		return protocolError("DSH returned an unsupported error")
	}
}

func classifyHTTPStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &agent.AdapterError{Code: "unauthorized", Err: fmt.Errorf("DSH returned HTTP %d", status)}
	case status == http.StatusTooManyRequests:
		return &agent.AdapterError{Code: "rate_limited", Err: fmt.Errorf("DSH returned HTTP %d", status)}
	case status >= 500:
		return providerUnavailable()
	default:
		return protocolError("unexpected DSH HTTP status")
	}
}

func classifyTransport(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return providerUnavailable()
}

func providerUnavailable() error {
	return &agent.AdapterError{Code: "provider_unavailable", Err: errors.New("DSH Host is unavailable")}
}

func protocolError(message string) error {
	return &agent.AdapterError{Code: "protocol_error", Err: errors.New(message)}
}

func validateBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("DSH base URL must be a loopback HTTP origin")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("DSH base URL must use a literal loopback host")
	}
	parsed.Path = ""
	return parsed, nil
}

func validExternalSessionID(value string) bool {
	if len(value) <= len("ses_") || len(value) > maximumExternalIDBytes || !strings.HasPrefix(value, "ses_") {
		return false
	}
	for _, character := range value[len("ses_"):] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validCredential(value string) bool {
	if len(value) == 0 || len(value) > maximumCredentialBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func decodeStrict(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
