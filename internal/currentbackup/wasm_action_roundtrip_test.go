package currentbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	backupWASMActionDescriptorPath = "content/actions.json"
	backupWASMActionModulePath     = "content/action.wasm"
	backupWASMActionTenantID       = "backup-wasm-action-tenant"
	backupWASMActionInstanceID     = "backup-wasm-action-instance"
	backupWASMActionInstallationID = "installation-backup-wasm-action-1"
	backupWASMActionActivationID   = "activation-backup-wasm-action-1"
)

func TestWASMActionArtifactBackupRoundTripPreservesPrivateOrdinaryFiles(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	artifact := newBackupWASMActionArtifact(
		t,
		"freeagent.test.backup-wasm-action",
		backupWASMActionMinimalModuleV1(),
		true,
	)
	installed := installBackupWASMAction(t, fixture, artifact)

	// Source modes are deliberately broader than the portable Backup
	// contract. Create and Restore must reconstruct private ordinary files;
	// a .wasm suffix must never acquire an executable bit.
	for _, path := range []string{
		installed,
		filepath.Join(installed, "content"),
	} {
		if err := os.Chmod(path, 0o777); err != nil {
			t.Fatalf("broaden source directory mode: %v", err)
		}
	}
	for _, path := range []string{
		moduleapi.ArtifactManifestPath,
		backupWASMActionDescriptorPath,
		backupWASMActionModulePath,
	} {
		if err := os.Chmod(
			filepath.Join(installed, filepath.FromSlash(path)),
			0o777,
		); err != nil {
			t.Fatalf("broaden source file mode: %v", err)
		}
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.databasePath); err != nil {
		t.Fatalf("verify source Current Store closure: %v", err)
	}

	bundle := filepath.Join(t.TempDir(), "wasm-action.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-wasm-action-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(WASM Action): %v", err)
	}
	assertBackupContainsArtifact(t, manifest, artifact)
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(WASM Action): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified manifest digest=%s want %s",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}
	assertBackupWASMArtifactBytes(t, filepath.Join(
		bundle,
		artifactsDirectory,
		artifact.digest,
	), artifact)
	assertBackupWASMPrivateModes(
		t,
		filepath.Join(bundle, artifactsDirectory),
		artifact.digest,
	)

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle(WASM Action): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Current Store closure: %v", err)
	}
	restoredArtifact := filepath.Join(restoredArtifacts, artifact.digest)
	assertBackupWASMArtifactBytes(t, restoredArtifact, artifact)
	assertBackupWASMPrivateModes(t, restoredArtifacts, artifact.digest)
	assertRestoredBackupWASMActivation(t, restoredDatabase, artifact)

	// Compilation occurs only here, after Create, Verify, and Restore have all
	// returned. It proves the ordinary bytes still form the exact module while
	// keeping the Backup operations themselves outside the WASM Host lifecycle.
	if err := wasmaction.ValidateModuleV1(ctx, readBackupWASMFile(
		t,
		restoredArtifact,
		backupWASMActionModulePath,
	)); err != nil {
		t.Fatalf("restored exact Wasm module is invalid: %v", err)
	}
}

func TestWASMActionBackupNeverCompilesInstantiatesOrExecutesArtifact(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	compilePoison := []byte("not-a-WebAssembly-module")
	if err := wasmaction.ValidateModuleV1(ctx, compilePoison); err == nil {
		t.Fatal("compile-poison fixture unexpectedly passed Wasm validation")
	}
	artifact := newBackupWASMActionArtifact(
		t,
		"freeagent.test.backup-wasm-action-opaque",
		compilePoison,
		false,
	)
	installBackupWASMAction(t, fixture, artifact)

	bundle := filepath.Join(t.TempDir(), "wasm-action-opaque.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-wasm-action-opaque-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle compiled or interpreted WASM bytes: %v", err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle compiled or interpreted WASM bytes: %v", err)
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
		t.Fatalf("RestoreBundle compiled or interpreted WASM bytes: %v", err)
	}
	restoredModule := readBackupWASMFile(
		t,
		filepath.Join(restoredArtifacts, artifact.digest),
		backupWASMActionModulePath,
	)
	if !bytes.Equal(restoredModule, compilePoison) {
		t.Fatalf("opaque Wasm bytes changed: got %x want %x", restoredModule, compilePoison)
	}
	if err := wasmaction.ValidateModuleV1(ctx, restoredModule); err == nil {
		t.Fatal("restored compile-poison fixture unexpectedly became executable")
	}
}

