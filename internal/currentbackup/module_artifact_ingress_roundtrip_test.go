package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

type moduleArtifactIngressBackupFixture struct {
	databasePath string
	artifactRoot string
	sourceRoot   string
	artifact     verifiedArtifact
	manifest     []byte
	admission    currentstore.ModuleArtifactAdmissionV1
	installation bool
}

func TestUninstalledModuleArtifactIngressBackupRoundTripIsOffline(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleArtifactIngressBackupFixture(t, false)

	// The source mapping is deliberately gone before Backup begins. An ingress
	// fact is rooted only by the server-owned artifact bytes and the Current
	// Store; Backup/Verify/Restore must not reopen a Source, Host, or network.
	if err := os.RemoveAll(fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "module-artifact-ingress.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"module-artifact-ingress-backup-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(uninstalled ingress): %v", err)
	}
	if manifest.ArtifactCount != 1 || len(manifest.Artifacts) != 1 ||
		manifest.Artifacts[0].Digest != fixture.artifact.digest {
		t.Fatalf("ingress bundle artifacts = %+v", manifest.Artifacts)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle(uninstalled ingress): %v", err)
	}

	restoreRoot, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		t.TempDir(),
		"trusted-restore-*",
	)
	if err != nil {
		t.Fatalf("provision trusted restore state: %v", err)
	}
	restoredDatabase := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle(uninstalled ingress): %v", err)
	}
	if _, err := moduleartifactstore.SelectArtifactRootV1(restoredArtifacts); err != nil {
		t.Fatalf("select restored artifact root for ingress: %v", err)
	}
	if _, err := verifyArtifactDirectoryContext(
		ctx,
		filepath.Join(restoredArtifacts, fixture.artifact.digest),
		fixture.artifact.digest,
	); err != nil {
		t.Fatalf("verify restored ingressed artifact: %v", err)
	}
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatal(err)
	}
	admission, readErr := restored.GetModuleArtifactAdmissionV1(
		ctx,
		fixture.admission.AdmissionID,
	)
	closeErr := restored.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read restored ingress admission: %v / %v", readErr, closeErr)
	}
	if admission.AdmissionID != fixture.admission.AdmissionID ||
		admission.Record.ArtifactDigest != fixture.artifact.digest ||
		!bytes.Equal(admission.Artifact.ManifestCanonical, fixture.manifest) {
		t.Fatalf("restored ingress admission = %+v", admission)
	}
	database, err := sql.Open("sqlite", restoredDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var installations, artifacts, admissions int
	if err := database.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM module_installations),
			(SELECT COUNT(*) FROM module_artifacts),
			(SELECT COUNT(*) FROM module_artifact_admissions)
	`).Scan(&installations, &artifacts, &admissions); err != nil {
		t.Fatal(err)
	}
	if installations != 0 || artifacts != 1 || admissions != 1 {
		t.Fatalf(
			"restored ingress row counts installation/artifact/admission=%d/%d/%d",
			installations,
			artifacts,
			admissions,
		)
	}
}

func TestInstalledAndIngressedArtifactIsBundledOnce(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleArtifactIngressBackupFixture(t, true)
	bundle := filepath.Join(t.TempDir(), "module-artifact-ingress-dedup.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"module-artifact-ingress-dedup-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(installed+ingressed): %v", err)
	}
	if !fixture.installation || manifest.ArtifactCount != 1 ||
		len(manifest.Artifacts) != 1 ||
		manifest.Artifacts[0].Digest != fixture.artifact.digest {
		t.Fatalf("deduplicated artifacts = %+v", manifest.Artifacts)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle(installed+ingressed): %v", err)
	}
	restoreRoot, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		t.TempDir(),
		"trusted-installed-restore-*",
	)
	if err != nil {
		t.Fatalf("provision trusted installed restore state: %v", err)
	}
	restoredDatabase := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(ctx, bundle, restoredDatabase, restoredArtifacts); err != nil {
		t.Fatalf("RestoreBundle(installed+ingressed): %v", err)
	}
	root, err := moduleartifactstore.SelectArtifactRootV1(restoredArtifacts)
	if err != nil {
		t.Fatalf("select restored installed artifact root: %v", err)
	}
	if err := moduleartifactstore.VerifyExistingRootV1(
		ctx,
		root,
		fixture.artifact.digest,
		fixture.admission.Artifact.ArtifactSizeBytes,
		fixture.admission.Artifact.CoveredFileCount,
		moduleartifactstore.ExistingModeStoreProvenInstalledV1,
	); err != nil {
		t.Fatalf("verify restored Store-proven installed artifact: %v", err)
	}
}

func TestModuleArtifactIngressCanonicalTamperBlocksBackup(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleArtifactIngressBackupFixture(t, false)
	database, err := sql.Open("sqlite", fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	forged := bytes.Replace(
		fixture.admission.Canonical,
		[]byte("packages/ingress-module"),
		[]byte("packages/ingress-tamper"),
		1,
	)
	if bytes.Equal(forged, fixture.admission.Canonical) ||
		len(forged) != len(fixture.admission.Canonical) {
		t.Fatal("failed to construct same-size closed-file ingress tamper")
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"module_artifact_admissions_reject_update"},
		`UPDATE module_artifact_admissions
		 SET admission_canonical=?
		 WHERE admission_id=?`,
		forged,
		fixture.admission.AdmissionID,
	)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		filepath.Join(t.TempDir(), "forbidden.bundle"),
		"module-artifact-ingress-tamper-test/v1",
	)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("CreateBundle(tampered ingress) error=%v, want ErrIntegrity", err)
	}
}

func TestMissingIngressArtifactBlocksBackup(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleArtifactIngressBackupFixture(t, false)
	if err := os.RemoveAll(filepath.Join(
		fixture.artifactRoot,
		fixture.artifact.digest,
	)); err != nil {
		t.Fatal(err)
	}
	_, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		filepath.Join(t.TempDir(), "forbidden.bundle"),
		"module-artifact-ingress-missing-test/v1",
	)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("CreateBundle(missing ingress) error=%v, want ErrIntegrity", err)
	}
}

func TestBackupAndRestoreRejectUnsafeStagingParentsBeforeSensitiveWrites(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleArtifactIngressBackupFixture(t, false)

	t.Run("create", func(t *testing.T) {
		unsafeParent := unsafeBackupParentV1(t)
		destination := filepath.Join(unsafeParent, "forbidden.bundle")
		if _, err := CreateBundle(
			ctx,
			fixture.databasePath,
			fixture.artifactRoot,
			destination,
			"unsafe-parent-test/v1",
		); err == nil {
			t.Fatal("CreateBundle unexpectedly staged into unsafe parent")
		}
		assertDirectoryEmptyV1(t, unsafeParent)
	})

	bundle := filepath.Join(t.TempDir(), "safe.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"unsafe-restore-parent-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle(safe): %v", err)
	}

	t.Run("artifact parent", func(t *testing.T) {
		databaseParent := t.TempDir()
		artifactParent := unsafeBackupParentV1(t)
		if err := RestoreBundle(
			ctx,
			bundle,
			filepath.Join(databaseParent, "current.sqlite"),
			filepath.Join(artifactParent, "artifacts"),
		); err == nil {
			t.Fatal("RestoreBundle unexpectedly staged below unsafe artifact parent")
		}
		assertDirectoryEmptyV1(t, databaseParent)
		assertDirectoryEmptyV1(t, artifactParent)
	})

	t.Run("database parent", func(t *testing.T) {
		databaseParent := unsafeBackupParentV1(t)
		artifactParent := t.TempDir()
		if err := RestoreBundle(
			ctx,
			bundle,
			filepath.Join(databaseParent, "current.sqlite"),
			filepath.Join(artifactParent, "artifacts"),
		); err == nil {
			t.Fatal("RestoreBundle unexpectedly staged below unsafe database parent")
		}
		assertDirectoryEmptyV1(t, databaseParent)
		assertDirectoryEmptyV1(t, artifactParent)
	})
}

func assertDirectoryEmptyV1(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsafe staging parent contains residue: %v", entries)
	}
}

func newModuleArtifactIngressBackupFixture(
	t *testing.T,
	install bool,
) moduleArtifactIngressBackupFixture {
	t.Helper()
	ctx := context.Background()
	stateRoot := t.TempDir()
	databasePath := filepath.Join(stateRoot, "current.sqlite")
	artifactRoot := filepath.Join(stateRoot, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}

	sourceRoot := filepath.Join(t.TempDir(), "source")
	sourceArtifact := filepath.Join(sourceRoot, "packages", "ingress-module")
	if err := os.MkdirAll(sourceArtifact, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := moduleArtifactIngressBackupManifest(t)
	if err := os.WriteFile(
		filepath.Join(sourceArtifact, moduleapi.ArtifactManifestPath),
		manifest,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sourceArtifact, "payload.txt"),
		[]byte("server-owned inert artifact\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	// The artifact namespace does not reserve the physical root's lease
	// basename. This file is ordinary, digest-covered module content because it
	// lives inside the digest directory; Backup must not reinterpret or skip it.
	if err := os.WriteFile(
		filepath.Join(sourceArtifact, ".freeagent-artifact-ingress.lock"),
		[]byte("ordinary artifact payload with a control-like basename\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		sourceArtifact,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyArtifactDirectory(sourceArtifact, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyVerifiedArtifact(
		ctx,
		sourceArtifact,
		filepath.Join(artifactRoot, digest),
		verified,
	); err != nil {
		t.Fatal(err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	normalizedSourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(normalizedSourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "backup.ingress",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"freeagent.test.ingress"},
			MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
			MaxPackageBytes:         1 << 20,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(
		ctx,
		currentstore.RegisterModuleSourceInput{PolicyCanonical: policyCanonical},
	); err != nil {
		t.Fatal(err)
	}
	refreshBasis, err := store.ReadModuleSourceRefreshBasis(ctx, "backup.ingress")
	if err != nil {
		t.Fatal(err)
	}
	module := moduleapi.Ref{
		ID:      "freeagent.test.ingress.backup",
		Version: "1.0.0",
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "backup.ingress",
			Entries: []moduleapi.ModuleDiscoveryEntryV1{{
				Module:            module,
				ArtifactDigest:    digest,
				ArtifactSizeBytes: uint64(verified.sizeBytes),
				PackagePath:       "packages/ingress-module",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(
		ctx,
		refreshBasis,
		indexCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	ingressBasis, err := store.ReadModuleArtifactIngressBasisV1(
		ctx,
		currentstore.ModuleArtifactIngressSelectionV1{
			SourceID:       "backup.ingress",
			SnapshotID:     snapshot.SnapshotID,
			Module:         module,
			ArtifactDigest: digest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, admission, err := store.CommitModuleArtifactIngressV1(
		ctx,
		ingressBasis,
		manifest,
		uint64(len(verified.tree.files)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if install {
		manifestRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			manifest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
			InstallationID:      "installation-ingress-backup",
			ModuleID:            module.ID,
			ExactVersion:        module.Version,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifest,
			ArtifactDigest:      digest,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return moduleArtifactIngressBackupFixture{
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		sourceRoot:   sourceRoot,
		artifact:     verified,
		manifest:     bytes.Clone(manifest),
		admission:    admission,
		installation: install,
	}
}

func moduleArtifactIngressBackupManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "freeagent.test.ingress.backup",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "freeagent.test.ingress.backup/v1",
		},
		Provides: []moduleapi.PortRef{{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, rebuilt, err := moduleapi.ParseModuleManifestV1(canonical); err != nil ||
		!bytes.Equal(rebuilt, canonical) {
		t.Fatalf("freeze ingress backup Manifest: %v", err)
	}
	return canonical
}
