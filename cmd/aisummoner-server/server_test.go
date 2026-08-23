package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/agent"
	"github.com/aisummoner/aisummoner/internal/app"
	"github.com/aisummoner/aisummoner/internal/config"
	"github.com/aisummoner/aisummoner/internal/httpapi"
	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/opencodebridge"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/tunnel"
	"github.com/coder/websocket"
)

const integrationPassword = "integration-password"

type adapterRegistryProbe struct {
	provider string
	adapter  agent.Adapter
	err      error
}

func (probe *adapterRegistryProbe) SetAdapter(provider string, adapter agent.Adapter) error {
	probe.provider = provider
	probe.adapter = adapter
	return probe.err
}

type compositionFixture struct {
	t              *testing.T
	server         *builtServer
	baseURL        string
	origin         string
	cookie         *http.Cookie
	clientCancel   context.CancelFunc
	clientDone     chan error
	deviceID       string
	pairingOffered chan tunnel.PairingNotification
	serverCancel   context.CancelFunc
	serverDone     chan error
	tunnelWS       *websocket.Conn
	terminalWS     *websocket.Conn
	eventsBody     io.ReadCloser
	eventsFrames   <-chan string
	eventsClosed   <-chan error
	closeOnce      sync.Once
}

func newCompositionFixture(t *testing.T) *compositionFixture {
	t.Helper()
	dataDirectory := t.TempDir()
	listenAddress := reserveLoopbackAddress(t)
	origin := "http://" + listenAddress
	configuration := config.Config{
		BaseURL: mustURL(t, origin), ListenAddr: listenAddress,
		DataDir: dataDirectory, DatabasePath: filepath.Join(dataDirectory, "aisummoner.db"),
		AdminPassword: integrationPassword, SessionSecret: bytes.Repeat([]byte{0x31}, 32),
		PairingSecret: bytes.Repeat([]byte{0x32}, 32), DevMode: true,
		AllowedOrigin: origin, AgentAdapter: config.AgentAdapterFake,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := buildServer(context.Background(), configuration, logger)
	if err != nil {
		t.Fatal(err)
	}
	serverContext, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.runtime.Run(serverContext) }()
	select {
	case <-server.runtime.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("production buildServer did not become ready")
	}
	fixture := &compositionFixture{
		t: t, server: server, baseURL: "http://" + server.publicAddress,
		origin: configuration.AllowedOrigin, pairingOffered: make(chan tunnel.PairingNotification, 1),
		serverCancel: cancelServer, serverDone: serverDone,
	}
	t.Cleanup(fixture.close)
	return fixture
}

func (fixture *compositionFixture) close() {
	fixture.closeOnce.Do(func() {
		// Keep the Remote process alive while Server shutdown owns all open
		// Tunnel, Terminal and SSE peers. Only after Runtime has joined every
		// domain do we cancel the Remote reconnect loop.
		fixture.serverCancel()
		select {
		case err := <-fixture.serverDone:
			if err != nil {
				fixture.t.Errorf("Server Runtime shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			fixture.t.Error("Server Runtime did not join shutdown")
		}
		if fixture.deviceID != "" && fixture.server.manager.IsOnline(fixture.deviceID) {
			fixture.t.Error("Runtime returned while the real Device remained online")
		}
		assertWebSocketClosed(fixture.t, fixture.terminalWS, "Terminal")
		assertWebSocketClosed(fixture.t, fixture.tunnelWS, "Tunnel")
		if fixture.eventsClosed != nil {
			select {
			case err := <-fixture.eventsClosed:
				if err == nil {
					break
				}
			case <-time.After(time.Second):
				fixture.t.Error("Agent SSE remained open after Runtime shutdown")
				_ = fixture.eventsBody.Close()
			}
		}
		if fixture.clientCancel != nil {
			fixture.clientCancel()
		}
		if fixture.clientDone != nil {
			select {
			case err := <-fixture.clientDone:
				if err != nil {
					fixture.t.Errorf("Remote Client shutdown: %v", err)
				}
			case <-time.After(5 * time.Second):
				fixture.t.Error("Remote Client did not join shutdown")
			}
		}
		if fixture.eventsBody != nil {
			_ = fixture.eventsBody.Close()
		}
		if fixture.terminalWS != nil {
			_ = fixture.terminalWS.CloseNow()
		}
		if fixture.tunnelWS != nil {
			_ = fixture.tunnelWS.CloseNow()
		}
	})
}

func assertWebSocketClosed(t *testing.T, connection *websocket.Conn, name string) {
	t.Helper()
	if connection == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		if _, _, err := connection.Read(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s WebSocket remained open after Runtime shutdown", name)
			}
			return
		}
	}
}

