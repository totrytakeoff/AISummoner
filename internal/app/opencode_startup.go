package app

import (
	"context"
	"errors"
	"time"

	"github.com/aisummoner/aisummoner/internal/opencode"
)

var (
	ErrOpenCodeUnavailable = errors.New("OpenCode sidecar is unavailable")
	ErrOpenCodeRateLimited = errors.New("OpenCode sidecar is rate limited")
)

type OpenCodeHealthProbe interface {
	Health(context.Context) opencode.HealthResult
}

type OpenCodeStartupOptions struct {
	Probe      OpenCodeHealthProbe
	RetryDelay time.Duration
	Wait       func(context.Context, time.Duration) error
}

// AwaitOpenCodeStartup requires a reachable, healthy sidecar before public
// readiness opens. Unavailable is retried until the caller's bounded startup
// context expires so Compose can start the shared-network sidecar after the
// Server container. A provider 429 is classified and fails immediately; it is
// never represented as public Server/SQLite health.
func AwaitOpenCodeStartup(ctx context.Context, options OpenCodeStartupOptions) error {
	if ctx == nil || options.Probe == nil {
		return errors.New("bounded OpenCode startup context and health probe are required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("OpenCode startup context requires a deadline")
	}
	if options.RetryDelay <= 0 || options.RetryDelay > 5*time.Second {
		options.RetryDelay = 500 * time.Millisecond
	}
	if options.Wait == nil {
		options.Wait = waitContext
	}
	for {
		if err := ctx.Err(); err != nil {
			return ErrOpenCodeUnavailable
		}
		health := options.Probe.Health(ctx)
		switch health.Status {
		case opencode.HealthAvailable:
			return nil
		case opencode.HealthRateLimited:
			return ErrOpenCodeRateLimited
		case opencode.HealthUnavailable:
		default:
			return ErrOpenCodeUnavailable
		}
		if err := options.Wait(ctx, options.RetryDelay); err != nil {
			return ErrOpenCodeUnavailable
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
