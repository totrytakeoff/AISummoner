package id

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestNewWithReader(t *testing.T) {
	value, err := newWithReader("req", bytes.NewReader(make([]byte, randomIDBytes)))
	if err != nil {
		t.Fatalf("newWithReader: %v", err)
	}
	if value != "req_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected id: %q", value)
	}
}

func TestNewRejectsInvalidPrefix(t *testing.T) {
	for _, prefix := range []string{"", "UPPER", "bad-prefix", "1bad"} {
		if _, err := newWithReader(prefix, bytes.NewReader(make([]byte, randomIDBytes))); err == nil {
			t.Fatalf("prefix %q was accepted", prefix)
		}
	}
}

func TestDeviceIsDeterministic(t *testing.T) {
	publicKey := ed25519.PublicKey(bytes.Repeat([]byte{0x42}, ed25519.PublicKeySize))
	first, err := Device(publicKey)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	second, err := Device(publicKey)
	if err != nil {
		t.Fatalf("Device second call: %v", err)
	}
	if first != second || !strings.HasPrefix(first, "dev_") || len(first) != 36 {
		t.Fatalf("unexpected deterministic device id: %q / %q", first, second)
	}
	if _, err := Device(ed25519.PublicKey{1, 2, 3}); err == nil {
		t.Fatal("short key was accepted")
	}
}
