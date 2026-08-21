//go:build !windows && !linux

package controlhandoff

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformFailsClosedV1(t *testing.T) {
	if err := VerifyNonElevatedV1(); !errors.Is(err, ErrElevatedProcess) {
		t.Fatalf("privilege verification error = %v", err)
	}
	if _, err := writeExclusivePrivateFilePlatformV1("unused", []byte("secret")); !errors.Is(err, ErrSecurityVerification) {
		t.Fatalf("publication error = %v", err)
	}
}
