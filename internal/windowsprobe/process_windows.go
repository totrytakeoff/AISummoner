//go:build windows

package windowsprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/aisummoner/aisummoner/internal/winprocess"
	"golang.org/x/sys/windows"
)

const maxCapturedStreamBytes = 4 * 1024 * 1024

// ProcessResult is the bounded evidence returned by the Windows non-PTY
// contract runner. Output is drained even after the capture limit is reached.
type ProcessResult struct {
	PID             uint32
	ExitCode        uint32
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	Cancelled       bool
}

// RunPowerShell starts Windows PowerShell suspended, assigns it to a
// kill-on-close Job Object, and only then resumes the primary thread.
func RunPowerShell(ctx context.Context, workingDirectory, script string) (ProcessResult, error) {
	if ctx == nil {
		return ProcessResult{}, errors.New("PowerShell context is required")
	}
	if len(script) == 0 || len(script) > 64*1024 {
		return ProcessResult{}, errors.New("PowerShell script must be between 1 and 65536 bytes")
	}
	workingDirectory, err := winprocess.ResolveWorkingDirectory(workingDirectory)
	if err != nil {
		return ProcessResult{}, err
	}
	executable, err := winprocess.PowerShellPath()
	if err != nil {
		return ProcessResult{}, err
	}
	encoded, err := winprocess.EncodePowerShellCommand(winprocess.UTF8PowerShellPrefix + script)
	if err != nil {
		return ProcessResult{}, err
	}

	stdin, err := os.OpenFile("NUL", os.O_RDONLY, 0)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("open NUL input: %w", err)
	}
	defer stdin.Close()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stdout pipe: %w", err)
	}
	defer stdoutRead.Close()
	defer stdoutWrite.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stderr pipe: %w", err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()

	childHandles := []windows.Handle{
		windows.Handle(stdin.Fd()), windows.Handle(stdoutWrite.Fd()), windows.Handle(stderrWrite.Fd()),
	}
	for _, handle := range childHandles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return ProcessResult{}, fmt.Errorf("make child pipe inheritable: %w", err)
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("allocate process attributes: %w", err)
	}
	defer attributes.Delete()
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&childHandles[0]),
		uintptr(len(childHandles))*unsafe.Sizeof(childHandles[0]),
	); err != nil {
		return ProcessResult{}, fmt.Errorf("restrict inherited handles: %w", err)
	}
	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:       uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:    windows.STARTF_USESTDHANDLES,
			StdInput: childHandles[0], StdOutput: childHandles[1], StdErr: childHandles[2],
		},
		ProcThreadAttributeList: attributes.List(),
	}
	job, err := winprocess.NewKillOnCloseJob()
	if err != nil {
		return ProcessResult{}, err
	}
	defer windows.CloseHandle(job)
	process, err := winprocess.CreateSuspendedInJob(
		job, executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded},
		workingDirectory, &startup, true, windows.CREATE_NO_WINDOW,
	)
	if err != nil {
		return ProcessResult{}, err
	}
	defer windows.CloseHandle(process.Process)

	// The child owns duplicate references selected by HANDLE_LIST. Closing the
	// parent writers is required for the drain workers to observe EOF.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	stdoutCapture := &boundedCapture{maximum: maxCapturedStreamBytes}
	stderrCapture := &boundedCapture{maximum: maxCapturedStreamBytes}
	drains := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(stdoutCapture, stdoutRead)
		drains <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(stderrCapture, stderrRead)
		drains <- copyErr
	}()

	waited := make(chan winprocess.WaitResult, 1)
	go func() { waited <- winprocess.WaitForProcess(process.Process) }()
	cancelled := false
	var waitResult winprocess.WaitResult
	select {
	case waitResult = <-waited:
	case <-ctx.Done():
		cancelled = true
		_ = windows.TerminateJobObject(job, 1)
		waitResult = <-waited
	}
	// A root shell can exit while a descendant still owns one of the output
	// handles. Terminate the complete session Job before joining the drains.
	_ = windows.TerminateJobObject(job, waitResult.ExitCode)
	drainError := errors.Join(<-drains, <-drains)
	result := ProcessResult{
		PID: process.ProcessId, ExitCode: waitResult.ExitCode,
		Stdout: stdoutCapture.Bytes(), Stderr: stderrCapture.Bytes(),
		StdoutTruncated: stdoutCapture.Truncated(), StderrTruncated: stderrCapture.Truncated(),
		Cancelled: cancelled,
	}
	if waitResult.Err != nil {
		return result, waitResult.Err
	}
	if drainError != nil {
		return result, fmt.Errorf("drain PowerShell output: %w", drainError)
	}
	if cancelled {
		return result, ctx.Err()
	}
	return result, nil
}

