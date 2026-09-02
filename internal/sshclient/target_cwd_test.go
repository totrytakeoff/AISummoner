package sshclient

import (
	"errors"
	"testing"

	"github.com/aisummoner/aisummoner/internal/protocol"
)

func TestValidateTargetCWDIsIndependentOfServerHost(t *testing.T) {
	valid := []struct {
		platform string
		cwd      string
	}{
		{protocol.PlatformLinux, ""},
		{protocol.PlatformLinux, "/"},
		{protocol.PlatformLinux, "/srv/repository"},
		{protocol.PlatformWindows, ""},
		{protocol.PlatformWindows, `C:\`},
		{protocol.PlatformWindows, `c:\Users\Alice\source`},
		{protocol.PlatformWindows, `D:\项目\代码`},
		{protocol.PlatformWindows, `\\server\share`},
		{protocol.PlatformWindows, `\\server\share\repository`},
	}
	for _, test := range valid {
		if err := validateTargetCWD(test.cwd, test.platform); err != nil {
			t.Errorf("valid target cwd platform=%q cwd=%q: %v", test.platform, test.cwd, err)
		}
	}

	invalid := []struct {
		platform string
		cwd      string
	}{
		{"", ""},
		{"darwin", "/tmp"},
		{protocol.PlatformLinux, "relative"},
		{protocol.PlatformLinux, `C:\Users\Alice`},
		{protocol.PlatformWindows, "relative"},
		{protocol.PlatformWindows, `C:relative`},
		{protocol.PlatformWindows, `/Users/Alice`},
		{protocol.PlatformWindows, `C:/Users/Alice`},
		{protocol.PlatformWindows, `C:\Users\..\Windows`},
		{protocol.PlatformWindows, `C:\Users\Alice\`},
		{protocol.PlatformWindows, `\\server`},
		{protocol.PlatformWindows, `\\?\C:\Windows`},
		{protocol.PlatformWindows, `\\.\pipe\name`},
	}
	for _, test := range invalid {
		err := validateTargetCWD(test.cwd, test.platform)
		if !errors.Is(err, ErrInvalidCWD) {
			t.Errorf("invalid target cwd platform=%q cwd=%q: %v", test.platform, test.cwd, err)
		}
	}
}
