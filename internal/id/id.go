// Package id creates opaque, prefixed resource identifiers.
package id

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const randomIDBytes = 16

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// New returns a cryptographically random identifier such as req_abcd....
func New(prefix string) (string, error) {
	return newWithReader(prefix, rand.Reader)
}

func newWithReader(prefix string, reader io.Reader) (string, error) {
	if !validPrefix(prefix) {
		return "", fmt.Errorf("invalid id prefix %q", prefix)
	}
	random := make([]byte, randomIDBytes)
	if _, err := io.ReadFull(reader, random); err != nil {
		return "", fmt.Errorf("read random id: %w", err)
	}
	encoded := strings.ToLower(base32NoPadding.EncodeToString(random))
	return prefix + "_" + encoded, nil
}

// MustNew is intended for request paths where a failed system CSPRNG is fatal.
func MustNew(prefix string) string {
	value, err := New(prefix)
	if err != nil {
		panic(err)
	}
	return value
}

// Token returns a URL-safe random token with the requested entropy in bytes.
func Token(size int) (string, error) {
	if size < 16 || size > 128 {
		return "", errors.New("token size must be between 16 and 128 bytes")
	}
	random := make([]byte, size)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// Device derives the stable device identifier defined by protocol version 1.
func Device(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid Ed25519 public key length: %d", len(publicKey))
	}
	digest := sha256.Sum256(publicKey)
	encoded := strings.ToLower(base32NoPadding.EncodeToString(digest[:]))
	return "dev_" + encoded[:32], nil
}

func validPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 16 {
		return false
	}
	for index, char := range prefix {
		if char >= 'a' && char <= 'z' {
			continue
		}
		if index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}
