//go:build windows

package sshserver

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"
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
		t.Fatal("unsupported Windows ConPTY shell reported success")
	}
}

func TestWindowsPowerShellExecRepeatedlyClosesNativeHandles(t *testing.T) {
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
	run(`[Console]::Out.Write("warmup")`)
	before, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 6; attempt++ {
		run(`[Console]::Out.Write("stdout"); [Console]::Error.Write("stderr")`)
	}
	after, err := windowsCurrentProcessHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > before+3 {
		t.Fatalf("PowerShell handle count grew from %d to %d", before, after)
	}
}

type windowsTestChannel struct{}

func (windowsTestChannel) Read([]byte) (int, error)                       { return 0, io.EOF }
func (windowsTestChannel) Write(contents []byte) (int, error)             { return len(contents), nil }
func (windowsTestChannel) Close() error                                   { return nil }
func (windowsTestChannel) CloseWrite() error                              { return nil }
func (windowsTestChannel) SendRequest(string, bool, []byte) (bool, error) { return false, nil }
func (channel windowsTestChannel) Stderr() io.ReadWriter                  { return channel }

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
