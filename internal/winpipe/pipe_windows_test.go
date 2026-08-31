//go:build windows

package winpipe

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestPipeNameContract(t *testing.T) {
	path, err := FullPath(DefaultName)
	if err != nil {
		t.Fatal(err)
	}
	if path != `\\.\pipe\LOCAL\AISummoner.Remote.v1` {
		t.Fatalf("native path = %q", path)
	}
	for _, invalid := range []string{
		"", `AISummoner.Remote.v1`, `LOCAL\`, `LOCAL\nested\pipe`,
		`LOCAL\../escape`, "LOCAL\\line\nbreak", "LOCAL\\nul\x00byte",
	} {
		if err := ValidateName(invalid); err == nil {
			t.Errorf("accepted invalid pipe name %q", invalid)
		}
	}
}

func TestAuthenticatedPipeExclusiveAndRestart(t *testing.T) {
	name := fmt.Sprintf(`LOCAL\AISummoner.Remote.production.%d.%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Listen(name); err == nil {
		second.Close()
		listener.Close()
		t.Fatal("second listener acquired the live first-instance endpoint")
	}

	serverResult := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		defer connection.Close()
		if err := listener.Authenticate(connection); err != nil {
			serverResult <- err
			return
		}
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		_, err = connection.Write([]byte("authenticated"))
		serverResult <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := Dial(ctx, name)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, len("authenticated"))
	if _, err := io.ReadFull(connection, buffer); err != nil || string(buffer) != "authenticated" {
		connection.Close()
		listener.Close()
		t.Fatalf("pre-read authentication response=%q err=%v", buffer, err)
	}
	connection.Close()
	if err := <-serverResult; err != nil {
		listener.Close()
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Listen(name)
	if err != nil {
		t.Fatalf("closed endpoint did not restart: %v", err)
	}
	restarted.Close()
}

func TestPipeAuthenticationRejectsNonPipeConnection(t *testing.T) {
	listener := &Listener{expectedLogonSID: "S-1-5-5-test"}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if err := listener.Authenticate(server); err == nil {
		t.Fatal("non-pipe connection was authenticated")
	}
}
