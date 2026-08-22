package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
)

const defaultHealthTimeout = time.Second

// SQLiteProbe is intentionally the already-approved Store.HasUsers surface.
// Health needs a bounded successful SQLite query, not a model/provider probe.
type SQLiteProbe interface {
	HasUsers(context.Context) (bool, error)
}

type Health struct {
	readiness *Readiness
	probe     SQLiteProbe
	timeout   time.Duration
}

func NewHealth(readiness *Readiness, probe SQLiteProbe, timeout time.Duration) (*Health, error) {
	if readiness == nil || probe == nil {
		return nil, errors.New("health readiness and SQLite probe are required")
	}
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = defaultHealthTimeout
	}
	return &Health{readiness: readiness, probe: probe, timeout: timeout}, nil
}

func (health *Health) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID, err := id.New("req")
	if err != nil {
		requestID = "req_unavailable"
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Request-ID", requestID)
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeHealthError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", requestID)
		return
	}
	if !health.readiness.IsReady() {
		writeHealthError(writer, http.StatusServiceUnavailable, "NOT_READY", "server is not ready", requestID)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), health.timeout)
	defer cancel()
	if _, err := health.probe.HasUsers(ctx); err != nil {
		writeHealthError(writer, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "server is not ready", requestID)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
}

func writeHealthError(writer http.ResponseWriter, status int, code, message, requestID string) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
		"code": code, "message": message, "request_id": requestID,
	}})
}
