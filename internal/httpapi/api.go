// Package httpapi implements the versioned browser JSON API.
package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/device"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/requestsource"
	"github.com/aisummoner/aisummoner/internal/store"
)

const (
	SessionCookieName      = "aisummoner_session"
	maxJSONBodyBytes       = 16 * 1024
	failureLimiterCapacity = 4096
)

type Auditor interface {
	CreateAuditEvent(context.Context, store.AuditEvent) error
}

type AuthService interface {
	Login(context.Context, string, string, time.Time) (auth.LoginResult, error)
	Authenticate(context.Context, string, time.Time) (store.User, error)
	Logout(context.Context, string) error
}

type SourceResolver interface {
	Resolve(*http.Request) (string, error)
}

type Options struct {
	Auth           AuthService
	Pairing        *pairing.Service
	Devices        *device.Service
	Auditor        Auditor
	AllowedOrigin  string
	CookieSecure   bool
	Logger         *slog.Logger
	Now            func() time.Time
	SourceResolver SourceResolver
}

type API struct {
	auth           AuthService
	pairing        *pairing.Service
	devices        *device.Service
	auditor        Auditor
	allowedOrigin  string
	cookieSecure   bool
	logger         *slog.Logger
	now            func() time.Time
	sourceResolver SourceResolver
	loginLimiter   *failureLimiter
	claimLimiter   *failureLimiter
}

func New(options Options) (*API, error) {
	if options.Auth == nil || options.Pairing == nil || options.Devices == nil {
		return nil, errors.New("auth, pairing, and device services are required")
	}
	if options.AllowedOrigin == "" {
		return nil, errors.New("allowed origin is required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.SourceResolver == nil {
		options.SourceResolver = requestsource.New(nil)
	}
	return &API{
		auth: options.Auth, pairing: options.Pairing, devices: options.Devices,
		auditor: options.Auditor, allowedOrigin: options.AllowedOrigin,
		cookieSecure: options.CookieSecure, logger: options.Logger, now: options.Now,
		sourceResolver: options.SourceResolver,
		loginLimiter:   newFailureLimiter(5, time.Minute),
		claimLimiter:   newFailureLimiter(5, time.Minute),
	}, nil
}

// Handler returns the complete middleware stack. It is safe for concurrent use.
func (a *API) Handler() http.Handler {
	return a.withMiddleware(http.HandlerFunc(a.route))
}

func (a *API) withMiddleware(next http.Handler) http.Handler {
	return a.withRequestID(a.withRecovery(a.withLogging(a.withSameOrigin(next))))
}

func (a *API) route(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	switch {
	case path == "/healthz":
		if request.Method != http.MethodGet {
			a.writeMethodNotAllowed(writer, request, http.MethodGet)
			return
		}
		a.writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	case path == "/api/v1/auth/login":
		if request.Method != http.MethodPost {
			a.writeMethodNotAllowed(writer, request, http.MethodPost)
			return
		}
		a.handleLogin(writer, request)
	case path == "/api/v1/auth/logout":
		if request.Method != http.MethodPost {
			a.writeMethodNotAllowed(writer, request, http.MethodPost)
			return
		}
		a.handleLogout(writer, request)
	case path == "/api/v1/me":
		if request.Method != http.MethodGet {
			a.writeMethodNotAllowed(writer, request, http.MethodGet)
			return
		}
		a.handleMe(writer, request)
	case path == "/api/v1/pairings/claim":
		if request.Method != http.MethodPost {
			a.writeMethodNotAllowed(writer, request, http.MethodPost)
			return
		}
		a.handlePairingClaim(writer, request)
	case path == "/api/v1/devices":
		if request.Method != http.MethodGet {
			a.writeMethodNotAllowed(writer, request, http.MethodGet)
			return
		}
		a.handleDevices(writer, request)
	case strings.HasPrefix(path, "/api/v1/devices/") && strings.Count(strings.TrimPrefix(path, "/api/v1/devices/"), "/") == 0:
		deviceID := strings.TrimPrefix(path, "/api/v1/devices/")
		if deviceID == "" || len(deviceID) > 128 {
			a.writeError(writer, request, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		switch request.Method {
		case http.MethodGet:
			a.handleDevice(writer, request, deviceID)
		case http.MethodDelete:
			a.handleDeviceDelete(writer, request, deviceID)
		default:
			a.writeMethodNotAllowed(writer, request, http.MethodGet+", "+http.MethodDelete)
		}
	default:
		a.writeError(writer, request, http.StatusNotFound, "NOT_FOUND", "resource not found")
	}
}

func (a *API) writeMethodNotAllowed(writer http.ResponseWriter, request *http.Request, allowed string) {
	writer.Header().Set("Allow", allowed)
	a.writeError(writer, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}

type requestIDContextKey struct{}
type userContextKey struct{}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey{}).(string)
	return value
}

func userFromContext(request *http.Request) (store.User, bool) {
	user, ok := request.Context().Value(userContextKey{}).(store.User)
	return user, ok
}

func (a *API) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := id.MustNew("req")
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (a *API) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("http request panic", "request_id", requestID(request), "method", request.Method, "path", request.URL.Path)
				a.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(contents []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(contents)
}

// Unwrap lets http.ResponseController discover capabilities added by the
// production server without forcing every middleware to know about them.
func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// FlushError delegates through ResponseController so SSE receives the real
// capability error when the underlying writer cannot flush.
func (writer *statusWriter) FlushError() error {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

// Flush preserves the conventional http.Flusher contract used by SSE code.
func (writer *statusWriter) Flush() {
	_ = writer.FlushError()
}

// Hijack preserves the connection-upgrade contract used by coder/websocket.
func (writer *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(writer.ResponseWriter).Hijack()
}

func (a *API) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := a.now()
		wrapped := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(wrapped, request)
		a.logger.Info("http request",
			"request_id", requestID(request), "method", request.Method, "path", request.URL.Path,
			"status", wrapped.status, "duration_ms", a.now().Sub(started).Milliseconds())
	})
}

