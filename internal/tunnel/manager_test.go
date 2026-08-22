package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/ssh"
)

func TestManagerNewestWinsAndOldRemovalCannotDeleteNew(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()
	oldConnection := &Connection{ID: "conn_old", DeviceID: "dev_one", done: make(chan struct{}), lastSeen: time.Now()}
	newConnection := &Connection{ID: "conn_new", DeviceID: "dev_one", done: make(chan struct{}), lastSeen: time.Now()}
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old connection was rejected")
	}
	if replaced, accepted := manager.Register(newConnection); !accepted || replaced != oldConnection {
		t.Fatalf("replaced = %p, want old connection", replaced)
	}
	select {
	case <-oldConnection.Done():
	default:
		t.Fatal("old connection was not closed")
	}
	manager.Remove(oldConnection)
	if !manager.IsOnline("dev_one") {
		t.Fatal("old connection removal deleted the newer connection")
	}
	manager.Remove(newConnection)
	if manager.IsOnline("dev_one") {
		t.Fatal("new connection remained online after removal")
	}
}

func TestFailedAuthenticatedWriteDoesNotPublishOrReplaceHealthyConnection(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()
	healthy := &Connection{ID: "conn_healthy", DeviceID: "dev_one", done: make(chan struct{}), lastSeen: time.Now()}
	if _, accepted := manager.Register(healthy); !accepted {
		t.Fatal("healthy connection was rejected")
	}

	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	wantError := errors.New("authenticated response write failed")
	transport := &failingWriteConn{writeErr: wantError}
	candidate := newConnection("conn_candidate", "dev_one", nil, transport, transport, signer, time.Now())
	gateway := &Gateway{
		manager: manager, heartbeatInterval: 5 * time.Second,
		limiter: newSourceLimiter(20, time.Minute),
	}
	released := false
	err = gateway.publishAuthenticated(context.Background(), store.Device{ID: "dev_one"}, candidate,
		protocol.NewCodec(transport), "req_test", "127.0.0.1", time.Now(), func() {
			released = true
		})
	if !errors.Is(err, wantError) {
		t.Fatalf("publish error = %v, want %v", err, wantError)
	}
	if released {
		t.Fatal("pre-auth slot was released as authenticated after failed response")
	}
	current, ok := manager.Get("dev_one")
	if !ok || current != healthy {
		t.Fatalf("manager current = %p, want healthy %p", current, healthy)
	}
	select {
	case <-healthy.Done():
		t.Fatal("failed replacement closed the healthy connection")
	default:
	}
}

func TestCanceledAuthenticationAfterDeadlineClearDoesNotPublish(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()
	healthy := &Connection{ID: "conn_healthy", DeviceID: "dev_one", done: make(chan struct{}), lastSeen: time.Now()}
	if _, accepted := manager.Register(healthy); !accepted {
		t.Fatal("healthy connection was rejected")
	}
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	transport := &cancelOnClearConn{cancel: cancel}
	candidate := newConnection("conn_candidate", "dev_one", nil, transport, transport, signer, time.Now())
	gateway := &Gateway{manager: manager, heartbeatInterval: 5 * time.Second, limiter: newSourceLimiter(20, time.Minute)}
	released := false
	err = gateway.publishAuthenticated(ctx, store.Device{ID: "dev_one", OwnerUserID: ptr("usr_owner")}, candidate,
		protocol.NewCodec(transport), "req_test", "127.0.0.1", time.Now(), func() { released = true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publish error = %v", err)
	}
	if released {
		t.Fatal("canceled authentication released pre-auth slot as successful")
	}
	current, ok := manager.Get("dev_one")
	if !ok || current != healthy {
		t.Fatal("canceled candidate replaced healthy connection")
	}
	select {
	case <-healthy.Done():
		t.Fatal("canceled candidate closed healthy connection")
	default:
	}
}

func ptr(value string) *string { return &value }

func TestManagerInvalidateDeviceDetachPreservesConcurrentReplacement(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()
	blockingControl := &blockingCloseConn{started: make(chan struct{}), release: make(chan struct{})}
	oldConnection := newConnection("conn_old", "dev_one", nil, nil, blockingControl, nil, time.Now())
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old connection was rejected")
	}

	invalidated := make(chan struct{})
	go func() {
		manager.InvalidateDevice("dev_one")
		close(invalidated)
	}()
	<-blockingControl.started
	if manager.IsOnline("dev_one") {
		t.Fatal("Device remained online while detached Connection close was blocked")
	}

	replacement := &Connection{ID: "conn_new", DeviceID: "dev_one", done: make(chan struct{}), lastSeen: time.Now()}
	if _, accepted := manager.Register(replacement); !accepted {
		t.Fatal("replacement was rejected")
	}
	close(blockingControl.release)
	<-invalidated
	current, ok := manager.Get("dev_one")
	if !ok || current != replacement {
		t.Fatal("old invalidation removed the concurrent replacement")
	}
	select {
	case <-replacement.Done():
		t.Fatal("old invalidation closed the concurrent replacement")
	default:
	}
	manager.InvalidateDevice("dev_one")
	select {
	case <-replacement.Done():
	default:
		t.Fatal("replacement was not closed by its own invalidation")
	}
}

