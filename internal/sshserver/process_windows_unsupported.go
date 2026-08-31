//go:build windows

package sshserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

type windowsUnsupportedExecutionBackend struct{}

func currentExecutionBackend() executionBackend { return windowsUnsupportedExecutionBackend{} }

func (windowsUnsupportedExecutionBackend) isAbsolutePath(value string) bool {
	return value != "" && !strings.ContainsRune(value, '\x00') && filepath.IsAbs(value)
}

func (windowsUnsupportedExecutionBackend) validateWorkingDirectory(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > MaxCWDBytes || strings.ContainsRune(value, '\x00') || !filepath.IsAbs(value) {
		return "", errors.New("working directory must be an absolute Windows path")
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", errors.New("working directory is unavailable")
	}
	return filepath.Clean(value), nil
}

func (windowsUnsupportedExecutionBackend) startExec(context.Context, ssh.Channel, string, string) (sessionProcess, error) {
	return nil, errors.New("Windows SSH exec is not available in this client build")
}

func (windowsUnsupportedExecutionBackend) startShell(context.Context, ssh.Channel, *ptyState, string) (sessionProcess, error) {
	return nil, errors.New("Windows SSH shell is not available in this client build")
}
