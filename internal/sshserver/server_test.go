package sshserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

func TestNewRequiresEd25519HostKey(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil host signer was accepted")
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(signer); err != nil {
		t.Fatalf("Ed25519 host signer rejected: %v", err)
	}
}

func TestExecPayloadValidation(t *testing.T) {
	valid := marshalString("printf ok")
	if command, ok := parseExec(valid); !ok || command != "printf ok" {
		t.Fatalf("valid exec rejected: command=%q ok=%v", command, ok)
	}
	cases := map[string][]byte{
		"empty":          marshalString(""),
		"truncated":      {0, 0, 0, 9, 'x'},
		"trailing":       append(marshalString("true"), 0),
		"nul":            marshalString("true\x00false"),
		"oversized":      marshalString(strings.Repeat("x", MaxCommandBytes+1)),
		"malformed_size": {0xff, 0xff, 0xff, 0xff},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseExec(payload); ok {
				t.Fatal("malformed exec payload was accepted")
			}
		})
	}
}

func TestEnvironmentPayloadAllowsOneAbsoluteCWDOnly(t *testing.T) {
	state := &sessionState{}
	directory := t.TempDir()
	if !state.acceptEnvironment(append(marshalString(CWDEnvironment), marshalString(directory)...)) {
		t.Fatal("valid AISUMMONER_CWD was rejected")
	}
	if state.cwd != directory {
		t.Fatalf("cwd = %q", state.cwd)
	}
	if state.acceptEnvironment(append(marshalString(CWDEnvironment), marshalString(directory)...)) {
		t.Fatal("duplicate cwd was accepted")
	}
	tests := [][]byte{
		append(marshalString("PATH"), marshalString("/bin")...),
		append(marshalString(CWDEnvironment), marshalString("relative")...),
		append(append(marshalString(CWDEnvironment), marshalString(directory)...), 0),
		{0, 0, 0, 20, 'x'},
	}
	for _, payload := range tests {
		if (&sessionState{}).acceptEnvironment(payload) {
			t.Fatal("invalid environment payload was accepted")
		}
	}
}

func TestPTYRequestPayloadBoundsAndShape(t *testing.T) {
	state := &sessionState{}
	if !state.acceptPTY(marshalPTY("xterm-256color", 120, 36, nil)) {
		t.Fatal("valid PTY request was rejected")
	}
	if state.pty.cols != 120 || state.pty.rows != 36 {
		t.Fatalf("PTY dimensions = %dx%d", state.pty.cols, state.pty.rows)
	}
	if state.acceptPTY(marshalPTY("xterm", 80, 24, nil)) {
		t.Fatal("second PTY request was accepted")
	}
	tests := [][]byte{
		marshalPTY("", 80, 24, nil),
		marshalPTY("xterm", 0, 24, nil),
		marshalPTY("xterm", MaxCols+1, 24, nil),
		marshalPTY("xterm", 80, MaxRows+1, nil),
		marshalPTY(strings.Repeat("x", MaxTerminalBytes+1), 80, 24, nil),
		append(marshalPTY("xterm", 80, 24, nil), 0),
		{0, 0, 0, 5, 'x'},
	}
	for _, payload := range tests {
		if (&sessionState{}).acceptPTY(payload) {
			t.Fatal("invalid PTY payload was accepted")
		}
	}
}

