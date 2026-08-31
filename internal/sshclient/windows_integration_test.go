//go:build windows

package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/tunnel"
	"github.com/aisummoner/aisummoner/internal/winprocess"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

func TestWindowsTunnelSSHPowerShellEndToEnd(t *testing.T) {
	fixture := newWindowsTunnelSSHFixture(t)
	directory := t.TempDir()
	result, err := fixture.dialer.Exec(context.Background(), fixture.deviceID, `
[Console]::Out.WriteLine("AIS_STDOUT_中文")
[Console]::Error.WriteLine("AIS_STDERR_中文")
[Console]::Out.WriteLine("AIS_CWD=" + (Get-Location).Path)
exit 17
`, ExecOptions{CWD: directory})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 17 || !bytes.Contains(result.Stdout, []byte("AIS_STDOUT_中文")) ||
		!bytes.Contains(result.Stderr, []byte("AIS_STDERR_中文")) {
		t.Fatalf("PowerShell exec result = %+v", result)
	}
	reportedDirectory, err := windowsMarkerText(result.Stdout, "AIS_CWD=")
	if err != nil {
		t.Fatal(err)
	}
	wantedInfo, wantedErr := os.Stat(directory)
	reportedInfo, reportedErr := os.Stat(reportedDirectory)
	if wantedErr != nil || reportedErr != nil || !os.SameFile(wantedInfo, reportedInfo) {
		t.Fatalf("PowerShell cwd %q is not %q: wanted_err=%v reported_err=%v",
			reportedDirectory, directory, wantedErr, reportedErr)
	}

	large, err := fixture.dialer.Exec(context.Background(), fixture.deviceID, `
$stdout = ('O' * 131072) -join ''
$stderr = ('E' * 131072) -join ''
[Console]::Out.Write($stdout)
[Console]::Error.Write($stderr)
exit 9
`, ExecOptions{CaptureLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if large.ExitCode != 9 || len(large.Stdout)+len(large.Stderr) != 100 ||
		(!large.StdoutTruncated && !large.StderrTruncated) {
		t.Fatalf("bounded PowerShell output = %+v", large)
	}

	for _, signal := range []struct {
		name       string
		exitStatus uint32
	}{{name: "TERM", exitStatus: 143}, {name: "KILL", exitStatus: 137}} {
		t.Run("signal_"+signal.name, func(t *testing.T) {
			connection, err := fixture.dialer.dial(context.Background(), fixture.deviceID)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			channel, requests, err := connection.client.OpenChannel("session", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer channel.Close()
			go io.Copy(io.Discard, channel)
			accepted, err := channel.SendRequest("exec", true, windowsMarshalSSHString("Start-Sleep -Seconds 120"))
			if err != nil || !accepted {
				t.Fatalf("Windows exec accepted=%v err=%v", accepted, err)
			}
			if signal.name == "TERM" {
				accepted, err = channel.SendRequest("signal", true, windowsMarshalSSHString("INT"))
				if err != nil || accepted {
					t.Fatalf("non-PTY Windows INT accepted=%v err=%v", accepted, err)
				}
			}
			accepted, err = channel.SendRequest("signal", true, windowsMarshalSSHString(signal.name))
			if err != nil || !accepted {
				t.Fatalf("Windows %s accepted=%v err=%v", signal.name, accepted, err)
			}
			waitForWindowsExitStatus(t, requests, signal.exitStatus)
		})
	}

	sentinelExecutable, err := winprocess.PowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := exec.Command(sentinelExecutable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 120")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Process.Kill()
		_ = sentinel.Wait()
	})

	cancelDirectory := t.TempDir()
	execContext, cancelExec := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, execErr := fixture.dialer.Exec(
			execContext, fixture.deviceID,
			windowsChildScript("cancel-child.pid", true), ExecOptions{CWD: cancelDirectory},
		)
		cancelled <- execErr
	}()
	cancelledPID := waitForWindowsPIDFile(t, filepath.Join(cancelDirectory, "cancel-child.pid"))
	cancelExec()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PowerShell cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PowerShell cancellation remained blocked")
	}
	waitForWindowsProcessGone(t, cancelledPID)
	if !fixture.manager.IsOnline(fixture.deviceID) {
		t.Fatal("canceling one Windows SSH exec closed the Device Tunnel")
	}
	if !windowsProcessStillRunning(uint32(sentinel.Process.Pid)) {
		t.Fatal("Job cancellation terminated an unrelated process")
	}

	backgroundDirectory := t.TempDir()
	background, err := fixture.dialer.Exec(
		context.Background(), fixture.deviceID,
		windowsChildScript("background-child.pid", false), ExecOptions{CWD: backgroundDirectory},
	)
	if err != nil || background.ExitCode != 0 {
		t.Fatalf("background-parent result=%+v err=%v", background, err)
	}
	backgroundPID := waitForWindowsPIDFile(t, filepath.Join(backgroundDirectory, "background-child.pid"))
	waitForWindowsProcessGone(t, backgroundPID)

	shutdownDirectory := t.TempDir()
	shutdownExec := make(chan error, 1)
	go func() {
		_, execErr := fixture.dialer.Exec(
			context.Background(), fixture.deviceID,
			windowsChildScript("shutdown-child.pid", true), ExecOptions{CWD: shutdownDirectory},
		)
		shutdownExec <- execErr
	}()
	shutdownPID := waitForWindowsPIDFile(t, filepath.Join(shutdownDirectory, "shutdown-child.pid"))
	if err := fixture.stopClient(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownExec:
	case <-time.After(5 * time.Second):
		t.Fatal("Windows SSH exec remained blocked after Tunnel shutdown")
	}
	waitForWindowsProcessGone(t, shutdownPID)
	if !windowsProcessStillRunning(uint32(sentinel.Process.Pid)) {
		t.Fatal("Tunnel shutdown terminated an unrelated process")
	}
}

