//go:build windows

package windowsprobe

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"

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
	workingDirectory, err := resolveWorkingDirectory(workingDirectory)
	if err != nil {
		return ProcessResult{}, err
	}
	executable, err := windowsPowerShellPath()
	if err != nil {
		return ProcessResult{}, err
	}
	encoded := encodePowerShellCommand(utf8PowerShellPrefix + script)

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
	job, err := newKillOnCloseJob()
	if err != nil {
		return ProcessResult{}, err
	}
	defer windows.CloseHandle(job)
	process, err := createSuspendedInJob(
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

	waited := make(chan processWaitResult, 1)
	go func() { waited <- waitForProcess(process.Process) }()
	cancelled := false
	var waitResult processWaitResult
	select {
	case waitResult = <-waited:
	case <-ctx.Done():
		cancelled = true
		_ = windows.TerminateJobObject(job, 1)
		waitResult = <-waited
	}
	// A root shell can exit while a descendant still owns one of the output
	// handles. Terminate the complete session Job before joining the drains.
	_ = windows.TerminateJobObject(job, waitResult.exitCode)
	drainError := errors.Join(<-drains, <-drains)
	result := ProcessResult{
		PID: process.ProcessId, ExitCode: waitResult.exitCode,
		Stdout: stdoutCapture.Bytes(), Stderr: stderrCapture.Bytes(),
		StdoutTruncated: stdoutCapture.Truncated(), StderrTruncated: stderrCapture.Truncated(),
		Cancelled: cancelled,
	}
	if waitResult.err != nil {
		return result, waitResult.err
	}
	if drainError != nil {
		return result, fmt.Errorf("drain PowerShell output: %w", drainError)
	}
	if cancelled {
		return result, ctx.Err()
	}
	return result, nil
}

const utf8PowerShellPrefix = `$utf8 = [System.Text.UTF8Encoding]::new($false); ` +
	`[Console]::InputEncoding = $utf8; [Console]::OutputEncoding = $utf8; $OutputEncoding = $utf8; `

func encodePowerShellCommand(script string) string {
	codeUnits := utf16.Encode([]rune(script))
	encoded := make([]byte, len(codeUnits)*2)
	for index, value := range codeUnits {
		encoded[index*2] = byte(value)
		encoded[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func windowsPowerShellPath() (string, error) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("resolve Windows system directory: %w", err)
	}
	path := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("locate Windows PowerShell: %w", err)
	}
	return path, nil
}

func resolveWorkingDirectory(value string) (string, error) {
	if value == "" {
		profile, err := windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return "", fmt.Errorf("resolve user profile: %w", err)
		}
		value = profile
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", errors.New("PowerShell working directory must be a clean absolute path")
	}
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("inspect PowerShell working directory: %w", err)
	}
	return value, nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create session Job Object: %w", err)
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("set kill-on-close Job limit: %w", err)
	}
	return job, nil
}

func createSuspendedInJob(job windows.Handle, executable string, arguments []string,
	workingDirectory string, startup *windows.StartupInfoEx, inheritHandles bool,
	additionalFlags uint32,
) (windows.ProcessInformation, error) {
	executable16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	commandLine := windows.ComposeCommandLine(append([]string{executable}, arguments...))
	commandLine16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	workingDirectory16, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	var process windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT)
	flags |= additionalFlags
	if err := windows.CreateProcess(
		executable16, commandLine16, nil, nil, inheritHandles, flags, nil,
		workingDirectory16, &startup.StartupInfo, &process,
	); err != nil {
		return windows.ProcessInformation{}, fmt.Errorf("create suspended process: %w", err)
	}
	fail := func(cause error) (windows.ProcessInformation, error) {
		_ = windows.TerminateProcess(process.Process, 1)
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
		return windows.ProcessInformation{}, cause
	}
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		return fail(fmt.Errorf("assign suspended process to Job Object: %w", err))
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return fail(fmt.Errorf("resume assigned process: %w", err))
	}
	windows.CloseHandle(process.Thread)
	process.Thread = 0
	return process, nil
}

type processWaitResult struct {
	exitCode uint32
	err      error
}

func waitForProcess(process windows.Handle) processWaitResult {
	event, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return processWaitResult{err: fmt.Errorf("wait for process: %w", err)}
	}
	if event != windows.WAIT_OBJECT_0 {
		return processWaitResult{err: fmt.Errorf("unexpected process wait result: %d", event)}
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
		return processWaitResult{err: fmt.Errorf("read process exit code: %w", err)}
	}
	return processWaitResult{exitCode: exitCode}
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
	processResult processWaitResult
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
	workingDirectory, err := resolveWorkingDirectory(workingDirectory)
	if err != nil {
		return nil, err
	}
	executable, err := windowsPowerShellPath()
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
	job, err := newKillOnCloseJob()
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return nil, err
	}
	encoded := encodePowerShellCommand(utf8PowerShellPrefix)
	process, err := createSuspendedInJob(
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
		result := waitForProcess(session.process)
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
		return result.exitCode, result.err
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
