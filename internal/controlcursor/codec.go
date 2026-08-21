// Package controlcursor authenticates process-local Control API page cursors.
//
// A token is an inert continuation hint. It never grants authorization and is
// accepted only after verification with the current process's ephemeral key.
package controlcursor

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	KeyBytesV1                       = sha256.Size
	MaximumModulesCursorTokenBytesV1 = 4096

	modulesCursorMACDomainV1    = "freeagent.control-modules-cursor-token-hmac/v1"
	cursorMagicV1               = "FACM"
	cursorEnvelopeVersionV1     = byte(1)
	cursorHeaderBytesV1         = 9
	cursorMACBytesV1            = sha256.Size
	maximumDecodedCursorBytesV1 = MaximumModulesCursorTokenBytesV1 * 3 / 4
	maximumPayloadBytesV1       = maximumDecodedCursorBytesV1 - cursorHeaderBytesV1 -
		cursorMACBytesV1 - 1
	maximumPayloadDepthV1 = 8
	maximumPayloadNodesV1 = 64
)

var (
	// ErrCursorInvalid is deliberately uniform for malformed, noncanonical,
	// unauthenticated, wrong-key, and semantically invalid tokens. Callers must
	// not expose which check failed.
	ErrCursorInvalid = errors.New("controlcursor: cursor invalid")

	// ErrCursorStale means an otherwise well-formed token belongs to another
	// boot, or the current registry has been revoked. The only safe recovery is
	// a full refetch; the unauthenticated envelope boot reference never grants
	// authority and no payload is returned on this path.
	ErrCursorStale = errors.New("controlcursor: cursor stale")

	// ErrUnavailable is limited to creation failure and server-side encoding
	// after shutdown. It carries no entropy-provider or key material.
	ErrUnavailable = errors.New("controlcursor: unavailable")
)

// ModulesCursorCodecV1 is the narrow transport-injection contract. Decode
// authenticates only the continuation metadata; the application service must
// still compare it with the currently admitted session, scope, and Store view.
type ModulesCursorCodecV1 interface {
	EncodeModulesCursorV1(controlapp.DecodedModulesCursorV1) (string, error)
	DecodeModulesCursorV1(string) (controlapp.DecodedModulesCursorV1, error)
	IsStaleModulesCursorErrorV1(error) bool
}

// RegistryV1 owns one boot-local HMAC key. It has no persistence, filesystem,
// network, logging, or key-export surface.
type RegistryV1 struct {
	mutex  sync.RWMutex
	key    [KeyBytesV1]byte
	bootID string
	closed bool
}

var _ ModulesCursorCodecV1 = (*RegistryV1)(nil)

// NewRegistryV1 binds a fresh process-local cursor authority to one exact boot
// identity and obtains its 256-bit key from the operating system CSPRNG.
func NewRegistryV1(bootID string) (*RegistryV1, error) {
	if !validBootIDV1(bootID) {
		return nil, ErrUnavailable
	}
	var key [KeyBytesV1]byte
	if _, err := rand.Read(key[:]); err != nil {
		clear(key[:])
		return nil, ErrUnavailable
	}
	registry := newRegistryWithKeyV1(bootID, key[:])
	clear(key[:])
	if registry == nil {
		return nil, ErrUnavailable
	}
	return registry, nil
}