func TestWASMActionBackupRejectsDescriptorModuleAndDigestTampering(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	artifact := newBackupWASMActionArtifact(
		t,
		"freeagent.test.backup-wasm-action-tamper",
		backupWASMActionMinimalModuleV1(),
		true,
	)
	installBackupWASMAction(t, fixture, artifact)
	bundle := filepath.Join(t.TempDir(), "wasm-action-source.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"currentbackup-wasm-action-tamper-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(WASM tamper source): %v", err)
	}

	for _, test := range []struct {
		name   string
		tamper func(*testing.T, string)
	}{
		{
			name: "descriptor bytes",
			tamper: func(t *testing.T, copied string) {
				flipLastByte(t, filepath.Join(
					copied,
					artifactsDirectory,
					artifact.digest,
					filepath.FromSlash(backupWASMActionDescriptorPath),
				))
			},
		},
		{
			name: "module bytes",
			tamper: func(t *testing.T, copied string) {
				flipLastByte(t, filepath.Join(
					copied,
					artifactsDirectory,
					artifact.digest,
					filepath.FromSlash(backupWASMActionModulePath),
				))
			},
		},
		{
			name: "artifact digest",
			tamper: func(t *testing.T, copied string) {
				tamperBackupWASMArtifactDigest(t, copied, manifest, artifact.digest)
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			copied := filepath.Join(t.TempDir(), "tampered.bundle")
			copyTestTree(t, bundle, copied)
			test.tamper(t, copied)
			if _, err := VerifyBundle(ctx, copied); err == nil ||
				(!errors.Is(err, ErrIntegrity) && !errors.Is(err, ErrInvalidBundle)) {
				t.Fatalf("VerifyBundle tamper error=%v; want fail-closed integrity error", err)
			}
			restoreRoot := t.TempDir()
			restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
			restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
			if err := RestoreBundle(
				ctx,
				copied,
				restoredDatabase,
				restoredArtifacts,
			); err == nil {
				t.Fatal("RestoreBundle accepted tampered WASM Action artifact")
			}
			for _, target := range []string{restoredDatabase, restoredArtifacts} {
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatalf("failed Restore published target %s: %v", target, err)
				}
			}
		})
	}
}

type backupWASMActionArtifact struct {
	directory  string
	moduleID   string
	version    string
	digest     string
	manifest   []byte
	descriptor []byte
	wasm       []byte
	sizeBytes  uint64
}

