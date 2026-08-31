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
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/remoteclient"
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
	Endpoint   string
	Controller Controller
	Logger     *slog.Logger
}

type Server struct {
	endpoint         string
	controller       Controller
	logger           *slog.Logger
	transport        localTransport
	authenticatePeer func(net.Conn) error
	timeout          time.Duration
	maxHandlers      int
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Controller == nil {
		return nil, errors.New("Remote controller is required")
	}
	transport := currentTransport()
	if err := transport.ValidateEndpoint(options.Endpoint); err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Server{
		endpoint: options.Endpoint, controller: options.Controller, logger: options.Logger,
		transport: transport, timeout: defaultHandlerTimeout, maxHandlers: MaxHandlers,
	}, nil
}

func (server *Server) Serve(ctx context.Context) error {
	listener, err := server.transport.Listen(server.endpoint)
	if err != nil {
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

	semaphore := make(chan struct{}, server.maxHandlers)
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		connection, err := listener.Accept()
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
				server.handle(ctx, listener, connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (server *Server) handle(parent context.Context, listener authenticatedListener, connection net.Conn) {
	defer connection.Close()
	// The controller operation itself remains bounded by server.timeout. Keep a
	// small, still-bounded tail so a TIMEOUT envelope can be written after that
	// context expires instead of racing the transport deadline at that instant.
	if err := connection.SetDeadline(time.Now().Add(server.timeout + responseWriteGrace)); err != nil {
		return
	}
	authenticate := listener.Authenticate
	if server.authenticatePeer != nil {
		authenticate = server.authenticatePeer
	}
	if err := authenticate(connection); err != nil {
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
