package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

func TestModuleArtifactIngressExactRetrySurvivesAdvancedSourceHead(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	basis, manifest := moduleArtifactIngressFixtureV1(t, store, "source.ingress", "example.ingress", "v1", strings.Repeat("a", 64))

	artifact, admission, err := store.CommitModuleArtifactIngressV1(ctx, basis, manifest, 2)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ArtifactDigest != basis.Entry.ArtifactDigest || artifact.Module != basis.Entry.Module ||
		artifact.ArtifactSizeBytes != basis.Entry.ArtifactSizeBytes || artifact.CoveredFileCount != 2 ||
		!bytes.Equal(artifact.ManifestCanonical, manifest) || admission.Record.PackagePath != basis.Entry.PackagePath ||
		admission.Record.EntryOrdinal != basis.EntryOrdinal || admission.Artifact.ArtifactDigest != artifact.ArtifactDigest {
		t.Fatalf("unexpected Artifact/Admission: artifact=%+v admission=%+v", artifact, admission)
	}

	refresh, err := store.ReadModuleSourceRefreshBasis(ctx, basis.Source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	next := moduleDiscoveryTestEntry("example.ingress.next", "v1", strings.Repeat("b", 64))
	if _, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, basis.Source.SourceID, basis.Entry, next)); err != nil {
		t.Fatal(err)
	}
	retryArtifact, retryAdmission, err := store.CommitModuleArtifactIngressV1(ctx, basis, bytes.Clone(manifest), 2)
	if err != nil {
		t.Fatalf("exact retry after source-head advance: %v", err)
	}
	if retryAdmission.AdmissionID != admission.AdmissionID || !retryAdmission.AdmittedAt.Equal(admission.AdmittedAt) ||
		!retryArtifact.IngressedAt.Equal(artifact.IngressedAt) {
		t.Fatalf("exact retry changed immutable result: first=%+v/%+v retry=%+v/%+v", artifact, admission, retryArtifact, retryAdmission)
	}
	selected, found, err := store.GetModuleArtifactAdmissionBySelectionV1(ctx, basis.Selection)
	if err != nil || !found || selected.AdmissionID != admission.AdmissionID ||
		!bytes.Equal(selected.Canonical, admission.Canonical) {
		t.Fatalf("GetModuleArtifactAdmissionBySelectionV1 = %+v, %t, %v", selected, found, err)
	}
	missingSelection := basis.Selection
	missingSelection.ArtifactDigest = strings.Repeat("9", 64)
	if _, found, err := store.GetModuleArtifactAdmissionBySelectionV1(ctx, missingSelection); err != nil || found {
		t.Fatalf("unseen historical selection = found %t, error %v", found, err)
	}
	got, err := store.GetModuleArtifactAdmissionV1(ctx, admission.AdmissionID)
	if err != nil || got.AdmissionID != admission.AdmissionID || !bytes.Equal(got.Canonical, admission.Canonical) {
		t.Fatalf("GetModuleArtifactAdmissionV1 = %+v, %v", got, err)
	}
	got.Canonical[0] = 'x'
	got.Artifact.ManifestCanonical[0] = 'x'
	again, err := store.GetModuleArtifactAdmissionV1(ctx, admission.AdmissionID)
	if err != nil || !bytes.Equal(again.Canonical, admission.Canonical) || !bytes.Equal(again.Artifact.ManifestCanonical, manifest) {
		t.Fatalf("returned Admission was not detached: %+v, %v", again, err)
	}
}

