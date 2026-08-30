//go:build windows

package windowsprobe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dpapiEntropy  = []byte("AISummoner.DeviceIdentity.v1")
	identityMagic = []byte{'A', 'I', 'S', 'D', 'P', 'A', 'P', 'I', 1}
)

// ProtectCurrentUser binds plaintext to the current Windows user and machine.
// The returned blob is safe to persist but remains sensitive application data.
func ProtectCurrentUser(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("DPAPI plaintext is empty")
	}
	input := dataBlob(plaintext)
	entropy := dataBlob(dpapiEntropy)
	description, err := windows.UTF16PtrFromString("AISummoner Remote Device Identity")
	if err != nil {
		return nil, err
	}
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input, description, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output,
	); err != nil {
		return nil, fmt.Errorf("protect Device Identity: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	protected := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(plaintext)
	return protected, nil
}

// UnprotectCurrentUser decrypts and integrity-checks a blob for the current
// Windows user and machine.
func UnprotectCurrentUser(protected []byte) ([]byte, error) {
	if len(protected) == 0 {
		return nil, errors.New("DPAPI blob is empty")
	}
	input := dataBlob(protected)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	var description *uint16
	if err := windows.CryptUnprotectData(
		&input, &description, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output,
	); err != nil {
		return nil, fmt.Errorf("unprotect Device Identity: %w", err)
	}
	if description != nil {
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(description)))
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	plaintext := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(protected)
	return plaintext, nil
}

// WriteProtectedIdentity proves the production storage sequence: protected
// directory, DPAPI envelope, same-directory temporary file, flush, protected
// file ACL, and atomic replacement.
func WriteProtectedIdentity(directory, filename string, plaintext []byte) error {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Base(filename) != filename || filename == "." {
		return errors.New("invalid protected identity path")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}
	if err := ProtectPath(directory, true); err != nil {
		return err
	}
	protected, err := ProtectCurrentUser(plaintext)
	if err != nil {
		return err
	}
	contents := make([]byte, len(identityMagic)+4+len(protected))
	copy(contents, identityMagic)
	binary.LittleEndian.PutUint32(contents[len(identityMagic):], uint32(len(protected)))
	copy(contents[len(identityMagic)+4:], protected)

	temporary, err := os.CreateTemp(directory, ".device-identity-*")
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
	if err := ProtectPath(temporaryPath, false); err != nil {
		return err
	}
	target := filepath.Join(directory, filename)
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace protected identity: %w", err)
	}
	committed = true
	return nil
}

// ReadProtectedIdentity validates the versioned envelope before DPAPI access.
func ReadProtectedIdentity(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read protected identity: %w", err)
	}
	minimum := len(identityMagic) + 4
	if len(contents) < minimum || string(contents[:len(identityMagic)]) != string(identityMagic) {
		return nil, errors.New("invalid protected identity envelope")
	}
	size := binary.LittleEndian.Uint32(contents[len(identityMagic):minimum])
	if size == 0 || uint64(size) != uint64(len(contents)-minimum) {
		return nil, errors.New("invalid protected identity size")
	}
	return UnprotectCurrentUser(contents[minimum:])
}

func dataBlob(contents []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(contents)), Data: &contents[0]}
}
