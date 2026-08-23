package clientipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/aisummoner/aisummoner/internal/remoteclient"
	"golang.org/x/sys/unix"
)

const (
	defaultHandlerTimeout = 5 * time.Second
	responseWriteGrace    = 250 * time.Millisecond
)

type Controller interface {
	Snapshot() remoteclient.Snapshot
	Events(uint64, int) []remoteclient.Event
	Pause(context.Context) error
	Resume() error
	RefreshPairing(context.Context) error
}

type ServerOptions struct {
	SocketPath string
	Controller Controller
	Logger     *slog.Logger
}

type Server struct {
	socketPath   string
	controller   Controller
	logger       *slog.Logger
	effectiveUID uint32
	peerUID      func(*net.UnixConn) (uint32, error)
	timeout      time.Duration
	maxHandlers  int
}

type socketIdentity struct {
	device uint64
	inode  uint64
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Controller == nil {
		return nil, errors.New("Remote controller is required")
	}
	if !filepath.IsAbs(options.SocketPath) || len(options.SocketPath) > 100 {
		return nil, errors.New("daemon socket path must be a bounded absolute path")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Server{
		socketPath: options.SocketPath, controller: options.Controller, logger: options.Logger,
		effectiveUID: uint32(os.Geteuid()), peerUID: unixPeerUID,
		timeout: defaultHandlerTimeout, maxHandlers: MaxHandlers,
	}, nil
}

func DefaultSocketPath(dataDirectory string) string {
	return filepath.Join(dataDirectory, "client.sock")
}

func (server *Server) Serve(ctx context.Context) error {
	if err := server.prepareParent(); err != nil {
		return err
	}
	if err := server.removeStaleSocket(); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on private client socket: %w", err)
	}
	if err := os.Chmod(server.socketPath, 0o600); err != nil {
		listener.Close()
		return fmt.Errorf("protect private client socket: %w", err)
	}
	identity, err := inspectSocket(server.socketPath, server.effectiveUID)
	if err != nil {
		listener.Close()
		return err
	}

	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	defer listener.Close()
	defer server.removeExactSocket(identity)

	semaphore := make(chan struct{}, server.maxHandlers)
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept private client connection: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { <-semaphore }()
				server.handle(ctx, connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (server *Server) handle(parent context.Context, connection *net.UnixConn) {
	defer connection.Close()
	// The controller operation itself remains bounded by server.timeout. Keep a
	// small, still-bounded tail so a TIMEOUT envelope can be written after that
	// context expires instead of racing the socket deadline at the same instant.
	if err := connection.SetDeadline(time.Now().Add(server.timeout + responseWriteGrace)); err != nil {
		return
	}
	peerUID, err := server.peerUID(connection)
	if err != nil || peerUID != server.effectiveUID {
		server.logger.Warn("rejected local client peer")
		return
	}
	contents, err := readFrame(connection)
	if err != nil {
		server.writeError(connection, "", "INVALID_REQUEST", "invalid local request")
		return
	}
	var request Request
	if err := decodeStrict(contents, &request); err != nil || request.Version != Version ||
		!validRequestID(request.ID) || len(request.Method) == 0 || len(request.Params) == 0 {
		server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
		return
	}
	if !knownMethod(request.Method) {
		server.writeError(connection, request.ID, "METHOD_NOT_FOUND", "local method is not supported")
		return
	}
	operationContext, cancel := context.WithTimeout(parent, server.timeout)
	defer cancel()

	switch request.Method {
	case MethodStatusGet:
		if err := decodeEmptyParams(request.Params); err != nil {
			server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
			return
		}
		server.writeResult(connection, request.ID, server.controller.Snapshot())
	case MethodEventsList:
		var params EventsListParams
		if err := decodeStrict(request.Params, &params); err != nil || params.Limit < 1 || params.Limit > remoteclient.MaxEvents {
			server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
			return
		}
		events := server.controller.Events(params.AfterSequence, params.Limit)
		next := params.AfterSequence
		if len(events) != 0 {
			next = events[len(events)-1].Sequence
		}
		server.writeResult(connection, request.ID, EventsListResult{Events: events, NextSequence: next})
	case MethodDaemonPause:
		if err := decodeEmptyParams(request.Params); err != nil {
			server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
			return
		}
		if err := server.controller.Pause(operationContext); err != nil {
			server.writeControllerError(connection, request.ID, err)
			return
		}
		server.writeResult(connection, request.ID, struct{}{})
	case MethodDaemonResume:
		if err := decodeEmptyParams(request.Params); err != nil {
			server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
			return
		}
		if err := server.controller.Resume(); err != nil {
			server.writeControllerError(connection, request.ID, err)
			return
		}
		server.writeResult(connection, request.ID, struct{}{})
	case MethodPairingRefresh:
		if err := decodeEmptyParams(request.Params); err != nil {
			server.writeError(connection, request.ID, "INVALID_REQUEST", "invalid local request")
			return
		}
		if err := server.controller.RefreshPairing(operationContext); err != nil {
			server.writeControllerError(connection, request.ID, err)
			return
		}
		server.writeResult(connection, request.ID, PairingRefreshResult{ClosesActiveSessions: true})
	}
}

func (server *Server) writeControllerError(connection net.Conn, requestID string, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		server.writeError(connection, requestID, "TIMEOUT", "local operation did not finish in time")
	case errors.Is(err, remoteclient.ErrNoPairingOffer):
		server.writeError(connection, requestID, "NO_PAIRING_OFFER", "no pairing code can be refreshed")
	case errors.Is(err, remoteclient.ErrNotRunning), errors.Is(err, remoteclient.ErrAlreadyStopped):
		server.writeError(connection, requestID, "DAEMON_UNAVAILABLE", "Remote service is unavailable")
	default:
		server.writeError(connection, requestID, "OPERATION_FAILED", "local operation failed")
	}
}

func (server *Server) writeResult(connection net.Conn, requestID string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		server.writeError(connection, requestID, "INTERNAL_ERROR", "local service failed")
		return
	}
	server.writeResponse(connection, Response{Version: Version, ID: requestID, OK: true, Result: encoded})
}

