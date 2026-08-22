// Package sshserver provides the Linux Embedded SSHD served directly over
// authenticated Tunnel streams. It never binds or accepts a TCP listener.
package sshserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const (
	Username          = "aisummoner"
	CWDEnvironment    = "AISUMMONER_CWD"
	MaxCommandBytes   = 8192
	MaxCWDBytes       = 4096
	MaxTerminalBytes  = 128
	MaxRequestPayload = 16 * 1024
	MaxCols           = 500
	MaxRows           = 300
	cleanupQuiescence = 2
	cleanupDeadline   = 2 * time.Second
)

// Handler owns one SSH transport carried by one Tunnel stream.
type Handler struct {
	hostSigner ssh.Signer
}

func New(hostSigner ssh.Signer) (*Handler, error) {
	if hostSigner == nil || hostSigner.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("Ed25519 Device host signer is required")
	}
	return &Handler{hostSigner: hostSigner}, nil
}

// Serve handles exactly one SSH transport. authorizedKey is the ephemeral
// connection-scoped key obtained through the authenticated control stream.
func (h *Handler) Serve(ctx context.Context, stream net.Conn, authorizedKey ssh.PublicKey) error {
	if stream == nil || authorizedKey == nil || authorizedKey.Type() != ssh.KeyAlgoED25519 {
		if stream != nil {
			_ = stream.Close()
		}
		return errors.New("valid SSH stream and Ed25519 client key are required")
	}
	defer stream.Close()
	setupDeadline := time.Now().Add(10 * time.Second)
	if deadline, exists := ctx.Deadline(); exists && deadline.Before(setupDeadline) {
		setupDeadline = deadline
	}
	if err := stream.SetDeadline(setupDeadline); err != nil {
		return err
	}
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-handshakeDone:
		}
	}()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if metadata.User() != Username || !equalPublicKeys(key, authorizedKey) {
				return nil, errors.New("SSH client authentication failed")
			}
			return nil, nil
		},
	}
	config.AddHostKey(h.hostSigner)
	serverConn, channels, requests, err := ssh.NewServerConn(stream, config)
	close(handshakeDone)
	if err != nil {
		return fmt.Errorf("SSH handshake failed: %w", err)
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = serverConn.Close()
		return err
	}
	defer serverConn.Close()
	transportDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = serverConn.Close()
		case <-transportDone:
		}
	}()
	defer close(transportDone)
	globalRequestsDone := make(chan struct{})
	go func() {
		defer close(globalRequestsDone)
		rejectGlobalRequests(requests)
	}()

	var sessions sync.WaitGroup
	for incoming := range channels {
		if incoming.ChannelType() != "session" || len(incoming.ExtraData()) != 0 {
			_ = incoming.Reject(ssh.UnknownChannelType, "unsupported channel")
			continue
		}
		channel, requests, err := incoming.Accept()
		if err != nil {
			continue
		}
		sessions.Add(1)
		go func() {
			defer sessions.Done()
			h.serveSession(ctx, channel, requests)
		}()
	}
	sessions.Wait()
	<-globalRequestsDone
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func rejectGlobalRequests(requests <-chan *ssh.Request) {
	for request := range requests {
		if request.WantReply {
			_ = request.Reply(false, nil)
		}
	}
}

type sessionState struct {
	cwd      string
	pty      *ptyState
	launched bool
	running  *process
}

type ptyState struct {
	term string
	cols uint32
	rows uint32
}

