//go:build windows

package windowsprobe

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	DefaultQtPipeName = `LOCAL\AISummoner.Remote.v1`
	pipePrefix        = `\\.\pipe\`
)

var (
	advapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClient = advapi32.NewProc("ImpersonateNamedPipeClient")
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

	// Pipe impersonation attaches to the calling OS thread. Lock the goroutine
	// until RevertToSelf has restored the daemon token.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, callErr := procImpersonateNamedPipeClient.Call(handleSource.Fd())
	if result == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = windows.ERROR_ACCESS_DENIED
		}
		return "", fmt.Errorf("impersonate named-pipe peer: %w", callErr)
	}
	defer windows.RevertToSelf()

	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		return "", fmt.Errorf("open named-pipe peer token: %w", err)
	}
	defer token.Close()
	return tokenLogonSID(token)
}