func (fixture *compositionFixture) request(method, path, body string, authenticated, origin bool) *http.Response {
	fixture.t.Helper()
	request, err := http.NewRequest(method, fixture.baseURL+path, strings.NewReader(body))
	if err != nil {
		fixture.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if origin {
		request.Header.Set("Origin", fixture.origin)
	}
	if authenticated && fixture.cookie != nil {
		request.AddCookie(fixture.cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return response
}

func (fixture *compositionFixture) login() {
	fixture.t.Helper()
	response := fixture.request(http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"`+integrationPassword+`"}`, false, true)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(response.Body)
		fixture.t.Fatalf("login status=%d body=%s", response.StatusCode, contents)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == httpapi.SessionCookieName {
			fixture.cookie = cookie
		}
	}
	if fixture.cookie == nil {
		fixture.t.Fatal("login did not return the session cookie")
	}
}

func (fixture *compositionFixture) connectAndPairRemote() tunnel.PairingNotification {
	return fixture.connectAndPairRemoteWithJitter(func(time.Duration) time.Duration { return time.Second })
}

func (fixture *compositionFixture) connectAndPairRemoteWithJitter(jitter func(time.Duration) time.Duration) tunnel.PairingNotification {
	fixture.t.Helper()
	deviceIdentity, err := identity.LoadOrCreate(filepath.Join(fixture.t.TempDir(), "remote"))
	if err != nil {
		fixture.t.Fatal(err)
	}
	hostSigner, err := deviceIdentity.SSHSigner()
	if err != nil {
		fixture.t.Fatal(err)
	}
	sshHandler, err := sshserver.New(hostSigner)
	if err != nil {
		fixture.t.Fatal(err)
	}
	client, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: fixture.baseURL, DevMode: true, Identity: deviceIdentity,
		DeviceName: "composition-remote", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnPairing: func(offer tunnel.PairingNotification) {
			select {
			case fixture.pairingOffered <- offer:
			default:
			}
		},
		StreamHandler: func(ctx context.Context, stream net.Conn, _ protocol.StreamHeader, session tunnel.ClientSession) {
			_ = sshHandler.Serve(ctx, stream, session.SSHClientPublicKey)
		},
		StableOnline: time.Second, Jitter: jitter,
	})
	if err != nil {
		fixture.t.Fatal(err)
	}
	clientContext, cancelClient := context.WithCancel(context.Background())
	fixture.clientCancel = cancelClient
	fixture.clientDone = make(chan error, 1)
	fixture.deviceID = deviceIdentity.DeviceID
	go func() { fixture.clientDone <- client.Run(clientContext) }()
	var offer tunnel.PairingNotification
	select {
	case offer = <-fixture.pairingOffered:
	case <-time.After(5 * time.Second):
		fixture.t.Fatal("real Remote did not receive a pairing offer")
	}
	claimBody, _ := json.Marshal(map[string]string{"code": offer.Code})
	response := fixture.request(http.MethodPost, "/api/v1/pairings/claim", string(claimBody), true, true)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(response.Body)
		fixture.t.Fatalf("pairing claim status=%d body=%s", response.StatusCode, contents)
	}
	return offer
}

func TestBuiltServerFakeFullRouteCompositionAndJoinedShutdown(t *testing.T) {
	fixture := newCompositionFixture(t)
	if fixture.server.provider != config.AgentAdapterFake || fixture.server.bridgeAddress != "" {
		t.Fatalf("Fake composition provider=%q bridge=%q", fixture.server.provider, fixture.server.bridgeAddress)
	}

	health := fixture.request(http.MethodGet, "/healthz", "", false, false)
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", health.StatusCode)
	}
	root := fixture.request(http.MethodGet, "/", "", false, false)
	rootContents, _ := io.ReadAll(root.Body)
	root.Body.Close()
	if root.StatusCode != http.StatusOK || !bytes.Contains(rootContents, []byte("AISummoner WebUI assets were not included")) || root.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("embedded placeholder status=%d cache=%q body=%q", root.StatusCode, root.Header.Get("Cache-Control"), rootContents)
	}
	spa := fixture.request(http.MethodGet, "/devices/client-route", "", false, false)
	spaContents, _ := io.ReadAll(spa.Body)
	spa.Body.Close()
	if spa.StatusCode != http.StatusOK || !bytes.Contains(spaContents, []byte("AISummoner WebUI assets were not included")) {
		t.Fatalf("SPA fallback status=%d body=%q", spa.StatusCode, spaContents)
	}
	apiMiss := fixture.request(http.MethodGet, "/api/v1/not-a-route", "", false, false)
	apiMissContents, _ := io.ReadAll(apiMiss.Body)
	apiMiss.Body.Close()
	if apiMiss.StatusCode != http.StatusNotFound || !bytes.Contains(apiMissContents, []byte(`"code":"NOT_FOUND"`)) || bytes.Contains(apiMissContents, []byte("<html")) {
		t.Fatalf("API miss status=%d body=%q", apiMiss.StatusCode, apiMissContents)
	}
	fixture.login()

	// A real WebSocket upgrade through the production dispatcher reaches the
	// unauthenticated Device Tunnel handler. It stays idle until shutdown owns
	// and joins it.
	tunnelContext, cancelTunnel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTunnel()
	tunnelConnection, response, err := websocket.Dial(tunnelContext, "ws://"+fixture.server.publicAddress+"/api/v1/tunnel", nil)
	if err != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Tunnel upgrade response=%v error=%v", response, err)
	}
	fixture.tunnelWS = tunnelConnection

	fixture.connectAndPairRemote()
	devices := fixture.request(http.MethodGet, "/api/v1/devices", "", true, false)
	deviceContents, _ := io.ReadAll(devices.Body)
	devices.Body.Close()
	if devices.StatusCode != http.StatusOK || !bytes.Contains(deviceContents, []byte(fixture.deviceID)) || !bytes.Contains(deviceContents, []byte(`"online":true`)) {
		t.Fatalf("Device JSON status=%d body=%s", devices.StatusCode, deviceContents)
	}

	terminalHeaders := http.Header{}
	terminalHeaders.Set("Origin", fixture.origin)
	terminalHeaders.Set("Cookie", fixture.cookie.String())
	terminalContext, cancelTerminal := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTerminal()
	terminalConnection, terminalResponse, err := websocket.Dial(
		terminalContext,
		"ws://"+fixture.server.publicAddress+"/api/v1/devices/"+fixture.deviceID+"/terminal",
		&websocket.DialOptions{HTTPHeader: terminalHeaders},
	)
	if err != nil || terminalResponse == nil || terminalResponse.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Terminal upgrade response=%v error=%v", terminalResponse, err)
	}
	fixture.terminalWS = terminalConnection
	terminalSentinel := "AISUMMONER_TERMINAL_DATA_PLANE_SENTINEL"
	writeContext, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	terminalCommand := "printf 'AISUMMONER_%s\\n' 'TERMINAL_DATA_PLANE_SENTINEL'\n"
	if strings.Contains(terminalCommand, terminalSentinel) {
		t.Fatal("Terminal command echo contains the complete output sentinel")
	}
	if err := terminalConnection.Write(writeContext, websocket.MessageBinary, []byte(terminalCommand)); err != nil {
		cancelWrite()
		t.Fatalf("Terminal data-plane write: %v", err)
	}
	cancelWrite()
	readContext, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	var terminalOutput bytes.Buffer
	for !strings.Contains(terminalOutput.String(), terminalSentinel) {
		messageType, contents, err := terminalConnection.Read(readContext)
		if err != nil {
			t.Fatalf("Terminal data-plane read before sentinel: output=%q error=%v", terminalOutput.String(), err)
		}
		if messageType != websocket.MessageBinary {
			t.Fatalf("Terminal emitted non-binary data frame %v", messageType)
		}
		terminalOutput.Write(contents)
		if terminalOutput.Len() > 128*1024 {
			t.Fatal("Terminal sentinel was not observed within bounded output")
		}
	}

	createSessionBody := `{"approval_mode":"full_access"}`
	created := fixture.request(http.MethodPost, "/api/v1/devices/"+fixture.deviceID+"/agent-sessions", createSessionBody, true, true)
	var createdPayload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(created.Body).Decode(&createdPayload); err != nil {
		created.Body.Close()
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated || createdPayload.Session.ID == "" {
		t.Fatalf("Agent create status=%d payload=%+v", created.StatusCode, createdPayload)
	}
	eventsRequest, err := http.NewRequest(http.MethodGet, fixture.baseURL+"/api/v1/agent-sessions/"+createdPayload.Session.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsRequest.AddCookie(fixture.cookie)
	eventsResponse, err := http.DefaultClient.Do(eventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	fixture.eventsBody = eventsResponse.Body
	if eventsResponse.StatusCode != http.StatusOK || eventsResponse.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("Agent SSE status=%d content-type=%q", eventsResponse.StatusCode, eventsResponse.Header.Get("Content-Type"))
	}
	frames, streamClosed := streamSSE(eventsResponse.Body)
	fixture.eventsFrames, fixture.eventsClosed = frames, streamClosed
	select {
	case frame := <-frames:
		if frame != ": connected\n\n" {
			t.Fatalf("Agent SSE first flush=%q", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent SSE did not immediately flush its connected frame")
	}
	message := fixture.request(http.MethodPost, "/api/v1/agent-sessions/"+createdPayload.Session.ID+"/messages", `{"content":"inspect the remote"}`, true, true)
	messageContents, _ := io.ReadAll(message.Body)
	message.Body.Close()
	if message.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent message status=%d body=%s", message.StatusCode, messageContents)
	}
	var eventText strings.Builder
	completed := false
	deadline := time.After(8 * time.Second)
	for !completed {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("Agent SSE closed before turn completion: %s", eventText.String())
			}
			eventText.WriteString(frame)
			if strings.Contains(frame, "event: turn.completed\n") {
				completed = true
			}
		case <-deadline:
			t.Fatalf("Agent remote turn did not complete over SSE: %s", eventText.String())
		}
	}
	snapshot := fixture.request(http.MethodGet, "/api/v1/agent-sessions/"+createdPayload.Session.ID, "", true, false)
	snapshotContents, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	remoteHostname, hostnameErr := os.Hostname()
	if hostnameErr != nil {
		t.Fatal(hostnameErr)
	}
	if snapshot.StatusCode != http.StatusOK || !bytes.Contains(snapshotContents, []byte("Remote hostname:")) ||
		!bytes.Contains(snapshotContents, []byte("Remote system:")) || !bytes.Contains(snapshotContents, []byte(remoteHostname)) ||
		strings.Count(eventText.String(), "event: tool_call.started\n") < 2 || strings.Count(eventText.String(), "event: tool_call.output\n") < 2 {
		t.Fatalf("Agent did not persist real Remote hostname/uname evidence: status=%d body=%s events=%s", snapshot.StatusCode, snapshotContents, eventText.String())
	}

	// Cleanup deliberately leaves all three upgraded/open domains active.
	// fixture.close cancels the Remote and Runtime and requires both joined
	// process owners to return within their bounded shutdown windows.
}

func TestBuiltServerDeleteUnpairInvalidatesLiveDeviceResources(t *testing.T) {
	fixture := newCompositionFixture(t)
	fixture.login()
	reconnect := newClientReconnectBarrier()
	// This cleanup is registered after fixture.close, so LIFO test cleanup
	// always releases a blocked Jitter callback before joining the Client.
	t.Cleanup(reconnect.Release)
	firstOffer := fixture.connectAndPairRemoteWithJitter(reconnect.Jitter)
	if !fixture.server.manager.IsOnline(fixture.deviceID) {
		t.Fatal("paired real Remote was not published online")
	}

	terminalHeaders := http.Header{}
	terminalHeaders.Set("Origin", fixture.origin)
	terminalHeaders.Set("Cookie", fixture.cookie.String())
	terminalContext, cancelTerminal := context.WithTimeout(context.Background(), 5*time.Second)
	terminalConnection, terminalResponse, err := websocket.Dial(
		terminalContext,
		"ws://"+fixture.server.publicAddress+"/api/v1/devices/"+fixture.deviceID+"/terminal",
		&websocket.DialOptions{HTTPHeader: terminalHeaders},
	)
	cancelTerminal()
	if err != nil || terminalResponse == nil || terminalResponse.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Terminal upgrade response=%v error=%v", terminalResponse, err)
	}
	fixture.terminalWS = terminalConnection

	created := fixture.request(
		http.MethodPost,
		"/api/v1/devices/"+fixture.deviceID+"/agent-sessions",
		`{"approval_mode":"full_access"}`,
		true,
		true,
	)
	var createdPayload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	decodeError := json.NewDecoder(created.Body).Decode(&createdPayload)
	created.Body.Close()
	if decodeError != nil || created.StatusCode != http.StatusCreated || createdPayload.Session.ID == "" {
		t.Fatalf("Agent create status=%d payload=%+v decode=%v", created.StatusCode, createdPayload, decodeError)
	}
	sessionID := createdPayload.Session.ID
	eventsRequest, err := http.NewRequest(http.MethodGet, fixture.baseURL+"/api/v1/agent-sessions/"+sessionID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsRequest.AddCookie(fixture.cookie)
	eventsResponse, err := http.DefaultClient.Do(eventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	fixture.eventsBody = eventsResponse.Body
	if eventsResponse.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(eventsResponse.Body)
		t.Fatalf("Agent SSE status=%d body=%s", eventsResponse.StatusCode, contents)
	}
	frames, streamClosed := streamSSE(eventsResponse.Body)
	fixture.eventsFrames, fixture.eventsClosed = frames, streamClosed
	select {
	case frame := <-frames:
		if frame != ": connected\n\n" {
			t.Fatalf("Agent SSE first flush=%q", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent SSE did not immediately flush before unpair")
	}

	unpair := fixture.request(http.MethodDelete, "/api/v1/devices/"+fixture.deviceID, "", true, true)
	unpairContents, _ := io.ReadAll(unpair.Body)
	unpair.Body.Close()
	if unpair.StatusCode != http.StatusNoContent || len(unpairContents) != 0 {
		t.Fatalf("Device DELETE status=%d body=%s", unpair.StatusCode, unpairContents)
	}
	// Do not release reconnect until every server-side invalidation surface has
	// been observed. The callback proves the original Tunnel actually exited.
	select {
	case <-reconnect.Observed():
	case <-time.After(3 * time.Second):
		t.Fatal("unpair did not close the real Remote Tunnel")
	}
	if fixture.server.manager.IsOnline(fixture.deviceID) {
		t.Fatal("unpaired Device remained online while reconnect was blocked")
	}
	assertDeviceHiddenAfterUnpair(t, fixture)
	assertWebSocketClosed(t, terminalConnection, "unpaired Terminal")
	select {
	case <-streamClosed:
		fixture.eventsClosed = nil
		_ = fixture.eventsBody.Close()
		fixture.eventsBody = nil
	case <-time.After(time.Second):
		t.Fatal("unpair did not close the Agent SSE subscription")
	}
	assertAgentSessionRevoked(t, fixture, sessionID)

	reconnect.Release()
	var freshOffer tunnel.PairingNotification
	select {
	case freshOffer = <-fixture.pairingOffered:
	case <-time.After(5 * time.Second):
		t.Fatal("same Remote identity did not receive a fresh pairing offer after unpair")
	}
	if freshOffer.Code == "" || freshOffer.Code == firstOffer.Code {
		t.Fatalf("fresh pairing code=%q initial=%q", freshOffer.Code, firstOffer.Code)
	}
	assertDeviceHiddenAfterUnpair(t, fixture)
}

func assertDeviceHiddenAfterUnpair(t *testing.T, fixture *compositionFixture) {
	t.Helper()
	response := fixture.request(http.MethodGet, "/api/v1/devices/"+fixture.deviceID, "", true, false)
	contents, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || !bytes.Contains(contents, []byte(`"code":"DEVICE_NOT_FOUND"`)) {
		t.Fatalf("unpaired Device owner surface status=%d body=%s", response.StatusCode, contents)
	}
}

func assertAgentSessionRevoked(t *testing.T, fixture *compositionFixture, sessionID string) {
	t.Helper()
	for _, request := range []struct {
		method string
		path   string
		body   string
		origin bool
	}{
		{method: http.MethodGet, path: "/api/v1/agent-sessions/" + sessionID},
		{method: http.MethodPost, path: "/api/v1/agent-sessions/" + sessionID + "/messages", body: `{"content":"must stay revoked"}`, origin: true},
	} {
		response := fixture.request(request.method, request.path, request.body, true, request.origin)
		contents, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound || !bytes.Contains(contents, []byte(`"code":"NOT_FOUND"`)) {
			t.Fatalf("revoked Agent request %s %s status=%d body=%s", request.method, request.path, response.StatusCode, contents)
		}
	}
}

type clientReconnectBarrier struct {
	observed    chan struct{}
	release     chan struct{}
	observeOnce sync.Once
	releaseOnce sync.Once
}

func newClientReconnectBarrier() *clientReconnectBarrier {
	return &clientReconnectBarrier{observed: make(chan struct{}), release: make(chan struct{})}
}

func (barrier *clientReconnectBarrier) Jitter(time.Duration) time.Duration {
	first := false
	barrier.observeOnce.Do(func() {
		first = true
		close(barrier.observed)
	})
	if first {
		<-barrier.release
		return 0
	}
	return time.Hour
}

func (barrier *clientReconnectBarrier) Observed() <-chan struct{} { return barrier.observed }

func (barrier *clientReconnectBarrier) Release() {
	barrier.releaseOnce.Do(func() { close(barrier.release) })
}

func TestBuiltServerOpenCodeHealthBeforeReadinessAndPrivateBridge(t *testing.T) {
	publicAddress := reserveLoopbackAddress(t)
	bridgeAddress := reserveLoopbackAddress(t)
	healthChecked := make(chan struct{}, 1)
	sidecar := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/global/health" || request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		username, password, authenticated := request.BasicAuth()
		if !authenticated || username != "composition-user" || password != "composition-password" {
			t.Error("OpenCode startup health omitted configured Basic Auth")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		bridgeConnection, err := net.DialTimeout("tcp", bridgeAddress, 250*time.Millisecond)
		if err != nil {
			t.Errorf("Bridge was not pre-bound before OpenCode health: %v", err)
		} else {
			_ = bridgeConnection.Close()
		}
		publicConnection, err := net.DialTimeout("tcp", publicAddress, 100*time.Millisecond)
		if err == nil {
			_ = publicConnection.Close()
			t.Error("public listener was bound before OpenCode startup health passed")
		}
		select {
		case healthChecked <- struct{}{}:
		default:
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"healthy":true,"version":"1.18.11"}`)
	}))
	defer sidecar.Close()

	configuration := openCodeConfiguration(t, publicAddress, bridgeAddress, sidecar.URL)
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 5*time.Second)
	server, err := buildServer(startupContext, configuration, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cancelStartup()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-healthChecked:
	default:
		t.Fatal("OpenCode startup health was not called")
	}
	if server.provider != "opencode" || server.bridgeAddress != bridgeAddress {
		t.Fatalf("OpenCode composition provider=%q bridge=%q", server.provider, server.bridgeAddress)
	}
	select {
	case <-server.runtime.Ready():
		t.Fatal("buildServer published readiness before Runtime started")
	default:
	}

	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- server.runtime.Run(runtimeContext) }()
	select {
	case <-server.runtime.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("OpenCode composition Runtime did not become ready")
	}
	publicCallback, err := http.Get("http://" + server.publicAddress + opencodebridge.CallbackPath)
	if err != nil {
		t.Fatal(err)
	}
	publicContents, _ := io.ReadAll(publicCallback.Body)
	publicCallback.Body.Close()
	if publicCallback.StatusCode != http.StatusNotFound || !bytes.Contains(publicContents, []byte(`"code":"NOT_FOUND"`)) {
		t.Fatalf("public Bridge callback status=%d body=%s", publicCallback.StatusCode, publicContents)
	}
	privateCallback, err := http.Get("http://" + server.bridgeAddress + opencodebridge.CallbackPath)
	if err != nil {
		t.Fatal(err)
	}
	privateContents, _ := io.ReadAll(privateCallback.Body)
	privateCallback.Body.Close()
	if privateCallback.StatusCode != http.StatusMethodNotAllowed || !bytes.Contains(privateContents, []byte(`"error":"request rejected"`)) {
		t.Fatalf("private Bridge callback status=%d body=%s", privateCallback.StatusCode, privateContents)
	}
	cancelRuntime()
	select {
	case err := <-runtimeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenCode composition Runtime did not join")
	}
	assertAddressReusable(t, bridgeAddress)
}

