//go:build windows

package sshserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/aisummoner/aisummoner/internal/winprocess"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

type windowsExecutionBackend struct{}

func currentExecutionBackend() executionBackend { return windowsExecutionBackend{} }

func (windowsExecutionBackend) isAbsolutePath(value string) bool {
	return value != "" && len(value) <= MaxCWDBytes &&
		!strings.ContainsRune(value, '\x00') && filepath.IsAbs(value)
}

func (windowsExecutionBackend) validateWorkingDirectory(value string) (string, error) {
	if len(value) > MaxCWDBytes || strings.ContainsRune(value, '\x00') {
		return "", errors.New("working directory must be a bounded absolute Windows path")
	}
	resolved, err := winprocess.ResolveWorkingDirectory(value)
	if err != nil {
		return "", errors.New("working directory is unavailable")
	}
	return resolved, nil
}

func (windowsExecutionBackend) startExec(ctx context.Context, channel ssh.Channel, command, cwd string) (sessionProcess, error) {
	if ctx == nil || channel == nil {
		return nil, errors.New("Windows SSH exec requires a context and channel")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if command == "" || len(command) > MaxCommandBytes || strings.ContainsRune(command, '\x00') || !utf8.ValidString(command) {
		return nil, errors.New("Windows PowerShell command must be bounded valid UTF-8")
	}
	if len(cwd) > MaxCWDBytes {
		return nil, errors.New("Windows PowerShell working directory is too long")
	}
	resolvedDirectory, err := winprocess.ResolveWorkingDirectory(cwd)
	if err != nil {
		return nil, err
	}
	executable, err := winprocess.PowerShellPath()
	if err != nil {
		return nil, err
	}
	encoded, err := winprocess.EncodePowerShellCommand(winprocess.UTF8PowerShellPrefix + command)
	if err != nil {
		return nil, err
	}

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create PowerShell stdin pipe: %w", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		stdinRead.Close()
		stdinWrite.Close()
		return nil, fmt.Errorf("create PowerShell stdout pipe: %w", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdinRead.Close()
		stdinWrite.Close()
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, fmt.Errorf("create PowerShell stderr pipe: %w", err)
	}
	allFiles := []*os.File{stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite}
	started := false
	defer func() {
		if !started {
			closeWindowsFiles(allFiles...)
		}
	}()

	childHandles := []windows.Handle{
		windows.Handle(stdinRead.Fd()), windows.Handle(stdoutWrite.Fd()), windows.Handle(stderrWrite.Fd()),
	}
	for _, handle := range childHandles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, fmt.Errorf("make PowerShell pipe inheritable: %w", err)
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("allocate PowerShell process attributes: %w", err)
	}
	defer attributes.Delete()
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&childHandles[0]),
		uintptr(len(childHandles))*unsafe.Sizeof(childHandles[0]),
	); err != nil {
		return nil, fmt.Errorf("restrict PowerShell inherited handles: %w", err)
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
		return nil, err
	}
	processInformation, err := winprocess.CreateSuspendedInJob(
		job, executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded},
		resolvedDirectory, &startup, true, windows.CREATE_NO_WINDOW,
	)
	runtime.KeepAlive(childHandles)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}

	// The child owns only the three handles named in HANDLE_LIST. The parent
	// must close its child-side copies so Job termination produces pipe EOF.
	stdinRead.Close()
	stdoutWrite.Close()
	stderrWrite.Close()
	process := &windowsProcess{
		job: job, process: processInformation.Process, pid: processInformation.ProcessId,
		stdin: stdinWrite, stdout: stdoutRead, stderr: stderrRead,
		done: make(chan struct{}),
	}
	process.startExecPumps(channel)
	process.watchContext(ctx)
	started = true
	return process, nil
}

func (windowsExecutionBackend) startShell(ctx context.Context, channel ssh.Channel, request *ptyState, cwd string) (sessionProcess, error) {
	if ctx == nil || channel == nil || request == nil {
		return nil, errors.New("Windows SSH shell requires a context, channel and PTY")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validWindow(request.cols, request.rows) {
		return nil, errors.New("Windows ConPTY dimensions are invalid")
	}
	if len(cwd) > MaxCWDBytes {
		return nil, errors.New("Windows ConPTY working directory is too long")
	}
	native, err := winprocess.StartPowerShellConPTY(cwd, int16(request.cols), int16(request.rows))
	if err != nil {
		return nil, err
	}
	process := &windowsProcess{
		job: native.Job, process: native.Process, pid: native.ProcessID,
		pseudo: native.Pseudo, terminal: true,
		stdin: native.Input, stdout: native.Output,
		done: make(chan struct{}),
	}
	process.startTerminalPumps(channel)
	process.watchContext(ctx)
	return process, nil
}

type windowsProcessState uint8

const (
	windowsProcessActive windowsProcessState = iota
	windowsProcessFinalizing
	windowsProcessFinished
)

type windowsProcess struct {
	job      windows.Handle
	process  windows.Handle
	pid      uint32
	pseudo   windows.Handle
	terminal bool
	stdin    *os.File
	stdout   *os.File
	stderr   *os.File
	done     chan struct{}

	inputPump  sync.WaitGroup
	outputPump sync.WaitGroup
	stdinOnce  sync.Once
	finishOnce sync.Once

	lifecycleMu sync.Mutex
	lifecycle   windowsProcessState
	terminalMu  sync.Mutex
}

func (process *windowsProcess) startExecPumps(channel ssh.Channel) {
	process.inputPump.Add(1)
	go func() {
		defer process.inputPump.Done()
		_, _ = io.Copy(process.stdin, channel)
		process.closeStdin()
	}()
	process.outputPump.Add(2)
	go func() {
		defer process.outputPump.Done()
		defer process.stdout.Close()
		_, _ = io.Copy(channel, process.stdout)
	}()
	go func() {
		defer process.outputPump.Done()
		defer process.stderr.Close()
		_, _ = io.Copy(channel.Stderr(), process.stderr)
	}()
}

func (process *windowsProcess) startTerminalPumps(channel ssh.Channel) {
	process.inputPump.Add(1)
	go func() {
		defer process.inputPump.Done()
		_, _ = io.Copy(process.stdin, channel)
		// PTY input EOF represents an interactive client disconnect rather than
		// the benign stdin EOF accepted by a non-interactive exec.
		process.terminate()
	}()
	process.outputPump.Add(1)
	go func() {
		defer process.outputPump.Done()
		defer process.stdout.Close()
		_, _ = io.Copy(channel, process.stdout)
	}()
}

func (process *windowsProcess) watchContext(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			process.terminate()
		case <-process.done:
		}
	}()
}