func newBackupWASMActionArtifact(
	t *testing.T,
	moduleID string,
	wasm []byte,
	requireValidModule bool,
) backupWASMActionArtifact {
	t.Helper()
	const version = "1.0.0"
	_, descriptor, err := wasmaction.NewDescriptorV1(wasmaction.DescriptorV1{
		SchemaVersion: wasmaction.DescriptorSchemaV1,
		ABIVersion:    wasmaction.ABIVersionV1,
		ModulePath:    backupWASMActionModulePath,
		Actions: []moduleapi.ActionDefinitionV1{{
			ProviderActionID:        "backup.wasm.echo",
			Description:             "Return one deterministic local value.",
			InputSchema:             json.RawMessage(`{"additionalProperties":false,"properties":{"value":{"maxLength":128,"type":"string"}},"required":["value"],"type":"object"}`),
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 256,
		}},
	})
	if err != nil {
		t.Fatalf("build WASM Action descriptor: %v", err)
	}
	ownedWASM := bytes.Clone(wasm)
	if requireValidModule {
		if err := wasmaction.ValidateModuleV1(context.Background(), ownedWASM); err != nil {
			t.Fatalf("validate exact Wasm module before Backup: %v", err)
		}
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleID,
		Version:    version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestWASM,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			Entrypoint: backupWASMActionDescriptorPath,
		},
		Provides: []moduleapi.PortRef{{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		}},
	}
	encoded, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(filepath.Join(directory, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBackupWASMFile(t, directory, moduleapi.ArtifactManifestPath, manifest)
	writeBackupWASMFile(t, directory, backupWASMActionDescriptorPath, descriptor)
	writeBackupWASMFile(t, directory, backupWASMActionModulePath, ownedWASM)
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	sizeBytes := uint64(len(manifest))
	for _, file := range files {
		sizeBytes += uint64(len(file.Content))
	}
	artifact := backupWASMActionArtifact{
		directory:  directory,
		moduleID:   moduleID,
		version:    version,
		digest:     digest,
		manifest:   bytes.Clone(manifest),
		descriptor: bytes.Clone(descriptor),
		wasm:       ownedWASM,
		sizeBytes:  sizeBytes,
	}
	if requireValidModule {
		provider := artifact.provider()
		loadedDescriptor, loadedWASM, err := wasmaction.LoadArtifactFromDirectory(
			context.Background(),
			provider,
			directory,
			sizeBytes,
		)
		if err != nil || loadedDescriptor.ModulePath != backupWASMActionModulePath ||
			!bytes.Equal(loadedWASM, ownedWASM) {
			t.Fatalf("exact WASM Action artifact preflight = %+v, %x, %v", loadedDescriptor, loadedWASM, err)
		}
	}
	return artifact
}

func (artifact backupWASMActionArtifact) provider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           artifact.moduleID,
		Version:            artifact.version,
		ArtifactDigest:     artifact.digest,
		InstanceID:         backupWASMActionInstanceID,
		ExecutionClass:     moduleapi.ExecutionWASM,
		AdapterIdentity:    wasmaction.AdapterIdentityV1,
		ActivationRevision: 1,
	}
}

func installBackupWASMAction(
	t *testing.T,
	fixture backupFixture,
	artifact backupWASMActionArtifact,
) string {
	t.Helper()
	ctx := context.Background()
	verified, err := verifyArtifactDirectory(artifact.directory, artifact.digest)
	if err != nil {
		t.Fatalf("verify WASM Action source artifact: %v", err)
	}
	installed := filepath.Join(fixture.artifactRoot, artifact.digest)
	if err := copyVerifiedArtifact(
		ctx,
		artifact.directory,
		installed,
		verified,
	); err != nil {
		t.Fatalf("stage WASM Action artifact: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatalf("open Current Store for WASM Action installation: %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		artifact.manifest,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("compute WASM Action manifest ref: %v", err)
	}
	if _, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      backupWASMActionInstallationID,
		ModuleID:            artifact.moduleID,
		ExactVersion:        artifact.version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       artifact.manifest,
		ArtifactDigest:      artifact.digest,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("install WASM Action backup fixture: %v", err)
	}
	if _, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       backupWASMActionActivationID,
		TenantID:           backupWASMActionTenantID,
		InstanceID:         backupWASMActionInstanceID,
		InstallationID:     backupWASMActionInstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionWASM,
		AdapterIdentity:    wasmaction.AdapterIdentityV1,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("activate WASM Action backup fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Current Store after WASM Action installation: %v", err)
	}
	return installed
}

