package opencodebridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
)

const testExternalSession = "ses_bridge_test"

type recordingInvoker struct {
	mu        sync.Mutex
	calls     []agent.ToolRequest
	active    int
	maxActive int
	started   chan struct{}
	wait      <-chan struct{}
	result    agent.ToolResult
	err       error
}

type controlledResponseWriter struct {
	header        http.Header
	status        int
	readDeadline  error
	writeDeadline error
	writeFailure  error
}

func (writer *controlledResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *controlledResponseWriter) WriteHeader(status int) { writer.status = status }

func (writer *controlledResponseWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	if writer.writeFailure != nil {
		return 0, writer.writeFailure
	}
	return len(value), nil
}

func (writer *controlledResponseWriter) SetReadDeadline(time.Time) error {
	return writer.readDeadline
}

func (writer *controlledResponseWriter) SetWriteDeadline(time.Time) error {
	return writer.writeDeadline
}

func (invoker *recordingInvoker) Invoke(ctx context.Context, request agent.ToolRequest) (agent.ToolResult, error) {
	invoker.mu.Lock()
	invoker.calls = append(invoker.calls, request)
	invoker.active++
	if invoker.active > invoker.maxActive {
		invoker.maxActive = invoker.active
	}
	started := invoker.started
	result := invoker.result
	err := invoker.err
	invoker.mu.Unlock()
	defer func() {
		invoker.mu.Lock()
		invoker.active--
		invoker.mu.Unlock()
	}()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if invoker.wait != nil {
		select {
		case <-ctx.Done():
			return agent.ToolResult{}, ctx.Err()
		case <-invoker.wait:
		}
	}
	return result, err
}

func (invoker *recordingInvoker) snapshot() (calls []agent.ToolRequest, active, maximum int) {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return append([]agent.ToolRequest(nil), invoker.calls...), invoker.active, invoker.maxActive
}

func newTestBridge(t *testing.T) (*Bridge, []byte, time.Time) {
	t.Helper()
	secret := []byte(strings.Repeat("b", 32))
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	bridge, err := New(Options{
		Secret: secret,
		Now:    func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.Close(ctx); err != nil {
			t.Errorf("close bridge: %v", err)
		}
	})
	return bridge, secret, now
}

