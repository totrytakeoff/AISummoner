// Package identity persists and validates the Remote Client's Ed25519 identity.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"golang.org/x/crypto/ssh"
)

const (
	PrivateKeyFilename = "device_ed25519"
	MetadataFilename   = "device.json"
	metadataVersion    = 1
)

type Identity struct {
	DeviceID   string
	PublicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	CreatedAt  time.Time
}

type metadata struct {
	Version   int       `json:"version"`
	DeviceID  string    `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
}

// LoadOrCreate loads a stable device identity, or atomically creates one. The
// private key is PKCS#8 PEM and is never returned through serialization APIs.
func LoadOrCreate(directory string) (*Identity, error) {
	return loadOrCreate(directory, rand.Reader, time.Now)
}

func loadOrCreate(directory string, random io.Reader, now func() time.Time) (*Identity, error) {
	if directory == "" {
		return nil, errors.New("identity directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create identity directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect identity directory: %w", err)
	}
	keyPath := filepath.Join(directory, PrivateKeyFilename)
	contents, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, metadataErr := os.Stat(filepath.Join(directory, MetadataFilename)); metadataErr == nil {
			return nil, errors.New("identity metadata exists but private key is missing")
		} else if !errors.Is(metadataErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect identity metadata: %w", metadataErr)
		}
		return create(directory, random, now().UTC())
	}
	if err != nil {
		return nil, fmt.Errorf("read device private key: %w", err)
	}
	if err := requirePrivateMode(keyPath); err != nil {
		return nil, err
	}
	privateKey, err := parsePrivateKey(contents)
	if err != nil {
		return nil, err
	}
	identity, err := fromPrivateKey(privateKey, time.Time{})
	if err != nil {
		return nil, err
	}
	metadataPath := filepath.Join(directory, MetadataFilename)
	metadataContents, err := os.ReadFile(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		identity.CreatedAt = now().UTC()
		if err := writeMetadata(metadataPath, identity); err != nil {
			return nil, err
		}
		return identity, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read identity metadata: %w", err)
	}
	var stored metadata
	if err := json.Unmarshal(metadataContents, &stored); err != nil {
		return nil, fmt.Errorf("decode identity metadata: %w", err)
	}
	if stored.Version != metadataVersion || stored.DeviceID != identity.DeviceID || stored.CreatedAt.IsZero() {
		return nil, errors.New("identity metadata does not match private key")
	}
	identity.CreatedAt = stored.CreatedAt.UTC()
	return identity, nil
}

func create(directory string, random io.Reader, createdAt time.Time) (*Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("generate device identity: %w", err)
	}
	identity, err := fromPrivateKey(privateKey, createdAt)
	if err != nil {
		return nil, err
	}
	identity.PublicKey = publicKey
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("encode device private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	keyPath := filepath.Join(directory, PrivateKeyFilename)
	if err := writePrivateFile(keyPath, pemBytes); err != nil {
		return nil, err
	}
	if err := writeMetadata(filepath.Join(directory, MetadataFilename), identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func fromPrivateKey(privateKey ed25519.PrivateKey, createdAt time.Time) (*Identity, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key length")
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("device key is not Ed25519")
	}
	deviceID, err := id.Device(publicKey)
	if err != nil {
		return nil, err
	}
	return &Identity{
		DeviceID: deviceID, PublicKey: append(ed25519.PublicKey(nil), publicKey...),
		privateKey: append(ed25519.PrivateKey(nil), privateKey...), CreatedAt: createdAt.UTC(),
	}, nil
}

func parsePrivateKey(contents []byte) (ed25519.PrivateKey, error) {
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("invalid device private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse device private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("device private key is not Ed25519")
	}
	return privateKey, nil
}

func writePrivateFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create device private key: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return fmt.Errorf("write device private key: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync device private key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close device private key: %w", err)
	}
	return nil
}

func writeMetadata(path string, identity *Identity) error {
	contents, err := json.MarshalIndent(metadata{
		Version: metadataVersion, DeviceID: identity.DeviceID, CreatedAt: identity.CreatedAt.UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identity metadata: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write identity metadata: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect identity metadata: %w", err)
	}
	return nil
}

func requirePrivateMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect device private key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("device private key is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("device private key permissions are %04o, require 0600", info.Mode().Perm())
	}
	return nil
}

// Sign signs a domain-separated authentication transcript.
func (i *Identity) Sign(message []byte) ([]byte, error) {
	if i == nil || len(i.privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("device identity is unavailable")
	}
	return ed25519.Sign(i.privateKey, message), nil
}

// SSHSigner returns a signer backed by the existing Device Identity. It never
// exposes or copies the private-key bytes outside this package.
func (i *Identity) SSHSigner() (ssh.Signer, error) {
	if i == nil || len(i.privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("device identity is unavailable")
	}
	signer, err := ssh.NewSignerFromKey(i.privateKey)
	if err != nil {
		return nil, fmt.Errorf("create device SSH signer: %w", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("device SSH signer is not Ed25519")
	}
	return signer, nil
}
