// Package moduleapi defines the dependency-light public contracts used by
// FreeAgent module packages and exact runtime adapters.
package moduleapi

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Permission is an untrusted capability request from a module manifest.
// Installation and activation policy decide whether any requested permission
// is granted; the value itself carries no authority.
type Permission string

func (permission Permission) Validate() error {
	if !validDottedIdentifier(string(permission), MaxIdentifierBytes) {
		return fmt.Errorf("invalid module permission %q", permission)
	}
	return nil
}

// Ref identifies one exact module package version.
type Ref struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (ref Ref) Validate() error {
	if !validDottedIdentifier(ref.ID, MaxIdentifierBytes) {
		return fmt.Errorf("invalid module id %q", ref.ID)
	}
	if !validVersion(ref.Version) {
		return fmt.Errorf("invalid exact module version %q", ref.Version)
	}
	return nil
}

// CanonicalKey returns the exact validated identity. The accepted grammars
// forbid '@', so this representation is lossless and unambiguous.
func (ref Ref) CanonicalKey() (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return CanonicalText(ref.ID) + "@" + CanonicalText(ref.Version), nil
}

func (ref Ref) String() string {
	return ref.ID + "@" + ref.Version
}

func (ref Ref) IsZero() bool {
	return ref.ID == "" && ref.Version == ""
}

// FailurePolicy belongs to a consumer-owned PortBinding. A module manifest
// cannot choose it for the consumer.
type FailurePolicy string

const (
	FailureRequired FailurePolicy = "REQUIRED"
	FailureOptional FailurePolicy = "OPTIONAL"
)

func (policy FailurePolicy) Validate() error {
	switch policy {
	case FailureRequired, FailureOptional:
		return nil
	default:
		return fmt.Errorf("unsupported failure policy %q", policy)
	}
}

func ValidSHA256(value string) bool {
	if len(value) != SHA256HexLength {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= '0' && character <= '9' ||
			character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func validateOpaqueID(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > MaxOpaqueIDBytes || !utf8.ValidString(value) {
		return fmt.Errorf(
			"%s must be canonical UTF-8 containing between 1 and %d bytes",
			name,
			MaxOpaqueIDBytes,
		)
	}
	if value != CanonicalText(value) {
		return fmt.Errorf("%s must use Unicode NFC", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func validateBoundedText(
	name string,
	value string,
	maximum int,
	allowEmpty bool,
) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if !allowEmpty && value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	return nil
}

func validDottedIdentifier(value string, maximum int) bool {
	if value == "" || len(value) > maximum ||
		strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	segmentStart := true
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '.' {
			if segmentStart {
				return false
			}
			segmentStart = true
			continue
		}
		if segmentStart {
			if character < 'a' || character > 'z' {
				return false
			}
			segmentStart = false
			continue
		}
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return !segmentStart
}

func validVersion(value string) bool {
	if value == "" || len(value) > MaxVersionBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && (character == '.' || character == '_' ||
			character == '-' || character == '+') {
			continue
		}
		return false
	}
	last := value[len(value)-1]
	return last != '.' && last != '_' && last != '-' && last != '+'
}
