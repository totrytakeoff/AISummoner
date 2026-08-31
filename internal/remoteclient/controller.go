// Package remoteclient owns the long-lived Remote Client state independently
// from any CLI or desktop UI.
package remoteclient

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/clientplatform"
	"github.com/aisummoner/aisummoner/internal/identity"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/tunnel"
)

const MaxEvents = 200

var (
	ErrAlreadyRunning = errors.New("remote client is already running")
	ErrAlreadyStopped = errors.New("remote client has already stopped")
	ErrNotRunning     = errors.New("remote client is not running")
	ErrNoPairingOffer = errors.New("no pairing offer is available")
)

type Phase string

const (
	PhaseStarting   Phase = "starting"
	PhaseConnecting Phase = "connecting"
	PhaseOnline     Phase = "online"
	PhaseRetrying   Phase = "retrying"
	PhasePaused     Phase = "paused"
	PhaseStopped    Phase = "stopped"
	PhaseError      Phase = "error"
)

type Pairing struct {
	Code      string    `json:"code,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Expired   bool      `json:"expired"`
}

type Snapshot struct {
	DeviceID          string     `json:"device_id"`
	DeviceName        string     `json:"device_name"`
	ClientVersion     string     `json:"client_version"`
	ServerOrigin      string     `json:"server_origin"`
	Phase             Phase      `json:"phase"`
	OnlineSince       *time.Time `json:"online_since,omitempty"`
	RetryAt           *time.Time `json:"retry_at,omitempty"`
	Pairing           *Pairing   `json:"pairing,omitempty"`
	ActiveSessions    int        `json:"active_sessions"`
	LastErrorCategory string     `json:"last_error_category,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Event struct {
	Sequence uint64    `json:"sequence"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Level    string    `json:"level"`
	Summary  string    `json:"summary"`
}

type Options struct {
	ServerURL     string
	DataDirectory string
	DeviceName    string
	ClientVersion string
	Development   bool
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

type tunnelRunner interface {
	Run(context.Context) error
}

type Controller struct {
	mu sync.Mutex

	runner tunnelRunner
	now    func() time.Time

	deviceID       string
	deviceName     string
	clientVersion  string
	serverOrigin   string
	phase          Phase
	onlineSince    *time.Time
	retryAt        *time.Time
	pairing        *Pairing
	pairingTimer   *time.Timer
	activeSessions int
	lastError      string
	updatedAt      time.Time

	events   []Event
	sequence uint64

	running       bool
	finished      bool
	desired       bool
	activeCancel  context.CancelFunc
	activeDone    chan struct{}
	wake          chan struct{}
	pairingOffers chan Pairing
}

func New(options Options) (*Controller, error) {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	deviceIdentity, err := identity.LoadOrCreate(options.DataDirectory)
	if err != nil {
		return nil, err
	}
	hostSigner, err := deviceIdentity.SSHSigner()
	if err != nil {
		return nil, err
	}
	sshHandler, err := sshserver.New(hostSigner)
	if err != nil {
		return nil, err
	}
	now := time.Now
	controller := newController(deviceIdentity.DeviceID, options.DeviceName, options.ClientVersion, options.ServerURL, now)
	client, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: options.ServerURL, DevMode: options.Development, Identity: deviceIdentity,
		DeviceName: options.DeviceName, Platform: clientplatform.Current().Name(), Arch: runtime.GOARCH, ClientVersion: options.ClientVersion,
		HTTPClient: options.HTTPClient, Logger: options.Logger,
		OnPairing: controller.observePairing, OnState: controller.observeTunnelState,
		OnStream: controller.observeStream,
		StreamHandler: func(ctx context.Context, stream net.Conn, _ protocol.StreamHeader, session tunnel.ClientSession) {
			_ = sshHandler.Serve(ctx, stream, session.SSHClientPublicKey)
		},
	})
	if err != nil {
		return nil, err
	}
	controller.runner = client
	return controller, nil
}

func newController(deviceID, deviceName, version, serverOrigin string, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	current := now().UTC()
	return &Controller{
		deviceID: deviceID, deviceName: deviceName, clientVersion: version,
		serverOrigin: serverOrigin, phase: PhaseStopped, updatedAt: current,
		now: now, wake: make(chan struct{}, 1), pairingOffers: make(chan Pairing, 1),
		events: make([]Event, 0, MaxEvents),
	}
}

func (controller *Controller) DeviceID() string {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.deviceID
}

// PairingOffers is a best-effort, latest-value notification for the legacy
// foreground CLI. Status consumers should use Snapshot instead. Publishing to
// this channel is non-blocking so stdout/UI backpressure can never hold the
// Tunnel control loop or its shutdown path.
func (controller *Controller) PairingOffers() <-chan Pairing {
	return controller.pairingOffers
}

// Run owns the single Tunnel worker until ctx is canceled. A Controller is
// deliberately single-use; daemon restart constructs a new instance.
func (controller *Controller) Run(ctx context.Context) error {
	controller.mu.Lock()
	if controller.running {
		controller.mu.Unlock()
		return ErrAlreadyRunning
	}
	if controller.finished {
		controller.mu.Unlock()
		return ErrAlreadyStopped
	}
	controller.running = true
	controller.desired = true
	controller.transitionLocked(PhaseStarting, "daemon.started", "info", "Remote service started")
	controller.mu.Unlock()

	defer controller.finish()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		controller.mu.Lock()
		if !controller.desired {
			controller.transitionLocked(PhasePaused, "daemon.paused", "info", "Remote control is paused")
			controller.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil
			case <-controller.wake:
			}
			continue
		}
		runContext, cancelRun := context.WithCancel(ctx)
		runDone := make(chan struct{})
		controller.activeCancel = cancelRun
		controller.activeDone = runDone
		controller.mu.Unlock()

		err := controller.runner.Run(runContext)
		cancelRun()

		controller.mu.Lock()
		if controller.activeDone == runDone {
			controller.activeCancel = nil
			controller.activeDone = nil
		}
		if !controller.desired {
			controller.transitionLocked(PhasePaused, "daemon.paused", "info", "Remote control is paused")
		} else if ctx.Err() == nil && err != nil {
			controller.lastError = "internal_error"
			controller.transitionLocked(PhaseError, "daemon.error", "error", "Remote service needs attention")
		}
		close(runDone)
		desired := controller.desired
		controller.mu.Unlock()

		if ctx.Err() != nil {
			return nil
		}
		if desired && err != nil {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil
			case <-controller.wake:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		}
	}
}

func (controller *Controller) Pause(ctx context.Context) error {
	controller.mu.Lock()
	if !controller.running || controller.finished {
		controller.mu.Unlock()
		return ErrNotRunning
	}
	if !controller.desired {
		done := controller.activeDone
		controller.mu.Unlock()
		return waitFor(ctx, done)
	}
	controller.desired = false
	cancel := controller.activeCancel
	done := controller.activeDone
	if cancel != nil {
		cancel()
	}
	if done == nil {
		controller.transitionLocked(PhasePaused, "daemon.paused", "info", "Remote control is paused")
	}
	controller.mu.Unlock()
	controller.signalWake()
	return waitFor(ctx, done)
}

func (controller *Controller) Resume() error {
	controller.mu.Lock()
	if !controller.running || controller.finished {
		controller.mu.Unlock()
		return ErrNotRunning
	}
	if controller.desired {
		controller.mu.Unlock()
		return nil
	}
	controller.desired = true
	controller.clearPairingLocked()
	controller.recordLocked("daemon.resumed", "info", "Remote control resumed")
	controller.transitionLocked(PhaseStarting, "tunnel.starting", "info", "Preparing connection")
	controller.mu.Unlock()
	controller.signalWake()
	return nil
}

func (controller *Controller) RefreshPairing(ctx context.Context) error {
	controller.mu.Lock()
	if !controller.running || controller.finished {
		controller.mu.Unlock()
		return ErrNotRunning
	}
	if controller.pairing == nil {
		controller.mu.Unlock()
		return ErrNoPairingOffer
	}
	controller.clearPairingLocked()
	controller.recordLocked("pairing.refresh_requested", "info", "A new pairing code was requested")
	controller.desired = true
	cancel := controller.activeCancel
	done := controller.activeDone
	if cancel != nil {
		cancel()
	}
	controller.mu.Unlock()
	controller.signalWake()
	return waitFor(ctx, done)
}

func (controller *Controller) Snapshot() Snapshot {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	result := Snapshot{
		DeviceID: controller.deviceID, DeviceName: controller.deviceName,
		ClientVersion: controller.clientVersion, ServerOrigin: controller.serverOrigin,
		Phase: controller.phase, ActiveSessions: controller.activeSessions,
		LastErrorCategory: controller.lastError, UpdatedAt: controller.updatedAt,
	}
	if controller.onlineSince != nil {
		value := *controller.onlineSince
		result.OnlineSince = &value
	}
	if controller.retryAt != nil {
		value := *controller.retryAt
		result.RetryAt = &value
	}
	if controller.pairing != nil {
		value := *controller.pairing
		result.Pairing = &value
	}
	return result
}

func (controller *Controller) Events(after uint64, limit int) []Event {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if limit < 1 || limit > MaxEvents {
		limit = MaxEvents
	}
	result := make([]Event, 0, limit)
	for _, event := range controller.events {
		if event.Sequence <= after {
			continue
		}
		result = append(result, event)
		if len(result) == limit {
			break
		}
	}
	return result
}

func (controller *Controller) observeTunnelState(notification tunnel.StateNotification) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !controller.running || controller.finished {
		return
	}
	now := controller.now().UTC()
	switch notification.Phase {
	case tunnel.ClientPhaseConnecting:
		controller.clearPairingLocked()
		controller.onlineSince = nil
		controller.retryAt = nil
		controller.transitionLocked(PhaseConnecting, "tunnel.connecting", "info", "Connecting to control service")
	case tunnel.ClientPhaseOnline:
		controller.onlineSince = &now
		controller.retryAt = nil
		controller.lastError = ""
		controller.transitionLocked(PhaseOnline, "tunnel.online", "info", "Connected to control service")
	case tunnel.ClientPhaseRetrying:
		controller.onlineSince = nil
		retryAt := now.Add(notification.RetryIn)
		controller.retryAt = &retryAt
		controller.lastError = notification.ErrorCategory
		controller.transitionLocked(PhaseRetrying, "tunnel.retrying", "warning", "Connection lost; retry scheduled")
	case tunnel.ClientPhaseStopped:
		controller.onlineSince = nil
		controller.retryAt = nil
		if !controller.desired {
			controller.transitionLocked(PhasePaused, "daemon.paused", "info", "Remote control is paused")
		}
	}
}

func (controller *Controller) observePairing(offer tunnel.PairingNotification) {
	now := controller.now().UTC()
	value := &Pairing{Code: offer.Code, ExpiresAt: offer.ExpiresAt.UTC()}
	controller.mu.Lock()
	if controller.finished {
		controller.mu.Unlock()
		return
	}
	controller.clearPairingLocked()
	controller.pairing = value
	controller.updatedAt = now
	controller.recordLocked("pairing.available", "info", "A pairing code is available")
	delay := value.ExpiresAt.Sub(now)
	if delay < 0 {
		delay = 0
	}
	expiresAt := value.ExpiresAt
	controller.pairingTimer = time.AfterFunc(delay, func() {
		controller.expirePairing(expiresAt)
	})
	controller.publishPairingLocked(*value)
	controller.mu.Unlock()
}

func (controller *Controller) expirePairing(expiresAt time.Time) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.finished || controller.pairing == nil || !controller.pairing.ExpiresAt.Equal(expiresAt) || controller.pairing.Expired {
		return
	}
	controller.pairing.Code = ""
	controller.pairing.Expired = true
	controller.pairingTimer = nil
	controller.updatedAt = controller.now().UTC()
	controller.recordLocked("pairing.expired", "warning", "The pairing code expired")
}

func (controller *Controller) observeStream(notification tunnel.StreamNotification) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.finished {
		return
	}
	if notification.Opened {
		controller.activeSessions++
		controller.recordLocked("control_session.started", "info", "A remote control session started")
		return
	}
	if controller.activeSessions == 0 {
		return
	}
	controller.activeSessions--
	controller.recordLocked("control_session.ended", "info", "A remote control session ended")
}

func (controller *Controller) finish() {
	controller.mu.Lock()
	if controller.pairingTimer != nil {
		controller.pairingTimer.Stop()
		controller.pairingTimer = nil
	}
	controller.running = false
	controller.finished = true
	controller.desired = false
	controller.activeCancel = nil
	controller.activeDone = nil
	controller.activeSessions = 0
	controller.onlineSince = nil
	controller.retryAt = nil
	controller.clearPairingLocked()
	controller.transitionLocked(PhaseStopped, "daemon.stopped", "info", "Remote service stopped")
	controller.mu.Unlock()
}

func (controller *Controller) transitionLocked(phase Phase, kind, level, summary string) {
	if controller.phase == phase {
		return
	}
	controller.phase = phase
	controller.updatedAt = controller.now().UTC()
	controller.recordLocked(kind, level, summary)
}

func (controller *Controller) recordLocked(kind, level, summary string) {
	controller.sequence++
	event := Event{Sequence: controller.sequence, At: controller.now().UTC(), Kind: kind, Level: level, Summary: summary}
	if len(controller.events) == MaxEvents {
		copy(controller.events, controller.events[1:])
		controller.events[len(controller.events)-1] = event
	} else {
		controller.events = append(controller.events, event)
	}
}

func (controller *Controller) clearPairingLocked() {
	if controller.pairingTimer != nil {
		controller.pairingTimer.Stop()
		controller.pairingTimer = nil
	}
	controller.pairing = nil
}

func (controller *Controller) publishPairingLocked(offer Pairing) {
	select {
	case controller.pairingOffers <- offer:
		return
	default:
	}
	select {
	case <-controller.pairingOffers:
	default:
	}
	select {
	case controller.pairingOffers <- offer:
	default:
	}
}

func (controller *Controller) signalWake() {
	select {
	case controller.wake <- struct{}{}:
	default:
	}
}

func waitFor(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