func TestManagerReplacementIsNonBlockingAndCloseAllJoinsEveryRetiredConnection(t *testing.T) {
	manager := NewManager()
	oldControl := &blockingCloseConn{started: make(chan struct{}), release: make(chan struct{})}
	oldConnection := newConnection("conn_old", "dev_one", nil, nil, oldControl, nil, time.Now())
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old connection was rejected")
	}
	newClose := &blockingCloseConn{started: make(chan struct{}), release: make(chan struct{})}
	newConnection := newConnection("conn_new", "dev_one", nil, nil, newClose, nil, time.Now())
	replaced, accepted := manager.Register(newConnection)
	if !accepted || replaced != oldConnection {
		t.Fatalf("replacement result accepted=%v replaced=%p", accepted, replaced)
	}
	<-oldControl.started
	select {
	case <-oldConnection.Done():
	default:
		t.Fatal("replaced Connection was not made logically offline synchronously")
	}
	current, ok := manager.Get("dev_one")
	if !ok || current != newConnection || !manager.IsOnline("dev_one") {
		t.Fatal("blocked old network Close stalled new publication")
	}

	closed := make(chan struct{})
	go func() {
		manager.CloseAll()
		close(closed)
	}()
	<-newClose.started
	select {
	case <-newConnection.Done():
	default:
		t.Fatal("CloseAll joined the first blocker before initiating every logical close")
	}
	select {
	case <-closed:
		t.Fatal("CloseAll returned before retired network cleanup joined")
	default:
	}
	close(newClose.release)
	select {
	case <-closed:
		t.Fatal("CloseAll forgot the already-retired blocked Connection")
	default:
	}
	close(oldControl.release)
	<-closed
	if manager.IsOnline("dev_one") {
		t.Fatal("Manager remained online after CloseAll")
	}
}

func TestManagerRejectsRegisterAfterCloseAndCallerCanJoinCandidate(t *testing.T) {
	manager := NewManager()
	manager.CloseAll()
	control := &blockingCloseConn{started: make(chan struct{}), release: make(chan struct{})}
	candidate := newConnection("conn_late", "dev_late", nil, nil, control, nil, time.Now())
	if _, accepted := manager.Register(candidate); accepted {
		t.Fatal("closed Manager accepted a late Connection")
	}
	<-control.started
	select {
	case <-candidate.Done():
	default:
		t.Fatal("rejected late Connection was not logically closed")
	}
	join := make(chan error, 1)
	go func() { join <- candidate.Close() }()
	select {
	case <-join:
		t.Fatal("candidate cleanup returned before blocked I/O was released")
	default:
	}
	close(control.release)
	if err := <-join; err != nil {
		t.Fatal(err)
	}
}

