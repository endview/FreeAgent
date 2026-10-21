//go:build windows

package harness

import (
	"encoding/base64"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const keyProtection = keyProtectionDPAPI

// The entropy ties ciphertext to FreeAgent; it is not a secret.
var keyEntropy = []byte("freeagent-keys-v1")

// protectKey encrypts for the current Windows user with DPAPI.
func protectKey(secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", errInvalidSecret
	}
	in := windows.DataBlob{Size: uint32(len(secret)), Data: &secret[0]}
	entropy := windows.DataBlob{Size: uint32(len(keyEntropy)), Data: &keyEntropy[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", errors.New("harness: Windows could not encrypt the key")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 {
		return "", errors.New("harness: Windows could not encrypt the key")
	}
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

// unprotectKey fails for keys saved by another user, another machine, or
// another system; the caller turns that into ErrKeyNeedsReentry.
func unprotectKey(protection, data string) ([]byte, error) {
	if protection != keyProtectionDPAPI {
		return nil, errors.New("harness: key was not encrypted for this Windows user")
	}
	sealed, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(sealed) == 0 {
		return nil, errors.New("harness: stored key is damaged")
	}
	in := windows.DataBlob{Size: uint32(len(sealed)), Data: &sealed[0]}
	entropy := windows.DataBlob{Size: uint32(len(keyEntropy)), Data: &keyEntropy[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, errors.New("harness: Windows could not decrypt the key")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 {
		return nil, errors.New("harness: stored key is damaged")
	}
	view := unsafe.Slice(out.Data, out.Size)
	plain := make([]byte, len(view))
	copy(plain, view)
	clear(view)
	return plain, nil
}
