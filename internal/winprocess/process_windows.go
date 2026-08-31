//go:build windows

// Package winprocess owns the bounded Windows process primitives shared by the
// production Remote SSH backend and the native compatibility probes.
package winprocess

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UTF8PowerShellPrefix freezes the Windows PowerShell 5.1 stream encoding used
// by both non-interactive exec and the production ConPTY backend.
const UTF8PowerShellPrefix = `$utf8 = [System.Text.UTF8Encoding]::new($false); ` +
	`[Console]::InputEncoding = $utf8; [Console]::OutputEncoding = $utf8; $OutputEncoding = $utf8; `

// EncodePowerShellCommand returns the UTF-16LE Base64 payload required by
// powershell.exe -EncodedCommand. Invalid UTF-8 is rejected rather than being
// silently replaced during rune conversion.
func EncodePowerShellCommand(script string) (string, error) {
	if script == "" {
		return "", errors.New("PowerShell command is empty")
	}
	if !utf8.ValidString(script) {
		return "", errors.New("PowerShell command is not valid UTF-8")
	}
	codeUnits := utf16.Encode([]rune(script))
	encoded := make([]byte, len(codeUnits)*2)
	for index, value := range codeUnits {
		encoded[index*2] = byte(value)
		encoded[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

// PowerShellPath resolves the inbox Windows PowerShell 5.1 executable without
// consulting PATH or selecting a user-installed shell.
func PowerShellPath() (string, error) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("resolve Windows system directory: %w", err)
	}
	path := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("locate Windows PowerShell: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Windows PowerShell executable is not a regular file")
	}
	return path, nil
}

// ResolveWorkingDirectory maps an omitted cwd to the current user's Profile
// Known Folder and otherwise accepts only an existing clean absolute directory.
func ResolveWorkingDirectory(value string) (string, error) {
	if value == "" {
		profile, err := windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return "", fmt.Errorf("resolve user profile: %w", err)
		}
		value = profile
	}
	if strings.ContainsRune(value, '\x00') || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", errors.New("PowerShell working directory must be a clean absolute path")
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("inspect PowerShell working directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("PowerShell working directory is not a directory")
	}
	return value, nil
}

// NewKillOnCloseJob creates the per-SSH-session Job Object. Closing the Job is
// a fail-closed cleanup path for every process still associated with it.
func NewKillOnCloseJob() (windows.Handle, error) {
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

// CreateSuspendedInJob creates a process suspended, assigns it to job, and
// resumes it only after successful assignment. startup and its attribute list
// must remain alive for the duration of this call.
func CreateSuspendedInJob(job windows.Handle, executable string, arguments []string,
	workingDirectory string, startup *windows.StartupInfoEx, inheritHandles bool,
	additionalFlags uint32,
) (windows.ProcessInformation, error) {
	if job == 0 || startup == nil {
		return windows.ProcessInformation{}, errors.New("Job Object and startup information are required")
	}
	if executable == "" || strings.ContainsRune(executable, '\x00') {
		return windows.ProcessInformation{}, errors.New("valid process executable is required")
	}
	resolvedDirectory, err := ResolveWorkingDirectory(workingDirectory)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	executable16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	commandLine := windows.ComposeCommandLine(append([]string{executable}, arguments...))
	commandLine16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	workingDirectory16, err := windows.UTF16PtrFromString(resolvedDirectory)
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
		cleanupErr := terminateAndWait(process.Process)
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
		return windows.ProcessInformation{}, errors.Join(cause, cleanupErr)
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

func terminateAndWait(process windows.Handle) error {
	if process == 0 {
		return nil
	}
	terminateErr := windows.TerminateProcess(process, 1)
	event, waitErr := windows.WaitForSingleObject(process, 5_000)
	if waitErr == nil && event != windows.WAIT_OBJECT_0 {
		waitErr = fmt.Errorf("unexpected terminated process wait result: %d", event)
	}
	return errors.Join(terminateErr, waitErr)
}

// WaitResult is the exact native exit result for one owned process handle.
type WaitResult struct {
	ExitCode uint32
	Err      error
}

// WaitForProcess waits without closing the caller-owned process handle.
func WaitForProcess(process windows.Handle) WaitResult {
	if process == 0 {
		return WaitResult{Err: errors.New("process handle is unavailable")}
	}
	event, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return WaitResult{Err: fmt.Errorf("wait for process: %w", err)}
	}
	if event != windows.WAIT_OBJECT_0 {
		return WaitResult{Err: fmt.Errorf("unexpected process wait result: %d", event)}
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
		return WaitResult{Err: fmt.Errorf("read process exit code: %w", err)}
	}
	return WaitResult{ExitCode: exitCode}
}