func TestBuiltServerOpenCodeStartupFailuresUnwindBridge(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		timeout time.Duration
		want    error
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, timeout: 5 * time.Second, want: app.ErrOpenCodeRateLimited},
		{name: "unavailable", status: http.StatusServiceUnavailable, timeout: 1500 * time.Millisecond, want: app.ErrOpenCodeUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sidecar := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
			}))
			defer sidecar.Close()
			publicAddress := reserveLoopbackAddress(t)
			bridgeAddress := reserveLoopbackAddress(t)
			configuration := openCodeConfiguration(t, publicAddress, bridgeAddress, sidecar.URL)
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			server, err := buildServer(ctx, configuration, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if server != nil || !errors.Is(err, test.want) {
				t.Fatalf("OpenCode startup result server=%v error=%v want=%v", server, err, test.want)
			}
			assertAddressReusable(t, bridgeAddress)
			assertAddressReusable(t, publicAddress)
		})
	}
}

func openCodeConfiguration(t *testing.T, publicAddress, bridgeAddress, sidecarURL string) config.Config {
	t.Helper()
	dataDirectory := t.TempDir()
	return config.Config{
		BaseURL: mustURL(t, "http://"+publicAddress), ListenAddr: publicAddress,
		DataDir: dataDirectory, DatabasePath: filepath.Join(dataDirectory, "aisummoner.db"),
		AdminPassword: integrationPassword, SessionSecret: bytes.Repeat([]byte{0x41}, 32),
		PairingSecret: bytes.Repeat([]byte{0x42}, 32), DevMode: true,
		AllowedOrigin: "http://" + publicAddress, AgentAdapter: config.AgentAdapterOpenCode,
		AgentWorkspaceRoot: filepath.Join(dataDirectory, "workspaces"), OpenCodeURL: sidecarURL,
		OpenCodeUsername: "composition-user", OpenCodePassword: "composition-password", OpenCodeModel: "free-model",
		AgentBridgeListenAddr: bridgeAddress,
		OpenCodeBridgeURL:     "http://" + bridgeAddress + opencodebridge.CallbackPath,
		AgentBridgeSecret:     bytes.Repeat([]byte{0x43}, 32),
	}
}

