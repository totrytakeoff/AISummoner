// Package sshserver provides the Embedded SSHD served directly over
// authenticated Tunnel streams. It never binds or accepts a TCP listener.
package sshserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
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
)

type processSignal uint8

const (
	processSignalInterrupt processSignal = iota + 1
	processSignalTerminate
	processSignalKill
)

// executionBackend isolates OS path, process and terminal primitives from the
// SSH wire protocol and joined session lifecycle.
type executionBackend interface {
	isAbsolutePath(string) bool
	validateWorkingDirectory(string) (string, error)
	startExec(context.Context, ssh.Channel, string, string) (sessionProcess, error)
	startShell(context.Context, ssh.Channel, *ptyState, string) (sessionProcess, error)
}

type sessionProcess interface {
	wait() int
	signalRequest(processSignal) error
	terminate()
	resizeTerminal(uint32, uint32) bool
	doneChannel() <-chan struct{}
	finish()
}

// Handler owns one SSH transport carried by one Tunnel stream.
type Handler struct {
	hostSigner ssh.Signer
	backend    executionBackend
}

func New(hostSigner ssh.Signer) (*Handler, error) {
	return newHandler(hostSigner, currentExecutionBackend())
}

func newHandler(hostSigner ssh.Signer, backend executionBackend) (*Handler, error) {
	if hostSigner == nil || hostSigner.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("Ed25519 Device host signer is required")
	}
	if backend == nil {
		return nil, errors.New("SSH execution backend is required")
	}
	return &Handler{hostSigner: hostSigner, backend: backend}, nil
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
	running  sessionProcess
	backend  executionBackend
}

type ptyState struct {
	term string
	cols uint32
	rows uint32
}

func (state *sessionState) executionBackend() executionBackend {
	if state.backend == nil {
		state.backend = currentExecutionBackend()
	}
	return state.backend
}

func (h *Handler) serveSession(ctx context.Context, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	state := &sessionState{backend: h.backend}
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
			workingDirectory, err := state.executionBackend().validateWorkingDirectory(state.cwd)
			if err != nil {
				reply(request, false)
				continue
			}
			process, err := state.executionBackend().startExec(ctx, channel, command, workingDirectory)
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
			workingDirectory, err := state.executionBackend().validateWorkingDirectory(state.cwd)
			if err != nil {
				reply(request, false)
				continue
			}
			process, err := state.executionBackend().startShell(ctx, channel, state.pty, workingDirectory)
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
		<-state.running.doneChannel()
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
	if !ok || len(rest) != 0 || value == "" || len(value) > MaxCWDBytes ||
		!state.executionBackend().isAbsolutePath(value) {
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
	if !state.launched || state.running == nil || state.pty == nil || len(payload) != 16 {
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
	var signal processSignal
	switch name {
	case "INT":
		signal = processSignalInterrupt
	case "TERM":
		signal = processSignalTerminate
	case "KILL":
		signal = processSignalKill
	default:
		return false
	}
	return state.running.signalRequest(signal) == nil
}

func parseExec(payload []byte) (string, bool) {
	command, rest, ok := consumeString(payload, MaxCommandBytes)
	return command, ok && len(rest) == 0 && command != "" && len(command) <= MaxCommandBytes && !strings.ContainsRune(command, '\x00')
}

func finishProcess(channel ssh.Channel, process sessionProcess) {
	status := process.wait()
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(status))
	_, _ = channel.SendRequest("exit-status", false, payload)
	_ = channel.CloseWrite()
	_ = channel.Close()
	process.finish()
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
