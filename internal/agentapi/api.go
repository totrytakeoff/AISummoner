// Package agentapi exposes the standalone ownership-checked Agent REST/SSE
// handler. Server integration mounts Handler() alongside the foundation API.
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/store"
)

const (
	sessionCookieName              = "aisummoner_session"
	maxJSONBodyBytes               = 6*agent.MaxMessageBytes + 1024
	maxDeepSeekConfigJSONBodyBytes = 16 * 1024
	keepaliveInterval              = 15 * time.Second
	defaultWriteWait               = 10 * time.Second
)

type AgentService interface {
	CreateSession(context.Context, string, string, string) (store.AgentSession, error)
	Snapshot(context.Context, string, string) (store.AgentSnapshot, error)
	LatestSnapshot(context.Context, string, string) (store.AgentSnapshot, error)
	StartTurn(context.Context, string, string, string) (store.AgentMessage, error)
	Decide(context.Context, string, string, string) (store.ToolCall, error)
	Subscribe(context.Context, string, string) (<-chan agent.Event, func(), error)
}

// ProviderConfigurator changes only the Server-side provider registry. The
// credential is accepted through the authenticated same-origin control plane
// and must not be persisted, logged, audited, or returned to the Browser.
type ProviderConfigurator interface {
	ConfigureDeepSeek(context.Context, string, string) error
}

type Options struct {
	Auth                 *auth.Service
	Agent                AgentService
	ProviderConfigurator ProviderConfigurator
	AllowedOrigin        string
	Logger               *slog.Logger
	Now                  func() time.Time
	Keepalive            time.Duration
	WriteTimeout         time.Duration
}

type API struct {
	auth                 *auth.Service
	agent                AgentService
	providerConfigurator ProviderConfigurator
	allowedOrigin        string
	logger               *slog.Logger
	now                  func() time.Time
	keepalive            time.Duration
	writeTimeout         time.Duration
}

func New(options Options) (*API, error) {
	if options.Auth == nil || options.Agent == nil || options.AllowedOrigin == "" {
		return nil, errors.New("auth, agent service, and allowed origin are required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Keepalive <= 0 {
		options.Keepalive = keepaliveInterval
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = defaultWriteWait
	}
	return &API{
		auth: options.Auth, agent: options.Agent, providerConfigurator: options.ProviderConfigurator,
		allowedOrigin: options.AllowedOrigin,
		logger:        options.Logger, now: options.Now, keepalive: options.Keepalive, writeTimeout: options.WriteTimeout,
	}, nil
}

func (api *API) Handler() http.Handler {
	return api.withRequestID(api.withRecovery(http.HandlerFunc(api.route)))
}

func (api *API) route(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if request.Method == http.MethodPost && !api.sameOrigin(request) {
		api.writeError(writer, request, http.StatusForbidden, "ORIGIN_FORBIDDEN", "request origin is not allowed")
		return
	}
	authenticated, user, ok := api.authenticate(writer, request)
	if !ok {
		return
	}
	switch {
	case path == "/api/v1/agent-provider/deepseek" && request.Method == http.MethodPost:
		if api.providerConfigurator == nil {
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		api.configureDeepSeek(writer, authenticated)
	case (request.Method == http.MethodPost || request.Method == http.MethodGet) && strings.HasPrefix(path, "/api/v1/devices/") && strings.HasSuffix(path, "/agent-sessions"):
		deviceID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/devices/"), "/agent-sessions")
		if !validPathID(deviceID) || strings.Contains(deviceID, "/") {
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if request.Method == http.MethodGet {
			api.latestSession(writer, authenticated, user, deviceID)
		} else {
			api.createSession(writer, authenticated, user, deviceID)
		}
	case strings.HasPrefix(path, "/api/v1/agent-sessions/"):
		remainder := strings.TrimPrefix(path, "/api/v1/agent-sessions/")
		if strings.HasSuffix(remainder, "/messages") && request.Method == http.MethodPost {
			sessionID := strings.TrimSuffix(remainder, "/messages")
			if !validPathID(sessionID) || strings.Contains(sessionID, "/") {
				api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
				return
			}
			api.postMessage(writer, authenticated, user, sessionID)
			return
		}
		if strings.HasSuffix(remainder, "/events") && request.Method == http.MethodGet {
			sessionID := strings.TrimSuffix(remainder, "/events")
			if !validPathID(sessionID) || strings.Contains(sessionID, "/") {
				api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
				return
			}
			api.events(writer, authenticated, user, sessionID)
			return
		}
		if request.Method == http.MethodGet && validPathID(remainder) && !strings.Contains(remainder, "/") {
			api.getSession(writer, authenticated, user, remainder)
			return
		}
		api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/tool-calls/") && strings.HasSuffix(path, "/decision"):
		toolCallID := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/tool-calls/"), "/decision")
		if !validPathID(toolCallID) || strings.Contains(toolCallID, "/") {
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		api.decide(writer, authenticated, user, toolCallID)
	default:
		api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
	}
}

type deepSeekConfigurationRequest struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

func (api *API) configureDeepSeek(writer http.ResponseWriter, request *http.Request) {
	var body deepSeekConfigurationRequest
	if err := decodeJSONWithLimit(writer, request, &body, maxDeepSeekConfigJSONBodyBytes); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := api.providerConfigurator.ConfigureDeepSeek(request.Context(), body.APIKey, body.Model); api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

type createSessionRequest struct {
	ApprovalMode string `json:"approval_mode"`
}

func (api *API) createSession(writer http.ResponseWriter, request *http.Request, user store.User, deviceID string) {
	var body createSessionRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	session, err := api.agent.CreateSession(request.Context(), user.ID, deviceID, body.ApprovalMode)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusCreated, map[string]any{"session": sessionResponse(session)})
}

func (api *API) latestSession(writer http.ResponseWriter, request *http.Request, user store.User, deviceID string) {
	snapshot, err := api.agent.LatestSnapshot(request.Context(), user.ID, deviceID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, snapshotResponse(snapshot))
}

func (api *API) getSession(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	snapshot, err := api.agent.Snapshot(request.Context(), user.ID, sessionID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, snapshotResponse(snapshot))
}

type messageRequest struct {
	Content string `json:"content"`
}

func (api *API) postMessage(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	var body messageRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	message, err := api.agent.StartTurn(request.Context(), user.ID, sessionID, body.Content)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusAccepted, map[string]any{"message": messageResponse(message)})
}

type decisionRequest struct {
	Decision string `json:"decision"`
}

func (api *API) decide(writer http.ResponseWriter, request *http.Request, user store.User, toolCallID string) {
	var body decisionRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	toolCall, err := api.agent.Decide(request.Context(), user.ID, toolCallID, body.Decision)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"tool_call": toolCallResponse(toolCall)})
}

func (api *API) events(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	events, unsubscribe, err := api.agent.Subscribe(request.Context(), user.ID, sessionID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	defer unsubscribe()
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Now().Add(api.writeTimeout)); err != nil {
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	if err := api.writeSSEFrame(writer, controller, ": connected\n\n"); err != nil {
		return
	}
	ticker := time.NewTicker(api.keepalive)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				return
			}
			frame := fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, encoded)
			if err := api.writeSSEFrame(writer, controller, frame); err != nil {
				return
			}
		case <-ticker.C:
			if err := api.writeSSEFrame(writer, controller, ": keepalive\n\n"); err != nil {
				return
			}
		}
	}
}