func TestBuiltServerDeepSeekCompositionHasNoOpenCodeBridge(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("DeepSeek provider was contacted during Server construction")
	}))
	defer provider.Close()
	publicAddress := reserveLoopbackAddress(t)
	dataDirectory := t.TempDir()
	configuration := config.Config{
		BaseURL: mustURL(t, "http://"+publicAddress), ListenAddr: publicAddress,
		DataDir: dataDirectory, DatabasePath: filepath.Join(dataDirectory, "aisummoner.db"),
		AdminPassword: integrationPassword, SessionSecret: bytes.Repeat([]byte{0x51}, 32),
		PairingSecret: bytes.Repeat([]byte{0x52}, 32), DevMode: true,
		AllowedOrigin: "http://" + publicAddress, AgentAdapter: config.AgentAdapterDeepSeek,
		DeepSeekURL: provider.URL, DeepSeekAPIKey: "composition-provider-key", DeepSeekModel: "deepseek-v4-flash",
	}
	server, err := buildServer(context.Background(), configuration, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if server.provider != "deepseek" || server.bridgeAddress != "" {
		t.Fatalf("DeepSeek composition provider=%q bridge=%q", server.provider, server.bridgeAddress)
	}
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.runtime.Run(runContext) }()
	select {
	case <-server.runtime.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("DeepSeek Server Runtime did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DeepSeek Server Runtime did not join")
	}
}

