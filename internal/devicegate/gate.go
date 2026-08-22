// Package devicegate serializes lifecycle changes for one Device while
// allowing unrelated Devices to proceed independently.
package devicegate

import (
	"context"
	"errors"
	"sync"
)

var ErrInvalidDeviceID = errors.New("device id is required")

// Gate is a context-aware keyed mutex. A Gate must be shared by the Tunnel
// Gateway and Device Service so authentication publication and unpair have one
// linearization order.
type Gate struct {
	mu      sync.Mutex
	entries map[string]*entry

	// beforeWait is a package-private test barrier. Production Gates leave it
	// nil; keeping it private avoids widening the lifecycle API.
	beforeWait func()
}

type entry struct {
	token chan struct{}
	refs  int
}

func New() *Gate {
	return &Gate{entries: make(map[string]*entry)}
}

// LockDevice waits for exclusive access to deviceID. The returned unlock is
// idempotent. Canceled waiters release their reference and cannot leak keys.
func (g *Gate) LockDevice(ctx context.Context, deviceID string) (func(), error) {
	if g == nil {
		return nil, errors.New("device lifecycle gate is required")
	}
	if deviceID == "" {
		return nil, ErrInvalidDeviceID
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	value := g.entries[deviceID]
	if value == nil {
		value = &entry{token: make(chan struct{}, 1)}
		value.token <- struct{}{}
		g.entries[deviceID] = value
	}
	value.refs++
	g.mu.Unlock()
	if g.beforeWait != nil {
		g.beforeWait()
	}

	select {
	case <-ctx.Done():
		g.dropReference(deviceID, value)
		return nil, ctx.Err()
	case <-value.token:
	}
	if err := ctx.Err(); err != nil {
		value.token <- struct{}{}
		g.dropReference(deviceID, value)
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			value.token <- struct{}{}
			g.dropReference(deviceID, value)
		})
	}, nil
}

func (g *Gate) dropReference(deviceID string, value *entry) {
	g.mu.Lock()
	value.refs--
	if value.refs == 0 && g.entries[deviceID] == value {
		delete(g.entries, deviceID)
	}
	g.mu.Unlock()
}

func (g *Gate) entryCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.entries)
}
