// Package opencodebridge exposes the loopback-only callback used by the
// reviewed OpenCode remote_exec tool. A callback can reach only the
// RemoteExecInvoker bound to its active external session.
package opencodebridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const (
	ProviderName = "opencode"

	CallbackPath = "/internal/opencode/remote-exec"

	EnvBridgeURL    = "AISUMMONER_OPENCODE_BRIDGE_URL"
	EnvBridgeSecret = "AISUMMONER_AGENT_BRIDGE_SECRET"

	HeaderTimestamp = "X-AISummoner-Timestamp"
	Authorization   = "AISummoner-HMAC"

	MaxRequestBytes  = 80 * 1024
	MaxResponseBytes = 2 * 1024 * 1024
)

const (
	proofDomain           = "AISummoner.OpenCodeBridge.v1"
	maximumClockSkew      = 2 * time.Minute
	maximumCallbackTime   = 185 * time.Second
	defaultBodyReadTime   = 5 * time.Second
	defaultResponseTime   = 5 * time.Second
	maximumIOTimeout      = 30 * time.Second
	minimumBridgeSecret   = 32
	maximumExternalIDSize = 512
)

var (
	errBridgeClosed      = errors.New("opencode bridge is closed")
	errDuplicateMapping  = errors.New("external session already active")
	errInactiveMapping   = errors.New("external session is not active")
	errResponseTooLarge  = errors.New("opencode bridge response exceeds limit")
	errResponseEncoding  = errors.New("opencode bridge response encoding failed")
	errResponseDelivery  = errors.New("opencode bridge response delivery failed")
	errInvalidActivation = errors.New("invalid opencode bridge activation")
)

// Options contains only process-local bridge dependencies. Task008 owns the
// listener and must mount Handler on a separate loopback-only server.
type Options struct {
	Secret          []byte
	Now             func() time.Time
	Logger          *slog.Logger
	BodyReadTimeout time.Duration
	ResponseTimeout time.Duration
}

// Activation is one Adapter Turn's external-session capability. Close is
// idempotent and joins every executing or queued callback.
type Activation interface {
	Failures() <-chan error
	Close()
}

// Activator is the narrow boundary consumed by the OpenCode Adapter.
type Activator interface {
	Activate(context.Context, string, string, agent.RemoteExecInvoker) (Activation, error)
}

// Bridge owns the active external-session capability registry and callback
// handler. It never selects a device from callback input.
type Bridge struct {
	secret          []byte
	now             func() time.Time
	logger          *slog.Logger
	bodyReadTimeout time.Duration
	responseTimeout time.Duration

	mu        sync.Mutex
	mappings  map[string]*mapping
	all       map[*mapping]struct{}
	closed    bool
	closeDone chan struct{}
}

type mapping struct {
	bridge            *Bridge
	productSessionID  string
	externalSessionID string
	invoker           agent.RemoteExecInvoker

	ctx    context.Context
	cancel context.CancelFunc
	gate   chan struct{}
	fatal  chan error

	mu        sync.Mutex
	active    bool
	inflight  sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	fatalOnce sync.Once
}

type activation struct{ mapping *mapping }

type callbackRequest struct {
	SessionID      string          `json:"session_id"`
	Command        string          `json:"command"`
	CWD            string          `json:"cwd,omitempty"`
	TimeoutSeconds json.RawMessage `json:"timeout_seconds,omitempty"`
}

