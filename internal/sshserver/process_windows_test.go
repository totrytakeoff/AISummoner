//go:build windows

package sshserver

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsExecutionBackendPathAndFailClosedShellContracts(t *testing.T) {
	backend := windowsExecutionBackend{}
	if !backend.isAbsolutePath(`C:\work`) || backend.isAbsolutePath(`/tmp`) ||
		backend.isAbsolutePath(`C:relative`) || backend.isAbsolutePath("C:\\bad\x00path") {
		t.Fatal("Windows path syntax contract is incorrect")
	}
	workingDirectory := t.TempDir()
	validated, err := backend.validateWorkingDirectory(workingDirectory)
	if err != nil || validated == "" {
		t.Fatalf("validate cwd = %q, err=%v", validated, err)
	}
	defaultDirectory, err := backend.validateWorkingDirectory("")
	if err != nil || !filepath.IsAbs(defaultDirectory) {
		t.Fatalf("default cwd = %q, err=%v", defaultDirectory, err)
	}
	for _, value := range []string{"relative", filepath.Join(workingDirectory, "missing"), workingDirectory + `\.`} {
		if _, err := backend.validateWorkingDirectory(value); err == nil {
			t.Fatalf("invalid cwd %q was accepted", value)
		}
	}
	if process, err := backend.startExec(context.Background(), nil, "Get-Location", validated); err == nil || process != nil {
		t.Fatal("Windows exec without an SSH channel reported success")
	}
	if process, err := backend.startExec(context.Background(), windowsTestChannel{}, string([]byte{0xff}), validated); err == nil || process != nil {
		t.Fatal("invalid UTF-8 Windows exec reported success")
	}
	if process, err := backend.startShell(context.Background(), nil, &ptyState{term: "xterm", cols: 80, rows: 24}, validated); err == nil || process != nil {
		t.Fatal("Windows ConPTY shell without an SSH channel reported success")
	}
}

func TestWindowsPowerShellExecRepeatedlyClosesNativeHandles(t *testing.T) {
	// Resolve the lazy DLL/procedure and let the Go Windows syscall path finish
	// its own bounded initialization before any process-lifecycle baseline is
	// captured. Otherwise the probe itself adds handles after its first result
	// and looks like a PowerShell leak.
	if _, err := windowsCurrentProcessHandleCount(); err != nil {
		t.Fatal(err)
	}
	backend := windowsExecutionBackend{}
	directory, err := backend.validateWorkingDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string) {
		t.Helper()
		process, err := backend.startExec(context.Background(), windowsTestChannel{}, command, directory)
		if err != nil {
			t.Fatal(err)
		}
		if status := process.wait(); status != 0 {
			t.Fatalf("PowerShell status = %d", status)
		}
		process.finish()
		select {
		case <-process.doneChannel():
		default:
			t.Fatal("PowerShell process did not publish joined completion")
		}
	}
	command := `[Console]::Out.Write("stdout"); [Console]::Error.Write("stderr")`
	// Exclude bounded process-wide initialization by Go and the complete
	// stdout/stderr pump path from the leak slope. The Windows runner has shown
	// that its pipe runtime may grow a small handle cache during the second
	// batch, so calibrate through two identical batches before measuring twice
	// that sample. A real per-exec leak continues growing across the observation
	// window instead of converging after calibration.
	for attempt := 0; attempt < 8; attempt++ {
		run(command)
	}
	for attempt := 0; attempt < 8; attempt++ {
		run(command)
	}
	calibrated, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 16; attempt++ {
		run(command)
	}
	after, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > calibrated+3 {
		t.Fatalf("PowerShell handle count kept growing after calibration: %d to %d", calibrated, after)
	}
	t.Logf("PowerShell handle count converged after calibration: %d to %d", calibrated, after)
}

func TestWindowsConPTYShellRepeatedlyClosesNativeHandles(t *testing.T) {
	backend := windowsExecutionBackend{}
	directory, err := backend.validateWorkingDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		channel := newWindowsPTYTestChannel()
		process, err := backend.startShell(
			context.Background(), channel,
			&ptyState{term: "xterm-256color", cols: 80, rows: 24}, directory,
		)
		if err != nil {
			channel.Close()
			t.Fatal(err)
		}
		if !process.resizeTerminal(101, 37) {
			process.terminate()
			channel.Close()
			process.finish()
			t.Fatal("live Windows ConPTY rejected resize")
		}
		if _, err := channel.inputWriter.Write([]byte("exit 0\r")); err != nil {
			process.terminate()
			channel.Close()
			process.finish()
			t.Fatal(err)
		}
		waited := make(chan int, 1)
		go func() { waited <- process.wait() }()
		var status int
		select {
		case status = <-waited:
		case <-time.After(15 * time.Second):
			process.terminate()
			channel.Close()
			status = <-waited
			process.finish()
			t.Fatalf("Windows ConPTY shell did not exit; final status=%d", status)
		}
		channel.Close()
		process.finish()
		if status != 0 {
			t.Fatalf("Windows ConPTY status = %d", status)
		}
		if process.resizeTerminal(120, 40) {
			t.Fatal("finished Windows ConPTY accepted resize")
		}
		select {
		case <-process.doneChannel():
		default:
			t.Fatal("Windows ConPTY did not publish joined completion")
		}
	}
	run()
	before, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		run()
	}
	after, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > before+8 {
		t.Fatalf("ConPTY handle count grew from %d to %d", before, after)
	}
}

type windowsTestChannel struct{}

func (windowsTestChannel) Read([]byte) (int, error)                       { return 0, io.EOF }
func (windowsTestChannel) Write(contents []byte) (int, error)             { return len(contents), nil }
func (windowsTestChannel) Close() error                                   { return nil }
func (windowsTestChannel) CloseWrite() error                              { return nil }
func (windowsTestChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (channel windowsTestChannel) Stderr() io.ReadWriter                  { return channel }

type windowsPTYTestChannel struct {
	inputReader *io.PipeReader
	inputWriter *io.PipeWriter
	closeOnce   sync.Once
}

func newWindowsPTYTestChannel() *windowsPTYTestChannel {
	reader, writer := io.Pipe()
	return &windowsPTYTestChannel{inputReader: reader, inputWriter: writer}
}

func (channel *windowsPTYTestChannel) Read(contents []byte) (int, error) {
	return channel.inputReader.Read(contents)
}

func (*windowsPTYTestChannel) Write(contents []byte) (int, error) { return len(contents), nil }

func (channel *windowsPTYTestChannel) Close() error {
	channel.closeOnce.Do(func() {
		_ = channel.inputWriter.Close()
		_ = channel.inputReader.Close()
	})
	return nil
}

func (channel *windowsPTYTestChannel) CloseWrite() error { return channel.inputWriter.Close() }

func (*windowsPTYTestChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }

func (channel *windowsPTYTestChannel) Stderr() io.ReadWriter { return channel }

var windowsGetProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func windowsCurrentProcessHandleCount() (uint32, error) {
	var count uint32
	result, _, callErr := windowsGetProcessHandleCount.Call(
		uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)),
	)
	if result == 0 {
		return 0, fmt.Errorf("read process handle count: %w", callErr)
	}
	return count, nil
}
