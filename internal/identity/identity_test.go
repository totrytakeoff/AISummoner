package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestLoadOrCreatePersistsStableIdentityAndPrivateMode(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	first, err := loadOrCreate(directory, bytes.NewReader(bytes.Repeat([]byte{7}, 64)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreate(directory, nil, func() time.Time { return now.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("identity was not stable: first=%+v second=%+v", first, second)
	}
	info, err := os.Stat(filepath.Join(directory, PrivateKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key mode = %04o, want 0600", got)
	}
	message := []byte("signed transcript")
	signature, err := second.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) == 0 {
		t.Fatal("empty signature")
	}
}

func TestLoadRejectsInsecurePrivateKeyMode(t *testing.T) {
	directory := t.TempDir()
	if _, err := loadOrCreate(directory, bytes.NewReader(bytes.Repeat([]byte{9}, 64)), time.Now); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, PrivateKeyFilename)
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("expected insecure key mode to be rejected")
	}
}

func TestLoadRejectsMetadataMismatch(t *testing.T) {
	directory := t.TempDir()
	if _, err := loadOrCreate(directory, bytes.NewReader(bytes.Repeat([]byte{5}, 64)), time.Now); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(directory, MetadataFilename)
	if err := os.WriteFile(metadataPath, []byte(`{"version":1,"device_id":"dev_wrong","created_at":"2026-08-13T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("expected metadata mismatch to be rejected")
	}
}

func TestSSHSignerUsesExactDevicePublicKey(t *testing.T) {
	deviceIdentity, err := loadOrCreate(t.TempDir(), bytes.NewReader(bytes.Repeat([]byte{3}, 64)), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := deviceIdentity.SSHSigner()
	if err != nil {
		t.Fatal(err)
	}
	want, err := ssh.NewPublicKey(deviceIdentity.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 || !bytes.Equal(signer.PublicKey().Marshal(), want.Marshal()) {
		t.Fatal("SSH signer does not use the exact Device Identity public key")
	}
	if _, err := (*Identity)(nil).SSHSigner(); err == nil {
		t.Fatal("nil identity returned an SSH signer")
	}
}
