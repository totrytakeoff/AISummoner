package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/sshclient"
	"github.com/aisummoner/aisummoner/internal/store"
)

type alwaysOnline struct{}

func (alwaysOnline) IsOnline(string) bool { return true }

type resultCapturingAdapter struct {
	results chan agent.ToolResult
}

func (adapter *resultCapturingAdapter) Run(ctx context.Context, request agent.RunRequest, _ agent.EventSink) error {
	arguments, err := json.Marshal(agent.RemoteExecArguments{Command: "large", TimeoutMS: 30_000})
	if err != nil {
		return err
	}
	result, err := request.RemoteExec.Invoke(ctx, agent.ToolRequest{Name: agent.ToolRemoteExec, Arguments: arguments})
	if err != nil {
		return err
	}
	adapter.results <- result
	return nil
}

func TestTerminalOpenerMapsDimensionsAndEmptyCWD(t *testing.T) {
	sentinel := errors.New("open PTY sentinel")
	called := false
	opener := terminalOpener(func(ctx context.Context, deviceID string, options sshclient.PTYOptions) (*sshclient.PTY, error) {
		called = true
		if ctx == nil || deviceID != "dev_one" || options.Cols != 121 || options.Rows != 37 || options.CWD != "" {
			t.Fatalf("OpenPTY arguments = context=%v device=%q options=%+v", ctx, deviceID, options)
		}
		return nil, sentinel
	})
	_, err := opener(context.Background(), "dev_one", 121, 37)
	if !called || !errors.Is(err, sentinel) {
		t.Fatalf("Terminal opener called=%v error=%v", called, err)
	}
}

func TestRemoteExecutorMapsCaptureSentinelAndResult(t *testing.T) {
	contextValue := struct{}{}
	ctx := context.WithValue(context.Background(), contextValue, "preserved")
	executor := &remoteExecutor{exec: func(actualCtx context.Context, deviceID, command string, options sshclient.ExecOptions) (sshclient.ExecResult, error) {
		if actualCtx.Value(contextValue) != "preserved" || deviceID != "dev_one" || command != "uname -a" ||
			options.CWD != "/var/tmp" || options.CaptureLimit != agent.MaxToolOutputBytes+1 {
			t.Fatalf("SSH Exec mapping = ctx=%v device=%q command=%q options=%+v", actualCtx, deviceID, command, options)
		}
		return sshclient.ExecResult{Stdout: []byte("out"), Stderr: []byte("err"), ExitCode: 17}, nil
	}}
	result, err := executor.Exec(ctx, "dev_one", "uname -a", "/var/tmp")
	if err != nil || string(result.Stdout) != "out" || string(result.Stderr) != "err" || result.ExitCode != 17 {
		t.Fatalf("Agent execution mapping = %+v, %v", result, err)
	}
}

func TestRemoteExecutorCaptureSentinelBecomesAgentTruncation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "ssh-adapter.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_ssh_adapter", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := "dev_ssh_adapter"
	if _, err := database.RegisterDevice(ctx, store.Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x5a}, 32), OwnerUserID: &owner.ID,
		Name: "adapter host", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	sourceCalled := false
	executor := &remoteExecutor{exec: func(_ context.Context, gotDeviceID, command string, options sshclient.ExecOptions) (sshclient.ExecResult, error) {
		sourceCalled = true
		if gotDeviceID != deviceID || command != "large" || options.CaptureLimit != agent.MaxToolOutputBytes+1 {
			t.Fatalf("SSH execution boundary = device=%q command=%q options=%+v", gotDeviceID, command, options)
		}
		return sshclient.ExecResult{
			Stdout: bytes.Repeat([]byte("z"), agent.MaxToolOutputBytes+1), ExitCode: 23,
			StdoutTruncated: true,
		}, nil
	}}
	adapter := &resultCapturingAdapter{results: make(chan agent.ToolResult, 1)}
	service, err := agent.NewService(agent.ServiceOptions{
		Store: database, Adapter: adapter, Provider: agent.ProviderFake,
		Executor: executor, Online: alwaysOnline{}, TurnTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	session, err := service.CreateSession(ctx, owner.ID, deviceID, store.AgentApprovalFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, owner.ID, session.ID, "run large output"); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-adapter.results:
		if !sourceCalled || !result.Truncated || len([]byte(result.Stdout)) != agent.MaxToolOutputBytes || result.ExitCode != 23 {
			t.Fatalf("Agent truncation result = called=%v bytes=%d truncated=%v exit=%d", sourceCalled, len([]byte(result.Stdout)), result.Truncated, result.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Agent did not return the SSH truncation result")
	}
}

func TestRemoteExecutorPreservesTransportError(t *testing.T) {
	sentinel := errors.New("transport sentinel")
	executor := &remoteExecutor{exec: func(context.Context, string, string, sshclient.ExecOptions) (sshclient.ExecResult, error) {
		return sshclient.ExecResult{}, sentinel
	}}
	result, err := executor.Exec(context.Background(), "dev_one", "true", "")
	if !errors.Is(err, sentinel) || len(result.Stdout) != 0 || len(result.Stderr) != 0 || result.ExitCode != 0 {
		t.Fatalf("transport error mapping = %+v, %v", result, err)
	}
}

func TestSSHAdapterConstructorsRejectNilDialer(t *testing.T) {
	if opener, err := NewTerminalOpener(nil); err == nil || opener != nil {
		t.Fatal("nil Terminal SSH dialer was accepted")
	}
	if executor, err := NewRemoteExecutor(nil); err == nil || executor != nil {
		t.Fatal("nil Agent SSH dialer was accepted")
	}
}
