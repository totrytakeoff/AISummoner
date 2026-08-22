package device

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/store"
)

type lifecycleRecorder struct {
	mu     sync.Mutex
	events []string
	held   bool
}

func (recorder *lifecycleRecorder) add(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *lifecycleRecorder) isHeld() bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.held
}

type recordingGate struct{ recorder *lifecycleRecorder }

func (gate *recordingGate) LockDevice(ctx context.Context, _ string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gate.recorder.mu.Lock()
	gate.recorder.held = true
	gate.recorder.events = append(gate.recorder.events, "lock")
	gate.recorder.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.recorder.mu.Lock()
			gate.recorder.held = false
			gate.recorder.events = append(gate.recorder.events, "unlock")
			gate.recorder.mu.Unlock()
		})
	}, nil
}

type lifecycleStoreFake struct {
	recorder  *lifecycleRecorder
	result    store.UnpairResult
	err       error
	committed chan struct{}
	release   chan struct{}
}

func (*lifecycleStoreFake) DevicesByOwner(context.Context, string) ([]store.Device, error) {
	return nil, nil
}
func (*lifecycleStoreFake) DeviceByOwner(context.Context, string, string) (store.Device, error) {
	return store.Device{}, store.ErrNotFound
}
func (fake *lifecycleStoreFake) UnpairDevice(context.Context, string, string, time.Time) (store.UnpairResult, error) {
	if !fake.recorder.isHeld() {
		return store.UnpairResult{}, errors.New("store called outside Device gate")
	}
	fake.recorder.add("store")
	if fake.committed != nil {
		close(fake.committed)
	}
	if fake.release != nil {
		<-fake.release
	}
	return fake.result, fake.err
}

type tunnelInvalidatorFake struct {
	recorder     *lifecycleRecorder
	closeEntered chan struct{}
	closeRelease chan struct{}
}

func (fake *tunnelInvalidatorFake) DetachDevice(string) func() error {
	if !fake.recorder.isHeld() {
		fake.recorder.add("tunnel_without_gate")
	} else {
		fake.recorder.add("tunnel")
	}
	return func() error {
		if fake.closeEntered != nil {
			close(fake.closeEntered)
		}
		if fake.closeRelease != nil {
			<-fake.closeRelease
		}
		fake.recorder.add("tunnel_cleanup")
		return nil
	}
}

type terminalInvalidatorFake struct {
	recorder *lifecycleRecorder
	entered  chan struct{}
	release  chan struct{}
}

func (fake *terminalInvalidatorFake) CancelDevice(string) {
	if fake.recorder.isHeld() {
		fake.recorder.add("terminal_with_gate")
	} else {
		fake.recorder.add("terminal")
	}
	close(fake.entered)
	<-fake.release
}

type agentInvalidatorFake struct {
	recorder          *lifecycleRecorder
	marked            []string
	invalidateEntered chan struct{}
	invalidateRelease chan struct{}
	cleanupCtxLive    bool
}

func (fake *agentInvalidatorFake) MarkDeviceRevoked(_ string, sessionIDs []string) {
	if !fake.recorder.isHeld() {
		fake.recorder.add("mark_without_gate")
		return
	}
	fake.marked = append([]string(nil), sessionIDs...)
	fake.recorder.add("mark")
}