func (api *API) writeSSEFrame(writer http.ResponseWriter, controller *http.ResponseController, frame string) error {
	if err := controller.SetWriteDeadline(time.Now().Add(api.writeTimeout)); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, frame); err != nil {
		return err
	}
	return controller.Flush()
}

func (api *API) writeServiceError(writer http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, agent.ErrNotFound):
		api.writeError(writer, request, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, agent.ErrInvalidRequest), errors.Is(err, agent.ErrInvalidTool):
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
	case errors.Is(err, agent.ErrTurnInProgress):
		api.writeError(writer, request, http.StatusConflict, "TURN_IN_PROGRESS", "an agent turn is already running")
	case errors.Is(err, agent.ErrInvalidState):
		api.writeError(writer, request, http.StatusConflict, "INVALID_STATE", "resource is not in the required state")
	case errors.Is(err, agent.ErrDeviceOffline):
		api.writeError(writer, request, http.StatusConflict, "DEVICE_OFFLINE", "device is offline")
	case errors.Is(err, agent.ErrApprovalTimeout), errors.Is(err, context.DeadlineExceeded):
		api.writeError(writer, request, http.StatusGatewayTimeout, "TIMEOUT", "operation timed out")
	case errors.Is(err, agent.ErrServiceClosed):
		api.writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "agent service is unavailable")
	default:
		api.logger.Error("agent API request failed", "request_id", requestID(request), "path", request.URL.Path)
		api.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
	return true
}

func (api *API) sameOrigin(request *http.Request) bool {
	origins := request.Header.Values("Origin")
	return len(origins) == 1 && origins[0] == api.allowedOrigin
}

type requestIDKey struct{}

func (api *API) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := id.MustNew("req")
		writer.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID)))
	})
}

func (api *API) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				api.logger.Error("agent API panic", "request_id", requestID(request), "path", request.URL.Path, "stack", string(debug.Stack()))
				api.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDKey{}).(string)
	return value
}

func (api *API) authenticate(writer http.ResponseWriter, request *http.Request) (*http.Request, store.User, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		api.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return request, store.User{}, false
	}
	user, err := api.auth.Authenticate(request.Context(), cookie.Value, api.now().UTC())
	if errors.Is(err, auth.ErrInvalidCredentials) {
		api.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return request, store.User{}, false
	}
	if err != nil {
		api.logger.Error("agent API authentication failed", "request_id", requestID(request))
		api.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
		return request, store.User{}, false
	}
	return request, user, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	return decodeJSONWithLimit(writer, request, destination, maxJSONBodyBytes)
}

func decodeJSONWithLimit(writer http.ResponseWriter, request *http.Request, destination any, maximumBytes int64) error {
	if contentType := strings.ToLower(request.Header.Get("Content-Type")); contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		return errors.New("content type must be application/json")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func (api *API) writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	api.writeJSON(writer, status, map[string]any{
		"error": map[string]string{"code": code, "message": message, "request_id": requestID(request)},
	})
}

func (api *API) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func validPathID(value string) bool { return len(value) >= 5 && len(value) <= 128 }