type boundedCapture struct {
	maximum   int
	contents  bytes.Buffer
	truncated atomic.Bool
}

func (capture *boundedCapture) Write(contents []byte) (int, error) {
	remaining := capture.maximum - capture.contents.Len()
	if remaining > 0 {
		kept := len(contents)
		if kept > remaining {
			kept = remaining
		}
		_, _ = capture.contents.Write(contents[:kept])
	}
	if len(contents) > remaining {
		capture.truncated.Store(true)
	}
	return len(contents), nil
}

func (capture *boundedCapture) Bytes() []byte {
	return append([]byte(nil), capture.contents.Bytes()...)
}

func (capture *boundedCapture) Truncated() bool { return capture.truncated.Load() }

// ConPTYSession owns one interactive Windows PowerShell, its pseudoconsole,
// pipes, and Job Object. Output is always serviced by a dedicated worker.
type ConPTYSession struct {
	job         windows.Handle
	process     windows.Handle
	pid         uint32
	pseudo      windows.Handle
	input       *os.File
	output      *os.File
	requests    chan conPTYWrite
	chunks      chan []byte
	closing     chan struct{}
	drainDone   chan struct{}
	writeDone   chan struct{}
	processDone chan struct{}

	processMu     sync.Mutex
	processResult winprocess.WaitResult
	nativeMu      sync.Mutex
	closeOnce     sync.Once
	closeError    error
	closed        atomic.Bool
}

type conPTYWrite struct {
	contents []byte
	finished chan error
}

// StartConPTY starts a fixed Windows PowerShell 5.1 interactive shell.
func StartConPTY(workingDirectory string, columns, rows int16) (*ConPTYSession, error) {
	if columns < 1 || rows < 1 {
		return nil, errors.New("ConPTY dimensions must be positive")
	}
	workingDirectory, err := winprocess.ResolveWorkingDirectory(workingDirectory)
	if err != nil {
		return nil, err
	}
	executable, err := winprocess.PowerShellPath()
	if err != nil {
		return nil, err
	}
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create ConPTY input pipe: %w", err)
	}
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputRead.Close()
		inputWrite.Close()
		return nil, fmt.Errorf("create ConPTY output pipe: %w", err)
	}
	cleanupPipes := func() {
		inputRead.Close()
		inputWrite.Close()
		outputRead.Close()
		outputWrite.Close()
	}
	var pseudo windows.Handle
	if err := windows.CreatePseudoConsole(
		windows.Coord{X: columns, Y: rows}, windows.Handle(inputRead.Fd()),
		windows.Handle(outputWrite.Fd()), 0, &pseudo,
	); err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("create ConPTY: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, fmt.Errorf("allocate ConPTY attributes: %w", err)
	}
	defer attributes.Delete()
	pseudoValue := *(*unsafe.Pointer)(unsafe.Pointer(&pseudo))
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		pseudoValue, unsafe.Sizeof(pseudo),
	); err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, fmt.Errorf("attach ConPTY attribute: %w", err)
	}
	startup := windows.StartupInfoEx{
		// Explicit null standard handles prevent a console-attached parent (for
		// example a CI runner) from donating its own console streams. The
		// pseudoconsole then remains the child's only console I/O path.
		StartupInfo: windows.StartupInfo{
			Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags: windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: attributes.List(),
	}
	job, err := winprocess.NewKillOnCloseJob()
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, err
	}
	encoded, err := winprocess.EncodePowerShellCommand(winprocess.UTF8PowerShellPrefix)
	if err != nil {
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, err
	}
	process, err := winprocess.CreateSuspendedInJob(
		job, executable,
		[]string{"-NoLogo", "-NoProfile", "-NoExit", "-EncodedCommand", encoded},
		workingDirectory, &startup, false, 0,
	)
	if err != nil {
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, err
	}
	// The pseudoconsole keeps its own references after child creation.
	inputRead.Close()
	outputWrite.Close()
	session := &ConPTYSession{
		job: job, process: process.Process, pid: process.ProcessId, pseudo: pseudo,
		input: inputWrite, output: outputRead,
		requests: make(chan conPTYWrite, 8), chunks: make(chan []byte, 32),
		closing: make(chan struct{}), drainDone: make(chan struct{}),
		writeDone: make(chan struct{}), processDone: make(chan struct{}),
	}
	go session.drainOutput()
	go session.writeInput()
	go func() {
		result := winprocess.WaitForProcess(session.process)
		session.processMu.Lock()
		session.processResult = result
		session.processMu.Unlock()
		close(session.processDone)
	}()
	return session, nil
}

