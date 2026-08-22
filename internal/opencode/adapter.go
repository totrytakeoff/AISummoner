// Package opencode implements the real Agent Adapter for a loopback OpenCode
// headless sidecar. It exposes no Server-local execution capability.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
)

const ProviderName = "opencode"

const (
	maximumModelIDBytes   = 256
	maximumHealthBytes    = 16 * 1024
	maximumSessionBytes   = 64 * 1024
	maximumAbortBytes     = 1024
	maximumSSERecordBytes = 256 * 1024
	maximumEventIDs       = 2048
	maximumTextParts      = 512
	requestTimeout        = 15 * time.Second
	abortTimeout          = 3 * time.Second
)

var builtInTools = []string{
	"invalid", "question", "bash", "read", "glob", "grep", "edit", "write",
	"task", "webfetch", "todowrite", "websearch", "skill", "apply_patch",
}

// Options configures a production Adapter without importing the integration
// config package. HTTPClient transport/timeout policy remains injectable.
type Options struct {
	BaseURL       string
	Username      string
	Password      string
	ModelID       string
	WorkspaceRoot string
	HTTPClient    *http.Client
	Logger        *slog.Logger
	Bridge        opencodebridge.Activator
}

type Adapter struct {
	baseURL    *url.URL
	username   string
	password   string
	modelID    string
	client     *http.Client
	logger     *slog.Logger
	bridge     opencodebridge.Activator
	workspaces *workspaceManager
}

type HealthStatus string

const (
	HealthAvailable   HealthStatus = "available"
	HealthRateLimited HealthStatus = "rate_limited"
	HealthUnavailable HealthStatus = "unavailable"
)

type HealthResult struct {
	Status  HealthStatus
	Version string
}

type createSessionRequest struct {
	Title string             `json:"title"`
	Model createSessionModel `json:"model"`
}

type createSessionModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant"`
}

type createSessionResponse struct {
	ID string `json:"id"`
}

type promptRequest struct {
	// MessageID is intentionally omitted in production requests. OpenCode
	// generates monotonic message IDs and uses their ordering to decide when a
	// turn has finished. Supplying an arbitrary external ID can keep its agent
	// loop running forever after an otherwise completed answer.
	MessageID string           `json:"messageID,omitempty"`
	Model     promptModel      `json:"model"`
	Agent     string           `json:"agent"`
	Tools     map[string]bool  `json:"tools"`
	Parts     []promptTextPart `json:"parts"`
}

type promptModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type promptTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewAdapter validates loopback/auth/model/workspace dependencies. Redirects
// are always rejected so Basic Auth cannot leave the configured authority.
func NewAdapter(options Options) (*Adapter, error) {
	baseURL, err := validateBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.Username == "" || options.Password == "" {
		return nil, errors.New("opencode credentials are required")
	}
	modelID, err := normalizeModelID(options.ModelID)
	if err != nil {
		return nil, err
	}
	if options.Bridge == nil {
		return nil, errors.New("opencode bridge is required")
	}
	workspaces, err := newWorkspaceManager(options.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copyClient := *client
		client = &copyClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// SSE is turn-bounded by request context; a global client timeout would
	// truncate legal approval waits.
	client.Timeout = 0
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Adapter{
		baseURL: baseURL, username: options.Username, password: options.Password, modelID: modelID,
		client: client, logger: options.Logger, bridge: options.Bridge, workspaces: workspaces,
	}, nil
}

func (adapter *Adapter) Health(ctx context.Context) HealthResult {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := adapter.newRequest(requestCtx, http.MethodGet, "/global/health", "", nil)
	if err != nil {
		return HealthResult{Status: HealthUnavailable}
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return HealthResult{Status: HealthUnavailable}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return HealthResult{Status: HealthRateLimited}
	}
	if response.StatusCode != http.StatusOK {
		return HealthResult{Status: HealthUnavailable}
	}
	var payload struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := decodeBoundedJSON(response.Body, maximumHealthBytes, &payload); err != nil || !payload.Healthy || len(payload.Version) > 128 {
		return HealthResult{Status: HealthUnavailable}
	}
	return HealthResult{Status: HealthAvailable, Version: payload.Version}
}

func (adapter *Adapter) Run(ctx context.Context, runRequest agent.RunRequest, sink agent.EventSink) error {
	if runRequest.SessionID == "" || runRequest.RemoteExec == nil || sink == nil {
		return &agent.AdapterError{Code: "protocol_error", Err: errors.New("invalid OpenCode run request")}
	}
	workspace, err := adapter.workspaces.prepare(runRequest.SessionID)
	if err != nil {
		return &agent.AdapterError{Code: "protocol_error", Err: err}
	}
	externalSessionID := runRequest.ExternalSessionID
	if externalSessionID == "" {
		externalSessionID, err = adapter.createSession(ctx, workspace, runRequest.SessionID)
		if err != nil {
			return err
		}
		if err := sink.SetExternalSessionID(ctx, externalSessionID); err != nil {
			return err
		}
	} else if !validExternalSessionID(externalSessionID) {
		return &agent.AdapterError{Code: "protocol_error", Err: errors.New("invalid persisted OpenCode session id")}
	}

	eventResponse, err := adapter.subscribe(ctx, workspace)
	if err != nil {
		return err
	}
	activation, err := adapter.bridge.Activate(ctx, runRequest.SessionID, externalSessionID, runRequest.RemoteExec)
	if err != nil {
		_ = eventResponse.Body.Close()
		return &agent.AdapterError{Code: "protocol_error", Err: err}
	}

	parserCtx, parserCancel := context.WithCancel(ctx)
	dispatched := make(chan struct{})
	parserResults := make(chan error, 1)
	parserDone := make(chan struct{})
	go func() {
		defer close(parserDone)
		parserResults <- consumeEvents(parserCtx, eventResponse.Body, externalSessionID, "", dispatched, sink)
	}()
	cleanup := func(runErr error) error {
		// Make the callback capability inactive and join it before allowing the
		// provider parser to stop. Closing the stream then unblocks a Read that
		// does not observe parserCtx, and joining prevents sink use after Run.
		activation.Close()
		parserCancel()
		_ = eventResponse.Body.Close()
		<-parserDone
		select {
		case fatal := <-activation.Failures():
			if fatal != nil {
				return fatal
			}
		default:
		}
		return runErr
	}

	if err := adapter.prompt(ctx, workspace, externalSessionID, runRequest.UserText, func() { close(dispatched) }); err != nil {
		err = cleanup(err)
		adapter.abort(externalSessionID, workspace)
		return err
	}
	waitErr := waitForTurn(ctx, activation.Failures(), parserResults)
	waitErr = cleanup(waitErr)
	if waitErr != nil {
		adapter.abort(externalSessionID, workspace)
	}
	return waitErr
}

func waitForTurn(ctx context.Context, failures <-chan error, parserResults <-chan error) error {
	for {
		// A bridge failure is buffered before its mapping cancellation. Always
		// drain that original failure before classifying a simultaneous cancel.
		select {
		case fatal := <-failures:
			if fatal != nil {
				return fatal
			}
		default:
		}
		select {
		case fatal := <-failures:
			if fatal != nil {
				return fatal
			}
		case parseErr := <-parserResults:
			select {
			case fatal := <-failures:
				if fatal != nil {
					return fatal
				}
			default:
			}
			return parseErr
		case <-ctx.Done():
			select {
			case fatal := <-failures:
				if fatal != nil {
					return fatal
				}
			default:
			}
			return ctx.Err()
		}
	}
}

func (adapter *Adapter) createSession(ctx context.Context, workspace, productSessionID string) (string, error) {
	titleSuffix := productSessionID
	if len(titleSuffix) > 64 {
		titleSuffix = titleSuffix[:64]
	}
	body := createSessionRequest{
		Title: "AISummoner " + titleSuffix,
		Model: createSessionModel{ID: adapter.modelID, ProviderID: ProviderName, Variant: "default"},
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := adapter.newJSONRequest(requestCtx, http.MethodPost, "/session", workspace, body)
	if err != nil {
		return "", &agent.AdapterError{Code: "protocol_error", Err: err}
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return "", classifyTransportError(err)
	}
	defer response.Body.Close()
	if err := classifyStatus(response.StatusCode); err != nil {
		return "", err
	}
	var payload createSessionResponse
	if err := decodeBoundedJSON(response.Body, maximumSessionBytes, &payload); err != nil || !validExternalSessionID(payload.ID) {
		return "", &agent.AdapterError{Code: "protocol_error", Err: errors.New("invalid OpenCode session response")}
	}
	return payload.ID, nil
}

func (adapter *Adapter) subscribe(ctx context.Context, workspace string) (*http.Response, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	headerTimer := time.AfterFunc(requestTimeout, cancel)
	request, err := adapter.newRequest(requestCtx, http.MethodGet, "/event", workspace, nil)
	if err != nil {
		headerTimer.Stop()
		cancel()
		return nil, &agent.AdapterError{Code: "protocol_error", Err: err}
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := adapter.client.Do(request)
	if !headerTimer.Stop() {
		cancel()
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, context.DeadlineExceeded
	}
	if err != nil {
		cancel()
		return nil, classifyTransportError(err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		cancel()
		if err := classifyNonSuccessStatus(response.StatusCode); err != nil {
			return nil, err
		}
		return nil, &agent.AdapterError{Code: "protocol_error", Err: errors.New("OpenCode event stream returned an unexpected status")}
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		_ = response.Body.Close()
		cancel()
		return nil, &agent.AdapterError{Code: "protocol_error", Err: errors.New("OpenCode event response is not SSE")}
	}
	response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func (adapter *Adapter) prompt(ctx context.Context, workspace, externalSessionID, userText string, beforeDispatch func()) error {
	tools := make(map[string]bool, len(builtInTools)+1)
	for _, toolName := range builtInTools {
		tools[toolName] = false
	}
	tools[agent.ToolRemoteExec] = true
	body := promptRequest{
		Model: promptModel{ProviderID: ProviderName, ModelID: adapter.modelID},
		Agent: "build", Tools: tools,
		Parts: []promptTextPart{{Type: "text", Text: userText}},
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := adapter.newJSONRequest(requestCtx, http.MethodPost, "/session/"+url.PathEscape(externalSessionID)+"/prompt_async", workspace, body)
	if err != nil {
		return &agent.AdapterError{Code: "protocol_error", Err: err}
	}
	beforeDispatch()
	response, err := adapter.client.Do(request)
	if err != nil {
		return classifyTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		switch {
		case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden,
			response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= 500:
			return classifyStatus(response.StatusCode)
		default:
			return &agent.AdapterError{Code: "protocol_error", Err: errors.New("OpenCode prompt returned an unexpected status")}
		}
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maximumAbortBytes+1))
	if err != nil || len(encoded) > maximumAbortBytes {
		return &agent.AdapterError{Code: "protocol_error", Err: errors.New("OpenCode prompt response exceeds limit")}
	}
	return nil
}

func (adapter *Adapter) abort(externalSessionID, workspace string) {
	ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()
	request, err := adapter.newRequest(ctx, http.MethodPost, "/session/"+url.PathEscape(externalSessionID)+"/abort", workspace, nil)
	if err != nil {
		return
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var aborted bool
	_ = decodeBoundedJSON(response.Body, maximumAbortBytes, &aborted)
}

func (adapter *Adapter) newJSONRequest(ctx context.Context, method, requestPath, workspace string, value any) (*http.Request, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, err := adapter.newRequest(ctx, method, requestPath, workspace, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func (adapter *Adapter) newRequest(ctx context.Context, method, requestPath, workspace string, body io.Reader) (*http.Request, error) {
	target := *adapter.baseURL
	target.Path = path.Join(adapter.baseURL.Path, requestPath)
	if workspace != "" {
		query := target.Query()
		query.Set("directory", workspace)
		target.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(adapter.username, adapter.password)
	request.Header.Set("Cache-Control", "no-store")
	return request, nil
}

func validateBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, errors.New("invalid OpenCode base URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("OpenCode base URL must be a loopback HTTP(S) origin")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("OpenCode base URL must use a loopback host")
		}
	}
	parsed.Path = ""
	return parsed, nil
}

func normalizeModelID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, ProviderName+"/") {
		value = strings.TrimPrefix(value, ProviderName+"/")
	}
	if value == "" || len(value) > maximumModelIDBytes || strings.ContainsAny(value, "/?&#%\\\x00") || !utf8.ValidString(value) {
		return "", errors.New("invalid OpenCode model id")
	}
	for _, character := range value {
		if character <= ' ' || character == 0x7f {
			return "", errors.New("invalid OpenCode model id")
		}
	}
	return value, nil
}

func validExternalSessionID(value string) bool {
	if len(value) <= 4 || len(value) > 512 || !strings.HasPrefix(value, "ses_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func decodeBoundedJSON(reader io.Reader, maximum int64, destination any) error {
	encoded, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(encoded)) > maximum {
		return errors.New("bounded OpenCode response read failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		return errors.New("OpenCode response has trailing JSON")
	}
	return nil
}

func classifyStatus(status int) error {
	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &agent.AdapterError{Code: "unauthorized", Err: fmt.Errorf("OpenCode returned status %d", status)}
	case status == http.StatusTooManyRequests:
		return &agent.AdapterError{Code: "rate_limited", Err: fmt.Errorf("OpenCode returned status %d", status)}
	case status >= 500:
		return &agent.AdapterError{Code: "provider_unavailable", Err: fmt.Errorf("OpenCode returned status %d", status)}
	default:
		return &agent.AdapterError{Code: "protocol_error", Err: fmt.Errorf("OpenCode returned status %d", status)}
	}
}

func classifyNonSuccessStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden,
		status == http.StatusTooManyRequests, status >= 500:
		return classifyStatus(status)
	default:
		return &agent.AdapterError{Code: "protocol_error", Err: fmt.Errorf("OpenCode returned status %d", status)}
	}
}

func classifyTransportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &agent.AdapterError{Code: "provider_unavailable", Err: err}
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelReadCloser) Close() error {
	body.cancel()
	return body.ReadCloser.Close()
}
