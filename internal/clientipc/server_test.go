//go:build linux

package clientipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/remoteclient"
)

type controllerStub struct {
	snapshot     remoteclient.Snapshot
	events       []remoteclient.Event
	pauseCalls   atomic.Int32
	resumeCalls  atomic.Int32
	refreshCalls atomic.Int32
	pauseEntered chan struct{}
	pauseRelease <-chan struct{}
}

func (stub *controllerStub) Snapshot() remoteclient.Snapshot { return stub.snapshot }
func (stub *controllerStub) Events(after uint64, limit int) []remoteclient.Event {
	result := make([]remoteclient.Event, 0, limit)
	for _, event := range stub.events {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result
}
func (stub *controllerStub) Pause(ctx context.Context) error {
	stub.pauseCalls.Add(1)
	if stub.pauseEntered != nil {
		stub.pauseEntered <- struct{}{}
	}
	if stub.pauseRelease != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stub.pauseRelease:
		}
	}
	return nil
}
func (stub *controllerStub) Resume() error {
	stub.resumeCalls.Add(1)
	return nil
}
func (stub *controllerStub) RefreshPairing(context.Context) error {
	stub.refreshCalls.Add(1)
	return nil
}

func TestPrivateSocketRoundTripPermissionsAndCleanup(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "client.sock")
	stub := &controllerStub{
		snapshot: remoteclient.Snapshot{DeviceID: "dev_test", DeviceName: "remote", ClientVersion: "test", Phase: remoteclient.PhaseOnline},
		events:   []remoteclient.Event{{Sequence: 2, Kind: "tunnel.online", Level: "info", Summary: "Connected to control service"}},
	}
	server, err := NewServer(ServerOptions{Endpoint: socketPath, Controller: stub, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	cancel, stopped := runTestServer(t, server, socketPath)

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode = %v", info.Mode())
	}
	var snapshot remoteclient.Snapshot
	if err := Call(context.Background(), socketPath, MethodStatusGet, EmptyParams{}, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.DeviceID != "dev_test" || snapshot.Phase != remoteclient.PhaseOnline {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	var events EventsListResult
	if err := Call(context.Background(), socketPath, MethodEventsList, EventsListParams{AfterSequence: 1, Limit: 20}, &events); err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 1 || events.NextSequence != 2 {
		t.Fatalf("events = %+v", events)
	}
	for method, counter := range map[string]*atomic.Int32{
		MethodDaemonPause: &stub.pauseCalls, MethodDaemonResume: &stub.resumeCalls,
	} {
		if err := Call(context.Background(), socketPath, method, EmptyParams{}, &struct{}{}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if counter.Load() != 1 {
			t.Fatalf("%s calls = %d", method, counter.Load())
		}
	}
	var refresh PairingRefreshResult
	if err := Call(context.Background(), socketPath, MethodPairingRefresh, EmptyParams{}, &refresh); err != nil {
		t.Fatal(err)
	}
	if !refresh.ClosesActiveSessions || stub.refreshCalls.Load() != 1 {
		t.Fatalf("refresh result = %+v, calls = %d", refresh, stub.refreshCalls.Load())
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
}

func TestServerRejectsWrongPeerBeforeReadingRequest(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "client.sock")
	server, err := NewServer(ServerOptions{Endpoint: socketPath, Controller: &controllerStub{}, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	server.authenticatePeer = func(net.Conn) error { return errors.New("wrong local peer") }
	cancel, stopped := runTestServer(t, server, socketPath)
	defer func() { cancel(); <-stopped }()
	var snapshot remoteclient.Snapshot
	if err := Call(context.Background(), socketPath, MethodStatusGet, EmptyParams{}, &snapshot); err == nil {
		t.Fatal("wrong-UID local peer was accepted")
	}
}

func TestServerRejectsUnsafeSocketPaths(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{name: "regular stale file", setup: func(t *testing.T, _ string, path string) {
			if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T, directory, path string) {
			target := filepath.Join(directory, "target")
			if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "client.sock")
			test.setup(t, directory, path)
			server, err := NewServer(ServerOptions{Endpoint: path, Controller: &controllerStub{}, Logger: discardLogger()})
			if err != nil {
				t.Fatal(err)
			}
			if err := server.Serve(context.Background()); err == nil {
				t.Fatal("unsafe socket path was accepted")
			}
		})
	}
}

func TestMalformedAndUnknownRequestsReturnFixedErrors(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "client.sock")
	server, err := NewServer(ServerOptions{Endpoint: socketPath, Controller: &controllerStub{}, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	cancel, stopped := runTestServer(t, server, socketPath)
	defer func() { cancel(); <-stopped }()

	tests := []struct {
		frame string
		code  string
	}{
		{frame: `{`, code: "INVALID_REQUEST"},
		{frame: `{"version":1,"id":"req_test","method":"status.get","params":{},"extra":true}`, code: "INVALID_REQUEST"},
		{frame: `{"version":1,"id":"req_test","method":"status.get","method":"events.list","params":{}}`, code: "INVALID_REQUEST"},
		{frame: `{"version":1,"id":"req_test","method":"events.list","params":{"after_sequence":0,"limit":1,"limit":2}}`, code: "INVALID_REQUEST"},
		{frame: `{"version":1,"id":"req_test","method":"unknown","params":{}}`, code: "METHOD_NOT_FOUND"},
		{frame: `{"version":1,"id":"req_test","method":"events.list","params":{"after_sequence":0,"limit":201}}`, code: "INVALID_REQUEST"},
	}
	for _, test := range tests {
		response := rawRequest(t, socketPath, test.frame+"\n")
		if response.OK || response.Error == nil || response.Error.Code != test.code {
			t.Fatalf("response to %q = %+v", test.frame, response)
		}
	}
}

func TestOversizedRequestAndControllerDeadlineStayBounded(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "client.sock")
	release := make(chan struct{})
	stub := &controllerStub{pauseEntered: make(chan struct{}, 1), pauseRelease: release}
	server, err := NewServer(ServerOptions{Endpoint: socketPath, Controller: stub, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	server.timeout = 30 * time.Millisecond
	cancel, stopped := runTestServer(t, server, socketPath)
	defer func() {
		close(release)
		cancel()
		<-stopped
	}()

	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(append(make([]byte, MaxFrameBytes), '\n')); err != nil {
		t.Fatal(err)
	}
	contents, err := readClientFrame(connection)
	_ = connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	var oversized Response
	if err := json.Unmarshal(contents, &oversized); err != nil {
		t.Fatal(err)
	}
	if oversized.OK || oversized.Error == nil || oversized.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("oversized response = %+v", oversized)
	}

	var ignored struct{}
	err = Call(context.Background(), socketPath, MethodDaemonPause, EmptyParams{}, &ignored)
	var remoteError *RemoteError
	if !errors.As(err, &remoteError) || remoteError.Code != "TIMEOUT" {
		t.Fatalf("pause deadline error = %v", err)
	}
}

func TestServerBoundsConcurrentHandlers(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "client.sock")
	release := make(chan struct{})
	stub := &controllerStub{pauseEntered: make(chan struct{}, MaxHandlers), pauseRelease: release}
	server, err := NewServer(ServerOptions{Endpoint: socketPath, Controller: stub, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	cancel, stopped := runTestServer(t, server, socketPath)
	defer func() { cancel(); <-stopped }()

	errorsOut := make(chan error, MaxHandlers)
	var calls sync.WaitGroup
	for index := 0; index < MaxHandlers; index++ {
		calls.Add(1)
		go func() {
			defer calls.Done()
			errorsOut <- Call(context.Background(), socketPath, MethodDaemonPause, EmptyParams{}, &struct{}{})
		}()
	}
	for index := 0; index < MaxHandlers; index++ {
		select {
		case <-stub.pauseEntered:
		case <-time.After(time.Second):
			t.Fatal("handler admission did not fill")
		}
	}
	ninthContext, stopNinth := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stopNinth()
	if err := Call(ninthContext, socketPath, MethodDaemonPause, EmptyParams{}, &struct{}{}); err == nil {
		t.Fatal("ninth concurrent handler was admitted")
	}
	close(release)
	calls.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("admitted handler failed: %v", err)
		}
	}
}

func runTestServer(t *testing.T, server *Server, path string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return cancel, stopped
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server socket was not created")
		}
		time.Sleep(time.Millisecond)
	}
}

func rawRequest(t *testing.T, path, frame string) Response {
	t.Helper()
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, frame); err != nil {
		t.Fatal(err)
	}
	contents, err := readClientFrame(connection)
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(contents, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
