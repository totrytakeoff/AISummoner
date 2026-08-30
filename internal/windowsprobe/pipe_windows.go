//go:build windows

package windowsprobe

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	DefaultQtPipeName = `LOCAL\AISummoner.Remote.v1`
	pipePrefix        = `\\.\pipe\`
)

// FullPipePath converts the Qt QLocalSocket server name to the native path used
// by the Go listener. Only the frozen login-session namespace is accepted.
func FullPipePath(qtServerName string) (string, error) {
	if len(qtServerName) < len(`LOCAL\A`) || len(qtServerName) > 200 ||
		!strings.HasPrefix(qtServerName, `LOCAL\`) || strings.ContainsRune(qtServerName, '/') ||
		strings.ContainsRune(qtServerName, '\x00') || strings.ContainsAny(qtServerName, "\r\n") {
		return "", errors.New("invalid AISummoner pipe name")
	}
	for _, character := range qtServerName[len(`LOCAL\`):] {
		if !(character == '.' || character == '-' || character == '_' ||
			character >= '0' && character <= '9' ||
			character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z') {
			return "", errors.New("invalid AISummoner pipe name")
		}
	}
	return pipePrefix + qtServerName, nil
}

// ListenVerifiedPipe creates an exclusive, remote-client-rejecting named pipe
// whose DACL and accepted peer are both bound to the current logon SID.
func ListenVerifiedPipe(qtServerName string) (net.Listener, error) {
	path, err := FullPipePath(qtServerName)
	if err != nil {
		return nil, err
	}
	logonSID, err := currentLogonSID()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + logonSID + ")",
		InputBufferSize:    64 * 1024, OutputBufferSize: 64 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on protected named pipe: %w", err)
	}
	return &verifiedPipeListener{Listener: listener, expectedLogonSID: logonSID}, nil
}

type verifiedPipeListener struct {
	net.Listener
	expectedLogonSID string
	rejected         atomic.Uint64
}

func (listener *verifiedPipeListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peerSID, err := namedPipePeerLogonSID(connection)
		if err == nil && peerSID == listener.expectedLogonSID {
			return connection, nil
		}
		listener.rejected.Add(1)
		_ = connection.Close()
	}
}

func namedPipePeerLogonSID(connection net.Conn) (string, error) {
	handleSource, ok := connection.(interface{ Fd() uintptr })
	if !ok || handleSource.Fd() == 0 {
		return "", errors.New("named pipe carrier does not expose its native handle")
	}
	pipe := windows.Handle(handleSource.Fd())
	var processID uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &processID); err != nil {
		return "", fmt.Errorf("read named-pipe peer process: %w", err)
	}
	if processID == 0 {
		return "", errors.New("named-pipe peer process is invalid")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return "", fmt.Errorf("open named-pipe peer process: %w", err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("open named-pipe peer process token: %w", err)
	}
	defer token.Close()
	logonSID, err := tokenLogonSID(token)
	if err != nil {
		return "", err
	}
	// Re-query the exact connected instance after opening the process token.
	// A disconnect or changed peer fails closed before any protocol byte is read.
	var confirmedProcessID uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &confirmedProcessID); err != nil || confirmedProcessID != processID {
		return "", errors.New("named-pipe peer changed during token verification")
	}
	return logonSID, nil
}
