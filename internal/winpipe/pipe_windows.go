//go:build windows

// Package winpipe owns the frozen Windows named-pipe carrier and exact peer
// authentication contract used by the Remote Core and Qt interoperability
// probe.
package winpipe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"github.com/aisummoner/aisummoner/internal/winsecurity"
	"golang.org/x/sys/windows"
)

const (
	DefaultName = `LOCAL\AISummoner.Remote.v1`
	pipePrefix  = `\\.\pipe\`
)

type Listener struct {
	net.Listener
	expectedLogonSID string
}

func ValidateName(name string) error {
	if len(name) < len(`LOCAL\A`) || len(name) > 200 ||
		!strings.HasPrefix(name, `LOCAL\`) || strings.ContainsRune(name, '/') ||
		strings.ContainsRune(name, '\x00') || strings.ContainsAny(name, "\r\n") {
		return errors.New("invalid AISummoner pipe name")
	}
	for _, character := range name[len(`LOCAL\`):] {
		if !(character == '.' || character == '-' || character == '_' ||
			character >= '0' && character <= '9' ||
			character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z') {
			return errors.New("invalid AISummoner pipe name")
		}
	}
	return nil
}

func FullPath(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return pipePrefix + name, nil
}

func Dial(ctx context.Context, name string) (net.Conn, error) {
	if ctx == nil {
		return nil, errors.New("named-pipe context is required")
	}
	path, err := FullPath(name)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, path)
}

// Listen creates an exclusive first instance. go-winio v0.6.2 creates every
// server instance with FILE_PIPE_REJECT_REMOTE_CLIENTS; the protected DACL and
// exact peer-token check additionally bind access to the current logon SID.
func Listen(name string) (*Listener, error) {
	path, err := FullPath(name)
	if err != nil {
		return nil, err
	}
	logonSID, err := winsecurity.CurrentLogonSID()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + logonSID + ")",
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on protected named pipe: %w", err)
	}
	return &Listener{Listener: listener, expectedLogonSID: logonSID}, nil
}

// Authenticate checks the exact accepted pipe instance before the caller
// reads protocol bytes.
func (listener *Listener) Authenticate(connection net.Conn) error {
	if listener == nil || listener.expectedLogonSID == "" || connection == nil {
		return errors.New("named-pipe peer authentication is unavailable")
	}
	peerSID, err := peerLogonSID(connection)
	if err != nil {
		return err
	}
	if peerSID != listener.expectedLogonSID {
		return errors.New("named-pipe peer logon does not match")
	}
	return nil
}

func peerLogonSID(connection net.Conn) (string, error) {
	handleSource, ok := connection.(interface{ Fd() uintptr })
	if !ok || handleSource.Fd() == 0 {
		return "", errors.New("named-pipe carrier does not expose its native handle")
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
	logonSID, err := winsecurity.TokenLogonSID(token)
	if err != nil {
		return "", err
	}
	var confirmedProcessID uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &confirmedProcessID); err != nil || confirmedProcessID != processID {
		return "", errors.New("named-pipe peer changed during token verification")
	}
	return logonSID, nil
}
