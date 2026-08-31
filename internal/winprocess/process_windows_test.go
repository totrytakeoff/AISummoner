//go:build windows

package winprocess

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestPowerShellCommandEncodingRoundTripAndRejectsInvalidUTF8(t *testing.T) {
	script := UTF8PowerShellPrefix + `[Console]::Out.Write("AIS_中文")`
	encoded, err := EncodePowerShellCommand(script)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(contents)%2 != 0 {
		t.Fatalf("decode PowerShell command: length=%d err=%v", len(contents), err)
	}
	codeUnits := make([]uint16, len(contents)/2)
	for index := range codeUnits {
		codeUnits[index] = uint16(contents[index*2]) | uint16(contents[index*2+1])<<8
	}
	if decoded := string(utf16.Decode(codeUnits)); decoded != script {
		t.Fatalf("PowerShell command round trip = %q", decoded)
	}
	if _, err := EncodePowerShellCommand(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 PowerShell command was accepted")
	}
	if _, err := EncodePowerShellCommand(""); err == nil {
		t.Fatal("empty PowerShell command was accepted")
	}
}

func TestPowerShellPathAndWorkingDirectoryContracts(t *testing.T) {
	executable, err := PowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("PowerShell executable %q: info=%v err=%v", executable, info, err)
	}
	profile, err := ResolveWorkingDirectory("")
	if err != nil || !filepath.IsAbs(profile) {
		t.Fatalf("default working directory = %q, err=%v", profile, err)
	}
	directory := t.TempDir()
	if resolved, err := ResolveWorkingDirectory(directory); err != nil || resolved != directory {
		t.Fatalf("resolved working directory = %q, err=%v", resolved, err)
	}
	file := filepath.Join(directory, "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"relative", directory + string(os.PathSeparator) + ".", file,
		filepath.Join(directory, "missing"), directory + "\x00suffix",
	} {
		if _, err := ResolveWorkingDirectory(value); err == nil {
			t.Fatalf("invalid working directory %q was accepted", value)
		}
	}
}

func TestPowerShellConPTYRejectsInvalidDimensionsBeforeLaunch(t *testing.T) {
	for _, dimensions := range [][2]int16{{0, 24}, {80, 0}, {-1, 24}, {80, -1}} {
		process, err := StartPowerShellConPTY("", dimensions[0], dimensions[1])
		if err == nil {
			t.Fatalf("ConPTY dimensions %v were accepted: %+v", dimensions, process)
		}
		if process.Job != 0 || process.Process != 0 || process.Pseudo != 0 || process.Input != nil || process.Output != nil {
			t.Fatalf("invalid ConPTY dimensions returned owned resources: %+v", process)
		}
	}
}