func TestPTYResizeLinearizesTerminationClose(t *testing.T) {
	terminal, peer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	resizeEntered := make(chan struct{})
	allowResize := make(chan struct{})
	closeEntered := make(chan struct{})
	closeAdmitted := make(chan struct{})
	operations := &barrierTerminalOperations{
		resizeEntered: resizeEntered,
		allowResize:   allowResize,
		closeEntered:  closeEntered,
	}
	process := &process{
		terminal: terminal, ptyOps: operations, done: make(chan struct{}),
		terminalCloseContended: func() { close(closeAdmitted) },
	}
	state := &sessionState{
		launched: true,
		running:  process,
		pty:      &ptyState{term: "xterm-256color", cols: 80, rows: 24},
	}
	payload := marshalWindowChange(132, 43)
	resizeResult := make(chan bool, 1)
	go func() { resizeResult <- state.resizePTY(payload) }()
	waitChannel(t, resizeEntered, "resize did not reach the PTY file-operation boundary")
	if process.terminalMu.TryLock() {
		process.terminalMu.Unlock()
		t.Fatal("resize did not retain PTY lifecycle ownership across the file operation")
	}

	terminationDone := make(chan struct{})
	go func() {
		process.terminate()
		close(terminationDone)
	}()
	waitChannel(t, closeAdmitted, "PTY close did not contend on the resize-held lifecycle lock")
	assertChannelBlocked(t, closeEntered, "PTY close crossed an in-flight resize")
	assertChannelBlocked(t, terminationDone, "termination completed across an in-flight resize")

	close(allowResize)
	if !waitBool(t, resizeResult, "resize did not complete after barrier release") {
		t.Fatal("valid in-flight resize failed")
	}
	waitChannel(t, closeEntered, "PTY close did not run after resize completion")
	waitChannel(t, terminationDone, "termination did not finish after resize completion")
	if state.pty.cols != 132 || state.pty.rows != 43 {
		t.Fatalf("accepted resize dimensions = %dx%d", state.pty.cols, state.pty.rows)
	}
	operations.mu.Lock()
	resizeCalls, closeCalls := operations.resizeCalls, operations.closeCalls
	operations.mu.Unlock()
	if resizeCalls != 1 || closeCalls != 1 {
		t.Fatalf("PTY operations: resize=%d close=%d, want 1 each", resizeCalls, closeCalls)
	}
	process.terminalMu.Lock()
	lifecycle := process.terminalLifecycle
	process.terminalMu.Unlock()
	if lifecycle != terminalClosed {
		t.Fatalf("terminal lifecycle = %d, want closed", lifecycle)
	}
}

func TestPTYResizeFailsClosedAfterFinalization(t *testing.T) {
	terminal, peer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	operations := &barrierTerminalOperations{}
	runningProcess := &process{terminal: terminal, ptyOps: operations, done: make(chan struct{})}
	state := &sessionState{
		launched: true,
		running:  runningProcess,
		pty:      &ptyState{term: "xterm-256color", cols: 80, rows: 24},
	}
	if !runningProcess.beginFinalization() {
		t.Fatal("active process did not begin finalization")
	}
	if state.resizePTY(marshalWindowChange(120, 40)) {
		t.Fatal("late PTY resize was accepted after finalization")
	}
	operations.mu.Lock()
	resizeCalls := operations.resizeCalls
	operations.mu.Unlock()
	if resizeCalls != 0 {
		t.Fatalf("late resize touched terminal file operations %d times", resizeCalls)
	}
	runningProcess.closeTerminal()
	runningProcess.closeTerminal()
	operations.mu.Lock()
	closeCalls := operations.closeCalls
	operations.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("idempotent PTY close called file operation %d times", closeCalls)
	}

	other := &process{lifecycle: processFinalizing, terminalLifecycle: terminalActive}
	if other.beginFinalization() {
		t.Fatal("second finalizer acquired process lifecycle")
	}
	other.terminalMu.Lock()
	otherTerminalLifecycle := other.terminalLifecycle
	other.terminalMu.Unlock()
	if otherTerminalLifecycle != terminalActive {
		t.Fatal("failed finalization attempt changed terminal lifecycle")
	}
}

func TestWorkingDirectoryValidatedImmediatelyBeforeLaunch(t *testing.T) {
	directory := t.TempDir()
	if got, err := validateWorkingDirectory(directory); err != nil || got != directory {
		t.Fatalf("valid cwd = %q, %v", got, err)
	}
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"relative", file, filepath.Join(directory, "missing")} {
		if _, err := validateWorkingDirectory(invalid); err == nil {
			t.Fatalf("invalid cwd %q was accepted", invalid)
		}
	}
}

func TestPIDFDSelectionRejectsIdentityChangeBeforeSignal(t *testing.T) {
	scope := processScope{session: 41}
	ops := &fakeProcessSignalOps{
		identities: map[int][]processIdentity{
			101: {
				{pid: 101, processGroup: 101, session: 41, startTime: 1},
				// The numeric PID was reused after pidfd_open. This identity must
				// never receive a signal through the old pidfd.
				{pid: 101, processGroup: 101, session: 41, startTime: 2},
			},
		},
		pidfdPIDs: map[int]int{1001: 101},
	}
	matched, err := signalProcessCandidates(scope, syscall.SIGKILL, []int{101}, ops)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 || len(ops.sent) != 0 {
		t.Fatalf("reused PID was signaled: matched=%d sent=%v", matched, ops.sent)
	}
}

