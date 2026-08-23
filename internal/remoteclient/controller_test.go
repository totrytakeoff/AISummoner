package remoteclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/tunnel"
)

type blockingRunner struct {
	mu        sync.Mutex
	runs      int
	active    int
	maxActive int
	starts    chan int
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{starts: make(chan int, 2048)}
}

func (runner *blockingRunner) Run(ctx context.Context) error {
	runner.mu.Lock()
	runner.runs++
	runner.active++
	if runner.active > runner.maxActive {
		runner.maxActive = runner.active
	}
	current := runner.runs
	runner.mu.Unlock()
	runner.starts <- current
	<-ctx.Done()
	runner.mu.Lock()
	runner.active--
	runner.mu.Unlock()
	return nil
}

func (runner *blockingRunner) waitStart(t *testing.T, want int) {
	t.Helper()
	select {
	case got := <-runner.starts:
		if got != want {
			t.Fatalf("runner start = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("runner start %d timed out", want)
	}
}

func (runner *blockingRunner) concurrency() (active, maximum int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.active, runner.maxActive
}

func TestControllerPauseResumeRefreshAndShutdownJoinOneRunner(t *testing.T) {
	runner := newBlockingRunner()
	controller := newController("dev_test", "test-device", "test", "https://example.test", time.Now)
	controller.runner = runner
	parent, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(parent) }()
	runner.waitStart(t, 1)
	controller.observeTunnelState(tunnel.StateNotification{Phase: tunnel.ClientPhaseOnline})

	operation, stopOperation := context.WithTimeout(context.Background(), time.Second)
	defer stopOperation()
	if err := controller.Pause(operation); err != nil {
		t.Fatal(err)
	}
	if got := controller.Snapshot(); got.Phase != PhasePaused || got.ActiveSessions != 0 {
		t.Fatalf("paused snapshot = %+v", got)
	}
	if err := controller.Resume(); err != nil {
		t.Fatal(err)
	}
	runner.waitStart(t, 2)
	controller.observePairing(tunnel.PairingNotification{Code: "ABCD-2345", ExpiresAt: time.Now().Add(time.Minute)})
	if err := controller.RefreshPairing(operation); err != nil {
		t.Fatal(err)
	}
	runner.waitStart(t, 3)
	if controller.Snapshot().Pairing != nil {
		t.Fatal("pairing offer survived refresh cycle")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Controller.Run did not join")
	}
	if got := controller.Snapshot(); got.Phase != PhaseStopped || got.ActiveSessions != 0 {
		t.Fatalf("stopped snapshot = %+v", got)
	}
	if err := controller.Resume(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Resume after stop error = %v", err)
	}
}

func TestControllerPairingExpiryAndEventsNeverExposeCode(t *testing.T) {
	controller := newController("dev_test", "test-device", "test", "https://secret-origin.invalid", time.Now)
	controller.observePairing(tunnel.PairingNotification{Code: "SAFE-2345", ExpiresAt: time.Now().Add(30 * time.Millisecond)})
	snapshot := controller.Snapshot()
	if snapshot.Pairing == nil || snapshot.Pairing.Code != "SAFE-2345" || snapshot.Pairing.Expired {
		t.Fatalf("fresh pairing snapshot = %+v", snapshot.Pairing)
	}
	deadline := time.After(time.Second)
	for {
		if value := controller.Snapshot().Pairing; value != nil && value.Expired {
			if value.Code != "" {
				t.Fatal("expired snapshot retained pairing code")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("pairing expiry was not published")
		case <-time.After(time.Millisecond):
		}
	}
	encoded, err := json.Marshal(controller.Events(0, MaxEvents))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SAFE-2345", "secret-origin"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("event JSON contains forbidden value %q", forbidden)
		}
	}
}

func TestPairingOfferNotificationIsNonBlockingAndKeepsLatestValue(t *testing.T) {
	controller := newController("dev_test", "test-device", "test", "https://example.test", time.Now)
	for _, code := range []string{"FIRST-2345", "SECOND-2345", "LATEST-2345"} {
		controller.observePairing(tunnel.PairingNotification{Code: code, ExpiresAt: time.Now().Add(time.Hour)})
	}
	select {
	case offer := <-controller.PairingOffers():
		if offer.Code != "LATEST-2345" {
			t.Fatalf("pairing offer = %+v", offer)
		}
	case <-time.After(time.Second):
		t.Fatal("latest pairing offer was not published")
	}
}

