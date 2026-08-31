//go:build windows

package winsecurity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOrdinaryInteractiveUserPolicy(t *testing.T) {
	ordinary := TokenFacts{
		UserSID: "S-1-5-21-test", LogonSID: "S-1-5-5-test", SessionID: 1,
		IntegrityRID: 0x2000,
	}
	if err := ValidateOrdinaryInteractiveUser(ordinary); err != nil {
		t.Fatalf("ordinary filtered token was rejected: %v", err)
	}
	for name, facts := range map[string]TokenFacts{
		"elevated":  withFact(ordinary, func(value *TokenFacts) { value.Elevated = true }),
		"high":      withFact(ordinary, func(value *TokenFacts) { value.IntegrityRID = mandatoryHighRID }),
		"system":    withFact(ordinary, func(value *TokenFacts) { value.System = true }),
		"session 0": withFact(ordinary, func(value *TokenFacts) { value.SessionID = 0 }),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateOrdinaryInteractiveUser(facts); err == nil {
				t.Fatal("unsafe Windows token was accepted")
			}
		})
	}
}

func TestCurrentTokenKnownFolderAndProtectedPathContracts(t *testing.T) {
	facts, err := CurrentTokenFacts()
	if err != nil {
		t.Fatal(err)
	}
	if facts.UserSID == "" || facts.LogonSID == "" || facts.IntegrityRID == 0 {
		t.Fatalf("incomplete token facts: %+v", facts)
	}
	directory, err := LocalDataDirectory("AISummoner", "RemoteClient")
	if err != nil || !filepath.IsAbs(directory) || filepath.Base(directory) != "RemoteClient" {
		t.Fatalf("LocalAppData directory = %q, err=%v", directory, err)
	}

	protectedDirectory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(protectedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RequireDirectory(protectedDirectory); err != nil {
		t.Fatal(err)
	}
	if err := ProtectPath(protectedDirectory, true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(protectedDirectory, "value.bin")
	if err := os.WriteFile(file, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireRegularFile(file); err != nil {
		t.Fatal(err)
	}
	if err := ProtectPath(file, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{protectedDirectory, file} {
		security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatalf("inspect DACL for %s: %v", path, err)
		}
		control, _, err := security.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("path %s DACL is not protected: control=%#x err=%v", path, control, err)
		}
	}
}

func TestDPAPICurrentUserRoundTripAndCorruption(t *testing.T) {
	plaintext := []byte("AISummoner Windows DPAPI 中文")
	entropy := []byte("AISummoner.Test.v1")
	protected, err := ProtectCurrentUser(plaintext, entropy, "AISummoner test")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, plaintext) {
		t.Fatal("DPAPI ciphertext contains plaintext")
	}
	roundTrip, err := UnprotectCurrentUser(protected, entropy)
	if err != nil || !bytes.Equal(roundTrip, plaintext) {
		t.Fatalf("DPAPI round trip: value=%q err=%v", roundTrip, err)
	}
	corrupted := append([]byte(nil), protected...)
	corrupted[len(corrupted)/2] ^= 0x80
	if _, err := UnprotectCurrentUser(corrupted, entropy); err == nil {
		t.Fatal("corrupted DPAPI ciphertext was accepted")
	}
	if _, err := UnprotectCurrentUser(protected, []byte("wrong entropy")); err == nil {
		t.Fatal("wrong DPAPI entropy was accepted")
	}
}

func withFact(value TokenFacts, change func(*TokenFacts)) TokenFacts {
	change(&value)
	return value
}
