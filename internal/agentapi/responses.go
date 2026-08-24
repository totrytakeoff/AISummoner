package agentapi

import (
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/store"
)

type credentialStatusJSON struct {
	Configured bool `json:"configured"`
	Writable   bool `json:"writable"`
}

type runtimeProviderModelJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int64  `json:"context_window,omitempty"`
	MaxTokens     int64  `json:"max_tokens,omitempty"`
}

type runtimeProviderProfileJSON struct {
	ID               string                     `json:"id"`
	DisplayName      string                     `json:"display_name"`
	Family           string                     `json:"family"`
	Active           bool                       `json:"active"`
	Configured       bool                       `json:"configured"`
	Custom           bool                       `json:"custom"`
	Removable        bool                       `json:"removable"`
	Revision         int64                      `json:"revision"`
	BaseURL          string                     `json:"base_url,omitempty"`
	API              string                     `json:"api,omitempty"`
	Models           []runtimeProviderModelJSON `json:"models"`
	ModelsOverridden bool                       `json:"models_overridden"`
	Credential       *credentialStatusJSON      `json:"credential,omitempty"`
}

func runtimeProviderDirectoryResponse(directory agent.RuntimeProviderDirectory) map[string]any {
	providers := make([]runtimeProviderProfileJSON, 0, len(directory.Providers))
	for _, provider := range directory.Providers {
		models := make([]runtimeProviderModelJSON, 0, len(provider.Models))
		for _, model := range provider.Models {
			models = append(models, runtimeProviderModelJSON{
				ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
			})
		}
		var credential *credentialStatusJSON
		if provider.Credential != nil {
			credential = &credentialStatusJSON{Configured: provider.Credential.Configured, Writable: provider.Credential.Writable}
		}
		providers = append(providers, runtimeProviderProfileJSON{
			ID: provider.ID, DisplayName: provider.DisplayName, Family: provider.Family,
			Active: provider.Active, Configured: provider.Configured, Custom: provider.Custom,
			Removable: provider.Removable, Revision: provider.Revision,
			BaseURL: provider.BaseURL, API: provider.API, Models: models,
			ModelsOverridden: provider.ModelsOverridden, Credential: credential,
		})
	}
	return map[string]any{"runtime": map[string]any{
		"id": directory.Runtime, "display_name": directory.DisplayName,
		"writable": directory.Writable, "custom_provider_revision": directory.CustomProviderRevision,
		"protocols": directory.Protocols, "providers": providers,
	}}
}

type modelSelectionJSON struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type modelReasoningEffortJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type runtimeModelJSON struct {
	ID                     string                     `json:"id"`
	Name                   string                     `json:"name"`
	Description            string                     `json:"description,omitempty"`
	ContextWindow          int64                      `json:"context_window,omitempty"`
	MaxTokens              int64                      `json:"max_tokens,omitempty"`
	ReasoningEfforts       []modelReasoningEffortJSON `json:"reasoning_efforts"`
	DefaultReasoningEffort string                     `json:"default_reasoning_effort,omitempty"`
}

func modelSelectionResponse(selection agent.ModelSelection) modelSelectionJSON {
	return modelSelectionJSON{
		Provider: selection.Provider, Model: selection.Model, ReasoningEffort: selection.ReasoningEffort,
	}
}

func modelDirectoryResponse(directory agent.ModelDirectory) map[string]any {
	groups := make([]map[string]any, 0, len(directory.Groups))
	for _, group := range directory.Groups {
		models := make([]runtimeModelJSON, 0, len(group.Models))
		for _, model := range group.Models {
			efforts := make([]modelReasoningEffortJSON, 0, len(model.ReasoningEfforts))
			for _, effort := range model.ReasoningEfforts {
				efforts = append(efforts, modelReasoningEffortJSON{ID: effort.ID, Name: effort.Name, Description: effort.Description})
			}
			models = append(models, runtimeModelJSON{
				ID: model.ID, Name: model.Name, Description: model.Description,
				ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
				ReasoningEfforts: efforts, DefaultReasoningEffort: model.DefaultReasoningEffort,
			})
		}
		groups = append(groups, map[string]any{"id": group.ID, "name": group.Name, "models": models})
	}
	failures := make([]map[string]string, 0, len(directory.Failures))
	for _, failure := range directory.Failures {
		failures = append(failures, map[string]string{"id": failure.ID, "name": failure.Name, "message": failure.Message})
	}
	response := map[string]any{
		"current": modelSelectionResponse(directory.Current), "routable": directory.Routable,
		"groups": groups, "failures": failures,
	}
	if directory.CurrentCredential != nil {
		response["current_credential"] = credentialStatusJSON{
			Configured: directory.CurrentCredential.Configured, Writable: directory.CurrentCredential.Writable,
		}
	}
	return response
}

