package app

import (
	"context"
	"errors"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/sshclient"
	"github.com/aisummoner/aisummoner/internal/terminal"
)

// NewTerminalOpener is the explicit covariance boundary between Task003's
// concrete PTY handle and Task005's interface. No SSH implementation detail is
// added to Terminal itself.
func NewTerminalOpener(dialer *sshclient.Dialer) (terminal.OpenPTYFunc, error) {
	if dialer == nil {
		return nil, errors.New("SSH dialer is required for Terminal")
	}
	return terminalOpener(dialer.OpenPTY), nil
}

type concretePTYOpener func(context.Context, string, sshclient.PTYOptions) (*sshclient.PTY, error)

func terminalOpener(open concretePTYOpener) terminal.OpenPTYFunc {
	return func(ctx context.Context, deviceID string, cols, rows uint16) (terminal.PTY, error) {
		handle, err := open(ctx, deviceID, sshclient.PTYOptions{Cols: cols, Rows: rows})
		if err != nil {
			return nil, err
		}
		return handle, nil
	}
}

type sshExecFunc func(context.Context, string, string, sshclient.ExecOptions) (sshclient.ExecResult, error)

type remoteExecutor struct {
	exec sshExecFunc
}

// NewRemoteExecutor maps the Agent's only execution boundary onto strict SSH.
// The +1 capture byte is a sentinel so Agent's own aggregate truncator can
// report the product-level truncated flag honestly.
func NewRemoteExecutor(dialer *sshclient.Dialer) (agent.RemoteExecutor, error) {
	if dialer == nil {
		return nil, errors.New("SSH dialer is required for Agent")
	}
	return &remoteExecutor{exec: dialer.Exec}, nil
}

func (executor *remoteExecutor) Exec(ctx context.Context, deviceID, command, cwd string) (agent.RemoteExecution, error) {
	result, err := executor.exec(ctx, deviceID, command, sshclient.ExecOptions{
		CWD: cwd, CaptureLimit: agent.MaxToolOutputBytes + 1,
	})
	if err != nil {
		return agent.RemoteExecution{}, err
	}
	return agent.RemoteExecution{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode}, nil
}
