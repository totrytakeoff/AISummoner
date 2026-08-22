package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeShutdownOrderAndDatabaseLast(t *testing.T) {
	publicListener := listenLoopback(t)
	bridgeListener := listenLoopback(t)
	readiness := NewReadiness()
	var mu sync.Mutex
	order := make([]string, 0, 7)
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness:       readiness,
		PublicServer:    &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("public")) })},
		PublicListener:  publicListener,
		BridgeServer:    &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("bridge")) })},
		BridgeListener:  bridgeListener,
		CloseAgent:      func() { record("agent") },
		CloseTerminal:   func() { record("terminal") },
		CloseTunnel:     func() { record("tunnel") },
		CloseBridge:     func(context.Context) error { record("bridge"); return nil },
		CloseDatabase:   func() error { record("database"); return nil },
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitRuntimeReady(t, runtime)
	if body := getBody(t, "http://"+publicListener.Addr().String()); body != "public" {
		t.Fatalf("public response = %q", body)
	}
	if body := getBody(t, "http://"+bridgeListener.Addr().String()); body != "bridge" {
		t.Fatalf("bridge response = %q", body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Runtime shutdown did not join")
	}
	mu.Lock()
	gotOrder := strings.Join(order, ",")
	mu.Unlock()
	if gotOrder != "agent,terminal,tunnel,bridge,database" {
		t.Fatalf("shutdown order = %q", gotOrder)
	}
	if readiness.IsReady() {
		t.Fatal("runtime remained ready after shutdown")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated joined Shutdown: %v", err)
	}
}

func TestRuntimeQuiescesBeforeJoinedCloseAndRejectsNewAdmission(t *testing.T) {
	listener := listenLoopback(t)
	readiness := NewReadiness()
	releaseAgent := make(chan struct{})
	agentStarted := make(chan struct{})
	databaseClosed := make(chan struct{})
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness: readiness,
		PublicServer: &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			if !readiness.IsReady() {
				http.Error(writer, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = writer.Write([]byte("ok"))
		})},
		PublicListener: listener,
		CloseAgent:     func() { close(agentStarted); <-releaseAgent },
		CloseTerminal:  func() {}, CloseTunnel: func() {},
		CloseDatabase:   func() error { close(databaseClosed); return nil },
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitRuntimeReady(t, runtime)
	cancel()
	select {
	case <-agentStarted:
	case <-time.After(time.Second):
		t.Fatal("agent close did not start")
	}
	if readiness.IsReady() {
		t.Fatal("runtime did not quiesce before joining Agent")
	}
	select {
	case <-databaseClosed:
		t.Fatal("database closed while Agent lifecycle was blocked")
	default:
	}
	close(releaseAgent)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeTreatsWrappedClosedListenerAsJoinedShutdown(t *testing.T) {
	listener := &wrappedClosedListener{Listener: listenLoopback(t)}
	t.Cleanup(func() { _ = listener.Listener.Close() })
	readiness := NewReadiness()
	var mu sync.Mutex
	order := make([]string, 0, 4)
	record := func(value string) {
		mu.Lock()
		order = append(order, value)
		mu.Unlock()
	}
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness: readiness,
		PublicServer: &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("ok"))
		})},
		PublicListener: listener,
		CloseAgent:     func() { record("agent") }, CloseTerminal: func() { record("terminal") },
		CloseTunnel: func() { record("tunnel") }, CloseDatabase: func() error { record("database"); return nil },
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitRuntimeReady(t, runtime)
	// Force Serve to track and use the controlled listener before shutdown.
	if got := getBody(t, "http://"+listener.Addr().String()); got != "ok" {
		t.Fatalf("public response = %q", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wrapped net.ErrClosed was not treated as joined shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapped-close Runtime did not join")
	}
	if listener.CloseCalls() < 2 {
		t.Fatalf("controlled listener close calls = %d, Shutdown did not observe the wrapped net.ErrClosed", listener.CloseCalls())
	}
	mu.Lock()
	gotOrder := strings.Join(order, ",")
	mu.Unlock()
	if gotOrder != "agent,terminal,tunnel,database" {
		t.Fatalf("wrapped-close shutdown order = %q", gotOrder)
	}
}

func TestExpectedHTTPShutdownErrorIsNarrow(t *testing.T) {
	if !expectedHTTPShutdownError(nil) ||
		!expectedHTTPShutdownError(fmt.Errorf("wrapped: %w", net.ErrClosed)) ||
		!expectedHTTPShutdownError(fmt.Errorf("wrapped: %w", http.ErrServerClosed)) {
		t.Fatal("expected HTTP shutdown condition was rejected")
	}
	if expectedHTTPShutdownError(errors.New("sentinel non-benign shutdown failure")) {
		t.Fatal("ordinary HTTP shutdown error was suppressed")
	}
}

func TestRuntimeDeadlineDoesNotCloseDatabaseUnderBlockedOwner(t *testing.T) {
	listener := listenLoopback(t)
	readiness := NewReadiness()
	blocked := make(chan struct{})
	databaseClosed := false
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness:    readiness,
		PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: listener,
		CloseAgent: func() { <-blocked }, CloseTerminal: func() {}, CloseTunnel: func() {},
		CloseDatabase:   func() error { databaseClosed = true; return nil },
		ShutdownTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitRuntimeReady(t, runtime)
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked shutdown exceeded its total bound")
	}
	if databaseClosed {
		t.Fatal("database closed underneath a blocked lifecycle owner")
	}
	close(blocked)
}

