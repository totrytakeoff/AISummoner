package tunnel

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/ssh"
)

var (
	ErrDeviceOffline      = errors.New("device is offline")
	ErrConnectionReplaced = errors.New("device connection was replaced")
)

// Connection is one authenticated Device tunnel. Its SSH signer exists only
// for this connection and is discarded when Close returns.
type Connection struct {
	ID       string
	DeviceID string

	session   *yamux.Session
	transport net.Conn
	control   net.Conn
	sshSigner ssh.Signer
	sshSetups chan struct{}

	mu          sync.RWMutex
	lastSeen    time.Time
	closed      bool
	closeOnce   sync.Once
	done        chan struct{}
	cleanupDone chan struct{}
	cleanupErr  error
}

func newConnection(id, deviceID string, session *yamux.Session, transport, control net.Conn, signer ssh.Signer, now time.Time) *Connection {
	return &Connection{
		ID: id, DeviceID: deviceID, session: session, transport: transport, control: control,
		sshSigner: signer, sshSetups: make(chan struct{}, 16), lastSeen: now.UTC(),
		done: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
}

func (c *Connection) Close() error {
	return c.beginClose()()
}

func (c *Connection) initLifecycle() {
	c.mu.Lock()
	if c.done == nil {
		c.done = make(chan struct{})
	}
	if c.cleanupDone == nil {
		c.cleanupDone = make(chan struct{})
	}
	c.mu.Unlock()
}

// beginClose is the non-blocking logical close boundary used while the Device
// lifecycle gate is held. It marks the Connection offline synchronously and
// starts exactly one owned I/O cleanup. The returned idempotent join function
// may block and therefore must only be called outside the Device gate.
func (c *Connection) beginClose() func() error {
	c.initLifecycle()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		done := c.done
		cleanupDone := c.cleanupDone
		c.mu.Unlock()
		close(done)
		go func() {
			if c.control != nil {
				_ = c.control.Close()
			}
			var result error
			if c.session != nil {
				result = c.session.Close()
			}
			if c.transport != nil {
				_ = c.transport.Close()
			}
			c.mu.Lock()
			c.cleanupErr = result
			c.mu.Unlock()
			close(cleanupDone)
		}()
	})
	return func() error {
		c.mu.RLock()
		cleanupDone := c.cleanupDone
		c.mu.RUnlock()
		<-cleanupDone
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.cleanupErr
	}
}

func (c *Connection) Done() <-chan struct{} {
	c.initLifecycle()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.done
}

func (c *Connection) LastSeen() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSeen
}

func (c *Connection) touch(at time.Time) {
	c.mu.Lock()
	if !c.closed {
		c.lastSeen = at.UTC()
	}
	c.mu.Unlock()
}

func (c *Connection) online() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed
}

func (c *Connection) sshCredentials() (*yamux.Session, ssh.Signer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.session == nil || c.sshSigner == nil || c.sshSetups == nil {
		return nil, nil, false
	}
	return c.session, c.sshSigner, true
}

// Manager is the single-node authoritative online Device registry.
type Manager struct {
	mu          sync.RWMutex
	connections map[string]*Connection
	owned       map[*Connection]struct{}
	closing     bool
	cleanupWG   sync.WaitGroup
}

func NewManager() *Manager {
	return &Manager{connections: make(map[string]*Connection), owned: make(map[*Connection]struct{})}
}

// Register atomically installs connection. The caller must send the successful
// authentication response before relying on it for new work. Newest wins.
func (m *Manager) Register(connection *Connection) (replaced *Connection, accepted bool) {
	if connection == nil {
		return nil, false
	}
	connection.initLifecycle()
	m.mu.Lock()
	if m.closing {
		connection.beginClose()
		m.mu.Unlock()
		return nil, false
	}
	m.ownLocked(connection)
	replaced = m.connections[connection.DeviceID]
	m.connections[connection.DeviceID] = connection
	if replaced != nil && replaced != connection {
		// Logical close is synchronous, but arbitrary network/yamux close runs
		// in the Connection-owned cleanup and never stalls a Device gate holder.
		replaced.beginClose()
	}
	m.mu.Unlock()
	return replaced, true
}

func (m *Manager) ownLocked(connection *Connection) {
	if _, exists := m.owned[connection]; exists {
		return
	}
	m.owned[connection] = struct{}{}
	m.cleanupWG.Add(1)
	go func() {
		connection.mu.RLock()
		cleanupDone := connection.cleanupDone
		connection.mu.RUnlock()
		<-cleanupDone
		m.mu.Lock()
		if m.connections[connection.DeviceID] == connection {
			delete(m.connections, connection.DeviceID)
		}
		delete(m.owned, connection)
		m.mu.Unlock()
		m.cleanupWG.Done()
	}()
}

