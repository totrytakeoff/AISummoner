package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const defaultShutdownTimeout = 15 * time.Second

type RuntimeOptions struct {
	Readiness      *Readiness
	PublicServer   *http.Server
	PublicListener net.Listener
	BridgeServer   *http.Server
	BridgeListener net.Listener

	CloseAgent    func()
	CloseTerminal func()
	CloseTunnel   func()
	CloseBridge   func(context.Context) error
	CloseDatabase func() error

	ShutdownTimeout time.Duration
}

// Runtime owns listener admission and the cross-package close ordering. The
// domain close functions remain responsible for joining their own workers.
type Runtime struct {
	readiness       *Readiness
	publicServer    *http.Server
	publicListener  net.Listener
	bridgeServer    *http.Server
	bridgeListener  net.Listener
	closeAgent      func()
	closeTerminal   func()
	closeTunnel     func()
	closeBridge     func(context.Context) error
	closeDatabase   func() error
	shutdownTimeout time.Duration

	stateMu      sync.Mutex
	started      bool
	closing      bool
	serveErrors  chan serveResult
	serveDone    sync.WaitGroup
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
	ready        chan struct{}
}

type serveResult struct {
	name string
	err  error
}

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Readiness == nil || options.PublicServer == nil || options.PublicServer.Handler == nil || options.PublicListener == nil ||
		options.CloseAgent == nil || options.CloseTerminal == nil || options.CloseTunnel == nil ||
		options.CloseDatabase == nil {
		return nil, errors.New("runtime public server, lifecycle closers, database, and readiness are required")
	}
	if (options.BridgeServer == nil) != (options.BridgeListener == nil) ||
		(options.BridgeServer != nil && options.CloseBridge == nil) ||
		(options.BridgeServer == nil && options.CloseBridge != nil) {
		return nil, errors.New("runtime bridge server, listener, and closer must be configured together")
	}
	if options.BridgeServer != nil {
		if options.BridgeServer.Handler == nil || !loopbackTCPListener(options.BridgeListener) {
			return nil, errors.New("runtime bridge requires a handler on a loopback TCP listener")
		}
		if options.BridgeListener.Addr().Network() == options.PublicListener.Addr().Network() &&
			options.BridgeListener.Addr().String() == options.PublicListener.Addr().String() {
			return nil, errors.New("runtime bridge and public listeners must be separate")
		}
	}
	if options.ShutdownTimeout <= 0 || options.ShutdownTimeout > time.Minute {
		options.ShutdownTimeout = defaultShutdownTimeout
	}
	return &Runtime{
		readiness:    options.Readiness,
		publicServer: options.PublicServer, publicListener: options.PublicListener,
		bridgeServer: options.BridgeServer, bridgeListener: options.BridgeListener,
		closeAgent: options.CloseAgent, closeTerminal: options.CloseTerminal,
		closeTunnel: options.CloseTunnel, closeBridge: options.CloseBridge,
		closeDatabase: options.CloseDatabase, shutdownTimeout: options.ShutdownTimeout,
		serveErrors: make(chan serveResult, 2), shutdownDone: make(chan struct{}), ready: make(chan struct{}),
	}, nil
}

func loopbackTCPListener(listener net.Listener) bool {
	address, ok := listener.Addr().(*net.TCPAddr)
	return ok && address.IP != nil && address.IP.IsLoopback()
}