// New validates and constructs a bridge. Secret bytes are copied so later
// caller mutation cannot change authentication behavior.
func New(options Options) (*Bridge, error) {
	if len(options.Secret) < minimumBridgeSecret {
		return nil, errors.New("opencode bridge secret must contain at least 32 bytes")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.BodyReadTimeout == 0 {
		options.BodyReadTimeout = defaultBodyReadTime
	}
	if options.ResponseTimeout == 0 {
		options.ResponseTimeout = defaultResponseTime
	}
	if options.BodyReadTimeout < 0 || options.BodyReadTimeout > maximumIOTimeout ||
		options.ResponseTimeout < 0 || options.ResponseTimeout > maximumIOTimeout {
		return nil, errors.New("opencode bridge I/O timeout is outside the safe range")
	}
	return &Bridge{
		secret:          append([]byte(nil), options.Secret...),
		now:             options.Now,
		logger:          options.Logger,
		bodyReadTimeout: options.BodyReadTimeout,
		responseTimeout: options.ResponseTimeout,
		mappings:        make(map[string]*mapping),
		all:             make(map[*mapping]struct{}),
		closeDone:       make(chan struct{}),
	}, nil
}

// Handler returns the bridge's standalone handler. Task008 must not mount it
// on the public browser listener.
func (bridge *Bridge) Handler() http.Handler {
	return http.HandlerFunc(bridge.serveHTTP)
}

// Activate binds one external OpenCode session to an already owner/device
// scoped invoker. Duplicate active external IDs fail closed.
func (bridge *Bridge) Activate(turnCtx context.Context, productSessionID, externalSessionID string, invoker agent.RemoteExecInvoker) (Activation, error) {
	if turnCtx == nil || invoker == nil || productSessionID == "" || !validExternalSessionID(externalSessionID) {
		return nil, errInvalidActivation
	}
	ctx, cancel := context.WithCancel(turnCtx)
	entry := &mapping{
		bridge: bridge, productSessionID: productSessionID, externalSessionID: externalSessionID, invoker: invoker,
		ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), fatal: make(chan error, 1), active: true, closeDone: make(chan struct{}),
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.closed {
		cancel()
		return nil, errBridgeClosed
	}
	if _, exists := bridge.mappings[externalSessionID]; exists {
		cancel()
		return nil, errDuplicateMapping
	}
	bridge.mappings[externalSessionID] = entry
	bridge.all[entry] = struct{}{}
	return &activation{mapping: entry}, nil
}

// ActiveCount is an operational/test metric. It exposes no session contents.
func (bridge *Bridge) ActiveCount() int {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return len(bridge.mappings)
}

// Close rejects new activations, cancels every current mapping, and waits for
// all callback handlers up to ctx. Cleanup continues if the caller times out.
func (bridge *Bridge) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	bridge.mu.Lock()
	if !bridge.closed {
		bridge.closed = true
		entries := make([]*mapping, 0, len(bridge.all))
		for entry := range bridge.all {
			entries = append(entries, entry)
		}
		go bridge.finishClose(entries)
	}
	done := bridge.closeDone
	bridge.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (bridge *Bridge) finishClose(entries []*mapping) {
	for _, entry := range entries {
		entry.startClose()
	}
	for _, entry := range entries {
		<-entry.closeDone
	}
	close(bridge.closeDone)
}

func (lease *activation) Failures() <-chan error { return lease.mapping.fatal }

func (lease *activation) Close() {
	lease.mapping.startClose()
	<-lease.mapping.closeDone
}

func (entry *mapping) startClose() {
	entry.closeOnce.Do(func() {
		entry.mu.Lock()
		entry.active = false
		entry.cancel()
		entry.mu.Unlock()
		entry.bridge.remove(entry)
		go func() {
			entry.inflight.Wait()
			close(entry.closeDone)
			entry.bridge.finished(entry)
		}()
	})
}

func (bridge *Bridge) finished(entry *mapping) {
	bridge.mu.Lock()
	delete(bridge.all, entry)
	bridge.mu.Unlock()
}

func (bridge *Bridge) remove(entry *mapping) {
	bridge.mu.Lock()
	if current := bridge.mappings[entry.externalSessionID]; current == entry {
		delete(bridge.mappings, entry.externalSessionID)
	}
	bridge.mu.Unlock()
}

func (entry *mapping) beginCall() bool {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.active || entry.ctx.Err() != nil {
		return false
	}
	entry.inflight.Add(1)
	return true
}

func (entry *mapping) reportFatal(err error) {
	if err == nil {
		return
	}
	entry.fatalOnce.Do(func() {
		select {
		case entry.fatal <- err:
		default:
		}
		// Fatal callbacks become inactive immediately. startClose only marks,
		// cancels, removes, and starts an asynchronous waiter; it never waits on
		// this handler's own in-flight slot. The Adapter later calls Close to join.
		entry.startClose()
	})
}

func (bridge *Bridge) lookup(externalSessionID string) *mapping {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.closed {
		return nil
	}
	return bridge.mappings[externalSessionID]
}

func (bridge *Bridge) serveHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != CallbackPath {
		bridge.reject(response, http.StatusNotFound)
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		bridge.reject(response, http.StatusMethodNotAllowed)
		return
	}
	if !loopbackRemote(request.RemoteAddr) {
		bridge.reject(response, http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		bridge.reject(response, http.StatusUnsupportedMediaType)
		return
	}
	controller := http.NewResponseController(response)
	if err := controller.SetReadDeadline(time.Now().Add(bridge.bodyReadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, MaxRequestBytes+1))
	if err != nil {
		bridge.reject(response, http.StatusBadRequest)
		return
	}
	if len(body) > MaxRequestBytes {
		bridge.reject(response, http.StatusRequestEntityTooLarge)
		return
	}
	var callback callbackRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&callback); err != nil {
		bridge.reject(response, http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		bridge.reject(response, http.StatusBadRequest)
		return
	}
	if !validExternalSessionID(callback.SessionID) || callback.Command == "" {
		bridge.reject(response, http.StatusBadRequest)
		return
	}
	timeoutSeconds := int(agent.DefaultExecTimeout / time.Second)
	if len(callback.TimeoutSeconds) > 0 {
		encodedTimeout := bytes.TrimSpace(callback.TimeoutSeconds)
		if bytes.Equal(encodedTimeout, []byte("null")) {
			bridge.reject(response, http.StatusBadRequest)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(encodedTimeout))
		if err := decoder.Decode(&timeoutSeconds); err != nil {
			bridge.reject(response, http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
			bridge.reject(response, http.StatusBadRequest)
			return
		}
	}
	if timeoutSeconds < int(agent.MinimumExecTimeout/time.Second) || timeoutSeconds > int(agent.MaximumExecTimeout/time.Second) {
		bridge.reject(response, http.StatusBadRequest)
		return
	}
	if !bridge.validProof(request, callback.SessionID) {
		bridge.reject(response, http.StatusUnauthorized)
		return
	}
	entry := bridge.lookup(callback.SessionID)
	if entry == nil || !entry.beginCall() {
		bridge.reject(response, http.StatusNotFound)
		return
	}
	defer entry.inflight.Done()

	callCtx, callCancel := context.WithTimeout(entry.ctx, maximumCallbackTime)
	stopRequestCancel := context.AfterFunc(request.Context(), callCancel)
	defer func() {
		stopRequestCancel()
		callCancel()
	}()
	select {
	case <-callCtx.Done():
		bridge.reject(response, http.StatusRequestTimeout)
		return
	case entry.gate <- struct{}{}:
	}
	defer func() { <-entry.gate }()
	if err := callCtx.Err(); err != nil {
		bridge.reject(response, http.StatusRequestTimeout)
		return
	}

	arguments, err := json.Marshal(agent.RemoteExecArguments{
		Command:   callback.Command,
		CWD:       callback.CWD,
		TimeoutMS: timeoutSeconds * int(time.Second/time.Millisecond),
	})
	if err != nil {
		fatal := &agent.AdapterError{Code: "protocol_error", Err: errResponseEncoding}
		entry.reportFatal(fatal)
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	result, err := entry.invoker.Invoke(callCtx, agent.ToolRequest{Name: agent.ToolRemoteExec, Arguments: arguments})
	if err != nil {
		// Reporting is non-blocking. It makes the mapping inactive and starts an
		// asynchronous join; the Adapter owns the final Activation.Close wait.
		entry.reportFatal(err)
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fatal := &agent.AdapterError{Code: "protocol_error", Err: errResponseEncoding}
		entry.reportFatal(fatal)
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	if len(encoded) > MaxResponseBytes {
		fatal := &agent.AdapterError{Code: "protocol_error", Err: errResponseTooLarge}
		entry.reportFatal(fatal)
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	if err := controller.SetWriteDeadline(time.Now().Add(bridge.responseTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		entry.reportFatal(&agent.AdapterError{Code: "protocol_error", Err: errResponseDelivery})
		bridge.reject(response, http.StatusInternalServerError)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	response.WriteHeader(http.StatusOK)
	if written, err := io.Copy(response, bytes.NewReader(encoded)); err != nil || written != int64(len(encoded)) {
		entry.reportFatal(&agent.AdapterError{Code: "protocol_error", Err: errResponseDelivery})
	}
}

func (bridge *Bridge) validProof(request *http.Request, externalSessionID string) bool {
	timestampText := request.Header.Get(HeaderTimestamp)
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || strconv.FormatInt(timestamp, 10) != timestampText {
		return false
	}
	now := bridge.now().Unix()
	maximumSkew := int64(maximumClockSkew / time.Second)
	if timestamp < now-maximumSkew || timestamp > now+maximumSkew {
		return false
	}
	authorization := request.Header.Get("Authorization")
	prefix := Authorization + " "
	if !strings.HasPrefix(authorization, prefix) || strings.Contains(authorization[len(prefix):], " ") {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(authorization, prefix))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	expected := signature(bridge.secret, externalSessionID, timestampText)
	return hmac.Equal(provided, expected)
}

func signature(secret []byte, externalSessionID, timestamp string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(proofDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(externalSessionID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(timestamp))
	return mac.Sum(nil)
}

func loopbackRemote(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validExternalSessionID(value string) bool {
	if len(value) <= len("ses_") || len(value) > maximumExternalIDSize || !strings.HasPrefix(value, "ses_") {
		return false
	}
	for _, character := range value[len("ses_"):] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func (bridge *Bridge) reject(response http.ResponseWriter, status int) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write([]byte(`{"error":"request rejected"}`))
}