func TestDescendantCleanupRescansForLateProcess(t *testing.T) {
	scope := processScope{session: 52}
	ops := &fakeProcessSignalOps{
		identities: map[int][]processIdentity{
			201: {{pid: 201, processGroup: 201, session: 52, startTime: 1}},
			202: {{pid: 202, processGroup: 202, session: 52, startTime: 1}},
		},
		pidfdPIDs: map[int]int{1201: 201, 1202: 202},
		latePID:   202,
	}
	if err := clearProcessScope(scope, 52, 100*time.Millisecond, ops); err != nil {
		t.Fatal(err)
	}
	if len(ops.sent) != 2 || ops.sent[0] != 201 || ops.sent[1] != 202 {
		t.Fatalf("repeat cleanup signals = %v, want initial and late descendants", ops.sent)
	}
}

func TestProcessSignalLinearizesFinalizationAndAnchorClose(t *testing.T) {
	identity := processIdentity{pid: 301, processGroup: 301, session: 301, startTime: 7}
	base := &fakeProcessSignalOps{
		identities: map[int][]processIdentity{301: {identity, identity}},
		pidfdPIDs:  map[int]int{1301: 301},
		openPIDFDs: map[int]int{1301: 301},
	}
	sendEntered := make(chan struct{})
	allowSend := make(chan struct{})
	ops := &barrierProcessSignalOps{
		delegate: base, sendEntered: sendEntered, allowSend: allowSend,
	}
	process := &process{
		command: &exec.Cmd{Process: &os.Process{Pid: identity.pid}},
		anchor:  &processAnchor{fd: 1301, identity: identity, ops: ops},
		pgid:    identity.processGroup,
		done:    make(chan struct{}),
	}

	signalResult := make(chan error, 1)
	go func() { signalResult <- process.signal(syscall.SIGTERM) }()
	waitChannel(t, sendEntered, "signal delivery did not reach the verified pidfd")

	finalizationBegan := make(chan bool, 1)
	anchorClosed := make(chan struct{})
	go func() {
		finalizationBegan <- process.beginFinalization()
		process.anchor.Close()
		close(anchorClosed)
	}()
	assertChannelBlocked(t, finalizationBegan, "finalization crossed an in-flight verified signal")
	assertChannelBlocked(t, anchorClosed, "anchor closed before the in-flight signal completed")

	close(allowSend)
	if err := waitError(t, signalResult, "signal did not complete"); err != nil {
		t.Fatalf("signal failed: %v", err)
	}
	if began := waitBool(t, finalizationBegan, "finalization did not acquire ownership"); !began {
		t.Fatal("finalization did not transition the active process")
	}
	waitChannel(t, anchorClosed, "anchor did not close after signal completion")
	base.mu.Lock()
	closed := append([]int(nil), base.closed...)
	identityCalls := base.identityAt[identity.pid]
	base.mu.Unlock()
	if len(closed) != 1 || closed[0] != 1301 {
		t.Fatalf("closed pidfds = %v, want [1301]", closed)
	}
	if err := process.signal(syscall.SIGKILL); err == nil {
		t.Fatal("late signal was accepted after finalization began")
	}
	base.mu.Lock()
	lateIdentityCalls := base.identityAt[identity.pid]
	base.mu.Unlock()
	if lateIdentityCalls != identityCalls {
		t.Fatalf("late signal touched stale anchor identity: calls %d -> %d", identityCalls, lateIdentityCalls)
	}
}

