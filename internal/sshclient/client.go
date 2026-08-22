// Package sshclient exposes bounded Remote exec and PTY APIs over one SSH
// transport per Tunnel stream.
package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/sshserver"
	"github.com/aisummoner/aisummoner/internal/store"
	"github.com/aisummoner/aisummoner/internal/tunnel"
	"golang.org/x/crypto/ssh"
)

const (
	DefaultCaptureLimit = 256 * 1024
	MaxCaptureLimit     = 1024 * 1024
)

var (
	ErrInvalidHostKey = errors.New("remote SSH host key does not match Device Identity")
	ErrCaptureLimit   = errors.New("invalid SSH capture limit")
)

// StreamOpener returns one typed SSH stream and the signer from the exact same
// authenticated Tunnel connection.
type StreamOpener interface {
	OpenSSH(context.Context, string) (net.Conn, ssh.Signer, error)
}

// DeviceKeyLookup returns the raw registered Ed25519 Device public key.
type DeviceKeyLookup interface {
	DevicePublicKey(context.Context, string) (ed25519.PublicKey, error)
}

type DeviceKeyLookupFunc func(context.Context, string) (ed25519.PublicKey, error)

func (function DeviceKeyLookupFunc) DevicePublicKey(ctx context.Context, deviceID string) (ed25519.PublicKey, error) {
	return function(ctx, deviceID)
}

type deviceStore interface {
	DeviceByID(context.Context, string) (store.Device, error)
}

// StoreDeviceKeys adapts the existing Device Registry without widening its
// persistence API or exposing any private key material.
type StoreDeviceKeys struct {
	Store deviceStore
}

func (lookup StoreDeviceKeys) DevicePublicKey(ctx context.Context, deviceID string) (ed25519.PublicKey, error) {
	if lookup.Store == nil {
		return nil, errors.New("Device Store is unavailable")
	}
	device, err := lookup.Store.DeviceByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if len(device.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidHostKey
	}
	return append(ed25519.PublicKey(nil), device.PublicKey...), nil
}

type Dialer struct {
	opener StreamOpener
	keys   DeviceKeyLookup
}

func NewDialer(opener StreamOpener, keys DeviceKeyLookup) (*Dialer, error) {
	if opener == nil || keys == nil {
		return nil, errors.New("SSH Tunnel opener and Device key lookup are required")
	}
	return &Dialer{opener: opener, keys: keys}, nil
}

type ExecOptions struct {
	CWD          string
	CaptureLimit int
}

