package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type terminalEnd struct {
	category string
	code     websocket.StatusCode
	reason   string
}

var (
	endRemoteEOF = terminalEnd{category: "remote_eof", code: websocket.StatusNormalClosure, reason: "remote terminal closed"}
	endBrowser   = terminalEnd{category: "browser_closed", code: websocket.StatusNormalClosure, reason: "terminal closed"}
	endProtocol  = terminalEnd{category: "protocol_error", code: websocket.StatusPolicyViolation, reason: "invalid terminal control"}
	endTooLarge  = terminalEnd{category: "frame_too_large", code: websocket.StatusMessageTooBig, reason: "terminal frame too large"}
	endRemote    = terminalEnd{category: "remote_closed", code: websocket.StatusGoingAway, reason: "remote terminal unavailable"}
	endInternal  = terminalEnd{category: "internal_error", code: websocket.StatusInternalError, reason: "terminal unavailable"}
	endCanceled  = terminalEnd{category: "canceled", code: websocket.StatusGoingAway, reason: "terminal canceled"}
	endUnpaired  = terminalEnd{category: "device_invalidated", code: websocket.StatusGoingAway, reason: "device unpaired"}
	endShutdown  = terminalEnd{category: "server_shutdown", code: websocket.StatusGoingAway, reason: "server shutting down"}
)

func (handler *Handler) serveTerminal(request *http.Request, session *activeSession, connection websocketConnection) {
	result := endInternal
	var ptyHandle PTY
	var workers sync.WaitGroup
	workerCtx, cancelWorkers := context.WithCancelCause(context.WithoutCancel(session.ctx))
	// This defer is the sole post-upgrade cleanup owner, including panic paths
	// from injected boundaries. Worker panics are converted to endInternal below
	// so a boundary bug cannot strand a PTY, WebSocket, or limiter slot.
	defer func() {
		panicked := recover() != nil
		if panicked {
			result = endInternal
		}
		if cause := context.Cause(session.ctx); cause != nil && !panicked {
			result = terminalEndForCause(cause)
		}
		handler.cleanupTerminal(session, cancelWorkers, ptyHandle, connection, result, &workers)
		handler.logClosed(request, session.userID, session.deviceID, result.category)
	}()

	connection.SetReadLimit(maxTerminalFrame)
	var err error
	ptyHandle, err = handler.openPTY(session.ctx, session.deviceID, defaultCols, defaultRows)
	if err != nil || ptyHandle == nil {
		return
	}

	input, output := ptyHandle.Input(), ptyHandle.Output()
	if input == nil || output == nil {
		return
	}

	firstResult := make(chan terminalEnd, 1)
	var first sync.Once
	report := func(result terminalEnd) {
		first.Do(func() { firstResult <- result })
	}
	workers.Add(3)
	go func() {
		defer workers.Done()
		runTerminalWorker(report, func() terminalEnd {
			return handler.readBrowser(workerCtx, connection, input, ptyHandle)
		})
	}()
	go func() {
		defer workers.Done()
		runTerminalWorker(report, func() terminalEnd {
			return handler.writeBrowser(workerCtx, connection, output)
		})
	}()
	go func() {
		defer workers.Done()
		runTerminalWorker(report, func() terminalEnd {
			if err := ptyHandle.Wait(); err != nil {
				return endRemote
			}
			return endRemoteEOF
		})
	}()

	select {
	case result = <-firstResult:
	case <-session.ctx.Done():
		result = terminalEndForCause(context.Cause(session.ctx))
	}
}