func TestManagerOpenSSHReturnsStreamAndSignerFromSameNewestConnection(t *testing.T) {
	manager := NewManager()
	oldServer, oldRemote := yamuxPair(t)
	defer oldRemote.Close()
	newServer, newRemote := yamuxPair(t)
	defer newRemote.Close()
	oldSigner := testSSHSigner(t)
	newSigner := testSSHSigner(t)
	oldConnection := newConnection("conn_old", "dev_one", oldServer, nil, nil, oldSigner, time.Now())
	newestConnection := newConnection("conn_new", "dev_one", newServer, nil, nil, newSigner, time.Now())
	if _, accepted := manager.Register(oldConnection); !accepted {
		t.Fatal("old connection was rejected")
	}
	if _, accepted := manager.Register(newestConnection); !accepted {
		t.Fatal("new connection was rejected")
	}
	defer manager.CloseAll()

	headerResult := make(chan protocol.StreamHeader, 1)
	errorResult := make(chan error, 1)
	go func() {
		stream, err := newRemote.AcceptStream()
		if err != nil {
			errorResult <- err
			return
		}
		defer stream.Close()
		header, err := protocol.NewCodec(stream).ReadHeader()
		if err != nil {
			errorResult <- err
			return
		}
		headerResult <- header
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, signer, err := manager.OpenSSH(ctx, "dev_one")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !bytes.Equal(signer.PublicKey().Marshal(), newSigner.PublicKey().Marshal()) {
		t.Fatal("OpenSSH mixed an old connection signer with the newest stream")
	}
	select {
	case header := <-headerResult:
		if header.Kind != protocol.StreamSSH || header.RequestID == "" {
			t.Fatalf("SSH stream header = %+v", header)
		}
	case err := <-errorResult:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("newest connection did not receive SSH stream")
	}
	select {
	case <-oldConnection.Done():
	default:
		t.Fatal("old connection was not closed by newest-wins")
	}
}

func TestManagerOpenSSHSetupSlotsReleaseAfterSuccessAndCancellation(t *testing.T) {
	manager := NewManager()
	serverSession, remoteSession := yamuxPair(t)
	connection := newConnection(
		"conn_slots", "dev_slots", serverSession, nil, nil, testSSHSigner(t), time.Now(),
	)
	// A canceled caller that never acquires the last slot must neither consume
	// nor release another setup's ownership.
	connection.sshSetups = make(chan struct{}, 1)
	connection.sshSetups <- struct{}{}
	if _, accepted := manager.Register(connection); !accepted {
		t.Fatal("connection was rejected")
	}
	defer manager.CloseAll()
	canceledContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := manager.OpenSSH(canceledContext, "dev_slots"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked setup error = %v, want deadline", err)
	}
	if len(connection.sshSetups) != 1 {
		t.Fatalf("canceled setup changed another caller's slot ownership: len=%d", len(connection.sshSetups))
	}
	<-connection.sshSetups

	const opens = 3
	remoteDone := make(chan error, 1)
	go func() {
		for index := 0; index < opens; index++ {
			stream, err := remoteSession.AcceptStream()
			if err != nil {
				remoteDone <- err
				return
			}
			header, err := readStreamHeader(stream)
			_ = stream.Close()
			if err != nil {
				remoteDone <- err
				return
			}
			if header.Kind != protocol.StreamSSH {
				remoteDone <- protocol.ErrUnsupported
				return
			}
		}
		remoteDone <- nil
	}()
	for index := 0; index < opens; index++ {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		stream, _, err := manager.OpenSSH(ctx, "dev_slots")
		stop()
		if err != nil {
			t.Fatalf("OpenSSH %d after canceled setup: %v", index, err)
		}
		_ = stream.Close()
	}
	if err := <-remoteDone; err != nil {
		t.Fatal(err)
	}
	if len(connection.sshSetups) != 0 {
		t.Fatalf("successful setups leaked slots: len=%d", len(connection.sshSetups))
	}
}

func yamuxPair(t *testing.T) (*yamux.Session, *yamux.Session) {
	t.Helper()
	serverTransport, remoteTransport := net.Pipe()
	configuration := yamux.DefaultConfig()
	configuration.LogOutput = io.Discard
	server, err := yamux.Server(serverTransport, configuration)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := yamux.Client(remoteTransport, configuration)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = remote.Close()
	})
	return server, remote
}

func testSSHSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

type failingWriteConn struct {
	writeErr error
}

type blockingCloseConn struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type cancelOnClearConn struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (*cancelOnClearConn) Read([]byte) (int, error)        { return 0, context.Canceled }
func (*cancelOnClearConn) Write(value []byte) (int, error) { return len(value), nil }
func (*cancelOnClearConn) Close() error                    { return nil }
func (*cancelOnClearConn) LocalAddr() net.Addr             { return tunnelTestAddr("local") }
func (*cancelOnClearConn) RemoteAddr() net.Addr            { return tunnelTestAddr("remote") }
func (conn *cancelOnClearConn) SetDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		conn.once.Do(conn.cancel)
	}
	return nil
}
func (*cancelOnClearConn) SetReadDeadline(time.Time) error  { return nil }
func (*cancelOnClearConn) SetWriteDeadline(time.Time) error { return nil }

func (*blockingCloseConn) Read([]byte) (int, error)        { return 0, context.Canceled }
func (*blockingCloseConn) Write(value []byte) (int, error) { return len(value), nil }
func (conn *blockingCloseConn) Close() error {
	conn.once.Do(func() { close(conn.started) })
	<-conn.release
	return nil
}
func (*blockingCloseConn) LocalAddr() net.Addr              { return tunnelTestAddr("local") }
func (*blockingCloseConn) RemoteAddr() net.Addr             { return tunnelTestAddr("remote") }
func (*blockingCloseConn) SetDeadline(time.Time) error      { return nil }
func (*blockingCloseConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockingCloseConn) SetWriteDeadline(time.Time) error { return nil }

func (*failingWriteConn) Read([]byte) (int, error)         { return 0, context.Canceled }
func (conn *failingWriteConn) Write([]byte) (int, error)   { return 0, conn.writeErr }
func (*failingWriteConn) Close() error                     { return nil }
func (*failingWriteConn) LocalAddr() net.Addr              { return tunnelTestAddr("local") }
func (*failingWriteConn) RemoteAddr() net.Addr             { return tunnelTestAddr("remote") }
func (*failingWriteConn) SetDeadline(time.Time) error      { return nil }
func (*failingWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (*failingWriteConn) SetWriteDeadline(time.Time) error { return nil }

type tunnelTestAddr string

func (address tunnelTestAddr) Network() string { return "test" }
func (address tunnelTestAddr) String() string  { return string(address) }
