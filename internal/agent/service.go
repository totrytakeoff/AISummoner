package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/store"
)

type Store interface {
	CreateAgentSession(context.Context, store.AgentSession) error
	AgentSessionByOwner(context.Context, string, string) (store.AgentSession, error)
	AgentSnapshotByOwner(context.Context, string, string) (store.AgentSnapshot, error)
	LatestAgentSnapshotByDeviceOwner(context.Context, string, string) (store.AgentSnapshot, error)
	RecentAgentSessionsByDeviceOwner(context.Context, string, string) ([]store.AgentSessionSummary, error)
	ArchivedAgentSessionsByOwner(context.Context, string) ([]store.AgentSessionSummary, error)
	AgentSettingsByOwner(context.Context, string) (store.AgentSettings, error)
	UpdateAgentSettings(context.Context, string, string, time.Time) (store.AgentSettings, error)
	UpdateAgentSessionApprovalMode(context.Context, string, string, string, time.Time) (store.AgentSession, error)
	SetAgentSessionArchived(context.Context, string, string, bool, time.Time) (store.AgentSession, error)
	DeleteAgentSessionByOwner(context.Context, string, string) (store.AgentSession, error)
	BeginAgentTurn(context.Context, string, string, time.Time) error
	UpdateAgentSessionState(context.Context, string, string, string, time.Time) error
	UpdateAgentExternalSessionID(context.Context, string, string, string, time.Time) error
	CreateAgentMessage(context.Context, string, store.AgentMessage) error
	CreateAgentToolCall(context.Context, string, store.ToolCall) error
	AgentToolCallByOwner(context.Context, string, string) (store.ToolCall, error)
	DecideAgentToolCall(context.Context, string, string, string, time.Time) (store.ToolCall, store.AgentSession, error)
	FailPendingAgentToolCall(context.Context, string, string, string, time.Time) (store.ToolCall, error)
	StartAgentToolCall(context.Context, string, string) error
	FinishAgentToolCall(context.Context, string, string, string, *int, *string, time.Time) error
	DeviceByOwner(context.Context, string, string) (store.Device, error)
}

type OnlineState interface {
	IsOnline(deviceID string) bool
}

type Auditor interface {
	CreateAuditEvent(context.Context, store.AuditEvent) error
}

type ServiceOptions struct {
	Store            Store
	Adapter          Adapter
	Provider         string
	Executor         RemoteExecutor
	Online           OnlineState
	Auditor          Auditor
	Logger           *slog.Logger
	Now              func() time.Time
	TurnTimeout      time.Duration
	ApprovalTimeout  time.Duration
	SubscriberBuffer int
}

type Service struct {
	store           Store
	adapters        map[string]Adapter
	provider        string
	executor        RemoteExecutor
	online          OnlineState
	auditor         Auditor
	logger          *slog.Logger
	now             func() time.Time
	turnTimeout     time.Duration
	approvalTimeout time.Duration
	hub             *eventHub
	beforeExecute   func(context.Context)

	rootCtx context.Context
	cancel  context.CancelFunc

	mu          sync.Mutex
	running     map[string]*runningTurn
	configuring map[string]struct{}
	pending     map[string]*pendingDecision
	revoked     map[string]string
	archived    map[string]string
	closed      bool

	mutations sync.WaitGroup
	turns     sync.WaitGroup
	closeDone chan struct{}
}

type runningTurn struct {
	sessionID string
	deviceID  string
	userID    string
	cancel    context.CancelFunc
	done      chan struct{}
}

type pendingDecision struct {
	sessionID string
	deviceID  string
	userID    string
	decision  chan string
}

type Snapshot = store.AgentSnapshot

const defaultCleanupTimeout = 2 * time.Second

const (
	MaxConversationHistoryMessages = 64
	MaxConversationHistoryBytes    = 256 * 1024
)

// JSON may encode each valid decoded byte as a six-byte \u00XX escape. Keep a
// hard raw cap while leaving room for both independent decoded maxima and the
// bounded keys/timeout envelope.
const maxRemoteExecArgumentBytes = 6*(MaxCommandBytes+MaxCWDBytes) + 1024

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || options.Adapter == nil || options.Executor == nil || options.Online == nil {
		return nil, errors.New("agent store, adapter, executor, and online state are required")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.TurnTimeout <= 0 || options.TurnTimeout > DefaultTurnTimeout {
		options.TurnTimeout = DefaultTurnTimeout
	}
	if options.ApprovalTimeout <= 0 || options.ApprovalTimeout > DefaultApprovalWait {
		options.ApprovalTimeout = DefaultApprovalWait
	}
	if options.Provider == "" {
		options.Provider = ProviderFake
	}
	if len(options.Provider) > 64 || !validProvider(options.Provider) {
		return nil, errors.New("invalid agent provider")
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	return &Service{
		store: options.Store, adapters: map[string]Adapter{options.Provider: options.Adapter}, provider: options.Provider,
		executor: options.Executor, online: options.Online, auditor: options.Auditor,
		logger: options.Logger, now: options.Now, turnTimeout: options.TurnTimeout,
		approvalTimeout: options.ApprovalTimeout, hub: newEventHub(options.SubscriberBuffer),
		rootCtx: rootCtx, cancel: cancel, running: make(map[string]*runningTurn), configuring: make(map[string]struct{}),
		pending: make(map[string]*pendingDecision),
		revoked: make(map[string]string), archived: make(map[string]string),
		closeDone: make(chan struct{}),
	}, nil
}

// SetAdapter makes adapter the provider used by sessions created after this
// call. Existing sessions keep their persisted provider name, and in-flight
// Turns keep the Adapter instance they already acquired. The registry retains
// prior providers so an older conversation never silently runs through a
// different provider.
func (s *Service) SetAdapter(provider string, adapter Adapter) error {
	if adapter == nil || len(provider) > 64 || !validProvider(provider) {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	if s.adapters == nil {
		s.adapters = make(map[string]Adapter)
	}
	s.adapters[provider] = adapter
	s.provider = provider
	return nil
}

func (s *Service) currentProvider() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrServiceClosed
	}
	return s.provider, nil
}

func (s *Service) adapterForProvider(provider string) (Adapter, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	adapter, ok := s.adapters[provider]
	return adapter, ok
}

