//go:build windows

package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsIdentityStableDPAPIStorageAndCorruption(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "RemoteClient")
	first, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || !bytes.Equal(first.privateKey, second.privateKey) {
		t.Fatal("Windows identity did not survive reload")
	}
	keyPath := filepath.Join(directory, PrivateKeyFilename)
	stored, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, first.privateKey) || !bytes.HasPrefix(stored, identityMagic) {
		t.Fatal("Windows identity is not stored only in the versioned DPAPI envelope")
	}
	for _, path := range []string{directory, keyPath, filepath.Join(directory, MetadataFilename)} {
		security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := security.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("path %s DACL is not protected: control=%#x err=%v", path, control, err)
		}
	}

	corrupted := append([]byte(nil), stored...)
	corrupted[len(corrupted)-1] ^= 1
	if err := os.WriteFile(keyPath, corrupted, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("corrupted DPAPI identity was silently replaced")
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(after, corrupted) {
		t.Fatalf("corrupted identity was modified: err=%v", err)
	}
}

func TestWindowsIdentityNoReplaceAndPartialState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "RemoteClient")
	storeValue, err := newStorage(directory)
	if err != nil {
		t.Fatal(err)
	}
	store := storeValue.(*windowsStorage)
	_, first, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePrivateKey(first); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePrivateKey(second); err == nil {
		t.Fatal("existing Windows Device key was replaced")
	}
	loaded, err := store.LoadPrivateKey()
	if err != nil || !bytes.Equal(loaded, first) {
		t.Fatalf("stored key changed after no-replace conflict: err=%v", err)
	}

	partialDirectory := filepath.Join(t.TempDir(), "partial")
	partialValue, err := newStorage(partialDirectory)
	if err != nil {
		t.Fatal(err)
	}
	partial := partialValue.(*windowsStorage)
	if err := partial.WriteMetadata(&metadata{
		Version: metadataVersion, DeviceID: "dev_orphan", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(partialDirectory); err == nil {
		t.Fatal("metadata-without-key partial state generated a new identity")
	}
}
