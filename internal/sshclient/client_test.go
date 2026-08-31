//go:build linux

package sshclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/store"
	"golang.org/x/crypto/ssh"
)

func TestExecSeparatesOutputStatusAndValidatesCWD(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	directory := t.TempDir()
	result, err := dialer.Exec(context.Background(), "dev_test", "printf stdout; printf stderr >&2; exit 7", ExecOptions{CWD: directory})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Stdout) != "stdout" || string(result.Stderr) != "stderr" || result.ExitCode != 7 {
		t.Fatalf("exec result = %+v", result)
	}
	result, err = dialer.Exec(context.Background(), "dev_test", "pwd -P", ExecOptions{CWD: directory})
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(result.Stdout)) != wantDirectory {
		t.Fatalf("remote cwd = %q, want %q", result.Stdout, wantDirectory)
	}
	for _, cwd := range []string{"relative", filepath.Join(directory, "missing")} {
		if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{CWD: cwd}); err == nil {
			t.Fatalf("invalid cwd %q was accepted", cwd)
		}
	}
	if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{CWD: "/tmp\x00invalid"}); err == nil {
		t.Fatal("NUL-containing cwd was accepted")
	}
}

func TestExecContextDeadlineCancelsRemoteProcess(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := dialer.Exec(ctx, "dev_test", "sleep 30", ExecOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline exec error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline exec returned after %v", elapsed)
	}
}

func TestExecCombinedCaptureIsBoundedAndDrained(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	exact, err := dialer.Exec(
		context.Background(), "dev_test",
		"head -c 32768 /dev/zero | tr '\\000' A; head -c 32768 /dev/zero | tr '\\000' B >&2",
		ExecOptions{CaptureLimit: 65536},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(exact.Stdout, bytes.Repeat([]byte{'A'}, 32768)) || !bytes.Equal(exact.Stderr, bytes.Repeat([]byte{'B'}, 32768)) {
		t.Fatalf("exact high-volume output lengths = %d/%d", len(exact.Stdout), len(exact.Stderr))
	}
	if exact.StdoutTruncated || exact.StderrTruncated {
		t.Fatal("below-limit exact output was marked truncated")
	}
	result, err := dialer.Exec(
		context.Background(), "dev_test",
		"yes O | head -c 131072; yes E | head -c 131072 >&2; exit 9",
		ExecOptions{CaptureLimit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Stdout) + len(result.Stderr); got != 100 {
		t.Fatalf("combined capture = %d bytes, want 100", got)
	}
	if !result.StdoutTruncated && !result.StderrTruncated {
		t.Fatal("bounded output did not report truncation")
	}
	if result.ExitCode != 9 {
		t.Fatalf("large drained exec exit code = %d", result.ExitCode)
	}
	if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{CaptureLimit: MaxCaptureLimit + 1}); !errors.Is(err, ErrCaptureLimit) {
		t.Fatalf("capture limit error = %v", err)
	}
}

func TestDialRejectsWrongHostClientKeyAndUsername(t *testing.T) {
	fixture := newSSHFixture(t)
	wrongHostPublic, _ := generateSigner(t)
	wrongClientPublic, wrongClientSigner := generateSigner(t)
	_ = wrongClientPublic

	t.Run("host key", func(t *testing.T) {
		dialer := fixture.dialer(t, wrongHostPublic, fixture.clientSigner)
		if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{}); !errors.Is(err, ErrInvalidHostKey) {
			t.Fatalf("wrong host-key error = %v", err)
		}
	})
	t.Run("client key", func(t *testing.T) {
		dialer := fixture.dialer(t, fixture.hostPublicKey, wrongClientSigner)
		if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{}); err == nil {
			t.Fatal("wrong connection-scoped client key was accepted")
		}
	})
	t.Run("username", func(t *testing.T) {
		stream, signer, err := fixture.OpenSSH(context.Background(), "dev_test")
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		config := &ssh.ClientConfig{
			User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
				return verifyDeviceHostKey(fixture.hostPublicKey, key)
			},
		}
		if _, _, _, err := ssh.NewClientConn(stream, "dev_test", config); err == nil {
			t.Fatal("wrong SSH username was accepted")
		}
	})
}

