// Package terminal exposes the authenticated browser WebSocket to Remote SSH
// PTY bridge. It owns browser-session admission and live Terminal lifecycle;
// it does not own SSH or Tunnel implementation details.
package terminal

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/coder/websocket"
)

const (
	sessionCookieName = "aisummoner_session"
	terminalPrefix    = "/api/v1/devices/"
	terminalSuffix    = "/terminal"

	maxTerminalsPerUser = 4
	maxTerminalFrame    = 64 * 1024
	defaultCols         = 80
	defaultRows         = 24
	defaultWriteTimeout = 10 * time.Second
	defaultCloseGrace   = 250 * time.Millisecond
)

var (
	errDeviceInvalidated = errors.New("terminal device was invalidated")
	errHandlerClosed     = errors.New("terminal handler is closed")
	errSessionEnded      = errors.New("terminal session ended")

	errTerminalLimit = errors.New("terminal concurrency limit reached")
)

// Authenticator is the narrow Task001 web-session boundary.
type Authenticator interface {
	Authenticate(context.Context, string, time.Time) (store.User, error)
}

// DeviceLookup returns a Device only when it is owned by the supplied user.
type DeviceLookup interface {
	DeviceByOwner(context.Context, string, string) (store.Device, error)
}

// OnlineState is the authoritative in-memory Device connection state.
type OnlineState interface {
	IsOnline(deviceID string) bool
}

// PTY is the task-owned closure boundary around Task003's strict SSH PTY.
type PTY interface {
	Input() io.Writer
	Output() io.Reader
	Resize(cols, rows uint16) error
	Wait() error
	Close() error
}

// OpenPTYFunc opens one Remote PTY. Task008 adapts sshclient.Dialer.OpenPTY to
// this closure so no covariance-breaking concrete SSH type crosses packages.
type OpenPTYFunc func(context.Context, string, uint16, uint16) (PTY, error)

type Options struct {
	Auth          Authenticator
	Devices       DeviceLookup
	Online        OnlineState
	OpenPTY       OpenPTYFunc
	AllowedOrigin string
	Logger        *slog.Logger
	Now           func() time.Time
}

// Handler is safe for concurrent HTTP requests and lifecycle invalidation.
type Handler struct {
	auth          Authenticator
	devices       DeviceLookup
	online        OnlineState
	openPTY       OpenPTYFunc
	allowedOrigin string
	logger        *slog.Logger
	now           func() time.Time
	accept        acceptWebSocketFunc
	writeTimeout  time.Duration
	closeGrace    time.Duration

	mu          sync.Mutex
	closed      bool
	closeOnce   sync.Once
	closeDone   chan struct{}
	generations map[string]uint64
	userCounts  map[string]int
	byDevice    map[string]map[*activeSession]struct{}
}

func New(options Options) (*Handler, error) {
	if options.Auth == nil || options.Devices == nil || options.Online == nil || options.OpenPTY == nil {
		return nil, errors.New("terminal auth, device lookup, online state, and PTY opener are required")
	}
	if options.AllowedOrigin == "" {
		return nil, errors.New("terminal allowed origin is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Handler{
		auth: options.Auth, devices: options.Devices, online: options.Online,
		openPTY: options.OpenPTY, allowedOrigin: options.AllowedOrigin,
		logger: options.Logger, now: options.Now, accept: websocketAccept,
		writeTimeout: defaultWriteTimeout, closeGrace: defaultCloseGrace,
		closeDone: make(chan struct{}), generations: make(map[string]uint64),
		userCounts: make(map[string]int), byDevice: make(map[string]map[*activeSession]struct{}),
	}, nil
}

// Handler returns the standalone HTTP surface mounted by Task008.
func (handler *Handler) Handler() http.Handler { return handler }

type requestIDKey struct{}

type requestState struct {
	upgraded bool
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID, err := id.New("req")
	if err != nil {
		requestID = "req_unavailable"
	}
	writer.Header().Set("X-Request-ID", requestID)
	request = request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID))
	state := &requestState{}
	defer func() {
		if recover() == nil {
			return
		}
		handler.logger.Error("terminal request panic", "request_id", requestID, "path", request.URL.Path)
		if !state.upgraded {
			handler.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
		}
	}()
	handler.serveHTTP(writer, request, state)
}

