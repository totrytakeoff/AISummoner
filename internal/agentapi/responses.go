package agentapi

import (
	"time"

	"github.com/aisummoner/aisummoner/internal/store"
)

type sessionJSON struct {
	ID                string  `json:"id"`
	DeviceID          string  `json:"device_id"`
	ApprovalMode      string  `json:"approval_mode"`
	Provider          string  `json:"provider"`
	ExternalSessionID *string `json:"external_session_id"`
	State             string  `json:"state"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

func sessionResponse(session store.AgentSession) sessionJSON {
	return sessionJSON{
		ID: session.ID, DeviceID: session.DeviceID, ApprovalMode: session.ApprovalMode,
		Provider: session.Provider, ExternalSessionID: session.ExternalSessionID, State: session.State,
		CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: session.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
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
