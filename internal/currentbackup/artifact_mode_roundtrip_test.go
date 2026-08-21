package currentbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRestoreBundleRebuildsExactLocalMCPExecutableModeWithoutExecution(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	local := newBackupLocalMCPArtifact(t)

	verified, err := verifyArtifactDirectory(local.directory, local.digest)
	if err != nil {
		t.Fatalf("verify LOCAL_PROCESS source artifact: %v", err)
	}
	installedDirectory := filepath.Join(fixture.artifactRoot, local.digest)
	if err := copyVerifiedArtifact(
		ctx,
		local.directory,
		installedDirectory,
		verified,
	); err != nil {
		t.Fatalf("stage LOCAL_PROCESS source artifact: %v", err)
	}
	// Deliberately give the installed source broader modes. Backup/restore must
	// never propagate these bits; it reconstructs only 0700/0600.
	for _, path := range []string{
		installedDirectory,
		filepath.Join(installedDirectory, "content"),
	} {
		if err := os.Chmod(path, 0o777); err != nil {
			t.Fatalf("broaden source directory mode: %v", err)
		}
	}
	for _, path := range []string{
		local.executable,
		"content/mcp.json",
		"content/payload.txt",
	} {
		if err := os.Chmod(
			filepath.Join(installedDirectory, filepath.FromSlash(path)),
			0o777,
		); err != nil {
			t.Fatalf("broaden source file mode: %v", err)
		}
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatalf("open Current Store for LOCAL_PROCESS installation: %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		local.manifest,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("compute LOCAL_PROCESS manifest ref: %v", err)
	}
	if _, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      "installation-backup-local-mcp-1",
		ModuleID:            local.moduleID,
		ExactVersion:        local.version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       local.manifest,
		ArtifactDigest:      local.digest,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("install LOCAL_PROCESS backup fixture: %v", err)
	}
	if _, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       "activation-backup-local-mcp-1",
		TenantID:           "backup-local-mcp-tenant",
		InstanceID:         "backup-local-mcp-instance",
		InstallationID:     "installation-backup-local-mcp-1",
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionLocalProcess,
		AdapterIdentity:    mcpstdio.AdapterIdentityV1,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("activate LOCAL_PROCESS backup fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Current Store after LOCAL_PROCESS installation: %v", err)
	}

	bundle := filepath.Join(t.TempDir(), "local-mcp.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-local-mcp-mode-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(LOCAL_PROCESS): %v", err)
	}
	if manifest.ArtifactCount < 1 {
		t.Fatalf("LOCAL_PROCESS bundle has no artifacts: %+v", manifest)
	}
	if _, err := os.Lstat(filepath.Join(
		bundle,
		artifactsDirectory,
		local.digest,
		"content",
		"started.marker",
	)); !os.IsNotExist(err) {
		t.Fatalf("backup unexpectedly executed LOCAL_PROCESS module: %v", err)
	}
	if runtime.GOOS != "windows" {
		assertBackupArtifactMode(
			t,
			filepath.Join(
				bundle,
				artifactsDirectory,
				local.digest,
				filepath.FromSlash(local.executable),
			),
			0o600,
		)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle(LOCAL_PROCESS): %v", err)
	}
	restoredArtifact := filepath.Join(restoredArtifacts, local.digest)
	if _, err := os.Lstat(filepath.Join(
		restoredArtifact,
		"content",
		"started.marker",
	)); !os.IsNotExist(err) {
		t.Fatalf("restore unexpectedly executed LOCAL_PROCESS module: %v", err)
	}
	if _, err := verifyArtifactDirectory(restoredArtifact, local.digest); err != nil {
		t.Fatalf("restored LOCAL_PROCESS artifact identity changed: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, path := range []string{
		restoredArtifact,
		filepath.Join(restoredArtifact, "content"),
	} {
		assertBackupArtifactMode(t, path, 0o700)
	}
	assertBackupArtifactMode(
		t,
		filepath.Join(restoredArtifact, filepath.FromSlash(local.executable)),
		0o700,
	)
	for _, path := range []string{
		moduleapi.ArtifactManifestPath,
		"content/mcp.json",
		"content/payload.txt",
	} {
		assertBackupArtifactMode(
			t,
			filepath.Join(restoredArtifact, filepath.FromSlash(path)),
			0o600,
		)
	}
}