func (handler *Handler) serveHTTP(writer http.ResponseWriter, request *http.Request, state *requestState) {
	deviceID, pathMatches := terminalDeviceID(request.URL.Path)
	if !pathMatches {
		handler.writeError(writer, request, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		handler.writeError(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if origins := request.Header.Values("Origin"); len(origins) != 1 || origins[0] != handler.allowedOrigin {
		handler.writeError(writer, request, http.StatusForbidden, "ORIGIN_FORBIDDEN", "request origin is not allowed")
		return
	}

	user, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	// Snapshot before the owner lookup. Admission later compares this exact
	// generation under the registry lock, closing the old-owner/unpair race.
	generation := handler.deviceGeneration(deviceID)
	if _, err := handler.devices.DeviceByOwner(request.Context(), user.ID, deviceID); errors.Is(err, store.ErrNotFound) {
		handler.writeError(writer, request, http.StatusNotFound, "DEVICE_NOT_FOUND", "device not found")
		return
	} else if err != nil {
		handler.internalError(writer, request, "device_lookup")
		return
	}
	if !handler.online.IsOnline(deviceID) {
		handler.writeError(writer, request, http.StatusConflict, "DEVICE_OFFLINE", "device is offline")
		return
	}
	if !handler.validateWebSocketHandshake(writer, request) {
		return
	}

	session, err := handler.admit(request.Context(), user.ID, deviceID, generation)
	if err != nil {
		switch {
		case errors.Is(err, errTerminalLimit):
			handler.writeError(writer, request, http.StatusTooManyRequests, "TERMINAL_LIMIT", "too many active terminals")
		case errors.Is(err, errDeviceInvalidated):
			handler.writeError(writer, request, http.StatusNotFound, "DEVICE_NOT_FOUND", "device not found")
		default:
			handler.writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "terminal service is unavailable")
		}
		return
	}
	defer handler.release(session)

	if cause := context.Cause(session.ctx); cause != nil {
		handler.writeCanceledAdmission(writer, request, cause)
		return
	}
	// The admitted session owns every phase from upgrade onward. In particular,
	// CancelDevice and Close must be able to interrupt and join an acceptor that
	// is still completing the upgrade rather than waiting on request.Context.
	request = request.WithContext(session.ctx)
	connection, err := handler.accept(writer, request)
	if err != nil {
		// All predictable handshake failures were rejected with the standard
		// envelope above. A remaining accept failure is an unrecoverable
		// transport/Hijack error and may already have committed bytes.
		handler.logClosed(request, user.ID, deviceID, "upgrade_failed")
		return
	}
	state.upgraded = true
	handler.serveTerminal(request, session, connection)
}

func terminalDeviceID(path string) (string, bool) {
	if !strings.HasPrefix(path, terminalPrefix) || !strings.HasSuffix(path, terminalSuffix) {
		return "", false
	}
	deviceID := strings.TrimSuffix(strings.TrimPrefix(path, terminalPrefix), terminalSuffix)
	if len(deviceID) < 5 || len(deviceID) > 128 || strings.Contains(deviceID, "/") {
		return "", false
	}
	return deviceID, true
}

func (handler *Handler) authenticate(writer http.ResponseWriter, request *http.Request) (store.User, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		handler.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return store.User{}, false
	}
	user, err := handler.auth.Authenticate(request.Context(), cookie.Value, handler.now().UTC())
	if errors.Is(err, auth.ErrInvalidCredentials) {
		handler.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return store.User{}, false
	}
	if err != nil {
		handler.internalError(writer, request, "authentication")
		return store.User{}, false
	}
	return user, true
}

func (handler *Handler) writeCanceledAdmission(writer http.ResponseWriter, request *http.Request, cause error) {
	if errors.Is(cause, errDeviceInvalidated) {
		handler.writeError(writer, request, http.StatusNotFound, "DEVICE_NOT_FOUND", "device not found")
		return
	}
	handler.writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "terminal service is unavailable")
}