func TestWindowsTunnelSSHConPTYEndToEnd(t *testing.T) {
	fixture := newWindowsTunnelSSHFixture(t)
	workingDirectory := t.TempDir()
	terminalContext, cancelTerminal := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTerminal()
	handle, capture, drained := openWindowsTestPTY(t, fixture, terminalContext, workingDirectory)
	defer handle.Close()
	waitForWindowsPTYBytes(t, terminalContext, capture, []byte("PS "))
	if err := handle.Resize(101, 37); err != nil {
		t.Fatal(err)
	}
	command := `$s=$Host.UI.RawUI.WindowSize; ` +
		`Write-Output ("AIS_PTY_"+"SIZE="+$s.Width+"x"+$s.Height); ` +
		`Write-Output ("AIS_PTY_"+"CWD="+(Get-Location).Path); ` +
		`Write-Output ("AIS_PTY_"+"READY_中文"); Start-Sleep -Seconds 120` + "\r"
	if _, err := io.WriteString(handle.Input(), command); err != nil {
		t.Fatal(err)
	}
	waitForWindowsPTYBytes(t, terminalContext, capture, []byte("AIS_PTY_READY_中文"))
	waitForWindowsPTYBytes(t, terminalContext, capture, []byte("AIS_PTY_SIZE=101x37"))
	promptsBeforeInterrupt := capture.Count([]byte("PS "))
	if _, err := handle.Input().Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	waitForWindowsPTYCount(t, terminalContext, capture, []byte("PS "), promptsBeforeInterrupt+1)
	if _, err := io.WriteString(
		handle.Input(), `Write-Output ("AIS_PTY_"+"DONE_中文"); exit 0`+"\r",
	); err != nil {
		t.Fatal(err)
	}
	waitForWindowsPTYBytes(t, terminalContext, capture, []byte("AIS_PTY_DONE_中文"))
	if err := waitForWindowsPTYResult(t, handle, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForWindowsPTYDrain(t, drained, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	reportedDirectory, err := windowsMarkerText(capture.Bytes(), "AIS_PTY_CWD=")
	if err != nil {
		t.Fatal(err)
	}
	wantedInfo, wantedErr := os.Stat(workingDirectory)
	reportedInfo, reportedErr := os.Stat(reportedDirectory)
	if wantedErr != nil || reportedErr != nil || !os.SameFile(wantedInfo, reportedInfo) {
		t.Fatalf("ConPTY cwd %q is not %q: wanted_err=%v reported_err=%v",
			reportedDirectory, workingDirectory, wantedErr, reportedErr)
	}

	sentinelExecutable, err := winprocess.PowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := exec.Command(sentinelExecutable, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 120")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Process.Kill()
		_ = sentinel.Wait()
	})

	t.Run("normal_root_exit_joins_descendant", func(t *testing.T) {
		pty, output, outputDone := openWindowsTestPTY(t, fixture, context.Background(), t.TempDir())
		waitForWindowsPTYBytes(t, context.Background(), output, []byte("PS "))
		if _, err := io.WriteString(pty.Input(), windowsPTYChildCommand("AIS_NORMAL_", "CHILD=", false)); err != nil {
			pty.Close()
			t.Fatal(err)
		}
		pid := waitForWindowsPTYMarkerPID(t, output, "AIS_NORMAL_CHILD=")
		if err := waitForWindowsPTYResult(t, pty, 10*time.Second); err != nil {
			pty.Close()
			t.Fatal(err)
		}
		_ = pty.Close()
		_ = waitForWindowsPTYDrain(t, outputDone, 5*time.Second)
		waitForWindowsProcessGone(t, pid)
	})

	t.Run("close_joins_descendant", func(t *testing.T) {
		pty, output, outputDone := openWindowsTestPTY(t, fixture, context.Background(), t.TempDir())
		waitForWindowsPTYBytes(t, context.Background(), output, []byte("PS "))
		if _, err := io.WriteString(pty.Input(), windowsPTYChildCommand("AIS_CLOSE_", "CHILD=", true)); err != nil {
			pty.Close()
			t.Fatal(err)
		}
		pid := waitForWindowsPTYMarkerPID(t, output, "AIS_CLOSE_CHILD=")
		_ = pty.Close()
		_ = waitForWindowsPTYResult(t, pty, 10*time.Second)
		_ = waitForWindowsPTYDrain(t, outputDone, 5*time.Second)
		waitForWindowsProcessGone(t, pid)
		if !fixture.manager.IsOnline(fixture.deviceID) {
			t.Fatal("closing one Windows PTY closed the Device Tunnel")
		}
	})

	t.Run("context_cancel_joins_descendant", func(t *testing.T) {
		ptyContext, cancelPTY := context.WithCancel(context.Background())
		pty, output, outputDone := openWindowsTestPTY(t, fixture, ptyContext, t.TempDir())
		waitForWindowsPTYBytes(t, ptyContext, output, []byte("PS "))
		if _, err := io.WriteString(pty.Input(), windowsPTYChildCommand("AIS_CANCEL_", "CHILD=", true)); err != nil {
			cancelPTY()
			pty.Close()
			t.Fatal(err)
		}
		pid := waitForWindowsPTYMarkerPID(t, output, "AIS_CANCEL_CHILD=")
		cancelPTY()
		if err := waitForWindowsPTYResult(t, pty, 10*time.Second); !errors.Is(err, context.Canceled) {
			pty.Close()
			t.Fatalf("ConPTY cancellation error = %v", err)
		}
		_ = pty.Close()
		_ = waitForWindowsPTYDrain(t, outputDone, 5*time.Second)
		waitForWindowsProcessGone(t, pid)
		if !fixture.manager.IsOnline(fixture.deviceID) {
			t.Fatal("canceling one Windows PTY closed the Device Tunnel")
		}
	})

	if !windowsProcessStillRunning(uint32(sentinel.Process.Pid)) {
		t.Fatal("ConPTY cleanup terminated an unrelated process")
	}

	t.Run("tunnel_shutdown_joins_descendant", func(t *testing.T) {
		pty, output, outputDone := openWindowsTestPTY(t, fixture, context.Background(), t.TempDir())
		waitForWindowsPTYBytes(t, context.Background(), output, []byte("PS "))
		if _, err := io.WriteString(pty.Input(), windowsPTYChildCommand("AIS_SHUTDOWN_", "CHILD=", true)); err != nil {
			pty.Close()
			t.Fatal(err)
		}
		pid := waitForWindowsPTYMarkerPID(t, output, "AIS_SHUTDOWN_CHILD=")
		if err := fixture.stopClient(); err != nil {
			pty.Close()
			t.Fatal(err)
		}
		_ = waitForWindowsPTYResult(t, pty, 10*time.Second)
		_ = pty.Close()
		_ = waitForWindowsPTYDrain(t, outputDone, 5*time.Second)
		waitForWindowsProcessGone(t, pid)
	})

	if !windowsProcessStillRunning(uint32(sentinel.Process.Pid)) {
		t.Fatal("Windows Tunnel shutdown terminated an unrelated process")
	}
}

type windowsPTYCapture struct {
	mu       sync.Mutex
	contents bytes.Buffer
}

func (capture *windowsPTYCapture) Write(contents []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.contents.Write(contents)
}

func (capture *windowsPTYCapture) Bytes() []byte {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return bytes.Clone(capture.contents.Bytes())
}

func (capture *windowsPTYCapture) Contains(marker []byte) bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return bytes.Contains(capture.contents.Bytes(), marker)
}

