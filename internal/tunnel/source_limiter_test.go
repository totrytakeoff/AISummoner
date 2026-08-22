package tunnel

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSourceLimiterHardCapAndDeterministicLRUEviction(t *testing.T) {
	limiter := newSourceLimiter(20, time.Minute)
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	for index := 0; index < maxSourceLimiterEntries; index++ {
		if !limiter.allow(fmt.Sprintf("source-%04d", index), now) {
			t.Fatalf("unique source %d was rejected", index)
		}
	}

	// Every entry has the same timestamp. Touching the first entry must make
	// its monotonic observation order newer than source-0001.
	if !limiter.allow("source-0000", now) {
		t.Fatal("existing source touch was rejected below its attempt limit")
	}
	if !limiter.allow("source-overflow", now) {
		t.Fatal("overflow source was rejected")
	}

	entries := sourceLimiterSnapshot(limiter)
	if len(entries) != maxSourceLimiterEntries {
		t.Fatalf("source limiter size = %d, want %d", len(entries), maxSourceLimiterEntries)
	}
	if _, exists := entries["source-0001"]; exists {
		t.Fatal("least-recently-observed source was not evicted")
	}
	for _, retained := range []string{"source-0000", "source-overflow", "source-4095"} {
		if _, exists := entries[retained]; !exists {
			t.Fatalf("recent source %q was evicted", retained)
		}
	}
}

func TestSourceLimiterReclaimsExpiredWindowsBeforeInsertion(t *testing.T) {
	limiter := newSourceLimiter(20, time.Minute)
	start := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)
	if !limiter.allow("expired", start) || !limiter.allow("live", start.Add(30*time.Second)) {
		t.Fatal("initial unique sources were rejected")
	}
	if !limiter.allow("fresh", start.Add(61*time.Second)) {
		t.Fatal("fresh source was rejected")
	}

	entries := sourceLimiterSnapshot(limiter)
	if len(entries) != 2 {
		t.Fatalf("source limiter size after expiry = %d, want 2", len(entries))
	}
	if _, exists := entries["expired"]; exists {
		t.Fatal("expired source window was not reclaimed")
	}
	for _, retained := range []string{"live", "fresh"} {
		if _, exists := entries[retained]; !exists {
			t.Fatalf("unexpired source %q was reclaimed", retained)
		}
	}
}

func TestSourceLimiterClockRollbackDoesNotExpireState(t *testing.T) {
	limiter := newSourceLimiter(1, time.Minute)
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	if !limiter.allow("future", now) {
		t.Fatal("initial source was rejected")
	}
	if !limiter.allow("rollback-trigger", now.Add(-2*time.Minute)) {
		t.Fatal("source observed after clock rollback was rejected")
	}
	entries := sourceLimiterSnapshot(limiter)
	if _, exists := entries["future"]; !exists {
		t.Fatal("clock rollback incorrectly expired an existing source")
	}
	if limiter.allow("future", now.Add(-time.Minute)) {
		t.Fatal("clock rollback reset the existing fixed-window attempt count")
	}
}

func TestSourceLimiterPreservesAttemptWindowAndSuccessDelete(t *testing.T) {
	limiter := newSourceLimiter(3, time.Minute)
	start := time.Date(2026, time.August, 13, 11, 0, 0, 0, time.UTC)
	for attempt := 0; attempt < 3; attempt++ {
		if !limiter.allow("same-source", start.Add(time.Duration(attempt)*time.Second)) {
			t.Fatalf("attempt %d was rejected", attempt+1)
		}
	}
	if limiter.allow("same-source", start.Add(59*time.Second)) {
		t.Fatal("attempt above the current-window limit was accepted")
	}
	if !limiter.allow("same-source", start.Add(time.Minute)) {
		t.Fatal("expired fixed window did not reset")
	}

	limiter.succeeded("same-source")
	if _, exists := sourceLimiterSnapshot(limiter)["same-source"]; exists {
		t.Fatal("successful authentication did not delete source state")
	}
	if !limiter.allow("same-source", start.Add(time.Minute)) {
		t.Fatal("source was not immediately admitted after successful deletion")
	}
	entry := sourceLimiterSnapshot(limiter)["same-source"]
	if entry.count != 1 {
		t.Fatalf("attempt count after successful deletion = %d, want 1", entry.count)
	}
}

func TestSourceLimiterConcurrentOperationsStayBounded(t *testing.T) {
	limiter := newSourceLimiter(20, time.Minute)
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	const (
		workers    = 32
		perWorker  = 160
		sharedKeys = 8
	)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer group.Done()
			for attempt := 0; attempt < perWorker; attempt++ {
				now := start.Add(time.Duration((worker+attempt)%50) * time.Millisecond)
				unique := fmt.Sprintf("concurrent-%02d-%03d", worker, attempt)
				limiter.allow(unique, now)
				if attempt%11 == 0 {
					limiter.succeeded(unique)
				}
				shared := fmt.Sprintf("shared-%d", attempt%sharedKeys)
				limiter.allow(shared, now)
				if (worker+attempt)%37 == 0 {
					limiter.succeeded(shared)
				}
			}
		}()
	}
	group.Wait()

	entries := sourceLimiterSnapshot(limiter)
	if len(entries) > maxSourceLimiterEntries {
		t.Fatalf("concurrent source limiter size = %d, cap = %d", len(entries), maxSourceLimiterEntries)
	}
}

func sourceLimiterSnapshot(limiter *sourceLimiter) map[string]sourceWindow {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entries := make(map[string]sourceWindow, len(limiter.entries))
	for source, entry := range limiter.entries {
		entries[source] = entry
	}
	return entries
}
