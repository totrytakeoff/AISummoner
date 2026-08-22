package devicegate

import (
	"context"
	"errors"
	"testing"
)

func TestSameDeviceSerializesCanceledWaiterAndCleansKey(t *testing.T) {
	gate := New()
	unlock, err := gate.LockDevice(context.Background(), "dev_one")
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan func(), 1)
	go func() {
		unlockSecond, lockErr := gate.LockDevice(context.Background(), "dev_one")
		if lockErr == nil {
			acquired <- unlockSecond
		}
	}()
	select {
	case <-acquired:
		t.Fatal("same Device lock was acquired concurrently")
	default:
	}

	waitCtx, cancelWait := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		_, lockErr := gate.LockDevice(waitCtx, "dev_one")
		waitResult <- lockErr
	}()
	waitForRefs(t, gate, "dev_one", 3)
	cancelWait()
	if err := <-waitResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter result = %v", err)
	}

	unlock()
	unlock() // idempotent
	unlockSecond := <-acquired
	unlockSecond()
	if count := gate.entryCount(); count != 0 {
		t.Fatalf("gate retained %d keys", count)
	}
}

func TestCanceledContextRacingAvailableTokenReturnsItAndCleansKey(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		gate := New()
		holder, err := gate.LockDevice(context.Background(), "dev_race")
		if err != nil {
			t.Fatal(err)
		}
		waiterReady := make(chan struct{})
		releaseWaiter := make(chan struct{})
		gate.beforeWait = func() {
			close(waiterReady)
			<-releaseWaiter
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, lockErr := gate.LockDevice(ctx, "dev_race")
			result <- lockErr
		}()
		<-waiterReady
		waitForRefs(t, gate, "dev_race", 2)
		// Make both select cases ready before the waiter can select. If it
		// chooses the token, LockDevice must observe ctx.Err, compensate the
		// token, and drop exactly one reference.
		cancel()
		holder()
		close(releaseWaiter)
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: canceled acquire = %v", iteration, err)
		}
		gate.beforeWait = nil
		unlock, err := gate.LockDevice(context.Background(), "dev_race")
		if err != nil {
			t.Fatalf("iteration %d: token was lost: %v", iteration, err)
		}
		unlock()
		if count := gate.entryCount(); count != 0 {
			t.Fatalf("iteration %d: gate retained %d keys", iteration, count)
		}
	}
}

func waitForRefs(t *testing.T, gate *Gate, deviceID string, want int) {
	t.Helper()
	for {
		gate.mu.Lock()
		value := gate.entries[deviceID]
		got := 0
		if value != nil {
			got = value.refs
		}
		gate.mu.Unlock()
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("refs=%d want=%d", got, want)
		}
	}
}

func TestDifferentDevicesProceedIndependently(t *testing.T) {
	gate := New()
	unlockOne, err := gate.LockDevice(context.Background(), "dev_one")
	if err != nil {
		t.Fatal(err)
	}
	defer unlockOne()

	unlockTwo, err := gate.LockDevice(context.Background(), "dev_two")
	if err != nil {
		t.Fatalf("different Device was blocked: %v", err)
	}
	unlockTwo()
}
