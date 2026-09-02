package sshserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

type backendStub struct {
	absolute func(string) bool
	validate func(string) (string, error)
}

func (backendStub backendStub) isAbsolutePath(value string) bool {
	return backendStub.absolute(value)
}

func (backendStub backendStub) validateWorkingDirectory(value string) (string, error) {
	if backendStub.validate != nil {
		return backendStub.validate(value)
	}
	return value, nil
}

func (backendStub) startExec(context.Context, ssh.Channel, string, string) (sessionProcess, error) {
	return nil, errors.New("not used")
}

func (backendStub) startShell(context.Context, ssh.Channel, *ptyState, string) (sessionProcess, error) {
	return nil, errors.New("not used")
}

type sessionProcessStub struct {
	signals []processSignal
	done    chan struct{}
	resize  bool
}

func (*sessionProcessStub) wait() int { return 0 }
func (process *sessionProcessStub) signalRequest(signal processSignal) error {
	process.signals = append(process.signals, signal)
	return nil
}
func (*sessionProcessStub) terminate()                                 {}
func (process *sessionProcessStub) resizeTerminal(uint32, uint32) bool { return process.resize }
func (process *sessionProcessStub) doneChannel() <-chan struct{}       { return process.done }
func (*sessionProcessStub) finish()                                    {}

func TestCommonSessionDelegatesPathSyntaxToExecutionBackend(t *testing.T) {
	state := &sessionState{backend: backendStub{absolute: func(value string) bool {
		return strings.HasPrefix(value, `C:\`)
	}}}
	if !state.acceptEnvironment(append(marshalCommonString(CWDEnvironment), marshalCommonString(`C:\work`)...)) {
		t.Fatal("backend-approved Windows path was rejected by common SSH code")
	}
	other := &sessionState{backend: state.backend}
	if other.acceptEnvironment(append(marshalCommonString(CWDEnvironment), marshalCommonString("/tmp")...)) {
		t.Fatal("common SSH code bypassed backend path syntax")
	}
	unavailable := &sessionState{backend: backendStub{
		absolute: func(string) bool { return true },
		validate: func(string) (string, error) { return "", errors.New("missing") },
	}}
	if unavailable.acceptEnvironment(append(marshalCommonString(CWDEnvironment), marshalCommonString(`C:\missing`)...)) {
		t.Fatal("common SSH code accepted a backend-rejected working directory")
	}
}

func TestCommonSessionMapsOnlySupportedSSHSignals(t *testing.T) {
	process := &sessionProcessStub{done: make(chan struct{})}
	state := &sessionState{launched: true, running: process}
	for _, name := range []string{"INT", "TERM", "KILL"} {
		if !state.signal(marshalCommonString(name)) {
			t.Fatalf("supported SSH signal %s was rejected", name)
		}
	}
	want := []processSignal{processSignalInterrupt, processSignalTerminate, processSignalKill}
	if len(process.signals) != len(want) {
		t.Fatalf("signals = %v", process.signals)
	}
	for index := range want {
		if process.signals[index] != want[index] {
			t.Fatalf("signals = %v, want %v", process.signals, want)
		}
	}
	if state.signal(marshalCommonString("USR1")) {
		t.Fatal("unsupported SSH signal was delegated to the platform backend")
	}
}

func marshalCommonString(value string) []byte {
	payload := make([]byte, 4+len(value))
	payload[0] = byte(len(value) >> 24)
	payload[1] = byte(len(value) >> 16)
	payload[2] = byte(len(value) >> 8)
	payload[3] = byte(len(value))
	copy(payload[4:], value)
	return payload
}