func TestModuleArtifactIngressInstalledEvidenceIsStoreOwned(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	basis, manifest := moduleArtifactIngressFixtureV1(
		t, store, "source.installed", "example.installed", "v1", strings.Repeat("8", 64),
	)
	installed, err := store.IsModuleArtifactInstalledV1(ctx, basis.Entry.ArtifactDigest)
	if err != nil || installed {
		t.Fatalf("pre-install evidence = %t, %v", installed, err)
	}
	manifestRef, err := ComputeContentDigest(ContentModuleManifest, moduleManifestMediaType, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstallModule(ctx, InstallModuleInput{
		InstallationID:      "installation-ingress-evidence",
		ModuleID:            basis.Entry.Module.ID,
		ExactVersion:        basis.Entry.Module.Version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       bytes.Clone(manifest),
		ArtifactDigest:      basis.Entry.ArtifactDigest,
	}); err != nil {
		t.Fatal(err)
	}
	installed, err = store.IsModuleArtifactInstalledV1(ctx, basis.Entry.ArtifactDigest)
	if err != nil || !installed {
		t.Fatalf("post-install evidence = %t, %v", installed, err)
	}
	if _, err := store.IsModuleArtifactInstalledV1(ctx, "not-a-digest"); !errors.Is(err, ErrInvalidModuleArtifactIngress) {
		t.Fatalf("invalid digest error = %v", err)
	}
}

func TestModuleArtifactIngressRejectsStaleBasisWithoutWrites(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	basis, manifest := moduleArtifactIngressFixtureV1(t, store, "source.stale", "example.stale", "v1", strings.Repeat("c", 64))
	refresh, err := store.ReadModuleSourceRefreshBasis(ctx, basis.Source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	next := moduleDiscoveryTestEntry("example.stale.next", "v1", strings.Repeat("f", 64))
	if _, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, basis.Source.SourceID, basis.Entry, next)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitModuleArtifactIngressV1(ctx, basis, manifest, 2); !errors.Is(err, ErrModuleArtifactIngressStale) {
		t.Fatalf("stale Commit error = %v", err)
	}
	assertModuleArtifactIngressCountsV1(t, store, 0, 0)
}

func TestModuleArtifactIngressRejectsConflictingArtifactTupleAtomically(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	first, manifest := moduleArtifactIngressFixtureV1(t, store, "source.conflict.a", "example.conflict", "v1", strings.Repeat("d", 64))
	if _, _, err := store.CommitModuleArtifactIngressV1(ctx, first, manifest, 2); err != nil {
		t.Fatal(err)
	}

	policy, _, _ := moduleDiscoveryTestPolicy(t, "source.conflict.b", "origin-source.conflict.b", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
		t.Fatal(err)
	}
	refresh, err := store.ReadModuleSourceRefreshBasis(ctx, "source.conflict.b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, "source.conflict.b", first.Entry)); err != nil {
		t.Fatal(err)
	}
	second, err := store.ReadModuleArtifactIngressBasisV1(ctx, ModuleArtifactIngressSelectionV1{
		SourceID: "source.conflict.b", SnapshotID: mustCurrentModuleSourceV1(t, store, "source.conflict.b").CurrentSnapshotID,
		Module: first.Entry.Module, ArtifactDigest: first.Entry.ArtifactDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	conflictingManifest := canonicalModuleManifest(t, "example.conflict", "v1", moduleapi.RuntimeModeRequestTrustedInProcess, nil)
	conflictingRef, err := ComputeContentDigest(ContentModuleManifest, moduleManifestMediaType, conflictingManifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitModuleArtifactIngressV1(ctx, second, conflictingManifest, 2); !errors.Is(err, ErrModuleArtifactIngressConflict) {
		t.Fatalf("conflicting Commit error = %v", err)
	}
	assertModuleArtifactIngressCountsV1(t, store, 1, 1)
	if _, err := store.GetContent(ctx, conflictingRef); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("conflicting Manifest content survived rollback: %v", err)
	}
}

func TestModuleArtifactIngressTablesAreAppendOnlyAndSemanticTamperFails(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	basis, manifest := moduleArtifactIngressFixtureV1(t, store, "source.tamper", "example.tamper", "v1", strings.Repeat("e", 64))
	_, admission, err := store.CommitModuleArtifactIngressV1(ctx, basis, manifest, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`UPDATE module_artifacts SET covered_file_count=3 WHERE artifact_digest='` + basis.Entry.ArtifactDigest + `'`,
		`DELETE FROM module_artifacts WHERE artifact_digest='` + basis.Entry.ArtifactDigest + `'`,
		`UPDATE module_artifact_admissions SET admitted_at=admitted_at+1 WHERE admission_id='` + admission.AdmissionID + `'`,
		`DELETE FROM module_artifact_admissions WHERE admission_id='` + admission.AdmissionID + `'`,
	} {
		if _, err := db.Exec(statement); err == nil || !strings.Contains(strings.ToLower(err.Error()), "append-only") {
			t.Fatalf("append-only statement error = %v; statement=%s", err, statement)
		}
	}
	if _, err := db.Exec(`DROP TRIGGER module_artifact_admissions_reject_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE module_artifact_admissions SET admitted_at=1 WHERE admission_id=?`, admission.AdmissionID); err != nil {
		t.Fatal(err)
	}
	if err := VerifyModuleArtifactIngressSemanticClosureV1(ctx, db); !errors.Is(err, ErrModuleArtifactIngressIntegrity) {
		t.Fatalf("semantic tamper error = %v", err)
	}
}

func TestModuleArtifactIngressSchemaQuotasAreBounded(t *testing.T) {
	t.Run("aggregate Backup-compatible bytes", func(t *testing.T) {
		db := createSchemaTestDatabase(t)
		if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 2; index++ {
			if _, err := db.Exec(`
				INSERT INTO module_artifacts(
					artifact_digest,module_id,exact_version,manifest_ref,
					artifact_size_bytes,covered_file_count,ingressed_at
				) VALUES(?,?,?,?,268435456,1,1)
			`, fmt.Sprintf("%064x", index+1), fmt.Sprintf("example.bytes.m%d", index),
				"v1", fmt.Sprintf("%064x", index+1000)); err != nil {
				t.Fatalf("seed aggregate Artifact %d: %v", index, err)
			}
		}
		if _, err := db.Exec(`
			INSERT INTO module_artifacts(
				artifact_digest,module_id,exact_version,manifest_ref,
				artifact_size_bytes,covered_file_count,ingressed_at
			) VALUES(?,?,?,?,1,1,1)
		`, strings.Repeat("f", 64), "example.bytes.overflow", "v1", strings.Repeat("e", 64)); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
			t.Fatalf("aggregate Artifact byte quota error = %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO module_artifacts(
				artifact_digest,module_id,exact_version,manifest_ref,
				artifact_size_bytes,covered_file_count,ingressed_at
			) VALUES(?,?,?,?,268435456,1,1)
			ON CONFLICT(artifact_digest) DO NOTHING
		`, fmt.Sprintf("%064x", 1), "example.bytes.m0", "v1", fmt.Sprintf("%064x", 1000)); err != nil {
			t.Fatalf("exact Artifact retry at byte quota: %v", err)
		}
	})

	db := createSchemaTestDatabase(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 256; index++ {
		digest := fmt.Sprintf("%064x", index+1)
		manifestRef := fmt.Sprintf("%064x", index+1000)
		if _, err := db.Exec(`
			INSERT INTO module_artifacts(
				artifact_digest,module_id,exact_version,manifest_ref,
				artifact_size_bytes,covered_file_count,ingressed_at
			) VALUES(?,?,?,?,1,1,1)
		`, digest, fmt.Sprintf("example.quota.m%d", index), "v1", manifestRef); err != nil {
			t.Fatalf("seed Artifact %d: %v", index, err)
		}
	}
	// An exact artifact digest remains a no-op at quota; a new digest fails.
	if _, err := db.Exec(`
		INSERT INTO module_artifacts(
			artifact_digest,module_id,exact_version,manifest_ref,
			artifact_size_bytes,covered_file_count,ingressed_at
		) VALUES(?,?,?,?,1,1,1) ON CONFLICT(artifact_digest) DO NOTHING
	`, fmt.Sprintf("%064x", 1), "example.quota.m0", "v1", fmt.Sprintf("%064x", 1000)); err != nil {
		t.Fatalf("exact Artifact retry at quota: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO module_artifacts(
			artifact_digest,module_id,exact_version,manifest_ref,
			artifact_size_bytes,covered_file_count,ingressed_at
		) VALUES(?,?,?,?,1,1,1)
	`, strings.Repeat("f", 64), "example.quota.overflow", "v1", strings.Repeat("e", 64)); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("Artifact quota error = %v", err)
	}

	if _, err := db.Exec(`DROP TRIGGER module_artifact_admissions_exact_parent_guard`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 256; index++ {
		admissionID := fmt.Sprintf("%064x", index+5000)
		canonical := []byte(fmt.Sprintf("record-%03d", index))
		if _, err := db.Exec(`
			INSERT INTO module_artifact_admissions(
				admission_id,admission_canonical,admission_size_bytes,
				source_id,source_policy_id,source_policy_revision,
				snapshot_id,observation_revision,entry_ordinal,
				artifact_digest,admitted_at
			) VALUES(?,?,?,?,?,1,?,1,0,?,1)
		`, admissionID, canonical, len(canonical), "source.quota", strings.Repeat("a", 64), strings.Repeat("b", 64), fmt.Sprintf("%064x", index+1)); err != nil {
			t.Fatalf("seed Admission %d: %v", index, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO module_artifact_admissions(
			admission_id,admission_canonical,admission_size_bytes,
			source_id,source_policy_id,source_policy_revision,
			snapshot_id,observation_revision,entry_ordinal,
			artifact_digest,admitted_at
		) VALUES(?,?,?,?,?,1,?,1,0,?,1)
	`, strings.Repeat("f", 64), []byte("overflow"), 8, "source.other", strings.Repeat("a", 64), strings.Repeat("b", 64), fmt.Sprintf("%064x", 1)); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("Admission quota error = %v", err)
	}
}

func moduleArtifactIngressFixtureV1(
	t *testing.T,
	store *Store,
	sourceID, moduleID, version, artifactDigest string,
) (ModuleArtifactIngressBasisV1, []byte) {
	t.Helper()
	ctx := context.Background()
	policy, _, _ := moduleDiscoveryTestPolicy(t, sourceID, "origin-"+sourceID, false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
		t.Fatal(err)
	}
	refresh, err := store.ReadModuleSourceRefreshBasis(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	entry := moduleDiscoveryTestEntry(moduleID, version, artifactDigest)
	snapshot, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, sourceID, entry))
	if err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleArtifactIngressBasisV1(ctx, ModuleArtifactIngressSelectionV1{
		SourceID: sourceID, SnapshotID: snapshot.SnapshotID, Module: entry.Module, ArtifactDigest: entry.ArtifactDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := canonicalModuleManifest(t, moduleID, version, moduleapi.RuntimeModeRequestDeclarative, nil)
	return basis, manifest
}

func mustCurrentModuleSourceV1(t *testing.T, store *Store, sourceID string) ModuleSource {
	t.Helper()
	source, err := store.GetModuleSource(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func assertModuleArtifactIngressCountsV1(t *testing.T, store *Store, artifacts, admissions int) {
	t.Helper()
	var gotArtifacts, gotAdmissions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM module_artifacts`).Scan(&gotArtifacts); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM module_artifact_admissions`).Scan(&gotAdmissions); err != nil {
		t.Fatal(err)
	}
	if gotArtifacts != artifacts || gotAdmissions != admissions {
		t.Fatalf("Artifact/Admission counts = %d/%d, want %d/%d", gotArtifacts, gotAdmissions, artifacts, admissions)
	}
}
