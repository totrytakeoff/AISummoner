// Package agent owns the product Agent session, approval, remote-tool and live
// event state. It has no local command execution capability.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	ProviderFake     = "fake"
	ProviderDeepSeek = "deepseek"
	ProviderDSH      = "dsh"

	ToolRemoteExec = "remote_exec"

	DecisionApproveOnce    = "approve_once"
	DecisionApproveSession = "approve_session"
	DecisionDeny           = "deny"

	EventSessionState   = "session.state"
	EventReasoningDelta = "response.reasoning.delta"
	EventReasoningDone  = "response.reasoning.done"
	EventTextDelta      = "response.text.delta"
	EventTextDone       = "response.text.done"
	EventToolPending    = "tool_call.pending"
	EventToolStarted    = "tool_call.started"
	EventToolOutput     = "tool_call.output"
	EventToolCompleted  = "tool_call.completed"
	EventTurnCompleted  = "turn.completed"
	EventTurnFailed     = "turn.failed"

	FailureDeviceOffline   = "DEVICE_OFFLINE"
	FailureDeviceNotFound  = "DEVICE_NOT_FOUND"
	FailureCommandDenied   = "COMMAND_DENIED"
	FailureApprovalTimeout = "APPROVAL_TIMEOUT"
	FailureExecTimeout     = "REMOTE_EXEC_TIMEOUT"
	FailureExecCanceled    = "REMOTE_EXEC_CANCELED"
	FailureExecTransport   = "REMOTE_EXEC_TRANSPORT"
)

const (
	MaxMessageBytes     = 32 * 1024
	MaxCommandBytes     = 8192
	MaxCWDBytes         = 4096
	MaxToolOutputBytes  = 256 * 1024
	MaxOutputExcerpt    = 8192
	DefaultExecTimeout  = 30 * time.Second
	MaximumExecTimeout  = 60 * time.Second
	MinimumExecTimeout  = time.Second
	DefaultTurnTimeout  = 5 * time.Minute
	DefaultApprovalWait = 2 * time.Minute
)

var (
	ErrNotFound        = errors.New("agent resource not found")
	ErrInvalidRequest  = errors.New("invalid agent request")
	ErrInvalidState    = errors.New("invalid agent state")
	ErrTurnInProgress  = errors.New("agent turn already in progress")
	ErrDeviceOffline   = errors.New("device is offline")
	ErrApprovalTimeout = errors.New("tool approval timed out")
	ErrInvalidTool     = errors.New("invalid tool request")
	ErrServiceClosed   = errors.New("agent service is closed")
)

// Adapter is the provider-neutral boundary implemented by deterministic Fake,
// the DSH/OpenCode sidecars, and direct provider integrations such as DeepSeek.
type Adapter interface {
	Run(context.Context, RunRequest, EventSink) error
}

// TurnPreflighter is an optional value-free readiness check performed before a
// user message is persisted. Provider adapters use it only for actionable
// configuration state; the provider remains responsible for the real Turn.
type TurnPreflighter interface {
	PreflightTurn(context.Context) error
}

type RunRequest struct {
	SessionID         string
	ExternalSessionID string
	UserText          string
	History           []ConversationMessage
	RemoteExec        RemoteExecInvoker
}

// ConversationMessage is the provider-neutral, bounded model history derived
// from the Server-owned transcript. Reasoning and tool internals never enter
// this cross-provider history representation.
type ConversationMessage struct {
	Role    string
	Content string
}

// EventSink is provider-facing. Product tool events are emitted by the
// invoker, not by an untrusted provider.
type EventSink interface {
	ReasoningDelta(context.Context, string) error
	TextDelta(context.Context, string) error
	ProviderState(context.Context, string) error
	SetExternalSessionID(context.Context, string) error
}

type RemoteExecInvoker interface {
	Invoke(context.Context, ToolRequest) (ToolResult, error)
}

type ToolRequest struct {
	Name      string
	Arguments json.RawMessage
}

type RemoteExecArguments struct {
	Command   string `json:"command"`
	CWD       string `json:"cwd,omitempty"`
	TimeoutMS int    `json:"timeout_ms"`
}

// RemoteExecutor is the only execution boundary available to Agent code. A
// production implementation will use strict SSH; tests inject a deterministic
// fake. Device selection is supplied by the Service, never by the Adapter.
type RemoteExecutor interface {
	Exec(ctx context.Context, deviceID, command, cwd string) (RemoteExecution, error)
}

type RemoteExecution struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExecutionError lets a RemoteExecutor preserve a stable transport failure
// code without exposing its internal error details to logs or browsers.
type ExecutionError struct {
	Code string
	Err  error
}

// AdapterError is the safe provider failure boundary used by task007. Code is
// normalized by the Service; Err details are never emitted or logged.
type AdapterError struct {
	Code string
	Err  error
}

func (e *AdapterError) Error() string {
	if e == nil || e.Code == "" {
		return "agent provider failed"
	}
	return "agent provider failed: " + e.Code
}

func (e *AdapterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ExecutionError) Error() string {
	if e == nil || e.Code == "" {
		return "remote execution failed"
	}
	return "remote execution failed: " + e.Code
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ToolFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ToolResult struct {
	ToolCallID string       `json:"tool_call_id"`
	Stdout     string       `json:"stdout"`
	Stderr     string       `json:"stderr"`
	ExitCode   int          `json:"exit_code"`
	Truncated  bool         `json:"truncated"`
	Denied     bool         `json:"denied"`
	Failure    *ToolFailure `json:"failure,omitempty"`
}

type Event struct {
	ID        string          `json:"event_id"`
	SessionID string          `json:"session_id"`
	CreatedAt time.Time       `json:"created_at"`
	Type      string          `json:"-"`
	Payload   json.RawMessage `json:"payload"`
}

func (e Event) Validate() error {
	if e.ID == "" || e.SessionID == "" || e.CreatedAt.IsZero() || !knownEventType(e.Type) || !json.Valid(e.Payload) {
		return fmt.Errorf("invalid agent event")
	}
	return nil
}

func knownEventType(eventType string) bool {
	switch eventType {
	case EventSessionState, EventReasoningDelta, EventReasoningDone, EventTextDelta, EventTextDone, EventToolPending, EventToolStarted,
		EventToolOutput, EventToolCompleted, EventTurnCompleted, EventTurnFailed:
		return true
	default:
		return false
	}
}
