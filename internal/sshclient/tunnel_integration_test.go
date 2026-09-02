//go:build linux

package sshclient

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/tunnel"
)

// TestTunnelSSHEndToEnd crosses the real Task002 composition seam without a
// warmup stream: Manager.OpenSSH -> typed Remote dispatch -> Embedded SSHD ->
// strict SSH Dialer. It also makes Tunnel shutdown wait for Remote child reap.
func TestTunnelSSHEndToEnd(t *testing.T) {
	ctx := context.Background()
	deviceIdentity, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := deviceIdentity.SSHSigner()
	if err != nil {
		t.Fatal(err)
	}
	remoteSSHD, err := sshserver.New(hostSigner)
	if err != nil {
		t.Fatal(err)
	}
	deviceStore := newTunnelSSHStore()
	manager := tunnel.NewManager()
	gateway, err := tunnel.NewGateway(tunnel.GatewayOptions{
		Store: deviceStore, Pairing: staticPairingOfferer{}, Manager: manager,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway)
	t.Cleanup(func() {
		server.Close()
		gateway.Close()
	})

	clientContext, cancelClient := context.WithCancel(ctx)
	t.Cleanup(cancelClient)
	client, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: server.URL, DevMode: true, Identity: deviceIdentity,
		DeviceName: "ssh-integration", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), StableOnline: time.Hour,
		Jitter: func(time.Duration) time.Duration { return time.Hour },
		StreamHandler: func(handlerContext context.Context, stream net.Conn, header protocol.StreamHeader, session tunnel.ClientSession) {
			if header.Kind != protocol.StreamSSH {
				_ = stream.Close()
				return
			}
			_ = remoteSSHD.Serve(handlerContext, stream, session.SSHClientPublicKey)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	clientDone := make(chan error, 1)
	go func() { clientDone <- client.Run(clientContext) }()
	waitForTunnelOnline(t, manager, deviceIdentity.DeviceID)

	dialer, err := NewDialer(manager, DeviceKeyLookupFunc(func(context.Context, string) (ed25519.PublicKey, error) {
		return append(ed25519.PublicKey(nil), deviceIdentity.PublicKey...), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	execResult, err := dialer.Exec(ctx, deviceIdentity.DeviceID, "printf stdout; printf stderr >&2; exit 17", ExecOptions{Platform: protocol.PlatformLinux})
	if err != nil {
		t.Fatal(err)
	}
	if string(execResult.Stdout) != "stdout" || string(execResult.Stderr) != "stderr" || execResult.ExitCode != 17 {
		t.Fatalf("full-chain exec result = %+v", execResult)
	}

	ptyHandle, err := dialer.OpenPTY(ctx, deviceIdentity.DeviceID, PTYOptions{Cols: 91, Rows: 27, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ptyOutput := &lockedBuffer{}
	ptyCopyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(ptyOutput, ptyHandle.Output())
		close(ptyCopyDone)
	}()
	if _, err := io.WriteString(ptyHandle.Input(), "stty -echo; printf '__FIRST__ '; stty size\n"); err != nil {
		t.Fatal(err)
	}
	waitForOutputCount(t, ptyOutput, "27 91", 1)
	if err := ptyHandle.Resize(123, 45); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(ptyHandle.Input(), "printf '__SECOND__ '; stty size\n"); err != nil {
		t.Fatal(err)
	}
	waitForOutputCount(t, ptyOutput, "45 123", 1)
	_ = ptyHandle.Close()
	select {
	case <-ptyCopyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("full-chain PTY copy did not exit")
	}

	// Sentinel is same UID but outside the Remote PTY/exec process scope. The
	// pidfd sweep must never signal it.
	sentinel := exec.Command("sleep", "30")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Process.Kill()
		_ = sentinel.Wait()
	})

	contextDirectory := t.TempDir()
	execContext, cancelExec := context.WithCancel(ctx)
	contextExecDone := make(chan error, 1)
	go func() {
		_, execErr := dialer.Exec(
			execContext, deviceIdentity.DeviceID,
			"sleep 30 & echo $! > child.pid; wait",
			ExecOptions{CWD: contextDirectory, Platform: protocol.PlatformLinux},
		)
		contextExecDone <- execErr
	}()
	contextChildPID := waitForPIDFile(t, filepath.Join(contextDirectory, "child.pid"))
	cancelExec()
	select {
	case err := <-contextExecDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("full-chain Exec context cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("full-chain Exec context cancellation remained blocked")
	}
	waitForProcessGone(t, contextChildPID)
	if !manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("canceling one SSH Exec closed the Device Tunnel")
	}

	directory := t.TempDir()
	execDone := make(chan error, 1)
	go func() {
		_, execErr := dialer.Exec(ctx, deviceIdentity.DeviceID, "sleep 30 & echo $! > child.pid; wait", ExecOptions{CWD: directory, Platform: protocol.PlatformLinux})
		execDone <- execErr
	}()
	childPID := waitForPIDFile(t, filepath.Join(directory, "child.pid"))
	cancelClient()
	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Tunnel client did not return after joined SSH handler cleanup")
	}
	select {
	case <-execDone:
	case <-time.After(time.Second):
		t.Fatal("full-chain exec remained blocked after Tunnel cancellation")
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Remote child %d survived joined Tunnel shutdown: %v", childPID, err)
	}
	if err := syscall.Kill(sentinel.Process.Pid, 0); err != nil {
		t.Fatalf("unrelated sentinel was signaled: %v", err)
	}
}

type tunnelSSHStore struct {
	device store.Device
}

func newTunnelSSHStore() *tunnelSSHStore { return &tunnelSSHStore{} }

func (value *tunnelSSHStore) RegisterDevice(_ context.Context, device store.Device) (store.Device, error) {
	value.device = device
	return device, nil
}

func (*tunnelSSHStore) UpdateDeviceLastSeen(context.Context, string, time.Time) error { return nil }

type staticPairingOfferer struct{}

func (staticPairingOfferer) Offer(_ context.Context, _ string, now time.Time) (pairing.Offer, error) {
	return pairing.Offer{Code: "K7HF-92PQ", ExpiresAt: now.Add(10 * time.Minute)}, nil
}

func waitForTunnelOnline(t *testing.T, manager *tunnel.Manager, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if manager.IsOnline(deviceID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Device did not become online")
}