func (h *Handler) serveSession(ctx context.Context, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	state := &sessionState{}
	for request := range requests {
		if len(request.Payload) > MaxRequestPayload {
			reply(request, false)
			continue
		}
		switch request.Type {
		case "env":
			reply(request, state.acceptEnvironment(request.Payload))
		case "pty-req":
			reply(request, state.acceptPTY(request.Payload))
		case "window-change":
			reply(request, state.resizePTY(request.Payload))
		case "exec":
			command, ok := parseExec(request.Payload)
			if !ok || state.launched || state.pty != nil {
				reply(request, false)
				continue
			}
			workingDirectory, err := validateWorkingDirectory(state.cwd)
			if err != nil {
				reply(request, false)
				continue
			}
			process, err := startExec(ctx, channel, command, workingDirectory)
			if err != nil {
				reply(request, false)
				continue
			}
			state.launched, state.running = true, process
			reply(request, true)
			go finishProcess(channel, process)
		case "shell":
			if len(request.Payload) != 0 || state.launched || state.pty == nil {
				reply(request, false)
				continue
			}
			workingDirectory, err := validateWorkingDirectory(state.cwd)
			if err != nil {
				reply(request, false)
				continue
			}
			process, err := startShell(ctx, channel, state.pty, workingDirectory)
			if err != nil {
				reply(request, false)
				continue
			}
			state.launched, state.running = true, process
			reply(request, true)
			go finishProcess(channel, process)
		case "signal":
			reply(request, state.signal(request.Payload))
		default:
			reply(request, false)
		}
	}
	if state.running != nil {
		state.running.terminate()
		<-state.running.done
	}
}

func reply(request *ssh.Request, accepted bool) {
	if request.WantReply {
		_ = request.Reply(accepted, nil)
	}
}

func (state *sessionState) acceptEnvironment(payload []byte) bool {
	if state.launched || state.cwd != "" {
		return false
	}
	name, rest, ok := consumeString(payload, MaxCWDBytes)
	if !ok || name != CWDEnvironment {
		return false
	}
	value, rest, ok := consumeString(rest, MaxCWDBytes)
	if !ok || len(rest) != 0 || value == "" || len(value) > MaxCWDBytes || !filepath.IsAbs(value) {
		return false
	}
	state.cwd = value
	return true
}

func (state *sessionState) acceptPTY(payload []byte) bool {
	if state.launched || state.pty != nil {
		return false
	}
	term, rest, ok := consumeString(payload, MaxTerminalBytes)
	if !ok || term == "" || len(rest) < 16 {
		return false
	}
	cols := binary.BigEndian.Uint32(rest[0:4])
	rows := binary.BigEndian.Uint32(rest[4:8])
	_, trailing, ok := consumeBytes(rest[16:], MaxRequestPayload)
	if !ok || len(trailing) != 0 || !validWindow(cols, rows) {
		return false
	}
	state.pty = &ptyState{term: term, cols: cols, rows: rows}
	return true
}

func (state *sessionState) resizePTY(payload []byte) bool {
	if !state.launched || state.running == nil || len(payload) != 16 {
		return false
	}
	cols := binary.BigEndian.Uint32(payload[0:4])
	rows := binary.BigEndian.Uint32(payload[4:8])
	if !validWindow(cols, rows) {
		return false
	}
	if !state.running.resizeTerminal(cols, rows) {
		return false
	}
	state.pty.cols, state.pty.rows = cols, rows
	return true
}

func (state *sessionState) signal(payload []byte) bool {
	if !state.launched || state.running == nil {
		return false
	}
	name, rest, ok := consumeString(payload, 16)
	if !ok || len(rest) != 0 {
		return false
	}
	var signal syscall.Signal
	switch name {
	case "INT":
		signal = syscall.SIGINT
	case "TERM":
		signal = syscall.SIGTERM
	case "KILL":
		signal = syscall.SIGKILL
	default:
		return false
	}
	return state.running.signal(signal) == nil
}

func parseExec(payload []byte) (string, bool) {
	command, rest, ok := consumeString(payload, MaxCommandBytes)
	return command, ok && len(rest) == 0 && command != "" && len(command) <= MaxCommandBytes && !strings.ContainsRune(command, '\x00')
}

func validateWorkingDirectory(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > MaxCWDBytes || !filepath.IsAbs(value) {
		return "", errors.New("working directory must be absolute")
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", errors.New("working directory is unavailable")
	}
	return value, nil
}

func userShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		return "/bin/sh"
	}
	info, err := os.Stat(shell)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "/bin/sh"
	}
	return shell
}