func TestDeepSeekProviderConfiguratorValidatesBeforeRegistryMutation(t *testing.T) {
	probe := &adapterRegistryProbe{}
	configurator := &deepSeekProviderConfigurator{registry: probe}
	if err := configurator.ConfigureDeepSeek(context.Background(), "", "deepseek-v4-flash"); !errors.Is(err, agent.ErrInvalidRequest) {
		t.Fatalf("empty key error=%v", err)
	}
	if probe.adapter != nil {
		t.Fatal("invalid credential mutated the Adapter registry")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := configurator.ConfigureDeepSeek(canceled, "valid-test-key", "deepseek-v4-flash"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled configuration error=%v", err)
	}
	if probe.adapter != nil {
		t.Fatal("canceled configuration mutated the Adapter registry")
	}
	if err := configurator.ConfigureDeepSeek(context.Background(), "valid-test-key", "deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	if probe.provider != agent.ProviderDeepSeek || probe.adapter == nil {
		t.Fatalf("registry provider=%q adapter=%T", probe.provider, probe.adapter)
	}
}

func TestBuiltServerWebDeepSeekConfigurationBindsNewConversation(t *testing.T) {
	fixture := newCompositionFixture(t)
	fixture.login()
	fixture.connectAndPairRemote()
	secret := "web-entry-test-key-sentinel"
	body, _ := json.Marshal(map[string]string{"api_key": secret, "model": "deepseek-v4-flash"})
	configured := fixture.request(http.MethodPost, "/api/v1/agent-provider/deepseek", string(body), true, true)
	configuredBody, _ := io.ReadAll(configured.Body)
	configured.Body.Close()
	if configured.StatusCode != http.StatusNoContent || len(configuredBody) != 0 || bytes.Contains(configuredBody, []byte(secret)) {
		t.Fatalf("DeepSeek configuration status=%d body=%q", configured.StatusCode, configuredBody)
	}

	created := fixture.request(
		http.MethodPost,
		"/api/v1/devices/"+fixture.deviceID+"/agent-sessions",
		`{"approval_mode":"per_command"}`,
		true,
		true,
	)
	defer created.Body.Close()
	var payload struct {
		Session struct {
			Provider string `json:"provider"`
		} `json:"session"`
	}
	if err := json.NewDecoder(created.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated || payload.Session.Provider != agent.ProviderDeepSeek {
		t.Fatalf("new conversation status=%d provider=%q", created.StatusCode, payload.Session.Provider)
	}
}

func assertAddressReusable(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener %s was not released: %v", address, err)
	}
	_ = listener.Close()
}

func streamSSE(body io.Reader) (<-chan string, <-chan error) {
	frames := make(chan string, 128)
	closed := make(chan error, 1)
	go func() {
		defer close(frames)
		reader := bufio.NewReader(body)
		var frame strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				frame.WriteString(line)
				if line == "\n" {
					frames <- frame.String()
					frame.Reset()
				}
			}
			if err != nil {
				closed <- err
				return
			}
		}
	}()
	return frames, closed
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestStartupReadyLogDoesNotExposeConfigurationValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logServerReady(logger)
	logged := output.String()
	if !strings.Contains(logged, "server ready") {
		t.Fatalf("startup readiness was not logged: %q", logged)
	}
	for _, sentinel := range []string{
		"base-url-sentinel.invalid", "127.0.0.1:65432", "provider-sentinel",
	} {
		if strings.Contains(logged, sentinel) {
			t.Fatalf("startup log exposed configuration sentinel %q: %q", sentinel, logged)
		}
	}
}

