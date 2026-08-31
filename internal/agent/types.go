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
	MaxRuntimeIDBytes   = 64
	MaxProviderIDBytes  = 128
	MaxModelIDBytes     = 256
	MaxDisplayNameBytes = 256
	MaxBaseURLBytes     = 2048
	MaxReasoningIDBytes = 64
	MaxProviderModels   = 256
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

// TurnPreflighter is an optional value-free readiness check performed after
// the Runtime Session is prepared and before a user message is persisted.
// Provider adapters use it only for actionable configuration state; the
// provider remains responsible for the real Turn.
type TurnPreflighter interface {
	PreflightTurn(context.Context, RunRequest) error
}

// RuntimeSessionAdapter is the optional rich Session configuration capability
// implemented by Runtimes with a native provider/model directory. The opaque
// external Session ID is minted and interpreted only by that Runtime.
type RuntimeSessionAdapter interface {
	PrepareSession(context.Context, string) (string, error)
	Models(context.Context, string) (ModelDirectory, error)
	SelectModel(context.Context, string, ModelSelection) (ModelSelection, error)
}

// TargetRuntimeSessionAdapter optionally lets a Runtime choose a target-aware
// profile while retaining the legacy PrepareSession contract.
type TargetRuntimeSessionAdapter interface {
	PrepareSessionForTarget(context.Context, string, ExecutionTarget) (string, error)
}

// RuntimeConfigurationAdapter is the optional Host-level provider
// configuration capability. All returned credential data is structurally
// value-free; mutations carry a secret only in the write direction.
type RuntimeConfigurationAdapter interface {
	ProviderDirectory(context.Context) (RuntimeProviderDirectory, error)
	ConfigureProvider(context.Context, RuntimeProviderMutation) error
	RemoveProvider(context.Context, string, int64) error
}

type RunRequest struct {
	SessionID         string
	ExternalSessionID string
	UserText          string
	History           []ConversationMessage
	RemoteExec        RemoteExecInvoker
	// Target is resolved from the owner-scoped Device immediately before the
	// Turn. Adapters may use it to select a target-aware execution profile, but
	// never to select or rewrite the Device itself.
	Target ExecutionTarget
}

// ExecutionTarget is value-only metadata for provider adapters. Platform and
// Arch originate from the authenticated, owner-scoped Device record.
type ExecutionTarget struct {
	Platform string
	Arch     string
}

// CredentialStatus is the value-free provider credential projection shared by
// Runtime configuration and current-Session readiness surfaces.
type CredentialStatus struct {
	Configured bool
	Writable   bool
}

// ModelSelection is the complete model route applied to the next Runtime
// request assembled for a Session.
type ModelSelection struct {
	Provider        string
	Model           string
	ReasoningEffort string
}

// ModelReasoningEffort is one adapter-owned effort accepted by an exact model.
type ModelReasoningEffort struct {
	ID          string
	Name        string
	Description string
}

// RuntimeModel describes one selectable model without provider secrets or
// provider-specific request configuration.
type RuntimeModel struct {
	ID                     string
	Name                   string
	Description            string
	ContextWindow          int64
	MaxTokens              int64
	ReasoningEfforts       []ModelReasoningEffort
	DefaultReasoningEffort string
}

// ModelProviderGroup is one provider and its bounded selectable model catalog.
type ModelProviderGroup struct {
	ID     string
	Name   string
	Models []RuntimeModel
}

// ModelCatalogFailure keeps one provider-local catalog failure from hiding
// otherwise usable provider groups.
type ModelCatalogFailure struct {
	ID      string
	Name    string
	Message string
}

// ModelDirectory is the Runtime-owned selection and catalog for one product
// Session. CurrentCredential is nil when the route names no managed reference.
type ModelDirectory struct {
	Current           ModelSelection
	Routable          bool
	Groups            []ModelProviderGroup
	Failures          []ModelCatalogFailure
	CurrentCredential *CredentialStatus
}

// RuntimeProviderModel is the curated model metadata exposed in provider
// settings. Zero capacities mean the Runtime owns the default.
type RuntimeProviderModel struct {
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
}

// RuntimeProviderProfile is one redacted configurable provider route. Family
// identifies the owning adapter schema, not a wire protocol.
type RuntimeProviderProfile struct {
	ID               string
	DisplayName      string
	Family           string
	Active           bool
	Configured       bool
	Custom           bool
	Removable        bool
	Revision         int64
	BaseURL          string
	API              string
	Models           []RuntimeProviderModel
	ModelsOverridden bool
	Credential       *CredentialStatus
}

// RuntimeProviderDirectory is the redacted Host-level provider configuration
// view. CustomProviderRevision belongs to the namespace used for a new route.
type RuntimeProviderDirectory struct {
	Runtime                string
	DisplayName            string
	Writable               bool
	CustomProviderRevision int64
	Protocols              []string
	Providers              []RuntimeProviderProfile
}

// RuntimeProviderMutation is the curated desired profile. Empty optional
// strings remove the corresponding user override; a nil Models slice restores
// the Runtime catalog, while a non-nil slice is an explicit catalog.
type RuntimeProviderMutation struct {
	Provider         string
	ExpectedRevision int64
	DisplayName      string
	BaseURL          string
	API              string
	Models           []RuntimeProviderModel
	ModelsOverridden bool
	APIKey           string
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
