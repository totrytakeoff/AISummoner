//go:build windows

package sshserver

import (
	"context"
	"testing"
)

func TestWindowsExecutionBackendFailsClosedUntilPowerShellTask(t *testing.T) {
	backend := windowsUnsupportedExecutionBackend{}
	if !backend.isAbsolutePath(`C:\work`) || backend.isAbsolutePath(`/tmp`) || backend.isAbsolutePath(`C:relative`) {
		t.Fatal("Windows path syntax contract is incorrect")
	}
	workingDirectory := t.TempDir()
	validated, err := backend.validateWorkingDirectory(workingDirectory)
	if err != nil || validated == "" {
		t.Fatalf("validate cwd = %q, err=%v", validated, err)
	}
	if process, err := backend.startExec(context.Background(), nil, "Get-Location", validated); err == nil || process != nil {
		t.Fatal("unsupported Windows exec reported success")
	}
	if process, err := backend.startShell(context.Background(), nil, &ptyState{term: "xterm", cols: 80, rows: 24}, validated); err == nil || process != nil {
		t.Fatal("unsupported Windows shell reported success")
	}
}