// Run serves pre-bound listeners until ctx is canceled or one listener fails.
// Binding is intentionally performed by the composition root so the loopback
// Bridge is reserved before constructing the OpenCode Adapter.
func (runtime *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("runtime context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.stateMu.Lock()
	if runtime.started {
		runtime.stateMu.Unlock()
		return errors.New("runtime may only be run once")
	}
	if runtime.closing {
		runtime.stateMu.Unlock()
		return errors.New("runtime is shutting down")
	}
	runtime.started = true
	// Every WaitGroup Add and the ready transition occur under the same lock
	// that serializes first shutdown. Shutdown can therefore never Wait at zero
	// and race a late Serve goroutine Add, nor can Run re-open readiness after
	// quiescence.
	runtime.startServer("public", runtime.publicServer, runtime.publicListener)
	if runtime.bridgeServer != nil {
		runtime.startServer("bridge", runtime.bridgeServer, runtime.bridgeListener)
	}
	runtime.readiness.MarkReady()
	close(runtime.ready)
	runtime.stateMu.Unlock()

	var trigger error
	select {
	case <-ctx.Done():
	case result := <-runtime.serveErrors:
		if !runtime.isClosing() {
			if result.err == nil {
				result.err = errors.New("server stopped unexpectedly")
			}
			trigger = fmt.Errorf("%s HTTP server: %w", result.name, result.err)
		}
	}

	// Shutdown owns its own bounded cleanup context; this caller only joins it.
	shutdownError := runtime.Shutdown(context.Background())
	return errors.Join(trigger, shutdownError)
}

// Ready closes once both configured Serve goroutines have been started on
// their pre-bound listeners. It is used by deterministic composition tests.
func (runtime *Runtime) Ready() <-chan struct{} { return runtime.ready }

func (runtime *Runtime) isClosing() bool {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	return runtime.closing
}

func (runtime *Runtime) startServer(name string, server *http.Server, listener net.Listener) {
	runtime.serveDone.Add(1)
	go func() {
		defer runtime.serveDone.Done()
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		runtime.serveErrors <- serveResult{name: name, err: err}
	}()
}

// Shutdown is idempotent and joined. Runtime owns one background cleanup
// context bounded by ShutdownTimeout. A caller's context only bounds that
// caller's wait; cancellation never abandons process cleanup. If a trusted
// lifecycle owner misses the internal deadline, cleanup stops without closing
// SQLite underneath live workers.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runtime.shutdownOnce.Do(func() {
		runtime.stateMu.Lock()
		runtime.closing = true
		runtime.readiness.Quiesce()
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), runtime.shutdownTimeout)
		go func() {
			defer cancelCleanup()
			runtime.finishShutdown(cleanupContext)
		}()
		runtime.stateMu.Unlock()
	})
	select {
	case <-runtime.shutdownDone:
		return runtime.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *Runtime) finishShutdown(ctx context.Context) {
	defer close(runtime.shutdownDone)
	// Closing only the listener stops new accepts without tearing down active
	// SSE/WebSocket requests before their owning domain joins them.
	_ = runtime.publicListener.Close()

	if err := runJoined(ctx, "agent", runtime.closeAgent); err != nil {
		runtime.shutdownErr = err
		return
	}
	if err := runJoined(ctx, "terminal", runtime.closeTerminal); err != nil {
		runtime.shutdownErr = err
		return
	}
	if err := runJoined(ctx, "tunnel", runtime.closeTunnel); err != nil {
		runtime.shutdownErr = err
		return
	}
	if runtime.bridgeServer != nil {
		if err := runtime.closeBridge(ctx); err != nil {
			runtime.shutdownErr = fmt.Errorf("close Agent capability bridge: %w", err)
			return
		}
		if err := runtime.bridgeServer.Shutdown(ctx); !expectedHTTPShutdownError(err) {
			runtime.shutdownErr = fmt.Errorf("shut down bridge HTTP server: %w", err)
			return
		}
		_ = runtime.bridgeListener.Close()
	}
	if err := runtime.publicServer.Shutdown(ctx); !expectedHTTPShutdownError(err) {
		runtime.shutdownErr = fmt.Errorf("shut down public HTTP server: %w", err)
		return
	}
	if err := waitGroupContext(ctx, &runtime.serveDone); err != nil {
		runtime.shutdownErr = fmt.Errorf("join HTTP servers: %w", err)
		return
	}
	if err := runtime.closeDatabase(); err != nil {
		runtime.shutdownErr = fmt.Errorf("close SQLite: %w", err)
	}
}

func expectedHTTPShutdownError(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed)
}

func runJoined(ctx context.Context, name string, closeFunction func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		closeFunction()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("join %s lifecycle: %w", name, ctx.Err())
	}
}

func waitGroupContext(ctx context.Context, group *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
