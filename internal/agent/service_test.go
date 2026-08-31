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
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/store"
)

type testOnline struct{ online atomic.Bool }

func (state *testOnline) IsOnline(string) bool { return state.online.Load() }

type adapterFunc func(context.Context, RunRequest, EventSink) error

func (function adapterFunc) Run(ctx context.Context, request RunRequest, sink EventSink) error {
	return function(ctx, request, sink)
}

type recoverablePreflightAdapter struct {
	configured atomic.Bool
	runs       chan RunRequest
}

type runtimeSessionProbe struct {
	mu sync.Mutex

	prepareEntered chan struct{}
	prepareRelease <-chan struct{}
	prepareOnce    sync.Once
	runEntered     chan struct{}
	runRelease     <-chan struct{}
	runOnce        sync.Once

	prepareIDs []string
	modelIDs   []string
	selections []ModelSelection
	runs       []RunRequest
}

func (probe *runtimeSessionProbe) PrepareSession(ctx context.Context, externalID string) (string, error) {
	probe.mu.Lock()
	probe.prepareIDs = append(probe.prepareIDs, externalID)
	probe.mu.Unlock()
	if probe.prepareEntered != nil {
		probe.prepareOnce.Do(func() { close(probe.prepareEntered) })
	}
	if probe.prepareRelease != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-probe.prepareRelease:
		}
	}
	if externalID != "" {
		return externalID, nil
	}
	return "ses_service_models", nil
}

func (probe *runtimeSessionProbe) Models(_ context.Context, externalID string) (ModelDirectory, error) {
	probe.mu.Lock()
	probe.modelIDs = append(probe.modelIDs, externalID)
	probe.mu.Unlock()
	return ModelDirectory{
		Current: ModelSelection{Provider: "provider-one", Model: "model-one"}, Routable: true,
		Groups: []ModelProviderGroup{{ID: "provider-one", Name: "Provider One", Models: []RuntimeModel{{ID: "model-one", Name: "Model One"}}}},
	}, nil
}

func (probe *runtimeSessionProbe) SelectModel(_ context.Context, externalID string, selection ModelSelection) (ModelSelection, error) {
	probe.mu.Lock()
	probe.modelIDs = append(probe.modelIDs, externalID)
	probe.selections = append(probe.selections, selection)
	probe.mu.Unlock()
	return selection, nil
}

func (probe *runtimeSessionProbe) Run(ctx context.Context, request RunRequest, sink EventSink) error {
	probe.mu.Lock()
	probe.runs = append(probe.runs, request)
	probe.mu.Unlock()
	if probe.runEntered != nil {
		probe.runOnce.Do(func() { close(probe.runEntered) })
	}
	if probe.runRelease != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-probe.runRelease:
		}
	}
	return sink.TextDelta(ctx, "runtime response")
}

func (probe *runtimeSessionProbe) snapshot() (prepareIDs, modelIDs []string, selections []ModelSelection, runs []RunRequest) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return append([]string(nil), probe.prepareIDs...), append([]string(nil), probe.modelIDs...),
		append([]ModelSelection(nil), probe.selections...), append([]RunRequest(nil), probe.runs...)
}

func (adapter *recoverablePreflightAdapter) PreflightTurn(context.Context, RunRequest) error {
	if !adapter.configured.Load() {
		return &AdapterError{Code: "credential_required", Err: errors.New("credential is not configured")}
	}
	return nil
}

func (adapter *recoverablePreflightAdapter) Run(ctx context.Context, request RunRequest, sink EventSink) error {
	adapter.runs <- request
	return sink.TextDelta(ctx, "recovered")
}

type auditCapture struct {
	mu     sync.Mutex
	events []store.AuditEvent
}

type failingAgentStore struct {
	*store.Store
	failAssistant bool
	failIdle      bool
}

type approvalBarrierStore struct {
	*store.Store
	committed chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (persistence *approvalBarrierStore) FailPendingAgentToolCall(ctx context.Context, ownerUserID, toolCallID, excerpt string, now time.Time) (store.ToolCall, error) {
	toolCall, err := persistence.Store.FailPendingAgentToolCall(ctx, ownerUserID, toolCallID, excerpt, now)
	persistence.once.Do(func() {
		close(persistence.committed)
		<-persistence.release
	})
	return toolCall, err
}

type launchBarrierStore struct {
	*store.Store
	entered chan struct{}
	once    sync.Once
}

type ownerLookupBarrierStore struct {
	*store.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (persistence *ownerLookupBarrierStore) AgentSessionByOwner(ctx context.Context, ownerUserID, sessionID string) (store.AgentSession, error) {
	session, err := persistence.Store.AgentSessionByOwner(ctx, ownerUserID, sessionID)
	persistence.once.Do(func() {
		close(persistence.entered)
		select {
		case <-persistence.release:
		case <-ctx.Done():
		}
	})
	return session, err
}

func (persistence *launchBarrierStore) CreateAgentMessage(ctx context.Context, ownerUserID string, message store.AgentMessage) error {
	if message.Role != "user" {
		return persistence.Store.CreateAgentMessage(ctx, ownerUserID, message)
	}
	persistence.once.Do(func() { close(persistence.entered) })
	<-ctx.Done()
	return ctx.Err()
}

type injectedAgentStore struct {
	*store.Store
	mu            sync.Mutex
	failLookup    int
	failWaiting   int
	failStart     int
	failAbort     int
	failFinishFor map[string]int
}

func (persistence *injectedAgentStore) take(counter *int) bool {
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if *counter <= 0 {
		return false
	}
	(*counter)--
	return true
}

func (persistence *injectedAgentStore) AgentToolCallByOwner(ctx context.Context, ownerUserID, toolCallID string) (store.ToolCall, error) {
	if persistence.take(&persistence.failLookup) {
		return store.ToolCall{}, errors.New("injected tool lookup failure")
	}
	return persistence.Store.AgentToolCallByOwner(ctx, ownerUserID, toolCallID)
}

func (persistence *injectedAgentStore) UpdateAgentSessionState(ctx context.Context, ownerUserID, sessionID, state string, now time.Time) error {
	if state == store.AgentSessionWaitingApproval && persistence.take(&persistence.failWaiting) {
		return errors.New("injected waiting transition failure")
	}
	return persistence.Store.UpdateAgentSessionState(ctx, ownerUserID, sessionID, state, now)
}

func (persistence *injectedAgentStore) StartAgentToolCall(ctx context.Context, ownerUserID, toolCallID string) error {
	if persistence.take(&persistence.failStart) {
		return errors.New("injected tool start failure")
	}
	return persistence.Store.StartAgentToolCall(ctx, ownerUserID, toolCallID)
}

func (persistence *injectedAgentStore) FailPendingAgentToolCall(ctx context.Context, ownerUserID, toolCallID, excerpt string, now time.Time) (store.ToolCall, error) {
	if persistence.take(&persistence.failAbort) {
		return store.ToolCall{}, errors.New("injected approval abort failure")
	}
	return persistence.Store.FailPendingAgentToolCall(ctx, ownerUserID, toolCallID, excerpt, now)
}

func (persistence *injectedAgentStore) FinishAgentToolCall(ctx context.Context, ownerUserID, toolCallID, status string, exitCode *int, excerpt *string, now time.Time) error {
	persistence.mu.Lock()
	failures := persistence.failFinishFor[status]
	if failures > 0 {
		persistence.failFinishFor[status] = failures - 1
	}
	persistence.mu.Unlock()
	if failures > 0 {
		return errors.New("injected tool finalization failure")
	}
	return persistence.Store.FinishAgentToolCall(ctx, ownerUserID, toolCallID, status, exitCode, excerpt, now)
}

func (persistence *failingAgentStore) CreateAgentMessage(ctx context.Context, ownerUserID string, message store.AgentMessage) error {
	if persistence.failAssistant && message.Role == "assistant" {
		return errors.New("injected assistant persistence failure")
	}
	return persistence.Store.CreateAgentMessage(ctx, ownerUserID, message)
}

func (persistence *failingAgentStore) UpdateAgentSessionState(ctx context.Context, ownerUserID, sessionID, state string, now time.Time) error {
	if persistence.failIdle && state == store.AgentSessionIdle {
		return errors.New("injected idle transition failure")
	}
	return persistence.Store.UpdateAgentSessionState(ctx, ownerUserID, sessionID, state, now)
}

func (capture *auditCapture) CreateAuditEvent(_ context.Context, event store.AuditEvent) error {
	capture.mu.Lock()
	capture.events = append(capture.events, event)
	capture.mu.Unlock()
	return nil
}

type executionCall struct {
	deviceID string
	command  string
	cwd      string
}

type testExecutor struct {
	mu           sync.Mutex
	calls        []executionCall
	results      map[string]RemoteExecution
	err          error
	wait         <-chan struct{}
	ignoreCancel bool
	entered      chan struct{}
	enterOnce    sync.Once
}

func (executor *testExecutor) Exec(ctx context.Context, deviceID, command, cwd string) (RemoteExecution, error) {
	executor.mu.Lock()
	executor.calls = append(executor.calls, executionCall{deviceID: deviceID, command: command, cwd: cwd})
	result := executor.results[command]
	err := executor.err
	executor.mu.Unlock()
	if executor.entered != nil {
		executor.enterOnce.Do(func() { close(executor.entered) })
	}
	if executor.wait != nil {
		if executor.ignoreCancel {
			<-executor.wait
			if err := ctx.Err(); err != nil {
				return RemoteExecution{}, err
			}
		} else {
			select {
			case <-ctx.Done():
				return RemoteExecution{}, ctx.Err()
			case <-executor.wait:
			}
		}
	}
	return result, err
}

func (executor *testExecutor) callCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return len(executor.calls)
}

func waitExecutorCalls(t *testing.T, executor *testExecutor, minimum int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for executor.callCount() < minimum && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls := executor.callCount(); calls < minimum {
		t.Fatalf("executor calls=%d want at least %d", calls, minimum)
	}
}

type serviceFixture struct {
	t        *testing.T
	store    *store.Store
	service  *Service
	executor *testExecutor
	online   *testOnline
	ownerID  string
	deviceID string
}

func newServiceFixture(t *testing.T, adapter Adapter, approvalTimeout time.Duration, logger *slog.Logger) *serviceFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent-service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Date(2026, 8, 13, 4, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_agent", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_agent_service"
	_, err = database.RegisterDevice(ctx, store.Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x45}, 32), OwnerUserID: &ownerID,
		Name: "agent service host", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &testExecutor{results: map[string]RemoteExecution{
		"hostname": {Stdout: []byte("lzr-host\n"), ExitCode: 0},
		"uname -a": {Stdout: []byte("Linux lzr-host test-kernel\n"), ExitCode: 0},
	}}
	online := &testOnline{}
	online.online.Store(true)
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	service, err := NewService(ServiceOptions{
		Store: database, Adapter: adapter, Executor: executor, Online: online, Logger: logger,
		TurnTimeout: time.Second, ApprovalTimeout: approvalTimeout, SubscriberBuffer: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return &serviceFixture{t: t, store: database, service: service, executor: executor, online: online, ownerID: ownerID, deviceID: deviceID}
}

func (fixture *serviceFixture) createSession(mode string) store.AgentSession {
	fixture.t.Helper()
	session, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.deviceID, mode)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return session
}

func waitSnapshot(t *testing.T, service *Service, ownerID, sessionID, state string) store.AgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := service.Snapshot(context.Background(), ownerID, sessionID)
		if err == nil && snapshot.Session.State == state {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, err := service.Snapshot(context.Background(), ownerID, sessionID)
	t.Fatalf("session did not reach %s: snapshot=%#v err=%v", state, snapshot, err)
	return store.AgentSnapshot{}
}

func waitTurnReleased(t *testing.T, service *Service, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for service.IsTurnRunning(sessionID) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if service.IsTurnRunning(sessionID) {
		t.Fatalf("turn %s was not released", sessionID)
	}
}

func waitPending(t *testing.T, service *Service, ownerID, sessionID string) store.ToolCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := service.Snapshot(context.Background(), ownerID, sessionID)
		if err == nil {
			for _, toolCall := range snapshot.ToolCalls {
				if toolCall.Status == store.ToolCallPending {
					return toolCall
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pending tool call not observed")
	return store.ToolCall{}
}

func TestPerCommandApproveOnceCompletesFakeRemoteEvidence(t *testing.T) {
	fixture := newServiceFixture(t, &FakeAdapter{}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "inspect system"); err != nil {
		t.Fatal(err)
	}
	first := waitPending(t, fixture.service, fixture.ownerID, session.ID)
	if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, first.ID, DecisionApproveOnce); err != nil {
		t.Fatal(err)
	}
	second := waitPending(t, fixture.service, fixture.ownerID, session.ID)
	if second.ID == first.ID {
		deadline := time.Now().Add(time.Second)
		for second.ID == first.ID && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			snapshot, _ := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
			for _, candidate := range snapshot.ToolCalls {
				if candidate.Status == store.ToolCallPending && candidate.ID != first.ID {
					second = candidate
				}
			}
		}
	}
	if second.ID == first.ID {
		t.Fatal("second fake tool call did not become pending")
	}
	if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, second.ID, DecisionApproveOnce); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if len(snapshot.Messages) != 2 || !strings.Contains(snapshot.Messages[1].Content, "lzr-host") ||
		!strings.Contains(snapshot.Messages[1].Content, "test-kernel") || !strings.Contains(snapshot.Messages[1].Content, "exit 0") {
		t.Fatalf("fake final response lacks remote evidence: %#v", snapshot.Messages)
	}
	if fixture.executor.callCount() != 2 {
		t.Fatalf("executor calls=%d want 2", fixture.executor.callCount())
	}
	if snapshot.Session.ApprovalMode != store.AgentApprovalPerCommand {
		t.Fatalf("approve_once changed session mode: %s", snapshot.Session.ApprovalMode)
	}
}