func (capture *windowsPTYCapture) Count(marker []byte) int {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return bytes.Count(capture.contents.Bytes(), marker)
}

func openWindowsTestPTY(t *testing.T, fixture *windowsTunnelSSHFixture, ctx context.Context, cwd string) (*PTY, *windowsPTYCapture, <-chan error) {
	t.Helper()
	handle, err := fixture.dialer.OpenPTY(
		ctx, fixture.deviceID, PTYOptions{Cols: 80, Rows: 24, CWD: cwd},
	)
	if err != nil {
		t.Fatal(err)
	}
	capture := &windowsPTYCapture{}
	drained := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(capture, handle.Output())
		drained <- copyErr
	}()
	return handle, capture, drained
}

func waitForWindowsPTYBytes(t *testing.T, ctx context.Context, capture *windowsPTYCapture, marker []byte) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if capture.Contains(marker) {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("wait for ConPTY marker %q: %v; output=%q", marker, err, capture.Bytes())
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("ConPTY marker %q did not arrive; output=%q", marker, capture.Bytes())
}

func waitForWindowsPTYCount(t *testing.T, ctx context.Context, capture *windowsPTYCapture, marker []byte, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if capture.Count(marker) >= count {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("wait for ConPTY marker %q count %d: %v; output=%q", marker, count, err, capture.Bytes())
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("ConPTY marker %q count %d did not arrive; output=%q", marker, count, capture.Bytes())
}

func waitForWindowsPTYResult(t *testing.T, handle *PTY, timeout time.Duration) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- handle.Wait() }()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return errors.New("Windows PTY did not join before timeout")
	}
}

