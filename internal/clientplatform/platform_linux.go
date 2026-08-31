//go:build linux

package clientplatform

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/aisummoner/aisummoner/internal/protocol"
)

type linuxRuntime struct{}

func currentRuntime() Runtime { return linuxRuntime{} }

func (linuxRuntime) Name() string { return protocol.PlatformLinux }

func (linuxRuntime) DefaultDataDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "aisummoner"), nil
}

func (linuxRuntime) ValidateDataDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("client data directory must be absolute")
	}
	return nil
}

func (linuxRuntime) ValidatePrivilege(development, allowPrivilegedDevelopment bool) error {
	return validateLinuxPrivilege(os.Geteuid(), development, allowPrivilegedDevelopment)
}

func (linuxRuntime) NotifyShutdown(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
}

func validateLinuxPrivilege(effectiveUID int, development, allowRootDevelopment bool) error {
	if effectiveUID == 0 && !(development && allowRootDevelopment) {
		return errors.New("refusing to run as root (development requires both --dev and --allow-root-dev)")
	}
	return nil
}
