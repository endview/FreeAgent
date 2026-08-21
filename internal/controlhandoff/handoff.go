package controlhandoff

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	SchemaVersionV1       = "freeagent.control-bootstrap-handoff/v1"
	DefaultFilenameV1     = "freeagent-control-bootstrap.json"
	MaximumCanonicalBytes = 4 << 10

	maximumSafeUnixMicrosecondsV1 = uint64(1<<53 - 1)
	maximumCanonicalDepthV1       = 8
	maximumCanonicalNodesV1       = 16
)

var (
	ErrInvalidInput         = errors.New("controlhandoff: invalid input")
	ErrElevatedProcess      = errors.New("controlhandoff: privileged process forbidden")
	ErrUnsafeParent         = errors.New("controlhandoff: unsafe parent directory")
	ErrTargetExists         = errors.New("controlhandoff: target already exists")
	ErrSecurityVerification = errors.New("controlhandoff: security verification failed")
	ErrIdentityChanged      = errors.New("controlhandoff: file identity changed")
)

// ConfigV1 selects exactly one delivery location. HandoffPath addresses one
// explicit leaf whose parent must already be private. RuntimeDirectory names
// one existing or newly-created private directory and uses DefaultFilenameV1.
type ConfigV1 struct {
	Origin           string
	Material         controlsession.BootstrapMaterialV1
	HandoffPath      string
	RuntimeDirectory string
}

type handoffWireV1 struct {
	SchemaVersion       string `json:"schema_version"`
	Origin              string `json:"origin"`
	Capability          string `json:"capability"`
	ExpiresAtUnixMicros uint64 `json:"expires_at_unix_micros"`
}

// HandoffV1 owns cleanup of one exact handoff leaf. Cleanup is idempotent and
// is also triggered when the caller context is cancelled or the material
// expires. A successful bootstrap exchange should call Cleanup immediately.
type HandoffV1 struct {
	path     string
	identity platformFileIdentityV1
	cancel   context.CancelFunc
	done     chan struct{}

	cleanupOnce sync.Once
	cleanupErr  error
}

// VerifyNonElevatedV1 fails closed unless the current process is demonstrably
// a non-privileged user process on the current platform.
func VerifyNonElevatedV1() error {
	return verifyNonElevatedPlatformV1()
}

// PublishV1 verifies the process and parent authority, exclusively creates a
// bounded canonical handoff, and rechecks its exact identity before return.
func PublishV1(ctx context.Context, config ConfigV1) (*HandoffV1, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrInvalidInput
	}
	if err := VerifyNonElevatedV1(); err != nil {
		return nil, err
	}
	canonical, expiry, err := canonicalHandoffV1(config)
	if err != nil {
		return nil, err
	}
	defer clear(canonical)

	path, createdDirectory, err := prepareDestinationV1(config)
	if err != nil {
		return nil, err
	}
	keepDirectory := false
	defer func() {
		if createdDirectory && !keepDirectory {
			removeEmptyDirectoryPlatformV1(filepath.Dir(path))
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, ErrInvalidInput
	}
	identity, err := writeExclusivePrivateFilePlatformV1(path, canonical)
	if err != nil {
		return nil, err
	}
	keepDirectory = true

	watchContext, cancel := context.WithCancel(ctx)
	handoff := &HandoffV1{
		path:     path,
		identity: identity,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go handoff.watch(watchContext, expiry)
	return handoff, nil
}

// Path returns the absolute path intended for the local owner process. It is
// not included in errors and must not be persisted as a Control contract.
func (handoff *HandoffV1) Path() string {
	if handoff == nil {
		return ""
	}
	return handoff.path
}

// Done closes after the first cleanup attempt, including automatic expiry or
// caller-context cancellation.
func (handoff *HandoffV1) Done() <-chan struct{} {
	if handoff == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return handoff.done
}

// Cleanup removes only the exact file identity published by this value.
// Repeated calls return the result of the first cleanup attempt.
func (handoff *HandoffV1) Cleanup() error {
	if handoff == nil {
		return nil
	}
	handoff.cleanupOnce.Do(func() {
		if handoff.cancel != nil {
			handoff.cancel()
		}
		handoff.cleanupErr = removeExactPrivateFilePlatformV1(
			handoff.path,
			handoff.identity,
		)
		close(handoff.done)
	})
	return handoff.cleanupErr
}

func (handoff *HandoffV1) watch(ctx context.Context, expiry time.Time) {
	delay := time.Until(expiry)
	if delay <= 0 {
		_ = handoff.Cleanup()
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = handoff.Cleanup()
	case <-timer.C:
		_ = handoff.Cleanup()
	case <-handoff.done:
	}
}

func canonicalHandoffV1(config ConfigV1) ([]byte, time.Time, error) {
	if canonicalOriginV1(config.Origin) == "" ||
		len(config.Material.Capability) != controlsession.CredentialBytesV1 ||
		strings.TrimSpace(config.Material.BootID) == "" ||
		config.Material.ExpiresAtUnixMicros == 0 ||
		config.Material.ExpiresAtUnixMicros > maximumSafeUnixMicrosecondsV1 {
		return nil, time.Time{}, ErrInvalidInput
	}
	expiry := time.UnixMicro(int64(config.Material.ExpiresAtUnixMicros))
	if !expiry.After(time.Now()) {
		return nil, time.Time{}, ErrInvalidInput
	}
	encodedCapability := base64.RawURLEncoding.EncodeToString(
		bytes.Clone(config.Material.Capability),
	)
	encoded, err := json.Marshal(handoffWireV1{
		SchemaVersion:       SchemaVersionV1,
		Origin:              config.Origin,
		Capability:          encodedCapability,
		ExpiresAtUnixMicros: config.Material.ExpiresAtUnixMicros,
	})
	if err != nil {
		return nil, time.Time{}, ErrInvalidInput
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumCanonicalBytes,
			MaxDepth: maximumCanonicalDepthV1,
			MaxNodes: maximumCanonicalNodesV1,
		},
	)
	clear(encoded)
	if err != nil || len(canonical) == 0 ||
		len(canonical) > MaximumCanonicalBytes || canonical[0] != '{' {
		clear(canonical)
		return nil, time.Time{}, ErrInvalidInput
	}
	return canonical, expiry, nil
}

