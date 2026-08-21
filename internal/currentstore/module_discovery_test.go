package currentstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

func TestModuleDiscoveryStoreRefreshExactRetryConflictAndLists(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	policyA, _, originA := moduleDiscoveryTestPolicy(t, "source.a", "origin-a", false, "")
	sourceA, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyA})
	if err != nil {
		t.Fatal(err)
	}
	if sourceA.PolicyRevision != 1 || sourceA.ObservationRevision != 0 {
		t.Fatalf("source A = %+v", sourceA)
	}
	// SourceID is a durable review-suppression boundary. An exact retry may
	// reuse it, but neither Kind nor OriginDigest may ever change.
	policyOtherOrigin, _, _ := moduleDiscoveryTestPolicy(t, "source.a", "origin-other", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyOtherOrigin, ExpectedPolicyRevision: 1}); !errors.Is(err, ErrModuleSourceConflict) {
		t.Fatalf("rebind SourceID error = %v", err)
	}
	if originA == "" {
		t.Fatal("empty origin digest")
	}

	basis, err := store.ReadModuleSourceRefreshBasis(ctx, "source.a")
	if err != nil {
		t.Fatal(err)
	}
	entryA := moduleDiscoveryTestEntry("example.module", "opaque-a", strings.Repeat("a", 64))
	indexA := moduleDiscoveryTestIndex(t, "source.a", entryA)
	snapshotA, err := store.CommitModuleSourceRefresh(ctx, basis, indexA)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.CommitModuleSourceRefresh(ctx, basis, bytes.Clone(indexA))
	if err != nil {
		t.Fatal(err)
	}
	if retry.SnapshotID != snapshotA.SnapshotID || retry.ObservationRevision != 1 || !retry.ObservedAt.Equal(snapshotA.ObservedAt) {
		t.Fatalf("exact retry changed immutable observation: before=%+v after=%+v", snapshotA, retry)
	}

	basis, err = store.ReadModuleSourceRefreshBasis(ctx, "source.a")
	if err != nil {
		t.Fatal(err)
	}
	entryB := moduleDiscoveryTestEntry("example.module", "opaque-b", strings.Repeat("b", 64))
	indexB := moduleDiscoveryTestIndex(t, "source.a", entryA, entryB)
	snapshotB, err := store.CommitModuleSourceRefresh(ctx, basis, indexB)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotB.ObservationRevision != 2 {
		t.Fatalf("B observation revision = %d", snapshotB.ObservationRevision)
	}
	// A -> B -> A is not an exact retry of Current. It is an attempted
	// observation rollback and must not move the source head backwards.
	if _, err := store.CommitModuleSourceRefresh(ctx, basis, indexA); !errors.Is(err, ErrModuleSourceConflict) {
		t.Fatalf("A->B->A error = %v", err)
	}

	policyB, _, _ := moduleDiscoveryTestPolicy(t, "source.b", "origin-b", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyB}); err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListModuleSources(ctx)
	if err != nil || len(sources) != 2 || sources[0].SourceID != "source.a" || sources[1].SourceID != "source.b" {
		t.Fatalf("ListModuleSources = %+v, %v", sources, err)
	}
	snapshots, err := store.ListModuleDiscoverySnapshots(ctx, "source.a")
	if err != nil || len(snapshots) != 2 || snapshots[0].SnapshotID != snapshotA.SnapshotID || snapshots[1].SnapshotID != snapshotB.SnapshotID {
		t.Fatalf("ListModuleDiscoverySnapshots = %+v, %v", snapshots, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyModuleDiscoveryPath(t, path); err != nil {
		t.Fatal(err)
	}
}

func TestModuleDiscoveryRefreshRechecksPolicyAndGlobalModuleRefAtomically(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	policyA, policyValue, _ := moduleDiscoveryTestPolicy(t, "source.a", "origin-a", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyA}); err != nil {
		t.Fatal(err)
	}
	stale, err := store.ReadModuleSourceRefreshBasis(ctx, "source.a")
	if err != nil {
		t.Fatal(err)
	}
	policyValue.MaxCandidates--
	_, changedPolicy, _, err := moduleapi.NewModuleSourcePolicyV1(policyValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: changedPolicy, ExpectedPolicyRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModuleSourceRefresh(ctx, stale, moduleDiscoveryTestIndex(t, "source.a", moduleDiscoveryTestEntry("same.module", "v1", strings.Repeat("a", 64)))); !errors.Is(err, ErrModuleSourceStale) {
		t.Fatalf("stale post-I/O basis error = %v", err)
	}

	basisA, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.a")
	entryA := moduleDiscoveryTestEntry("same.module", "v1", strings.Repeat("a", 64))
	if _, err := store.CommitModuleSourceRefresh(ctx, basisA, moduleDiscoveryTestIndex(t, "source.a", entryA)); err != nil {
		t.Fatal(err)
	}
	policyB, _, _ := moduleDiscoveryTestPolicy(t, "source.b", "origin-b", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyB}); err != nil {
		t.Fatal(err)
	}
	basisB, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.b")
	conflicting := moduleDiscoveryTestEntry("same.module", "v1", strings.Repeat("b", 64))
	if _, err := store.CommitModuleSourceRefresh(ctx, basisB, moduleDiscoveryTestIndex(t, "source.b", conflicting)); !errors.Is(err, ErrModuleSourceConflict) {
		t.Fatalf("cross-source conflict error = %v", err)
	}
	if got, _ := store.GetModuleSource(ctx, "source.b"); got.ObservationRevision != 0 || got.CurrentSnapshotID != "" {
		t.Fatalf("conflict partially wrote source B: %+v", got)
	}
	if snapshots, _ := store.ListModuleDiscoverySnapshots(ctx, "source.b"); len(snapshots) != 0 {
		t.Fatalf("conflict left %d snapshots", len(snapshots))
	}
	// The global identity allows observation by multiple independent sources
	// only when the exact ArtifactDigest agrees.
	matching := moduleDiscoveryTestEntry("same.module", "v1", strings.Repeat("a", 64))
	if _, err := store.CommitModuleSourceRefresh(ctx, basisB, moduleDiscoveryTestIndex(t, "source.b", matching)); err != nil {
		t.Fatalf("same ModuleRef/digest across sources: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyModuleDiscoveryPath(t, path); err != nil {
		t.Fatal(err)
	}
}

func TestModuleDiscoveryNewObservationRequiresExactPreIOHead(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	policy, _, _ := moduleDiscoveryTestPolicy(t, "source.concurrent", "origin-concurrent", false, "")
	if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
		t.Fatal(err)
	}
	// Both refresh workers captured the same pre-I/O Store head. Once one
	// commits, the other may exact-retry that same content, but cannot publish
	// different (possibly older/slower) content from its stale basis.
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, "source.concurrent")
	if err != nil {
		t.Fatal(err)
	}
	firstIndex := moduleDiscoveryTestIndex(t, basis.Source.SourceID,
		moduleDiscoveryTestEntry("example.concurrent.a", "v1", strings.Repeat("a", 64)))
	if _, err := store.CommitModuleSourceRefresh(ctx, basis, firstIndex); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModuleSourceRefresh(ctx, basis, bytes.Clone(firstIndex)); err != nil {
		t.Fatalf("same-content stale-basis exact retry: %v", err)
	}
	secondIndex := moduleDiscoveryTestIndex(t, basis.Source.SourceID,
		moduleDiscoveryTestEntry("example.concurrent.b", "v1", strings.Repeat("b", 64)))
	if _, err := store.CommitModuleSourceRefresh(ctx, basis, secondIndex); !errors.Is(err, ErrModuleSourceStale) {
		t.Fatalf("different-content stale-basis error = %v", err)
	}
	source, err := store.GetModuleSource(ctx, basis.Source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.ObservationRevision != 1 {
		t.Fatalf("stale refresh advanced observation to %d", source.ObservationRevision)
	}
}

