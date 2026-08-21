package currentbackup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

func TestModuleDiscoveryBackupRoundTripAndSemanticTamperGate(t *testing.T) {
	ctx := context.Background()
	sourceParent := t.TempDir()
	databasePath := filepath.Join(sourceParent, "current.sqlite")
	artifactRoot := filepath.Join(sourceParent, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte("backup-roundtrip-origin"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "backup.source",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"backup"},
			MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
			MaxPackageBytes:         1 << 20,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{
		PolicyCanonical: policyCanonical,
	}); err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, "backup.source")
	if err != nil {
		t.Fatal(err)
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "backup.source",
			Entries: []moduleapi.ModuleDiscoveryEntryV1{{
				Module:            moduleapi.Ref{ID: "backup.module", Version: "opaque-v1"},
				ArtifactDigest:    strings.Repeat("a", 64),
				ArtifactSizeBytes: 256,
				PackagePath:       "backup/module-v1.zip",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(ctx, basis, indexCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "module-discovery.bundle")
	if _, err := CreateBundle(ctx, databasePath, artifactRoot, bundle, "module-discovery-test/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	restoreParent := t.TempDir()
	restoredDB := filepath.Join(restoreParent, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreParent, "restored-artifacts")
	if err := RestoreBundle(ctx, bundle, restoredDB, restoredArtifacts); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); err != nil {
		t.Fatal(err)
	}
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	restoredSource, err := restored.GetModuleSource(ctx, "backup.source")
	if err != nil {
		t.Fatal(err)
	}
	restoredSnapshot, err := restored.GetModuleDiscoverySnapshot(ctx, snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredSource.CurrentSnapshotID != snapshot.SnapshotID ||
		restoredSource.ObservationRevision != 1 ||
		restoredSnapshot.IndexID != snapshot.IndexID ||
		len(restoredSnapshot.Snapshot.Entries) != 1 {
		t.Fatalf("restored source/snapshot = %+v / %+v", restoredSource, restoredSnapshot)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}

	// A structurally valid SQLite copy with canonical parent drift must fail
	// the same offline semantic gate used by Create/Verify/Restore. No source
	// network access is possible or required for this check.
	database, err := sql.Open("sqlite", restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE module_discovery_entries
		SET package_path='backup/tampered.zip'
		WHERE snapshot_id=? AND entry_ordinal=0
	`, snapshot.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("semantic tamper error = %v", err)
	}
}

func TestModuleDiscoveryBackupPreservesSignedSnapshotAndRevokedKey(t *testing.T) {
	ctx := context.Background()
	sourceParent := t.TempDir()
	databasePath := filepath.Join(sourceParent, "current.sqlite")
	artifactRoot := filepath.Join(sourceParent, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, keyCanonical, keyID, err := moduleapi.NewModulePublisherKeyV1(moduleapi.ModulePublisherKeyV1{
		SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
		Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte("backup-signed-origin"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                "backup.signed",
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		SignatureRequired:       true,
		PublisherKeyID:          keyID,
		AllowedModuleIDPrefixes: []string{"backup"},
		MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
		MaxPackageBytes:         1 << 20,
		MaxCandidates:           8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{
		PolicyCanonical:       policyCanonical,
		PublisherKeyCanonical: keyCanonical,
	}); err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, "backup.signed")
	if err != nil {
		t.Fatal(err)
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{
		SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      "backup.signed",
		Entries: []moduleapi.ModuleDiscoveryEntryV1{{
			Module:            moduleapi.Ref{ID: "backup.signed.module", Version: "opaque-v1"},
			ArtifactDigest:    strings.Repeat("b", 64),
			ArtifactSizeBytes: 256,
			PackagePath:       "backup/signed-module-v1.zip",
			SignatureID:       strings.Repeat("c", 64),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(ctx, basis, indexCanonical)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := store.RevokeModulePublisherKey(ctx, keyID, 1)
	if err != nil || revoked.RevokedAt == nil || revoked.Revision != 2 {
		t.Fatalf("revoked key = %+v, %v", revoked, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "module-discovery-signed.bundle")
	if _, err := CreateBundle(ctx, databasePath, artifactRoot, bundle, "module-discovery-signed-test/v1"); err != nil {
		t.Fatal(err)
	}
	restoreParent := t.TempDir()
	restoredDB := filepath.Join(restoreParent, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreParent, "restored-artifacts")
	if err := RestoreBundle(ctx, bundle, restoredDB, restoredArtifacts); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); err != nil {
		t.Fatal(err)
	}
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.ReadModuleSourceRefreshBasis(ctx, "backup.signed"); !errors.Is(err, currentstore.ErrModulePublisherKeyRevoked) {
		t.Fatalf("restored revoked basis error = %v", err)
	}
	restoredRevocation, err := restored.RevokeModulePublisherKey(ctx, keyID, 1)
	if err != nil || restoredRevocation.Revision != 2 || restoredRevocation.RevokedAt == nil ||
		!restoredRevocation.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("restored revocation = %+v, %v", restoredRevocation, err)
	}
	restoredSnapshot, err := restored.GetModuleDiscoverySnapshot(ctx, snapshot.SnapshotID)
	if err != nil || restoredSnapshot.PublisherKeyID != keyID ||
		restoredSnapshot.PublisherKeyRevision != 1 || len(restoredSnapshot.Snapshot.Entries) != 1 {
		t.Fatalf("restored signed Snapshot = %+v, %v", restoredSnapshot, err)
	}
}