func (handler *Handler) validateWebSocketHandshake(writer http.ResponseWriter, request *http.Request) bool {
	invalid := func(status int, code, message string) bool {
		handler.writeError(writer, request, status, code, message)
		return false
	}
	if !request.ProtoAtLeast(1, 1) {
		return invalid(http.StatusUpgradeRequired, "WEBSOCKET_UPGRADE_REQUIRED", "WebSocket upgrade required")
	}
	if !headerContainsToken(request.Header, "Connection", "upgrade") ||
		!headerContainsToken(request.Header, "Upgrade", "websocket") {
		return invalid(http.StatusUpgradeRequired, "WEBSOCKET_UPGRADE_REQUIRED", "WebSocket upgrade required")
	}
	versions := request.Header.Values("Sec-WebSocket-Version")
	if len(versions) != 1 || strings.TrimSpace(versions[0]) != "13" {
		return invalid(http.StatusBadRequest, "INVALID_WEBSOCKET_HANDSHAKE", "invalid WebSocket handshake")
	}
	keys := request.Header.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return invalid(http.StatusBadRequest, "INVALID_WEBSOCKET_HANDSHAKE", "invalid WebSocket handshake")
	}
	decodedKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	if err != nil || len(decodedKey) != 16 {
		return invalid(http.StatusBadRequest, "INVALID_WEBSOCKET_HANDSHAKE", "invalid WebSocket handshake")
	}
	origin, err := url.Parse(handler.allowedOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil ||
		origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") ||
		!strings.EqualFold(origin.Host, request.Host) {
		return invalid(http.StatusForbidden, "ORIGIN_FORBIDDEN", "request origin is not allowed")
	}
	if _, ok := unwrapHijacker(writer); !ok {
		return invalid(http.StatusNotImplemented, "WEBSOCKET_UNAVAILABLE", "WebSocket upgrade is unavailable")
	}
	return true
}

func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, candidate := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(candidate), token) {
				return true
			}
		}
	}
	return false
}

func (handler *Handler) internalError(writer http.ResponseWriter, request *http.Request, category string) {
	handler.logger.Error("terminal request failed", "request_id", terminalRequestID(request), "category", category)
	handler.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
}

func (handler *Handler) writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message, "request_id": terminalRequestID(request)},
	})
}

func terminalRequestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDKey{}).(string)
	return value
}

func (handler *Handler) logClosed(request *http.Request, userID, deviceID, category string) {
	handler.logger.Info("terminal session closed",
		"request_id", terminalRequestID(request), "user_id", userID,
		"device_id", deviceID, "category", category,
	)
}

type activeSession struct {
	userID      string
	deviceID    string
	generation  uint64
	ctx         context.Context
	cancel      context.CancelCauseFunc
	done        chan struct{}
	doneOnce    sync.Once
	afterFinish func()
}

func (session *activeSession) finish() {
	session.doneOnce.Do(func() {
		close(session.done)
		if session.afterFinish != nil {
			session.afterFinish()
		}
	})
}

func (handler *Handler) deviceGeneration(deviceID string) uint64 {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.generations[deviceID]
}