type process struct {
	command   *exec.Cmd
	terminal  *os.File
	ptyOps    terminalOperations
	stdin     *os.File
	anchor    *processAnchor
	pgid      int
	sid       int
	done      chan struct{}
	pumps     sync.WaitGroup
	killOnce  sync.Once
	closeOnce sync.Once

	// lifecycleMu linearizes every externally requested signal with the point
	// at which final cleanup takes exclusive ownership of the process. It must
	// never be held while waiting for the child or an I/O pump.
	lifecycleMu sync.Mutex
	lifecycle   processLifecycle

	// terminalMu linearizes every resize ioctl with every close of a
	// published PTY. If both lifecycle locks are needed, terminalMu is always
	// acquired before lifecycleMu. Neither lock is held while waiting for the
	// child, doing SSH channel I/O, or joining copy pumps.
	terminalMu        sync.Mutex
	terminalLifecycle terminalLifecycle

	// terminalCloseContended is a package-private deterministic test hook. It
	// is nil in production. When present, closeTerminal calls it only after an
	// actual terminalMu acquisition attempt has failed and immediately before
	// waiting on that same mutex.
	terminalCloseContended func()
}

type processLifecycle uint8

const (
	processActive processLifecycle = iota
	processFinalizing
	processFinished
)

type terminalLifecycle uint8

const (
	terminalActive terminalLifecycle = iota
	terminalFinalizing
	terminalClosed
)

type terminalOperations interface {
	Resize(*os.File, *pty.Winsize) error
	Close(*os.File) error
}

type linuxTerminalOperations struct{}

func (linuxTerminalOperations) Resize(terminal *os.File, size *pty.Winsize) error {
	return pty.Setsize(terminal, size)
}

func (linuxTerminalOperations) Close(terminal *os.File) error {
	return terminal.Close()
}

func startExec(ctx context.Context, channel ssh.Channel, command, cwd string) (*process, error) {
	cmd := exec.Command(userShell(), "-lc", command)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, channel, channel.Stderr()
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return nil, err
	}
	_ = stdinRead.Close()
	anchor, err := openProcessAnchor(cmd.Process.Pid)
	if err != nil || anchor.identity.processGroup != cmd.Process.Pid {
		killStartedProcess(cmd, anchor)
		_ = stdinWrite.Close()
		if err == nil {
			err = errors.New("exec process group was not isolated")
		}
		return nil, err
	}
	value := &process{
		command: cmd, stdin: stdinWrite, anchor: anchor,
		pgid: anchor.identity.processGroup, done: make(chan struct{}),
	}
	value.watchContext(ctx)
	value.pumps.Add(1)
	go func() {
		defer value.pumps.Done()
		_, _ = io.Copy(stdinWrite, channel)
		_ = stdinWrite.Close()
	}()
	return value, nil
}

func startShell(ctx context.Context, channel ssh.Channel, request *ptyState, cwd string) (*process, error) {
	cmd := exec.Command(userShell(), "-i")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM="+request.term)
	terminal, err := pty.StartWithAttrs(
		cmd,
		&pty.Winsize{Cols: uint16(request.cols), Rows: uint16(request.rows)},
		&syscall.SysProcAttr{Setsid: true, Setctty: true},
	)
	if err != nil {
		return nil, err
	}
	anchor, err := openProcessAnchor(cmd.Process.Pid)
	if err != nil || anchor.identity.processGroup != cmd.Process.Pid || anchor.identity.session != cmd.Process.Pid {
		killStartedProcess(cmd, anchor)
		_ = terminal.Close()
		if err == nil {
			err = errors.New("PTY process did not create an isolated session")
		}
		return nil, err
	}
	value := &process{
		command: cmd, terminal: terminal, ptyOps: linuxTerminalOperations{}, anchor: anchor,
		pgid: anchor.identity.processGroup, sid: anchor.identity.session,
		done: make(chan struct{}),
	}
	value.watchContext(ctx)
	value.pumps.Add(2)
	go func() {
		defer value.pumps.Done()
		_, _ = io.Copy(terminal, channel)
		value.terminate()
	}()
	go func() {
		defer value.pumps.Done()
		_, _ = io.Copy(channel, terminal)
	}()
	return value, nil
}

