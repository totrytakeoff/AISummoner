package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/wsstream"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

type tunnelStoreFake struct {
	mu       sync.Mutex
	devices  map[string]store.Device
	lastSeen map[string]time.Time
}

func (fake *tunnelStoreFake) RegisterDevice(_ context.Context, device store.Device) (store.Device, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if existing, ok := fake.devices[device.ID]; ok {
		if !bytes.Equal(existing.PublicKey, device.PublicKey) {
			return store.Device{}, store.ErrConflict
		}
		device.OwnerUserID = existing.OwnerUserID
		device.CreatedAt = existing.CreatedAt
	}
	fake.devices[device.ID] = device
	return device, nil
}

func (fake *tunnelStoreFake) UpdateDeviceLastSeen(_ context.Context, deviceID string, at time.Time) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.devices[deviceID]; !ok {
		return store.ErrNotFound
	}
	fake.lastSeen[deviceID] = at
	return nil
}

type pairingFake struct {
	mu      sync.Mutex
	devices []string
	code    string
}

func (fake *pairingFake) Offer(_ context.Context, deviceID string, now time.Time) (pairing.Offer, error) {
	fake.mu.Lock()
	fake.devices = append(fake.devices, deviceID)
	fake.mu.Unlock()
	return pairing.Offer{Code: fake.code, ExpiresAt: now.Add(10 * time.Minute)}, nil
}

func TestClientServerHandshakePairingHeartbeatAndOffline(t *testing.T) {
	deviceIdentity := testIdentity(t)
	deviceStore := &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)}
	pairings := &pairingFake{code: "K7HF-92PQ"}
	manager := NewManager()
	gateway, err := NewGateway(GatewayOptions{
		Store: deviceStore, Pairing: pairings, Manager: manager,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()

	pairingReceived := make(chan PairingNotification, 1)
	online := make(chan ClientSession, 1)
	client, err := NewClient(ClientOptions{
		ServerURL: server.URL, DevMode: true, Identity: deviceIdentity,
		DeviceName: "integration-device", Platform: "linux", Arch: "amd64", ClientVersion: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnPairing: func(offer PairingNotification) { pairingReceived <- offer },
		OnOnline: func(session ClientSession) { online <- session }, StableOnline: time.Second,
		Jitter: func(time.Duration) time.Duration { return time.Second },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case session := <-online:
		if session.ConnectionID == "" || session.SSHClientPublicKey == nil {
			t.Fatalf("invalid authenticated session: %+v", session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not authenticate")
	}
	select {
	case offer := <-pairingReceived:
		if offer.Code != pairings.code {
			t.Fatalf("pairing code = %q", offer.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pairing offer not delivered")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		deviceStore.mu.Lock()
		seen := deviceStore.lastSeen[deviceIdentity.DeviceID]
		deviceStore.mu.Unlock()
		if !seen.IsZero() && manager.IsOnline(deviceIdentity.DeviceID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	deviceStore.mu.Lock()
	seen := deviceStore.lastSeen[deviceIdentity.DeviceID]
	registered := deviceStore.devices[deviceIdentity.DeviceID]
	deviceStore.mu.Unlock()
	if seen.IsZero() || !bytes.Equal(registered.PublicKey, deviceIdentity.PublicKey) || !manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatalf("heartbeat/registration state missing: seen=%v registered=%+v online=%v", seen, registered, manager.IsOnline(deviceIdentity.DeviceID))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not stop after cancellation")
	}
	deadline = time.Now().Add(time.Second)
	for manager.IsOnline(deviceIdentity.DeviceID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if manager.IsOnline(deviceIdentity.DeviceID) {
		t.Fatal("device remained online after disconnect")
	}
}

func TestGatewayRejectsMismatchedDeviceIDAndTamperedSignature(t *testing.T) {
	tests := []struct {
		name      string
		mutateID  bool
		tamperSig bool
	}{
		{name: "mismatched device id", mutateID: true},
		{name: "tampered signature", tamperSig: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deviceIdentity := testIdentity(t)
			deviceStore := &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)}
			manager := NewManager()
			gateway, err := NewGateway(GatewayOptions{
				Store: deviceStore, Pairing: &pairingFake{code: "K7HF-92PQ"}, Manager: manager,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AuthTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(gateway)
			defer server.Close()
			defer gateway.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			transport := wsstream.New(ctx, ws)
			defer transport.Close()
			session, err := yamux.Client(transport, yamuxConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			control, err := session.OpenStream()
			if err != nil {
				t.Fatal(err)
			}
			codec := protocol.NewCodec(control)
			requestID := "req_test"
			if err := codec.WriteHeader(protocol.StreamHeader{Version: 1, Kind: protocol.StreamControl, RequestID: requestID}); err != nil {
				t.Fatal(err)
			}
			deviceID := deviceIdentity.DeviceID
			if test.mutateID {
				deviceID = "dev_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if err := codec.WriteMessage(protocol.TypeClientHello, requestID, protocol.ClientHello{
				DeviceID: deviceID, DevicePublicKey: base64.RawURLEncoding.EncodeToString(deviceIdentity.PublicKey),
				DeviceName: "invalid", Platform: "linux", Arch: "amd64", ClientVersion: "test",
			}); err != nil {
				t.Fatal(err)
			}
			if test.mutateID {
				if _, err := codec.ReadMessage(); err == nil {
					t.Fatal("mismatched device id was accepted")
				}
			} else {
				challengeMessage, err := codec.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				var challenge protocol.ServerChallenge
				if err := protocol.DecodePayload(challengeMessage, &challenge); err != nil {
					t.Fatal(err)
				}
				nonce, _ := base64.RawURLEncoding.DecodeString(challenge.Nonce)
				signature, _ := deviceIdentity.Sign(AuthenticationTranscript(nonce, deviceIdentity.PublicKey))
				signature[0] ^= 0xff
				if err := codec.WriteMessage(protocol.TypeDeviceProof, requestID, protocol.DeviceProof{Signature: base64.RawURLEncoding.EncodeToString(signature)}); err != nil {
					t.Fatal(err)
				}
				if _, err := codec.ReadMessage(); err == nil {
					t.Fatal("tampered signature was accepted")
				}
			}
			if manager.IsOnline(deviceIdentity.DeviceID) {
				t.Fatal("invalid peer became online")
			}
		})
	}
}

func testIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	directory := t.TempDir()
	value, err := identity.LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAuthenticationTranscriptExactShape(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{1}, 32)
	got := AuthenticationTranscript(nonce, publicKey)
	want := append(append([]byte(authDomain), nonce...), publicKey...)
	if !bytes.Equal(got, want) {
		t.Fatal("authentication transcript differs")
	}
	derived, err := id.Device(publicKey)
	if err != nil || derived == "" {
		t.Fatalf("device id derivation failed: %v", err)
	}
}

func TestGatewayAuthenticationTimeout(t *testing.T) {
	gateway, err := NewGateway(GatewayOptions{
		Store:   &tunnelStoreFake{devices: make(map[string]store.Device), lastSeen: make(map[string]time.Time)},
		Pairing: &pairingFake{code: "K7HF-92PQ"}, Manager: NewManager(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AuthTimeout: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := wsstream.New(ctx, ws)
	defer transport.Close()
	session, err := yamux.Client(transport, yamuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	_, err = session.OpenStream()
	if err == nil {
		t.Fatal("pre-auth connection remained usable after timeout")
	}
}