type ExecResult struct {
	Stdout          []byte
	Stderr          []byte
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

// Exec treats a non-zero remote status as a successful transport result.
// Authentication, protocol and cancellation failures remain errors.
func (d *Dialer) Exec(ctx context.Context, deviceID, command string, options ExecOptions) (ExecResult, error) {
	if command == "" || len(command) > sshserver.MaxCommandBytes || strings.ContainsRune(command, '\x00') {
		return ExecResult{}, errors.New("SSH command must be between 1 and 8192 bytes")
	}
	if err := validateCWD(options.CWD); err != nil {
		return ExecResult{}, err
	}
	limit := options.CaptureLimit
	if limit == 0 {
		limit = DefaultCaptureLimit
	}
	if limit < 1 || limit > MaxCaptureLimit {
		return ExecResult{}, ErrCaptureLimit
	}
	connection, err := d.dial(ctx, deviceID)
	if err != nil {
		return ExecResult{}, err
	}
	defer connection.Close()
	session, err := connection.client.NewSession()
	if err != nil {
		return ExecResult{}, fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()
	if options.CWD != "" {
		if err := session.Setenv(sshserver.CWDEnvironment, options.CWD); err != nil {
			return ExecResult{}, fmt.Errorf("set remote cwd: %w", err)
		}
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return ExecResult{}, fmt.Errorf("open SSH stdout: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return ExecResult{}, fmt.Errorf("open SSH stderr: %w", err)
	}
	if err := session.Start(command); err != nil {
		return ExecResult{}, fmt.Errorf("start SSH exec: %w", err)
	}
	captures := newCapturePair(limit)
	stdoutResult := make(chan error, 1)
	stderrResult := make(chan error, 1)
	go func() { stdoutResult <- captures.drain(stdout, true) }()
	go func() { stderrResult <- captures.drain(stderr, false) }()
	waitErr := session.Wait()
	stdoutErr, stderrErr := <-stdoutResult, <-stderrResult
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	if stdoutErr != nil || stderrErr != nil {
		return ExecResult{}, fmt.Errorf("read SSH exec output: %w", errors.Join(stdoutErr, stderrErr))
	}
	stdoutContents, stderrContents, stdoutTruncated, stderrTruncated := captures.result()
	result := ExecResult{
		Stdout: stdoutContents, Stderr: stderrContents,
		StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated,
	}
	if waitErr == nil {
		return result, nil
	}
	var exitError *ssh.ExitError
	if errors.As(waitErr, &exitError) {
		result.ExitCode = exitError.ExitStatus()
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	return ExecResult{}, fmt.Errorf("wait for SSH exec: %w", waitErr)
}

type PTYOptions struct {
	Cols uint16
	Rows uint16
	CWD  string
}

// PTY is the narrow interface consumed by the future Terminal gateway.
type PTY struct {
	connection *clientConnection
	session    *ssh.Session
	stdin      io.WriteCloser
	stdout     io.Reader
	waitDone   chan struct{}
	waitErr    error
	waitMu     sync.Mutex
	closeOnce  sync.Once
}

func (d *Dialer) OpenPTY(ctx context.Context, deviceID string, options PTYOptions) (*PTY, error) {
	if options.Cols < 1 || options.Cols > sshserver.MaxCols || options.Rows < 1 || options.Rows > sshserver.MaxRows {
		return nil, errors.New("invalid PTY dimensions")
	}
	if err := validateCWD(options.CWD); err != nil {
		return nil, err
	}
	connection, err := d.dial(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	session, err := connection.client.NewSession()
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("open SSH session: %w", err)
	}
	fail := func(err error) (*PTY, error) {
		_ = session.Close()
		_ = connection.Close()
		return nil, err
	}
	if options.CWD != "" {
		if err := session.Setenv(sshserver.CWDEnvironment, options.CWD); err != nil {
			return fail(fmt.Errorf("set remote cwd: %w", err))
		}
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return fail(fmt.Errorf("open SSH PTY input: %w", err))
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return fail(fmt.Errorf("open SSH PTY output: %w", err))
	}
	if err := session.RequestPty("xterm-256color", int(options.Rows), int(options.Cols), ssh.TerminalModes{}); err != nil {
		return fail(fmt.Errorf("request remote PTY: %w", err))
	}
	if err := session.Shell(); err != nil {
		return fail(fmt.Errorf("start remote shell: %w", err))
	}
	handle := &PTY{
		connection: connection, session: session, stdin: stdin, stdout: stdout,
		waitDone: make(chan struct{}),
	}
	go func() {
		err := session.Wait()
		handle.waitMu.Lock()
		handle.waitErr = normalizeWaitError(ctx, err)
		handle.waitMu.Unlock()
		close(handle.waitDone)
		_ = connection.Close()
	}()
	return handle, nil
}

func (handle *PTY) Input() io.Writer  { return handle.stdin }
func (handle *PTY) Output() io.Reader { return handle.stdout }

func (handle *PTY) Resize(cols, rows uint16) error {
	if cols < 1 || cols > sshserver.MaxCols || rows < 1 || rows > sshserver.MaxRows {
		return errors.New("invalid PTY dimensions")
	}
	return handle.session.WindowChange(int(rows), int(cols))
}

func (handle *PTY) Wait() error {
	<-handle.waitDone
	handle.waitMu.Lock()
	defer handle.waitMu.Unlock()
	return handle.waitErr
}

func (handle *PTY) Close() error {
	var result error
	handle.closeOnce.Do(func() {
		if handle.stdin != nil {
			_ = handle.stdin.Close()
		}
		if handle.session != nil {
			result = handle.session.Close()
		}
		if handle.connection != nil {
			_ = handle.connection.Close()
		}
	})
	return result
}

type clientConnection struct {
	stream net.Conn
	client *ssh.Client
	closed chan struct{}
	once   sync.Once
}

func (connection *clientConnection) Close() error {
	var result error
	connection.once.Do(func() {
		if connection.client != nil {
			result = connection.client.Close()
		}
		if connection.stream != nil {
			_ = connection.stream.Close()
		}
		if connection.closed != nil {
			close(connection.closed)
		}
	})
	return result
}

func (d *Dialer) dial(ctx context.Context, deviceID string) (*clientConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	devicePublicKey, err := d.keys.DevicePublicKey(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("load Device SSH host key: %w", err)
	}
	if len(devicePublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidHostKey
	}
	stream, signer, err := d.opener.OpenSSH(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if signer == nil || signer.PublicKey() == nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		_ = stream.Close()
		return nil, errors.New("Tunnel returned no Ed25519 SSH signer")
	}
	config := &ssh.ClientConfig{
		User: sshserver.Username,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			return verifyDeviceHostKey(devicePublicKey, key)
		},
		Timeout: 10 * time.Second,
	}
	if deadline, exists := ctx.Deadline(); exists {
		if err := stream.SetDeadline(deadline); err != nil {
			_ = stream.Close()
			return nil, err
		}
	} else {
		if err := stream.SetDeadline(time.Now().Add(config.Timeout)); err != nil {
			_ = stream.Close()
			return nil, err
		}
	}
	type handshake struct {
		connection ssh.Conn
		channels   <-chan ssh.NewChannel
		requests   <-chan *ssh.Request
		err        error
	}
	done := make(chan handshake, 1)
	go func() {
		connection, channels, requests, err := ssh.NewClientConn(stream, deviceID, config)
		done <- handshake{connection: connection, channels: channels, requests: requests, err: err}
	}()
	var result handshake
	select {
	case <-ctx.Done():
		_ = stream.Close()
		<-done
		return nil, ctx.Err()
	case result = <-done:
	}
	if result.err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("SSH handshake: %w", result.err)
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = result.connection.Close()
		_ = stream.Close()
		return nil, err
	}
	client := ssh.NewClient(result.connection, result.channels, result.requests)
	connection := &clientConnection{stream: stream, client: client, closed: make(chan struct{})}
	go func() {
		_ = client.Wait()
		_ = connection.Close()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-connection.closed:
		}
	}()
	return connection, nil
}

func verifyDeviceHostKey(raw ed25519.PublicKey, actual ssh.PublicKey) error {
	if len(raw) != ed25519.PublicKeySize || actual == nil || actual.Type() != ssh.KeyAlgoED25519 {
		return ErrInvalidHostKey
	}
	expected, err := ssh.NewPublicKey(raw)
	if err != nil {
		return ErrInvalidHostKey
	}
	expectedBytes, actualBytes := expected.Marshal(), actual.Marshal()
	if len(expectedBytes) != len(actualBytes) || subtle.ConstantTimeCompare(expectedBytes, actualBytes) != 1 {
		return ErrInvalidHostKey
	}
	return nil
}

func validateCWD(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > sshserver.MaxCWDBytes || !filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return errors.New("SSH cwd must be a bounded absolute path")
	}
	return nil
}

