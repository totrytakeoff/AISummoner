package wsstream

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestConnCarriesStreamAndSupportsConcurrentWrites(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ws, err := websocket.Accept(writer, request, nil)
		if err != nil {
			serverDone <- err
			return
		}
		stream := New(request.Context(), ws)
		defer stream.Close()
		contents, err := io.ReadAll(stream)
		if err == nil && len(contents) != 1024 {
			err = io.ErrUnexpectedEOF
		}
		serverDone <- err
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := New(ctx, ws)
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := stream.Write(make([]byte, 64)); err != nil {
				t.Errorf("write: %v", err)
			}
		}()
	}
	group.Wait()
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestConnDeadlineInterruptsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ws, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := New(ctx, ws)
	defer stream.Close()
	if err := stream.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := stream.Read(buffer); err == nil {
		t.Fatal("expected read deadline")
	}
}

func TestConnConcurrentCloseIsIdempotent(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	stream := &Conn{underlying: client}
	const callers = 32
	errorsOut := make(chan error, callers)
	var group sync.WaitGroup
	for index := 0; index < callers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsOut <- stream.Close()
		}()
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent Close returned %v", err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("repeated Close returned %v", err)
	}
}

func TestConnWriteDeadlineInterruptsBlockedWrite(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	stream := &Conn{underlying: client}
	defer stream.Close()
	if err := stream.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := stream.Write([]byte("blocked until deadline"))
	if err == nil {
		t.Fatal("expected write deadline error")
	}
	var networkError net.Error
	if !errors.As(err, &networkError) || !networkError.Timeout() {
		t.Fatalf("write error = %v, want timeout", err)
	}
}
