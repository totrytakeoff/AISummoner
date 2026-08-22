package app

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDispatcherExactPathShapes(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	dispatcher := newProbeDispatcher(t, readiness, nil)
	tests := []struct {
		path string
		want string
	}{
		{path: "/api/v1/tunnel", want: "tunnel"},
		{path: "/api/v1/tunnel/", want: "browser"},
		{path: "/api/v1/devices/dev_one/terminal", want: "terminal"},
		{path: "/api/v1/devices//terminal", want: "browser"},
		{path: "/api/v1/devices/dev/extra/terminal", want: "browser"},
		{path: "/api/v1/devices/dev_one/agent-sessions", want: "agent"},
		{path: "/api/v1/agent-provider/deepseek", want: "agent"},
		{path: "/api/v1/agent-provider/deepseek/", want: "browser"},
		{path: "/api/v1/agent-sessions/ags_one", want: "agent"},
		{path: "/api/v1/agent-sessions/ags_one/messages", want: "agent"},
		{path: "/api/v1/agent-sessions/ags_one/events", want: "agent"},
		{path: "/api/v1/agent-sessions/ags_one/unknown", want: "browser"},
		{path: "/api/v1/tool-calls/tol_one/decision", want: "agent"},
		{path: "/api/v1/tool-calls/tol/extra/decision", want: "browser"},
		{path: "/healthz", want: "health"},
		{path: "/healthz/details", want: "NOT_FOUND"},
		{path: "/api", want: "browser"},
		{path: "/api/v1/missing", want: "browser"},
		{path: "/internal/opencode/remote-exec", want: "NOT_FOUND"},
		{path: "/devices/dev_one", want: "static"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			dispatcher.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "http://example.test"+test.path, nil))
			if !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("route body = %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}

func TestDispatcherQuiescencePreservesHealthAndRejectsNewWork(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	dispatcher := newProbeDispatcher(t, readiness, nil)
	readiness.Quiesce()
	readiness.MarkReady()
	if readiness.IsReady() {
		t.Fatal("quiesced readiness was reopened")
	}

	response := httptest.NewRecorder()
	dispatcher.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/devices", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "SERVER_UNAVAILABLE") {
		t.Fatalf("quiesced API response = %d %q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	dispatcher.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/healthz", nil))
	if response.Body.String() != "health" {
		t.Fatalf("health was not dispatched while quiesced: %q", response.Body.String())
	}
}

func TestDispatcherForwardsOriginalResponseWriterCapabilities(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	writer := &capabilityWriter{header: make(http.Header)}
	checked := false
	probe := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if response != writer {
			t.Fatal("dispatcher replaced the ResponseWriter")
		}
		if _, ok := response.(http.Hijacker); !ok {
			t.Fatal("Hijacker capability was lost")
		}
		controller := http.NewResponseController(response)
		if err := controller.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("write deadline capability was lost: %v", err)
		}
		if err := controller.Flush(); err != nil {
			t.Fatalf("flush capability was lost: %v", err)
		}
		checked = true
	})
	dispatcher := newProbeDispatcher(t, readiness, map[string]http.Handler{"agent": probe})
	dispatcher.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/agent-sessions/ags_one/events", nil))
	if !checked || writer.flushes != 1 || writer.deadlines != 1 {
		t.Fatalf("capability probe checked=%v flushes=%d deadlines=%d", checked, writer.flushes, writer.deadlines)
	}
}

func TestDispatcherRealWebSocketAndSSECapabilities(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	websocketHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		_ = connection.CloseNow()
	})
	sseRelease := make(chan struct{})
	sseHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		controller := http.NewResponseController(writer)
		if err := controller.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(": connected\n\n"))
		if err := controller.Flush(); err != nil {
			return
		}
		select {
		case <-sseRelease:
		case <-request.Context().Done():
		}
	})
	dispatcher := newProbeDispatcher(t, readiness, map[string]http.Handler{
		"tunnel": websocketHandler, "terminal": websocketHandler, "agent": sseHandler,
	})
	server := httptest.NewServer(dispatcher)
	defer server.Close()
	for _, path := range []string{"/api/v1/tunnel", "/api/v1/devices/dev_one/terminal"} {
		connection, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
		if err != nil {
			t.Fatalf("WebSocket route %s: %v", path, err)
		}
		_ = connection.CloseNow()
	}

	response, err := http.Get(server.URL + "/api/v1/agent-sessions/ags_one/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != ": connected\n" {
		t.Fatalf("immediate SSE frame = %q, %v", line, err)
	}
	close(sseRelease)
}

func newProbeDispatcher(t *testing.T, readiness *Readiness, overrides map[string]http.Handler) *Dispatcher {
	t.Helper()
	probe := func(name string) http.Handler {
		if override := overrides[name]; override != nil {
			return override
		}
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(name)) })
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		Readiness: readiness, Tunnel: probe("tunnel"), Terminal: probe("terminal"), Agent: probe("agent"),
		Health: probe("health"), Browser: probe("browser"), Static: probe("static"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

type capabilityWriter struct {
	header    http.Header
	flushes   int
	deadlines int
}

func (writer *capabilityWriter) Header() http.Header     { return writer.header }
func (*capabilityWriter) Write(body []byte) (int, error) { return len(body), nil }
func (*capabilityWriter) WriteHeader(int)                {}
func (*capabilityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("not used")
}
func (writer *capabilityWriter) Flush() { writer.flushes++ }
func (writer *capabilityWriter) SetWriteDeadline(time.Time) error {
	writer.deadlines++
	return nil
}

var _ http.ResponseWriter = (*capabilityWriter)(nil)
var _ http.Hijacker = (*capabilityWriter)(nil)
var _ http.Flusher = (*capabilityWriter)(nil)

type healthProbe struct {
	mu       sync.Mutex
	err      error
	deadline bool
}

func (probe *healthProbe) HasUsers(ctx context.Context) (bool, error) {
	_, deadline := ctx.Deadline()
	probe.mu.Lock()
	probe.deadline = deadline
	probe.mu.Unlock()
	return true, probe.err
}

func TestHealthReadinessSQLiteAndMethods(t *testing.T) {
	readiness := NewReadiness()
	probe := &healthProbe{}
	health, err := NewHealth(readiness, probe, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		health.ServeHTTP(response, httptest.NewRequest(method, "http://example.test/healthz", nil))
		return response
	}
	if response := request(http.MethodGet); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "NOT_READY") {
		t.Fatalf("not-ready response = %d %q", response.Code, response.Body.String())
	}
	readiness.MarkReady()
	if response := request(http.MethodPost); response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("wrong-method response = %d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
	if response := request(http.MethodGet); response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("healthy response = %d %q", response.Code, response.Body.String())
	}
	probe.mu.Lock()
	deadline := probe.deadline
	probe.mu.Unlock()
	if !deadline {
		t.Fatal("SQLite health probe had no deadline")
	}
	probe.err = errors.New("database unavailable with private detail")
	if response := request(http.MethodGet); response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "DATABASE_UNAVAILABLE") || strings.Contains(response.Body.String(), "private detail") {
		t.Fatalf("failed probe response = %d %q", response.Code, response.Body.String())
	}
}