func canonicalOriginV1(value string) string {
	if value == "" || strings.TrimSpace(value) != value ||
		strings.ContainsAny(value, "?#@") ||
		!strings.HasPrefix(value, "http://") {
		return ""
	}
	authority := strings.TrimPrefix(value, "http://")
	if strings.Contains(authority, "/") {
		return ""
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil || host != "127.0.0.1" || portText == "" {
		return ""
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return ""
	}
	canonical := "http://" + net.JoinHostPort(
		"127.0.0.1",
		strconv.FormatUint(port, 10),
	)
	if canonical != value {
		return ""
	}
	return canonical
}

func prepareDestinationV1(config ConfigV1) (string, bool, error) {
	explicit := strings.TrimSpace(config.HandoffPath)
	runtimeDirectory := strings.TrimSpace(config.RuntimeDirectory)
	if (explicit == "") == (runtimeDirectory == "") ||
		explicit != config.HandoffPath ||
		runtimeDirectory != config.RuntimeDirectory {
		return "", false, ErrInvalidInput
	}
	if explicit != "" {
		path, err := absoluteCleanLeafV1(explicit)
		if err != nil {
			return "", false, err
		}
		if err := verifyPrivateDirectoryPlatformV1(filepath.Dir(path)); err != nil {
			return "", false, err
		}
		return path, false, nil
	}
	directory, err := absoluteCleanDirectoryV1(runtimeDirectory)
	if err != nil {
		return "", false, err
	}
	created, err := ensurePrivateRuntimeDirectoryPlatformV1(directory)
	if err != nil {
		return "", false, err
	}
	return filepath.Join(directory, DefaultFilenameV1), created, nil
}

func absoluteCleanLeafV1(input string) (string, error) {
	if input == "" || strings.ContainsRune(input, '\x00') {
		return "", ErrInvalidInput
	}
	absolute, err := filepath.Abs(input)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", ErrInvalidInput
	}
	base := filepath.Base(absolute)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", ErrInvalidInput
	}
	if err := validatePlatformPathV1(absolute, false); err != nil {
		return "", err
	}
	return absolute, nil
}

func absoluteCleanDirectoryV1(input string) (string, error) {
	if input == "" || strings.ContainsRune(input, '\x00') {
		return "", ErrInvalidInput
	}
	absolute, err := filepath.Abs(input)
	if err != nil || filepath.Clean(absolute) != absolute ||
		filepath.Dir(absolute) == absolute {
		return "", ErrInvalidInput
	}
	if err := validatePlatformPathV1(absolute, true); err != nil {
		return "", err
	}
	return absolute, nil
}
