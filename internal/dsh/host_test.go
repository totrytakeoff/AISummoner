package dsh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type markerHealthProbe struct{ marker string }

func (probe markerHealthProbe) Health(context.Context) HealthResult {
	if _, err := os.Stat(probe.marker); err == nil {
		return HealthResult{Status: HealthAvailable, Version: "test"}
	}
	return HealthResult{Status: HealthUnavailable}
}

type unavailableHealthProbe struct{}

func (unavailableHealthProbe) Health(context.Context) HealthResult {
	return HealthResult{Status: HealthUnavailable}
}

func TestStartHostUsesAllowlistedEnvironmentAndCloseEscalatesAndJoins(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	nodePath := filepath.Join(root, "node")
	cliPath := filepath.Join(root, "cli.js")
	marker := filepath.Join(home, "started")
	secret := strings.Repeat("h", 32)
	bridgeURL := "http://127.0.0.1:14097" + CallbackPath
	script := fmt.Sprintf(`#!/bin/sh
set -eu
test "$1" = %s
test "$2" = "--profile"
test "$3" = "web"
test "$6" = "--host"
test "$7" = "127.0.0.1"
test "$8" = "--port"
test "$9" = "14096"
test "$DSH_HOME" = %s
test "$HOME" = %s
test "$AISUMMONER_DSH_BRIDGE_URL" = %s
test "$AISUMMONER_AGENT_BRIDGE_SECRET" = %s
test "$DSH_TELEMETRY_DISABLED" = "1"
test "$PATH" = %s
: > %s
trap '' TERM
exec /usr/bin/tail -f /dev/null
`, shellLiteral(cliPath), shellLiteral(home), shellLiteral(home), shellLiteral(bridgeURL),
		shellLiteral(secret), shellLiteral(filepath.Dir(nodePath)), shellLiteral(marker))
	writeExecutable(t, nodePath, script)
	if err := os.WriteFile(cliPath, []byte("// test entrypoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	host, err := StartHost(context.Background(), HostOptions{
		NodePath: nodePath, CLIPath: cliPath, Home: home,
		BaseURL: "http://127.0.0.1:14096", BridgeURL: bridgeURL,
		BridgeSecret: []byte(secret), Probe: markerHealthProbe{marker: marker},
		StartupPollInterval: 5 * time.Millisecond, HealthAttemptTimeout: 20 * time.Millisecond,
		TerminationGrace: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if host.command.Env != nil {
		t.Fatal("Host retained the bridge secret environment after start")
	}
	for _, argument := range host.command.Args {
		if strings.Contains(argument, secret) {
			t.Fatal("Host command arguments contain the bridge secret")
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Close error=%v", err)
	}
	select {
	case <-host.Done():
	case <-time.After(time.Second):
		t.Fatal("stubborn Host was not killed and reaped")
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatalf("joined Close error=%v", err)
	}
	if host.command.ProcessState == nil {
		t.Fatal("joined Host has no process state")
	}
	if err := host.command.Process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("reaped Host remains signalable: %v", err)
	}
}

func TestStartHostTimeoutCleansExactChild(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	nodePath := filepath.Join(root, "node")
	cliPath := filepath.Join(root, "cli.js")
	pidPath := filepath.Join(root, "child.pid")
	writeExecutable(t, nodePath, fmt.Sprintf(`#!/bin/sh
set -eu
echo "$$" > %s
trap '' TERM
exec /usr/bin/tail -f /dev/null
`, shellLiteral(pidPath)))
	if err := os.WriteFile(cliPath, []byte("// test entrypoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := StartHost(ctx, HostOptions{
		NodePath: nodePath, CLIPath: cliPath, Home: home,
		BaseURL: "http://127.0.0.1:14096", BridgeURL: "http://127.0.0.1:14097" + CallbackPath,
		BridgeSecret: []byte(strings.Repeat("s", 32)), Probe: unavailableHealthProbe{},
		StartupPollInterval: 5 * time.Millisecond, HealthAttemptTimeout: 5 * time.Millisecond,
		TerminationGrace: 30 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("unavailable DSH Host startup succeeded")
	}
	encodedPID, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(encodedPID)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if signalErr := syscall.Kill(pid, 0); signalErr == nil || !errors.Is(signalErr, syscall.ESRCH) {
		t.Fatalf("startup child %d remains reachable: %v", pid, signalErr)
	}
}

func TestStartHostRejectsNonPrivateRuntimeInputsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	nodePath := filepath.Join(root, "node")
	cliPath := filepath.Join(root, "cli.js")
	writeExecutable(t, nodePath, "#!/bin/sh\nexit 99\n")
	if err := os.WriteFile(cliPath, []byte("// test entrypoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := HostOptions{
		NodePath: nodePath, CLIPath: cliPath, Home: filepath.Join(root, "home"),
		BaseURL: "http://127.0.0.1:14096", BridgeURL: "http://127.0.0.1:14097" + CallbackPath,
		BridgeSecret: []byte(strings.Repeat("s", 32)), Probe: unavailableHealthProbe{},
	}
	for _, mutate := range []func(*HostOptions){
		func(value *HostOptions) { value.BaseURL = "http://0.0.0.0:14096" },
		func(value *HostOptions) { value.BridgeURL = "http://127.0.0.1:14097/internal/opencode/remote-exec" },
		func(value *HostOptions) { value.BridgeSecret = []byte("short") },
		func(value *HostOptions) { value.Home = "/" },
	} {
		candidate := options
		mutate(&candidate)
		if _, err := StartHost(context.Background(), candidate); err == nil {
			t.Fatalf("invalid Host options were accepted: %#v", candidate)
		}
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
