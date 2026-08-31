//go:build windows

package clientipc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/remoteclient"
)

type windowsControllerStub struct{}

func (*windowsControllerStub) Snapshot() remoteclient.Snapshot {
	return remoteclient.Snapshot{DeviceID: "dev_windows", DeviceName: "Windows", ClientVersion: "test", Phase: remoteclient.PhaseOnline}
}
func (*windowsControllerStub) Events(uint64, int) []remoteclient.Event { return nil }
func (*windowsControllerStub) Pause(context.Context) error             { return nil }
func (*windowsControllerStub) Resume() error                           { return nil }
func (*windowsControllerStub) RefreshPairing(context.Context) error    { return nil }

func TestWindowsPrivateIPCRoundTripSecondInstanceAndRestart(t *testing.T) {
	endpoint := fmt.Sprintf(`LOCAL\AISummoner.Remote.ipc.%d.%d`, os.Getpid(), time.Now().UnixNano())
	if DefaultEndpoint(`C:\ignored`) != `LOCAL\AISummoner.Remote.v1` {
		t.Fatal("Windows default endpoint changed")
	}
	server, err := NewServer(ServerOptions{
		Endpoint: endpoint, Controller: &windowsControllerStub{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(ctx) }()
	waitForWindowsIPC(t, endpoint)

	second, err := NewServer(ServerOptions{Endpoint: endpoint, Controller: &windowsControllerStub{}})
	if err != nil {
		cancel()
		<-stopped
		t.Fatal(err)
	}
	secondContext, secondCancel := context.WithCancel(context.Background())
	secondStopped := make(chan error, 1)
	go func() { secondStopped <- second.Serve(secondContext) }()
	select {
	case err := <-secondStopped:
		secondCancel()
		if err == nil {
			cancel()
			<-stopped
			t.Fatal("second daemon acquired a live named-pipe endpoint")
		}
	case <-time.After(2 * time.Second):
		secondCancel()
		<-secondStopped
		cancel()
		<-stopped
		t.Fatal("second daemon did not reject the live named-pipe endpoint")
	}
	cancel()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}

	restarted, err := NewServer(ServerOptions{Endpoint: endpoint, Controller: &windowsControllerStub{}})
	if err != nil {
		t.Fatal(err)
	}
	restartContext, restartCancel := context.WithCancel(context.Background())
	restartStopped := make(chan error, 1)
	go func() { restartStopped <- restarted.Serve(restartContext) }()
	waitForWindowsIPC(t, endpoint)
	restartCancel()
	if err := <-restartStopped; err != nil {
		t.Fatal(err)
	}
}

func waitForWindowsIPC(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		var snapshot remoteclient.Snapshot
		err := Call(ctx, endpoint, MethodStatusGet, EmptyParams{}, &snapshot)
		cancel()
		if err == nil {
			if snapshot.DeviceID != "dev_windows" {
				t.Fatalf("snapshot = %+v", snapshot)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows IPC did not become ready: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