func (server *Server) writeError(connection net.Conn, requestID, code, message string) {
	if !validRequestID(requestID) {
		requestID = ""
	}
	server.writeResponse(connection, Response{Version: Version, ID: requestID, OK: false, Error: &Error{Code: code, Message: message}})
}

func (server *Server) writeResponse(connection net.Conn, response Response) {
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded)+1 > MaxFrameBytes {
		return
	}
	encoded = append(encoded, '\n')
	_, _ = connection.Write(encoded)
}

func (server *Server) prepareParent() error {
	parent := filepath.Dir(server.socketPath)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect daemon socket directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("daemon socket directory must be a non-symlink mode-0700 directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != server.effectiveUID {
		return errors.New("daemon socket directory must be owned by the daemon user")
	}
	return nil
}

func (server *Server) removeStaleSocket() error {
	_, err := os.Lstat(server.socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect daemon socket: %w", err)
	}
	if _, err := inspectSocket(server.socketPath, server.effectiveUID); err != nil {
		return err
	}
	if err := os.Remove(server.socketPath); err != nil {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
}

func (server *Server) removeExactSocket(identity socketIdentity) {
	current, err := inspectSocket(server.socketPath, server.effectiveUID)
	if err != nil || current != identity {
		return
	}
	_ = os.Remove(server.socketPath)
}

func inspectSocket(path string, owner uint32) (socketIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return socketIdentity{}, fmt.Errorf("inspect daemon socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return socketIdentity{}, errors.New("daemon socket path is not an owned mode-0600 socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return socketIdentity{}, errors.New("daemon socket is not owned by the daemon user")
	}
	return socketIdentity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}

func unixPeerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if controlErr != nil {
		return 0, controlErr
	}
	if credential == nil {
		return 0, errors.New("local peer credentials are unavailable")
	}
	return credential.Uid, nil
}

func readFrame(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: MaxFrameBytes + 1}
	contents, err := bufio.NewReaderSize(limited, 4096).ReadBytes('\n')
	if err != nil || len(contents) < 2 || len(contents) > MaxFrameBytes || contents[len(contents)-1] != '\n' {
		return nil, errors.New("invalid local frame")
	}
	return bytes.TrimSuffix(contents, []byte{'\n'}), nil
}

func decodeEmptyParams(contents []byte) error {
	var params EmptyParams
	return decodeStrict(contents, &params)
}