func TestModuleDiscoveryConcurrentRefreshFromSameBasisCommitsExactlyOnce(t *testing.T) {
	store, _ := newModuleDiscoveryTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	policy, _, _ := moduleDiscoveryTestPolicy(
		t,
		"source.concurrent.real",
		"origin-concurrent-real",
		false,
		"",
	)
	if _, err := store.RegisterModuleSource(
		ctx,
		RegisterModuleSourceInput{PolicyCanonical: policy},
	); err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleSourceRefreshBasis(
		ctx,
		"source.concurrent.real",
	)
	if err != nil {
		t.Fatal(err)
	}
	indexes := [][]byte{
		moduleDiscoveryTestIndex(
			t,
			basis.Source.SourceID,
			moduleDiscoveryTestEntry(
				"example.concurrent.real.a",
				"v1",
				strings.Repeat("a", 64),
			),
		),
		moduleDiscoveryTestIndex(
			t,
			basis.Source.SourceID,
			moduleDiscoveryTestEntry(
				"example.concurrent.real.b",
				"v1",
				strings.Repeat("b", 64),
			),
		),
	}
	start := make(chan struct{})
	errorsSeen := make(chan error, len(indexes))
	var wait sync.WaitGroup
	for _, indexCanonical := range indexes {
		indexCanonical := bytes.Clone(indexCanonical)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.CommitModuleSourceRefresh(
				ctx,
				basis,
				indexCanonical,
			)
			errorsSeen <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	successes, stale := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrModuleSourceStale):
			stale++
		default:
			t.Fatalf("concurrent refresh error = %v", err)
		}
	}
	if successes != 1 || stale != 1 {
		t.Fatalf(
			"concurrent refresh successes=%d stale=%d, want 1/1",
			successes,
			stale,
		)
	}
	source, err := store.GetModuleSource(ctx, basis.Source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.ObservationRevision != 1 || source.CurrentSnapshotID == "" {
		t.Fatalf("concurrent refresh source = %+v", source)
	}
	snapshots, err := store.ListModuleDiscoverySnapshots(
		ctx,
		basis.Source.SourceID,
	)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("concurrent refresh snapshots = %+v, %v", snapshots, err)
	}
}