func (handler *Handler) cleanupTerminal(
	session *activeSession,
	cancelWorkers context.CancelCauseFunc,
	ptyHandle PTY,
	connection websocketConnection,
	result terminalEnd,
	workers *sync.WaitGroup,
) {
	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		handler.closeWebSocket(connection, result)
	}()

	timer := time.NewTimer(handler.closeGrace)
	closeFinished := false
	select {
	case <-closeDone:
		closeFinished = true
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}

	// Preserve the static close frame during the short grace period. After it,
	// cancellation bounds a slow data writer/reader, PTY Close releases Remote
	// I/O and Wait, and ForceClose closes the captured raw socket when a silent
	// peer leaves coder/websocket's Close handshake blocked.
	session.cancel(errSessionEnded)
	cancelWorkers(errSessionEnded)
	safeClosePTY(ptyHandle)
	if !closeFinished {
		safeForceCloseWebSocket(connection)
	}
	workers.Wait()
	<-closeDone
}

func runTerminalWorker(report func(terminalEnd), work func() terminalEnd) {
	defer func() {
		if recover() != nil {
			report(endInternal)
		}
	}()
	report(work())
}

func safeClosePTY(ptyHandle PTY) {
	if ptyHandle == nil {
		return
	}
	defer func() { _ = recover() }()
	_ = ptyHandle.Close()
}

func terminalEndForCause(cause error) terminalEnd {
	switch {
	case errors.Is(cause, errDeviceInvalidated):
		return endUnpaired
	case errors.Is(cause, errHandlerClosed):
		return endShutdown
	default:
		return endCanceled
	}
}

func (handler *Handler) readBrowser(ctx context.Context, connection websocketConnection, input io.Writer, ptyHandle PTY) terminalEnd {
	for {
		messageType, contents, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return terminalEndForCause(context.Cause(ctx))
			}
			switch websocket.CloseStatus(err) {
			case websocket.StatusNormalClosure, websocket.StatusGoingAway:
				return endBrowser
			case websocket.StatusMessageTooBig:
				return endTooLarge
			default:
				return endRemote
			}
		}
		switch messageType {
		case websocket.MessageBinary:
			if err := writeAll(input, contents); err != nil {
				return endRemote
			}
		case websocket.MessageText:
			cols, rows, err := decodeResize(contents)
			if err != nil {
				return endProtocol
			}
			if err := ptyHandle.Resize(cols, rows); err != nil {
				return endRemote
			}
		default:
			return endProtocol
		}
	}
}

func (handler *Handler) writeBrowser(ctx context.Context, connection websocketConnection, output io.Reader) terminalEnd {
	buffer := make([]byte, 32*1024)
	for {
		read, err := output.Read(buffer)
		if read > 0 {
			writeContext, cancel := context.WithTimeout(ctx, handler.writeTimeout)
			writeErr := connection.Write(writeContext, websocket.MessageBinary, buffer[:read])
			cancel()
			if writeErr != nil {
				if ctx.Err() != nil {
					return terminalEndForCause(context.Cause(ctx))
				}
				return endRemote
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return endRemoteEOF
			}
			if ctx.Err() != nil {
				return terminalEndForCause(context.Cause(ctx))
			}
			return endRemote
		}
		if read == 0 {
			return endInternal
		}
	}
}

func writeAll(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if written < 0 || written > len(contents) {
			return io.ErrShortWrite
		}
		contents = contents[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type resizeMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func decodeResize(contents []byte) (uint16, uint16, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var message resizeMessage
	if err := decoder.Decode(&message); err != nil {
		return 0, 0, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return 0, 0, errors.New("terminal control must contain one JSON object")
	}
	if message.Type != "terminal.resize" || message.Cols < 1 || message.Cols > 500 || message.Rows < 1 || message.Rows > 300 {
		return 0, 0, errors.New("invalid terminal resize")
	}
	return uint16(message.Cols), uint16(message.Rows), nil
}

func (handler *Handler) closeWebSocket(connection websocketConnection, result terminalEnd) {
	if connection == nil {
		return
	}
	defer func() {
		if recover() != nil {
			safeForceCloseWebSocket(connection)
		}
	}()
	if err := connection.Close(result.code, result.reason); err != nil {
		safeForceCloseWebSocket(connection)
	}
}

func safeForceCloseWebSocket(connection websocketConnection) {
	defer func() { _ = recover() }()
	_ = connection.ForceClose()
}