type sessionJSON struct {
	ID                string  `json:"id"`
	DeviceID          string  `json:"device_id"`
	ApprovalMode      string  `json:"approval_mode"`
	Provider          string  `json:"provider"`
	ExternalSessionID *string `json:"external_session_id"`
	State             string  `json:"state"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	ArchivedAt        *string `json:"archived_at"`
}

func sessionResponse(session store.AgentSession) sessionJSON {
	return sessionJSON{
		ID: session.ID, DeviceID: session.DeviceID, ApprovalMode: session.ApprovalMode,
		Provider: session.Provider, ExternalSessionID: session.ExternalSessionID, State: session.State,
		CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ArchivedAt: formatOptionalTime(session.ArchivedAt),
	}
}

type sessionSummaryJSON struct {
	ID           string  `json:"id"`
	DeviceID     string  `json:"device_id"`
	DeviceName   string  `json:"device_name"`
	ApprovalMode string  `json:"approval_mode"`
	Provider     string  `json:"provider"`
	State        string  `json:"state"`
	Title        string  `json:"title"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ArchivedAt   *string `json:"archived_at"`
}

func sessionSummaryResponse(summary store.AgentSessionSummary) sessionSummaryJSON {
	return sessionSummaryJSON{
		ID: summary.ID, DeviceID: summary.DeviceID, DeviceName: summary.DeviceName,
		ApprovalMode: summary.ApprovalMode, Provider: summary.Provider,
		State: summary.State, Title: summary.Title,
		CreatedAt:  summary.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:  summary.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ArchivedAt: formatOptionalTime(summary.ArchivedAt),
	}
}

type settingsJSON struct {
	DefaultApprovalMode string  `json:"default_approval_mode"`
	UpdatedAt           *string `json:"updated_at"`
}

func settingsResponse(settings store.AgentSettings) settingsJSON {
	return settingsJSON{DefaultApprovalMode: settings.DefaultApprovalMode, UpdatedAt: formatOptionalTime(settings.UpdatedAt)}
}

type messageJSON struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func messageResponse(message store.AgentMessage) messageJSON {
	return messageJSON{
		ID: message.ID, Role: message.Role, Content: message.Content,
		CreatedAt: message.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

type toolCallJSON struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ArgumentsJSON string  `json:"arguments_json"`
	Status        string  `json:"status"`
	Decision      *string `json:"decision"`
	ExitCode      *int    `json:"exit_code"`
	OutputExcerpt *string `json:"output_excerpt"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   *string `json:"completed_at"`
}

func toolCallResponse(toolCall store.ToolCall) toolCallJSON {
	return toolCallJSON{
		ID: toolCall.ID, Name: toolCall.Name, ArgumentsJSON: toolCall.ArgumentsJSON,
		Status: toolCall.Status, Decision: toolCall.Decision, ExitCode: toolCall.ExitCode,
		OutputExcerpt: toolCall.OutputExcerpt, CreatedAt: toolCall.CreatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt: formatOptionalTime(toolCall.CompletedAt),
	}
}

func snapshotResponse(snapshot store.AgentSnapshot) map[string]any {
	messages := make([]messageJSON, 0, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		messages = append(messages, messageResponse(message))
	}
	toolCalls := make([]toolCallJSON, 0, len(snapshot.ToolCalls))
	for _, toolCall := range snapshot.ToolCalls {
		toolCalls = append(toolCalls, toolCallResponse(toolCall))
	}
	return map[string]any{
		"session": sessionResponse(snapshot.Session), "messages": messages, "tool_calls": toolCalls,
	}
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}