func (a *API) withSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut ||
			request.Method == http.MethodPatch || request.Method == http.MethodDelete {
			origins := request.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != a.allowedOrigin {
				a.writeError(writer, request, http.StatusForbidden, "ORIGIN_FORBIDDEN", "request origin is not allowed")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (a *API) authenticate(writer http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		a.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return request, false
	}
	user, err := a.auth.Authenticate(request.Context(), cookie.Value, a.now())
	if errors.Is(err, auth.ErrInvalidCredentials) {
		a.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return request, false
	}
	if err != nil {
		a.internalError(writer, request, err)
		return request, false
	}
	ctx := context.WithValue(request.Context(), userContextKey{}, user)
	return request.WithContext(ctx), true
}

type failureWindow struct {
	started      time.Time
	lastSeen     time.Time
	lastObserved uint64
	count        int
}

type failureLimiter struct {
	mu           sync.Mutex
	entries      map[string]failureWindow
	maximum      int
	duration     time.Duration
	capacity     int
	observations uint64
}

func newFailureLimiter(maximum int, duration time.Duration) *failureLimiter {
	return &failureLimiter{
		entries: make(map[string]failureWindow), maximum: maximum, duration: duration,
		capacity: failureLimiterCapacity,
	}
}

func (limiter *failureLimiter) allow(key string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.removeExpired(now)
	entry, ok := limiter.entries[key]
	if !ok {
		return true
	}
	limiter.observe(&entry, now)
	limiter.entries[key] = entry
	return entry.count < limiter.maximum
}

func (limiter *failureLimiter) failed(key string, now time.Time) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.removeExpired(now)
	entry, ok := limiter.entries[key]
	if !ok {
		if len(limiter.entries) >= limiter.capacity {
			limiter.evictLeastRecentlyObserved()
		}
		entry.started = now
	}
	entry.count++
	limiter.observe(&entry, now)
	limiter.entries[key] = entry
}

func (limiter *failureLimiter) succeeded(key string) {
	limiter.mu.Lock()
	delete(limiter.entries, key)
	limiter.mu.Unlock()
}

func (limiter *failureLimiter) observe(entry *failureWindow, now time.Time) {
	limiter.observations++
	entry.lastSeen = now
	entry.lastObserved = limiter.observations
}

func (limiter *failureLimiter) removeExpired(now time.Time) {
	for key, entry := range limiter.entries {
		if now.Sub(entry.started) >= limiter.duration || now.Sub(entry.lastSeen) >= limiter.duration {
			delete(limiter.entries, key)
		}
	}
}

func (limiter *failureLimiter) evictLeastRecentlyObserved() {
	var oldestKey string
	var oldestObservation uint64
	first := true
	for key, entry := range limiter.entries {
		if first || entry.lastObserved < oldestObservation {
			oldestKey = key
			oldestObservation = entry.lastObserved
			first = false
		}
	}
	if !first {
		delete(limiter.entries, oldestKey)
	}
}

func (a *API) internalError(writer http.ResponseWriter, request *http.Request, err error) {
	a.logger.Error("http request failed", "request_id", requestID(request), "error", err)
	a.writeError(writer, request, http.StatusInternalServerError, "INTERNAL", "internal server error")
}

func (a *API) writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	a.writeJSONStatus(writer, status, map[string]any{
		"error": map[string]string{"code": code, "message": message, "request_id": requestID(request)},
	})
}

func (a *API) writeJSON(writer http.ResponseWriter, status int, value any) {
	a.writeJSONStatus(writer, status, value)
}

func (a *API) writeJSONStatus(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		a.logger.Error("encode http response", "error", err)
	}
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	if contentType := request.Header.Get("Content-Type"); contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return errors.New("content type must be application/json")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("request body must contain one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}
