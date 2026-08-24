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
	maxDSHConfigJSONBodyBytes      = 16 * 1024
	maxRuntimeConfigJSONBodyBytes  = 512 * 1024
	keepaliveInterval              = 15 * time.Second
	defaultWriteWait               = 10 * time.Second
)

type AgentService interface {
	CreateSession(context.Context, string, string, string) (store.AgentSession, error)
	Snapshot(context.Context, string, string) (store.AgentSnapshot, error)
	LatestSnapshot(context.Context, string, string) (store.AgentSnapshot, error)
	ListSessions(context.Context, string, string) ([]store.AgentSessionSummary, error)
	ListArchivedSessions(context.Context, string) ([]store.AgentSessionSummary, error)
	Settings(context.Context, string) (store.AgentSettings, error)
	UpdateSettings(context.Context, string, string) (store.AgentSettings, error)
	UpdateSessionApprovalMode(context.Context, string, string, string) (store.AgentSession, error)
	SetSessionArchived(context.Context, string, string, bool) (store.AgentSession, error)
	DeleteSession(context.Context, string, string) error
	StartTurn(context.Context, string, string, string) (store.AgentMessage, error)
	Decide(context.Context, string, string, string) (store.ToolCall, error)
	Subscribe(context.Context, string, string) (<-chan agent.Event, func(), error)
}

// RuntimeConfigurationService is optional because not every Agent adapter has
// a Browser-configurable provider directory.
type RuntimeConfigurationService interface {
	RuntimeProviderDirectory(context.Context, string) (agent.RuntimeProviderDirectory, error)
	ConfigureRuntimeProvider(context.Context, string, agent.RuntimeProviderMutation) error
	RemoveRuntimeProvider(context.Context, string, string, int64) error
}

// SessionModelService is optional because direct/test adapters can own one
// fixed model without exposing a native per-Session selector.
type SessionModelService interface {
	SessionModels(context.Context, string, string) (agent.ModelDirectory, error)
	SelectSessionModel(context.Context, string, string, agent.ModelSelection) (agent.ModelSelection, error)
}

// ProviderConfigurator changes only the Server-side provider registry. The
// credential is accepted through the authenticated same-origin control plane
// and must not be persisted, logged, audited, or returned to the Browser.
type ProviderConfigurator interface {
	ConfigureDeepSeek(context.Context, string, string) error
}

// DSHCredentialConfigurator writes a provider credential into the private DSH
// Host store without returning, persisting, or logging its value in AISummoner.
type DSHCredentialConfigurator interface {
	ConfigureDSH(context.Context, string) error
	DescribeDSH(context.Context) (DSHCredentialStatus, error)
}

type DSHCredentialStatus struct {
	Configured bool
	Writable   bool
}

type Options struct {
	Auth                 *auth.Service
	Agent                AgentService
	ProviderConfigurator ProviderConfigurator
	DSHConfigurator      DSHCredentialConfigurator
	RuntimeConfiguration RuntimeConfigurationService
	SessionModels        SessionModelService
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
	dshConfigurator      DSHCredentialConfigurator
	runtimeConfiguration RuntimeConfigurationService
	sessionModels        SessionModelService
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
	runtimeConfiguration := options.RuntimeConfiguration
	if runtimeConfiguration == nil {
		runtimeConfiguration, _ = options.Agent.(RuntimeConfigurationService)
	}
	sessionModels := options.SessionModels
	if sessionModels == nil {
		sessionModels, _ = options.Agent.(SessionModelService)
	}
	return &API{
		auth: options.Auth, agent: options.Agent, providerConfigurator: options.ProviderConfigurator,
		dshConfigurator:      options.DSHConfigurator,
		runtimeConfiguration: runtimeConfiguration, sessionModels: sessionModels,
		allowedOrigin: options.AllowedOrigin,
		logger:        options.Logger, now: options.Now, keepalive: options.Keepalive, writeTimeout: options.WriteTimeout,
	}, nil
}

