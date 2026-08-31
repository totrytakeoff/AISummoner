//go:build linux

package clientplatform

import (
	"path/filepath"
	"testing"
)

func TestLinuxPrivilegeRequiresBothDevelopmentFlagsForRoot(t *testing.T) {
	tests := []struct {
		name        string
		effectiveID int
		development bool
		allowRoot   bool
		wantError   bool
	}{
		{name: "ordinary user", effectiveID: 1000},
		{name: "root default", effectiveID: 0, wantError: true},
		{name: "root dev only", effectiveID: 0, development: true, wantError: true},
		{name: "root allow only", effectiveID: 0, allowRoot: true, wantError: true},
		{name: "root explicit development override", effectiveID: 0, development: true, allowRoot: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateLinuxPrivilege(test.effectiveID, test.development, test.allowRoot)
			if (err != nil) != test.wantError {
				t.Fatalf("validateLinuxPrivilege() error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestLinuxRuntimeContract(t *testing.T) {
	runtime := linuxRuntime{}
	if runtime.Name() != "linux" {
		t.Fatalf("platform = %q", runtime.Name())
	}
	directory, err := runtime.DefaultDataDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(directory) || filepath.Base(directory) != "aisummoner" {
		t.Fatalf("default data directory = %q", directory)
	}
	if err := runtime.ValidateDataDirectory(directory); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ValidateDataDirectory("relative"); err == nil {
		t.Fatal("relative Linux data directory was accepted")
	}
}
