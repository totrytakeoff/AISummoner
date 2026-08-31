//go:build windows

package clientplatform

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/winsecurity"
)

const maxWindowsDataDirectoryBytes = 1024

type windowsRuntime struct{}

func currentRuntime() Runtime { return windowsRuntime{} }

func (windowsRuntime) Name() string { return protocol.PlatformWindows }

func (windowsRuntime) DefaultDataDirectory() (string, error) {
	return winsecurity.LocalDataDirectory("AISummoner", "RemoteClient")
}

func (windowsRuntime) ValidateDataDirectory(directory string) error {
	return validateWindowsDataDirectory(directory)
}

func (windowsRuntime) ValidatePrivilege(bool, bool) error {
	return winsecurity.RequireOrdinaryInteractiveUser()
}

func (windowsRuntime) NotifyShutdown(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}

func validateWindowsDataDirectory(directory string) error {
	if directory == "" || len(directory) > maxWindowsDataDirectoryBytes || strings.ContainsRune(directory, '\x00') {
		return errors.New("Windows client data directory is invalid")
	}
	cleaned := filepath.Clean(directory)
	if !filepath.IsAbs(cleaned) {
		return errors.New("Windows client data directory must be absolute")
	}
	volume := filepath.VolumeName(cleaned)
	if len(volume) != 2 || volume[1] != ':' ||
		!((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) {
		return errors.New("Windows client data directory must use a local drive")
	}
	if cleaned == volume+`\` || cleaned == volume+`/` {
		return errors.New("Windows client data directory cannot be a volume root")
	}
	return nil
}