func TestControllerEventRingAndConcurrentStreamCountStayBounded(t *testing.T) {
	controller := newController("dev_test", "test-device", "test", "https://example.test", time.Now)
	var workers sync.WaitGroup
	for index := 0; index < 320; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			controller.observeStream(tunnel.StreamNotification{Opened: true})
			controller.observeStream(tunnel.StreamNotification{Opened: false})
		}()
	}
	workers.Wait()
	if got := controller.Snapshot().ActiveSessions; got != 0 {
		t.Fatalf("active sessions = %d", got)
	}
	events := controller.Events(0, MaxEvents)
	if len(events) != MaxEvents {
		t.Fatalf("event count = %d, want %d", len(events), MaxEvents)
	}
	for index := 1; index < len(events); index++ {
		if events[index].Sequence != events[index-1].Sequence+1 {
			t.Fatalf("event sequence gap at %d: %+v", index, events[index])
		}
	}
	page := controller.Events(events[len(events)-3].Sequence, 2)
	if len(page) != 2 || page[0].Sequence != events[len(events)-2].Sequence {
		t.Fatalf("cursor page = %+v", page)
	}
}

func TestSnapshotReturnsDefensiveTimestampAndPairingCopies(t *testing.T) {
	runner := newBlockingRunner()
	controller := newController("dev_test", "test-device", "test", "https://example.test", time.Now)
	controller.runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	runner.waitStart(t, 1)
	controller.observeTunnelState(tunnel.StateNotification{Phase: tunnel.ClientPhaseOnline})
	controller.observePairing(tunnel.PairingNotification{Code: "ABCD-2345", ExpiresAt: time.Now().Add(time.Hour)})
	first := controller.Snapshot()
	if first.OnlineSince == nil || first.Pairing == nil {
		t.Fatalf("snapshot = %+v", first)
	}
	*first.OnlineSince = time.Time{}
	first.Pairing.Code = "mutated"
	second := controller.Snapshot()
	if second.OnlineSince == nil || second.OnlineSince.IsZero() || second.Pairing == nil || second.Pairing.Code != "ABCD-2345" {
		t.Fatalf("controller state was mutated through snapshot: %+v", second)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Controller.Run did not join")
	}
}

func TestConcurrentPauseResumeRefreshAndCancelNeverDuplicateRunner(t *testing.T) {
	runner := newBlockingRunner()
	controller := newController("dev_test", "test-device", "test", "https://example.test", time.Now)
	controller.runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	runner.waitStart(t, 1)

	unexpected := make(chan error, 16)
	var workers sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			for operation := 0; operation < 60; operation++ {
				switch (worker + operation) % 4 {
				case 0:
					operationContext, stop := context.WithTimeout(context.Background(), time.Second)
					err := controller.Pause(operationContext)
					stop()
					if err != nil && !errors.Is(err, ErrNotRunning) {
						select {
						case unexpected <- err:
						default:
						}
					}
				case 1:
					if err := controller.Resume(); err != nil && !errors.Is(err, ErrNotRunning) {
						select {
						case unexpected <- err:
						default:
						}
					}
				case 2:
					controller.observePairing(tunnel.PairingNotification{Code: "RACE-2345", ExpiresAt: time.Now().Add(time.Hour)})
					operationContext, stop := context.WithTimeout(context.Background(), time.Second)
					err := controller.RefreshPairing(operationContext)
					stop()
					if err != nil && !errors.Is(err, ErrNoPairingOffer) && !errors.Is(err, ErrNotRunning) {
						select {
						case unexpected <- err:
						default:
						}
					}
				case 3:
					_ = controller.Snapshot()
					_ = controller.Events(0, MaxEvents)
				}
			}
		}()
	}
	workers.Wait()
	select {
	case err := <-unexpected:
		t.Fatalf("concurrent operation failed: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Controller.Run did not join")
	}
	active, maximum := runner.concurrency()
	if active != 0 || maximum != 1 {
		t.Fatalf("runner concurrency active=%d maximum=%d", active, maximum)
	}
	if snapshot := controller.Snapshot(); snapshot.Phase != PhaseStopped || snapshot.ActiveSessions != 0 {
		t.Fatalf("final snapshot = %+v", snapshot)
	}
}