func TestModuleDiscoveryOperationsDoNotWriteRuntimeOrEffectTables(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	baseline := moduleDiscoveryNonDiscoveryCounts(t, path)
	keyCanonical, keyID := moduleDiscoveryTestPublisherKey(t)
	policy, _, _ := moduleDiscoveryTestPolicy(
		t,
		"source.zero.runtime",
		"origin-zero-runtime",
		true,
		keyID,
	)
	source, err := store.RegisterModuleSource(
		ctx,
		RegisterModuleSourceInput{
			PolicyCanonical:       policy,
			PublisherKeyCanonical: keyCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	moduleDiscoveryAssertNonDiscoveryCounts(t, path, baseline, "register")
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	entry := moduleDiscoveryTestEntry(
		"example.zero.runtime",
		"v1",
		strings.Repeat("a", 64),
	)
	entry.SignatureID = strings.Repeat("b", 64)
	if _, err := store.CommitModuleSourceRefresh(
		ctx,
		basis,
		moduleDiscoveryTestIndex(t, source.SourceID, entry),
	); err != nil {
		t.Fatal(err)
	}
	moduleDiscoveryAssertNonDiscoveryCounts(t, path, baseline, "refresh")
	if _, err := store.RevokeModulePublisherKey(ctx, keyID, 1); err != nil {
		t.Fatal(err)
	}
	moduleDiscoveryAssertNonDiscoveryCounts(t, path, baseline, "revoke")
}

func TestModuleDiscoveryAndInstallationGlobalIdentityIsSymmetric(t *testing.T) {
	ctx := context.Background()
	t.Run("Installation then Discovery", func(t *testing.T) {
		store, _ := newModuleDiscoveryTestStore(t)
		manifest := canonicalModuleManifest(t, "example.install.first", "v1", moduleapi.RuntimeModeRequestDeclarative, nil)
		if _, err := store.InstallModule(ctx, installInput(t, "install-first", manifest, strings.Repeat("a", 64))); err != nil {
			t.Fatal(err)
		}
		policy, _, _ := moduleDiscoveryTestPolicy(t, "source.install.first", "origin-install-first", false, "")
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
			t.Fatal(err)
		}
		basis, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.install.first")
		conflict := moduleDiscoveryTestEntry("example.install.first", "v1", strings.Repeat("b", 64))
		if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, basis.Source.SourceID, conflict)); !errors.Is(err, ErrModuleSourceConflict) {
			t.Fatalf("Installation->Discovery conflict error = %v", err)
		}
		matching := conflict
		matching.ArtifactDigest = strings.Repeat("a", 64)
		if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, basis.Source.SourceID, matching)); err != nil {
			t.Fatalf("Installation->Discovery matching digest: %v", err)
		}
	})

	t.Run("Discovery then Installation", func(t *testing.T) {
		store, _ := newModuleDiscoveryTestStore(t)
		policy, _, _ := moduleDiscoveryTestPolicy(t, "source.discovery.first", "origin-discovery-first", false, "")
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
			t.Fatal(err)
		}
		basis, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.discovery.first")
		entry := moduleDiscoveryTestEntry("example.discovery.first", "v1", strings.Repeat("a", 64))
		if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, basis.Source.SourceID, entry)); err != nil {
			t.Fatal(err)
		}
		manifest := canonicalModuleManifest(t, entry.Module.ID, entry.Module.Version, moduleapi.RuntimeModeRequestDeclarative, nil)
		if _, err := store.InstallModule(ctx, installInput(t, "install-conflict", manifest, strings.Repeat("b", 64))); !errors.Is(err, ErrModuleSourceConflict) {
			t.Fatalf("Discovery->Installation conflict error = %v", err)
		}
		if _, err := store.InstallModule(ctx, installInput(t, "install-matching", manifest, strings.Repeat("a", 64))); err != nil {
			t.Fatalf("Discovery->Installation matching digest: %v", err)
		}
	})
}