// EncodeModulesCursorV1 validates and freezes one application cursor, then
// returns a single unpadded RawURL-Base64 token bounded to 4 KiB.
func (registry *RegistryV1) EncodeModulesCursorV1(
	cursor controlapp.DecodedModulesCursorV1,
) (string, error) {
	if registry == nil {
		return "", ErrUnavailable
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	if registry.closed {
		return "", ErrUnavailable
	}
	if cursor.BootID != registry.bootID {
		return "", ErrCursorInvalid
	}
	payload, err := freezeModulesCursorV1(cursor)
	if err != nil {
		return "", ErrCursorInvalid
	}
	defer clear(payload)
	if cursorHeaderBytesV1+len(registry.bootID)+len(payload)+cursorMACBytesV1 >
		maximumDecodedCursorBytesV1 {
		return "", ErrCursorInvalid
	}
	wire := sealV1(registry.key[:], registry.bootID, payload)
	defer clear(wire)
	envelope := base64.RawURLEncoding.EncodeToString(wire)
	if len(envelope) == 0 || len(envelope) > MaximumModulesCursorTokenBytesV1 {
		return "", ErrCursorInvalid
	}
	return envelope, nil
}

// DecodeModulesCursorV1 authenticates and strictly restores one token. A
// structurally valid foreign boot returns only ErrCursorStale; every other
// attacker-controlled failure is collapsed to ErrCursorInvalid.
func (registry *RegistryV1) DecodeModulesCursorV1(
	token string,
) (controlapp.DecodedModulesCursorV1, error) {
	if registry == nil || len(token) == 0 ||
		len(token) > MaximumModulesCursorTokenBytesV1 ||
		strings.IndexByte(token, '=') >= 0 {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	wire, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(wire) != token {
		clear(wire)
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	defer clear(wire)

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	if registry.closed {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorStale
	}
	payload, err := openV1(registry.key[:], registry.bootID, wire)
	if err != nil {
		return controlapp.DecodedModulesCursorV1{}, err
	}
	cursor, err := restoreModulesCursorV1(payload)
	if err != nil || cursor.BootID != registry.bootID {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	return cursor, nil
}

// IsStaleModulesCursorErrorV1 lets a transport map this package's deliberately
// narrow error identity without importing concrete registry state. It does
// not inspect token or key material and is safe on a typed-nil receiver.
func (registry *RegistryV1) IsStaleModulesCursorErrorV1(err error) bool {
	return errors.Is(err, ErrCursorStale)
}

// Close revokes the codec and zeroes its only retained key. It is idempotent;
// an already admitted call may finish before Close obtains the exclusive lock.
func (registry *RegistryV1) Close() {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.closed {
		return
	}
	clear(registry.key[:])
	registry.bootID = ""
	registry.closed = true
}

func newRegistryWithKeyV1(bootID string, key []byte) *RegistryV1 {
	if !validBootIDV1(bootID) || len(key) != KeyBytesV1 {
		return nil
	}
	registry := &RegistryV1{bootID: bootID}
	copy(registry.key[:], key)
	return registry
}

func freezeModulesCursorV1(
	cursor controlapp.DecodedModulesCursorV1,
) ([]byte, error) {
	if err := cursor.Validate(); err != nil {
		return nil, ErrCursorInvalid
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return nil, ErrCursorInvalid
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumPayloadBytesV1,
			MaxDepth: maximumPayloadDepthV1,
			MaxNodes: maximumPayloadNodesV1,
		},
	)
	clear(encoded)
	if err != nil || len(canonical) == 0 || len(canonical) > maximumPayloadBytesV1 ||
		canonical[0] != '{' {
		clear(canonical)
		return nil, ErrCursorInvalid
	}
	return canonical, nil
}

func restoreModulesCursorV1(
	payload []byte,
) (controlapp.DecodedModulesCursorV1, error) {
	if len(payload) == 0 || len(payload) > maximumPayloadBytesV1 ||
		payload[0] != '{' {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		payload,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumPayloadBytesV1,
			MaxDepth: maximumPayloadDepthV1,
			MaxNodes: maximumPayloadNodesV1,
		},
	)
	if err != nil || !bytes.Equal(canonical, payload) {
		clear(canonical)
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	clear(canonical)

	var cursor controlapp.DecodedModulesCursorV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	rebuilt, err := freezeModulesCursorV1(cursor)
	if err != nil || !bytes.Equal(rebuilt, payload) {
		clear(rebuilt)
		return controlapp.DecodedModulesCursorV1{}, ErrCursorInvalid
	}
	clear(rebuilt)
	return cursor, nil
}

func sealV1(key []byte, bootID string, payload []byte) []byte {
	wire := make(
		[]byte,
		cursorHeaderBytesV1+len(bootID)+len(payload)+cursorMACBytesV1,
	)
	copy(wire[:len(cursorMagicV1)], cursorMagicV1)
	wire[len(cursorMagicV1)] = cursorEnvelopeVersionV1
	binary.BigEndian.PutUint16(
		wire[len(cursorMagicV1)+1:len(cursorMagicV1)+3],
		uint16(len(bootID)),
	)
	binary.BigEndian.PutUint16(
		wire[len(cursorMagicV1)+3:cursorHeaderBytesV1],
		uint16(len(payload)),
	)
	copy(wire[cursorHeaderBytesV1:], bootID)
	copy(wire[cursorHeaderBytesV1+len(bootID):], payload)
	tag := authenticateV1(key, wire[:len(wire)-cursorMACBytesV1])
	copy(wire[len(wire)-cursorMACBytesV1:], tag)
	clear(tag)
	return wire
}

func openV1(key []byte, currentBootID string, wire []byte) ([]byte, error) {
	if len(wire) < cursorHeaderBytesV1+1+cursorMACBytesV1 ||
		!bytes.Equal(wire[:len(cursorMagicV1)], []byte(cursorMagicV1)) ||
		wire[len(cursorMagicV1)] != cursorEnvelopeVersionV1 {
		return nil, ErrCursorInvalid
	}
	bootBytes := int(binary.BigEndian.Uint16(
		wire[len(cursorMagicV1)+1 : len(cursorMagicV1)+3],
	))
	payloadBytes := int(binary.BigEndian.Uint16(
		wire[len(cursorMagicV1)+3 : cursorHeaderBytesV1],
	))
	if bootBytes <= 0 || bootBytes > moduleapi.MaxOpaqueIDBytes ||
		payloadBytes <= 0 || payloadBytes > maximumPayloadBytesV1 ||
		len(wire) != cursorHeaderBytesV1+bootBytes+payloadBytes+cursorMACBytesV1 {
		return nil, ErrCursorInvalid
	}
	bootID := string(wire[cursorHeaderBytesV1 : cursorHeaderBytesV1+bootBytes])
	if !validBootIDV1(bootID) {
		return nil, ErrCursorInvalid
	}
	if bootID != currentBootID {
		return nil, ErrCursorStale
	}
	signedBytes := cursorHeaderBytesV1 + bootBytes + payloadBytes
	signed := wire[:signedBytes]
	expected := authenticateV1(key, signed)
	valid := hmac.Equal(expected, wire[signedBytes:])
	clear(expected)
	if !valid {
		return nil, ErrCursorInvalid
	}
	payloadStart := cursorHeaderBytesV1 + bootBytes
	return wire[payloadStart : payloadStart+payloadBytes], nil
}

func authenticateV1(key, signed []byte) []byte {
	authenticator := hmac.New(sha256.New, key)
	_, _ = authenticator.Write([]byte(modulesCursorMACDomainV1))
	_, _ = authenticator.Write([]byte{0})
	_, _ = authenticator.Write(signed)
	return authenticator.Sum(nil)
}

func validBootIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