func TestApproveSessionUpgradesOnlyCurrentSessionAndDenialSkipsExec(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	adapter := &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
		{Kind: FakeText, Text: "done"},
	}}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	upgraded := fixture.createSession(store.AgentApprovalPerCommand)
	other := fixture.createSession(store.AgentApprovalPerCommand)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, upgraded.ID, "inspect"); err != nil {
		t.Fatal(err)
	}
	pending := waitPending(t, fixture.service, fixture.ownerID, upgraded.ID)
	if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, pending.ID, DecisionApproveSession); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, upgraded.ID, store.AgentSessionIdle)
	if fixture.executor.callCount() != 2 || snapshot.Session.ApprovalMode != store.AgentApprovalFullAccess || len(snapshot.ToolCalls) != 2 {
		t.Fatalf("approve_session flow: calls=%d snapshot=%#v", fixture.executor.callCount(), snapshot)
	}
	otherSnapshot, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, other.ID)
	if err != nil || otherSnapshot.Session.ApprovalMode != store.AgentApprovalPerCommand {
		t.Fatalf("approval leaked to other session: %#v err=%v", otherSnapshot, err)
	}

	denialAdapter := &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
		{Kind: FakeText, Text: "denial handled"},
	}}
	denialFixture := newServiceFixture(t, denialAdapter, time.Second, nil)
	deniedSession := denialFixture.createSession(store.AgentApprovalPerCommand)
	_, _ = denialFixture.service.StartTurn(context.Background(), denialFixture.ownerID, deniedSession.ID, "inspect")
	deniedTool := waitPending(t, denialFixture.service, denialFixture.ownerID, deniedSession.ID)
	if _, err := denialFixture.service.Decide(context.Background(), denialFixture.ownerID, deniedTool.ID, DecisionDeny); err != nil {
		t.Fatal(err)
	}
	deniedSnapshot := waitSnapshot(t, denialFixture.service, denialFixture.ownerID, deniedSession.ID, store.AgentSessionIdle)
	if denialFixture.executor.callCount() != 0 || deniedSnapshot.ToolCalls[0].Status != store.ToolCallDenied {
		t.Fatalf("denial executed or status wrong: calls=%d snapshot=%#v", denialFixture.executor.callCount(), deniedSnapshot)
	}
	if _, err := denialFixture.service.Decide(context.Background(), denialFixture.ownerID, deniedTool.ID, DecisionApproveOnce); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate decision result=%v", err)
	}
}

func TestFullAccessOfflineAndCancelDevice(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	adapter := &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	fixture.online.online.Store(false)
	_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "inspect")
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
	if fixture.executor.callCount() != 0 {
		t.Fatal("offline execution reached executor")
	}

	blocked := make(chan struct{})
	cancelFixture := newServiceFixture(t, adapter, time.Second, nil)
	cancelFixture.executor.wait = blocked
	cancelSession := cancelFixture.createSession(store.AgentApprovalFullAccess)
	_, _ = cancelFixture.service.StartTurn(context.Background(), cancelFixture.ownerID, cancelSession.ID, "inspect")
	deadline := time.Now().Add(time.Second)
	for cancelFixture.executor.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancelFixture.service.CancelDevice(cancelFixture.deviceID)
	waitSnapshot(t, cancelFixture.service, cancelFixture.ownerID, cancelSession.ID, store.AgentSessionFailed)
}