func callbackBody(t *testing.T, sessionID, command, cwd string, timeoutSeconds any) []byte {
	t.Helper()
	body := map[string]any{"session_id": sessionID, "command": command}
	if cwd != "" {
		body["cwd"] = cwd
	}
	if timeoutSeconds != nil {
		body["timeout_seconds"] = timeoutSeconds
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func signedRequest(t *testing.T, secret []byte, now time.Time, body []byte, proofSession string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://bridge.invalid"+CallbackPath, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:43120"
	request.Header.Set("Content-Type", "application/json")
	timestamp := strconv.FormatInt(now.Unix(), 10)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(signature(secret, proofSession, timestamp)))
	return request
}

func formatUnix(value int64) string {
	return strconv.FormatInt(value, 10)
}

func perform(bridge *Bridge, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	bridge.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestBridgeValidCallbackAndSecurityRejections(t *testing.T) {
	bridge, secret, now := newTestBridge(t)
	invoker := &recordingInvoker{result: agent.ToolResult{Stdout: "lzr-host\n", ExitCode: 0}}
	lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := bridge.Activate(context.Background(), "ags_other", testExternalSession, invoker); !errors.Is(err, errDuplicateMapping) {
		t.Fatalf("duplicate activation result=%v", err)
	}

	validBody := callbackBody(t, testExternalSession, "hostname", "/tmp", 30)
	valid := signedRequest(t, secret, now, validBody, testExternalSession)
	response := perform(bridge, valid)
	if response.Code != http.StatusOK {
		t.Fatalf("valid callback status=%d body=%s", response.Code, response.Body.String())
	}
	var result agent.ToolResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Stdout != "lzr-host\n" {
		t.Fatalf("valid result=%#v err=%v", result, err)
	}
	calls, _, _ := invoker.snapshot()
	if len(calls) != 1 || calls[0].Name != agent.ToolRemoteExec {
		t.Fatalf("invoker calls=%#v", calls)
	}
	var arguments agent.RemoteExecArguments
	if err := json.Unmarshal(calls[0].Arguments, &arguments); err != nil || arguments.Command != "hostname" || arguments.CWD != "/tmp" || arguments.TimeoutMS != 30000 {
		t.Fatalf("forwarded arguments=%#v err=%v", arguments, err)
	}

	tests := []struct {
		name   string
		mutate func(*http.Request)
		body   []byte
		proof  string
		status int
	}{
		{name: "non loopback", mutate: func(request *http.Request) { request.RemoteAddr = "192.0.2.10:44" }, status: http.StatusForbidden},
		{name: "wrong method", mutate: func(request *http.Request) { request.Method = http.MethodGet }, status: http.StatusMethodNotAllowed},
		{name: "missing proof", mutate: func(request *http.Request) { request.Header.Del("Authorization") }, status: http.StatusUnauthorized},
		{name: "wrong session proof", proof: "ses_wrong", status: http.StatusUnauthorized},
		{name: "expired", mutate: func(request *http.Request) {
			timestamp := formatUnix(now.Add(-maximumClockSkew - time.Second).Unix())
			request.Header.Set(HeaderTimestamp, timestamp)
			request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(signature(secret, testExternalSession, timestamp)))
		}, status: http.StatusUnauthorized},
		{name: "future", mutate: func(request *http.Request) {
			timestamp := formatUnix(now.Add(maximumClockSkew + time.Second).Unix())
			request.Header.Set(HeaderTimestamp, timestamp)
			request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(signature(secret, testExternalSession, timestamp)))
		}, status: http.StatusUnauthorized},
		{name: "noncanonical timestamp", mutate: func(request *http.Request) {
			timestamp := "0" + formatUnix(now.Unix())
			request.Header.Set(HeaderTimestamp, timestamp)
			request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(signature(secret, testExternalSession, timestamp)))
		}, status: http.StatusUnauthorized},
		{name: "wrong mac length", mutate: func(request *http.Request) {
			request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(make([]byte, 31)))
		}, status: http.StatusUnauthorized},
		{name: "unknown field", body: append(validBody[:len(validBody)-1], []byte(`,"device_id":"dev_forbidden"}`)...), status: http.StatusBadRequest},
		{name: "inactive session", body: callbackBody(t, "ses_absent", "hostname", "", 30), proof: "ses_absent", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := validBody
			if test.body != nil {
				body = test.body
			}
			proof := testExternalSession
			if test.proof != "" {
				proof = test.proof
			}
			request := signedRequest(t, secret, now, body, proof)
			if test.mutate != nil {
				test.mutate(request)
			}
			response := perform(bridge, request)
			if response.Code != test.status || strings.Contains(response.Body.String(), "hostname") || strings.Contains(response.Body.String(), "dev_forbidden") {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}

	oversized := bytes.Repeat([]byte(" "), MaxRequestBytes+1)
	request := signedRequest(t, secret, now, oversized, testExternalSession)
	if response := perform(bridge, request); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d", response.Code)
	}
}