// RuntimeProviderDirectory exposes an Adapter's optional Host-level provider
// configuration capability without leaking the concrete Adapter through the
// HTTP layer.
func (s *Service) RuntimeProviderDirectory(ctx context.Context, runtime string) (RuntimeProviderDirectory, error) {
	if len(runtime) > MaxRuntimeIDBytes || !validProvider(runtime) {
		return RuntimeProviderDirectory{}, ErrNotFound
	}
	if err := s.beginMutation(); err != nil {
		return RuntimeProviderDirectory{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	adapter, ok := s.adapterForProvider(runtime)
	if !ok {
		return RuntimeProviderDirectory{}, ErrNotFound
	}
	configurator, ok := adapter.(RuntimeConfigurationAdapter)
	if !ok {
		return RuntimeProviderDirectory{}, ErrNotFound
	}
	return configurator.ProviderDirectory(ctx)
}

func (s *Service) ConfigureRuntimeProvider(ctx context.Context, runtime string, mutation RuntimeProviderMutation) error {
	if len(runtime) > MaxRuntimeIDBytes || !validProvider(runtime) || validateRuntimeProviderMutation(mutation) != nil {
		return ErrInvalidRequest
	}
	if err := s.beginMutation(); err != nil {
		return err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	adapter, ok := s.adapterForProvider(runtime)
	if !ok {
		return ErrNotFound
	}
	configurator, ok := adapter.(RuntimeConfigurationAdapter)
	if !ok {
		return ErrNotFound
	}
	return configurator.ConfigureProvider(ctx, mutation)
}

func (s *Service) RemoveRuntimeProvider(ctx context.Context, runtime, provider string, expectedRevision int64) error {
	if len(runtime) > MaxRuntimeIDBytes || !validProvider(runtime) || len(provider) > MaxProviderIDBytes ||
		!validProvider(provider) || expectedRevision < 0 {
		return ErrInvalidRequest
	}
	if err := s.beginMutation(); err != nil {
		return err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	adapter, ok := s.adapterForProvider(runtime)
	if !ok {
		return ErrNotFound
	}
	configurator, ok := adapter.(RuntimeConfigurationAdapter)
	if !ok {
		return ErrNotFound
	}
	return configurator.RemoveProvider(ctx, provider, expectedRevision)
}

func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		closed := s.closeDone
		s.mu.Unlock()
		<-closed
		return
	}
	s.closed = true
	s.cancel()
	for _, turn := range s.running {
		turn.cancel()
	}
	s.mu.Unlock()
	s.hub.close()
	s.mutations.Wait()
	s.turns.Wait()
	close(s.closeDone)
}

func (s *Service) beginMutation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	// Add is serialized with Close setting closed, so it cannot race the first
	// Wait while the counter is zero.
	s.mutations.Add(1)
	return nil
}

func (s *Service) endMutation() { s.mutations.Done() }

func (s *Service) mutationContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.rootCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *Service) cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultCleanupTimeout)
}