func (process *process) watchContext(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			process.terminate()
		case <-process.done:
		}
	}()
}

func (process *process) wait() int {
	// Observe exit without reaping. Keeping the leader as a zombie anchors its
	// PID, process-group ID and PTY session ID while the final pidfd-only sweep
	// removes descendants that may have forked after an earlier cancellation.
	waitErr := process.anchor.waitExited()
	if waitErr != nil {
		process.terminate()
		// A transient waitid failure must not permit finalization to race a
		// still-running leader. Give the pidfd wait one final opportunity after
		// fail-closed termination; a persistent error is reflected in status 255.
		if retryErr := process.anchor.waitExited(); retryErr == nil {
			waitErr = nil
		} else {
			waitErr = errors.Join(waitErr, retryErr)
		}
	}
	if !process.beginFinalization() {
		return 255
	}
	cleanupErr := errors.Join(waitErr, process.finalCleanup())
	err := process.command.Wait()
	status := 0
	if err != nil {
		if errors.Is(err, exec.ErrWaitDelay) && process.command.ProcessState != nil && process.command.ProcessState.Success() {
			err = nil
		}
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			status = exitError.ExitCode()
			if status < 0 {
				status = 128
			}
		} else {
			status = 255
		}
	}
	if cleanupErr != nil {
		status = 255
	}
	return status
}

func (process *process) signal(signal syscall.Signal) error {
	process.lifecycleMu.Lock()
	defer process.lifecycleMu.Unlock()
	// Check state before touching the anchor or numeric process scope. Once the
	// finalizer owns the process, a late SSH signal fails closed without looking
	// at identities that may soon become reusable.
	if process.lifecycle != processActive {
		return errors.New("process is unavailable")
	}
	if process.command == nil || process.command.Process == nil || process.anchor == nil || process.pgid <= 1 {
		return errors.New("process is unavailable")
	}
	scope := process.scope()
	return process.anchor.withVerified(func(pidfd int, identity processIdentity, ops processSignalOps) error {
		result := ops.Send(pidfd, signal)
		_, descendantResult := signalProcessScope(scope, identity.pid, signal, ops)
		if errors.Is(descendantResult, syscall.ESRCH) {
			descendantResult = nil
		}
		return errors.Join(result, descendantResult)
	})
}

func (process *process) terminate() {
	process.killOnce.Do(func() {
		_ = process.signal(syscall.SIGKILL)
		if process.stdin != nil {
			_ = process.stdin.Close()
		}
		process.closeTerminal()
	})
}

func (process *process) resizeTerminal(cols, rows uint32) bool {
	process.terminalMu.Lock()
	defer process.terminalMu.Unlock()
	if process.terminalLifecycle != terminalActive || process.terminal == nil || process.ptyOps == nil {
		return false
	}
	return process.ptyOps.Resize(process.terminal, &pty.Winsize{
		Cols: uint16(cols), Rows: uint16(rows),
	}) == nil
}

func (process *process) closeTerminal() {
	process.lockTerminalForClose()
	defer process.terminalMu.Unlock()
	if process.terminal == nil || process.terminalLifecycle == terminalClosed {
		return
	}
	process.terminalLifecycle = terminalFinalizing
	operations := process.ptyOps
	if operations == nil {
		operations = linuxTerminalOperations{}
	}
	_ = operations.Close(process.terminal)
	process.terminalLifecycle = terminalClosed
}

func (process *process) lockTerminalForClose() {
	if process.terminalCloseContended == nil {
		process.terminalMu.Lock()
		return
	}
	if process.terminalMu.TryLock() {
		return
	}
	process.terminalCloseContended()
	process.terminalMu.Lock()
}

