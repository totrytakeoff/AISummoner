//go:build windows

package winsecurity

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxDPAPIBytes = 1024 * 1024

// ProtectCurrentUser binds plaintext to the current Windows user and machine.
func ProtectCurrentUser(plaintext, entropy []byte, description string) ([]byte, error) {
	if len(plaintext) == 0 || len(plaintext) > maxDPAPIBytes || len(entropy) == 0 || len(entropy) > maxDPAPIBytes {
		return nil, errors.New("invalid DPAPI input")
	}
	descriptionPointer, err := windows.UTF16PtrFromString(description)
	if err != nil {
		return nil, errors.New("invalid DPAPI description")
	}
	input := dataBlob(plaintext)
	optionalEntropy := dataBlob(entropy)
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input, descriptionPointer, &optionalEntropy, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &output,
	); err != nil {
		return nil, fmt.Errorf("protect Windows data: %w", err)
	}
	defer freeDataBlob(output)
	protected, err := copyDataBlob(output)
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(entropy)
	return protected, err
}

// UnprotectCurrentUser decrypts and integrity-checks a blob for the current
// Windows user and machine.
func UnprotectCurrentUser(protected, entropy []byte) ([]byte, error) {
	if len(protected) == 0 || len(protected) > maxDPAPIBytes || len(entropy) == 0 || len(entropy) > maxDPAPIBytes {
		return nil, errors.New("invalid DPAPI input")
	}
	input := dataBlob(protected)
	optionalEntropy := dataBlob(entropy)
	var output windows.DataBlob
	var description *uint16
	if err := windows.CryptUnprotectData(
		&input, &description, &optionalEntropy, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &output,
	); err != nil {
		return nil, fmt.Errorf("unprotect Windows data: %w", err)
	}
	if description != nil {
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(description)))
	}
	defer freeDataBlob(output)
	plaintext, err := copyDataBlob(output)
	runtime.KeepAlive(protected)
	runtime.KeepAlive(entropy)
	return plaintext, err
}

func dataBlob(contents []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(contents)), Data: &contents[0]}
}

func copyDataBlob(blob windows.DataBlob) ([]byte, error) {
	if blob.Data == nil || blob.Size == 0 || blob.Size > maxDPAPIBytes {
		return nil, errors.New("DPAPI returned invalid data")
	}
	return append([]byte(nil), unsafe.Slice(blob.Data, blob.Size)...), nil
}

func freeDataBlob(blob windows.DataBlob) {
	if blob.Data != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	}
}