func TestBridgeCustomCallbackPathAndProofDomainAreCryptographicallyIsolated(t *testing.T) {
	secret := []byte(strings.Repeat("d", 32))
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	const customPath = "/internal/dsh/remote-exec"
	const customDomain = "AISummoner.DSHBridge.v1"
	bridge, err := New(Options{
		Secret: secret, CallbackPath: customPath, ProofDomain: customDomain,
		Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.Close(ctx); err != nil {
			t.Errorf("close custom bridge: %v", err)
		}
	})
	invoker := &recordingInvoker{result: agent.ToolResult{Stdout: "remote-only\n", ExitCode: 0}}
	lease, err := bridge.Activate(context.Background(), "ags_dsh", testExternalSession, invoker)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	body := callbackBody(t, testExternalSession, "hostname", "", 30)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	requestFor := func(path, domain string) *http.Request {
		request := httptest.NewRequest(http.MethodPost, "http://bridge.invalid"+path, bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:43120"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(HeaderTimestamp, timestamp)
		proof := signProof(secret, domain, testExternalSession, timestamp)
		request.Header.Set("Authorization", Authorization+" "+base64.RawURLEncoding.EncodeToString(proof))
		return request
	}
	if response := perform(bridge, requestFor(CallbackPath, customDomain)); response.Code != http.StatusNotFound {
		t.Fatalf("default path status=%d", response.Code)
	}
	if response := perform(bridge, requestFor(customPath, proofDomain)); response.Code != http.StatusUnauthorized {
		t.Fatalf("OpenCode proof on DSH bridge status=%d", response.Code)
	}
	response := perform(bridge, requestFor(customPath, customDomain))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "remote-only") {
		t.Fatalf("custom bridge status=%d body=%q", response.Code, response.Body.String())
	}
	calls, _, _ := invoker.snapshot()
	if len(calls) != 1 {
		t.Fatalf("custom bridge calls=%d", len(calls))
	}
}

func TestBridgeRejectsInvalidCustomWireIdentity(t *testing.T) {
	secret := []byte(strings.Repeat("b", 32))
	tests := []Options{
		{Secret: secret, CallbackPath: "/public/dsh", ProofDomain: "safe"},
		{Secret: secret, CallbackPath: "/internal/dsh//remote-exec", ProofDomain: "safe"},
		{Secret: secret, CallbackPath: "/internal/dsh/remote-exec?x=1", ProofDomain: "safe"},
		{Secret: secret, CallbackPath: "/internal/dsh/remote-exec", ProofDomain: "bad domain"},
	}
	for _, options := range tests {
		if _, err := New(options); err == nil {
			t.Fatalf("invalid custom bridge options accepted: %#v", options)
		}
	}
}

func TestBridgeConcurrentSessionsNeverCrossInvoke(t *testing.T) {
	bridge, secret, now := newTestBridge(t)
	first := &recordingInvoker{result: agent.ToolResult{Stdout: "first"}}
	second := &recordingInvoker{result: agent.ToolResult{Stdout: "second"}}
	firstLease, err := bridge.Activate(context.Background(), "ags_first", "ses_first", first)
	if err != nil {
		t.Fatal(err)
	}
	defer firstLease.Close()
	secondLease, err := bridge.Activate(context.Background(), "ags_second", "ses_second", second)
	if err != nil {
		t.Fatal(err)
	}
	defer secondLease.Close()
	for _, test := range []struct{ session, want string }{{"ses_first", "first"}, {"ses_second", "second"}} {
		response := perform(bridge, signedRequest(t, secret, now, callbackBody(t, test.session, "hostname", "", 30), test.session))
		var result agent.ToolResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Stdout != test.want {
			t.Fatalf("session=%s status=%d result=%#v", test.session, response.Code, result)
		}
	}
	firstCalls, _, _ := first.snapshot()
	secondCalls, _, _ := second.snapshot()
	if len(firstCalls) != 1 || len(secondCalls) != 1 {
		t.Fatalf("cross-call first=%d second=%d", len(firstCalls), len(secondCalls))
	}
}

func TestBridgeCloseCancelsAndJoinsActiveCallback(t *testing.T) {
	bridge, secret, now := newTestBridge(t)
	invoker := &recordingInvoker{started: make(chan struct{}, 1), wait: make(chan struct{})}
	if _, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker); err != nil {
		t.Fatal(err)
	}
	handlerDone := make(chan struct{})
	go func() {
		_ = perform(bridge, signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 30), testExternalSession))
		close(handlerDone)
	}()
	select {
	case <-invoker.started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerDone:
	default:
		t.Fatal("Bridge.Close returned before handler joined")
	}
	if bridge.ActiveCount() != 0 {
		t.Fatalf("active count=%d", bridge.ActiveCount())
	}
}