type capturePair struct {
	mu              sync.Mutex
	remaining       int
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stdoutTruncated bool
	stderrTruncated bool
}

func newCapturePair(limit int) *capturePair { return &capturePair{remaining: limit} }

func (captures *capturePair) drain(reader io.Reader, stdout bool) error {
	buffer := make([]byte, 32*1024)
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			captures.mu.Lock()
			accepted := read
			if accepted > captures.remaining {
				accepted = captures.remaining
			}
			if stdout {
				_, _ = captures.stdout.Write(buffer[:accepted])
				captures.stdoutTruncated = captures.stdoutTruncated || accepted < read
			} else {
				_, _ = captures.stderr.Write(buffer[:accepted])
				captures.stderrTruncated = captures.stderrTruncated || accepted < read
			}
			captures.remaining -= accepted
			captures.mu.Unlock()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (captures *capturePair) result() ([]byte, []byte, bool, bool) {
	captures.mu.Lock()
	defer captures.mu.Unlock()
	return bytes.Clone(captures.stdout.Bytes()), bytes.Clone(captures.stderr.Bytes()), captures.stdoutTruncated, captures.stderrTruncated
}

func normalizeWaitError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var exitError *ssh.ExitError
	if err == nil || errors.As(err, &exitError) {
		return nil
	}
	return err
}

var _ StreamOpener = (*tunnel.Manager)(nil)