func (handler *Handler) admit(parent context.Context, userID, deviceID string, generation uint64) (*activeSession, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.closed {
		return nil, errHandlerClosed
	}
	if handler.generations[deviceID] != generation {
		return nil, errDeviceInvalidated
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if handler.userCounts[userID] >= maxTerminalsPerUser {
		return nil, errTerminalLimit
	}
	ctx, cancel := context.WithCancelCause(parent)
	session := &activeSession{
		userID: userID, deviceID: deviceID, generation: generation,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	handler.userCounts[userID]++
	if handler.byDevice[deviceID] == nil {
		handler.byDevice[deviceID] = make(map[*activeSession]struct{})
	}
	handler.byDevice[deviceID][session] = struct{}{}
	return session, nil
}

func (handler *Handler) release(session *activeSession) {
	handler.mu.Lock()
	// A lifecycle caller that observes this session as absent must also observe
	// its handler completion. Keep finish and registry removal in one critical
	// section, with finish first, so CancelDevice/Close cannot miss the join.
	session.finish()
	if sessions := handler.byDevice[session.deviceID]; sessions != nil {
		if _, exists := sessions[session]; exists {
			delete(sessions, session)
			handler.userCounts[session.userID]--
			if handler.userCounts[session.userID] == 0 {
				delete(handler.userCounts, session.userID)
			}
			if len(sessions) == 0 {
				delete(handler.byDevice, session.deviceID)
			}
		}
	}
	handler.mu.Unlock()
}

// CancelDevice advances the Device admission generation before canceling and
// joining every already-admitted pre-open or open session. A later legitimate
// pairing snapshots the new generation and is not part of this invalidation.
func (handler *Handler) CancelDevice(deviceID string) {
	handler.mu.Lock()
	handler.generations[deviceID]++
	sessions := copySessions(handler.byDevice[deviceID])
	handler.mu.Unlock()
	for _, session := range sessions {
		session.cancel(errDeviceInvalidated)
	}
	waitSessions(sessions)
}

// Close rejects new admission, cancels every Terminal and joins all handlers.
// Concurrent and repeated calls wait for the same completed shutdown.
func (handler *Handler) Close() {
	handler.closeOnce.Do(func() {
		go handler.closeAll()
	})
	<-handler.closeDone
}

func (handler *Handler) closeAll() {
	handler.mu.Lock()
	handler.closed = true
	sessions := make([]*activeSession, 0)
	for _, byDevice := range handler.byDevice {
		sessions = append(sessions, copySessions(byDevice)...)
	}
	handler.mu.Unlock()
	for _, session := range sessions {
		session.cancel(errHandlerClosed)
	}
	waitSessions(sessions)
	close(handler.closeDone)
}

func copySessions(values map[*activeSession]struct{}) []*activeSession {
	result := make([]*activeSession, 0, len(values))
	for session := range values {
		result = append(result, session)
	}
	return result
}

func waitSessions(sessions []*activeSession) {
	for _, session := range sessions {
		<-session.done
	}
}

type websocketConnection interface {
	SetReadLimit(int64)
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
	Close(websocket.StatusCode, string) error
	ForceClose() error
}

type acceptWebSocketFunc func(http.ResponseWriter, *http.Request) (websocketConnection, error)

var websocketAccept = acceptWebSocket

func acceptWebSocket(writer http.ResponseWriter, request *http.Request) (websocketConnection, error) {
	capture := &hijackCaptureWriter{ResponseWriter: writer}
	connection, err := websocket.Accept(capture, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if capture.raw != nil {
			_ = capture.raw.Close()
		}
		return nil, err
	}
	if capture.raw == nil {
		_ = connection.CloseNow()
		return nil, errors.New("websocket upgrade did not expose its network connection")
	}
	return &networkWebSocket{Conn: connection, raw: capture.raw}, nil
}

type responseWriterUnwrapper interface {
	Unwrap() http.ResponseWriter
}

// unwrapHijacker mirrors net/http's ResponseController unwrapping convention
// without performing the destructive Hijack during preflight.
func unwrapHijacker(writer http.ResponseWriter) (http.Hijacker, bool) {
	for depth := 0; depth < 32 && writer != nil; depth++ {
		if hijacker, ok := writer.(http.Hijacker); ok {
			return hijacker, true
		}
		unwrapper, ok := writer.(responseWriterUnwrapper)
		if !ok {
			return nil, false
		}
		next := unwrapper.Unwrap()
		if next == nil {
			return nil, false
		}
		writer = next
	}
	return nil, false
}

type hijackCaptureWriter struct {
	http.ResponseWriter
	raw net.Conn
}

func (writer *hijackCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := unwrapHijacker(writer.ResponseWriter)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	raw, buffered, err := hijacker.Hijack()
	if err == nil {
		writer.raw = raw
	}
	return raw, buffered, err
}

type networkWebSocket struct {
	*websocket.Conn
	raw net.Conn
}

func (connection *networkWebSocket) ForceClose() error {
	if connection.raw == nil {
		return errors.New("websocket network connection is unavailable")
	}
	return connection.raw.Close()
}