func (process *windowsProcess) wait() int {
	result := winprocess.WaitForProcess(process.process)
	process.lifecycleMu.Lock()
	if process.lifecycle != windowsProcessActive {
		process.lifecycleMu.Unlock()
		return 255
	}
	process.lifecycle = windowsProcessFinalizing
	job := process.job
	process.lifecycleMu.Unlock()

	// A normally exiting root may leave Job-owned descendants holding inherited
	// stdout/stderr handles. End the complete Job before joining the drains.
	if job != 0 {
		_ = windows.TerminateJobObject(job, result.ExitCode)
	}
	process.closeStdin()
	process.closePseudoConsole()
	process.outputPump.Wait()
	if result.Err != nil {
		return 255
	}
	return int(result.ExitCode)
}

func (process *windowsProcess) signalRequest(request processSignal) error {
	switch request {
	case processSignalInterrupt:
		if !process.terminal {
			return errors.New("non-PTY Windows interrupt is unsupported")
		}
		process.lifecycleMu.Lock()
		if process.lifecycle != windowsProcessActive || process.stdin == nil {
			process.lifecycleMu.Unlock()
			return errors.New("process is unavailable")
		}
		input := process.stdin
		process.lifecycleMu.Unlock()
		if _, err := input.Write([]byte{3}); err != nil {
			return fmt.Errorf("write Windows ConPTY interrupt: %w", err)
		}
		return nil
	case processSignalTerminate:
		return process.terminateActiveJob(143)
	case processSignalKill:
		return process.terminateActiveJob(137)
	default:
		return errors.New("process signal is unsupported")
	}
}

func (process *windowsProcess) terminateActiveJob(exitCode uint32) error {
	process.lifecycleMu.Lock()
	if process.lifecycle != windowsProcessActive || process.job == 0 {
		process.lifecycleMu.Unlock()
		return errors.New("process is unavailable")
	}
	process.closeStdin()
	err := windows.TerminateJobObject(process.job, exitCode)
	process.lifecycleMu.Unlock()
	process.closePseudoConsole()
	if err != nil {
		return fmt.Errorf("terminate Windows process Job: %w", err)
	}
	return nil
}

func (process *windowsProcess) terminate() {
	process.closeStdin()
	process.lifecycleMu.Lock()
	if process.lifecycle != windowsProcessFinished && process.job != 0 {
		_ = windows.TerminateJobObject(process.job, 1)
	}
	process.lifecycleMu.Unlock()
	process.closePseudoConsole()
}

func (process *windowsProcess) resizeTerminal(cols, rows uint32) bool {
	if !process.terminal || !validWindow(cols, rows) {
		return false
	}
	process.lifecycleMu.Lock()
	if process.lifecycle != windowsProcessActive {
		process.lifecycleMu.Unlock()
		return false
	}
	process.terminalMu.Lock()
	process.lifecycleMu.Unlock()
	defer process.terminalMu.Unlock()
	if process.pseudo == 0 {
		return false
	}
	return windows.ResizePseudoConsole(
		process.pseudo, windows.Coord{X: int16(cols), Y: int16(rows)},
	) == nil
}

func (process *windowsProcess) doneChannel() <-chan struct{} { return process.done }

func (process *windowsProcess) finish() {
	process.finishOnce.Do(func() {
		process.terminate()
		process.inputPump.Wait()
		process.outputPump.Wait()

		process.lifecycleMu.Lock()
		processHandle, job := process.process, process.job
		process.process, process.job = 0, 0
		process.lifecycle = windowsProcessFinished
		process.lifecycleMu.Unlock()
		if processHandle != 0 {
			windows.CloseHandle(processHandle)
		}
		if job != 0 {
			windows.CloseHandle(job)
		}
		close(process.done)
	})
}

func (process *windowsProcess) closePseudoConsole() {
	process.terminalMu.Lock()
	defer process.terminalMu.Unlock()
	if process.pseudo != 0 {
		windows.ClosePseudoConsole(process.pseudo)
		process.pseudo = 0
	}
}

func (process *windowsProcess) closeStdin() {
	process.stdinOnce.Do(func() {
		if process.stdin != nil {
			_ = process.stdin.Close()
		}
	})
}

func closeWindowsFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
