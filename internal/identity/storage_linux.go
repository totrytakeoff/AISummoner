//go:build linux

package identity

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	PrivateKeyFilename = "device_ed25519"
	MetadataFilename   = "device.json"
)

type linuxStorage struct {
	directory string
}

func newStorage(directory string) (storage, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create identity directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect identity directory: %w", err)
	}
	return &linuxStorage{directory: directory}, nil
}

func (store *linuxStorage) LoadPrivateKey() (ed25519.PrivateKey, error) {
	path := filepath.Join(store.directory, PrivateKeyFilename)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errPrivateKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read device private key: %w", err)
	}
	if err := requirePrivateMode(path); err != nil {
		return nil, err
	}
	return parsePrivateKey(contents)
}

func (store *linuxStorage) CreatePrivateKey(privateKey ed25519.PrivateKey) error {
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("encode device private key: %w", err)
	}
	contents := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	return writePrivateFile(filepath.Join(store.directory, PrivateKeyFilename), contents)
}

func (store *linuxStorage) LoadMetadata() (*metadata, error) {
	contents, err := os.ReadFile(filepath.Join(store.directory, MetadataFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errMetadataNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read identity metadata: %w", err)
	}
	return decodeMetadata(contents)
}

func (store *linuxStorage) WriteMetadata(value *metadata) error {
	contents, err := encodeMetadata(value)
	if err != nil {
		return err
	}
	path := filepath.Join(store.directory, MetadataFilename)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write identity metadata: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect identity metadata: %w", err)
	}
	return nil
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