func (process *process) scope() processScope {
	scope := processScope{processGroup: process.pgid}
	if process.sid > 1 {
		scope = processScope{session: process.sid}
	}
	return scope
}

func (process *process) beginFinalization() bool {
	// terminalMu must be first so publication of terminalFinalizing cannot race
	// a resize that has already crossed its active-state check. No other path
	// acquires lifecycleMu and then terminalMu.
	process.terminalMu.Lock()
	process.lifecycleMu.Lock()
	if process.lifecycle != processActive {
		process.lifecycleMu.Unlock()
		process.terminalMu.Unlock()
		return false
	}
	process.lifecycle = processFinalizing
	if process.terminal != nil && process.terminalLifecycle == terminalActive {
		process.terminalLifecycle = terminalFinalizing
	}
	process.lifecycleMu.Unlock()
	process.terminalMu.Unlock()
	return true
}

// finalCleanup is private to the finalizer. It deliberately does not call
// signal or terminate, because public signaling is rejected after
// beginFinalization. The leader remains unreaped and its pidfd remains leased
// for the complete final repeat-scan.
func (process *process) finalCleanup() error {
	if process.anchor == nil {
		return errors.New("process identity is unavailable")
	}
	scope := process.scope()
	return process.anchor.withVerified(func(pidfd int, identity processIdentity, ops processSignalOps) error {
		leaderErr := ops.Send(pidfd, syscall.SIGKILL)
		if errors.Is(leaderErr, syscall.ESRCH) {
			leaderErr = nil
		}
		return errors.Join(leaderErr, clearProcessScope(scope, identity.pid, cleanupDeadline, ops))
	})
}

type processIdentity struct {
	pid          int
	processGroup int
	session      int
	startTime    uint64
}

type processScope struct {
	processGroup int
	session      int
}

func (scope processScope) matches(identity processIdentity) bool {
	if scope.session > 1 {
		return identity.session == scope.session
	}
	return scope.processGroup > 1 && identity.processGroup == scope.processGroup
}

type processSignalOps interface {
	Candidates() ([]int, error)
	Open(int) (int, error)
	Identity(int) (processIdentity, error)
	PID(int) (int, error)
	Send(int, syscall.Signal) error
	Close(int) error
}

type linuxProcessSignalOps struct{}

func (linuxProcessSignalOps) Candidates() ([]int, error) { return procProcessIDs() }
func (linuxProcessSignalOps) Open(pid int) (int, error)  { return unix.PidfdOpen(pid, 0) }
func (linuxProcessSignalOps) Identity(pid int) (processIdentity, error) {
	return readProcessIdentity(pid)
}
func (linuxProcessSignalOps) PID(pidfd int) (int, error) { return readPIDFDProcessID(pidfd) }
func (linuxProcessSignalOps) Send(pidfd int, signal syscall.Signal) error {
	return unix.PidfdSendSignal(pidfd, unix.Signal(signal), nil, 0)
}
func (linuxProcessSignalOps) Close(pidfd int) error { return unix.Close(pidfd) }

type processAnchor struct {
	mu       sync.RWMutex
	fd       int
	identity processIdentity
	ops      processSignalOps
	closed   bool
}

func openProcessAnchor(pid int) (*processAnchor, error) {
	ops := linuxProcessSignalOps{}
	pidfd, err := ops.Open(pid)
	if err != nil {
		return nil, err
	}
	anchor := &processAnchor{fd: pidfd, ops: ops}
	identity, err := verifiedPIDFDIdentity(pid, pidfd, ops)
	if err != nil {
		_ = ops.Close(pidfd)
		return nil, err
	}
	anchor.identity = identity
	return anchor, nil
}

func (anchor *processAnchor) send(signal syscall.Signal) error {
	return anchor.withVerified(func(pidfd int, _ processIdentity, ops processSignalOps) error {
		return ops.Send(pidfd, signal)
	})
}