func (api *API) Handler() http.Handler {
	return api.withRequestID(api.withRecovery(http.HandlerFunc(api.route)))
}

func (api *API) route(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if (request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodPatch || request.Method == http.MethodDelete) && !api.sameOrigin(request) {
		api.writeError(writer, request, http.StatusForbidden, "ORIGIN_FORBIDDEN", "request origin is not allowed")
		return
	}
	authenticated, user, ok := api.authenticate(writer, request)
	if !ok {
		return
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/agent-runtimes/"):
		remainder := strings.TrimPrefix(path, "/api/v1/agent-runtimes/")
		parts := strings.Split(remainder, "/")
		if api.runtimeConfiguration == nil || len(parts) < 2 || parts[1] != "providers" ||
			!validRuntimeID(parts[0]) {
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		switch {
		case len(parts) == 2 && request.Method == http.MethodGet:
			api.runtimeProviders(writer, authenticated, parts[0])
		case len(parts) == 3 && validProviderID(parts[2]) && request.Method == http.MethodPut:
			api.configureRuntimeProvider(writer, authenticated, parts[0], parts[2])
		case len(parts) == 3 && validProviderID(parts[2]) && request.Method == http.MethodDelete:
			api.removeRuntimeProvider(writer, authenticated, parts[0], parts[2])
		default:
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
		}
	case path == "/api/v1/agent-provider/dsh" && (request.Method == http.MethodGet || request.Method == http.MethodPost):
		if api.dshConfigurator == nil {
			api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if request.Method == http.MethodGet {
			api.describeDSH(writer, authenticated)
		} else {
			api.configureDSH(writer, authenticated)
		}
	case path == "/api/v1/agent-settings" && (request.Method == http.MethodGet || request.Method == http.MethodPatch):
		if request.Method == http.MethodGet {
			api.getSettings(writer, authenticated, user)
		} else {
			api.updateSettings(writer, authenticated, user)
		}
	case path == "/api/v1/agent-sessions" && request.Method == http.MethodGet:
		query := request.URL.Query()
		views, hasView := query["view"]
		if len(query) != 1 || !hasView || len(views) != 1 || views[0] != "archived" {
			api.writeError(writer, authenticated, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
			return
		}
		api.listArchivedSessions(writer, authenticated, user)
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
			query := request.URL.Query()
			views, hasView := query["view"]
			switch {
			case len(query) == 0:
				api.latestSession(writer, authenticated, user, deviceID)
			case len(query) == 1 && hasView && len(views) == 1 && views[0] == "index":
				api.listSessions(writer, authenticated, user, deviceID)
			default:
				api.writeError(writer, authenticated, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
			}
		} else {
			api.createSession(writer, authenticated, user, deviceID)
		}
	case strings.HasPrefix(path, "/api/v1/agent-sessions/"):
		remainder := strings.TrimPrefix(path, "/api/v1/agent-sessions/")
		if strings.HasSuffix(remainder, "/models") && (request.Method == http.MethodGet || request.Method == http.MethodPatch) {
			sessionID := strings.TrimSuffix(remainder, "/models")
			if api.sessionModels == nil || !validPathID(sessionID) || strings.Contains(sessionID, "/") {
				api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
				return
			}
			if request.Method == http.MethodGet {
				api.getSessionModels(writer, authenticated, user, sessionID)
			} else {
				api.selectSessionModel(writer, authenticated, user, sessionID)
			}
			return
		}
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
		if validPathID(remainder) && !strings.Contains(remainder, "/") {
			switch request.Method {
			case http.MethodGet:
				api.getSession(writer, authenticated, user, remainder)
			case http.MethodPatch:
				api.updateSession(writer, authenticated, user, remainder)
			case http.MethodDelete:
				api.deleteSession(writer, authenticated, user, remainder)
			default:
				api.writeError(writer, authenticated, http.StatusNotFound, "NOT_FOUND", "resource not found")
			}
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

type dshConfigurationRequest struct {
	APIKey string `json:"api_key"`
}

type runtimeProviderModelRequest struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int64  `json:"context_window,omitempty"`
	MaxTokens     int64  `json:"max_tokens,omitempty"`
}

type runtimeProviderMutationRequest struct {
	ExpectedRevision *int64                         `json:"expected_revision"`
	DisplayName      string                         `json:"display_name,omitempty"`
	BaseURL          string                         `json:"base_url,omitempty"`
	API              string                         `json:"api,omitempty"`
	Models           *[]runtimeProviderModelRequest `json:"models,omitempty"`
	ModelsOverridden bool                           `json:"models_overridden"`
	APIKey           string                         `json:"api_key,omitempty"`
}

type runtimeProviderRemovalRequest struct {
	ExpectedRevision *int64 `json:"expected_revision"`
}

func (api *API) runtimeProviders(writer http.ResponseWriter, request *http.Request, runtime string) {
	directory, err := api.runtimeConfiguration.RuntimeProviderDirectory(request.Context(), runtime)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, runtimeProviderDirectoryResponse(directory))
}

func (api *API) configureRuntimeProvider(writer http.ResponseWriter, request *http.Request, runtime, provider string) {
	var body runtimeProviderMutationRequest
	if err := decodeJSONWithLimit(writer, request, &body, maxRuntimeConfigJSONBodyBytes); err != nil ||
		body.ExpectedRevision == nil || (body.ModelsOverridden && body.Models == nil) || (!body.ModelsOverridden && body.Models != nil) {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	mutation := agent.RuntimeProviderMutation{
		Provider: provider, ExpectedRevision: *body.ExpectedRevision,
		DisplayName: body.DisplayName, BaseURL: body.BaseURL, API: body.API,
		ModelsOverridden: body.ModelsOverridden, APIKey: body.APIKey,
	}
	if body.Models != nil {
		mutation.Models = make([]agent.RuntimeProviderModel, 0, len(*body.Models))
		for _, model := range *body.Models {
			mutation.Models = append(mutation.Models, agent.RuntimeProviderModel{
				ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
			})
		}
	}
	if err := api.runtimeConfiguration.ConfigureRuntimeProvider(request.Context(), runtime, mutation); api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) removeRuntimeProvider(writer http.ResponseWriter, request *http.Request, runtime, provider string) {
	var body runtimeProviderRemovalRequest
	if err := decodeJSONWithLimit(writer, request, &body, maxRuntimeConfigJSONBodyBytes); err != nil || body.ExpectedRevision == nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := api.runtimeConfiguration.RemoveRuntimeProvider(request.Context(), runtime, provider, *body.ExpectedRevision); api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) configureDSH(writer http.ResponseWriter, request *http.Request) {
	var body dshConfigurationRequest
	if err := decodeJSONWithLimit(writer, request, &body, maxDSHConfigJSONBodyBytes); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := api.dshConfigurator.ConfigureDSH(request.Context(), body.APIKey); api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) describeDSH(writer http.ResponseWriter, request *http.Request) {
	status, err := api.dshConfigurator.DescribeDSH(request.Context())
	if api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	api.writeJSON(writer, http.StatusOK, map[string]any{"credential": map[string]bool{
		"configured": status.Configured, "writable": status.Writable,
	}})
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

func (api *API) listSessions(writer http.ResponseWriter, request *http.Request, user store.User, deviceID string) {
	values, err := api.agent.ListSessions(request.Context(), user.ID, deviceID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	sessions := make([]sessionSummaryJSON, 0, len(values))
	for _, value := range values {
		sessions = append(sessions, sessionSummaryResponse(value))
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"sessions": sessions})
}

func (api *API) listArchivedSessions(writer http.ResponseWriter, request *http.Request, user store.User) {
	values, err := api.agent.ListArchivedSessions(request.Context(), user.ID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	sessions := make([]sessionSummaryJSON, 0, len(values))
	for _, value := range values {
		sessions = append(sessions, sessionSummaryResponse(value))
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"sessions": sessions})
}

type agentSettingsRequest struct {
	DefaultApprovalMode string `json:"default_approval_mode"`
}

func (api *API) getSettings(writer http.ResponseWriter, request *http.Request, user store.User) {
	settings, err := api.agent.Settings(request.Context(), user.ID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"settings": settingsResponse(settings)})
}

func (api *API) updateSettings(writer http.ResponseWriter, request *http.Request, user store.User) {
	var body agentSettingsRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	settings, err := api.agent.UpdateSettings(request.Context(), user.ID, body.DefaultApprovalMode)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"settings": settingsResponse(settings)})
}

type updateSessionRequest struct {
	ApprovalMode *string `json:"approval_mode,omitempty"`
	Archived     *bool   `json:"archived,omitempty"`
}

func (api *API) updateSession(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	var body updateSessionRequest
	if err := decodeJSON(writer, request, &body); err != nil || (body.ApprovalMode == nil) == (body.Archived == nil) {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	var (
		session store.AgentSession
		err     error
	)
	if body.ApprovalMode != nil {
		session, err = api.agent.UpdateSessionApprovalMode(request.Context(), user.ID, sessionID, *body.ApprovalMode)
	} else {
		session, err = api.agent.SetSessionArchived(request.Context(), user.ID, sessionID, *body.Archived)
	}
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"session": sessionResponse(session)})
}

func (api *API) deleteSession(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	if err := api.agent.DeleteSession(request.Context(), user.ID, sessionID); api.writeServiceError(writer, request, err) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) getSession(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	snapshot, err := api.agent.Snapshot(request.Context(), user.ID, sessionID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, snapshotResponse(snapshot))
}

type sessionModelSelectionRequest struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (api *API) getSessionModels(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	directory, err := api.sessionModels.SessionModels(request.Context(), user.ID, sessionID)
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, modelDirectoryResponse(directory))
}

func (api *API) selectSessionModel(writer http.ResponseWriter, request *http.Request, user store.User, sessionID string) {
	var body sessionModelSelectionRequest
	if err := decodeJSONWithLimit(writer, request, &body, maxRuntimeConfigJSONBodyBytes); err != nil {
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	selected, err := api.sessionModels.SelectSessionModel(request.Context(), user.ID, sessionID, agent.ModelSelection{
		Provider: body.Provider, Model: body.Model, ReasoningEffort: body.ReasoningEffort,
	})
	if api.writeServiceError(writer, request, err) {
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"selected": modelSelectionResponse(selected)})
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
	var adapterError *agent.AdapterError
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
	case errors.As(err, &adapterError) && (adapterError.Code == "provider_unavailable" || adapterError.Code == "rate_limited"):
		api.writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "agent provider is unavailable")
	case errors.As(err, &adapterError) && adapterError.Code == "credential_required":
		api.writeError(writer, request, http.StatusConflict, "PROVIDER_CREDENTIAL_REQUIRED", "agent provider credential is required")
	case errors.As(err, &adapterError) && adapterError.Code == "configuration_conflict":
		api.writeError(writer, request, http.StatusConflict, "CONFIGURATION_CONFLICT", "provider configuration changed; refresh and retry")
	case errors.As(err, &adapterError) && adapterError.Code == "model_unavailable":
		api.writeError(writer, request, http.StatusConflict, "MODEL_UNAVAILABLE", "the selected model is unavailable")
	case errors.As(err, &adapterError):
		api.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "provider configuration was rejected")
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

func validRuntimeID(value string) bool {
	return len(value) > 0 && len(value) <= agent.MaxRuntimeIDBytes && validRouteID(value)
}

func validProviderID(value string) bool {
	return len(value) > 0 && len(value) <= agent.MaxProviderIDBytes && validRouteID(value)
}

func validRouteID(value string) bool {
	for index, character := range value {
		if index == 0 && (character < 'a' || character > 'z') {
			return false
		}
		if index > 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}
