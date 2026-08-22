package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/opencode"
)

type sequenceHealthProbe struct {
	statuses []opencode.HealthStatus
	calls    int
}

func (probe *sequenceHealthProbe) Health(context.Context) opencode.HealthResult {
	index := probe.calls
	probe.calls++
	if index >= len(probe.statuses) {
		index = len(probe.statuses) - 1
	}
	return opencode.HealthResult{Status: probe.statuses[index], Version: "private-provider-version"}
}

func TestAwaitOpenCodeStartupStatusMatrix(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []opencode.HealthStatus
		wantCalls int
		wantError error
		wantWaits int
	}{
		{name: "available", statuses: []opencode.HealthStatus{opencode.HealthAvailable}, wantCalls: 1},
		{name: "unavailable then available", statuses: []opencode.HealthStatus{opencode.HealthUnavailable, opencode.HealthUnavailable, opencode.HealthAvailable}, wantCalls: 3, wantWaits: 2},
		{name: "rate limited fails closed", statuses: []opencode.HealthStatus{opencode.HealthRateLimited}, wantCalls: 1, wantError: ErrOpenCodeRateLimited},
		{name: "unknown fails closed", statuses: []opencode.HealthStatus{"future_status"}, wantCalls: 1, wantError: ErrOpenCodeUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &sequenceHealthProbe{statuses: test.statuses}
			waits := 0
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := AwaitOpenCodeStartup(ctx, OpenCodeStartupOptions{
				Probe: probe, RetryDelay: time.Millisecond,
				Wait: func(context.Context, time.Duration) error { waits++; return nil },
			})
			if !errors.Is(err, test.wantError) || probe.calls != test.wantCalls || waits != test.wantWaits {
				t.Fatalf("Await = err %v calls %d waits %d; want err %v calls %d waits %d", err, probe.calls, waits, test.wantError, test.wantCalls, test.wantWaits)
			}
			if err != nil && strings.Contains(err.Error(), "private-provider-version") {
				t.Fatalf("startup error exposed provider detail: %v", err)
			}
		})
	}
}

func TestAwaitOpenCodeStartupCallerDeadlineBoundsRetries(t *testing.T) {
	probe := &sequenceHealthProbe{statuses: []opencode.HealthStatus{opencode.HealthUnavailable}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A canceled context still has no deadline, and is rejected before probing.
	if err := AwaitOpenCodeStartup(ctx, OpenCodeStartupOptions{Probe: probe}); err == nil || probe.calls != 0 {
		t.Fatalf("unbounded canceled startup = %v, calls=%d", err, probe.calls)
	}

	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := AwaitOpenCodeStartup(ctx, OpenCodeStartupOptions{
		Probe: probe,
		Wait:  func(context.Context, time.Duration) error { return context.DeadlineExceeded },
	})
	if !errors.Is(err, ErrOpenCodeUnavailable) || probe.calls != 1 {
		t.Fatalf("bounded unavailable startup = %v, calls=%d", err, probe.calls)
	}
}
