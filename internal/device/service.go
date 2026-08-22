// Package device exposes ownership-checked device views to product APIs.
package device

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/store"
)

var ErrLifecycleUnavailable = errors.New("device lifecycle invalidation is not configured")

type Store interface {
	DevicesByOwner(context.Context, string) ([]store.Device, error)
	DeviceByOwner(context.Context, string, string) (store.Device, error)
}

type LifecycleStore interface {
	Store
	UnpairDevice(context.Context, string, string, time.Time) (store.UnpairResult, error)
}

// OnlineState is implemented by task002's in-memory Connection Manager.
type OnlineState interface {
	IsOnline(deviceID string) bool
}

type OfflineState struct{}

func (OfflineState) IsOnline(string) bool { return false }

type Service struct {
	store     Store
	online    OnlineState
	lifecycle *lifecycle
}

type LifecycleGate interface {
	LockDevice(context.Context, string) (func(), error)
}

type TunnelInvalidator interface {
	DetachDevice(deviceID string) func() error
}

type TerminalInvalidator interface {
	CancelDevice(deviceID string)
}

type AgentInvalidator interface {
	MarkDeviceRevoked(deviceID string, revokedSessionIDs []string)
	InvalidateDevice(context.Context, string, []string) error
}

type LifecycleOptions struct {
	Store          LifecycleStore
	Online         OnlineState
	Gate           LifecycleGate
	Tunnel         TunnelInvalidator
	Terminal       TerminalInvalidator
	Agent          AgentInvalidator
	Logger         *slog.Logger
	CleanupTimeout time.Duration
}

type lifecycle struct {
	store          LifecycleStore
	gate           LifecycleGate
	tunnel         TunnelInvalidator
	terminal       TerminalInvalidator
	agent          AgentInvalidator
	logger         *slog.Logger
	cleanupTimeout time.Duration

	// afterCleanupReport is a package-private test observation point after a
	// worker has delivered its result. Production services leave it nil.
	afterCleanupReport func(string)
}

type View struct {
	Device store.Device
	Online bool
}

func NewService(store Store, online OnlineState) *Service {
	if online == nil {
		online = OfflineState{}
	}
	return &Service{store: store, online: online}
}

// NewLifecycleService is the production constructor for a Device Service that
// can authorize and fully invalidate unpair. NewService remains the narrow
// List/Get constructor for standalone package users and rejects Unpair.
func NewLifecycleService(options LifecycleOptions) (*Service, error) {
	if options.Store == nil || options.Gate == nil || options.Tunnel == nil || options.Terminal == nil || options.Agent == nil {
		return nil, errors.New("device lifecycle store, gate, tunnel, terminal, and agent are required")
	}
	if options.Online == nil {
		options.Online = OfflineState{}
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = 5 * time.Second
	}
	return &Service{
		store: options.Store, online: options.Online,
		lifecycle: &lifecycle{
			store: options.Store, gate: options.Gate, tunnel: options.Tunnel,
			terminal: options.Terminal, agent: options.Agent,
			logger: options.Logger, cleanupTimeout: options.CleanupTimeout,
		},
	}, nil
}

func (s *Service) List(ctx context.Context, ownerUserID string) ([]View, error) {
	devices, err := s.store.DevicesByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(devices))
	for _, item := range devices {
		views = append(views, View{Device: item, Online: s.online.IsOnline(item.ID)})
	}
	return views, nil
}

func (s *Service) Get(ctx context.Context, ownerUserID, deviceID string) (View, error) {
	item, err := s.store.DeviceByOwner(ctx, ownerUserID, deviceID)
	if err != nil {
		return View{}, err
	}
	return View{Device: item, Online: s.online.IsOnline(item.ID)}, nil
}

func (s *Service) Unpair(ctx context.Context, ownerUserID, deviceID string, now time.Time) error {
	if s.lifecycle == nil {
		return ErrLifecycleUnavailable
	}
	unlock, err := s.lifecycle.gate.LockDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	result, err := s.lifecycle.store.UnpairDevice(ctx, ownerUserID, deviceID, now.UTC())
	if err != nil {
		unlock()
		return err
	}

	// The commit above is irreversible. These synchronous in-memory steps do
	// not use the request context and complete before another lifecycle holder
	// can observe the Device.
	s.lifecycle.agent.MarkDeviceRevoked(deviceID, result.RevokedAgentSessionIDs)
	joinTunnel := s.lifecycle.tunnel.DetachDevice(deviceID)
	unlock()

	s.runPostCommitCleanup(deviceID, result.RevokedAgentSessionIDs, joinTunnel)
	return nil
}

func (s *Service) runPostCommitCleanup(deviceID string, sessionIDs []string, joinTunnel func() error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), s.lifecycle.cleanupTimeout)
	defer cancel()
	type cleanupResult struct {
		component string
		err       error
	}
	results := make(chan cleanupResult, 3)
	go func() {
		var err error
		if joinTunnel != nil {
			err = joinTunnel()
		}
		results <- cleanupResult{component: "tunnel", err: err}
		s.afterCleanupReport("tunnel")
	}()
	go func() {
		s.lifecycle.terminal.CancelDevice(deviceID)
		results <- cleanupResult{component: "terminal"}
		s.afterCleanupReport("terminal")
	}()
	go func() {
		results <- cleanupResult{
			component: "agent",
			err:       s.lifecycle.agent.InvalidateDevice(cleanupCtx, deviceID, sessionIDs),
		}
		s.afterCleanupReport("agent")
	}()

	var once sync.Once
	logTimeout := func() {
		once.Do(func() {
			s.lifecycle.logger.Warn("device unpair cleanup timed out", "device_id", deviceID)
		})
	}
	for completed := 0; completed < 3; completed++ {
		select {
		case result := <-results:
			if result.err != nil {
				s.lifecycle.logger.Warn("device unpair cleanup incomplete", "device_id", deviceID, "component", result.component, "error", safeCleanupError(result.err))
			}
		case <-cleanupCtx.Done():
			logTimeout()
			return
		}
	}
}

func (s *Service) afterCleanupReport(component string) {
	if hook := s.lifecycle.afterCleanupReport; hook != nil {
		hook(component)
	}
}

func safeCleanupError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "cleanup_failed"
}
