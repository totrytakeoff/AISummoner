// Package identity persists and validates the Remote Client's Ed25519 identity.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"golang.org/x/crypto/ssh"
)

const metadataVersion = 1

var (
	errPrivateKeyNotFound = errors.New("device private key not found")
	errMetadataNotFound   = errors.New("identity metadata not found")
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

// storage is the platform security boundary for Device Identity material.
// Common code owns key generation and identity validation; a platform store
// owns at-rest encoding, permissions/ACLs and persistence.
type storage interface {
	LoadPrivateKey() (ed25519.PrivateKey, error)
	CreatePrivateKey(ed25519.PrivateKey) error
	LoadMetadata() (*metadata, error)
	WriteMetadata(*metadata) error
}

// LoadOrCreate loads a stable device identity, or atomically creates one. The
// private key is never returned through serialization APIs.
func LoadOrCreate(directory string) (*Identity, error) {
	return loadOrCreate(directory, rand.Reader, time.Now)
}

func loadOrCreate(directory string, random io.Reader, now func() time.Time) (*Identity, error) {
	if directory == "" {
		return nil, errors.New("identity directory is required")
	}
	store, err := newStorage(directory)
	if err != nil {
		return nil, err
	}
	return loadOrCreateFromStorage(store, random, now)
}

func loadOrCreateFromStorage(store storage, random io.Reader, now func() time.Time) (*Identity, error) {
	if store == nil {
		return nil, errors.New("identity storage is required")
	}
	privateKey, err := store.LoadPrivateKey()
	if errors.Is(err, errPrivateKeyNotFound) {
		if _, metadataErr := store.LoadMetadata(); metadataErr == nil {
			return nil, errors.New("identity metadata exists but private key is missing")
		} else if !errors.Is(metadataErr, errMetadataNotFound) {
			return nil, fmt.Errorf("inspect identity metadata: %w", metadataErr)
		}
		return create(store, random, now().UTC())
	}
	if err != nil {
		return nil, err
	}
	deviceIdentity, err := fromPrivateKey(privateKey, time.Time{})
	if err != nil {
		return nil, err
	}
	stored, err := store.LoadMetadata()
	if errors.Is(err, errMetadataNotFound) {
		deviceIdentity.CreatedAt = now().UTC()
		if err := store.WriteMetadata(metadataFromIdentity(deviceIdentity)); err != nil {
			return nil, err
		}
		return deviceIdentity, nil
	}
	if err != nil {
		return nil, err
	}
	if stored.Version != metadataVersion || stored.DeviceID != deviceIdentity.DeviceID || stored.CreatedAt.IsZero() {
		return nil, errors.New("identity metadata does not match private key")
	}
	deviceIdentity.CreatedAt = stored.CreatedAt.UTC()
	return deviceIdentity, nil
}

func create(store storage, random io.Reader, createdAt time.Time) (*Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("generate device identity: %w", err)
	}
	deviceIdentity, err := fromPrivateKey(privateKey, createdAt)
	if err != nil {
		return nil, err
	}
	deviceIdentity.PublicKey = publicKey
	if err := store.CreatePrivateKey(privateKey); err != nil {
		return nil, err
	}
	if err := store.WriteMetadata(metadataFromIdentity(deviceIdentity)); err != nil {
		return nil, err
	}
	return deviceIdentity, nil
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

func metadataFromIdentity(deviceIdentity *Identity) *metadata {
	return &metadata{
		Version: metadataVersion, DeviceID: deviceIdentity.DeviceID,
		CreatedAt: deviceIdentity.CreatedAt.UTC(),
	}
}

func encodeMetadata(value *metadata) ([]byte, error) {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode identity metadata: %w", err)
	}
	return append(contents, '\n'), nil
}

func decodeMetadata(contents []byte) (*metadata, error) {
	var value metadata
	if err := json.Unmarshal(contents, &value); err != nil {
		return nil, fmt.Errorf("decode identity metadata: %w", err)
	}
	return &value, nil
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