func TestProcessWaitDoesNotReapLeaderDuringSignalScopeScan(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "read ignored")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	anchor, err := openProcessAnchor(command.Process.Pid)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	if anchor.identity.processGroup != command.Process.Pid {
		_ = command.Process.Kill()
		_ = command.Wait()
		anchor.Close()
		t.Fatal("test leader did not start in an isolated process group")
	}
	candidatesEntered := make(chan struct{})
	allowCandidates := make(chan struct{})
	var releaseCandidatesOnce sync.Once
	releaseCandidates := func() { releaseCandidatesOnce.Do(func() { close(allowCandidates) }) }
	anchor.ops = &barrierProcessSignalOps{
		delegate: anchor.ops, candidatesEntered: candidatesEntered, allowCandidates: allowCandidates,
	}
	process := &process{
		command: command, stdin: nil, anchor: anchor,
		pgid: anchor.identity.processGroup, done: make(chan struct{}),
	}
	waitResult := make(chan int, 1)
	signalResult := make(chan error, 1)
	waitConsumed := false
	defer func() {
		releaseCandidates()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
		}
		if !waitConsumed {
			select {
			case <-waitResult:
			case <-time.After(5 * time.Second):
			}
		}
		anchor.Close()
	}()
	go func() { waitResult <- process.wait() }()
	go func() { signalResult <- process.signal(syscall.SIGCONT) }()
	waitChannel(t, candidatesEntered, "signal did not reach descendant scope scan")

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waitForZombie(t, command.Process.Pid)
	if command.ProcessState != nil {
		t.Fatal("command.Wait reaped leader while signal scope scan was blocked")
	}
	anchor.mu.RLock()
	closed := anchor.closed
	anchor.mu.RUnlock()
	if closed {
		t.Fatal("leader anchor closed while signal scope scan was blocked")
	}
	assertChannelBlocked(t, waitResult, "process wait crossed the blocked signal scope scan")

	releaseCandidates()
	if err := waitError(t, signalResult, "signal did not finish after scope release"); err != nil {
		t.Fatalf("signal failed: %v", err)
	}
	status := waitInt(t, waitResult, "process wait did not finish after signal release")
	waitConsumed = true
	if status == 255 {
		t.Fatal("process cleanup failed after serialized signal/finalization")
	}
	if command.ProcessState == nil {
		t.Fatal("command was not reaped after signal scope scan completed")
	}
}

type barrierProcessSignalOps struct {
	delegate processSignalOps

	sendOnce    sync.Once
	sendEntered chan struct{}
	allowSend   <-chan struct{}

	candidatesOnce    sync.Once
	candidatesEntered chan struct{}
	allowCandidates   <-chan struct{}
}

type barrierTerminalOperations struct {
	mu sync.Mutex

	resizeEntered chan struct{}
	allowResize   <-chan struct{}
	closeEntered  chan struct{}
	resizeOnce    sync.Once
	closeOnce     sync.Once
	resizeCalls   int
	closeCalls    int
}

func (operations *barrierTerminalOperations) Resize(_ *os.File, _ *pty.Winsize) error {
	operations.mu.Lock()
	operations.resizeCalls++
	operations.mu.Unlock()
	if operations.resizeEntered != nil {
		operations.resizeOnce.Do(func() { close(operations.resizeEntered) })
	}
	if operations.allowResize != nil {
		<-operations.allowResize
	}
	return nil
}

func (operations *barrierTerminalOperations) Close(terminal *os.File) error {
	operations.mu.Lock()
	operations.closeCalls++
	operations.mu.Unlock()
	if operations.closeEntered != nil {
		operations.closeOnce.Do(func() { close(operations.closeEntered) })
	}
	return terminal.Close()
}

func (ops *barrierProcessSignalOps) Candidates() ([]int, error) {
	if ops.candidatesEntered != nil && ops.allowCandidates != nil {
		ops.candidatesOnce.Do(func() {
			close(ops.candidatesEntered)
			<-ops.allowCandidates
		})
	}
	return ops.delegate.Candidates()
}

func (ops *barrierProcessSignalOps) Open(pid int) (int, error) {
	return ops.delegate.Open(pid)
}

func (ops *barrierProcessSignalOps) Identity(pid int) (processIdentity, error) {
	return ops.delegate.Identity(pid)
}

func (ops *barrierProcessSignalOps) PID(pidfd int) (int, error) {
	return ops.delegate.PID(pidfd)
}

func (ops *barrierProcessSignalOps) Send(pidfd int, signal syscall.Signal) error {
	if ops.sendEntered != nil && ops.allowSend != nil {
		ops.sendOnce.Do(func() {
			close(ops.sendEntered)
			<-ops.allowSend
		})
	}
	return ops.delegate.Send(pidfd, signal)
}

func (ops *barrierProcessSignalOps) Close(pidfd int) error {
	return ops.delegate.Close(pidfd)
}