func (session *ConPTYSession) PID() uint32 { return session.pid }

// Output returns VT/UTF-8 chunks. A caller should consume them continuously;
// teardown switches the worker to drain-and-discard so Close cannot deadlock.
func (session *ConPTYSession) Output() <-chan []byte { return session.chunks }

func (session *ConPTYSession) Write(ctx context.Context, contents []byte) error {
	if ctx == nil || len(contents) == 0 {
		return errors.New("ConPTY input is empty")
	}
	if session.closed.Load() {
		return netClosedError()
	}
	request := conPTYWrite{contents: append([]byte(nil), contents...), finished: make(chan error, 1)}
	select {
	case session.requests <- request:
	case <-session.closing:
		return netClosedError()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.finished:
		return err
	case <-session.closing:
		return netClosedError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (session *ConPTYSession) Resize(columns, rows int16) error {
	if columns < 1 || rows < 1 {
		return errors.New("ConPTY dimensions must be positive")
	}
	session.nativeMu.Lock()
	defer session.nativeMu.Unlock()
	if session.closed.Load() || session.pseudo == 0 {
		return netClosedError()
	}
	if err := windows.ResizePseudoConsole(session.pseudo, windows.Coord{X: columns, Y: rows}); err != nil {
		return fmt.Errorf("resize ConPTY: %w", err)
	}
	return nil
}

func (session *ConPTYSession) Wait(ctx context.Context) (uint32, error) {
	if ctx == nil {
		return 0, errors.New("ConPTY wait context is required")
	}
	select {
	case <-session.processDone:
		session.processMu.Lock()
		result := session.processResult
		session.processMu.Unlock()
		return result.ExitCode, result.Err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (session *ConPTYSession) Close() error {
	session.closeOnce.Do(func() {
		session.closed.Store(true)
		close(session.closing)
		_ = session.input.Close()
		_ = windows.TerminateJobObject(session.job, 1)

		session.nativeMu.Lock()
		if session.pseudo != 0 {
			windows.ClosePseudoConsole(session.pseudo)
			session.pseudo = 0
		}
		session.nativeMu.Unlock()

		<-session.processDone
		_ = session.output.Close()
		<-session.drainDone
		<-session.writeDone
		windows.CloseHandle(session.process)
		windows.CloseHandle(session.job)
	})
	return session.closeError
}

func (session *ConPTYSession) drainOutput() {
	defer close(session.drainDone)
	defer close(session.chunks)
	buffer := make([]byte, 32*1024)
	for {
		count, err := session.output.Read(buffer)
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			select {
			case session.chunks <- chunk:
			case <-session.closing:
				// Continue reading and discard final ConPTY frames during teardown.
			}
		}
		if err != nil {
			return
		}
	}
}

func (session *ConPTYSession) writeInput() {
	defer close(session.writeDone)
	for {
		select {
		case request := <-session.requests:
			_, err := session.input.Write(request.contents)
			request.finished <- err
		case <-session.closing:
			return
		}
	}
}

func netClosedError() error { return errors.New("ConPTY session is closed") }
