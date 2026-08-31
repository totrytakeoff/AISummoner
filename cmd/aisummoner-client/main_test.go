//go:build linux

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/clientipc"
	"github.com/aisummoner/aisummoner/internal/remoteclient"
)

func TestParseLaunchOptionsKeepsLegacyStartAndPrivateDaemonSocket(t *testing.T) {
	dataDirectory := t.TempDir()
	common := []string{"--server", "http://127.0.0.1:8080", "--data-dir", dataDirectory, "--dev"}
	if os.Geteuid() == 0 {
		common = append(common, "--allow-root-dev")
	}
	start, err := parseLaunchOptions("start", common, false)
	if err != nil {
		t.Fatal(err)
	}
	if start.serverURL != "http://127.0.0.1:8080" || start.dataDirectory != dataDirectory || start.socketPath != "" {
		t.Fatalf("start options = %+v", start)
	}
	daemon, err := parseLaunchOptions("daemon", common, true)
	if err != nil {
		t.Fatal(err)
	}
	if daemon.socketPath != filepath.Join(dataDirectory, "client.sock") {
		t.Fatalf("daemon socket = %q", daemon.socketPath)
	}
	if _, err := parseLaunchOptions("start", append(common, "--socket", filepath.Join(dataDirectory, "wrong.sock")), false); err == nil {
		t.Fatal("legacy start accepted a daemon socket")
	}
}

func TestParseControlOptionsRejectsPositionals(t *testing.T) {
	dataDirectory := t.TempDir()
	gotDirectory, socketPath, err := parseControlOptions("status", []string{"--data-dir", dataDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if gotDirectory != dataDirectory || socketPath != "" {
		t.Fatalf("control options = %q %q", gotDirectory, socketPath)
	}
	if _, _, err := parseControlOptions("status", []string{"extra"}); err == nil {
		t.Fatal("control command accepted positional argument")
	}
}

func TestDaemonAndControlSocketStayInsideAbsoluteDataDirectory(t *testing.T) {
	dataDirectory := t.TempDir()
	outsideSocket := filepath.Join(filepath.Dir(dataDirectory), "outside.sock")
	launchArguments := []string{
		"--server", "http://127.0.0.1:8080",
		"--data-dir", dataDirectory,
		"--socket", outsideSocket,
		"--dev",
	}
	if os.Geteuid() == 0 {
		launchArguments = append(launchArguments, "--allow-root-dev")
	}
	if _, err := parseLaunchOptions("daemon", launchArguments, true); err == nil {
		t.Fatal("daemon accepted a socket outside its data directory")
	}
	if _, _, err := parseControlOptions("status", []string{
		"--data-dir", dataDirectory,
		"--socket", outsideSocket,
	}); err == nil {
		t.Fatal("control command accepted a socket outside its data directory")
	}
	if _, err := parseLaunchOptions("daemon", []string{
		"--server", "http://127.0.0.1:8080",
		"--data-dir", "relative-data",
		"--dev",
		"--allow-root-dev",
	}, true); err == nil {
		t.Fatal("daemon accepted a relative data directory")
	}
	if _, _, err := parseControlOptions("status", []string{"--data-dir", "relative-data"}); err == nil {
		t.Fatal("control command accepted a relative data directory")
	}
}

func TestPrivateIPCObservesPausesAndResumesRealRemoteCore(t *testing.T) {
	requests := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dataDirectory := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller, err := newController(launchOptions{
		serverURL: server.URL, dataDirectory: dataDirectory,
		deviceName: "ipc-core", development: true,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(dataDirectory, "client.sock")
	ipcServer, err := clientipc.NewServer(clientipc.ServerOptions{
		Endpoint: socketPath, Controller: controller, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	coreDone := make(chan error, 1)
	ipcDone := make(chan error, 1)
	go func() { coreDone <- controller.Run(ctx) }()
	go func() { ipcDone <- ipcServer.Serve(ctx) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			for name, done := range map[string]<-chan error{"core": coreDone, "ipc": ipcDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("%s shutdown: %v", name, err)
					}
				case <-time.After(time.Second):
					t.Errorf("%s did not join", name)
				}
			}
		})
	}
	t.Cleanup(stop)

	waitSignal(t, requests, "initial Tunnel request")
	waitForIPCPhase(t, socketPath, remoteclient.PhaseRetrying)
	if err := clientipc.Call(context.Background(), socketPath, clientipc.MethodDaemonPause, clientipc.EmptyParams{}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	waitForIPCPhase(t, socketPath, remoteclient.PhasePaused)
	if err := clientipc.Call(context.Background(), socketPath, clientipc.MethodDaemonResume, clientipc.EmptyParams{}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, requests, "resumed Tunnel request")
	stop()
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("daemon socket remains after joined stop: %v", err)
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("%s timed out", name)
	}
}

func waitForIPCPhase(t *testing.T, socketPath string, want remoteclient.Phase) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		var snapshot remoteclient.Snapshot
		if err := clientipc.Call(context.Background(), socketPath, clientipc.MethodStatusGet, clientipc.EmptyParams{}, &snapshot); err == nil && snapshot.Phase == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("IPC phase did not become %s", want)
		}
		time.Sleep(time.Millisecond)
	}
}
