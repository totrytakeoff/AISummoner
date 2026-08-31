//go:build linux

package clientipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type unixTransport struct{}

type socketIdentity struct {
	device uint64
	inode  uint64
}

type unixAuthenticatedListener struct {
	*net.UnixListener
	path         string
	effectiveUID uint32
	identity     socketIdentity
	closeOnce    sync.Once
}

func currentTransport() localTransport { return unixTransport{} }

func (unixTransport) ValidateEndpoint(endpoint string) error {
	if !filepath.IsAbs(endpoint) || len(endpoint) > 100 {
		return errors.New("daemon socket path must be a bounded absolute path")
	}
	return nil
}

func (transport unixTransport) ValidateEndpointForDataDirectory(dataDirectory, endpoint string) error {
	if err := transport.ValidateEndpoint(endpoint); err != nil {
		return err
	}
	if filepath.Clean(filepath.Dir(endpoint)) != filepath.Clean(dataDirectory) {
		return errors.New("daemon socket must be inside its data directory")
	}
	return nil
}

func (unixTransport) DefaultEndpoint(dataDirectory string) string {
	return filepath.Join(dataDirectory, "client.sock")
}

func (transport unixTransport) Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	if err := transport.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "unix", endpoint)
}

func (transport unixTransport) Listen(endpoint string) (authenticatedListener, error) {
	if err := transport.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	effectiveUID := uint32(os.Geteuid())
	if err := prepareSocketParent(endpoint, effectiveUID); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(endpoint, effectiveUID); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on private client socket: %w", err)
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("protect private client socket: %w", err)
	}
	identity, err := inspectSocket(endpoint, effectiveUID)
	if err != nil {
		listener.Close()
		return nil, err
	}
	return &unixAuthenticatedListener{
		UnixListener: listener, path: endpoint, effectiveUID: effectiveUID, identity: identity,
	}, nil
}

func (listener *unixAuthenticatedListener) Authenticate(connection net.Conn) error {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("local peer transport is invalid")
	}
	peerUID, err := unixPeerUID(unixConnection)
	if err != nil {
		return err
	}
	if peerUID != listener.effectiveUID {
		return errors.New("local peer identity does not match")
	}
	return nil
}

func (listener *unixAuthenticatedListener) Close() error {
	var result error
	listener.closeOnce.Do(func() {
		result = listener.UnixListener.Close()
		removeExactSocket(listener.path, listener.effectiveUID, listener.identity)
	})
	return result
}

func prepareSocketParent(path string, owner uint32) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect daemon socket directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("daemon socket directory must be a non-symlink mode-0700 directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return errors.New("daemon socket directory must be owned by the daemon user")
	}
	return nil
}

func removeStaleSocket(path string, owner uint32) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect daemon socket: %w", err)
	}
	if _, err := inspectSocket(path, owner); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
}

func removeExactSocket(path string, owner uint32, identity socketIdentity) {
	current, err := inspectSocket(path, owner)
	if err != nil || current != identity {
		return
	}
	_ = os.Remove(path)
}

func inspectSocket(path string, owner uint32) (socketIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return socketIdentity{}, fmt.Errorf("inspect daemon socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return socketIdentity{}, errors.New("daemon socket path is not an owned mode-0600 socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return socketIdentity{}, errors.New("daemon socket is not owned by the daemon user")
	}
	return socketIdentity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}

func unixPeerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if controlErr != nil {
		return 0, controlErr
	}
	if credential == nil {
		return 0, errors.New("local peer credentials are unavailable")
	}
	return credential.Uid, nil
}