type fakeProcessSignalOps struct {
	mu          sync.Mutex
	nextFD      int
	identities  map[int][]processIdentity
	identityAt  map[int]int
	pidfdPIDs   map[int]int
	openPIDFDs  map[int]int
	sent        []int
	closed      []int
	latePID     int
	lateVisible bool
}

func (ops *fakeProcessSignalOps) Candidates() ([]int, error) {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	pids := make([]int, 0, len(ops.identities))
	for pid := range ops.identities {
		if pid == ops.latePID && !ops.lateVisible {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func (ops *fakeProcessSignalOps) Open(pid int) (int, error) {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if pid == ops.latePID && !ops.lateVisible {
		return 0, syscall.ESRCH
	}
	if len(ops.identities[pid]) == 0 {
		return 0, syscall.ESRCH
	}
	if ops.openPIDFDs == nil {
		ops.openPIDFDs = make(map[int]int)
	}
	for fd, anchoredPID := range ops.pidfdPIDs {
		if anchoredPID == pid {
			ops.openPIDFDs[fd] = pid
			return fd, nil
		}
	}
	ops.nextFD++
	fd := 2000 + ops.nextFD
	ops.openPIDFDs[fd] = pid
	return fd, nil
}

func (ops *fakeProcessSignalOps) Identity(pid int) (processIdentity, error) {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	values := ops.identities[pid]
	if len(values) == 0 {
		return processIdentity{}, os.ErrNotExist
	}
	if ops.identityAt == nil {
		ops.identityAt = make(map[int]int)
	}
	index := ops.identityAt[pid]
	if index >= len(values) {
		index = len(values) - 1
	}
	ops.identityAt[pid]++
	return values[index], nil
}

func (ops *fakeProcessSignalOps) PID(pidfd int) (int, error) {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	pid, ok := ops.openPIDFDs[pidfd]
	if !ok {
		return 0, syscall.ESRCH
	}
	return pid, nil
}

func (ops *fakeProcessSignalOps) Send(pidfd int, _ syscall.Signal) error {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	pid, ok := ops.openPIDFDs[pidfd]
	if !ok {
		return syscall.ESRCH
	}
	ops.sent = append(ops.sent, pid)
	delete(ops.identities, pid)
	if ops.latePID != 0 && pid != ops.latePID {
		ops.lateVisible = true
	}
	return nil
}

func (ops *fakeProcessSignalOps) Close(pidfd int) error {
	ops.mu.Lock()
	defer ops.mu.Unlock()
	ops.closed = append(ops.closed, pidfd)
	delete(ops.openPIDFDs, pidfd)
	return nil
}

func waitChannel(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func waitError(t *testing.T, channel <-chan error, message string) error {
	t.Helper()
	select {
	case err := <-channel:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		return nil
	}
}

func waitBool(t *testing.T, channel <-chan bool, message string) bool {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		return false
	}
}

func waitInt(t *testing.T, channel <-chan int, message string) int {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		return 0
	}
}

func assertChannelBlocked[T any](t *testing.T, channel <-chan T, message string) {
	t.Helper()
	select {
	case <-channel:
		t.Fatal(message)
	default:
	}
}

func waitForZombie(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err == nil {
			closingParen := bytes.LastIndexByte(contents, ')')
			if closingParen >= 0 {
				fields := strings.Fields(string(contents[closingParen+1:]))
				if len(fields) > 0 && fields[0] == "Z" {
					return
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("process did not become an unreaped zombie")
}

func marshalString(value string) []byte {
	payload := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(payload, uint32(len(value)))
	copy(payload[4:], value)
	return payload
}

func marshalPTY(term string, cols, rows uint32, modes []byte) []byte {
	payload := marshalString(term)
	dimensions := make([]byte, 16)
	binary.BigEndian.PutUint32(dimensions[0:4], cols)
	binary.BigEndian.PutUint32(dimensions[4:8], rows)
	payload = append(payload, dimensions...)
	payload = append(payload, marshalBytes(modes)...)
	return payload
}

func marshalWindowChange(cols, rows uint32) []byte {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint32(payload[0:4], cols)
	binary.BigEndian.PutUint32(payload[4:8], rows)
	return payload
}

func marshalBytes(value []byte) []byte {
	payload := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(payload, uint32(len(value)))
	copy(payload[4:], value)
	return payload
}
