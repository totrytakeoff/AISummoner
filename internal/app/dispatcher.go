// Package app owns only process composition boundaries. Domain state machines
// remain in their independently reviewed packages.
package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aisummoner/aisummoner/internal/id"
)

type DispatcherOptions struct {
	Readiness *Readiness
	Tunnel    http.Handler
	Terminal  http.Handler
	Agent     http.Handler
	Health    http.Handler
	Browser   http.Handler
	Static    http.Handler
}

// Dispatcher performs exact path-shape selection and then calls the selected
// handler with the original ResponseWriter. In particular, it never wraps or
// records the writer, preserving Hijacker, Flusher and ResponseController.
type Dispatcher struct {
	readiness *Readiness
	tunnel    http.Handler
	terminal  http.Handler
	agent     http.Handler
	health    http.Handler
	browser   http.Handler
	static    http.Handler
	notFound  http.Handler
}

func NewDispatcher(options DispatcherOptions) (*Dispatcher, error) {
	if options.Readiness == nil || options.Tunnel == nil || options.Terminal == nil ||
		options.Agent == nil || options.Health == nil || options.Browser == nil || options.Static == nil {
		return nil, errors.New("all public dispatcher handlers and readiness are required")
	}
	return &Dispatcher{
		readiness: options.Readiness,
		tunnel:    options.Tunnel, terminal: options.Terminal, agent: options.Agent,
		health: options.Health, browser: options.Browser, static: options.Static,
		notFound: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeNotFound(writer)
		}),
	}, nil
}

func (dispatcher *Dispatcher) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	kind := classifyPath(request.URL.Path)
	if kind != routeHealth && !dispatcher.readiness.IsReady() {
		writeUnavailable(writer)
		return
	}
	switch kind {
	case routeTunnel:
		dispatcher.tunnel.ServeHTTP(writer, request)
	case routeTerminal:
		dispatcher.terminal.ServeHTTP(writer, request)
	case routeAgent:
		dispatcher.agent.ServeHTTP(writer, request)
	case routeHealth:
		dispatcher.health.ServeHTTP(writer, request)
	case routeBrowser:
		dispatcher.browser.ServeHTTP(writer, request)
	case routeDenied:
		dispatcher.notFound.ServeHTTP(writer, request)
	default:
		dispatcher.static.ServeHTTP(writer, request)
	}
}

type routeKind uint8

const (
	routeStatic routeKind = iota
	routeTunnel
	routeTerminal
	routeAgent
	routeHealth
	routeBrowser
	routeDenied
)

func classifyPath(value string) routeKind {
	switch {
	case value == "/api/v1/tunnel":
		return routeTunnel
	case oneSegmentBetween(value, "/api/v1/devices/", "/terminal"):
		return routeTerminal
	case oneSegmentBetween(value, "/api/v1/devices/", "/agent-sessions"):
		return routeAgent
	case value == "/api/v1/agent-provider/deepseek":
		return routeAgent
	case agentSessionPath(value), oneSegmentBetween(value, "/api/v1/tool-calls/", "/decision"):
		return routeAgent
	case value == "/healthz":
		return routeHealth
	case strings.HasPrefix(value, "/healthz/"):
		return routeDenied
	case value == "/api" || strings.HasPrefix(value, "/api/"):
		return routeBrowser
	case value == "/internal" || strings.HasPrefix(value, "/internal/"):
		return routeDenied
	default:
		return routeStatic
	}
}

func agentSessionPath(value string) bool {
	const prefix = "/api/v1/agent-sessions/"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(value, prefix)
	if remainder == "" {
		return false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) == 1 {
		return segments[0] != ""
	}
	return len(segments) == 2 && segments[0] != "" && (segments[1] == "messages" || segments[1] == "events")
}

func oneSegmentBetween(value, prefix, suffix string) bool {
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	return middle != "" && !strings.Contains(middle, "/")
}

func writeUnavailable(writer http.ResponseWriter) {
	requestID, err := id.New("req")
	if err != nil {
		requestID = "req_unavailable"
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Request-ID", requestID)
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
		"code": "SERVER_UNAVAILABLE", "message": "server is unavailable", "request_id": requestID,
	}})
}

func writeNotFound(writer http.ResponseWriter) {
	requestID, err := id.New("req")
	if err != nil {
		requestID = "req_unavailable"
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Request-ID", requestID)
	writer.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{
		"code": "NOT_FOUND", "message": "resource not found", "request_id": requestID,
	}})
}