func TestModuleDiscoveryCommitSemanticFailureRollsBackWholeObservation(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	policyA, _, _ := moduleDiscoveryTestPolicy(t, "source.a", "origin-a", false, "")
	policyB, _, _ := moduleDiscoveryTestPolicy(t, "source.b", "origin-b", false, "")
	for _, policy := range [][]byte{policyA, policyB} {
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
			t.Fatal(err)
		}
	}
	basisA, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.a")
	if _, err := store.CommitModuleSourceRefresh(ctx, basisA, moduleDiscoveryTestIndex(t, "source.a", moduleDiscoveryTestEntry("example.a", "v1", strings.Repeat("a", 64)))); err != nil {
		t.Fatal(err)
	}
	// Inject a projection drift outside Store APIs. Commit B must detect this
	// in its pre-COMMIT full closure and roll every B write back.
	external, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.Exec(`UPDATE module_discovery_entries SET package_path='tampered.zip' WHERE module_id='example.a'`); err != nil {
		t.Fatal(err)
	}
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}
	basisB, _ := store.ReadModuleSourceRefreshBasis(ctx, "source.b")
	_, err = store.CommitModuleSourceRefresh(ctx, basisB, moduleDiscoveryTestIndex(t, "source.b", moduleDiscoveryTestEntry("example.b", "v1", strings.Repeat("b", 64))))
	if !errors.Is(err, ErrModuleDiscoveryIntegrity) {
		t.Fatalf("semantic failure injection error = %v", err)
	}
	var count int
	external, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := external.QueryRow(`SELECT COUNT(*) FROM module_discovery_snapshots WHERE source_id='source.b'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("semantic failure left %d source B Snapshots", count)
	}
}

func TestModuleDiscoverySemanticClosureRejectsMissingCurrentHead(t *testing.T) {
	t.Run("tampered refreshed head", func(t *testing.T) {
		store, path := newModuleDiscoveryTestStore(t)
		ctx := context.Background()
		policy, _, _ := moduleDiscoveryTestPolicy(t, "source.head", "origin-head", false, "")
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy}); err != nil {
			t.Fatal(err)
		}
		basis, err := store.ReadModuleSourceRefreshBasis(ctx, "source.head")
		if err != nil {
			t.Fatal(err)
		}
		entry := moduleDiscoveryTestEntry("example.head", "v1", strings.Repeat("a", 64))
		if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, "source.head", entry)); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE module_sources SET current_snapshot_id=NULL WHERE source_id='source.head'`); err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if err := verifyModuleDiscoveryPath(t, path); !errors.Is(err, ErrModuleDiscoveryIntegrity) {
			t.Fatalf("cleared current head error = %v", err)
		}
	})

	t.Run("policy update awaiting refresh", func(t *testing.T) {
		store, path := newModuleDiscoveryTestStore(t)
		ctx := context.Background()
		policyCanonical, policy, _ := moduleDiscoveryTestPolicy(t, "source.policy", "origin-policy", false, "")
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyCanonical}); err != nil {
			t.Fatal(err)
		}
		basis, err := store.ReadModuleSourceRefreshBasis(ctx, "source.policy")
		if err != nil {
			t.Fatal(err)
		}
		entry := moduleDiscoveryTestEntry("example.policy", "v1", strings.Repeat("a", 64))
		if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, "source.policy", entry)); err != nil {
			t.Fatal(err)
		}
		policy.MaxCandidates--
		_, changedPolicy, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{
			PolicyCanonical:        changedPolicy,
			ExpectedPolicyRevision: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated.PolicyRevision != 2 || updated.CurrentSnapshotID != "" {
			t.Fatalf("updated Source = %+v", updated)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := verifyModuleDiscoveryPath(t, path); err != nil {
			t.Fatalf("policy update without new observation: %v", err)
		}
	})
}