func TestBridgeSerializesCallbacksAndDeactivationCancelsAndJoins(t *testing.T) {
	bridge, secret, now := newTestBridge(t)
	never := make(chan struct{})
	invoker := &recordingInvoker{started: make(chan struct{}, 2), wait: never}
	lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
	if err != nil {
		t.Fatal(err)
	}
	body := callbackBody(t, testExternalSession, "hostname", "", 30)
	responses := make(chan *httptest.ResponseRecorder, 2)
	startHandler := make(chan struct{}, 2)
	for index := 0; index < 2; index++ {
		go func() {
			startHandler <- struct{}{}
			responses <- perform(bridge, signedRequest(t, secret, now, body, testExternalSession))
		}()
	}
	<-startHandler
	<-startHandler
	select {
	case <-invoker.started:
	case <-time.After(time.Second):
		t.Fatal("first callback did not invoke")
	}
	// The second handler is registered as in-flight but cannot enter Invoke.
	deadline := time.Now().Add(time.Second)
	for {
		calls, _, maximum := invoker.snapshot()
		if len(calls) == 1 && maximum == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("callback serialization calls=%d max=%d", len(calls), maximum)
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() {
		lease.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("activation close did not cancel and join callbacks")
	}
	for index := 0; index < 2; index++ {
		select {
		case response := <-responses:
			if response.Code == http.StatusOK {
				t.Fatalf("canceled callback returned success: %s", response.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("callback handler remained after deactivation")
		}
	}
	calls, active, maximum := invoker.snapshot()
	if len(calls) != 1 || active != 0 || maximum != 1 || bridge.ActiveCount() != 0 {
		t.Fatalf("deactivation state calls=%d active=%d maximum=%d registry=%d", len(calls), active, maximum, bridge.ActiveCount())
	}
}

func TestBridgeFatalInvocationAndResponseBounds(t *testing.T) {
	t.Run("original invocation error", func(t *testing.T) {
		bridge, secret, now := newTestBridge(t)
		sentinel := errors.New("sentinel invocation failure")
		invoker := &recordingInvoker{err: sentinel}
		lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
		if err != nil {
			t.Fatal(err)
		}
		request := signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 30), testExternalSession)
		response := perform(bridge, request)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), sentinel.Error()) {
			t.Fatalf("fatal response status=%d body=%q", response.Code, response.Body.String())
		}
		select {
		case fatal := <-lease.Failures():
			if !errors.Is(fatal, sentinel) {
				t.Fatalf("fatal error=%v", fatal)
			}
		case <-time.After(time.Second):
			t.Fatal("invocation error was not reported to Adapter")
		}
		lease.Close()
	})

	t.Run("maximum escaped result fits", func(t *testing.T) {
		bridge, secret, now := newTestBridge(t)
		invoker := &recordingInvoker{result: agent.ToolResult{Stdout: strings.Repeat("\x01", agent.MaxToolOutputBytes), Truncated: true}}
		lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
		request := signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 60), testExternalSession)
		response := perform(bridge, request)
		if response.Code != http.StatusOK || response.Body.Len() > MaxResponseBytes {
			t.Fatalf("maximum result status=%d bytes=%d", response.Code, response.Body.Len())
		}
	})

	t.Run("oversized result is fatal not truncated", func(t *testing.T) {
		bridge, secret, now := newTestBridge(t)
		invoker := &recordingInvoker{result: agent.ToolResult{Stdout: strings.Repeat("\x01", MaxResponseBytes)}}
		lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
		if err != nil {
			t.Fatal(err)
		}
		request := signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 30), testExternalSession)
		response := perform(bridge, request)
		if response.Code != http.StatusInternalServerError || response.Body.Len() >= MaxResponseBytes {
			t.Fatalf("oversized result status=%d bytes=%d", response.Code, response.Body.Len())
		}
		select {
		case fatal := <-lease.Failures():
			var adapterError *agent.AdapterError
			if !errors.As(fatal, &adapterError) || adapterError.Code != "protocol_error" || !errors.Is(adapterError, errResponseTooLarge) {
				t.Fatalf("oversized fatal=%v", fatal)
			}
		case <-time.After(time.Second):
			t.Fatal("oversized response did not stop Adapter")
		}
		lease.Close()
	})
}