func (fake *agentInvalidatorFake) InvalidateDevice(ctx context.Context, _ string, _ []string) error {
	if fake.recorder.isHeld() {
		fake.recorder.add("agent_with_gate")
	} else {
		fake.recorder.add("agent")
	}
	fake.cleanupCtxLive = ctx.Err() == nil
	close(fake.invalidateEntered)
	select {
	case <-fake.invalidateRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLegacyServiceRejectsUnsafeDirectUnpair(t *testing.T) {
	service := NewService(&lifecycleStoreFake{}, OfflineState{})
	if err := service.Unpair(context.Background(), "usr", "dev", time.Now()); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("legacy Unpair = %v", err)
	}
}

func TestUnpairCommitCancellationStillMarksDetachesThenUnlocksBeforeJoinedCleanup(t *testing.T) {
	recorder := &lifecycleRecorder{}
	committed := make(chan struct{})
	releaseCommit := make(chan struct{})
	storeFake := &lifecycleStoreFake{
		recorder: recorder, result: store.UnpairResult{RevokedAgentSessionIDs: []string{"ags_a", "ags_b"}},
		committed: committed, release: releaseCommit,
	}
	terminal := &terminalInvalidatorFake{recorder: recorder, entered: make(chan struct{}), release: make(chan struct{})}
	agent := &agentInvalidatorFake{
		recorder: recorder, invalidateEntered: make(chan struct{}), invalidateRelease: make(chan struct{}),
	}
	service, err := NewLifecycleService(LifecycleOptions{
		Store: storeFake, Gate: &recordingGate{recorder: recorder}, Tunnel: &tunnelInvalidatorFake{recorder: recorder},
		Terminal: terminal, Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CleanupTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- service.Unpair(requestCtx, "usr", "dev", time.Now()) }()
	<-committed
	cancelRequest()
	close(releaseCommit)
	<-terminal.entered
	<-agent.invalidateEntered
	if recorder.isHeld() {
		t.Fatal("Device gate remained held during joined cleanup")
	}
	select {
	case err := <-result:
		t.Fatalf("Unpair returned before cleanup joins: %v", err)
	default:
	}
	close(terminal.release)
	close(agent.invalidateRelease)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !agent.cleanupCtxLive || len(agent.marked) != 2 || agent.marked[0] != "ags_a" || agent.marked[1] != "ags_b" {
		t.Fatalf("post-commit Agent cleanup mismatch: live=%v marked=%#v", agent.cleanupCtxLive, agent.marked)
	}
	recorder.mu.Lock()
	events := append([]string(nil), recorder.events...)
	recorder.mu.Unlock()
	wantPrefix := []string{"lock", "store", "mark", "tunnel", "unlock"}
	if len(events) < len(wantPrefix) {
		t.Fatalf("events = %#v", events)
	}
	for index, want := range wantPrefix {
		if events[index] != want {
			t.Fatalf("events = %#v, want prefix %#v", events, wantPrefix)
		}
	}
	for _, event := range events {
		if event == "mark_without_gate" || event == "tunnel_without_gate" || event == "terminal_with_gate" || event == "agent_with_gate" {
			t.Fatalf("invalid lifecycle order: %#v", events)
		}
	}
}

func TestUnpairFailureUnlocksWithoutInvalidation(t *testing.T) {
	recorder := &lifecycleRecorder{}
	terminal := &terminalInvalidatorFake{recorder: recorder, entered: make(chan struct{}), release: make(chan struct{})}
	agent := &agentInvalidatorFake{recorder: recorder, invalidateEntered: make(chan struct{}), invalidateRelease: make(chan struct{})}
	service, err := NewLifecycleService(LifecycleOptions{
		Store: &lifecycleStoreFake{recorder: recorder, err: store.ErrNotFound}, Gate: &recordingGate{recorder: recorder},
		Tunnel: &tunnelInvalidatorFake{recorder: recorder}, Terminal: terminal, Agent: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Unpair(context.Background(), "wrong", "dev", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong-owner Unpair = %v", err)
	}
	recorder.mu.Lock()
	events := append([]string(nil), recorder.events...)
	recorder.mu.Unlock()
	if len(events) != 3 || events[0] != "lock" || events[1] != "store" || events[2] != "unlock" {
		t.Fatalf("failed Unpair lifecycle = %#v", events)
	}
}

func TestUnpairCleanupTimeoutDoesNotStrandLateWorkerReports(t *testing.T) {
	recorder := &lifecycleRecorder{}
	terminal := &terminalInvalidatorFake{
		recorder: recorder, entered: make(chan struct{}), release: make(chan struct{}),
	}
	agent := &agentInvalidatorFake{
		recorder: recorder, invalidateEntered: make(chan struct{}), invalidateRelease: make(chan struct{}),
	}
	service, err := NewLifecycleService(LifecycleOptions{
		Store: &lifecycleStoreFake{recorder: recorder}, Gate: &recordingGate{recorder: recorder},
		Tunnel: &tunnelInvalidatorFake{recorder: recorder}, Terminal: terminal, Agent: agent,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CleanupTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	reported := make(chan string, 3)
	service.lifecycle.afterCleanupReport = func(component string) { reported <- component }

	result := make(chan error, 1)
	go func() { result <- service.Unpair(context.Background(), "usr", "dev", time.Now()) }()
	<-terminal.entered
	<-agent.invalidateEntered
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	// The cleanup deadline has returned to the caller. Releasing a worker now
	// proves its result send is buffered; an unbuffered result channel would
	// strand this goroutine forever because the receiver has gone away.
	close(terminal.release)
	close(agent.invalidateRelease)
	seen := make(map[string]bool)
	for len(seen) < 3 {
		select {
		case component := <-reported:
			seen[component] = true
		case <-time.After(time.Second):
			t.Fatalf("late cleanup worker remained blocked after timeout; reports=%v", seen)
		}
	}
}