// Remove removes only the exact connection, so an old handler cannot remove a
// newer connection after newest-wins replacement.
func (m *Manager) Remove(connection *Connection) {
	if connection == nil {
		return
	}
	m.mu.Lock()
	if m.connections[connection.DeviceID] == connection {
		delete(m.connections, connection.DeviceID)
	}
	m.mu.Unlock()
}

// DetachDevice makes the exact current Connection logically offline at one
// atomic map transition without waiting for network I/O. The returned
// idempotent join closes only that captured Connection, so a later replacement
// is never removed or closed.
func (m *Manager) DetachDevice(deviceID string) func() error {
	m.mu.Lock()
	connection := m.connections[deviceID]
	if connection != nil {
		delete(m.connections, deviceID)
	}
	if connection == nil {
		m.mu.Unlock()
		return func() error { return nil }
	}
	join := connection.beginClose()
	m.mu.Unlock()
	return join
}

// InvalidateDevice preserves the standalone synchronous convenience API.
// Lifecycle composition uses DetachDevice under its gate and joins afterward.
func (m *Manager) InvalidateDevice(deviceID string) {
	_ = m.DetachDevice(deviceID)()
}

func (m *Manager) IsOnline(deviceID string) bool {
	connection, ok := m.Get(deviceID)
	return ok && connection.online()
}

func (m *Manager) Get(deviceID string) (*Connection, bool) {
	m.mu.RLock()
	connection, ok := m.connections[deviceID]
	m.mu.RUnlock()
	return connection, ok
}

// OpenSSH atomically selects one live Connection and returns both a typed yamux
// stream and that exact Connection's ephemeral SSH signer. Consumers must not
// look up either resource independently because newest-wins replacement may
// occur between calls.
func (m *Manager) OpenSSH(ctx context.Context, deviceID string) (net.Conn, ssh.Signer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.RLock()
	connection, ok := m.connections[deviceID]
	var session *yamux.Session
	var signer ssh.Signer
	if ok {
		session, signer, ok = connection.sshCredentials()
	}
	m.mu.RUnlock()
	if !ok {
		return nil, nil, ErrDeviceOffline
	}
	type streamResult struct {
		stream *yamux.Stream
		err    error
	}
	select {
	case connection.sshSetups <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-connection.Done():
		return nil, nil, ErrConnectionReplaced
	}
	opened := make(chan streamResult, 1)
	go func() {
		defer func() { <-connection.sshSetups }()
		stream, err := session.OpenStream()
		opened <- streamResult{stream: stream, err: err}
	}()
	var stream *yamux.Stream
	var err error
	select {
	case <-ctx.Done():
		go func() {
			result := <-opened
			if result.stream != nil {
				_ = result.stream.Close()
			}
		}()
		return nil, nil, ctx.Err()
	case result := <-opened:
		stream, err = result.stream, result.err
	}
	if err != nil {
		return nil, nil, ErrDeviceOffline
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = stream.Close()
		}
	}()
	setupDeadline := time.Now().Add(10 * time.Second)
	if deadline, exists := ctx.Deadline(); exists && deadline.Before(setupDeadline) {
		setupDeadline = deadline
	}
	if err := stream.SetDeadline(setupDeadline); err != nil {
		return nil, nil, err
	}
	setupDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-setupDone:
		}
	}()
	defer close(setupDone)
	requestID, err := id.New("req")
	if err != nil {
		return nil, nil, err
	}
	if err := protocol.NewCodec(stream).WriteHeader(protocol.StreamHeader{
		Version: protocol.Version, Kind: protocol.StreamSSH, RequestID: requestID,
	}); err != nil {
		return nil, nil, err
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		return nil, nil, err
	}
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-connection.Done():
		return nil, nil, ErrConnectionReplaced
	default:
	}
	closeOnError = false
	return stream, signer, nil
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		m.cleanupWG.Wait()
		return
	}
	m.closing = true
	connections := make([]*Connection, 0, len(m.owned))
	for connection := range m.owned {
		connections = append(connections, connection)
	}
	m.connections = make(map[string]*Connection)
	m.mu.Unlock()
	// Initiate every logical close before joining any network cleanup, so one
	// blocked Close cannot leave another Connection logically live.
	for _, connection := range connections {
		connection.beginClose()
	}
	m.cleanupWG.Wait()
}