func waitForWindowsPTYDrain(t *testing.T, drained <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-drained:
		return err
	case <-time.After(timeout):
		return errors.New("Windows PTY output drain did not join before timeout")
	}
}

func waitForWindowsPTYMarkerPID(t *testing.T, capture *windowsPTYCapture, marker string) uint32 {
	t.Helper()
	waitForWindowsPTYBytes(t, context.Background(), capture, []byte(marker))
	value, err := windowsMarkerText(capture.Bytes(), marker)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(value, 10, 32)
	if err != nil || pid == 0 {
		t.Fatalf("invalid ConPTY PID marker %q: value=%q err=%v output=%q", marker, value, err, capture.Bytes())
	}
	return uint32(pid)
}

func windowsPTYChildCommand(markerLeft, markerRight string, wait bool) string {
	command := fmt.Sprintf(
		`$exe=Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"; `+
			`$child=Start-Process -FilePath $exe -ArgumentList @("-NoLogo","-NoProfile","-NonInteractive","-Command","Start-Sleep -Seconds 120") -PassThru; `+
			`Write-Output (%q+%q+$child.Id)`, markerLeft, markerRight,
	)
	if wait {
		command += `; Wait-Process -Id $child.Id`
	} else {
		command += `; exit 0`
	}
	return command + "\r"
}

type windowsTunnelSSHFixture struct {
	deviceID   string
	dialer     *Dialer
	manager    *tunnel.Manager
	cancel     context.CancelFunc
	clientDone chan error
	stopOnce   sync.Once
	stopErr    error
	server     *httptest.Server
	gateway    *tunnel.Gateway
}

