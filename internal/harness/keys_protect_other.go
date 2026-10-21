//go:build !windows

package harness

import "errors"

// Without an operating-system key store the key is kept as entered in a 0600
// file; key listings report it as unencrypted.
const keyProtection = keyProtectionPlaintext

func protectKey(secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", errInvalidSecret
	}
	return string(secret), nil
}

func unprotectKey(protection, data string) ([]byte, error) {
	if protection != keyProtectionPlaintext {
		return nil, errors.New("harness: key was encrypted on another system")
	}
	return []byte(data), nil
}