func TestNormalizeRestoredIngressOnlyLocalMCPKeepsEveryFileInert(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable Unix permission assertion")
	}
	ctx := context.Background()
	local := newBackupLocalMCPArtifact(t)
	verified, err := verifyArtifactDirectory(local.directory, local.digest)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), local.digest)
	if err := copyVerifiedArtifact(ctx, local.directory, target, verified); err != nil {
		t.Fatal(err)
	}
	// Simulate a bundle/source mode that once made the descriptor-owned helper
	// executable. An ingress-only Store fact is not an Installation and restore
	// must remove even this otherwise valid LOCAL_PROCESS executable bit.
	executable := filepath.Join(target, filepath.FromSlash(local.executable))
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRestoredArtifactModes(ctx, target, verified, false); err != nil {
		t.Fatalf("normalize ingress-only LOCAL_PROCESS artifact: %v", err)
	}
	assertBackupArtifactMode(t, target, 0o700)
	assertBackupArtifactMode(t, executable, 0o600)
	for _, path := range []string{
		moduleapi.ArtifactManifestPath,
		"content/mcp.json",
		"content/payload.txt",
	} {
		assertBackupArtifactMode(
			t,
			filepath.Join(target, filepath.FromSlash(path)),
			0o600,
		)
	}
}

type backupLocalMCPArtifact struct {
	directory  string
	moduleID   string
	version    string
	digest     string
	manifest   []byte
	executable string
}

func newBackupLocalMCPArtifact(t *testing.T) backupLocalMCPArtifact {
	t.Helper()
	const (
		moduleID   = "freeagent.test.backup-local-mcp"
		version    = "1.0.0"
		executable = "content/mcp-helper.sh"
	)
	directory := filepath.Join(t.TempDir(), "artifact")
	content := filepath.Join(directory, "content")
	if err := os.MkdirAll(content, 0o700); err != nil {
		t.Fatal(err)
	}
	executableBytes := []byte("#!/bin/sh\n: > started.marker\nexit 97\n")
	if err := os.WriteFile(
		filepath.Join(directory, filepath.FromSlash(executable)),
		executableBytes,
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	executableDigest := sha256.Sum256(executableBytes)
	descriptorRaw, err := json.Marshal(mcpstdio.HostDescriptorV1{
		SchemaVersion:    mcpstdio.HostDescriptorSchemaV1,
		ProtocolVersion:  mcpstdio.ProtocolVersionV1,
		Executable:       executable,
		ExecutableSHA256: hex.EncodeToString(executableDigest[:]),
		WorkingDirectory: "content",
		Tools: []mcpstdio.ToolBindingV1{{
			ProviderActionID:        "backup.mode.probe",
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 256,
		}},
		StartupTimeoutMillis: 1000,
		CallTimeoutMillis:    1000,
		CloseTimeoutMillis:   1000,
		MaxFrameBytes:        64 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptorCanonical, err := moduleapi.CanonicalJSON(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(content, "mcp.json"),
		descriptorCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(content, "payload.txt"),
		[]byte("ordinary non-executable payload\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifestRaw := []byte(
		`{"api_version":"freeagent.module/v1","id":"` + moduleID +
			`","provides":[{"exact_version":"v1","name":"action.provider"}],` +
			`"runtime":{"entrypoint":"content/mcp.json","mode":"LOCAL_PROCESS",` +
			`"protocol":"mcp-stdio/2025-11-25"},"version":"` + version + `"}`,
	)
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, manifestCanonical, err = moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           moduleID,
		Version:            version,
		ArtifactDigest:     digest,
		InstanceID:         "backup-local-mcp-instance",
		ExecutionClass:     moduleapi.ExecutionLocalProcess,
		AdapterIdentity:    mcpstdio.AdapterIdentityV1,
		ActivationRevision: 1,
	}
	if _, err := mcpstdio.NewContext(
		context.Background(),
		provider,
		directory,
		"content/mcp.json",
		descriptorCanonical,
	); err != nil {
		t.Fatalf("LOCAL_PROCESS backup fixture is not exact MCP: %v", err)
	}
	return backupLocalMCPArtifact{
		directory:  directory,
		moduleID:   moduleID,
		version:    version,
		digest:     digest,
		manifest:   manifestCanonical,
		executable: executable,
	}
}

func assertBackupArtifactMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect restored artifact mode %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("restored artifact mode %s=%#o want %#o", path, got, want)
	}
}