func assertBackupContainsArtifact(
	t *testing.T,
	manifest Manifest,
	artifact backupWASMActionArtifact,
) {
	t.Helper()
	matches := 0
	for _, candidate := range manifest.Artifacts {
		if candidate.Digest == artifact.digest {
			matches++
			if candidate.Path != artifactsDirectory+"/"+artifact.digest ||
				candidate.SizeBytes != int64(artifact.sizeBytes) {
				t.Fatalf("WASM Action artifact manifest entry = %+v", candidate)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("WASM Action artifact entries=%d want 1", matches)
	}
}

func assertBackupWASMArtifactBytes(
	t *testing.T,
	directory string,
	artifact backupWASMActionArtifact,
) {
	t.Helper()
	verified, err := verifyArtifactDirectory(directory, artifact.digest)
	if err != nil || verified.sizeBytes != int64(artifact.sizeBytes) {
		t.Fatalf("verify WASM Action artifact = %+v, %v", verified, err)
	}
	for path, want := range map[string][]byte{
		moduleapi.ArtifactManifestPath: artifact.manifest,
		backupWASMActionDescriptorPath: artifact.descriptor,
		backupWASMActionModulePath:     artifact.wasm,
	} {
		if got := readBackupWASMFile(t, directory, path); !bytes.Equal(got, want) {
			t.Fatalf("restored %s bytes changed", path)
		}
	}
}

func assertBackupWASMPrivateModes(
	t *testing.T,
	artifactRoot string,
	digest string,
) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	artifact := filepath.Join(artifactRoot, digest)
	for _, path := range []string{
		artifactRoot,
		artifact,
		filepath.Join(artifact, "content"),
	} {
		assertBackupArtifactMode(t, path, 0o700)
	}
	for _, path := range []string{
		moduleapi.ArtifactManifestPath,
		backupWASMActionDescriptorPath,
		backupWASMActionModulePath,
	} {
		assertBackupArtifactMode(
			t,
			filepath.Join(artifact, filepath.FromSlash(path)),
			0o600,
		)
	}
}

func assertRestoredBackupWASMActivation(
	t *testing.T,
	databasePath string,
	artifact backupWASMActionArtifact,
) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, getErr := store.GetModuleActivation(
		context.Background(),
		backupWASMActionActivationID,
	)
	installation, installationErr := store.GetModuleInstallation(
		context.Background(),
		backupWASMActionInstallationID,
	)
	closeErr := store.Close()
	if getErr != nil || installationErr != nil || closeErr != nil {
		t.Fatalf(
			"read restored WASM activation=%+v installation=%+v get=%v install=%v close=%v",
			activation,
			installation,
			getErr,
			installationErr,
			closeErr,
		)
	}
	if activation.TenantID != backupWASMActionTenantID ||
		activation.InstanceID != backupWASMActionInstanceID ||
		activation.ExecutionClass != moduleapi.ExecutionWASM ||
		activation.AdapterIdentity != wasmaction.AdapterIdentityV1 ||
		activation.InstallationID != backupWASMActionInstallationID ||
		installation.ArtifactDigest != artifact.digest {
		t.Fatalf("restored WASM activation = %+v", activation)
	}
}

func tamperBackupWASMArtifactDigest(
	t *testing.T,
	bundle string,
	manifest Manifest,
	originalDigest string,
) {
	t.Helper()
	wrongDigest := strings.Repeat("f", moduleapi.SHA256HexLength)
	if wrongDigest == originalDigest {
		wrongDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	changed := cloneManifest(manifest)
	found := false
	for index := range changed.Artifacts {
		if changed.Artifacts[index].Digest == originalDigest {
			changed.Artifacts[index].Digest = wrongDigest
			changed.Artifacts[index].Path = artifactsDirectory + "/" + wrongDigest
			found = true
			break
		}
	}
	if !found {
		t.Fatal("WASM Action artifact is absent from backup manifest")
	}
	_, canonical, err := freezeManifest(changed)
	if err != nil {
		t.Fatalf("freeze digest-tampered manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, manifestName), canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(bundle, artifactsDirectory, originalDigest),
		filepath.Join(bundle, artifactsDirectory, wrongDigest),
	); err != nil {
		t.Fatal(err)
	}
}

func writeBackupWASMFile(
	t *testing.T,
	directory string,
	path string,
	content []byte,
) {
	t.Helper()
	fullPath := filepath.Join(directory, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBackupWASMFile(
	t *testing.T,
	directory string,
	path string,
) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func backupWASMActionMinimalModuleV1() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0c, 0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x02,
		0x07, 0x36, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x12, 'f', 'r', 'e', 'e', 'a', 'g', 'e', 'n', 't', '_', 'a', 'l', 'l', 'o', 'c', '_', 'v', '1', 0x00, 0x00,
		0x14, 'f', 'r', 'e', 'e', 'a', 'g', 'e', 'n', 't', '_', 'e', 'x', 'e', 'c', 'u', 't', 'e', '_', 'v', '1', 0x00, 0x01,
		0x0a, 0x0c, 0x02,
		0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
		0x04, 0x00, 0x42, 0x00, 0x0b,
	}
}
