//go:build windows

package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aisummoner/aisummoner/internal/winsecurity"
	"golang.org/x/sys/windows"
)

const (
	PrivateKeyFilename      = "device_ed25519.dpapi"
	MetadataFilename        = "device.json"
	maxPrivateEnvelopeBytes = 64 * 1024
	maxMetadataBytes        = 16 * 1024
)

var (
	dpapiEntropy  = []byte("AISummoner.DeviceIdentity.v1")
	identityMagic = []byte{'A', 'I', 'S', 'D', 'P', 'A', 'P', 'I', 1}
)

type windowsStorage struct {
	directory string
}

func newStorage(directory string) (storage, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, errors.New("Windows identity directory must be absolute")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create identity directory: %w", err)
	}
	if err := winsecurity.RequireDirectory(directory); err != nil {
		return nil, err
	}
	if err := winsecurity.ProtectPath(directory, true); err != nil {
		return nil, err
	}
	return &windowsStorage{directory: directory}, nil
}

func (store *windowsStorage) LoadPrivateKey() (ed25519.PrivateKey, error) {
	path := filepath.Join(store.directory, PrivateKeyFilename)
	contents, err := readSecureFile(path, maxPrivateEnvelopeBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errPrivateKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read protected device private key: %w", err)
	}
	minimum := len(identityMagic) + 4
	if len(contents) < minimum || !bytes.Equal(contents[:len(identityMagic)], identityMagic) {
		return nil, errors.New("invalid protected device private key envelope")
	}
	size := binary.LittleEndian.Uint32(contents[len(identityMagic):minimum])
	if size == 0 || uint64(size) != uint64(len(contents)-minimum) {
		return nil, errors.New("invalid protected device private key size")
	}
	plaintext, err := winsecurity.UnprotectCurrentUser(contents[minimum:], dpapiEntropy)
	if err != nil {
		return nil, fmt.Errorf("decrypt device private key: %w", err)
	}
	defer clear(plaintext)
	parsed, err := x509.ParsePKCS8PrivateKey(plaintext)
	if err != nil {
		return nil, fmt.Errorf("parse device private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("device private key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func (store *windowsStorage) CreatePrivateKey(privateKey ed25519.PrivateKey) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid Ed25519 private key length")
	}
	plaintext, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("encode device private key: %w", err)
	}
	defer clear(plaintext)
	protected, err := winsecurity.ProtectCurrentUser(
		plaintext, dpapiEntropy, "AISummoner Remote Device Identity",
	)
	if err != nil {
		return err
	}
	contents := make([]byte, len(identityMagic)+4+len(protected))
	copy(contents, identityMagic)
	binary.LittleEndian.PutUint32(contents[len(identityMagic):], uint32(len(protected)))
	copy(contents[len(identityMagic)+4:], protected)
	if len(contents) > maxPrivateEnvelopeBytes {
		return errors.New("protected device private key is too large")
	}
	return writeSecureFileExclusive(store.directory, PrivateKeyFilename, contents)
}

func (store *windowsStorage) LoadMetadata() (*metadata, error) {
	contents, err := readSecureFile(filepath.Join(store.directory, MetadataFilename), maxMetadataBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errMetadataNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read identity metadata: %w", err)
	}
	return decodeMetadata(contents)
}

func (store *windowsStorage) WriteMetadata(value *metadata) error {
	contents, err := encodeMetadata(value)
	if err != nil {
		return err
	}
	if len(contents) > maxMetadataBytes {
		return errors.New("identity metadata is too large")
	}
	return writeSecureFileExclusive(store.directory, MetadataFilename, contents)
}

func readSecureFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Windows identity path is not a regular file")
	}
	if err := winsecurity.RequireRegularFile(path); err != nil {
		return nil, err
	}
	if err := winsecurity.ProtectPath(path, false); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maximum {
		return nil, errors.New("Windows identity file is too large")
	}
	return contents, nil
}

func writeSecureFileExclusive(directory, filename string, contents []byte) error {
	if filepath.Base(filename) != filename || filename == "." || len(contents) == 0 {
		return errors.New("invalid Windows identity file")
	}
	temporary, err := os.CreateTemp(directory, ".aisummoner-identity-*")
	if err != nil {
		return fmt.Errorf("create identity temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write identity temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync identity temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close identity temporary file: %w", err)
	}
	if err := winsecurity.RequireRegularFile(temporaryPath); err != nil {
		return err
	}
	if err := winsecurity.ProtectPath(temporaryPath, false); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return errors.New("invalid identity temporary path")
	}
	targetPath := filepath.Join(directory, filename)
	target, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return errors.New("invalid identity target path")
	}
	// No MOVEFILE_REPLACE_EXISTING: concurrent startup must never rotate a key
	// that another daemon committed first.
	if err := windows.MoveFileEx(from, target, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("commit identity file without replacement: %w", err)
	}
	committed = true
	return nil
}
