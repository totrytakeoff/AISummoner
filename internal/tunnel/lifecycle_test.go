package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/device"
	"github.com/aisummoner/aisummoner/internal/devicegate"
	"github.com/aisummoner/aisummoner/internal/httpapi"
	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/wsstream"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

func TestGatewayMissedHeartbeatTimesOutWithoutPeerCancellation(t *testing.T) {
	deviceIdentity := testIdentity(t)
	manager := NewManager()
	gateway := newLifecycleGateway(t, manager, 10*time.Millisecond, 60*time.Millisecond, GatewayOptions{})
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	peer := authenticateManualPeer(t, server.URL, deviceIdentity)
	defer peer.Close()
	eventually(t, time.Second, func() bool { return manager.IsOnline(deviceIdentity.DeviceID) }, "authenticated peer was not published online")
	eventually(t, time.Second, func() bool { return !manager.IsOnline(deviceIdentity.DeviceID) }, "missed heartbeat did not mark device offline")
}

func TestGatewayAuthenticatedNewestWinsAndOldCleanupKeepsNew(t *testing.T) {
	deviceIdentity := testIdentity(t)
	manager := NewManager()
	gateway := newLifecycleGateway(t, manager, 10*time.Millisecond, time.Second, GatewayOptions{})
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	first := authenticateManualPeer(t, server.URL, deviceIdentity)
	defer first.Close()
	eventually(t, time.Second, func() bool { return manager.IsOnline(deviceIdentity.DeviceID) }, "first authenticated connection was not published")
	firstConnection, ok := manager.Get(deviceIdentity.DeviceID)
	if !ok || firstConnection.ID != first.sessionInfo.ConnectionID {
		t.Fatal("first authenticated connection was not installed")
	}
	second := authenticateManualPeer(t, server.URL, deviceIdentity)
	defer second.Close()
	eventually(t, time.Second, func() bool {
		value, exists := manager.Get(deviceIdentity.DeviceID)
		return exists && value.ID == second.sessionInfo.ConnectionID
	}, "second authenticated connection was not published")
	secondConnection, ok := manager.Get(deviceIdentity.DeviceID)
	if !ok || secondConnection.ID != second.sessionInfo.ConnectionID || secondConnection == firstConnection {
		t.Fatal("authenticated replacement was not installed")
	}
	select {
	case <-firstConnection.Done():
	case <-time.After(time.Second):
		t.Fatal("newest-wins did not close the old authenticated connection")
	}
	// Give the old Gateway handler time to run its exact-instance Remove.
	time.Sleep(20 * time.Millisecond)
	current, ok := manager.Get(deviceIdentity.DeviceID)
	if !ok || current != secondConnection {
		t.Fatal("old handler cleanup removed the replacement connection")
	}
	second.Close()
	eventually(t, time.Second, func() bool { return !manager.IsOnline(deviceIdentity.DeviceID) }, "closing current peer did not mark it offline")
}

func TestGatewayPreAuthLimitsAndSlotRelease(t *testing.T) {
	manager := NewManager()
	gateway := newLifecycleGateway(t, manager, 10*time.Millisecond, time.Second, GatewayOptions{
		MaxPreAuth: 1, PreAuthPerMinute: 100, AuthTimeout: time.Second,
	})
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	idle := openIdleTunnel(t, server.URL)
	_, response, err := websocket.Dial(context.Background(), websocketURL(server.URL), nil)
	if err == nil {
		t.Fatal("global pre-auth limit accepted a second unauthenticated tunnel")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("global-limit response = %#v, error=%v", response, err)
	}
	closeResponse(response)
	idle.Close()

	var admitted *idleTunnel
	eventually(t, time.Second, func() bool {
		candidate, dialResponse, dialErr := tryOpenIdleTunnel(server.URL)
		closeResponse(dialResponse)
		if dialErr != nil {
			return false
		}
		admitted = candidate
		return true
	}, "pre-auth slot was not released after disconnect")
	admitted.Close()
	eventually(t, time.Second, func() bool { return len(gateway.preAuth) == 0 }, "admitted idle tunnel did not release its slot")

	peer := authenticateManualPeer(t, server.URL, testIdentity(t))
	defer peer.Close()
	var postAuth *idleTunnel
	eventually(t, time.Second, func() bool {
		candidate, dialResponse, dialErr := tryOpenIdleTunnel(server.URL)
		closeResponse(dialResponse)
		if dialErr != nil {
			return false
		}
		postAuth = candidate
		return true
	}, "successful authentication did not release its pre-auth slot")
	postAuth.Close()
}