func newWindowsTunnelSSHFixture(t *testing.T) *windowsTunnelSSHFixture {
	t.Helper()
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
	deviceStore := &windowsTunnelStore{}
	manager := tunnel.NewManager()
	gateway, err := tunnel.NewGateway(tunnel.GatewayOptions{
		Store: deviceStore, Pairing: windowsPairingOfferer{}, Manager: manager,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(gateway)
	clientContext, cancelClient := context.WithCancel(context.Background())
	client, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: server.URL, HTTPClient: server.Client(), Identity: deviceIdentity,
		DeviceName: "windows-ssh-integration", Platform: protocol.PlatformWindows,
		Arch: "amd64", ClientVersion: "test",
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
		server.Close()
		gateway.Close()
		t.Fatal(err)
	}
	fixture := &windowsTunnelSSHFixture{
		deviceID: deviceIdentity.DeviceID, manager: manager,
		cancel: cancelClient, clientDone: make(chan error, 1), server: server, gateway: gateway,
	}
	go func() { fixture.clientDone <- client.Run(clientContext) }()
	waitForWindowsTunnelOnline(t, manager, deviceIdentity.DeviceID)
	dialer, err := NewDialer(manager, DeviceKeyLookupFunc(func(context.Context, string) (ed25519.PublicKey, error) {
		return append(ed25519.PublicKey(nil), deviceIdentity.PublicKey...), nil
	}))
	if err != nil {
		fixture.stopClient()
		server.Close()
		gateway.Close()
		t.Fatal(err)
	}
	fixture.dialer = dialer
	t.Cleanup(func() {
		if err := fixture.stopClient(); err != nil {
			t.Errorf("stop Windows Tunnel fixture: %v", err)
		}
		server.Close()
		gateway.Close()
	})
	return fixture
}

func (fixture *windowsTunnelSSHFixture) stopClient() error {
	fixture.stopOnce.Do(func() {
		fixture.cancel()
		select {
		case fixture.stopErr = <-fixture.clientDone:
		case <-time.After(6 * time.Second):
			fixture.stopErr = errors.New("Windows Tunnel client did not join after cancellation")
		}
	})
	return fixture.stopErr
}

type windowsTunnelStore struct {
	mu     sync.Mutex
	device store.Device
}

func (value *windowsTunnelStore) RegisterDevice(_ context.Context, device store.Device) (store.Device, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.device = device
	return device, nil
}

func (*windowsTunnelStore) UpdateDeviceLastSeen(context.Context, string, time.Time) error { return nil }

type windowsPairingOfferer struct{}

func (windowsPairingOfferer) Offer(_ context.Context, _ string, now time.Time) (pairing.Offer, error) {
	return pairing.Offer{Code: "K7HF-92PQ", ExpiresAt: now.Add(10 * time.Minute)}, nil
}

func waitForWindowsTunnelOnline(t *testing.T, manager *tunnel.Manager, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if manager.IsOnline(deviceID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Windows Device did not become online over TLS/WSS")
}

func windowsChildScript(filename string, wait bool) string {
	script := fmt.Sprintf(`
$exe = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$child = Start-Process -FilePath $exe -ArgumentList @("-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 120") -PassThru
[System.IO.File]::WriteAllText((Join-Path (Get-Location).Path %q), [string]$child.Id)
`, filename)
	if wait {
		script += "Wait-Process -Id $child.Id\n"
	}
	return script
}

func waitForWindowsPIDFile(t *testing.T, path string) uint32 {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.ParseUint(strings.TrimSpace(string(contents)), 10, 32)
			if parseErr == nil && pid > 0 {
				return uint32(pid)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Windows child PID file %q was not written", path)
	return 0
}

func waitForWindowsProcessGone(t *testing.T, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for windowsProcessStillRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if windowsProcessStillRunning(pid) {
		t.Fatalf("Windows Job descendant %d survived cleanup", pid)
	}
}

func windowsProcessStillRunning(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && result == uint32(windows.WAIT_TIMEOUT)
}

func windowsMarkerText(output []byte, prefix string) (string, error) {
	text := string(output)
	index := strings.Index(text, prefix)
	if index < 0 {
		return "", fmt.Errorf("marker %q not found in %q", prefix, output)
	}
	text = text[index+len(prefix):]
	if end := strings.IndexAny(text, "\r\n"); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text), nil
}

func windowsMarshalSSHString(value string) []byte {
	payload := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(payload, uint32(len(value)))
	copy(payload[4:], value)
	return payload
}

func waitForWindowsExitStatus(t *testing.T, requests <-chan *ssh.Request, want uint32) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case request, open := <-requests:
			if !open {
				t.Fatal("Windows SSH request stream closed before exit status")
			}
			if request.Type != "exit-status" {
				continue
			}
			if len(request.Payload) != 4 || binary.BigEndian.Uint32(request.Payload) != want {
				t.Fatalf("Windows SSH exit status payload = %v, want %d", request.Payload, want)
			}
			return
		case <-timer.C:
			t.Fatal("Windows SSH signal did not finish the Job")
		}
	}
}