func TestBridgeDeadlineAndDeliveryFailuresFailClosed(t *testing.T) {
	t.Run("read deadline", func(t *testing.T) {
		bridge, secret, now := newTestBridge(t)
		invoker := &recordingInvoker{}
		lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
		writer := &controlledResponseWriter{readDeadline: errors.New("deadline rejected")}
		bridge.Handler().ServeHTTP(writer, signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 30), testExternalSession))
		calls, _, _ := invoker.snapshot()
		if writer.status != http.StatusInternalServerError || len(calls) != 0 {
			t.Fatalf("status=%d calls=%d", writer.status, len(calls))
		}
	})

	for _, test := range []struct {
		name   string
		writer *controlledResponseWriter
	}{
		{name: "write deadline", writer: &controlledResponseWriter{writeDeadline: errors.New("deadline rejected")}},
		{name: "response write", writer: &controlledResponseWriter{writeFailure: errors.New("peer closed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge, secret, now := newTestBridge(t)
			invoker := &recordingInvoker{result: agent.ToolResult{Stdout: "remote evidence"}}
			lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
			if err != nil {
				t.Fatal(err)
			}
			bridge.Handler().ServeHTTP(test.writer, signedRequest(t, secret, now, callbackBody(t, testExternalSession, "hostname", "", 30), testExternalSession))
			select {
			case fatal := <-lease.Failures():
				var adapterError *agent.AdapterError
				if !errors.As(fatal, &adapterError) || adapterError.Code != "protocol_error" || !errors.Is(adapterError, errResponseDelivery) {
					t.Fatalf("fatal=%v", fatal)
				}
			case <-time.After(time.Second):
				t.Fatal("delivery failure was not reported")
			}
			lease.Close()
		})
	}
}

