//go:build windows

package clientplatform

import (
	"path/filepath"
	"testing"

	"github.com/aisummoner/aisummoner/internal/protocol"
	"github.com/aisummoner/aisummoner/internal/winsecurity"
)

func TestWindowsRuntimeContract(t *testing.T) {
	runtime := windowsRuntime{}
	if runtime.Name() != protocol.PlatformWindows {
		t.Fatalf("platform = %q", runtime.Name())
	}
	directory, err := runtime.DefaultDataDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(directory) || filepath.Base(directory) != "RemoteClient" {
		t.Fatalf("default data directory = %q", directory)
	}
	if err := runtime.ValidateDataDirectory(directory); err != nil {
		t.Fatal(err)
	}
	facts, err := winsecurity.CurrentTokenFacts()
	if err != nil {
		t.Fatal(err)
	}
	wantError := winsecurity.ValidateOrdinaryInteractiveUser(facts) != nil
	for _, flags := range [][2]bool{{false, false}, {true, true}} {
		err := runtime.ValidatePrivilege(flags[0], flags[1])
		if (err != nil) != wantError {
			t.Fatalf("ValidatePrivilege%v error=%v wantError=%v", flags, err, wantError)
		}
	}
}

func TestWindowsDataDirectoryValidation(t *testing.T) {
	for _, directory := range []string{`C:\Users\alice\AppData\Local\AISummoner`, `d:/data/aisummoner`} {
		if err := validateWindowsDataDirectory(directory); err != nil {
			t.Errorf("valid directory %q: %v", directory, err)
		}
	}
	for _, directory := range []string{
		"", `relative\data`, `C:\`, `\\server\share\aisummoner`,
		`\\?\C:\AISummoner`, "C:\\AISummoner\x00bad",
	} {
		if err := validateWindowsDataDirectory(directory); err == nil {
			t.Errorf("invalid directory %q was accepted", directory)
		}
	}
}
