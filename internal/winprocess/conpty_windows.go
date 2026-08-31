//go:build windows

package winprocess

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConPTYProcess contains the caller-owned native objects for one interactive
// Windows PowerShell session. Callers must serialize pseudoconsole resize with
// close, terminate/wait the Job, join their I/O workers, and close every field.
type ConPTYProcess struct {
	Job       windows.Handle
	Process   windows.Handle
	ProcessID uint32
	Pseudo    windows.Handle
	Input     *os.File
	Output    *os.File
}

// StartPowerShellConPTY creates the fixed interactive Windows PowerShell 5.1
// backend. The shell starts suspended and cannot run until it belongs to its
// kill-on-close Job Object.
func StartPowerShellConPTY(workingDirectory string, columns, rows int16) (ConPTYProcess, error) {
	if columns < 1 || rows < 1 {
		return ConPTYProcess{}, errors.New("ConPTY dimensions must be positive")
	}
	resolvedDirectory, err := ResolveWorkingDirectory(workingDirectory)
	if err != nil {
		return ConPTYProcess{}, err
	}
	executable, err := PowerShellPath()
	if err != nil {
		return ConPTYProcess{}, err
	}
	encoded, err := EncodePowerShellCommand(UTF8PowerShellPrefix)
	if err != nil {
		return ConPTYProcess{}, err
	}

	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return ConPTYProcess{}, fmt.Errorf("create ConPTY input pipe: %w", err)
	}
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		inputRead.Close()
		inputWrite.Close()
		return ConPTYProcess{}, fmt.Errorf("create ConPTY output pipe: %w", err)
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
		return ConPTYProcess{}, fmt.Errorf("create ConPTY: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return ConPTYProcess{}, fmt.Errorf("allocate ConPTY attributes: %w", err)
	}
	defer attributes.Delete()
	pseudoValue := *(*unsafe.Pointer)(unsafe.Pointer(&pseudo))
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		pseudoValue, unsafe.Sizeof(pseudo),
	); err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return ConPTYProcess{}, fmt.Errorf("attach ConPTY attribute: %w", err)
	}
	startup := windows.StartupInfoEx{
		// A console-attached launcher or CI runner must not donate its own
		// standard streams and let PowerShell bypass the pseudoconsole.
		StartupInfo: windows.StartupInfo{
			Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags: windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: attributes.List(),
	}
	job, err := NewKillOnCloseJob()
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return ConPTYProcess{}, err
	}
	process, err := CreateSuspendedInJob(
		job, executable,
		[]string{"-NoLogo", "-NoProfile", "-NoExit", "-EncodedCommand", encoded},
		resolvedDirectory, &startup, false, 0,
	)
	if err != nil {
		windows.CloseHandle(job)
		windows.ClosePseudoConsole(pseudo)
		cleanupPipes()
		return ConPTYProcess{}, err
	}

	// CreatePseudoConsole owns duplicate references to its two host-side pipe
	// handles. Only the parent-facing input/output ends remain with the caller.
	inputRead.Close()
	outputWrite.Close()
	return ConPTYProcess{
		Job: job, Process: process.Process, ProcessID: process.ProcessId,
		Pseudo: pseudo, Input: inputWrite, Output: outputRead,
	}, nil
}