func TestBridgeRealTCPReadAndWriteDeadlines(t *testing.T) {
	t.Run("slow body", func(t *testing.T) {
		bridge, err := New(Options{Secret: []byte(strings.Repeat("b", 32)), BodyReadTimeout: 25 * time.Millisecond, ResponseTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(bridge.Handler())
		server.Start()
		defer server.Close()
		connection, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		_, _ = io.WriteString(connection, "POST "+CallbackPath+" HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{")
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		started := time.Now()
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if time.Since(started) > 500*time.Millisecond || response.StatusCode == http.StatusOK {
			t.Fatalf("slow body status=%d elapsed=%s", response.StatusCode, time.Since(started))
		}
	})

	t.Run("slow response", func(t *testing.T) {
		secret := []byte(strings.Repeat("b", 32))
		bridge, err := New(Options{Secret: secret, BodyReadTimeout: time.Second, ResponseTimeout: 25 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		invoker := &recordingInvoker{result: agent.ToolResult{Stdout: strings.Repeat("x", MaxResponseBytes-4096)}}
		lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
		server := httptest.NewUnstartedServer(bridge.Handler())
		server.Config.WriteTimeout = 0
		server.Config.ConnState = func(connection net.Conn, state http.ConnState) {
			if state == http.StateNew {
				if tcp, ok := connection.(*net.TCPConn); ok {
					_ = tcp.SetWriteBuffer(1024)
				}
			}
		}
		server.Start()
		defer server.Close()
		connection, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		body := callbackBody(t, testExternalSession, "hostname", "", 30)
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		proof := base64.RawURLEncoding.EncodeToString(signature(secret, testExternalSession, timestamp))
		request := "POST " + CallbackPath + " HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n" + HeaderTimestamp + ": " + timestamp + "\r\nAuthorization: " + Authorization + " " + proof + "\r\n\r\n"
		_, _ = io.WriteString(connection, request)
		_, _ = connection.Write(body)
		time.Sleep(150 * time.Millisecond)
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, connection)
		select {
		case fatal := <-lease.Failures():
			var adapterError *agent.AdapterError
			if !errors.As(fatal, &adapterError) || adapterError.Code != "protocol_error" {
				t.Fatalf("slow response fatal=%v", fatal)
			}
		case <-time.After(time.Second):
			t.Fatal("real TCP slow reader did not trigger delivery fatal")
		}
	})
}

func TestBridgeEscapedRequestBoundaryAndTaskLimitPropagation(t *testing.T) {
	bridge, secret, now := newTestBridge(t)
	invoker := &recordingInvoker{result: agent.ToolResult{ExitCode: 0}}
	lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	command := strings.Repeat("\x01", agent.MaxCommandBytes)
	cwd := "/" + strings.Repeat("\x01", agent.MaxCWDBytes-1)
	body := callbackBody(t, testExternalSession, command, cwd, 60)
	if len(body) > MaxRequestBytes {
		t.Fatalf("valid escaped request bytes=%d cap=%d", len(body), MaxRequestBytes)
	}
	response := perform(bridge, signedRequest(t, secret, now, body, testExternalSession))
	if response.Code != http.StatusOK {
		t.Fatalf("escaped maximum status=%d body=%s", response.Code, response.Body.String())
	}
	calls, _, _ := invoker.snapshot()
	var arguments agent.RemoteExecArguments
	if len(calls) != 1 || json.Unmarshal(calls[0].Arguments, &arguments) != nil || arguments.Command != command || arguments.CWD != cwd {
		t.Fatalf("escaped maximum was not preserved")
	}

	tooLongInvoker := &recordingInvoker{err: agent.ErrInvalidTool}
	tooLongLease, err := bridge.Activate(context.Background(), "ags_product_2", "ses_bridge_oversize", tooLongInvoker)
	if err != nil {
		t.Fatal(err)
	}
	defer tooLongLease.Close()
	tooLong := callbackBody(t, "ses_bridge_oversize", strings.Repeat("x", agent.MaxCommandBytes+1), "", 30)
	response = perform(bridge, signedRequest(t, secret, now, tooLong, "ses_bridge_oversize"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("decoded oversize status=%d", response.Code)
	}
	select {
	case fatal := <-tooLongLease.Failures():
		if !errors.Is(fatal, agent.ErrInvalidTool) {
			t.Fatalf("decoded oversize error=%v", fatal)
		}
	case <-time.After(time.Second):
		t.Fatal("decoded oversize was not propagated from authoritative invoker")
	}
}

func TestBridgeRedactsCommandOutputSecretAndCredentialFromLogsAndErrors(t *testing.T) {
	commandSentinel := "COMMAND_SENTINEL_DO_NOT_LOG"
	outputSentinel := "OUTPUT_SENTINEL_DO_NOT_LOG"
	secretSentinel := strings.Repeat("SECRET_SENTINEL_", 3)
	var logs bytes.Buffer
	bridge, err := New(Options{Secret: []byte(secretSentinel), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	invoker := &recordingInvoker{result: agent.ToolResult{Stdout: outputSentinel}}
	lease, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, invoker)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	body := callbackBody(t, testExternalSession, commandSentinel, "", 30)
	response := perform(bridge, signedRequest(t, []byte(secretSentinel), now, body, testExternalSession))
	if response.Code != http.StatusOK {
		t.Fatalf("valid status=%d", response.Code)
	}
	badRequest := signedRequest(t, []byte(secretSentinel), now, body, testExternalSession)
	badRequest.Header.Set("Authorization", Authorization+" "+secretSentinel)
	rejected := perform(bridge, badRequest)
	combined := logs.String() + rejected.Body.String()
	for _, sentinel := range []string{commandSentinel, outputSentinel, secretSentinel} {
		if strings.Contains(combined, sentinel) {
			t.Fatalf("sentinel %q leaked in logs/error response", sentinel)
		}
	}
}

func TestBridgeCloseRejectsActivation(t *testing.T) {
	bridge, _, _ := newTestBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Activate(context.Background(), "ags_product", testExternalSession, &recordingInvoker{}); !errors.Is(err, errBridgeClosed) {
		t.Fatalf("activation after close result=%v", err)
	}
}