func (s *Service) CreateSession(ctx context.Context, ownerUserID, deviceID, approvalMode string) (store.AgentSession, error) {
	if err := s.beginMutation(); err != nil {
		return store.AgentSession{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	provider, err := s.currentProvider()
	if err != nil {
		return store.AgentSession{}, err
	}
	if approvalMode == "" {
		settings, settingsErr := s.store.AgentSettingsByOwner(ctx, ownerUserID)
		if settingsErr != nil {
			return store.AgentSession{}, mapStoreNotFound(settingsErr)
		}
		approvalMode = settings.DefaultApprovalMode
	}
	if approvalMode != store.AgentApprovalPerCommand && approvalMode != store.AgentApprovalFullAccess {
		return store.AgentSession{}, ErrInvalidRequest
	}
	if _, err := s.store.DeviceByOwner(ctx, ownerUserID, deviceID); err != nil {
		return store.AgentSession{}, mapStoreNotFound(err)
	}
	if !s.online.IsOnline(deviceID) {
		return store.AgentSession{}, ErrDeviceOffline
	}
	sessionID, err := id.New("ags")
	if err != nil {
		return store.AgentSession{}, err
	}
	now := s.now().UTC()
	session := store.AgentSession{
		ID: sessionID, UserID: ownerUserID, DeviceID: deviceID, ApprovalMode: approvalMode,
		Provider: provider, State: store.AgentSessionIdle, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateAgentSession(ctx, session); err != nil {
		return store.AgentSession{}, mapStoreNotFound(err)
	}
	s.audit(ctx, ownerUserID, deviceID, "agent.session_created", map[string]any{"session_id": session.ID, "approval_mode": session.ApprovalMode, "provider": session.Provider})
	return session, nil
}

func (s *Service) Snapshot(ctx context.Context, ownerUserID, sessionID string) (store.AgentSnapshot, error) {
	value, err := s.store.AgentSnapshotByOwner(ctx, ownerUserID, sessionID)
	if err != nil {
		return store.AgentSnapshot{}, mapStoreNotFound(err)
	}
	return value, nil
}

// SessionModels lazily prepares the Runtime Session, persists its opaque ID,
// and returns the Runtime-owned model directory. Model operations and Turn
// admission are mutually exclusive for one product Session.
func (s *Service) SessionModels(ctx context.Context, ownerUserID, sessionID string) (ModelDirectory, error) {
	if err := s.beginMutation(); err != nil {
		return ModelDirectory{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	runtime, externalID, release, err := s.prepareRuntimeSession(ctx, ownerUserID, sessionID)
	if err != nil {
		return ModelDirectory{}, err
	}
	defer release()
	return runtime.Models(ctx, externalID)
}

func (s *Service) SelectSessionModel(ctx context.Context, ownerUserID, sessionID string, selection ModelSelection) (ModelSelection, error) {
	if !validModelSelection(selection) {
		return ModelSelection{}, ErrInvalidRequest
	}
	if err := s.beginMutation(); err != nil {
		return ModelSelection{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	runtime, externalID, release, err := s.prepareRuntimeSession(ctx, ownerUserID, sessionID)
	if err != nil {
		return ModelSelection{}, err
	}
	defer release()
	return runtime.SelectModel(ctx, externalID, selection)
}

func (s *Service) prepareRuntimeSession(ctx context.Context, ownerUserID, sessionID string) (RuntimeSessionAdapter, string, func(), error) {
	session, err := s.store.AgentSessionByOwner(ctx, ownerUserID, sessionID)
	if err != nil {
		return nil, "", nil, mapStoreNotFound(err)
	}
	adapter, ok := s.adapterForProvider(session.Provider)
	if !ok {
		return nil, "", nil, &AdapterError{Code: "provider_unavailable", Err: errors.New("agent provider is unavailable")}
	}
	runtime, ok := adapter.(RuntimeSessionAdapter)
	if !ok {
		return nil, "", nil, ErrNotFound
	}
	if err := s.reserveSessionConfiguration(session.ID); err != nil {
		return nil, "", nil, err
	}
	release := func() { s.releaseSessionConfiguration(session.ID) }
	externalID := ""
	if session.ExternalSessionID != nil {
		externalID = *session.ExternalSessionID
	}
	preparedID, err := runtime.PrepareSession(ctx, externalID)
	if err != nil {
		release()
		return nil, "", nil, err
	}
	if preparedID == "" {
		release()
		return nil, "", nil, &AdapterError{Code: "protocol_error", Err: errors.New("runtime returned an empty Session ID")}
	}
	if preparedID != externalID {
		if err := s.store.UpdateAgentExternalSessionID(ctx, ownerUserID, session.ID, preparedID, s.now().UTC()); err != nil {
			release()
			return nil, "", nil, mapStoreNotFound(err)
		}
	}
	return runtime, preparedID, release, nil
}

func (s *Service) reserveSessionConfiguration(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	if _, revoked := s.revoked[sessionID]; revoked {
		return ErrNotFound
	}
	if _, archived := s.archived[sessionID]; archived {
		return ErrNotFound
	}
	if _, running := s.running[sessionID]; running {
		return ErrTurnInProgress
	}
	if _, configuring := s.configuring[sessionID]; configuring {
		return ErrTurnInProgress
	}
	s.configuring[sessionID] = struct{}{}
	return nil
}

func (s *Service) releaseSessionConfiguration(sessionID string) {
	s.mu.Lock()
	delete(s.configuring, sessionID)
	s.mu.Unlock()
}

func (s *Service) LatestSnapshot(ctx context.Context, ownerUserID, deviceID string) (store.AgentSnapshot, error) {
	value, err := s.store.LatestAgentSnapshotByDeviceOwner(ctx, ownerUserID, deviceID)
	if err != nil {
		return store.AgentSnapshot{}, mapStoreNotFound(err)
	}
	return value, nil
}

// ListSessions returns the Store's fixed-size owner-scoped Controller index.
// An empty result intentionally covers both no visible Sessions and an
// unknown/unowned Device without disclosing ownership state.
func (s *Service) ListSessions(ctx context.Context, ownerUserID, deviceID string) ([]store.AgentSessionSummary, error) {
	values, err := s.store.RecentAgentSessionsByDeviceOwner(ctx, ownerUserID, deviceID)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (s *Service) ListArchivedSessions(ctx context.Context, ownerUserID string) ([]store.AgentSessionSummary, error) {
	values, err := s.store.ArchivedAgentSessionsByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, mapStoreNotFound(err)
	}
	return values, nil
}

func (s *Service) Settings(ctx context.Context, ownerUserID string) (store.AgentSettings, error) {
	settings, err := s.store.AgentSettingsByOwner(ctx, ownerUserID)
	if err != nil {
		return store.AgentSettings{}, mapStoreNotFound(err)
	}
	return settings, nil
}

func (s *Service) UpdateSettings(ctx context.Context, ownerUserID, approvalMode string) (store.AgentSettings, error) {
	if err := s.beginMutation(); err != nil {
		return store.AgentSettings{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	if approvalMode != store.AgentApprovalPerCommand && approvalMode != store.AgentApprovalFullAccess {
		return store.AgentSettings{}, ErrInvalidRequest
	}
	settings, err := s.store.UpdateAgentSettings(ctx, ownerUserID, approvalMode, s.now().UTC())
	if err != nil {
		return store.AgentSettings{}, mapStoreNotFound(err)
	}
	return settings, nil
}

func (s *Service) UpdateSessionApprovalMode(ctx context.Context, ownerUserID, sessionID, approvalMode string) (store.AgentSession, error) {
	if err := s.beginMutation(); err != nil {
		return store.AgentSession{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	if approvalMode != store.AgentApprovalPerCommand && approvalMode != store.AgentApprovalFullAccess {
		return store.AgentSession{}, ErrInvalidRequest
	}
	session, err := s.store.UpdateAgentSessionApprovalMode(ctx, ownerUserID, sessionID, approvalMode, s.now().UTC())
	if errors.Is(err, store.ErrConflict) {
		return store.AgentSession{}, ErrTurnInProgress
	}
	if err != nil {
		return store.AgentSession{}, mapStoreNotFound(err)
	}
	s.audit(ctx, ownerUserID, session.DeviceID, "agent.session_permission_changed", map[string]any{
		"session_id": session.ID, "approval_mode": session.ApprovalMode,
	})
	return session, nil
}

func (s *Service) SetSessionArchived(ctx context.Context, ownerUserID, sessionID string, archived bool) (store.AgentSession, error) {
	if err := s.beginMutation(); err != nil {
		return store.AgentSession{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	session, err := s.store.SetAgentSessionArchived(ctx, ownerUserID, sessionID, archived, s.now().UTC())
	if errors.Is(err, store.ErrConflict) {
		return store.AgentSession{}, ErrTurnInProgress
	}
	if err != nil {
		return store.AgentSession{}, mapStoreNotFound(err)
	}
	s.mu.Lock()
	if archived {
		s.archived[session.ID] = session.DeviceID
	} else {
		delete(s.archived, session.ID)
	}
	s.mu.Unlock()
	if archived {
		s.hub.closeSession(session.ID)
	}
	s.audit(ctx, ownerUserID, session.DeviceID, "agent.session_archived", map[string]any{
		"session_id": session.ID, "archived": archived,
	})
	return session, nil
}

func (s *Service) DeleteSession(ctx context.Context, ownerUserID, sessionID string) error {
	if err := s.beginMutation(); err != nil {
		return err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	session, err := s.store.DeleteAgentSessionByOwner(ctx, ownerUserID, sessionID)
	if errors.Is(err, store.ErrConflict) {
		return ErrTurnInProgress
	} else if err != nil {
		return mapStoreNotFound(err)
	}
	s.mu.Lock()
	delete(s.archived, session.ID)
	s.revoked[session.ID] = session.DeviceID
	s.mu.Unlock()
	s.hub.closeSession(session.ID)
	s.audit(ctx, ownerUserID, session.DeviceID, "agent.session_deleted", map[string]any{"session_id": session.ID})
	return nil
}

// StartTurn persists the user message synchronously, then runs the bounded
// provider Turn in the background. It allows exactly one running Turn per
// product Session.
func (s *Service) StartTurn(ctx context.Context, ownerUserID, sessionID, content string) (store.AgentMessage, error) {
	if err := s.beginMutation(); err != nil {
		return store.AgentMessage{}, err
	}
	defer s.endMutation()
	ctx, mutationCancel := s.mutationContext(ctx)
	defer mutationCancel()
	if err := validateMessage(content); err != nil {
		return store.AgentMessage{}, err
	}
	session, err := s.store.AgentSessionByOwner(ctx, ownerUserID, sessionID)
	if err != nil {
		return store.AgentMessage{}, mapStoreNotFound(err)
	}
	adapter, ok := s.adapterForProvider(session.Provider)
	if !ok {
		return store.AgentMessage{}, &AdapterError{Code: "provider_unavailable", Err: errors.New("agent provider is unavailable")}
	}
	device, err := s.store.DeviceByOwner(ctx, ownerUserID, session.DeviceID)
	if err != nil {
		return store.AgentMessage{}, mapStoreNotFound(err)
	}
	turnCtx, cancel := context.WithTimeout(s.rootCtx, s.turnTimeout)
	if err := s.reserveTurn(session, ownerUserID, cancel); err != nil {
		cancel()
		return store.AgentMessage{}, err
	}
	reserved := true
	defer func() {
		if reserved {
			cancel()
			s.releaseTurn(session.ID)
		}
	}()
	externalSessionID := ""
	if session.ExternalSessionID != nil {
		externalSessionID = *session.ExternalSessionID
	}
	if runtime, supportsRuntimeSession := adapter.(RuntimeSessionAdapter); supportsRuntimeSession {
		preparedID, prepareErr := runtime.PrepareSession(ctx, externalSessionID)
		if prepareErr != nil {
			return store.AgentMessage{}, prepareErr
		}
		if preparedID == "" {
			return store.AgentMessage{}, &AdapterError{Code: "protocol_error", Err: errors.New("runtime returned an empty Session ID")}
		}
		if preparedID != externalSessionID {
			if err := s.store.UpdateAgentExternalSessionID(ctx, ownerUserID, session.ID, preparedID, s.now().UTC()); err != nil {
				return store.AgentMessage{}, mapStoreNotFound(err)
			}
			externalSessionID = preparedID
		}
	}
	request := RunRequest{SessionID: session.ID, ExternalSessionID: externalSessionID, UserText: content,
		Target: ExecutionTarget{Platform: device.Platform, Arch: device.Arch}}
	if preflight, supportsPreflight := adapter.(TurnPreflighter); supportsPreflight {
		if err := preflight.PreflightTurn(ctx, request); err != nil {
			return store.AgentMessage{}, err
		}
	}
	messageID, err := id.New("msg")
	if err != nil {
		return store.AgentMessage{}, err
	}
	now := s.now().UTC()
	if err := s.store.BeginAgentTurn(ctx, ownerUserID, session.ID, now); errors.Is(err, store.ErrConflict) {
		return store.AgentMessage{}, ErrTurnInProgress
	} else if err != nil {
		if !s.isRevoked(session.ID) {
			s.persistSessionState(ownerUserID, session.ID, store.AgentSessionFailed)
		}
		return store.AgentMessage{}, mapStoreNotFound(err)
	}
	session, err = s.store.AgentSessionByOwner(ctx, ownerUserID, session.ID)
	if err != nil {
		if !s.isRevoked(sessionID) {
			s.persistSessionState(ownerUserID, sessionID, store.AgentSessionFailed)
		}
		return store.AgentMessage{}, mapStoreNotFound(err)
	}
	message := store.AgentMessage{ID: messageID, SessionID: session.ID, Role: "user", Content: content, CreatedAt: now}
	if err := s.store.CreateAgentMessage(ctx, ownerUserID, message); err != nil {
		if !s.isRevoked(session.ID) {
			s.persistSessionState(ownerUserID, session.ID, store.AgentSessionFailed)
		}
		return store.AgentMessage{}, mapStoreNotFound(err)
	}
	reserved = false
	go s.runTurn(turnCtx, cancel, session, message)
	return message, nil
}

func (s *Service) reserveTurn(session store.AgentSession, ownerUserID string, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrServiceClosed
	}
	if _, revoked := s.revoked[session.ID]; revoked {
		return ErrNotFound
	}
	if _, archived := s.archived[session.ID]; archived {
		return ErrNotFound
	}
	if _, exists := s.running[session.ID]; exists {
		return ErrTurnInProgress
	}
	if _, exists := s.configuring[session.ID]; exists {
		return ErrTurnInProgress
	}
	// Add is protected by the same mutex/closed gate as Close.
	s.turns.Add(1)
	s.running[session.ID] = &runningTurn{
		sessionID: session.ID, deviceID: session.DeviceID, userID: ownerUserID,
		cancel: cancel, done: make(chan struct{}),
	}
	return nil
}

func (s *Service) releaseTurn(sessionID string) {
	s.mu.Lock()
	if turn := s.running[sessionID]; turn != nil {
		// Publish completion before removing the registry entry, so an
		// invalidator cannot observe absence while work can still continue.
		close(turn.done)
	}
	delete(s.running, sessionID)
	for toolCallID, pending := range s.pending {
		if pending.sessionID == sessionID {
			delete(s.pending, toolCallID)
		}
	}
	s.mu.Unlock()
	s.turns.Done()
}

func (s *Service) runTurn(ctx context.Context, cancel context.CancelFunc, session store.AgentSession, message store.AgentMessage) {
	defer cancel()
	defer s.releaseTurn(session.ID)
	s.publish(session.ID, EventSessionState, map[string]any{"state": store.AgentSessionRunning})
	sink := &turnSink{service: s, ownerUserID: session.UserID, sessionID: session.ID}
	invoker := newTurnInvoker(s, &session)
	adapter, ok := s.adapterForProvider(session.Provider)
	if !ok {
		s.failTurn(session, "provider_unavailable")
		return
	}
	externalSessionID := ""
	if session.ExternalSessionID != nil {
		externalSessionID = *session.ExternalSessionID
	}
	snapshot, snapshotErr := s.store.AgentSnapshotByOwner(ctx, session.UserID, session.ID)
	if snapshotErr != nil {
		if !s.isRevoked(session.ID) {
			s.failTurn(session, "PERSISTENCE_FAILURE")
		}
		return
	}
	device, deviceErr := s.store.DeviceByOwner(ctx, session.UserID, session.DeviceID)
	if deviceErr != nil {
		if !s.isRevoked(session.ID) {
			s.failTurn(session, "DEVICE_NOT_FOUND")
		}
		return
	}
	err := adapter.Run(ctx, RunRequest{
		SessionID: session.ID, ExternalSessionID: externalSessionID,
		UserText: message.Content, History: conversationHistory(snapshot.Messages), RemoteExec: invoker,
		Target: ExecutionTarget{Platform: device.Platform, Arch: device.Arch},
	}, sink)
	if err == nil {
		err = ctx.Err()
	}
	if transcriptErr := s.persistTurnTranscript(session, sink); transcriptErr != nil {
		s.failTurn(session, "PERSISTENCE_FAILURE")
		return
	}
	if sink.reasoning.Len() > 0 {
		s.publish(session.ID, EventReasoningDone, map[string]any{"text": sink.reasoning.String()})
	}
	if sink.text.Len() > 0 {
		s.publish(session.ID, EventTextDone, map[string]any{"text": sink.text.String()})
	}
	if err != nil {
		failureCode := FailureExecTransport
		if errors.Is(err, context.DeadlineExceeded) {
			failureCode = FailureExecTimeout
		} else if errors.Is(err, context.Canceled) {
			failureCode = FailureExecCanceled
		} else if errors.Is(err, ErrApprovalTimeout) {
			failureCode = FailureApprovalTimeout
		} else if errors.Is(err, ErrDeviceOffline) {
			failureCode = FailureDeviceOffline
		} else {
			var adapterError *AdapterError
			if errors.As(err, &adapterError) {
				failureCode = safeAdapterFailureCode(adapterError.Code)
			}
		}
		s.failTurn(session, failureCode)
		return
	}
	if err := s.persistSessionState(session.UserID, session.ID, store.AgentSessionIdle); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		s.failTurn(session, "PERSISTENCE_FAILURE")
		return
	}
	s.publish(session.ID, EventTurnCompleted, map[string]any{"state": "completed"})
	s.publish(session.ID, EventSessionState, map[string]any{"state": store.AgentSessionIdle})
}

func conversationHistory(messages []store.AgentMessage) []ConversationMessage {
	reversed := make([]ConversationMessage, 0, MaxConversationHistoryMessages)
	totalBytes := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		messageBytes := len([]byte(message.Content))
		if len(reversed) >= MaxConversationHistoryMessages || totalBytes > MaxConversationHistoryBytes-messageBytes {
			break
		}
		reversed = append(reversed, ConversationMessage{Role: message.Role, Content: message.Content})
		totalBytes += messageBytes
	}
	history := make([]ConversationMessage, len(reversed))
	for index := range reversed {
		history[len(reversed)-1-index] = reversed[index]
	}
	for len(history) > 0 && history[0].Role != "user" {
		history = history[1:]
	}
	return history
}

func (s *Service) persistTurnTranscript(session store.AgentSession, sink *turnSink) error {
	completedAt := s.now().UTC()
	values := []struct {
		role      string
		content   string
		createdAt time.Time
	}{
		{role: "reasoning", content: sink.reasoning.String(), createdAt: sink.reasoningStartedAt},
		// The bounded projection stores one final assistant message per Turn.
		// Timestamp it at completion so replay cannot place its command card
		// below output that only exists after the command finished.
		{role: "assistant", content: sink.text.String(), createdAt: completedAt},
	}
	for _, value := range values {
		if value.content == "" {
			continue
		}
		messageID, err := id.New("msg")
		if err != nil {
			return err
		}
		createdAt := value.createdAt
		if createdAt.IsZero() {
			createdAt = s.now().UTC()
		}
		cleanupCtx, cleanupCancel := s.cleanupContext()
		err = s.store.CreateAgentMessage(cleanupCtx, session.UserID, store.AgentMessage{
			ID: messageID, SessionID: session.ID, Role: value.role, Content: value.content, CreatedAt: createdAt,
		})
		cleanupCancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Decide(ctx context.Context, ownerUserID, toolCallID, decision string) (store.ToolCall, error) {
	if err := s.beginMutation(); err != nil {
		return store.ToolCall{}, err
	}
	defer s.endMutation()
	ctx, cancel := s.mutationContext(ctx)
	defer cancel()
	if decision != DecisionApproveOnce && decision != DecisionApproveSession && decision != DecisionDeny {
		return store.ToolCall{}, ErrInvalidRequest
	}
	toolCall, _, err := s.store.DecideAgentToolCall(ctx, ownerUserID, toolCallID, decision, s.now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return store.ToolCall{}, ErrNotFound
	}
	if errors.Is(err, store.ErrConflict) {
		return store.ToolCall{}, ErrInvalidState
	}
	if err != nil {
		return store.ToolCall{}, err
	}
	s.mu.Lock()
	if _, revoked := s.revoked[toolCall.SessionID]; revoked {
		s.mu.Unlock()
		return store.ToolCall{}, ErrNotFound
	}
	pending := s.pending[toolCallID]
	if pending != nil && pending.userID == ownerUserID {
		select {
		case pending.decision <- decision:
		default:
		}
	}
	s.mu.Unlock()
	session, sessionErr := s.store.AgentSessionByOwner(ctx, ownerUserID, toolCall.SessionID)
	if sessionErr == nil {
		s.audit(ctx, ownerUserID, session.DeviceID, "agent.tool_decision", map[string]any{"session_id": session.ID, "tool_call_id": toolCall.ID, "decision": decision})
	}
	return toolCall, nil
}

func (s *Service) Subscribe(ctx context.Context, ownerUserID, sessionID string) (<-chan Event, func(), error) {
	if _, err := s.store.AgentSessionByOwner(ctx, ownerUserID, sessionID); err != nil {
		return nil, nil, mapStoreNotFound(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrServiceClosed
	}
	if _, revoked := s.revoked[sessionID]; revoked {
		return nil, nil, ErrNotFound
	}
	if _, archived := s.archived[sessionID]; archived {
		return nil, nil, ErrNotFound
	}
	// The runtime tombstone check and registration are one Service-mutex
	// operation. MarkDeviceRevoked therefore either sees this subscriber and
	// later closes it, or makes this registration fail.
	return s.hub.subscribe(sessionID)
}

// SubscriberCount is an operational/test metric used to verify that canceled
// SSE clients do not retain hub registrations. It exposes no event contents.
func (s *Service) SubscriberCount() int { return s.hub.countAll() }

// IsTurnRunning is a synchronization/operational view used by integration
// tests and shutdown checks. It does not expose prompt or tool contents.
func (s *Service) IsTurnRunning(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, running := s.running[sessionID]
	return running
}

// CancelDevice is the narrow lifecycle hook used by later Device unpair
// wiring. It cancels running Turns and pending approval waits for this Device.
func (s *Service) CancelDevice(deviceID string) {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0)
	for _, turn := range s.running {
		if turn.deviceID == deviceID {
			cancels = append(cancels, turn.cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// MarkDeviceRevoked synchronously closes the Store-lookup-to-runtime-admission
// window after the durable unpair commit. It performs no I/O and intentionally
// retains tombstones for the process lifetime so same-owner re-pair cannot
// revive an old Session.
func (s *Service) MarkDeviceRevoked(deviceID string, sessionIDs []string) {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID != "" {
			s.revoked[sessionID] = deviceID
			if turn := s.running[sessionID]; turn != nil {
				cancels = append(cancels, turn.cancel)
			}
		}
	}
	s.mu.Unlock()
	// Context cancellation is non-blocking and must be initiated before the
	// Device gate is released. This prevents an old revoked Turn from reaching
	// a freshly re-paired connection before joined cleanup begins.
	for _, cancel := range cancels {
		cancel()
	}
}

// InvalidateDevice is the joined, idempotent cleanup phase. The caller must
// invoke MarkDeviceRevoked while holding the shared Device lifecycle gate,
// then invoke this method after releasing that gate.
func (s *Service) InvalidateDevice(ctx context.Context, deviceID string, sessionIDs []string) error {
	s.MarkDeviceRevoked(deviceID, sessionIDs)

	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(sessionIDs))
	done := make([]<-chan struct{}, 0, len(sessionIDs))
	for _, turn := range s.running {
		if revokedDevice, revoked := s.revoked[turn.sessionID]; revoked && revokedDevice == deviceID {
			cancels = append(cancels, turn.cancel)
			done = append(done, turn.done)
		}
	}
	s.mu.Unlock()

	// Close subscribers before cancellation can reach a finalizer. Future
	// subscriptions are rejected by the runtime tombstone under Service.mu.
	for _, sessionID := range sessionIDs {
		s.hub.closeSession(sessionID)
	}
	for _, cancel := range cancels {
		cancel()
	}
	for _, completed := range done {
		select {
		case <-completed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Service) isRevoked(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, revoked := s.revoked[sessionID]
	return revoked
}

func (s *Service) publish(sessionID, eventType string, payload any) {
	eventID, err := id.New("evt")
	if err != nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	event := Event{ID: eventID, SessionID: sessionID, CreatedAt: s.now().UTC(), Type: eventType, Payload: encoded}
	if event.Validate() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, revoked := s.revoked[sessionID]; revoked {
		return
	}
	if _, archived := s.archived[sessionID]; archived {
		return
	}
	s.hub.publish(event)
}

func (s *Service) audit(ctx context.Context, userID, deviceID, eventType string, metadata map[string]any) {
	if s.auditor == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, defaultCleanupTimeout)
	defer cancel()
	eventID, err := id.New("audit")
	if err != nil {
		return
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if err := s.auditor.CreateAuditEvent(ctx, store.AuditEvent{
		ID: eventID, ActorUserID: &userID, DeviceID: &deviceID,
		EventType: eventType, MetadataJSON: string(encoded), CreatedAt: s.now().UTC(),
	}); err != nil {
		s.logger.Error("agent audit event failed", "event_type", eventType, "session_id", metadata["session_id"])
	}
}

func (s *Service) failTurn(session store.AgentSession, code string) {
	if s.isRevoked(session.ID) {
		return
	}
	if err := s.persistSessionState(session.UserID, session.ID, store.AgentSessionFailed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if s.isRevoked(session.ID) {
			return
		}
		s.logger.Error("agent turn terminal persistence failed", "session_id", session.ID, "code", "PERSISTENCE_FAILURE")
		return
	}
	s.publish(session.ID, EventTurnFailed, map[string]any{"code": code, "message": safeTurnFailureMessage(code)})
	s.publish(session.ID, EventSessionState, map[string]any{"state": store.AgentSessionFailed})
	// Command/output and provider errors are deliberately omitted from logs.
	s.logger.Warn("agent turn failed", "session_id", session.ID, "code", code)
}

func (s *Service) persistSessionState(ownerUserID, sessionID, state string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := s.cleanupContext()
		err := s.store.UpdateAgentSessionState(ctx, ownerUserID, sessionID, state, s.now().UTC())
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

type turnSink struct {
	service            *Service
	ownerUserID        string
	sessionID          string
	reasoning          strings.Builder
	text               strings.Builder
	reasoningStartedAt time.Time
	textStartedAt      time.Time
}

func (sink *turnSink) ReasoningDelta(ctx context.Context, delta string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(delta) || sink.reasoning.Len()+len(delta) > MaxMessageBytes {
		return ErrInvalidRequest
	}
	if sink.reasoning.Len() == 0 {
		sink.reasoningStartedAt = sink.service.now().UTC()
	}
	sink.reasoning.WriteString(delta)
	sink.service.publish(sink.sessionID, EventReasoningDelta, map[string]any{"delta": delta})
	return nil
}

func (sink *turnSink) TextDelta(ctx context.Context, delta string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(delta) {
		return ErrInvalidRequest
	}
	if sink.text.Len()+len(delta) > MaxMessageBytes {
		return ErrInvalidRequest
	}
	if sink.text.Len() == 0 {
		sink.textStartedAt = sink.service.now().UTC()
	}
	sink.text.WriteString(delta)
	sink.service.publish(sink.sessionID, EventTextDelta, map[string]any{"delta": delta})
	return nil
}

func (sink *turnSink) ProviderState(ctx context.Context, state string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(state) == 0 || len(state) > 128 || !utf8.ValidString(state) {
		return ErrInvalidRequest
	}
	sink.service.publish(sink.sessionID, EventSessionState, map[string]any{"provider_state": state})
	return nil
}

func (sink *turnSink) SetExternalSessionID(ctx context.Context, externalSessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sink.service.store.UpdateAgentExternalSessionID(ctx, sink.ownerUserID, sink.sessionID, externalSessionID, sink.service.now().UTC())
}

type turnInvoker struct {
	service *Service
	session *store.AgentSession
	gate    chan struct{}
}

func newTurnInvoker(service *Service, session *store.AgentSession) *turnInvoker {
	return &turnInvoker{
		service: service,
		session: session,
		gate:    make(chan struct{}, 1),
	}
}

func (invoker *turnInvoker) Invoke(ctx context.Context, request ToolRequest) (ToolResult, error) {
	// One Adapter Turn may issue concurrent callbacks. Serialize the complete
	// authoritative tool lifecycle so approval-mode observation and
	// approve_session changes have one defined order. A queued callback remains
	// cancelable.
	select {
	case <-ctx.Done():
		return ToolResult{}, ctx.Err()
	case invoker.gate <- struct{}{}:
	}
	defer func() { <-invoker.gate }()
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}

	if request.Name != ToolRemoteExec {
		return ToolResult{}, ErrInvalidTool
	}
	arguments, err := parseRemoteExecArguments(request.Arguments)
	if err != nil {
		return ToolResult{}, err
	}
	toolCallID, err := id.New("tool")
	if err != nil {
		return ToolResult{}, err
	}
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return ToolResult{}, err
	}
	now := invoker.service.now().UTC()
	status := store.ToolCallStarted
	if invoker.session.ApprovalMode == store.AgentApprovalPerCommand {
		status = store.ToolCallPending
	}
	toolCall := store.ToolCall{
		ID: toolCallID, SessionID: invoker.session.ID, Name: ToolRemoteExec,
		ArgumentsJSON: string(argumentsJSON), Status: status, CreatedAt: now,
	}
	if err := invoker.service.store.CreateAgentToolCall(ctx, invoker.session.UserID, toolCall); err != nil {
		return ToolResult{}, err
	}
	if status == store.ToolCallPending {
		pending, err := invoker.service.installPending(invoker.session, toolCallID)
		if err != nil {
			terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallFailed, nil, "tool approval registration failed")
			if terminalStatus != "" {
				invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
			}
			if terminalErr != nil {
				return ToolResult{}, terminalErr
			}
			return ToolResult{}, err
		}
		invoker.service.publish(invoker.session.ID, EventToolPending, map[string]any{
			"tool_call_id": toolCallID, "name": ToolRemoteExec, "arguments": arguments,
		})
		decision, err := invoker.service.waitForDecision(ctx, invoker.session, toolCallID, pending)
		if err != nil {
			excerpt := "approval timed out or was canceled"
			terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallFailed, nil, excerpt)
			if terminalStatus != "" {
				invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
			}
			failureCode := FailureApprovalTimeout
			if errors.Is(err, context.Canceled) {
				failureCode = FailureExecCanceled
			}
			if terminalStatus != "" {
				invoker.service.audit(context.Background(), invoker.session.UserID, invoker.session.DeviceID, "agent.tool_failed", map[string]any{
					"session_id": invoker.session.ID, "tool_call_id": toolCallID, "code": failureCode,
				})
			}
			if terminalErr != nil {
				return ToolResult{}, terminalErr
			}
			return ToolResult{}, err
		}
		if decision == DecisionDeny {
			excerpt := "command denied by user"
			terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallDenied, nil, excerpt)
			if terminalStatus != "" {
				invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
			}
			if terminalErr != nil {
				return ToolResult{}, terminalErr
			}
			result := ToolResult{ToolCallID: toolCallID, Denied: true, Failure: &ToolFailure{Code: FailureCommandDenied, Message: "command denied by user"}}
			invoker.service.audit(ctx, invoker.session.UserID, invoker.session.DeviceID, "agent.tool_failed", map[string]any{
				"session_id": invoker.session.ID, "tool_call_id": toolCallID, "code": FailureCommandDenied,
			})
			return result, nil
		}
		if err := invoker.service.store.StartAgentToolCall(ctx, invoker.session.UserID, toolCallID); err != nil {
			terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallFailed, nil, "tool start failed")
			if terminalStatus != "" {
				invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
			}
			if terminalErr != nil {
				return ToolResult{}, terminalErr
			}
			return ToolResult{}, err
		}
		if decision == DecisionApproveSession {
			invoker.session.ApprovalMode = store.AgentApprovalFullAccess
		}
	}
	// Full-access calls do not emit tool_call.pending, so started must carry the
	// validated command metadata needed by a newly-created WebUI tool card.
	// Per-command calls repeat the same canonical metadata, which also makes a
	// started event self-contained if the pending frame was missed.
	invoker.service.publish(invoker.session.ID, EventToolStarted, map[string]any{
		"tool_call_id": toolCallID, "name": ToolRemoteExec, "arguments": arguments,
	})
	result, execErr := invoker.service.execute(ctx, *invoker.session, arguments)
	result.ToolCallID = toolCallID
	excerpt := outputExcerpt(result)
	if execErr != nil {
		terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallFailed, nil, excerpt)
		if terminalStatus != "" {
			invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
		}
		if terminalErr != nil {
			return ToolResult{}, terminalErr
		}
		failureCode := FailureExecTransport
		if result.Failure != nil {
			failureCode = result.Failure.Code
		}
		invoker.service.audit(context.Background(), invoker.session.UserID, invoker.session.DeviceID, "agent.tool_failed", map[string]any{
			"session_id": invoker.session.ID, "tool_call_id": toolCallID, "code": failureCode,
		})
		// Device/transport failures are structured tool results for the Adapter.
		// The provider may use them to explain or recover. Parent Turn cancellation,
		// overall timeout, invalid state and limits still stop the Turn promptly.
		if result.Failure != nil && !errors.Is(execErr, context.Canceled) && !errors.Is(execErr, context.DeadlineExceeded) {
			return result, nil
		}
		return result, execErr
	}
	exitCode := result.ExitCode
	terminalStatus, terminalErr := invoker.service.finishTool(invoker.session, toolCallID, store.ToolCallCompleted, &exitCode, excerpt)
	if terminalErr != nil {
		if terminalStatus != "" {
			invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{"tool_call_id": toolCallID, "status": terminalStatus})
		}
		return ToolResult{}, terminalErr
	}
	invoker.service.publish(invoker.session.ID, EventToolOutput, map[string]any{
		"tool_call_id": toolCallID, "stdout": result.Stdout, "stderr": result.Stderr,
		"exit_code": result.ExitCode, "truncated": result.Truncated,
	})
	invoker.service.publish(invoker.session.ID, EventToolCompleted, map[string]any{
		"tool_call_id": toolCallID, "status": store.ToolCallCompleted, "exit_code": result.ExitCode,
	})
	invoker.service.audit(ctx, invoker.session.UserID, invoker.session.DeviceID, "agent.tool_completed", map[string]any{
		"session_id": invoker.session.ID, "tool_call_id": toolCallID,
		"exit_code": result.ExitCode, "truncated": result.Truncated,
	})
	return result, nil
}

func (s *Service) installPending(session *store.AgentSession, toolCallID string) (*pendingDecision, error) {
	pending := &pendingDecision{
		sessionID: session.ID, deviceID: session.DeviceID, userID: session.UserID, decision: make(chan string, 1),
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServiceClosed
	}
	if _, revoked := s.revoked[session.ID]; revoked {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	s.pending[toolCallID] = pending
	s.mu.Unlock()
	// Reconcile a decision that was durably recorded before this waiter became
	// visible. This closes the DB-create / in-memory-register race without
	// relying on browser timing.
	toolCall, err := s.lookupTool(session.UserID, toolCallID)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, toolCallID)
		s.mu.Unlock()
		return nil, err
	}
	if toolCall.Decision != nil {
		pending.decision <- *toolCall.Decision
	}
	return pending, nil
}

func (s *Service) waitForDecision(ctx context.Context, session *store.AgentSession, toolCallID string, pending *pendingDecision) (string, error) {
	defer func() {
		s.mu.Lock()
		delete(s.pending, toolCallID)
		s.mu.Unlock()
	}()
	if err := s.store.UpdateAgentSessionState(ctx, session.UserID, session.ID, store.AgentSessionWaitingApproval, s.now().UTC()); err != nil {
		return s.resolvePendingAbort(session, toolCallID, err)
	}
	s.publish(session.ID, EventSessionState, map[string]any{"state": store.AgentSessionWaitingApproval})
	timer := time.NewTimer(s.approvalTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return s.resolvePendingAbort(session, toolCallID, ctx.Err())
	case <-timer.C:
		return s.resolvePendingAbort(session, toolCallID, ErrApprovalTimeout)
	case decision := <-pending.decision:
		toolCall, err := s.lookupTool(session.UserID, toolCallID)
		if err != nil {
			return "", err
		}
		if toolCall.Status != store.ToolCallPending || toolCall.Decision == nil || *toolCall.Decision != decision {
			return "", ErrInvalidState
		}
		if err := s.store.UpdateAgentSessionState(ctx, session.UserID, session.ID, store.AgentSessionRunning, s.now().UTC()); err != nil {
			return "", err
		}
		s.publish(session.ID, EventSessionState, map[string]any{"state": store.AgentSessionRunning})
		return decision, nil
	}
}

// resolvePendingAbort makes the timeout/cancel/transition failure contend in
// durable storage with Decide. If the abort wins, the row is terminal failed;
// if a decision won first, that exact persisted decision is resumed.
func (s *Service) resolvePendingAbort(session *store.AgentSession, toolCallID string, cause error) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := s.cleanupContext()
		toolCall, err := s.store.FailPendingAgentToolCall(ctx, session.UserID, toolCallID, "approval timed out or was canceled", s.now().UTC())
		cancel()
		if err == nil {
			return "", cause
		}
		if errors.Is(err, store.ErrConflict) {
			if toolCall.Status == store.ToolCallPending && toolCall.Decision != nil {
				return *toolCall.Decision, nil
			}
			if toolCall.Status == store.ToolCallFailed {
				return "", cause
			}
			return "", ErrInvalidState
		}
		lastErr = err
	}
	return "", fmt.Errorf("persist approval abort: %w", lastErr)
}

func (s *Service) lookupTool(ownerUserID, toolCallID string) (store.ToolCall, error) {
	ctx, cancel := s.cleanupContext()
	defer cancel()
	return s.store.AgentToolCallByOwner(ctx, ownerUserID, toolCallID)
}

func terminalToolStatus(status string) bool {
	return status == store.ToolCallCompleted || status == store.ToolCallDenied || status == store.ToolCallFailed
}

// finishTool uses a fresh bounded cleanup context because the Turn context is
// commonly canceled on the paths that most need durable cleanup. An ambiguous
// write is reconciled by lookup; a failed success/denial finalization is
// compensated to failed before any terminal event may be published.
func (s *Service) finishTool(session *store.AgentSession, toolCallID, desiredStatus string, exitCode *int, excerpt string) (string, error) {
	if desiredStatus == store.ToolCallFailed {
		var abortErr error
		for attempt := 0; attempt < 2; attempt++ {
			ctx, cancel := s.cleanupContext()
			toolCall, err := s.store.FailPendingAgentToolCall(ctx, session.UserID, toolCallID, excerpt, s.now().UTC())
			cancel()
			if err == nil {
				return store.ToolCallFailed, nil
			}
			if errors.Is(err, store.ErrConflict) {
				if terminalToolStatus(toolCall.Status) {
					if toolCall.Status == store.ToolCallFailed {
						return store.ToolCallFailed, nil
					}
					return toolCall.Status, fmt.Errorf("tool terminal state changed before failure finalization")
				}
				if toolCall.Status == store.ToolCallStarted || toolCall.Decision != nil {
					break
				}
			}
			abortErr = err
		}
		if toolCall, lookupErr := s.lookupTool(session.UserID, toolCallID); lookupErr == nil && toolCall.Status == store.ToolCallPending && toolCall.Decision == nil {
			return "", fmt.Errorf("atomic pending-tool failure persistence failed: %w", abortErr)
		}
	}
	finish := func(status string, code *int, value string) error {
		ctx, cancel := s.cleanupContext()
		defer cancel()
		return s.store.FinishAgentToolCall(ctx, session.UserID, toolCallID, status, code, &value, s.now().UTC())
	}
	reconcile := func() (store.ToolCall, bool) {
		toolCall, err := s.lookupTool(session.UserID, toolCallID)
		return toolCall, err == nil && terminalToolStatus(toolCall.Status)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := finish(desiredStatus, exitCode, excerpt); err == nil {
			return desiredStatus, nil
		} else {
			lastErr = err
		}
		if toolCall, terminal := reconcile(); terminal {
			if toolCall.Status == desiredStatus {
				return desiredStatus, nil
			}
			return toolCall.Status, fmt.Errorf("tool terminal state changed before finalization")
		}
	}
	if desiredStatus != store.ToolCallFailed {
		const compensationExcerpt = "tool finalization persistence failed"
		for attempt := 0; attempt < 2; attempt++ {
			if err := finish(store.ToolCallFailed, nil, compensationExcerpt); err == nil {
				return store.ToolCallFailed, fmt.Errorf("tool finalization compensated: %w", lastErr)
			} else {
				lastErr = err
			}
			if toolCall, terminal := reconcile(); terminal {
				return toolCall.Status, fmt.Errorf("tool finalization reconciled: %w", lastErr)
			}
		}
	}
	return "", fmt.Errorf("tool terminal persistence failed: %w", lastErr)
}

func (s *Service) execute(ctx context.Context, session store.AgentSession, arguments RemoteExecArguments) (ToolResult, error) {
	if s.beforeExecute != nil {
		s.beforeExecute(ctx)
	}
	if err := ctx.Err(); err != nil || s.isRevoked(session.ID) {
		if err == nil {
			err = context.Canceled
		}
		return ToolResult{Failure: &ToolFailure{Code: FailureExecCanceled, Message: "remote execution canceled"}}, err
	}
	if _, err := s.store.DeviceByOwner(ctx, session.UserID, session.DeviceID); err != nil {
		return ToolResult{Failure: &ToolFailure{Code: FailureDeviceNotFound, Message: "device not found"}}, ErrNotFound
	}
	if !s.online.IsOnline(session.DeviceID) {
		return ToolResult{Failure: &ToolFailure{Code: FailureDeviceOffline, Message: "device is offline"}}, ErrDeviceOffline
	}
	if err := ctx.Err(); err != nil || s.isRevoked(session.ID) {
		if err == nil {
			err = context.Canceled
		}
		return ToolResult{Failure: &ToolFailure{Code: FailureExecCanceled, Message: "remote execution canceled"}}, err
	}
	// TimeoutMS was range-checked as an int before this bounded conversion.
	timeout := time.Duration(arguments.TimeoutMS) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execution, err := s.executor.Exec(execCtx, session.DeviceID, arguments.Command, arguments.CWD)
	if err != nil {
		code := FailureExecTransport
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			code = FailureExecTimeout
		} else if errors.Is(err, context.Canceled) || errors.Is(execCtx.Err(), context.Canceled) {
			code = FailureExecCanceled
		} else {
			var executionError *ExecutionError
			if errors.As(err, &executionError) && executionError.Code != "" {
				code = stableFailureCode(executionError.Code)
			}
		}
		return ToolResult{Failure: &ToolFailure{Code: code, Message: "remote execution failed"}}, err
	}
	stdout, stderr, truncated := truncateOutput(execution.Stdout, execution.Stderr, MaxToolOutputBytes)
	return ToolResult{Stdout: string(stdout), Stderr: string(stderr), ExitCode: execution.ExitCode, Truncated: truncated}, nil
}

func parseRemoteExecArguments(raw json.RawMessage) (RemoteExecArguments, error) {
	if len(raw) == 0 || len(raw) > maxRemoteExecArgumentBytes {
		return RemoteExecArguments{}, ErrInvalidTool
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var arguments struct {
		Command   string `json:"command"`
		CWD       string `json:"cwd,omitempty"`
		TimeoutMS *int   `json:"timeout_ms,omitempty"`
	}
	if err := decoder.Decode(&arguments); err != nil {
		return RemoteExecArguments{}, fmt.Errorf("%w: invalid remote_exec arguments", ErrInvalidTool)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return RemoteExecArguments{}, ErrInvalidTool
	} else if !errors.Is(err, io.EOF) {
		return RemoteExecArguments{}, ErrInvalidTool
	}
	if !utf8.ValidString(arguments.Command) || len(arguments.Command) == 0 || len([]byte(arguments.Command)) > MaxCommandBytes || strings.IndexByte(arguments.Command, 0) >= 0 {
		return RemoteExecArguments{}, ErrInvalidTool
	}
	if arguments.CWD != "" && (!utf8.ValidString(arguments.CWD) || len([]byte(arguments.CWD)) > MaxCWDBytes || !filepath.IsAbs(arguments.CWD) || strings.IndexByte(arguments.CWD, 0) >= 0) {
		return RemoteExecArguments{}, ErrInvalidTool
	}
	timeoutMS := int(DefaultExecTimeout.Milliseconds())
	if arguments.TimeoutMS != nil {
		timeoutMS = *arguments.TimeoutMS
	}
	if timeoutMS < int(MinimumExecTimeout/time.Millisecond) || timeoutMS > int(MaximumExecTimeout/time.Millisecond) {
		return RemoteExecArguments{}, ErrInvalidTool
	}
	return RemoteExecArguments{Command: arguments.Command, CWD: arguments.CWD, TimeoutMS: timeoutMS}, nil
}

func truncateOutput(stdout, stderr []byte, maximum int) ([]byte, []byte, bool) {
	if len(stdout)+len(stderr) <= maximum {
		return append([]byte(nil), stdout...), append([]byte(nil), stderr...), false
	}
	stdoutLimit := len(stdout)
	if stdoutLimit > maximum {
		stdoutLimit = maximum
	}
	remaining := maximum - stdoutLimit
	stderrLimit := len(stderr)
	if stderrLimit > remaining {
		stderrLimit = remaining
	}
	return append([]byte(nil), stdout[:stdoutLimit]...), append([]byte(nil), stderr[:stderrLimit]...), true
}

func outputExcerpt(result ToolResult) string {
	contents := result.Stdout + result.Stderr
	if result.Failure != nil {
		contents = result.Failure.Code
	}
	bytesValue := []byte(contents)
	if len(bytesValue) > MaxOutputExcerpt {
		bytesValue = bytesValue[:MaxOutputExcerpt]
		for !utf8.Valid(bytesValue) && len(bytesValue) > 0 {
			bytesValue = bytesValue[:len(bytesValue)-1]
		}
	}
	return string(bytesValue)
}

func validateMessage(content string) error {
	if !utf8.ValidString(content) || len([]byte(content)) == 0 || len([]byte(content)) > MaxMessageBytes || strings.TrimSpace(content) == "" {
		return ErrInvalidRequest
	}
	return nil
}

func mapStoreNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func validProvider(provider string) bool {
	if provider == "" {
		return false
	}
	for _, character := range provider {
		if (character < 'a' || character > 'z') && character != '_' && character != '-' && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validateRuntimeProviderMutation(mutation RuntimeProviderMutation) error {
	if len(mutation.Provider) > MaxProviderIDBytes || !validProvider(mutation.Provider) || mutation.ExpectedRevision < 0 ||
		!validOptionalRuntimeText(mutation.DisplayName, MaxDisplayNameBytes) ||
		!validOptionalRuntimeText(mutation.BaseURL, MaxBaseURLBytes) ||
		!validOptionalRuntimeText(mutation.API, MaxReasoningIDBytes) || len(mutation.APIKey) > 4096 ||
		len(mutation.Models) > MaxProviderModels {
		return ErrInvalidRequest
	}
	if !mutation.ModelsOverridden {
		if len(mutation.Models) != 0 {
			return ErrInvalidRequest
		}
		return nil
	}
	seen := make(map[string]struct{}, len(mutation.Models))
	for _, model := range mutation.Models {
		if !validRuntimeText(model.ID, MaxModelIDBytes) || !validOptionalRuntimeText(model.Name, MaxDisplayNameBytes) ||
			model.ContextWindow < 0 || model.MaxTokens < 0 {
			return ErrInvalidRequest
		}
		if _, duplicate := seen[model.ID]; duplicate {
			return ErrInvalidRequest
		}
		seen[model.ID] = struct{}{}
	}
	return nil
}

func validModelSelection(selection ModelSelection) bool {
	return validRuntimeText(selection.Provider, MaxProviderIDBytes) && validRuntimeText(selection.Model, MaxModelIDBytes) &&
		validOptionalRuntimeText(selection.ReasoningEffort, MaxReasoningIDBytes)
}

func validRuntimeText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value
}

func validOptionalRuntimeText(value string, maximum int) bool {
	return value == "" || validRuntimeText(value, maximum)
}

func stableFailureCode(code string) string {
	if code == "" || len(code) > 64 {
		return FailureExecTransport
	}
	for _, character := range code {
		if (character < 'A' || character > 'Z') && character != '_' && (character < '0' || character > '9') {
			return FailureExecTransport
		}
	}
	return code
}

func safeAdapterFailureCode(code string) string {
	switch code {
	case "rate_limited", "unauthorized", "provider_unavailable", "provider_rejected", "protocol_error":
		return code
	default:
		return "provider_error"
	}
}

func safeTurnFailureMessage(code string) string {
	switch code {
	case "rate_limited":
		return "Agent provider is rate limited. Try again later."
	case "unauthorized":
		return "Agent provider authentication failed."
	case "provider_unavailable", "provider_error":
		return "Agent provider is unavailable. Try again."
	case "provider_rejected":
		return "Agent provider rejected the request or model configuration."
	case "protocol_error":
		return "Agent provider returned an unsupported response. Start a new conversation and try again."
	case FailureDeviceOffline:
		return "The remote device is offline."
	case FailureApprovalTimeout:
		return "Command approval timed out."
	case FailureExecTimeout:
		return "Remote command timed out."
	case FailureExecCanceled:
		return "Agent turn was canceled."
	default:
		return "Agent turn failed."
	}
}
