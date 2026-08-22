package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type FakeStepKind string

const (
	FakeText       FakeStepKind = "text"
	FakeTool       FakeStepKind = "tool"
	FakeSetSession FakeStepKind = "set_external_session"
	FakeFail       FakeStepKind = "fail"
)

type FakeStep struct {
	Kind      FakeStepKind
	Text      string
	Tool      ToolRequest
	SessionID string
	Err       error
}

// FakeAdapter is deterministic test/product fallback infrastructure. It can
// only emit text/state or invoke the supplied RemoteExec boundary.
type FakeAdapter struct {
	Steps []FakeStep
}

func (adapter *FakeAdapter) Run(ctx context.Context, request RunRequest, sink EventSink) error {
	steps := adapter.Steps
	if len(steps) == 0 {
		hostnameArguments, err := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: int(DefaultExecTimeout.Milliseconds())})
		if err != nil {
			return err
		}
		unameArguments, err := json.Marshal(RemoteExecArguments{Command: "uname -a", TimeoutMS: int(DefaultExecTimeout.Milliseconds())})
		if err != nil {
			return err
		}
		hostname, err := request.RemoteExec.Invoke(ctx, ToolRequest{Name: ToolRemoteExec, Arguments: hostnameArguments})
		if err != nil {
			return err
		}
		if hostname.Failure != nil {
			if hostname.Failure.Code == FailureDeviceOffline || hostname.Failure.Code == FailureDeviceNotFound {
				return ErrDeviceOffline
			}
			return &ExecutionError{Code: hostname.Failure.Code, Err: errors.New(hostname.Failure.Message)}
		}
		system, err := request.RemoteExec.Invoke(ctx, ToolRequest{Name: ToolRemoteExec, Arguments: unameArguments})
		if err != nil {
			return err
		}
		if system.Failure != nil {
			if system.Failure.Code == FailureDeviceOffline || system.Failure.Code == FailureDeviceNotFound {
				return ErrDeviceOffline
			}
			return &ExecutionError{Code: system.Failure.Code, Err: errors.New(system.Failure.Message)}
		}
		response := fmt.Sprintf(
			"Remote hostname: %s (exit %d, truncated=%t). Remote system: %s (exit %d, truncated=%t).",
			evidence(hostname), hostname.ExitCode, hostname.Truncated,
			evidence(system), system.ExitCode, system.Truncated,
		)
		return sink.TextDelta(ctx, response)
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch step.Kind {
		case FakeText:
			if err := sink.TextDelta(ctx, step.Text); err != nil {
				return err
			}
		case FakeTool:
			result, err := request.RemoteExec.Invoke(ctx, step.Tool)
			if err != nil {
				return err
			}
			if result.Failure != nil && result.Failure.Code != FailureCommandDenied {
				return &ExecutionError{Code: result.Failure.Code, Err: errors.New(result.Failure.Message)}
			}
		case FakeSetSession:
			if err := sink.SetExternalSessionID(ctx, step.SessionID); err != nil {
				return err
			}
		case FakeFail:
			if step.Err != nil {
				return step.Err
			}
			return errors.New("fake adapter failure")
		default:
			return fmt.Errorf("unknown fake adapter step %q", step.Kind)
		}
	}
	return nil
}

func evidence(result ToolResult) string {
	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		value = strings.TrimSpace(result.Stderr)
	}
	if value == "" {
		value = "<no output>"
	}
	return value
}