func TestModuleDiscoverySemanticClosureRejectsBackwardPolicyRevisionHistory(
	t *testing.T,
) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	policyCanonical, policy, _ := moduleDiscoveryTestPolicy(
		t,
		"source.policy.history",
		"origin-policy-history",
		false,
		"",
	)
	if _, err := store.RegisterModuleSource(
		ctx,
		RegisterModuleSourceInput{PolicyCanonical: policyCanonical},
	); err != nil {
		t.Fatal(err)
	}
	entry := moduleDiscoveryTestEntry(
		"example.policy.history",
		"v1",
		strings.Repeat("a", 64),
	)
	snapshotIDs := make([]string, 0, 3)
	for revision := uint64(1); revision <= 3; revision++ {
		if revision > 1 {
			policy.MaxCandidates--
			_, nextPolicyCanonical, _, err :=
				moduleapi.NewModuleSourcePolicyV1(policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.RegisterModuleSource(
				ctx,
				RegisterModuleSourceInput{
					PolicyCanonical:        nextPolicyCanonical,
					ExpectedPolicyRevision: revision - 1,
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		basis, err := store.ReadModuleSourceRefreshBasis(
			ctx,
			"source.policy.history",
		)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.CommitModuleSourceRefresh(
			ctx,
			basis,
			moduleDiscoveryTestIndex(t, basis.Source.SourceID, entry),
		)
		if err != nil {
			t.Fatal(err)
		}
		snapshotIDs = append(snapshotIDs, snapshot.SnapshotID)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Reorder the immutable observations from policy revisions 1,2,3 to
	// 3,2,1 while retaining contiguous observation revisions and a matching
	// physical head pointer. Structural/FK checks alone accept this drift; the
	// semantic gate must reject its backwards policy chronology.
	if _, err := transaction.Exec(
		`UPDATE module_discovery_snapshots SET observation_revision=99 WHERE snapshot_id=?`,
		snapshotIDs[0],
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Exec(
		`UPDATE module_discovery_snapshots SET observation_revision=1 WHERE snapshot_id=?`,
		snapshotIDs[2],
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Exec(
		`UPDATE module_discovery_snapshots SET observation_revision=3 WHERE snapshot_id=?`,
		snapshotIDs[0],
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Exec(
		`UPDATE module_sources SET current_snapshot_id=? WHERE source_id='source.policy.history'`,
		snapshotIDs[0],
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	err = verifyModuleDiscoveryPath(t, path)
	if !errors.Is(err, ErrModuleDiscoveryIntegrity) ||
		!strings.Contains(err.Error(), "policy revisions move backwards") {
		t.Fatalf("backwards policy chronology error = %v", err)
	}
}

func TestModuleDiscoveryPublisherKeyRevocationAndTamperFailClosed(t *testing.T) {
	store, path := newModuleDiscoveryTestStore(t)
	ctx := context.Background()
	keyCanonical, keyID := moduleDiscoveryTestPublisherKey(t)
	policy, _, _ := moduleDiscoveryTestPolicy(t, "signed.source", "signed-origin", true, keyID)
	source, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policy, PublisherKeyCanonical: keyCanonical})
	if err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleSourceRefreshBasis(ctx, source.SourceID)
	if err != nil || basis.PublisherKey == nil || basis.PublisherKey.Revision != 1 {
		t.Fatalf("basis = %+v, %v", basis, err)
	}
	revoked, err := store.RevokeModulePublisherKey(ctx, keyID, 1)
	if err != nil || revoked.Revision != 2 || revoked.RevokedAt == nil {
		t.Fatalf("revoked = %+v, %v", revoked, err)
	}
	retry, err := store.RevokeModulePublisherKey(ctx, keyID, 1)
	if err != nil || retry.Revision != 2 || !retry.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("revocation exact retry = %+v, %v", retry, err)
	}
	if _, err := store.ReadModuleSourceRefreshBasis(ctx, source.SourceID); !errors.Is(err, ErrModulePublisherKeyRevoked) {
		t.Fatalf("read revoked basis error = %v", err)
	}
	entry := moduleDiscoveryTestEntry("signed.module", "v1", strings.Repeat("a", 64))
	entry.SignatureID = strings.Repeat("c", 64)
	if _, err := store.CommitModuleSourceRefresh(ctx, basis, moduleDiscoveryTestIndex(t, source.SourceID, entry)); !errors.Is(err, ErrModulePublisherKeyRevoked) {
		t.Fatalf("commit after revocation error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := moduleDiscoveryTestPublisherKey(t)
	if _, err := db.Exec(`UPDATE module_publisher_keys SET key_canonical=? WHERE publisher_key_id=?`, otherKey, keyID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenExistingCurrentStore(ctx, path)
	if !errors.Is(err, ErrLoopIntegrity) {
		if err == nil {
			_ = store.Close()
		}
		t.Fatalf("tampered key OpenExisting error = %v, want ErrLoopIntegrity", err)
	}
}

func newModuleDiscoveryTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func moduleDiscoveryTestPolicy(t *testing.T, sourceID, origin string, signed bool, keyID string) ([]byte, moduleapi.ModuleSourcePolicyV1, string) {
	t.Helper()
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(moduleapi.ModuleSourceKindLocalDirectoryV1, []byte(origin))
	if err != nil {
		t.Fatal(err)
	}
	policy := moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                sourceID,
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		SignatureRequired:       signed,
		PublisherKeyID:          keyID,
		AllowedModuleIDPrefixes: []string{"example", "same", "signed"},
		MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
		MaxPackageBytes:         1 << 20,
		MaxCandidates:           16,
	}
	frozen, canonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, frozen, originDigest
}

func moduleDiscoveryTestPublisherKey(t *testing.T) ([]byte, string) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, keyID, err := moduleapi.NewModulePublisherKeyV1(moduleapi.ModulePublisherKeyV1{
		SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
		Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(public),
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical, keyID
}

func moduleDiscoveryTestEntry(id, version, digest string) moduleapi.ModuleDiscoveryEntryV1 {
	return moduleapi.ModuleDiscoveryEntryV1{
		Module:            moduleapi.Ref{ID: id, Version: version},
		ArtifactDigest:    digest,
		ArtifactSizeBytes: 128,
		PackagePath:       id + "/" + version + ".zip",
	}
}

func moduleDiscoveryTestIndex(t *testing.T, sourceID string, entries ...moduleapi.ModuleDiscoveryEntryV1) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{
		SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      sourceID,
		Entries:       entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func verifyModuleDiscoveryPath(t *testing.T, path string) error {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	connection, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN`); err != nil {
		return err
	}
	err = VerifyModuleDiscoverySemanticClosureV1(context.Background(), connection)
	_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
	return errors.Join(err, rollbackErr)
}

var moduleDiscoveryNonDiscoveryTables = []string{
	"module_installations",
	"module_activations",
	"runtime_catalog_generations",
	"control_snapshots",
	"control_current",
	"runs",
	"model_dispatch_attempts",
	"model_usage",
	"dispatch_attempts",
}

func moduleDiscoveryNonDiscoveryCounts(
	t *testing.T,
	path string,
) map[string]int64 {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close non-Discovery count database: %v", err)
		}
	}()
	counts := make(map[string]int64, len(moduleDiscoveryNonDiscoveryTables))
	for _, table := range moduleDiscoveryNonDiscoveryTables {
		var count int64
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(
			&count,
		); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func moduleDiscoveryAssertNonDiscoveryCounts(
	t *testing.T,
	path string,
	want map[string]int64,
	operation string,
) {
	t.Helper()
	got := moduleDiscoveryNonDiscoveryCounts(t, path)
	for _, table := range moduleDiscoveryNonDiscoveryTables {
		if got[table] != want[table] {
			t.Fatalf(
				"%s changed %s rows from %d to %d",
				operation,
				table,
				want[table],
				got[table],
			)
		}
	}
}