func TestFullAccessStartedEventCarriesValidatedCommandMetadata(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", CWD: "/home/myself", TimeoutMS: 30000})
	adapter := &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "inspect"); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("events closed before tool_call.started")
			}
			if event.Type != EventToolStarted {
				continue
			}
			var payload struct {
				ToolCallID string              `json:"tool_call_id"`
				Name       string              `json:"name"`
				Arguments  RemoteExecArguments `json:"arguments"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ToolCallID == "" || payload.Name != ToolRemoteExec || payload.Arguments.Command != "hostname" ||
				payload.Arguments.CWD != "/home/myself" || payload.Arguments.TimeoutMS != 30000 {
				t.Fatalf("started metadata = %#v", payload)
			}
			return
		case <-deadline.C:
			t.Fatal("tool_call.started was not published")
		}
	}
}

func TestCreateSessionRequiresOnline(t *testing.T) {
	fixture := newServiceFixture(t, &FakeAdapter{}, time.Second, nil)
	fixture.online.online.Store(false)
	if _, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.deviceID, store.AgentApprovalPerCommand); !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("offline session creation result=%v", err)
	}
}

func TestToolValidationLimitTruncationAndTimeout(t *testing.T) {
	valid := func(body string) error {
		_, err := parseRemoteExecArguments(json.RawMessage(body))
		return err
	}
	for _, body := range []string{
		`{"command":""}`, `{"command":"ok","cwd":"relative"}`, `{"command":"ok","timeout_ms":999}`,
		`{"command":"ok","timeout_ms":60001}`, `{"command":"ok","unknown":true}`,
	} {
		if err := valid(body); !errors.Is(err, ErrInvalidTool) {
			t.Fatalf("invalid arguments %s result=%v", body, err)
		}
	}
	if err := valid(`{"command":"ok","cwd":"/tmp","timeout_ms":1000}`); err != nil {
		t.Fatal(err)
	}
	exactCommand := strings.Repeat("c", MaxCommandBytes)
	exactCWD := "/" + strings.Repeat("d", MaxCWDBytes-1)
	exactRaw, _ := json.Marshal(map[string]any{"command": exactCommand, "cwd": exactCWD, "timeout_ms": 60000})
	parsed, err := parseRemoteExecArguments(exactRaw)
	if err != nil || parsed.Command != exactCommand || parsed.CWD != exactCWD {
		t.Fatalf("exact decoded maxima rejected: command=%d cwd=%d raw=%d err=%v", len(exactCommand), len(exactCWD), len(exactRaw), err)
	}
	escapedCommand := strings.Repeat("\x01", MaxCommandBytes)
	escapedCWD := "/" + strings.Repeat("\x01", MaxCWDBytes-1)
	escapedRaw, _ := json.Marshal(map[string]any{"command": escapedCommand, "cwd": escapedCWD, "timeout_ms": 1000})
	parsed, err = parseRemoteExecArguments(escapedRaw)
	if err != nil || parsed.Command != escapedCommand || parsed.CWD != escapedCWD {
		t.Fatalf("escaped decoded maxima rejected: raw=%d err=%v", len(escapedRaw), err)
	}
	tooLongCommand, _ := json.Marshal(map[string]string{"command": strings.Repeat("x", MaxCommandBytes+1)})
	if _, err := parseRemoteExecArguments(tooLongCommand); !errors.Is(err, ErrInvalidTool) {
		t.Fatalf("one-byte command oversize result=%v", err)
	}
	tooLongCWD, _ := json.Marshal(map[string]string{"command": "ok", "cwd": "/" + strings.Repeat("x", MaxCWDBytes)})
	if _, err := parseRemoteExecArguments(tooLongCWD); !errors.Is(err, ErrInvalidTool) {
		t.Fatalf("one-byte cwd oversize result=%v", err)
	}
	stdout := bytes.Repeat([]byte("a"), MaxToolOutputBytes)
	truncatedOut, truncatedErr, truncated := truncateOutput(stdout, []byte("stderr"), MaxToolOutputBytes)
	if !truncated || len(truncatedOut)+len(truncatedErr) != MaxToolOutputBytes {
		t.Fatalf("truncation incorrect: stdout=%d stderr=%d truncated=%v", len(truncatedOut), len(truncatedErr), truncated)
	}

	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 1000})
	timeoutFixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, 20*time.Millisecond, nil)
	timeoutSession := timeoutFixture.createSession(store.AgentApprovalPerCommand)
	_, _ = timeoutFixture.service.StartTurn(context.Background(), timeoutFixture.ownerID, timeoutSession.ID, "timeout")
	waitSnapshot(t, timeoutFixture.service, timeoutFixture.ownerID, timeoutSession.ID, store.AgentSessionFailed)
	if timeoutFixture.executor.callCount() != 0 {
		t.Fatal("approval timeout executed command")
	}
}

func TestConcurrentInvokeSerializesWithoutTurnCountLimit(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	request := ToolRequest{Name: ToolRemoteExec, Arguments: arguments}
	const concurrentCalls = 20
	type outcome struct {
		succeeded int
		other     int
	}
	allAttempting := make(chan struct{})
	outcomes := make(chan outcome, 1)
	adapter := adapterFunc(func(ctx context.Context, runRequest RunRequest, _ EventSink) error {
		start := make(chan struct{})
		errorsByCall := make(chan error, concurrentCalls)
		var ready sync.WaitGroup
		var attempting sync.WaitGroup
		var complete sync.WaitGroup
		ready.Add(concurrentCalls)
		attempting.Add(concurrentCalls)
		complete.Add(concurrentCalls)
		for index := 0; index < concurrentCalls; index++ {
			go func() {
				defer complete.Done()
				ready.Done()
				<-start
				attempting.Done()
				_, err := runRequest.RemoteExec.Invoke(ctx, request)
				errorsByCall <- err
			}()
		}
		ready.Wait()
		close(start)
		attempting.Wait()
		close(allAttempting)
		complete.Wait()
		close(errorsByCall)
		result := outcome{}
		for err := range errorsByCall {
			if err == nil {
				result.succeeded++
			} else {
				result.other++
			}
		}
		outcomes <- result
		return nil
	})
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	fixture.service.turnTimeout = 5 * time.Second
	releaseExecution := make(chan struct{})
	fixture.executor.wait = releaseExecution
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "concurrent limit"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-allAttempting:
	case <-time.After(time.Second):
		t.Fatal("concurrent invocations did not reach the gate")
	}
	waitExecutorCalls(t, fixture.executor, 1)
	if calls := fixture.executor.callCount(); calls != 1 {
		close(releaseExecution)
		t.Fatalf("gate admitted %d concurrent executor calls", calls)
	}
	close(releaseExecution)
	var result outcome
	select {
	case result = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent invocations did not complete")
	}
	if result.succeeded != concurrentCalls || result.other != 0 {
		t.Fatalf("concurrent outcomes=%+v", result)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if fixture.executor.callCount() != concurrentCalls || len(snapshot.ToolCalls) != concurrentCalls {
		t.Fatalf("unbounded serialized tools: executor=%d tools=%d", fixture.executor.callCount(), len(snapshot.ToolCalls))
	}
	for _, toolCall := range snapshot.ToolCalls {
		if toolCall.Status != store.ToolCallCompleted {
			t.Fatalf("admitted tool did not complete: %#v", toolCall)
		}
	}
}

func TestConcurrentInvokeApproveSessionOrdersQueuedCall(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	request := ToolRequest{Name: ToolRemoteExec, Arguments: arguments}
	launchSecond := make(chan struct{})
	secondAttempting := make(chan struct{})
	type outcome struct{ first, second error }
	outcomes := make(chan outcome, 1)
	adapter := adapterFunc(func(ctx context.Context, runRequest RunRequest, _ EventSink) error {
		firstDone := make(chan error, 1)
		go func() {
			_, err := runRequest.RemoteExec.Invoke(ctx, request)
			firstDone <- err
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-launchSecond:
		}
		secondDone := make(chan error, 1)
		go func() {
			close(secondAttempting)
			_, err := runRequest.RemoteExec.Invoke(ctx, request)
			secondDone <- err
		}()
		result := outcome{first: <-firstDone, second: <-secondDone}
		outcomes <- result
		if result.first != nil {
			return result.first
		}
		return result.second
	})
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	fixture.service.turnTimeout = 5 * time.Second
	releaseExecution := make(chan struct{})
	fixture.executor.wait = releaseExecution
	session := fixture.createSession(store.AgentApprovalPerCommand)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "approve session ordering"); err != nil {
		t.Fatal(err)
	}
	pending := waitPending(t, fixture.service, fixture.ownerID, session.ID)
	if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, pending.ID, DecisionApproveSession); err != nil {
		t.Fatal(err)
	}
	waitExecutorCalls(t, fixture.executor, 1)
	close(launchSecond)
	select {
	case <-secondAttempting:
	case <-time.After(time.Second):
		t.Fatal("second invocation did not attempt the occupied gate")
	}
	beforeRelease, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeRelease.ToolCalls) != 1 || fixture.executor.callCount() != 1 {
		close(releaseExecution)
		t.Fatalf("queued invocation crossed occupied gate: executor=%d tools=%#v", fixture.executor.callCount(), beforeRelease.ToolCalls)
	}
	close(releaseExecution)
	var result outcome
	select {
	case result = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("ordered invocations did not complete")
	}
	if result.first != nil || result.second != nil {
		t.Fatalf("ordered invocation errors: first=%v second=%v", result.first, result.second)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if snapshot.Session.ApprovalMode != store.AgentApprovalFullAccess || fixture.executor.callCount() != 2 || len(snapshot.ToolCalls) != 2 {
		t.Fatalf("approve_session ordering: executor=%d snapshot=%#v", fixture.executor.callCount(), snapshot)
	}
	decided := 0
	autoStarted := 0
	for _, toolCall := range snapshot.ToolCalls {
		if toolCall.Status != store.ToolCallCompleted {
			t.Fatalf("ordered tool did not complete: %#v", toolCall)
		}
		if toolCall.Decision == nil {
			autoStarted++
		} else if *toolCall.Decision == DecisionApproveSession {
			decided++
		}
	}
	if decided != 1 || autoStarted != 1 {
		t.Fatalf("queued call did not inherit Full Access: %#v", snapshot.ToolCalls)
	}
}

func TestConcurrentInvokeCanceledGateWaitCreatesNoWork(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	request := ToolRequest{Name: ToolRemoteExec, Arguments: arguments}
	const followupCalls = 16
	launchCanceled := make(chan struct{})
	cancelReady := make(chan context.CancelFunc, 1)
	canceledAttempting := make(chan struct{})
	canceledResults := make(chan error, 1)
	type outcome struct {
		followups int
		err       error
	}
	outcomes := make(chan outcome, 1)
	adapter := adapterFunc(func(ctx context.Context, runRequest RunRequest, _ EventSink) error {
		firstDone := make(chan error, 1)
		go func() {
			_, err := runRequest.RemoteExec.Invoke(ctx, request)
			firstDone <- err
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-launchCanceled:
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancelReady <- cancel
		secondDone := make(chan error, 1)
		go func() {
			close(canceledAttempting)
			_, err := runRequest.RemoteExec.Invoke(canceledCtx, request)
			secondDone <- err
		}()
		canceledErr := <-secondDone
		canceledResults <- canceledErr
		if firstErr := <-firstDone; firstErr != nil {
			outcomes <- outcome{err: firstErr}
			return firstErr
		}
		result := outcome{}
		for index := 0; index < followupCalls; index++ {
			if _, err := runRequest.RemoteExec.Invoke(ctx, request); err != nil {
				result.err = err
				outcomes <- result
				return err
			}
			result.followups++
		}
		outcomes <- result
		return nil
	})
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	fixture.service.turnTimeout = 5 * time.Second
	releaseExecution := make(chan struct{})
	fixture.executor.wait = releaseExecution
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "cancel queued invocation"); err != nil {
		t.Fatal(err)
	}
	waitExecutorCalls(t, fixture.executor, 1)
	close(launchCanceled)
	var cancel context.CancelFunc
	select {
	case cancel = <-cancelReady:
	case <-time.After(time.Second):
		t.Fatal("queued invocation cancel function was not prepared")
	}
	select {
	case <-canceledAttempting:
	case <-time.After(time.Second):
		t.Fatal("cancelable invocation did not attempt the occupied gate")
	}
	cancel()
	select {
	case err := <-canceledResults:
		if !errors.Is(err, context.Canceled) {
			close(releaseExecution)
			t.Fatalf("queued cancellation result=%v", err)
		}
	case <-time.After(time.Second):
		close(releaseExecution)
		t.Fatal("queued invocation did not return promptly on cancellation")
	}
	whileBlocked, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		close(releaseExecution)
		t.Fatal(err)
	}
	if fixture.executor.callCount() != 1 || len(whileBlocked.ToolCalls) != 1 {
		close(releaseExecution)
		t.Fatalf("canceled gate wait created work: executor=%d tools=%#v", fixture.executor.callCount(), whileBlocked.ToolCalls)
	}
	close(releaseExecution)
	var result outcome
	select {
	case result = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up invocations did not complete")
	}
	if result.err != nil || result.followups != followupCalls {
		t.Fatalf("follow-up invocations failed after canceled wait: outcome=%+v", result)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	wantCalls := followupCalls + 1
	if fixture.executor.callCount() != wantCalls || len(snapshot.ToolCalls) != wantCalls {
		t.Fatalf("canceled wait persisted work: executor=%d tools=%d", fixture.executor.callCount(), len(snapshot.ToolCalls))
	}
}

func TestExecutorTimeoutAndEndToEndOutputTruncation(t *testing.T) {
	timeoutArguments, _ := json.Marshal(RemoteExecArguments{Command: "slow", TimeoutMS: 1000})
	timeoutFixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: timeoutArguments}},
	}}, time.Second, nil)
	never := make(chan struct{})
	timeoutFixture.executor.wait = never
	timeoutFixture.service.turnTimeout = 1500 * time.Millisecond
	timeoutSession := timeoutFixture.createSession(store.AgentApprovalFullAccess)
	_, _ = timeoutFixture.service.StartTurn(context.Background(), timeoutFixture.ownerID, timeoutSession.ID, "executor timeout")
	timeoutSnapshot := waitSnapshot(t, timeoutFixture.service, timeoutFixture.ownerID, timeoutSession.ID, store.AgentSessionFailed)
	if timeoutFixture.executor.callCount() != 1 || len(timeoutSnapshot.ToolCalls) != 1 || timeoutSnapshot.ToolCalls[0].Status != store.ToolCallFailed {
		t.Fatalf("executor timeout not persisted: calls=%d snapshot=%#v", timeoutFixture.executor.callCount(), timeoutSnapshot)
	}

	large := bytes.Repeat([]byte("z"), MaxToolOutputBytes+1024)
	truncateArguments, _ := json.Marshal(RemoteExecArguments{Command: "large", TimeoutMS: 30000})
	truncateFixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: truncateArguments}},
		{Kind: FakeText, Text: "adapter consumed truncated result"},
	}}, time.Second, nil)
	truncateFixture.executor.results["large"] = RemoteExecution{Stdout: large, ExitCode: 9}
	truncateSession := truncateFixture.createSession(store.AgentApprovalFullAccess)
	events, unsubscribe, err := truncateFixture.service.Subscribe(context.Background(), truncateFixture.ownerID, truncateSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	_, _ = truncateFixture.service.StartTurn(context.Background(), truncateFixture.ownerID, truncateSession.ID, "truncate")
	truncateSnapshot := waitSnapshot(t, truncateFixture.service, truncateFixture.ownerID, truncateSession.ID, store.AgentSessionIdle)
	if len(truncateSnapshot.ToolCalls) != 1 || truncateSnapshot.ToolCalls[0].ExitCode == nil || *truncateSnapshot.ToolCalls[0].ExitCode != 9 ||
		truncateSnapshot.ToolCalls[0].OutputExcerpt == nil || len([]byte(*truncateSnapshot.ToolCalls[0].OutputExcerpt)) != MaxOutputExcerpt {
		t.Fatalf("truncated tool persistence wrong: %#v", truncateSnapshot.ToolCalls)
	}
	deadline := time.After(time.Second)
	found := false
	for !found {
		select {
		case event := <-events:
			if event.Type == EventToolOutput && bytes.Contains(event.Payload, []byte(`"truncated":true`)) {
				found = true
			}
		case <-deadline:
			t.Fatal("tool output event did not report end-to-end truncation")
		}
	}
}

func TestPendingDecisionAlreadyPersistedIsReconciled(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
		{Kind: FakeText, Text: "done"},
	}}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	// Directly exercise the race closure: persist pending+decision first, then
	// publish the waiter. installPending must load and enqueue that decision.
	toolCall := store.ToolCall{
		ID: "tool_earlydecision", SessionID: session.ID, Name: ToolRemoteExec,
		ArgumentsJSON: string(arguments), Status: store.ToolCallPending, CreatedAt: time.Now().UTC(),
	}
	if err := fixture.store.CreateAgentToolCall(context.Background(), fixture.ownerID, toolCall); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.store.DecideAgentToolCall(context.Background(), fixture.ownerID, toolCall.ID, DecisionApproveOnce, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.service.installPending(&session, toolCall.ID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := fixture.service.waitForDecision(context.Background(), &session, toolCall.ID, pending)
	if err != nil || decision != DecisionApproveOnce {
		t.Fatalf("early decision reconciliation: decision=%q err=%v", decision, err)
	}
}

func TestApprovalTimeoutAndCancellationDurablyBeatLateSessionApproval(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	for _, test := range []struct {
		name   string
		cancel func(*serviceFixture)
	}{
		{name: "timeout"},
		{name: "cancel", cancel: func(fixture *serviceFixture) { fixture.service.CancelDevice(fixture.deviceID) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, 20*time.Millisecond, nil)
			barrier := &approvalBarrierStore{Store: fixture.store, committed: make(chan struct{}), release: make(chan struct{})}
			fixture.service.store = barrier
			session := fixture.createSession(store.AgentApprovalPerCommand)
			if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "race"); err != nil {
				t.Fatal(err)
			}
			pending := waitPending(t, fixture.service, fixture.ownerID, session.ID)
			if test.cancel != nil {
				test.cancel(fixture)
			}
			select {
			case <-barrier.committed:
			case <-time.After(time.Second):
				t.Fatal("durable abort did not reach barrier")
			}
			if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, pending.ID, DecisionApproveSession); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("late approve_session result=%v", err)
			}
			close(barrier.release)
			snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
			waitTurnReleased(t, fixture.service, session.ID)
			if fixture.executor.callCount() != 0 || snapshot.Session.ApprovalMode != store.AgentApprovalPerCommand ||
				len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Status != store.ToolCallFailed || snapshot.ToolCalls[0].Decision != nil {
				t.Fatalf("late approval changed durable state: calls=%d snapshot=%#v", fixture.executor.callCount(), snapshot)
			}
		})
	}
}

func TestDecisionWinnerIsReconciledAfterAbortSignal(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "race"); err != nil {
		t.Fatal(err)
	}
	pending := waitPending(t, fixture.service, fixture.ownerID, session.ID)
	if _, _, err := fixture.store.DecideAgentToolCall(context.Background(), fixture.ownerID, pending.ID, DecisionApproveSession, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.service.CancelDevice(fixture.deviceID)
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
	if fixture.executor.callCount() != 0 || snapshot.Session.ApprovalMode != store.AgentApprovalFullAccess ||
		len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Decision == nil || *snapshot.ToolCalls[0].Decision != DecisionApproveSession || snapshot.ToolCalls[0].Status != store.ToolCallFailed {
		t.Fatalf("decision winner was not reconciled safely: calls=%d snapshot=%#v", fixture.executor.callCount(), snapshot)
	}
}

func TestCloseJoinsTurnsRejectsMutationsAndCleansDurableState(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	adapter := &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}

	t.Run("execution and repeated close", func(t *testing.T) {
		fixture := newServiceFixture(t, adapter, time.Second, nil)
		blocked := make(chan struct{})
		fixture.executor.wait = blocked
		fixture.executor.ignoreCancel = true
		session := fixture.createSession(store.AgentApprovalFullAccess)
		_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "close")
		deadline := time.Now().Add(time.Second)
		for fixture.executor.callCount() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		firstDone := make(chan struct{})
		secondDone := make(chan struct{})
		go func() { fixture.service.Close(); close(firstDone) }()
		go func() { fixture.service.Close(); close(secondDone) }()
		select {
		case <-firstDone:
			t.Fatal("first Close returned before execution cleanup")
		case <-secondDone:
			t.Fatal("concurrent Close returned before execution cleanup")
		case <-time.After(20 * time.Millisecond):
		}
		close(blocked)
		select {
		case <-firstDone:
		case <-time.After(time.Second):
			t.Fatal("Close did not join running turn")
		}
		select {
		case <-secondDone:
		case <-time.After(time.Second):
			t.Fatal("concurrent Close did not share completion boundary")
		}
		snapshot, err := fixture.store.AgentSnapshotByOwner(context.Background(), fixture.ownerID, session.ID)
		if err != nil || snapshot.Session.State != store.AgentSessionFailed || len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Status != store.ToolCallFailed || fixture.service.IsTurnRunning(session.ID) {
			t.Fatalf("execution close cleanup: snapshot=%#v err=%v", snapshot, err)
		}
	})

	t.Run("pending approval", func(t *testing.T) {
		fixture := newServiceFixture(t, adapter, time.Second, nil)
		persistence := &injectedAgentStore{Store: fixture.store, failAbort: 1, failFinishFor: make(map[string]int)}
		fixture.service.store = persistence
		session := fixture.createSession(store.AgentApprovalPerCommand)
		_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "close")
		waitPending(t, fixture.service, fixture.ownerID, session.ID)
		fixture.service.Close()
		snapshot, err := fixture.store.AgentSnapshotByOwner(context.Background(), fixture.ownerID, session.ID)
		if err != nil || snapshot.Session.State != store.AgentSessionFailed || len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Status != store.ToolCallFailed || fixture.service.SubscriberCount() != 0 {
			t.Fatalf("pending close cleanup: snapshot=%#v subscribers=%d err=%v", snapshot, fixture.service.SubscriberCount(), err)
		}
	})

	t.Run("reserve to launch and closed mutation rejection", func(t *testing.T) {
		fixture := newServiceFixture(t, adapter, time.Second, nil)
		barrier := &launchBarrierStore{Store: fixture.store, entered: make(chan struct{})}
		fixture.service.store = barrier
		session := fixture.createSession(store.AgentApprovalFullAccess)
		startDone := make(chan error, 1)
		go func() {
			_, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "close")
			startDone <- err
		}()
		<-barrier.entered
		closeDone := make(chan struct{})
		go func() { fixture.service.Close(); close(closeDone) }()
		select {
		case <-closeDone:
		case <-time.After(time.Second):
			t.Fatal("Close did not cancel/join reserve-to-launch mutation")
		}
		if err := <-startDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("reserve-to-launch result=%v", err)
		}
		if _, err := fixture.service.CreateSession(context.Background(), fixture.ownerID, fixture.deviceID, store.AgentApprovalPerCommand); !errors.Is(err, ErrServiceClosed) {
			t.Fatalf("CreateSession after close result=%v", err)
		}
		if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "late"); !errors.Is(err, ErrServiceClosed) {
			t.Fatalf("StartTurn after close result=%v", err)
		}
		if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, "tool_missing", DecisionDeny); !errors.Is(err, ErrServiceClosed) {
			t.Fatalf("Decide after close result=%v", err)
		}
		snapshot, err := fixture.store.AgentSnapshotByOwner(context.Background(), fixture.ownerID, session.ID)
		if err != nil || snapshot.Session.State != store.AgentSessionFailed || fixture.service.IsTurnRunning(session.ID) {
			t.Fatalf("reserve-to-launch cleanup: snapshot=%#v err=%v", snapshot, err)
		}
	})
}

func TestCancelDeviceWakesPendingApproval(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
	}}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "wait")
	waitPending(t, fixture.service, fixture.ownerID, session.ID)
	fixture.service.CancelDevice(fixture.deviceID)
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
	if fixture.executor.callCount() != 0 {
		t.Fatal("pending cancellation reached executor")
	}
}

func TestOneRunningTurnExternalSessionAndLogAuditRedaction(t *testing.T) {
	secretCommand := "private-command-marker"
	secretOutput := "private-output-marker"
	arguments, _ := json.Marshal(RemoteExecArguments{Command: secretCommand, TimeoutMS: 30000})
	blocked := make(chan struct{})
	var logBuffer bytes.Buffer
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeSetSession, SessionID: "external-test"},
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
	}}, time.Second, slog.New(slog.NewTextHandler(&logBuffer, nil)))
	fixture.executor.results[secretCommand] = RemoteExecution{Stdout: []byte(secretOutput)}
	fixture.executor.wait = blocked
	audits := &auditCapture{}
	fixture.service.auditor = audits
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "second"); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("concurrent turn result=%v", err)
	}
	close(blocked)
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	snapshot, _ := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if snapshot.Session.ExternalSessionID == nil || *snapshot.Session.ExternalSessionID != "external-test" {
		t.Fatalf("external session not persisted: %#v", snapshot.Session)
	}
	if strings.Contains(logBuffer.String(), secretCommand) || strings.Contains(logBuffer.String(), secretOutput) {
		t.Fatalf("logs contain command/output secret: %s", logBuffer.String())
	}
	audits.mu.Lock()
	defer audits.mu.Unlock()
	for _, event := range audits.events {
		if strings.Contains(event.MetadataJSON, secretCommand) || strings.Contains(event.MetadataJSON, secretOutput) {
			t.Fatalf("audit contains command/output secret: %#v", event)
		}
	}
	if len(audits.events) == 0 {
		t.Fatal("agent lifecycle emitted no audit events")
	}
	foundCompletion := false
	for _, event := range audits.events {
		if event.EventType == "agent.tool_completed" {
			foundCompletion = true
		}
	}
	if !foundCompletion {
		t.Fatalf("tool completion audit missing: %#v", audits.events)
	}
}

func TestFailureLogAndAuditRedaction(t *testing.T) {
	secretCommand := "failure-command-marker"
	secretOutput := "failure-output-marker"
	arguments, _ := json.Marshal(RemoteExecArguments{Command: secretCommand, TimeoutMS: 30000})
	var logs bytes.Buffer
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{
		{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}},
	}}, time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
	audits := &auditCapture{}
	fixture.service.auditor = audits
	fixture.executor.err = errors.New(secretOutput)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "fail")
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
	waitTurnReleased(t, fixture.service, session.ID)
	if strings.Contains(logs.String(), secretCommand) || strings.Contains(logs.String(), secretOutput) {
		t.Fatalf("failure logs contain secret: %s", logs.String())
	}
	audits.mu.Lock()
	defer audits.mu.Unlock()
	foundFailure := false
	for _, event := range audits.events {
		if strings.Contains(event.MetadataJSON, secretCommand) || strings.Contains(event.MetadataJSON, secretOutput) {
			t.Fatalf("failure audit contains secret: %#v", event)
		}
		if event.EventType == "agent.tool_failed" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("tool failure audit missing: %#v", audits.events)
	}
}

func TestAdapterTypedFailureCodeAndRedaction(t *testing.T) {
	secret := "provider-secret-detail"
	var logs bytes.Buffer
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeFail, Err: &AdapterError{
		Code: "rate_limited", Err: errors.New(secret),
	}}}}, time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
	session := fixture.createSession(store.AgentApprovalFullAccess)
	events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "provider failure")
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
	waitTurnReleased(t, fixture.service, session.ID)
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("adapter error detail leaked to log: %s", logs.String())
	}
	found := false
	deadline := time.After(time.Second)
	for !found {
		select {
		case event := <-events:
			if event.Type == EventTurnFailed && bytes.Contains(event.Payload, []byte(`"code":"rate_limited"`)) &&
				bytes.Contains(event.Payload, []byte(`"message":"Agent provider is rate limited. Try again later."`)) &&
				!bytes.Contains(event.Payload, []byte(secret)) {
				found = true
			}
		case <-deadline:
			t.Fatal("typed adapter failure event missing")
		}
	}
}

func TestSafeAdapterFailureMessagesStaySpecificAndBounded(t *testing.T) {
	tests := []struct {
		code    string
		message string
	}{
		{code: "rate_limited", message: "Agent provider is rate limited. Try again later."},
		{code: "unauthorized", message: "Agent provider authentication failed."},
		{code: "provider_unavailable", message: "Agent provider is unavailable. Try again."},
		{code: "provider_rejected", message: "Agent provider rejected the request or model configuration."},
		{code: "protocol_error", message: "Agent provider returned an unsupported response. Start a new conversation and try again."},
	}
	for _, test := range tests {
		if got := safeAdapterFailureCode(test.code); got != test.code {
			t.Fatalf("safe code %q = %q", test.code, got)
		}
		if got := safeTurnFailureMessage(test.code); got != test.message {
			t.Fatalf("safe message %q = %q", test.code, got)
		}
	}
	if got := safeAdapterFailureCode("provider-secret-detail"); got != "provider_error" {
		t.Fatalf("unknown Adapter code escaped safe fallback: %q", got)
	}
}

func TestHubDropsSlowSubscriberAndCleansCancel(t *testing.T) {
	hub := newEventHub(1)
	events, cancel, err := hub.subscribe("ags_test")
	if err != nil {
		t.Fatal(err)
	}
	event := Event{ID: "evt_test", SessionID: "ags_test", CreatedAt: time.Now(), Type: EventSessionState, Payload: json.RawMessage(`{"state":"running"}`)}
	hub.publish(event)
	hub.publish(event)
	if hub.count("ags_test") != 0 {
		t.Fatal("slow subscriber was not removed")
	}
	if _, ok := <-events; !ok {
		t.Fatal("buffered first event unexpectedly missing")
	}
	if _, ok := <-events; ok {
		t.Fatal("slow subscriber channel not closed")
	}
	cancel()

	_, cancelSecond, err := hub.subscribe("ags_second")
	if err != nil {
		t.Fatal(err)
	}
	cancelSecond()
	if hub.count("ags_second") != 0 {
		t.Fatal("subscriber cancel did not clean hub")
	}
	hub.close()
}

func TestStartTurnLookupBeforeRevocationCannotReserve(t *testing.T) {
	var adapterCalls atomic.Int32
	fixture := newServiceFixture(t, adapterFunc(func(context.Context, RunRequest, EventSink) error {
		adapterCalls.Add(1)
		return nil
	}), time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	barrier := &ownerLookupBarrierStore{
		Store: fixture.store, entered: make(chan struct{}), release: make(chan struct{}),
	}
	fixture.service.store = barrier

	result := make(chan error, 1)
	go func() {
		_, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "must not run")
		result <- err
	}()
	<-barrier.entered
	unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	close(barrier.release)
	if err := <-result; !errors.Is(err, ErrNotFound) {
		t.Fatalf("StartTurn after lookup/commit race = %v", err)
	}
	if adapterCalls.Load() != 0 || fixture.service.IsTurnRunning(session.ID) {
		t.Fatalf("revoked turn admitted: adapter=%d running=%v", adapterCalls.Load(), fixture.service.IsTurnRunning(session.ID))
	}
}

func TestMarkRevokedCancelsStartedToolBeforeExecutorAfterSameOwnerRepair(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, time.Second, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	executeContext := make(chan context.Context, 1)
	var once sync.Once
	fixture.service.beforeExecute = func(ctx context.Context) {
		executeContext <- ctx
		once.Do(func() { close(entered) })
		<-release
	}
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	<-entered
	unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	select {
	case ctx := <-executeContext:
		select {
		case <-ctx.Done():
		default:
			t.Fatal("MarkDeviceRevoked returned before the running Turn context was canceled")
		}
	default:
		t.Fatal("execute barrier did not expose the running Turn context")
	}
	// Model a same-owner re-pair and a fresh online Tunnel before the old Turn
	// resumes. The runtime mark/cancel must still prevent executor admission.
	repairNow := time.Now().UTC()
	repairDigest := bytes.Repeat([]byte{0x79}, 32)
	if err := fixture.store.CreatePairingCode(context.Background(), store.PairingCode{
		ID: "pair_agent_repair", DeviceID: fixture.deviceID, CodeDigest: repairDigest,
		CreatedAt: repairNow, ExpiresAt: repairNow.Add(10 * time.Minute),
	}, repairNow); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ClaimPairing(context.Background(), fixture.ownerID, repairDigest, repairNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DeviceByOwner(context.Background(), fixture.ownerID, fixture.deviceID); err != nil {
		t.Fatalf("same-owner repair did not restore Device ownership: %v", err)
	}
	fixture.online.online.Store(true)
	if !fixture.online.IsOnline(fixture.deviceID) {
		t.Fatal("same-owner repaired Device was not online before releasing execute barrier")
	}
	close(release)
	if err := fixture.service.InvalidateDevice(context.Background(), fixture.deviceID, unpaired.RevokedAgentSessionIDs); err != nil {
		t.Fatal(err)
	}
	if fixture.executor.callCount() != 0 {
		t.Fatalf("revoked started tool reached repaired Device executor %d times", fixture.executor.callCount())
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-owner repair revived old Session after Turn finalizer: %v", err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "revive"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-owner repair admitted old Session: %v", err)
	}
}

func TestInvalidateDeviceClosesSSEAndJoinsRunningTurnWithoutFinalStateOverwrite(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, time.Second, nil)
	blocked := make(chan struct{})
	fixture.executor.wait = blocked
	fixture.executor.ignoreCancel = true
	fixture.executor.entered = make(chan struct{})
	session := fixture.createSession(store.AgentApprovalFullAccess)
	events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	<-fixture.executor.entered

	unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	joined := make(chan error, 1)
	go func() {
		joined <- fixture.service.InvalidateDevice(context.Background(), fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	}()
	select {
	case err := <-joined:
		t.Fatalf("InvalidateDevice returned before running Turn finished: %v", err)
	default:
	}
	close(blocked)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	if fixture.service.IsTurnRunning(session.ID) {
		t.Fatal("running Turn remained after joined invalidation")
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked snapshot = %v", err)
	}
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		default:
			t.Fatal("revoked SSE subscriber was not closed")
		}
	}
}

func TestSubscribeLookupRaceAndPendingApprovalInvalidation(t *testing.T) {
	t.Run("lookup before mark cannot register", func(t *testing.T) {
		fixture := newServiceFixture(t, &FakeAdapter{}, time.Second, nil)
		session := fixture.createSession(store.AgentApprovalPerCommand)
		barrier := &ownerLookupBarrierStore{
			Store: fixture.store, entered: make(chan struct{}), release: make(chan struct{}),
		}
		fixture.service.store = barrier
		result := make(chan error, 1)
		go func() {
			_, _, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
			result <- err
		}()
		<-barrier.entered
		unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
		close(barrier.release)
		if err := <-result; !errors.Is(err, ErrNotFound) {
			t.Fatalf("Subscribe lookup/mark race = %v", err)
		}
		if fixture.service.SubscriberCount() != 0 {
			t.Fatal("revoked Subscribe left a subscriber")
		}
	})

	t.Run("pending approval is canceled joined and hidden", func(t *testing.T) {
		arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
		fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, time.Second, nil)
		session := fixture.createSession(store.AgentApprovalPerCommand)
		events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer unsubscribe()
		if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "approve"); err != nil {
			t.Fatal(err)
		}
		var toolID string
		for toolID == "" {
			event, open := <-events
			if !open {
				t.Fatal("events closed before pending approval")
			}
			if event.Type == EventToolPending {
				var payload struct {
					ToolCallID string `json:"tool_call_id"`
				}
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				toolID = payload.ToolCallID
			}
		}
		unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
		if err := fixture.service.InvalidateDevice(context.Background(), fixture.deviceID, unpaired.RevokedAgentSessionIDs); err != nil {
			t.Fatal(err)
		}
		if fixture.executor.callCount() != 0 || fixture.service.IsTurnRunning(session.ID) {
			t.Fatal("pending revoked command executed or remained running")
		}
		if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, toolID, DecisionApproveSession); !errors.Is(err, ErrNotFound) {
			t.Fatalf("decision after revoke = %v", err)
		}
	})
}

func TestInvalidateDeviceTimeoutCanBeRetriedAndJoined(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, time.Second, nil)
	blocked := make(chan struct{})
	fixture.executor.wait = blocked
	fixture.executor.ignoreCancel = true
	fixture.executor.entered = make(chan struct{})
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	<-fixture.executor.entered
	unpaired, err := fixture.store.UnpairDevice(context.Background(), fixture.ownerID, fixture.deviceID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.MarkDeviceRevoked(fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fixture.service.InvalidateDevice(canceled, fixture.deviceID, unpaired.RevokedAgentSessionIDs); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled invalidation = %v", err)
	}
	joined := make(chan error, 1)
	go func() {
		joined <- fixture.service.InvalidateDevice(context.Background(), fixture.deviceID, unpaired.RevokedAgentSessionIDs)
	}()
	select {
	case err := <-joined:
		t.Fatalf("retry returned before Turn completion: %v", err)
	default:
	}
	close(blocked)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
}

func TestProviderIsConfigurableAndValidated(t *testing.T) {
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeText, Text: "ok"}}}, time.Second, nil)
	configured, err := NewService(ServiceOptions{
		Store: fixture.store, Adapter: &FakeAdapter{}, Provider: "opencode",
		Executor: fixture.executor, Online: fixture.online,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer configured.Close()
	session, err := configured.CreateSession(context.Background(), fixture.ownerID, fixture.deviceID, store.AgentApprovalPerCommand)
	if err != nil || session.Provider != "opencode" {
		t.Fatalf("configured provider: session=%#v err=%v", session, err)
	}
	if _, err := NewService(ServiceOptions{
		Store: fixture.store, Adapter: &FakeAdapter{}, Provider: "bad/provider",
		Executor: fixture.executor, Online: fixture.online,
	}); err == nil {
		t.Fatal("invalid provider was accepted")
	}
}

func TestCredentialPreflightDoesNotPersistAndConfiguredRetryReusesSession(t *testing.T) {
	adapter := &recoverablePreflightAdapter{runs: make(chan RunRequest, 1)}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	const externalID = "ses_existing_dsh_conversation"
	if err := fixture.store.UpdateAgentExternalSessionID(context.Background(), fixture.ownerID, session.ID, externalID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.UpdateAgentSessionState(context.Background(), fixture.ownerID, session.ID, store.AgentSessionFailed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "retry me"); err == nil {
		t.Fatal("missing credential accepted a Turn")
	} else {
		var adapterError *AdapterError
		if !errors.As(err, &adapterError) || adapterError.Code != "credential_required" {
			t.Fatalf("missing credential error=%v", err)
		}
	}
	missingSnapshot, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if missingSnapshot.Session.State != store.AgentSessionFailed || len(missingSnapshot.Messages) != 0 || len(missingSnapshot.ToolCalls) != 0 {
		t.Fatalf("preflight failure persisted a Turn: %#v", missingSnapshot)
	}

	adapter.configured.Store(true)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "retry me"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if snapshot.Session.ID != session.ID || len(snapshot.Messages) != 2 || snapshot.Messages[0].Content != "retry me" || snapshot.Messages[1].Content != "recovered" {
		t.Fatalf("configured retry replaced or lost the conversation: %#v", snapshot)
	}
	select {
	case request := <-adapter.runs:
		if request.SessionID != session.ID || request.ExternalSessionID != externalID {
			t.Fatalf("configured retry request=%#v", request)
		}
	default:
		t.Fatal("configured retry never reached the Adapter")
	}
}

func TestRuntimeSessionModelsPersistOpaqueIDAndSelectionFeedsTheNextTurn(t *testing.T) {
	adapter := &runtimeSessionProbe{}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	if err := fixture.service.SetAdapter(ProviderDSH, adapter); err != nil {
		t.Fatal(err)
	}
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.SelectSessionModel(context.Background(), fixture.ownerID, session.ID, ModelSelection{
		Provider: "provider-one", Model: " model-one",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid model selection error=%v", err)
	}
	before, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil || before.Session.ExternalSessionID != nil {
		t.Fatalf("invalid selection prepared a Runtime Session: external=%#v err=%v", before.Session.ExternalSessionID, err)
	}
	directory, err := fixture.service.SessionModels(context.Background(), fixture.ownerID, session.ID)
	if err != nil || directory.Current.Model != "model-one" {
		t.Fatalf("models=%#v err=%v", directory, err)
	}
	snapshot, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil || snapshot.Session.ExternalSessionID == nil || *snapshot.Session.ExternalSessionID != "ses_service_models" {
		t.Fatalf("persisted Runtime Session=%#v err=%v", snapshot.Session.ExternalSessionID, err)
	}
	selection := ModelSelection{Provider: "provider-one", Model: "model-one"}
	selected, err := fixture.service.SelectSessionModel(context.Background(), fixture.ownerID, session.ID, selection)
	if err != nil || selected != selection {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "use selected model"); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	prepareIDs, modelIDs, selections, runs := adapter.snapshot()
	if len(prepareIDs) != 3 || prepareIDs[0] != "" || prepareIDs[1] != "ses_service_models" || prepareIDs[2] != "ses_service_models" ||
		len(modelIDs) != 2 || modelIDs[0] != "ses_service_models" || modelIDs[1] != "ses_service_models" ||
		len(selections) != 1 || selections[0] != selection || len(runs) != 1 || runs[0].ExternalSessionID != "ses_service_models" {
		t.Fatalf("prepare=%v models=%v selections=%#v runs=%#v", prepareIDs, modelIDs, selections, runs)
	}
}

func TestRuntimeProviderMutationCommonBounds(t *testing.T) {
	valid := RuntimeProviderMutation{
		Provider: "provider-one", ExpectedRevision: 1, BaseURL: "https://provider.example/v1",
		ModelsOverridden: true, Models: []RuntimeProviderModel{{ID: "model-one", ContextWindow: 8192}},
	}
	if err := validateRuntimeProviderMutation(valid); err != nil {
		t.Fatalf("valid mutation error=%v", err)
	}
	for _, mutation := range []RuntimeProviderMutation{
		{Provider: "provider-one", ExpectedRevision: -1},
		{Provider: "provider-one", ExpectedRevision: 1, Models: []RuntimeProviderModel{{ID: "model-one"}}},
		{Provider: "provider-one", ExpectedRevision: 1, ModelsOverridden: true, Models: []RuntimeProviderModel{{ID: "duplicate"}, {ID: "duplicate"}}},
		{Provider: "provider-one", ExpectedRevision: 1, APIKey: strings.Repeat("k", 4097)},
	} {
		if err := validateRuntimeProviderMutation(mutation); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid mutation accepted: %#v err=%v", mutation, err)
		}
	}
}

func TestSessionModelMutationAndTurnAdmissionAreMutuallyExclusive(t *testing.T) {
	prepareEntered := make(chan struct{})
	prepareRelease := make(chan struct{})
	adapter := &runtimeSessionProbe{prepareEntered: prepareEntered, prepareRelease: prepareRelease}
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	if err := fixture.service.SetAdapter(ProviderDSH, adapter); err != nil {
		t.Fatal(err)
	}
	session := fixture.createSession(store.AgentApprovalFullAccess)
	modelsDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.SessionModels(context.Background(), fixture.ownerID, session.ID)
		modelsDone <- err
	}()
	select {
	case <-prepareEntered:
	case <-time.After(time.Second):
		t.Fatal("model preparation did not enter")
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "must not race"); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("Turn admitted during model preparation: %v", err)
	}
	blockedSnapshot, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID)
	if err != nil || len(blockedSnapshot.Messages) != 0 {
		t.Fatalf("blocked Turn persisted messages: %#v err=%v", blockedSnapshot.Messages, err)
	}
	close(prepareRelease)
	if err := <-modelsDone; err != nil {
		t.Fatal(err)
	}

	runEntered := make(chan struct{})
	runRelease := make(chan struct{})
	adapter.runEntered = runEntered
	adapter.runRelease = runRelease
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "hold the Turn"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runEntered:
	case <-time.After(time.Second):
		t.Fatal("Runtime Turn did not enter")
	}
	if _, err := fixture.service.SessionModels(context.Background(), fixture.ownerID, session.ID); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("model operation admitted during Turn: %v", err)
	}
	close(runRelease)
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
}

func TestSessionPermissionUpdateIsAuthoritativeAndConflictsWithRunningTurn(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{
		Name: ToolRemoteExec, Arguments: arguments,
	}}}}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	updated, err := fixture.service.UpdateSessionApprovalMode(context.Background(), fixture.ownerID, session.ID, store.AgentApprovalFullAccess)
	if err != nil || updated.ApprovalMode != store.AgentApprovalFullAccess {
		t.Fatalf("permission update=%#v err=%v", updated, err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "run without another prompt"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if fixture.executor.callCount() != 1 || len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Decision != nil ||
		snapshot.ToolCalls[0].Status != store.ToolCallCompleted {
		t.Fatalf("Full Access did not execute directly: calls=%d snapshot=%#v", fixture.executor.callCount(), snapshot)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := adapterFunc(func(ctx context.Context, _ RunRequest, sink EventSink) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return sink.TextDelta(ctx, "done")
		}
	})
	racing := newServiceFixture(t, blocking, time.Second, nil)
	running := racing.createSession(store.AgentApprovalPerCommand)
	if _, err := racing.service.StartTurn(context.Background(), racing.ownerID, running.ID, "hold"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Turn did not reach Adapter")
	}
	if _, err := racing.service.UpdateSessionApprovalMode(context.Background(), racing.ownerID, running.ID, store.AgentApprovalFullAccess); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("running permission update=%v", err)
	}
	close(release)
	runningSnapshot := waitSnapshot(t, racing.service, racing.ownerID, running.ID, store.AgentSessionIdle)
	if runningSnapshot.Session.ApprovalMode != store.AgentApprovalPerCommand {
		t.Fatalf("conflicting permission update leaked into Turn: %#v", runningSnapshot.Session)
	}
}

func TestArchiveClosesSubscribersAndRestoreDeleteKeepOwnershipBoundary(t *testing.T) {
	fixture := newServiceFixture(t, &FakeAdapter{}, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalPerCommand)
	events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	archived, err := fixture.service.SetSessionArchived(context.Background(), fixture.ownerID, session.ID, true)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive=%#v err=%v", archived, err)
	}
	select {
	case _, open := <-events:
		if open {
			t.Fatal("archiving left an SSE subscriber open")
		}
	case <-time.After(time.Second):
		t.Fatal("archiving did not close the SSE subscriber")
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived Session remained on active surface: %v", err)
	}
	restored, err := fixture.service.SetSessionArchived(context.Background(), fixture.ownerID, session.ID, false)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID); err != nil {
		t.Fatalf("restored Session unavailable: %v", err)
	}
	if err := fixture.service.DeleteSession(context.Background(), fixture.ownerID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Session remained visible: %v", err)
	}
}

func TestSetAdapterBindsNewSessionsWithoutSwitchingExistingProviders(t *testing.T) {
	oldRuns := make(chan string, 1)
	newRuns := make(chan string, 1)
	oldAdapter := adapterFunc(func(ctx context.Context, request RunRequest, sink EventSink) error {
		oldRuns <- request.SessionID
		return sink.TextDelta(ctx, "old provider")
	})
	newAdapter := adapterFunc(func(ctx context.Context, request RunRequest, sink EventSink) error {
		newRuns <- request.SessionID
		return sink.TextDelta(ctx, "new provider")
	})
	fixture := newServiceFixture(t, oldAdapter, time.Second, nil)
	oldSession := fixture.createSession(store.AgentApprovalFullAccess)
	if oldSession.Provider != ProviderFake {
		t.Fatalf("old session provider=%q", oldSession.Provider)
	}
	if err := fixture.service.SetAdapter(ProviderDeepSeek, newAdapter); err != nil {
		t.Fatal(err)
	}
	newSession := fixture.createSession(store.AgentApprovalFullAccess)
	if newSession.Provider != ProviderDeepSeek {
		t.Fatalf("new session provider=%q", newSession.Provider)
	}

	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, oldSession.ID, "use the old provider"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, newSession.ID, "use the new provider"); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, fixture.service, fixture.ownerID, oldSession.ID, store.AgentSessionIdle)
	waitSnapshot(t, fixture.service, fixture.ownerID, newSession.ID, store.AgentSessionIdle)
	select {
	case sessionID := <-oldRuns:
		if sessionID != oldSession.ID {
			t.Fatalf("old Adapter ran session %q", sessionID)
		}
	default:
		t.Fatal("old session did not retain its provider Adapter")
	}
	select {
	case sessionID := <-newRuns:
		if sessionID != newSession.ID {
			t.Fatalf("new Adapter ran session %q", sessionID)
		}
	default:
		t.Fatal("new session did not use the configured provider Adapter")
	}
}

func TestSetAdapterValidationCloseAndUnavailablePersistedProvider(t *testing.T) {
	fixture := newServiceFixture(t, &FakeAdapter{}, time.Second, nil)
	if err := fixture.service.SetAdapter("bad/provider", &FakeAdapter{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid provider error=%v", err)
	}
	if err := fixture.service.SetAdapter(ProviderDeepSeek, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil Adapter error=%v", err)
	}

	now := time.Now().UTC()
	missing := store.AgentSession{
		ID: "ags_missing_provider", UserID: fixture.ownerID, DeviceID: fixture.deviceID,
		ApprovalMode: store.AgentApprovalFullAccess, Provider: "opencode", State: store.AgentSessionIdle,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.store.CreateAgentSession(context.Background(), missing); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, missing.ID, "run safely"); err == nil {
		t.Fatal("unavailable persisted provider accepted a new Turn")
	} else {
		var adapterError *AdapterError
		if !errors.As(err, &adapterError) || adapterError.Code != "provider_unavailable" {
			t.Fatalf("unavailable provider error=%v", err)
		}
	}
	snapshot, err := fixture.service.Snapshot(context.Background(), fixture.ownerID, missing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.State != store.AgentSessionIdle || len(snapshot.Messages) != 0 {
		t.Fatalf("unavailable provider mutated persisted Turn: %#v", snapshot)
	}

	fixture.service.Close()
	if err := fixture.service.SetAdapter(ProviderDeepSeek, &FakeAdapter{}); !errors.Is(err, ErrServiceClosed) {
		t.Fatalf("closed service error=%v", err)
	}
}

func TestReasoningAndAssistantTranscriptStaySeparateAndResumeLatest(t *testing.T) {
	adapter := adapterFunc(func(ctx context.Context, _ RunRequest, sink EventSink) error {
		if err := sink.ReasoningDelta(ctx, "inspect the remote state"); err != nil {
			return err
		}
		return sink.TextDelta(ctx, "The remote state is healthy.")
	})
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "check it"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
	if len(snapshot.Messages) != 3 || snapshot.Messages[0].Role != "user" || snapshot.Messages[1].Role != "reasoning" ||
		snapshot.Messages[1].Content != "inspect the remote state" || snapshot.Messages[2].Role != "assistant" ||
		snapshot.Messages[2].Content != "The remote state is healthy." {
		t.Fatalf("reasoning and answer transcript merged: %#v", snapshot.Messages)
	}
	latest, err := fixture.service.LatestSnapshot(context.Background(), fixture.ownerID, fixture.deviceID)
	if err != nil || latest.Session.ID != session.ID || len(latest.Messages) != 3 {
		t.Fatalf("latest snapshot=%#v err=%v", latest, err)
	}
}

func TestRunRequestHistoryIsOwnerScopedBoundedAndExcludesReasoning(t *testing.T) {
	requests := make(chan RunRequest, 1)
	adapter := adapterFunc(func(ctx context.Context, request RunRequest, sink EventSink) error {
		requests <- request
		return sink.TextDelta(ctx, "bounded history accepted")
	})
	fixture := newServiceFixture(t, adapter, time.Second, nil)
	session := fixture.createSession(store.AgentApprovalFullAccess)
	baseTime := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	for index := 0; index < 70; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		if err := fixture.store.CreateAgentMessage(context.Background(), fixture.ownerID, store.AgentMessage{
			ID: fmt.Sprintf("msg_history_%03d", index), SessionID: session.ID, Role: role,
			Content: fmt.Sprintf("%03d:%s", index, strings.Repeat("h", 4996)), CreatedAt: baseTime.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.CreateAgentMessage(context.Background(), fixture.ownerID, store.AgentMessage{
			ID: fmt.Sprintf("msg_reasoning_%03d", index), SessionID: session.ID, Role: "reasoning",
			Content: "provider-private reasoning", CreatedAt: baseTime.Add(time.Duration(index)*time.Second + time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "current request"); err != nil {
		t.Fatal(err)
	}
	var request RunRequest
	select {
	case request = <-requests:
	case <-time.After(time.Second):
		t.Fatal("Adapter did not receive bounded history")
	}
	if len(request.History) == 0 || len(request.History) > MaxConversationHistoryMessages {
		t.Fatalf("history messages = %d", len(request.History))
	}
	totalBytes := 0
	for _, message := range request.History {
		if message.Role != "user" && message.Role != "assistant" {
			t.Fatalf("provider history retained role %q", message.Role)
		}
		totalBytes += len([]byte(message.Content))
	}
	if totalBytes > MaxConversationHistoryBytes {
		t.Fatalf("history bytes = %d", totalBytes)
	}
	last := request.History[len(request.History)-1]
	if last.Role != "user" || last.Content != "current request" || request.UserText != "current request" {
		t.Fatalf("history/current request mismatch: last=%#v text=%q", last, request.UserText)
	}
	if request.Target.Platform != "linux" || request.Target.Arch != "amd64" {
		t.Fatalf("adapter target metadata = %#v", request.Target)
	}
	waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionIdle)
}

func TestAssistantPersistenceAndCompletionUpdateFailuresBecomeFailed(t *testing.T) {
	for _, test := range []struct {
		name          string
		failAssistant bool
		failIdle      bool
	}{
		{name: "assistant message", failAssistant: true},
		{name: "completion state", failIdle: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeText, Text: "answer"}}}, time.Second, nil)
			persistence := &failingAgentStore{Store: fixture.store, failAssistant: test.failAssistant, failIdle: test.failIdle}
			service, err := NewService(ServiceOptions{
				Store: persistence, Adapter: &FakeAdapter{Steps: []FakeStep{{Kind: FakeText, Text: "answer"}}},
				Executor: fixture.executor, Online: fixture.online,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			session, err := service.CreateSession(context.Background(), fixture.ownerID, fixture.deviceID, store.AgentApprovalFullAccess)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = service.StartTurn(context.Background(), fixture.ownerID, session.ID, "run")
			waitSnapshot(t, service, fixture.ownerID, session.ID, store.AgentSessionFailed)
		})
	}
}

func TestPostCreateToolFailuresAlwaysTerminalizeDurably(t *testing.T) {
	arguments, _ := json.Marshal(RemoteExecArguments{Command: "hostname", TimeoutMS: 30000})
	for _, test := range []struct {
		name       string
		mode       string
		decision   string
		configure  func(*injectedAgentStore, *serviceFixture)
		wantStatus string
	}{
		{
			name: "pending registration lookup", mode: store.AgentApprovalPerCommand, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) { persistence.failLookup = 1 },
		},
		{
			name: "waiting transition", mode: store.AgentApprovalPerCommand, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) { persistence.failWaiting = 1 },
		},
		{
			name: "approval abort transient write", mode: store.AgentApprovalPerCommand, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) { persistence.failAbort = 1 },
		},
		{
			name: "start transition", mode: store.AgentApprovalPerCommand, decision: DecisionApproveOnce, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) { persistence.failStart = 1 },
		},
		{
			name: "denial finalization compensation", mode: store.AgentApprovalPerCommand, decision: DecisionDeny, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) {
				persistence.failFinishFor[store.ToolCallDenied] = 2
			},
		},
		{
			name: "execution failure finalization retry", mode: store.AgentApprovalFullAccess, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, fixture *serviceFixture) {
				persistence.failFinishFor[store.ToolCallFailed] = 1
				fixture.executor.err = errors.New("transport failure")
			},
		},
		{
			name: "success finalization compensation", mode: store.AgentApprovalFullAccess, wantStatus: store.ToolCallFailed,
			configure: func(persistence *injectedAgentStore, _ *serviceFixture) {
				persistence.failFinishFor[store.ToolCallCompleted] = 2
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t, &FakeAdapter{Steps: []FakeStep{{Kind: FakeTool, Tool: ToolRequest{Name: ToolRemoteExec, Arguments: arguments}}}}, 20*time.Millisecond, nil)
			persistence := &injectedAgentStore{Store: fixture.store, failFinishFor: make(map[string]int)}
			test.configure(persistence, fixture)
			fixture.service.store = persistence
			session := fixture.createSession(test.mode)
			events, unsubscribe, err := fixture.service.Subscribe(context.Background(), fixture.ownerID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer unsubscribe()
			_, _ = fixture.service.StartTurn(context.Background(), fixture.ownerID, session.ID, "failure")
			if test.decision != "" {
				pending := waitPending(t, fixture.service, fixture.ownerID, session.ID)
				if _, err := fixture.service.Decide(context.Background(), fixture.ownerID, pending.ID, test.decision); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := waitSnapshot(t, fixture.service, fixture.ownerID, session.ID, store.AgentSessionFailed)
			waitTurnReleased(t, fixture.service, session.ID)
			if len(snapshot.ToolCalls) != 1 || snapshot.ToolCalls[0].Status != test.wantStatus || snapshot.ToolCalls[0].CompletedAt == nil {
				t.Fatalf("non-terminal tool after injected failure: %#v", snapshot.ToolCalls)
			}
			for {
				select {
				case event := <-events:
					if event.Type == EventToolCompleted && !bytes.Contains(event.Payload, []byte(`"status":"`+snapshot.ToolCalls[0].Status+`"`)) {
						t.Fatalf("terminal event disagreed with persistence: payload=%s tool=%#v", event.Payload, snapshot.ToolCalls[0])
					}
				default:
					return
				}
			}
		})
	}
}