func TestDialRejectsNonEd25519SignerAndPropagatesDeadlineFailure(t *testing.T) {
	fixture := newSSHFixture(t)
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonEd25519Signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	dialer, err := NewDialer(
		staticOpener{stream: clientSide, signer: nonEd25519Signer},
		DeviceKeyLookupFunc(func(context.Context, string) (ed25519.PublicKey, error) {
			return fixture.hostPublicKey, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{}); err == nil {
		t.Fatal("non-Ed25519 connection signer was accepted")
	}

	wantError := errors.New("deadline setup failed")
	clientSide, serverSide = net.Pipe()
	defer serverSide.Close()
	wrapped := &deadlineFailConn{Conn: clientSide, err: wantError}
	dialer, err = NewDialer(
		staticOpener{stream: wrapped, signer: fixture.clientSigner},
		DeviceKeyLookupFunc(func(context.Context, string) (ed25519.PublicKey, error) {
			return fixture.hostPublicKey, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialer.Exec(context.Background(), "dev_test", "true", ExecOptions{}); !errors.Is(err, wantError) {
		t.Fatalf("deadline setup error = %v, want %v", err, wantError)
	}
	if !wrapped.closed {
		t.Fatal("stream remained open after deadline setup failure")
	}
}

func TestStoreDeviceKeysClonesExactEd25519KeyAndPropagatesErrors(t *testing.T) {
	publicKey, _ := generateSigner(t)
	deviceStore := &deviceStoreFake{device: store.Device{ID: "dev_test", PublicKey: append([]byte(nil), publicKey...)}}
	lookup := StoreDeviceKeys{Store: deviceStore}
	got, err := lookup.DevicePublicKey(context.Background(), "dev_test")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, publicKey) {
		t.Fatal("StoreDeviceKeys returned a different public key")
	}
	got[0] ^= 0xff
	if bytes.Equal(got, deviceStore.device.PublicKey) {
		t.Fatal("StoreDeviceKeys leaked the Store key slice")
	}
	deviceStore.device.PublicKey = []byte("malformed")
	if _, err := lookup.DevicePublicKey(context.Background(), "dev_test"); !errors.Is(err, ErrInvalidHostKey) {
		t.Fatalf("malformed Store key error = %v", err)
	}
	wantError := errors.New("store lookup failed")
	deviceStore.err = wantError
	if _, err := lookup.DevicePublicKey(context.Background(), "dev_test"); !errors.Is(err, wantError) {
		t.Fatalf("Store error = %v, want %v", err, wantError)
	}
	if _, err := (StoreDeviceKeys{}).DevicePublicKey(context.Background(), "dev_test"); err == nil {
		t.Fatal("nil Store adapter was accepted")
	}
}

func TestServerRejectsGlobalChannelsAndMalformedSessionRequests(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	connection, err := dialer.dial(context.Background(), "dev_test")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	accepted, _, err := connection.client.SendRequest("forbidden-global", true, nil)
	if err != nil || accepted {
		t.Fatalf("global request accepted=%v err=%v", accepted, err)
	}
	for _, channelType := range []string{"direct-tcpip", "forwarded-tcpip", "auth-agent@openssh.com", "x11", "unknown"} {
		if _, _, err := connection.client.OpenChannel(channelType, nil); err == nil {
			t.Fatalf("forbidden %q channel was accepted", channelType)
		}
	}
	if _, _, err := connection.client.OpenChannel("session", []byte{1}); err == nil {
		t.Fatal("session channel with extra data was accepted")
	}

	channel, requests, err := connection.client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	requestsToReject := []struct {
		name    string
		payload []byte
	}{
		{name: "env", payload: ssh.Marshal(struct{ Name, Value string }{"PATH", "/bin"})},
		{name: "exec", payload: []byte{0, 0, 0, 20, 'x'}},
		{name: "shell", payload: []byte{0}},
		{name: "window-change", payload: make([]byte, 16)},
		{name: "pty-req", payload: ssh.Marshal(struct {
			Term                      string
			Cols, Rows, Width, Height uint32
			Modes                     string
		}{"xterm", sshserver.MaxCols + 1, 24, 0, 0, ""})},
	}
	for _, request := range requestsToReject {
		accepted, err := channel.SendRequest(request.name, true, request.payload)
		if err != nil || accepted {
			t.Fatalf("%s accepted=%v err=%v", request.name, accepted, err)
		}
	}
	accepted, err = channel.SendRequest("exec", true, marshalSSHString("sleep 30"))
	if err != nil || !accepted {
		t.Fatalf("first exec accepted=%v err=%v", accepted, err)
	}
	accepted, err = channel.SendRequest("exec", true, marshalSSHString("true"))
	if err != nil || accepted {
		t.Fatalf("second launch accepted=%v err=%v", accepted, err)
	}
	accepted, err = channel.SendRequest("signal", true, marshalSSHString("UNKNOWN"))
	if err != nil || accepted {
		t.Fatalf("unknown signal accepted=%v err=%v", accepted, err)
	}
}

func TestExecExitDoesNotWaitForeverForClientEOF(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	connection, err := dialer.dial(context.Background(), "dev_test")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, requests, err := connection.client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	accepted, err := channel.SendRequest("exec", true, marshalSSHString("exit 0"))
	if err != nil || !accepted {
		t.Fatalf("exec accepted=%v err=%v", accepted, err)
	}
	// Deliberately keep the channel write side open. The Embedded SSHD owns a
	// bounded stdin-copy lifecycle and must still publish exit-status.
	select {
	case request, ok := <-requests:
		if !ok || request.Type != "exit-status" || len(request.Payload) != 4 || binary.BigEndian.Uint32(request.Payload) != 0 {
			t.Fatalf("exit request = %+v, open=%v", request, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec waited indefinitely for client stdin EOF")
	}
}

func TestPTYInitialSizeResizeAndIdempotentClose(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	handle, err := dialer.OpenPTY(context.Background(), "dev_test", PTYOptions{Cols: 120, Rows: 36, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	collector := &lockedBuffer{}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(collector, handle.Output())
		close(copyDone)
	}()
	if _, err := io.WriteString(handle.Input(), "stty -echo; printf '__INITIAL__ '; stty size; printf '__INITIAL_END__\\n'\n"); err != nil {
		t.Fatal(err)
	}
	waitForOutputCount(t, collector, "__INITIAL_END__", 2)
	waitForOutputCount(t, collector, "36 120", 1)
	if output := collector.String(); !strings.Contains(normalizePTYOutput(output), "__INITIAL__ 36 120") {
		t.Fatalf("initial PTY size missing from %q", output)
	}
	if err := handle.Resize(123, 45); err != nil {
		t.Fatal(err)
	}
	if err := handle.Resize(0, 45); err == nil {
		t.Fatal("zero-width PTY resize was accepted")
	}
	if _, err := io.WriteString(handle.Input(), "printf '__RESIZED__ '; stty size; printf '__RESIZED_END__\\n'\n"); err != nil {
		t.Fatal(err)
	}
	waitForOutputCount(t, collector, "__RESIZED_END__", 1)
	waitForOutputCount(t, collector, "45 123", 1)
	if output := collector.String(); !strings.Contains(normalizePTYOutput(output), "__RESIZED__ 45 123") {
		t.Fatalf("resized PTY size missing from %q", output)
	}
	if _, err := io.WriteString(handle.Input(), "exit\n"); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- handle.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY shell did not exit")
	}
	_ = handle.Close()
	_ = handle.Close()
	select {
	case <-copyDone:
	case <-time.After(time.Second):
		t.Fatal("PTY output copy remained blocked")
	}
}

func TestExecCancelKillsAndReapsProcessGroup(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := dialer.Exec(ctx, "dev_test", "sleep 30 & echo $! > child.pid; wait", ExecOptions{CWD: directory})
		done <- err
	}()
	pid := waitForPIDFile(t, filepath.Join(directory, "child.pid"))
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("exec cancel error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled exec did not return")
	}
	waitForProcessGone(t, pid)
}

func TestExecNormalParentExitKillsBackgroundDescendant(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	directory := t.TempDir()
	result, err := dialer.Exec(
		context.Background(), "dev_test",
		"sleep 30 & echo $! > child.pid",
		ExecOptions{CWD: directory},
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("background-parent exec result=%+v err=%v", result, err)
	}
	pid := waitForPIDFile(t, filepath.Join(directory, "child.pid"))
	waitForProcessGone(t, pid)
}

func TestPTYDisconnectKillsAndReapsChild(t *testing.T) {
	fixture := newSSHFixture(t)
	dialer := fixture.dialer(t, fixture.hostPublicKey, fixture.clientSigner)
	directory := t.TempDir()
	handle, err := dialer.OpenPTY(context.Background(), "dev_test", PTYOptions{Cols: 80, Rows: 24, CWD: directory})
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, handle.Output())
	if _, err := io.WriteString(handle.Input(), "sleep 30 & echo $! > child.pid; wait\n"); err != nil {
		t.Fatal(err)
	}
	pid := waitForPIDFile(t, filepath.Join(directory, "child.pid"))
	_ = handle.Close()
	waitDone := make(chan error, 1)
	go func() { waitDone <- handle.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected PTY did not return")
	}
	waitForProcessGone(t, pid)
}

type sshFixture struct {
	handler       *sshserver.Handler
	hostPublicKey ed25519.PublicKey
	clientSigner  ssh.Signer
	authorizedKey ssh.PublicKey
}

func newSSHFixture(t *testing.T) *sshFixture {
	t.Helper()
	hostPublic, hostSigner := generateSigner(t)
	_, clientSigner := generateSigner(t)
	handler, err := sshserver.New(hostSigner)
	if err != nil {
		t.Fatal(err)
	}
	return &sshFixture{
		handler: handler, hostPublicKey: hostPublic,
		clientSigner: clientSigner, authorizedKey: clientSigner.PublicKey(),
	}
}

func (fixture *sshFixture) dialer(t *testing.T, hostPublic ed25519.PublicKey, returnedSigner ssh.Signer) *Dialer {
	t.Helper()
	opener := &fixtureOpener{
		handler: fixture.handler, authorizedKey: fixture.authorizedKey,
		returnedSigner: returnedSigner,
	}
	dialer, err := NewDialer(opener, DeviceKeyLookupFunc(func(context.Context, string) (ed25519.PublicKey, error) {
		return append(ed25519.PublicKey(nil), hostPublic...), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return dialer
}

func (fixture *sshFixture) OpenSSH(ctx context.Context, _ string) (net.Conn, ssh.Signer, error) {
	return (&fixtureOpener{
		handler: fixture.handler, authorizedKey: fixture.authorizedKey,
		returnedSigner: fixture.clientSigner,
	}).OpenSSH(ctx, "dev_test")
}

type fixtureOpener struct {
	handler        *sshserver.Handler
	authorizedKey  ssh.PublicKey
	returnedSigner ssh.Signer
}

type staticOpener struct {
	stream net.Conn
	signer ssh.Signer
}

type deviceStoreFake struct {
	device store.Device
	err    error
}

func (fake *deviceStoreFake) DeviceByID(context.Context, string) (store.Device, error) {
	if fake.err != nil {
		return store.Device{}, fake.err
	}
	return fake.device, nil
}

func (opener staticOpener) OpenSSH(context.Context, string) (net.Conn, ssh.Signer, error) {
	return opener.stream, opener.signer, nil
}

type deadlineFailConn struct {
	net.Conn
	err    error
	closed bool
}

func (connection *deadlineFailConn) SetDeadline(time.Time) error { return connection.err }
func (connection *deadlineFailConn) Close() error {
	connection.closed = true
	return connection.Conn.Close()
}

func (opener *fixtureOpener) OpenSSH(_ context.Context, _ string) (net.Conn, ssh.Signer, error) {
	clientSide, serverSide, err := unixSocketPair()
	if err != nil {
		return nil, nil, err
	}
	go func() {
		_ = opener.handler.Serve(context.Background(), serverSide, opener.authorizedKey)
	}()
	return clientSide, opener.returnedSigner, nil
}

func unixSocketPair() (net.Conn, net.Conn, error) {
	descriptors, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	firstFile := os.NewFile(uintptr(descriptors[0]), "aisummoner-ssh-client")
	secondFile := os.NewFile(uintptr(descriptors[1]), "aisummoner-ssh-server")
	first, firstErr := net.FileConn(firstFile)
	second, secondErr := net.FileConn(secondFile)
	_ = firstFile.Close()
	_ = secondFile.Close()
	if firstErr != nil || secondErr != nil {
		if first != nil {
			_ = first.Close()
		}
		if second != nil {
			_ = second.Close()
		}
		return nil, nil, errors.Join(firstErr, secondErr)
	}
	return first, second, nil
}

func generateSigner(t *testing.T) (ed25519.PublicKey, ssh.Signer) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, signer
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(contents)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitForOutputCount(t *testing.T, buffer *lockedBuffer, marker string, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(buffer.String(), marker) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("PTY output did not contain %d copies of %q; output=%q", count, marker, buffer.String())
}

func normalizePTYOutput(value string) string {
	return strings.ReplaceAll(value, "\r", "")
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr == nil && pid > 1 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process pid was not written to %s", path)
	return 0
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d remained after SSH closure", pid)
}

func marshalSSHString(value string) []byte {
	payload := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(payload, uint32(len(value)))
	copy(payload[4:], value)
	return payload
}
