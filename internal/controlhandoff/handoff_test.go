package controlhandoff

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPublishRuntimeDirectoryWritesExactCanonicalAndCleans(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	root := t.TempDir()
	runtimeDirectory := filepath.Join(root, "private-runtime")
	material := testBootstrapMaterialV1(time.Now().Add(2 * time.Minute))
	handoff, err := PublishV1(context.Background(), ConfigV1{
		Origin:           "http://127.0.0.1:43127",
		Material:         material,
		RuntimeDirectory: runtimeDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(runtimeDirectory, DefaultFilenameV1)
	if handoff.Path() != wantPath {
		t.Fatalf("handoff path = %q", handoff.Path())
	}
	canonical, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) == 0 || len(canonical) > MaximumCanonicalBytes {
		t.Fatalf("canonical length = %d", len(canonical))
	}
	wantCanonical := []byte(
		`{"capability":"` +
			base64.RawURLEncoding.EncodeToString(material.Capability) +
			`","expires_at_unix_micros":` +
			strconv.FormatUint(material.ExpiresAtUnixMicros, 10) +
			`,"origin":"http://127.0.0.1:43127","schema_version":"` +
			SchemaVersionV1 + `"}`,
	)
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatalf("canonical canary drifted:\n got %s\nwant %s", canonical, wantCanonical)
	}
	recanonical, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumCanonicalBytes,
			MaxDepth: maximumCanonicalDepthV1,
			MaxNodes: maximumCanonicalNodesV1,
		},
	)
	if err != nil || !bytes.Equal(canonical, recanonical) {
		t.Fatalf("handoff is not exact canonical JSON: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 4 || wire["schema_version"] != SchemaVersionV1 ||
		wire["origin"] != "http://127.0.0.1:43127" ||
		wire["capability"] != base64.RawURLEncoding.EncodeToString(material.Capability) ||
		uint64(wire["expires_at_unix_micros"].(float64)) != material.ExpiresAtUnixMicros {
		t.Fatalf("handoff wire drifted: %#v", wire)
	}
	if bytes.Contains(canonical, []byte(material.BootID)) {
		t.Fatal("handoff persisted the registry Boot ID")
	}
	if err := handoff.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := handoff.Cleanup(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	select {
	case <-handoff.Done():
	default:
		t.Fatal("Done did not close")
	}
	if _, err := os.Lstat(wantPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("handoff survived cleanup: %v", err)
	}
	if _, err := os.Stat(runtimeDirectory); err != nil {
		t.Fatalf("owned runtime directory was unexpectedly removed: %v", err)
	}
}

func TestPublishExplicitPathUsesExistingPrivateParent(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	private := filepath.Join(t.TempDir(), "private-runtime")
	seed, err := PublishV1(context.Background(), ConfigV1{
		Origin:           "http://127.0.0.1:40101",
		Material:         testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		RuntimeDirectory: private,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Cleanup(); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(private, "chosen-name.json")
	handoff, err := PublishV1(context.Background(), ConfigV1{
		Origin:      "http://127.0.0.1:40102",
		Material:    testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		HandoffPath: explicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Path() != explicit {
		t.Fatalf("explicit path = %q", handoff.Path())
	}
	if err := handoff.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRejectsExistingLeafWithoutChangingIt(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	private := testPrivateRuntimeDirectoryV1(t)
	path := filepath.Join(private, "existing.json")
	want := []byte("do-not-replace")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PublishV1(context.Background(), ConfigV1{
		Origin:      "http://127.0.0.1:40103",
		Material:    testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		HandoffPath: path,
	})
	if !errors.Is(err, ErrTargetExists) {
		t.Fatalf("existing target error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("existing target changed: bytes=%q err=%v", got, readErr)
	}
}

func TestPublishCancellationAndExpiryRemoveHandoff(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	t.Run("caller shutdown", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		handoff, err := PublishV1(ctx, ConfigV1{
			Origin:           "http://127.0.0.1:40104",
			Material:         testBootstrapMaterialV1(time.Now().Add(time.Minute)),
			RuntimeDirectory: filepath.Join(t.TempDir(), "private-runtime"),
		})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		awaitCleanupV1(t, handoff)
	})
	t.Run("expiry", func(t *testing.T) {
		handoff, err := PublishV1(context.Background(), ConfigV1{
			Origin:           "http://127.0.0.1:40105",
			Material:         testBootstrapMaterialV1(time.Now().Add(150 * time.Millisecond)),
			RuntimeDirectory: filepath.Join(t.TempDir(), "private-runtime"),
		})
		if err != nil {
			t.Fatal(err)
		}
		awaitCleanupV1(t, handoff)
	})
}

func TestPublishRejectsInvalidOriginMaterialAndLocation(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	valid := ConfigV1{
		Origin:           "http://127.0.0.1:40106",
		Material:         testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		RuntimeDirectory: filepath.Join(t.TempDir(), "private-runtime"),
	}
	tests := []struct {
		name   string
		mutate func(*ConfigV1)
	}{
		{"authority only", func(config *ConfigV1) { config.Origin = "127.0.0.1:40106" }},
		{"localhost", func(config *ConfigV1) { config.Origin = "http://localhost:40106" }},
		{"https", func(config *ConfigV1) { config.Origin = "https://127.0.0.1:40106" }},
		{"leading zero port", func(config *ConfigV1) { config.Origin = "http://127.0.0.1:040106" }},
		{"zero port", func(config *ConfigV1) { config.Origin = "http://127.0.0.1:0" }},
		{"short capability", func(config *ConfigV1) { config.Material.Capability = []byte("short") }},
		{"empty boot", func(config *ConfigV1) { config.Material.BootID = "" }},
		{"expired", func(config *ConfigV1) {
			config.Material.ExpiresAtUnixMicros = uint64(time.Now().Add(-time.Second).UnixMicro())
		}},
		{"two locations", func(config *ConfigV1) { config.HandoffPath = filepath.Join(t.TempDir(), "also") }},
		{"no location", func(config *ConfigV1) { config.RuntimeDirectory = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.Material.Capability = bytes.Clone(valid.Material.Capability)
			test.mutate(&config)
			if handoff, err := PublishV1(context.Background(), config); handoff != nil ||
				!errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid input result: handoff=%v err=%v", handoff, err)
			}
		})
	}
}

func TestPublishRejectsUnsafeParentAndSymlinkWithoutLeakingMaterial(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	material := testBootstrapMaterialV1(time.Now().Add(time.Minute))
	encodedCapability := base64.RawURLEncoding.EncodeToString(material.Capability)
	unsafeParent := t.TempDir()
	if err := os.Chmod(unsafeParent, 0o755); err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(unsafeParent, "sensitive-leaf.json")
	_, err := PublishV1(context.Background(), ConfigV1{
		Origin:      "http://127.0.0.1:40107",
		Material:    material,
		HandoffPath: unsafePath,
	})
	if err == nil {
		t.Fatal("owner-unverified explicit parent was accepted")
	}
	if strings.Contains(err.Error(), encodedCapability) || strings.Contains(err.Error(), unsafePath) {
		t.Fatalf("error leaked path or capability: %v", err)
	}
	if _, statErr := os.Lstat(unsafePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected parent gained a leaf: %v", statErr)
	}

}

func TestPublishRejectsSymlinkOrReparseParent(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	private := testPrivateRuntimeDirectoryV1(t)
	link := filepath.Join(t.TempDir(), "private-link")
	if symlinkErr := os.Symlink(private, link); symlinkErr != nil {
		t.Skipf("symlink/reparse creation unavailable: %v", symlinkErr)
	}
	_, err := PublishV1(context.Background(), ConfigV1{
		Origin:      "http://127.0.0.1:40108",
		Material:    testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		HandoffPath: filepath.Join(link, "handoff.json"),
	})
	if err == nil {
		t.Fatal("symlinked/reparse parent was accepted")
	}
}

func TestCleanupRefusesReplacementIdentity(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	handoff, err := PublishV1(context.Background(), ConfigV1{
		Origin:           "http://127.0.0.1:40109",
		Material:         testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		RuntimeDirectory: filepath.Join(t.TempDir(), "private-runtime"),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := handoff.Path()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("replacement-owned-by-test")
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handoff.Cleanup(); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("replacement cleanup error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("replacement was removed or changed: %q, %v", got, err)
	}
}

func testBootstrapMaterialV1(expiry time.Time) controlsession.BootstrapMaterialV1 {
	capability := make([]byte, controlsession.CredentialBytesV1)
	for index := range capability {
		capability[index] = byte(index + 1)
	}
	return controlsession.BootstrapMaterialV1{
		BootID:              "test-boot-identity",
		Capability:          capability,
		ExpiresAtUnixMicros: uint64(expiry.UnixMicro()),
	}
}

func testPrivateRuntimeDirectoryV1(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "private-runtime")
	handoff, err := PublishV1(context.Background(), ConfigV1{
		Origin:           "http://127.0.0.1:40200",
		Material:         testBootstrapMaterialV1(time.Now().Add(time.Minute)),
		RuntimeDirectory: directory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handoff.Cleanup(); err != nil {
		t.Fatal(err)
	}
	return directory
}

func awaitCleanupV1(t *testing.T, handoff *HandoffV1) {
	t.Helper()
	select {
	case <-handoff.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for automatic cleanup")
	}
	if _, err := os.Lstat(handoff.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("automatic cleanup left handoff: %v", err)
	}
}

func TestPlatformBuildTargetIsKnown(t *testing.T) {
	if runtime.GOOS == "" {
		t.Fatal("empty GOOS")
	}
}