// withVerified keeps a read lease on the leader pidfd from identity
// verification through signal delivery and any numeric scope scan performed by
// fn. Close therefore cannot recycle the descriptor in the middle of an
// identity-sensitive operation.
func (anchor *processAnchor) withVerified(fn func(int, processIdentity, processSignalOps) error) error {
	if anchor == nil {
		return errors.New("process identity is unavailable")
	}
	anchor.mu.RLock()
	defer anchor.mu.RUnlock()
	if anchor.closed || anchor.fd < 0 || anchor.ops == nil {
		return errors.New("process identity is unavailable")
	}
	identity, err := verifiedPIDFDIdentity(anchor.identity.pid, anchor.fd, anchor.ops)
	if err != nil {
		return err
	}
	if identity != anchor.identity {
		return syscall.ESRCH
	}
	return fn(anchor.fd, identity, anchor.ops)
}

func (anchor *processAnchor) waitExited() error {
	if anchor == nil {
		return errors.New("process identity is unavailable")
	}
	anchor.mu.RLock()
	if anchor.closed || anchor.fd < 0 {
		anchor.mu.RUnlock()
		return errors.New("process identity is unavailable")
	}
	pidfd := anchor.fd
	anchor.mu.RUnlock()
	// The owning process lifecycle guarantees Close cannot begin before this
	// observation returns. Do not retain a mutex while blocking for child exit.
	for {
		err := unix.Waitid(unix.P_PIDFD, pidfd, nil, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func (anchor *processAnchor) Close() {
	if anchor == nil {
		return
	}
	anchor.mu.Lock()
	defer anchor.mu.Unlock()
	if anchor.closed {
		return
	}
	if anchor.ops != nil && anchor.fd >= 0 {
		_ = anchor.ops.Close(anchor.fd)
	}
	anchor.closed = true
	anchor.fd = -1
}

func killStartedProcess(command *exec.Cmd, anchor *processAnchor) {
	if anchor != nil {
		_ = anchor.send(syscall.SIGKILL)
	} else if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
	if command != nil {
		_ = command.Wait()
	}
	if anchor != nil {
		anchor.Close()
	}
}

func signalProcessScope(scope processScope, leaderPID int, signal syscall.Signal, ops processSignalOps) (int, error) {
	pids, err := ops.Candidates()
	if err != nil {
		return 0, err
	}
	filtered := pids[:0]
	for _, pid := range pids {
		if pid > 1 && pid != leaderPID {
			filtered = append(filtered, pid)
		}
	}
	return signalProcessCandidates(scope, signal, filtered, ops)
}

func procProcessIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "" || entry.Name()[0] < '0' || entry.Name()[0] > '9' {
			continue
		}
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr == nil && pid > 1 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func signalProcessCandidates(scope processScope, signal syscall.Signal, pids []int, ops processSignalOps) (int, error) {
	matched := 0
	var result error
	for _, pid := range pids {
		pidfd, err := ops.Open(pid)
		if err != nil {
			if !errors.Is(err, syscall.ESRCH) {
				result = errors.Join(result, err)
			}
			continue
		}
		identity, verifyErr := verifiedPIDFDIdentity(pid, pidfd, ops)
		if verifyErr == nil && scope.matches(identity) {
			matched++
			if sendErr := ops.Send(pidfd, signal); sendErr != nil && !errors.Is(sendErr, syscall.ESRCH) {
				result = errors.Join(result, sendErr)
			}
		} else if verifyErr != nil && !errors.Is(verifyErr, syscall.ESRCH) && !errors.Is(verifyErr, os.ErrNotExist) {
			result = errors.Join(result, verifyErr)
		}
		_ = ops.Close(pidfd)
	}
	return matched, result
}

func clearProcessScope(scope processScope, leaderPID int, timeout time.Duration, ops processSignalOps) error {
	deadline := time.Now().Add(timeout)
	quietScans := 0
	for {
		matched, err := signalProcessScope(scope, leaderPID, syscall.SIGKILL, ops)
		if err != nil {
			return err
		}
		if matched == 0 {
			quietScans++
			if quietScans >= cleanupQuiescence {
				return nil
			}
		} else {
			quietScans = 0
		}
		if time.Now().After(deadline) {
			return errors.New("process descendants did not quiesce before cleanup deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

func verifiedPIDFDIdentity(pid, pidfd int, ops processSignalOps) (processIdentity, error) {
	identity, err := ops.Identity(pid)
	if err != nil {
		return processIdentity{}, err
	}
	anchoredPID, err := ops.PID(pidfd)
	if err != nil || anchoredPID != pid {
		return processIdentity{}, syscall.ESRCH
	}
	confirmed, err := ops.Identity(pid)
	if err != nil {
		return processIdentity{}, err
	}
	if identity != confirmed || identity.pid != pid {
		return processIdentity{}, syscall.ESRCH
	}
	return identity, nil
}

func readProcessIdentity(pid int) (processIdentity, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return processIdentity{}, err
	}
	closingParen := bytes.LastIndexByte(stat, ')')
	openingParen := bytes.IndexByte(stat, '(')
	if openingParen < 1 || closingParen < openingParen || closingParen+1 >= len(stat) {
		return processIdentity{}, errors.New("malformed process stat")
	}
	parsedPID, err := strconv.Atoi(strings.TrimSpace(string(stat[:openingParen])))
	if err != nil {
		return processIdentity{}, errors.New("malformed process stat pid")
	}
	fields := strings.Fields(string(stat[closingParen+1:]))
	// After comm: state, ppid, pgrp, session, ... starttime is field 22
	// overall and therefore index 19 in this slice.
	if len(fields) <= 19 {
		return processIdentity{}, errors.New("malformed process stat fields")
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil {
		return processIdentity{}, errors.New("malformed process group")
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return processIdentity{}, errors.New("malformed process session")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processIdentity{}, errors.New("malformed process start time")
	}
	return processIdentity{pid: parsedPID, processGroup: processGroup, session: session, startTime: startTime}, nil
}

func readPIDFDProcessID(pidfd int) (int, error) {
	contents, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", strconv.Itoa(pidfd)))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		value, found := strings.CutPrefix(line, "Pid:")
		if !found {
			continue
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil || pid <= 0 {
			return 0, syscall.ESRCH
		}
		return pid, nil
	}
	return 0, errors.New("pidfd identity is unavailable")
}

func finishProcess(channel ssh.Channel, process *process) {
	status := process.wait()
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(status))
	_, _ = channel.SendRequest("exit-status", false, payload)
	_ = channel.CloseWrite()
	_ = channel.Close()
	process.closeOnce.Do(func() {
		if process.stdin != nil {
			_ = process.stdin.Close()
		}
		process.closeTerminal()
		process.pumps.Wait()
		process.lifecycleMu.Lock()
		process.anchor.Close()
		process.lifecycle = processFinished
		close(process.done)
		process.lifecycleMu.Unlock()
	})
}

func equalPublicKeys(left, right ssh.PublicKey) bool {
	if left == nil || right == nil || left.Type() != ssh.KeyAlgoED25519 || right.Type() != ssh.KeyAlgoED25519 {
		return false
	}
	leftBytes, rightBytes := left.Marshal(), right.Marshal()
	return len(leftBytes) == len(rightBytes) && subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

func validWindow(cols, rows uint32) bool {
	return cols >= 1 && cols <= MaxCols && rows >= 1 && rows <= MaxRows
}

func consumeString(payload []byte, limit int) (string, []byte, bool) {
	value, rest, ok := consumeBytes(payload, limit)
	return string(value), rest, ok
}

func consumeBytes(payload []byte, limit int) ([]byte, []byte, bool) {
	if len(payload) < 4 {
		return nil, nil, false
	}
	size := int(binary.BigEndian.Uint32(payload[:4]))
	if size < 0 || size > limit || size > len(payload)-4 {
		return nil, nil, false
	}
	return bytes.Clone(payload[4 : 4+size]), payload[4+size:], true
}