func TestStartupCleanupTransferLeavesSoleRuntimeOwnership(t *testing.T) {
	closed := make([]string, 0, 2)
	cleanup := &startupCleanup{}
	cleanup.Add(func() { closed = append(closed, "database") })
	cleanup.Add(func() { closed = append(closed, "tunnel") })
	cleanup.Transfer()
	cleanup.Cleanup()
	if len(closed) != 0 {
		t.Fatalf("transferred startup resources were closed: %v", closed)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("resource addition after transfer did not fail closed")
		}
	}()
	cleanup.Add(func() {})
}

func TestStartupCleanupRunsReverseBeforeTransfer(t *testing.T) {
	closed := make([]string, 0, 3)
	cleanup := &startupCleanup{}
	cleanup.Add(func() { closed = append(closed, "database") })
	cleanup.Add(func() { closed = append(closed, "tunnel") })
	cleanup.Add(func() { closed = append(closed, "bridge") })
	cleanup.Cleanup()
	if got := strings.Join(closed, ","); got != "bridge,tunnel,database" {
		t.Fatalf("startup failure cleanup order=%q", got)
	}
}

func TestServerSourceHasNoPostTransferDatabaseOrTunnelDefer(t *testing.T) {
	contents, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, forbidden := range []string{"defer database.Close", "defer tunnelGateway.Close", "defer publicListener.Close"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("composition retained direct lifetime defer %q", forbidden)
		}
	}
	for _, forbidden := range []string{`"address", server.publicAddress`, `"base_url"`, `"agent_provider"`} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("composition startup logging retained configured value %q", forbidden)
		}
	}
	if !strings.Contains(source, "logServerReady(logger)") || !strings.Contains(source, `logger.Info("server ready")`) {
		t.Fatal("composition does not use the fixed-value startup readiness log")
	}
	if !strings.Contains(source, "startup.Transfer()") || !strings.Contains(source, "CloseDatabase: database.Close") {
		t.Fatal("composition does not explicitly transfer SQLite ownership to Runtime")
	}
	databaseCleanup := strings.Index(source, "startup.Add(func() { _ = database.Close() })")
	adapterSelection := strings.Index(source, "switch configuration.AgentAdapter")
	ownershipTransfer := strings.LastIndex(source, "startup.Transfer()")
	if databaseCleanup < 0 || adapterSelection < 0 || ownershipTransfer < 0 || databaseCleanup > adapterSelection || adapterSelection > ownershipTransfer {
		t.Fatalf("SQLite startup cleanup/adapter selection/Runtime transfer order is unsafe: db=%d adapter=%d transfer=%d", databaseCleanup, adapterSelection, ownershipTransfer)
	}
	for _, required := range []string{
		"lifecycleGate := devicegate.New()", "DeviceGate: lifecycleGate", "Gate: lifecycleGate",
		"app.AwaitOpenCodeStartup", "BridgeServer: bridgeServer", "CloseBridge: closeBridge",
		"CloseAgent: agentService.Close", "CloseTerminal: terminalHandler.Close", "CloseTunnel: tunnelGateway.Close",
		"agentAdapter = &agent.FakeAdapter{}",
		"deepseek.NewAdapter", "APIKey: configuration.DeepSeekAPIKey", "provider = deepseek.ProviderName",
		"ProviderConfigurator: &deepSeekProviderConfigurator{registry: agentService}",
		"BaseURL: deepseek.DefaultBaseURL", "registry.SetAdapter(agent.ProviderDeepSeek, adapter)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("composition source lacks required wiring %q", required)
		}
	}
	if strings.Count(source, "devicegate.New()") != 1 {
		t.Fatalf("composition must construct exactly one shared Device lifecycle gate; count=%d", strings.Count(source, "devicegate.New()"))
	}
	if strings.Contains(source, "Handle(opencodebridge.CallbackPath") || strings.Contains(source, "Handle(\"/internal/opencode") {
		t.Fatal("composition directly mounted the OpenCode Bridge on a public handler")
	}
}

func TestServerSourceSharesOneRequestSourceResolver(t *testing.T) {
	contents, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	const construction = "sourceResolver := requestsource.New(configuration.TrustedProxyIPs)"
	if strings.Count(source, construction) != 1 {
		t.Fatalf("request source resolver construction count=%d want=1", strings.Count(source, construction))
	}
	const injection = "SourceResolver: sourceResolver"
	if strings.Count(source, injection) != 2 {
		t.Fatalf("shared request source resolver injection count=%d want=2", strings.Count(source, injection))
	}
	if strings.Count(source, "requestsource.New(") != 1 {
		t.Fatalf("composition constructed more than one request source resolver: count=%d", strings.Count(source, "requestsource.New("))
	}

	constructionIndex := strings.Index(source, construction)
	tunnelIndex := strings.Index(source, injection)
	browserIndex := strings.LastIndex(source, injection)
	if constructionIndex < 0 || tunnelIndex <= constructionIndex || browserIndex <= tunnelIndex {
		t.Fatalf("request source construction/injection order is invalid: construction=%d tunnel=%d browser=%d", constructionIndex, tunnelIndex, browserIndex)
	}
}