func TestGatewayPreAuthSourceLimit(t *testing.T) {
	gateway := newLifecycleGateway(t, NewManager(), 10*time.Millisecond, time.Second, GatewayOptions{
		MaxPreAuth: 4, PreAuthPerMinute: 1,
	})
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	_, dialResponse, dialErr := websocket.Dial(context.Background(), websocketURL(server.URL), nil)
	if dialErr == nil {
		t.Fatal("per-source pre-auth limit accepted a repeated attempt")
	}
	if dialResponse == nil || dialResponse.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("source-limit response = %#v, error=%v", dialResponse, dialErr)
	}
	closeResponse(dialResponse)
}

func TestRejectedProofHasNoPublicationThenRealPairingClaimShowsOnline(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "tunnel-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.NewService(database)
	if _, created, err := authService.Bootstrap(ctx, "test-admin-password", time.Now()); err != nil || !created {
		t.Fatalf("bootstrap admin: created=%v err=%v", created, err)
	}
	pairingService, err := pairing.NewService(database, bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	countingPairing := &countingPairingOfferer{service: pairingService}
	manager := NewManager()
	gateway, err := NewGateway(GatewayOptions{
		Store: database, Pairing: countingPairing, Manager: manager,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	api, err := httpapi.New(httpapi.Options{
		Auth: authService, Pairing: pairingService, Devices: device.NewService(database, manager),
		AllowedOrigin: "http://aisummoner.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/tunnel", gateway)
	mux.Handle("/", api.Handler())
	server := httptest.NewServer(mux)
	defer server.Close()

	deviceIdentity := testIdentity(t)
	rejectManualPeer(t, server.URL, deviceIdentity)
	if _, err := database.DeviceByID(ctx, deviceIdentity.DeviceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rejected proof registration result = %v", err)
	}
	if countingPairing.Count() != 0 || manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatalf("rejected proof published pairing/online: offers=%d online=%v", countingPairing.Count(), manager.IsOnline(deviceIdentity.DeviceID))
	}

	peer := authenticateManualPeer(t, server.URL, deviceIdentity)
	defer peer.Close()
	offerMessage, err := peer.codec.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if offerMessage.Type != protocol.TypePairingOffered {
		t.Fatalf("message type = %q, want pairing offer", offerMessage.Type)
	}
	var offer protocol.PairingOffered
	if err := protocol.DecodePayload(offerMessage, &offer); err != nil {
		t.Fatal(err)
	}
	if countingPairing.Count() != 1 {
		t.Fatalf("accepted authentication pairing offers = %d, want 1", countingPairing.Count())
	}

	cookie := loginForTunnelAPI(t, server.URL, "http://aisummoner.test")
	claimBody, _ := json.Marshal(map[string]string{"code": offer.Code})
	claim := apiRequest(t, http.MethodPost, server.URL+"/api/v1/pairings/claim", claimBody, cookie, "http://aisummoner.test")
	if claim.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(claim.Body)
		claim.Body.Close()
		t.Fatalf("pairing claim status=%d body=%s", claim.StatusCode, contents)
	}
	claim.Body.Close()
	detail := apiRequest(t, http.MethodGet, server.URL+"/api/v1/devices/"+deviceIdentity.DeviceID, nil, cookie, "")
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(detail.Body)
		t.Fatalf("device detail status=%d body=%s", detail.StatusCode, contents)
	}
	var responseBody struct {
		Device struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		} `json:"device"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&responseBody); err != nil {
		t.Fatal(err)
	}
	if responseBody.Device.ID != deviceIdentity.DeviceID || !responseBody.Device.Online {
		t.Fatalf("owner device response = %+v", responseBody.Device)
	}
}

func TestGatewayFirstPublicationThenUnpairDetachesAndNextConnectionGetsFreshOffer(t *testing.T) {
	ctx := context.Background()
	deviceIdentity := testIdentity(t)
	database, ownerID := lifecycleDatabase(t, deviceIdentity, true)
	pairingService, err := pairing.NewService(database, bytes.Repeat([]byte{0x62}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pairings := &countingPairingOfferer{service: pairingService}
	manager := NewManager()
	oldConnection := newConnection("conn_old", deviceIdentity.DeviceID, nil, nil, nil, nil, time.Now())
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old Connection was rejected")
	}
	gate := &observedLifecycleGate{inner: devicegate.New(), attempts: make(chan string, 8)}
	gateway, err := NewGateway(GatewayOptions{
		Store: database, Pairing: pairings, Manager: manager, DeviceGate: gate,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HeartbeatInterval: 20 * time.Millisecond,
		HeartbeatTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishHeld := make(chan struct{})
	releasePublish := make(chan struct{})
	var publishOnce sync.Once
	gateway.beforePublish = func() {
		publishOnce.Do(func() {
			close(publishHeld)
			<-releasePublish
		})
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	deviceService := newTunnelLifecycleDeviceService(t, database, manager, gate)
	peer, challenge, requestID := beginManualAuthentication(t, server.URL, deviceIdentity)
	defer peer.Close()
	sendManualProof(t, peer, deviceIdentity, challenge, requestID)
	<-publishHeld
	if attempted := <-gate.attempts; attempted != deviceIdentity.DeviceID {
		t.Fatalf("Gateway gate attempt = %q", attempted)
	}
	message, err := peer.codec.ReadMessage()
	if err != nil || message.Type != protocol.TypeServerAuthenticated {
		t.Fatalf("pre-publication response type=%q err=%v", message.Type, err)
	}
	current, ok := manager.Get(deviceIdentity.DeviceID)
	if !ok || current != oldConnection {
		t.Fatal("candidate became current before the publication barrier")
	}

	unpaired := make(chan error, 1)
	go func() { unpaired <- deviceService.Unpair(ctx, ownerID, deviceIdentity.DeviceID, time.Now().UTC()) }()
	if attempted := <-gate.attempts; attempted != deviceIdentity.DeviceID {
		t.Fatalf("Unpair gate attempt = %q", attempted)
	}
	if _, err := database.DeviceByOwner(ctx, ownerID, deviceIdentity.DeviceID); err != nil {
		t.Fatalf("Unpair committed while candidate still held the Device gate: %v", err)
	}
	close(releasePublish)
	if err := <-unpaired; err != nil {
		t.Fatal(err)
	}
	if manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("Unpair did not detach the just-published candidate")
	}
	select {
	case <-oldConnection.Done():
	default:
		t.Fatal("candidate publication did not retire the old Connection")
	}
	if pairings.Count() != 0 {
		t.Fatalf("owned publication emitted %d pairing offers", pairings.Count())
	}

	next := authenticateManualPeer(t, server.URL, deviceIdentity)
	defer next.Close()
	offerMessage, err := next.codec.ReadMessage()
	if err != nil || offerMessage.Type != protocol.TypePairingOffered {
		t.Fatalf("next message type=%q err=%v, want pairing offer", offerMessage.Type, err)
	}
	var offer protocol.PairingOffered
	if err := protocol.DecodePayload(offerMessage, &offer); err != nil {
		t.Fatal(err)
	}
	if offer.Code == "" || pairings.Count() != 1 {
		t.Fatalf("fresh pairing offer=%q count=%d", offer.Code, pairings.Count())
	}
	eventually(t, time.Second, func() bool { return manager.IsOnline(deviceIdentity.DeviceID) }, "unowned replacement was not published after its offer")
}

func TestUnpairFirstCandidateRereadsUnownedSendsOfferThenPublishes(t *testing.T) {
	ctx := context.Background()
	deviceIdentity := testIdentity(t)
	database, ownerID := lifecycleDatabase(t, deviceIdentity, true)
	blockingStore := &unpairCommitBarrierStore{
		Store: database, committed: make(chan struct{}), release: make(chan struct{}),
	}
	pairingService, err := pairing.NewService(database, bytes.Repeat([]byte{0x63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	blockedPairing := &pairingOfferBarrier{
		service: pairingService, entered: make(chan struct{}), release: make(chan struct{}),
	}
	manager := NewManager()
	oldClose := &blockingCloseConn{started: make(chan struct{}), release: make(chan struct{})}
	oldConnection := newConnection("conn_old", deviceIdentity.DeviceID, nil, nil, oldClose, nil, time.Now())
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old Connection was rejected")
	}
	gate := &observedLifecycleGate{inner: devicegate.New(), attempts: make(chan string, 8)}
	gateway, err := NewGateway(GatewayOptions{
		Store: blockingStore, Pairing: blockedPairing, Manager: manager, DeviceGate: gate,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HeartbeatInterval: 20 * time.Millisecond,
		HeartbeatTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	publishHeld := make(chan struct{})
	releasePublish := make(chan struct{})
	gateway.beforePublish = func() { close(publishHeld); <-releasePublish }
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()
	deviceService := newTunnelLifecycleDeviceService(t, blockingStore, manager, gate)

	unpaired := make(chan error, 1)
	go func() { unpaired <- deviceService.Unpair(ctx, ownerID, deviceIdentity.DeviceID, time.Now().UTC()) }()
	if attempted := <-gate.attempts; attempted != deviceIdentity.DeviceID {
		t.Fatalf("Unpair gate attempt = %q", attempted)
	}
	<-blockingStore.committed
	if _, err := database.DeviceByOwner(ctx, ownerID, deviceIdentity.DeviceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Unpair transaction did not commit before barrier: %v", err)
	}

	peer, challenge, requestID := beginManualAuthentication(t, server.URL, deviceIdentity)
	defer peer.Close()
	sendManualProof(t, peer, deviceIdentity, challenge, requestID)
	if attempted := <-gate.attempts; attempted != deviceIdentity.DeviceID {
		t.Fatalf("candidate gate attempt = %q", attempted)
	}
	close(blockingStore.release)
	<-oldClose.started
	<-blockedPairing.entered
	message, err := peer.codec.ReadMessage()
	if err != nil || message.Type != protocol.TypeServerAuthenticated {
		t.Fatalf("first post-unpair response type=%q err=%v", message.Type, err)
	}
	if manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("candidate became online before pairing offer completed")
	}
	offerRead := make(chan protocol.Message, 1)
	offerReadErr := make(chan error, 1)
	go func() {
		value, readErr := peer.codec.ReadMessage()
		if readErr != nil {
			offerReadErr <- readErr
			return
		}
		offerRead <- value
	}()
	select {
	case <-offerRead:
		t.Fatal("pairing offer was delivered before Offer completed")
	case err := <-offerReadErr:
		t.Fatalf("pairing stream failed before Offer completed: %v", err)
	default:
	}
	close(blockedPairing.release)
	select {
	case offerMessage := <-offerRead:
		if offerMessage.Type != protocol.TypePairingOffered {
			t.Fatalf("second post-unpair response type=%q", offerMessage.Type)
		}
	case err := <-offerReadErr:
		t.Fatal(err)
	}
	<-publishHeld
	if manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("candidate became online before the final publication boundary")
	}
	close(releasePublish)
	eventually(t, time.Second, func() bool { return manager.IsOnline(deviceIdentity.DeviceID) }, "candidate was not published after authenticated and pairing.offered")
	select {
	case err := <-unpaired:
		t.Fatalf("Unpair returned before detached Tunnel cleanup joined: %v", err)
	default:
	}
	close(oldClose.release)
	if err := <-unpaired; err != nil {
		t.Fatal(err)
	}
	if !manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("late detached cleanup removed the replacement Connection")
	}
	select {
	case <-oldConnection.Done():
	default:
		t.Fatal("Unpair did not detach the old Connection before candidate publication")
	}
}

func TestPairingOfferFailureNeverPublishesOrReplacesHealthyConnection(t *testing.T) {
	deviceIdentity := testIdentity(t)
	database, _ := lifecycleDatabase(t, deviceIdentity, false)
	manager := NewManager()
	healthy := newConnection("conn_healthy", deviceIdentity.DeviceID, nil, nil, nil, nil, time.Now())
	if _, accepted := manager.Register(healthy); !accepted {
		t.Fatal("healthy Connection was rejected")
	}
	gateway, err := NewGateway(GatewayOptions{
		Store: database, Pairing: pairingOfferError{err: errors.New("offer failed")}, Manager: manager,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HeartbeatInterval: 20 * time.Millisecond,
		HeartbeatTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()
	peer, challenge, requestID := beginManualAuthentication(t, server.URL, deviceIdentity)
	defer peer.Close()
	sendManualProof(t, peer, deviceIdentity, challenge, requestID)
	message, err := peer.codec.ReadMessage()
	if err != nil || message.Type != protocol.TypeServerAuthenticated {
		t.Fatalf("authenticated response type=%q err=%v", message.Type, err)
	}
	if _, err := peer.codec.ReadMessage(); err == nil {
		t.Fatal("offer failure left the candidate stream active")
	}
	current, ok := manager.Get(deviceIdentity.DeviceID)
	if !ok || current != healthy {
		t.Fatal("offer failure replaced the healthy Connection")
	}
	select {
	case <-healthy.Done():
		t.Fatal("offer failure closed the healthy Connection")
	default:
	}
}

func TestGatewayCloseJoinsBlockedHandlersAndRejectsNewAdmissions(t *testing.T) {
	deviceIdentity := testIdentity(t)
	gate := &blockingLifecycleGate{entered: make(chan struct{}), release: make(chan struct{})}
	gateway := newLifecycleGateway(t, NewManager(), 20*time.Millisecond, 5*time.Second, GatewayOptions{DeviceGate: gate})
	closedBoundary := make(chan struct{})
	gateway.afterClosed = func() { close(closedBoundary) }
	extraWatcherEntered := make(chan struct{})
	extraWatcherRelease := make(chan struct{})
	var extraWatcherOnce sync.Once
	gateway.beforeExtraWatcherExit = func() {
		extraWatcherOnce.Do(func() { close(extraWatcherEntered) })
		<-extraWatcherRelease
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	idle := openIdleTunnel(t, server.URL)
	defer idle.Close()
	peer, challenge, requestID := beginManualAuthentication(t, server.URL, deviceIdentity)
	defer peer.Close()
	sendManualProof(t, peer, deviceIdentity, challenge, requestID)
	<-gate.entered

	firstClosed := make(chan struct{})
	secondClosed := make(chan struct{})
	go func() { gateway.Close(); close(firstClosed) }()
	go func() { gateway.Close(); close(secondClosed) }()
	<-closedBoundary
	select {
	case <-firstClosed:
		t.Fatal("Gateway.Close returned while an admitted handler remained blocked")
	case <-secondClosed:
		t.Fatal("concurrent Gateway.Close returned before the shared completion boundary")
	default:
	}
	close(gate.release)
	select {
	case <-extraWatcherEntered:
	case <-time.After(time.Second):
		t.Fatal("extra-stream watcher did not reach its exit barrier")
	}
	select {
	case <-firstClosed:
		t.Fatal("Gateway.Close returned without joining the extra-stream watcher")
	case <-secondClosed:
		t.Fatal("concurrent Gateway.Close returned without joining the extra-stream watcher")
	default:
	}
	close(extraWatcherRelease)
	select {
	case <-firstClosed:
	case <-time.After(time.Second):
		t.Fatal("Gateway.Close did not join the blocked handler")
	}
	select {
	case <-secondClosed:
	case <-time.After(time.Second):
		t.Fatal("concurrent Gateway.Close did not observe shared completion")
	}
	if got := len(gateway.preAuth); got != 0 {
		t.Fatalf("Gateway.Close left %d pre-auth slots", got)
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), time.Second)
	defer cancelDial()
	_, response, err := websocket.Dial(dialCtx, websocketURL(server.URL), nil)
	if err == nil {
		t.Fatal("closed Gateway admitted a new WebSocket")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("post-close response=%#v error=%v", response, err)
	}
	closeResponse(response)
}

type observedLifecycleGate struct {
	inner    *devicegate.Gate
	attempts chan string
}

func (gate *observedLifecycleGate) LockDevice(ctx context.Context, deviceID string) (func(), error) {
	gate.attempts <- deviceID
	return gate.inner.LockDevice(ctx, deviceID)
}

type blockingLifecycleGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (gate *blockingLifecycleGate) LockDevice(context.Context, string) (func(), error) {
	gate.once.Do(func() { close(gate.entered) })
	<-gate.release
	return func() {}, nil
}

type unpairCommitBarrierStore struct {
	*store.Store
	committed chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (value *unpairCommitBarrierStore) UnpairDevice(ctx context.Context, ownerUserID, deviceID string, now time.Time) (store.UnpairResult, error) {
	result, err := value.Store.UnpairDevice(ctx, ownerUserID, deviceID, now)
	if err != nil {
		return result, err
	}
	value.once.Do(func() { close(value.committed) })
	<-value.release
	return result, nil
}

type pairingOfferBarrier struct {
	service *pairing.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (value *pairingOfferBarrier) Offer(ctx context.Context, deviceID string, now time.Time) (pairing.Offer, error) {
	offer, err := value.service.Offer(ctx, deviceID, now)
	if err != nil {
		return pairing.Offer{}, err
	}
	value.once.Do(func() { close(value.entered) })
	<-value.release
	return offer, nil
}

type pairingOfferError struct{ err error }

func (value pairingOfferError) Offer(context.Context, string, time.Time) (pairing.Offer, error) {
	return pairing.Offer{}, value.err
}

type tunnelLifecycleTerminal struct{}

func (tunnelLifecycleTerminal) CancelDevice(string) {}

type tunnelLifecycleAgent struct{}

func (tunnelLifecycleAgent) MarkDeviceRevoked(string, []string)                       {}
func (tunnelLifecycleAgent) InvalidateDevice(context.Context, string, []string) error { return nil }

func lifecycleDatabase(t *testing.T, deviceIdentity *identity.Identity, owned bool) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ownerID := "usr_lifecycle"
	if _, _, err := database.BootstrapAdmin(ctx, ownerID, "admin", "test-phc", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var owner *string
	if owned {
		owner = &ownerID
	}
	if _, err := database.RegisterDevice(ctx, store.Device{
		ID: deviceIdentity.DeviceID, PublicKey: append([]byte(nil), deviceIdentity.PublicKey...), OwnerUserID: owner,
		Name: "lifecycle-device", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return database, ownerID
}

func newTunnelLifecycleDeviceService(t *testing.T, database device.LifecycleStore, manager *Manager, gate device.LifecycleGate) *device.Service {
	t.Helper()
	service, err := device.NewLifecycleService(device.LifecycleOptions{
		Store: database, Online: manager, Gate: gate, Tunnel: manager,
		Terminal: tunnelLifecycleTerminal{}, Agent: tunnelLifecycleAgent{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CleanupTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func sendManualProof(t *testing.T, peer *manualPeer, deviceIdentity *identity.Identity, challenge []byte, requestID string) {
	t.Helper()
	signature, err := deviceIdentity.Sign(AuthenticationTranscript(challenge, deviceIdentity.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.codec.WriteMessage(protocol.TypeDeviceProof, requestID, protocol.DeviceProof{
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		t.Fatal(err)
	}
}

type manualPeer struct {
	cancel      context.CancelFunc
	transport   *wsstream.Conn
	session     *yamux.Session
	control     netConn
	codec       *protocol.Codec
	sessionInfo protocol.ServerAuthenticated
	closeOnce   sync.Once
}

// netConn is the subset needed here and keeps the helper independent of yamux's concrete stream type.
type netConn interface {
	io.ReadWriteCloser
}

func authenticateManualPeer(t *testing.T, serverURL string, deviceIdentity *identity.Identity) *manualPeer {
	t.Helper()
	peer, challenge, requestID := beginManualAuthentication(t, serverURL, deviceIdentity)
	signature, err := deviceIdentity.Sign(AuthenticationTranscript(challenge, deviceIdentity.PublicKey))
	if err != nil {
		peer.Close()
		t.Fatal(err)
	}
	if err := peer.codec.WriteMessage(protocol.TypeDeviceProof, requestID, protocol.DeviceProof{
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	message, err := peer.codec.ReadMessage()
	if err != nil {
		peer.Close()
		t.Fatal(err)
	}
	if message.Type != protocol.TypeServerAuthenticated {
		peer.Close()
		t.Fatalf("message type = %q, want authenticated", message.Type)
	}
	if err := protocol.DecodePayload(message, &peer.sessionInfo); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool {
		return peer.sessionInfo.ConnectionID != ""
	}, "authenticated response was empty")
	return peer
}

func rejectManualPeer(t *testing.T, serverURL string, deviceIdentity *identity.Identity) {
	t.Helper()
	peer, challenge, requestID := beginManualAuthentication(t, serverURL, deviceIdentity)
	defer peer.Close()
	signature, err := deviceIdentity.Sign(AuthenticationTranscript(challenge, deviceIdentity.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	signature[0] ^= 0xff
	if err := peer.codec.WriteMessage(protocol.TypeDeviceProof, requestID, protocol.DeviceProof{
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.codec.ReadMessage(); err == nil {
		t.Fatal("tampered proof received an authenticated response")
	}
}

func beginManualAuthentication(t *testing.T, serverURL string, deviceIdentity *identity.Identity) (*manualPeer, []byte, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dialContext, cancelDial := context.WithTimeout(ctx, time.Second)
	websocketConn, _, err := websocket.Dial(dialContext, websocketURL(serverURL), nil)
	cancelDial()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	transport := wsstream.New(ctx, websocketConn)
	session, err := yamux.Client(transport, yamuxConfig())
	if err != nil {
		transport.Close()
		cancel()
		t.Fatal(err)
	}
	control, err := session.OpenStream()
	if err != nil {
		session.Close()
		transport.Close()
		cancel()
		t.Fatal(err)
	}
	peer := &manualPeer{cancel: cancel, transport: transport, session: session, control: control, codec: protocol.NewCodec(control)}
	requestID := "req_manual"
	if err := peer.codec.WriteHeader(protocol.StreamHeader{Version: protocol.Version, Kind: protocol.StreamControl, RequestID: requestID}); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	if err := peer.codec.WriteMessage(protocol.TypeClientHello, requestID, protocol.ClientHello{
		DeviceID: deviceIdentity.DeviceID, DevicePublicKey: base64.RawURLEncoding.EncodeToString(deviceIdentity.PublicKey),
		DeviceName: "manual-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
	}); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	message, err := peer.codec.ReadMessage()
	if err != nil {
		peer.Close()
		t.Fatal(err)
	}
	var challenge protocol.ServerChallenge
	if err := protocol.DecodePayload(message, &challenge); err != nil {
		peer.Close()
		t.Fatal(err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Nonce)
	if err != nil || len(nonce) != 32 {
		peer.Close()
		t.Fatalf("challenge nonce invalid: %v", err)
	}
	return peer, nonce, requestID
}

func (peer *manualPeer) SendHeartbeat(t *testing.T) {
	t.Helper()
	requestID := "req_heartbeat"
	if err := peer.codec.WriteMessage(protocol.TypeDeviceHeartbeat, requestID, protocol.DeviceHeartbeat{SentAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	message, err := peer.codec.ReadMessage()
	if err != nil || message.Type != protocol.TypeHeartbeatAck {
		t.Fatalf("heartbeat acknowledgement: type=%q err=%v", message.Type, err)
	}
}

func (peer *manualPeer) Close() {
	peer.closeOnce.Do(func() {
		if peer.control != nil {
			peer.control.Close()
		}
		if peer.session != nil {
			peer.session.Close()
		}
		if peer.transport != nil {
			peer.transport.Close()
		}
		if peer.cancel != nil {
			peer.cancel()
		}
	})
}

type idleTunnel struct {
	cancel    context.CancelFunc
	transport *wsstream.Conn
	session   *yamux.Session
}

func openIdleTunnel(t *testing.T, serverURL string) *idleTunnel {
	t.Helper()
	value, response, err := tryOpenIdleTunnel(serverURL)
	closeResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func tryOpenIdleTunnel(serverURL string) (*idleTunnel, *http.Response, error) {
	ctx, cancel := context.WithCancel(context.Background())
	websocketConn, response, err := websocket.Dial(ctx, websocketURL(serverURL), nil)
	if err != nil {
		cancel()
		return nil, response, err
	}
	transport := wsstream.New(ctx, websocketConn)
	session, err := yamux.Client(transport, yamuxConfig())
	if err != nil {
		transport.Close()
		cancel()
		return nil, response, err
	}
	return &idleTunnel{cancel: cancel, transport: transport, session: session}, response, nil
}

func (tunnel *idleTunnel) Close() {
	if tunnel == nil {
		return
	}
	tunnel.session.Close()
	tunnel.transport.Close()
	tunnel.cancel()
}

func newLifecycleGateway(t *testing.T, manager *Manager, heartbeatInterval, heartbeatTimeout time.Duration, overrides GatewayOptions) *Gateway {
	t.Helper()
	options := overrides
	options.Store = &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)}
	options.Pairing = &pairingFake{code: "K7HF-92PQ"}
	options.Manager = manager
	options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	options.HeartbeatInterval = heartbeatInterval
	options.HeartbeatTimeout = heartbeatTimeout
	gateway, err := NewGateway(options)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

type countingPairingOfferer struct {
	service *pairing.Service
	mu      sync.Mutex
	count   int
}

func (offerer *countingPairingOfferer) Offer(ctx context.Context, deviceID string, now time.Time) (pairing.Offer, error) {
	offerer.mu.Lock()
	offerer.count++
	offerer.mu.Unlock()
	return offerer.service.Offer(ctx, deviceID, now)
}

func (offerer *countingPairingOfferer) Count() int {
	offerer.mu.Lock()
	defer offerer.mu.Unlock()
	return offerer.count
}

func loginForTunnelAPI(t *testing.T, serverURL, origin string) *http.Cookie {
	t.Helper()
	response := apiRequest(t, http.MethodPost, serverURL+"/api/v1/auth/login", []byte(`{"username":"admin","password":"test-admin-password"}`), nil, origin)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("login status=%d body=%s", response.StatusCode, contents)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == httpapi.SessionCookieName {
			return cookie
		}
	}
	t.Fatal("login did not return session cookie")
	return nil
}

func apiRequest(t *testing.T, method, target string, body []byte, cookie *http.Cookie, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func eventually(t *testing.T, timeout time.Duration, predicate func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !predicate() {
		t.Fatal(message)
	}
}

func websocketURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/tunnel"
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
}