func TestShutdownCallerTimeoutDoesNotAbandonInternalCleanup(t *testing.T) {
	listener := listenLoopback(t)
	readiness := NewReadiness()
	agentStarted := make(chan struct{})
	releaseAgent := make(chan struct{})
	databaseClosed := make(chan struct{})
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness:    readiness,
		PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: listener,
		CloseAgent:    func() { close(agentStarted); <-releaseAgent },
		CloseTerminal: func() {}, CloseTunnel: func() {},
		CloseDatabase:   func() error { close(databaseClosed); return nil },
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- runtime.Run(context.Background()) }()
	waitRuntimeReady(t, runtime)

	callerContext, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	if err := runtime.Shutdown(callerContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("short first Shutdown caller error = %v", err)
	}
	select {
	case <-agentStarted:
	case <-time.After(time.Second):
		t.Fatal("internal cleanup did not continue after first caller cancellation")
	}
	select {
	case <-databaseClosed:
		t.Fatal("database closed before blocked Agent joined")
	default:
	}
	close(releaseAgent)

	joinContext, cancelJoin := context.WithTimeout(context.Background(), time.Second)
	defer cancelJoin()
	if err := runtime.Shutdown(joinContext); err != nil {
		t.Fatalf("second Shutdown caller did not join final cleanup: %v", err)
	}
	select {
	case <-databaseClosed:
	default:
		t.Fatal("database was not closed last after lifecycle release")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run after external Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not join externally initiated Shutdown")
	}
}

func TestRuntimeRejectsPartialBridge(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	_, err := NewRuntime(RuntimeOptions{
		Readiness: NewReadiness(), PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: listener,
		BridgeServer: &http.Server{Handler: http.NotFoundHandler()},
		CloseAgent:   func() {}, CloseTerminal: func() {}, CloseTunnel: func() {}, CloseDatabase: func() error { return nil },
	})
	if err == nil {
		t.Fatal("partial Bridge runtime was accepted")
	}
}

func TestRuntimeRejectsNonLoopbackBridge(t *testing.T) {
	publicListener := listenLoopback(t)
	defer publicListener.Close()
	bridgeListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind wildcard fixture: %v", err)
	}
	defer bridgeListener.Close()
	_, err = NewRuntime(RuntimeOptions{
		Readiness:    NewReadiness(),
		PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: publicListener,
		BridgeServer: &http.Server{Handler: http.NotFoundHandler()}, BridgeListener: bridgeListener,
		CloseAgent: func() {}, CloseTerminal: func() {}, CloseTunnel: func() {},
		CloseBridge: func(context.Context) error { return nil }, CloseDatabase: func() error { return nil },
	})
	if err == nil {
		t.Fatal("non-loopback Bridge listener was accepted")
	}
}

func TestShutdownBeforeRunCannotReopenAdmissionOrAddServeWorkers(t *testing.T) {
	listener := listenLoopback(t)
	readiness := NewReadiness()
	databaseClosed := make(chan struct{})
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness:    readiness,
		PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: listener,
		CloseAgent: func() {}, CloseTerminal: func() {}, CloseTunnel: func() {},
		CloseDatabase:   func() error { close(databaseClosed); return nil },
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-databaseClosed:
	default:
		t.Fatal("shutdown-before-run did not complete")
	}
	if err := runtime.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("Run after Shutdown error = %v", err)
	}
	if readiness.IsReady() {
		t.Fatal("Run after Shutdown reopened readiness")
	}
	select {
	case <-runtime.Ready():
		t.Fatal("Run after Shutdown published a ready checkpoint")
	default:
	}
}

func TestCanceledRunContextDoesNotPublishReadiness(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	readiness := NewReadiness()
	databaseClosed := false
	runtime, err := NewRuntime(RuntimeOptions{
		Readiness:    readiness,
		PublicServer: &http.Server{Handler: http.NotFoundHandler()}, PublicListener: listener,
		CloseAgent: func() {}, CloseTerminal: func() {}, CloseTunnel: func() {},
		CloseDatabase: func() error { databaseClosed = true; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context error = %v", err)
	}
	if readiness.IsReady() || databaseClosed {
		t.Fatalf("canceled Run changed readiness/database: ready=%v databaseClosed=%v", readiness.IsReady(), databaseClosed)
	}
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

type wrappedClosedListener struct {
	net.Listener
	mu        sync.Mutex
	closeOnce sync.Once
	closes    int
}

func (listener *wrappedClosedListener) Close() error {
	listener.mu.Lock()
	listener.closes++
	closes := listener.closes
	listener.mu.Unlock()
	// Runtime's admission close is the first call. Keep Accept blocked until
	// http.Server.Shutdown performs the second call, then return a wrapped
	// net.ErrClosed from that exact path while also releasing Serve.
	if closes >= 2 {
		listener.closeOnce.Do(func() { _ = listener.Listener.Close() })
	}
	return fmt.Errorf("controlled listener close: %w", net.ErrClosed)
}

func (listener *wrappedClosedListener) CloseCalls() int {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return listener.closes
}

func waitRuntimeReady(t *testing.T, runtime *Runtime) {
	t.Helper()
	select {
	case <-runtime.Ready():
	case <-time.After(time.Second):
		t.Fatal("runtime did not become ready")
	}
}

func getBody(t *testing.T, target string) string {
	t.Helper()
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
