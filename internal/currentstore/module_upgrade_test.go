package currentstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type moduleUpgradeStoreFixture struct {
	publication    *publicationFixture
	snapshot       ModuleDiscoverySnapshot
	selection      ReadModuleUpgradeReviewBasisInput
	targetManifest []byte
}

func TestModuleUpgradeReviewCommitExactRetryDecisionAndZeroEffects(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	before := moduleUpgradeEffectCounts(t, store)
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	if basis.CurrentInstallation.ModuleID != fixture.selection.TargetModule.ID ||
		basis.CurrentInstallation.ExactVersion != "v1" ||
		basis.Candidate.Change != moduleapi.ModuleCandidateChangeExactVersionV1 ||
		basis.Candidate.Target.Module != fixture.selection.TargetModule ||
		len(basis.BindingImpacts) != 1 {
		t.Fatalf("basis = %+v", basis)
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{} {
		t.Fatalf("read basis wrote supply facts: %v", got)
	}
	reviewCanonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	committed, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical, TargetManifestCanonical: fixture.targetManifest})
	if err != nil {
		t.Fatal(err)
	}
	targetManifestEvidence, err := store.GetContent(ctx, committed.Review.Target.ManifestRef)
	if err != nil || targetManifestEvidence.Kind != ContentModuleManifest ||
		targetManifestEvidence.MediaType != moduleManifestMediaType ||
		!bytes.Equal(targetManifestEvidence.CanonicalBytes, fixture.targetManifest) {
		t.Fatalf("target Manifest evidence=%+v err=%v", targetManifestEvidence, err)
	}
	retry, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: bytes.Clone(reviewCanonical), TargetManifestCanonical: fixture.targetManifest})
	if err != nil || retry.ReviewID != committed.ReviewID || !retry.CreatedAt.Equal(committed.CreatedAt) {
		t.Fatalf("review retry=%+v err=%v", retry, err)
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{1, 1, 0} {
		t.Fatalf("review counts=%v", got)
	}
	decisionCanonical := moduleUpgradeDecisionCanonical(t, committed.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "approved exact review")
	misconfirmedReject := moduleUpgradeDecisionCanonical(t, committed.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "missing explicit tenant-wide confirmation")
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: committed.ReviewID, DecisionCanonical: misconfirmedReject}); !errors.Is(err, ErrInvalidModuleUpgradeReview) {
		t.Fatalf("unconfirmed tenant-wide REJECT error=%v", err)
	}
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: committed.ReviewID, DecisionCanonical: decisionCanonical, ConfirmTenantWideReject: true}); !errors.Is(err, ErrInvalidModuleUpgradeReview) {
		t.Fatalf("APPROVE with tenant-wide rejection confirmation error=%v", err)
	}
	decision, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: committed.ReviewID, DecisionCanonical: decisionCanonical})
	if err != nil {
		t.Fatal(err)
	}
	decisionRetry, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: committed.ReviewID, DecisionCanonical: bytes.Clone(decisionCanonical)})
	if err != nil || decisionRetry.DecisionID != decision.DecisionID || !decisionRetry.DecidedAt.Equal(decision.DecidedAt) {
		t.Fatalf("decision retry=%+v err=%v", decisionRetry, err)
	}
	conflict := moduleUpgradeDecisionCanonical(t, committed.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "different terminal decision")
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: committed.ReviewID, DecisionCanonical: conflict, ConfirmTenantWideReject: true}); !errors.Is(err, ErrModuleCandidateDecisionConflict) {
		t.Fatalf("different terminal error=%v", err)
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{1, 1, 1} {
		t.Fatalf("terminal counts=%v", got)
	}
	if after := moduleUpgradeEffectCounts(t, store); !reflect.DeepEqual(after, before) {
		t.Fatalf("U3 changed runtime/effect tables: before=%v after=%v", before, after)
	}
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, store.db); err != nil {
		t.Fatal(err)
	}
}

func TestModuleUpgradeStoreRequiresExactTargetManifestEvidence(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	reviewCanonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	wrongManifest := canonicalModuleManifest(t, "test.model", "v3", moduleapi.RuntimeModeRequestTrustedInProcess, nil)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical, TargetManifestCanonical: wrongManifest}); !errors.Is(err, ErrInvalidModuleUpgradeReview) {
		t.Fatalf("mismatched target Manifest error=%v", err)
	}
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical}); !errors.Is(err, ErrInvalidModuleUpgradeReview) {
		t.Fatalf("missing target Manifest error=%v", err)
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{} {
		t.Fatalf("invalid target Manifest wrote U3 facts=%v", got)
	}
}

func TestModuleUpgradeReviewPostIOStaleAndTenantRejectSuppression(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	reviewCanonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	second := fixture.publication.input(t, "upgrade-control-2", 2, "upgrade-catalog-2", 2, 1, 2, nil)
	if _, err := store.PublishControlCatalog(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical, TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeReviewStale) {
		t.Fatalf("stale commit error=%v", err)
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{} {
		t.Fatalf("stale commit wrote=%v", got)
	}

	// A deny-only decision remains legal after the review basis becomes stale,
	// but prevents every later scope in this Tenant from resubmitting the same
	// stable ReviewKey.
	freshFixture := newModuleUpgradeStoreFixture(t)
	freshStore := freshFixture.publication.store
	freshBasis, err := freshStore.ReadModuleUpgradeReviewBasis(ctx, freshFixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	freshCanonical := moduleUpgradeReviewCanonical(t, freshBasis, freshFixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	review, err := freshStore.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: freshBasis, ReviewCanonical: freshCanonical, TargetManifestCanonical: freshFixture.targetManifest})
	if err != nil {
		t.Fatal(err)
	}
	advance := freshFixture.publication.input(t, "reject-control-2", 2, "reject-catalog-2", 2, 1, 2, nil)
	if _, err := freshStore.PublishControlCatalog(ctx, advance); err != nil {
		t.Fatal(err)
	}
	reject := moduleUpgradeDecisionCanonical(t, review.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "reject exact supply tuple")
	rejected, err := freshStore.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: freshFixture.publication.tenantID, ReviewID: review.ReviewID, DecisionCanonical: reject, ConfirmTenantWideReject: true})
	if err != nil {
		t.Fatalf("stale deny-only reject=%v", err)
	}
	retry, err := freshStore.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: freshFixture.publication.tenantID, ReviewID: review.ReviewID, DecisionCanonical: bytes.Clone(reject)})
	if err != nil || retry.DecisionID != rejected.DecisionID || !retry.DecidedAt.Equal(rejected.DecidedAt) {
		t.Fatalf("lost-response REJECT retry=%+v err=%v", retry, err)
	}
	selection := freshFixture.selection
	selection.TargetInstanceID = "instance-model-target-other"
	if _, err := freshStore.ReadModuleUpgradeReviewBasis(ctx, selection); !errors.Is(err, ErrModuleUpgradeSuppressed) {
		t.Fatalf("stable ReviewKey suppression error=%v", err)
	}
}

func TestModuleUpgradeReviewPostIORechecksSourcePolicySnapshotAndRevocation(t *testing.T) {
	ctx := context.Background()
	t.Run("snapshot head", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
		if err != nil {
			t.Fatal(err)
		}
		canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
		refresh, err := store.ReadModuleSourceRefreshBasis(ctx, fixture.selection.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		extra := moduleDiscoveryTestEntry("test.other", "v1", strings.Repeat("e", 64))
		if _, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, fixture.selection.SourceID, fixture.snapshot.Snapshot.Entries[0], extra)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("changed Snapshot head error=%v", err)
		}
		if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{} {
			t.Fatalf("stale Snapshot wrote=%v", got)
		}
	})
	t.Run("source policy", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
		if err != nil {
			t.Fatal(err)
		}
		canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
		policy := basis.Source.Policy
		policy.AllowedModuleIDPrefixes = []string{"test", "future"}
		_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyCanonical, ExpectedPolicyRevision: basis.Source.PolicyRevision}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("changed SourcePolicy error=%v", err)
		}
	})
	t.Run("publisher key revocation", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		keyCanonical, keyID := moduleDiscoveryTestPublisherKey(t)
		policyCanonical, policy, _ := moduleDiscoveryTestPolicy(t, "source.upgrade.signed", "upgrade-signed-origin", true, keyID)
		policy.AllowedModuleIDPrefixes = []string{"test"}
		_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyCanonical, PublisherKeyCanonical: keyCanonical}); err != nil {
			t.Fatal(err)
		}
		refresh, err := store.ReadModuleSourceRefreshBasis(ctx, "source.upgrade.signed")
		if err != nil {
			t.Fatal(err)
		}
		target := moduleDiscoveryTestEntry("test.model", "v2", strings.Repeat("b", 64))
		target.SignatureID = strings.Repeat("c", 64)
		snapshot, err := store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, "source.upgrade.signed", target))
		if err != nil {
			t.Fatal(err)
		}
		selection := fixture.selection
		selection.SourceID = "source.upgrade.signed"
		selection.SnapshotID = snapshot.SnapshotID
		basis, err := store.ReadModuleUpgradeReviewBasis(ctx, selection)
		if err != nil {
			t.Fatal(err)
		}
		canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
		if _, err := store.RevokeModulePublisherKey(ctx, keyID, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("revoked PublisherKey error=%v", err)
		}
	})
}

func TestModuleUpgradeRejectsNonWouldApplyApprovalAndSemanticTampering(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionUnsupportedV1)
	review, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest})
	if err != nil {
		t.Fatal(err)
	}
	approve := moduleUpgradeDecisionCanonical(t, review.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "must not approve unsupported")
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: review.ReviewID, DecisionCanonical: approve}); !errors.Is(err, ErrModuleUpgradeReviewConflict) {
		t.Fatalf("approve unsupported error=%v", err)
	}
	execClosedFileTamperV1(t, store,
		[]string{"module_upgrade_reviews_reject_update"},
		`UPDATE module_upgrade_reviews SET pointer_revision=pointer_revision+1 WHERE review_id=?`,
		review.ReviewID,
	)
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, store.db); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("tampered projection error=%v", err)
	}
}

func TestModuleUpgradeReviewBindsExactActivationRow(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	review, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{
		Basis:                   basis,
		ReviewCanonical:         moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1),
		TargetManifestCanonical: fixture.targetManifest,
	})
	if err != nil {
		t.Fatal(err)
	}

	// This row is a valid immutable Activation and deliberately reuses the
	// Review's exact tenant, instance, Installation, execution class, and
	// adapter. Only its identity and revision differ. Repointing the SQL FK to
	// it must not detach the Review projection from its canonical activation.
	other, err := store.ActivateModule(ctx, ActivateModuleInput{
		ActivationID:       "activation-review-parent-tamper",
		TenantID:           review.Review.TenantID,
		InstanceID:         review.Review.Current.Activation.InstanceID,
		InstallationID:     review.Review.Current.InstallationID,
		ActivationRevision: review.Review.Current.Activation.ActivationRevision + 1,
		ExecutionClass:     review.Review.Current.Activation.ExecutionClass,
		AdapterIdentity:    review.Review.Current.Activation.AdapterIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(t, store,
		[]string{"module_upgrade_reviews_reject_update"},
		`UPDATE module_upgrade_reviews SET current_activation_id=? WHERE review_id=?`,
		other.ActivationID, review.ReviewID,
	)
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, store.db); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("foreign-key-valid activation parent tamper error=%v", err)
	}
}

func TestModuleUpgradeExactRetryFailsClosedOnUnrelatedTamper(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name          string
		retryDecision bool
	}{
		{name: "Review"},
		{name: "Decision", retryDecision: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleUpgradeStoreFixture(t)
			addSecondModuleUpgradeProfile(t, fixture.publication)
			firstSelection := fixture.selection
			secondSelection := fixture.selection
			secondSelection.BindingTarget = moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: "profile-secondary"}
			secondSelection.TargetInstanceID = "instance-model-target-secondary"
			firstBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, firstSelection)
			if err != nil {
				t.Fatal(err)
			}
			secondBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, secondSelection)
			if err != nil {
				t.Fatal(err)
			}
			firstCanonical := moduleUpgradeReviewCanonical(t, firstBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
			first, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: firstBasis, ReviewCanonical: firstCanonical, TargetManifestCanonical: fixture.targetManifest})
			if err != nil {
				t.Fatal(err)
			}
			second, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: secondBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, secondBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
			if err != nil {
				t.Fatal(err)
			}
			var decisionCanonical []byte
			if test.retryDecision {
				decisionCanonical = moduleUpgradeDecisionCanonical(t, first.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "exact retry before unrelated tamper")
				if _, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: first.ReviewID, DecisionCanonical: decisionCanonical}); err != nil {
					t.Fatal(err)
				}
			}
			execClosedFileTamperV1(t, fixture.publication.store,
				[]string{"module_upgrade_reviews_reject_update"},
				`UPDATE module_upgrade_reviews SET pointer_revision=pointer_revision+1 WHERE review_id=?`,
				second.ReviewID,
			)
			if test.retryDecision {
				if _, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: first.ReviewID, DecisionCanonical: bytes.Clone(decisionCanonical)}); !errors.Is(err, ErrModuleUpgradeIntegrity) {
					t.Fatalf("Decision exact retry after unrelated tamper error=%v", err)
				}
			} else if _, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: firstBasis, ReviewCanonical: bytes.Clone(firstCanonical), TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeIntegrity) {
				t.Fatalf("Review exact retry after unrelated tamper error=%v", err)
			}
		})
	}
}

func TestModuleUpgradeExactRetryVerifiesHistoricalPublicationContent(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest}); err != nil {
		t.Fatal(err)
	}
	// Keep the FK-valid ContentRecord but change its immutable body and size.
	// Full historical publication verification must reach this Binding CONFIG
	// even though the Review retry itself references only frozen Review bytes.
	tampered := []byte(`{"provider":"tampered"}`)
	execClosedFileTamperV1(t, store,
		[]string{"content_records_reject_update"},
		`UPDATE content_records SET canonical_bytes=?,size_bytes=? WHERE content_digest=?`,
		tampered, len(tampered), basis.SelectedBinding.ConfigRef,
	)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: bytes.Clone(canonical), TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("exact retry with tampered historical CONFIG error=%v", err)
	}
}

func TestModuleUpgradeExactRetryVerifiesDiscoveryEntryProjection(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: fixture.targetManifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE module_discovery_entries
		SET artifact_size_bytes=artifact_size_bytes+1
		WHERE snapshot_id=? AND module_id=? AND exact_version=?
	`, basis.Snapshot.SnapshotID, basis.TargetEntry.Module.ID, basis.TargetEntry.Module.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: bytes.Clone(canonical), TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("exact retry with tampered discovery entry projection error=%v", err)
	}
}

func TestModuleUpgradeStoreRejectsHiddenTargetInstanceConflict(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	installation, err := store.GetModuleInstallationByIdentity(ctx, fixture.publication.activation.ModuleID, fixture.publication.activation.Version)
	if err != nil {
		t.Fatal(err)
	}
	targetActivation, err := store.ActivateModule(ctx, ActivateModuleInput{ActivationID: "activation-existing-target", TenantID: fixture.publication.tenantID, InstanceID: fixture.selection.TargetInstanceID, InstallationID: installation.InstallationID, ActivationRevision: 1, ExecutionClass: fixture.publication.activation.ExecutionClass, AdapterIdentity: fixture.publication.activation.AdapterIdentity})
	if err != nil {
		t.Fatal(err)
	}
	_, control, catalog, err := store.LoadPublishedBasis(ctx, fixture.publication.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "upgrade-control-target-conflict"
	control.Revision = 2
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "upgrade-catalog-target-conflict"
	catalog.Generation = 2
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{Activation: activatedRefFromStore(targetActivation, installation), Provides: []moduleapi.PortRef{fixture.selection.Port}})
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishControlCatalog(ctx, PublishControlCatalogInput{ExpectedPointerRevision: 1, NewPointerRevision: 2, ControlRef: controlRef, ControlCanonical: controlCanonical, CatalogRef: catalogRef, CatalogCanonical: catalogCanonical}); err != nil {
		t.Fatal(err)
	}
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	hidden := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: hidden, TargetManifestCanonical: fixture.targetManifest}); !errors.Is(err, ErrInvalidModuleUpgradeReview) {
		t.Fatalf("hidden target collision error=%v", err)
	}
	conflict := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionConflictV1)
	if _, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: conflict, TargetManifestCanonical: fixture.targetManifest}); err != nil {
		t.Fatalf("honest target conflict=%v", err)
	}
}

func TestModuleUpgradeConcurrentExactReviewAndTerminalDecision(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	canonical := moduleUpgradeReviewCanonical(t, basis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1)
	const workers = 16
	results := make(chan ModuleUpgradeReviewRecord, workers)
	errorsOut := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, commitErr := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: bytes.Clone(canonical), TargetManifestCanonical: fixture.targetManifest})
			results <- result
			errorsOut <- commitErr
		}()
	}
	group.Wait()
	close(results)
	close(errorsOut)
	var reviewID string
	for commitErr := range errorsOut {
		if commitErr != nil {
			t.Fatalf("concurrent exact Review: %v", commitErr)
		}
	}
	for result := range results {
		if reviewID == "" {
			reviewID = result.ReviewID
		}
		if result.ReviewID != reviewID {
			t.Fatalf("exact Review returned IDs %q and %q", reviewID, result.ReviewID)
		}
	}
	if got := moduleUpgradeSupplyCounts(t, store); got != [3]int{1, 1, 0} {
		t.Fatalf("concurrent exact Review counts=%v", got)
	}
	review, err := store.GetModuleUpgradeReview(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	decisions := [][]byte{
		moduleUpgradeDecisionCanonical(t, review.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "concurrent approve"),
		moduleUpgradeDecisionCanonical(t, review.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "concurrent reject"),
	}
	decisionErrors := make(chan error, len(decisions))
	for _, decision := range decisions {
		group.Add(1)
		go func(canonical []byte) {
			defer group.Done()
			decision, _, _, parseErr := parseCandidateDecision(canonical)
			if parseErr != nil {
				decisionErrors <- parseErr
				return
			}
			_, decideErr := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: reviewID, DecisionCanonical: canonical, ConfirmTenantWideReject: decision.Decision == moduleapi.ModuleCandidateDecisionRejectV1})
			decisionErrors <- decideErr
		}(decision)
	}
	group.Wait()
	close(decisionErrors)
	succeeded, conflicted := 0, 0
	for decideErr := range decisionErrors {
		switch {
		case decideErr == nil:
			succeeded++
		case errors.Is(decideErr, ErrModuleCandidateDecisionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent terminal error=%v", decideErr)
		}
	}
	if succeeded != 1 || conflicted != 1 || moduleUpgradeSupplyCounts(t, store)[2] != 1 {
		t.Fatalf("terminal race success=%d conflict=%d counts=%v", succeeded, conflicted, moduleUpgradeSupplyCounts(t, store))
	}
}

func TestModuleUpgradeTwoScopesShareDecisionFactAndTenantRejectWins(t *testing.T) {
	ctx := context.Background()
	t.Run("same canonical Decision may bind two exact Reviews", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		addSecondModuleUpgradeProfile(t, fixture.publication)
		firstSelection := fixture.selection
		secondSelection := fixture.selection
		secondSelection.BindingTarget = moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: "profile-secondary"}
		secondSelection.TargetInstanceID = "instance-model-target-secondary"
		firstBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, firstSelection)
		if err != nil {
			t.Fatal(err)
		}
		secondBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, secondSelection)
		if err != nil {
			t.Fatal(err)
		}
		if firstBasis.CandidateID != secondBasis.CandidateID || len(firstBasis.BindingImpacts) != 2 || len(secondBasis.BindingImpacts) != 2 {
			t.Fatalf("two-scope bases differ: first=%+v second=%+v", firstBasis, secondBasis)
		}
		first, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: firstBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, firstBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: secondBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, secondBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
		if err != nil {
			t.Fatal(err)
		}
		decisionCanonical := moduleUpgradeDecisionCanonical(t, first.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "same approval for exact candidate")
		firstDecision, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: first.ReviewID, DecisionCanonical: decisionCanonical})
		if err != nil {
			t.Fatal(err)
		}
		secondDecision, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: second.ReviewID, DecisionCanonical: bytes.Clone(decisionCanonical)})
		if err != nil {
			t.Fatal(err)
		}
		if firstDecision.DecisionID != secondDecision.DecisionID || firstDecision.ReviewID == secondDecision.ReviewID || moduleUpgradeSupplyCounts(t, fixture.publication.store)[2] != 2 {
			t.Fatalf("shared Decision fact associations=%+v / %+v", firstDecision, secondDecision)
		}
	})
	t.Run("Tenant REJECT suppresses prior open Review in another scope", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		addSecondModuleUpgradeProfile(t, fixture.publication)
		firstSelection := fixture.selection
		secondSelection := fixture.selection
		secondSelection.BindingTarget = moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: "profile-secondary"}
		secondSelection.TargetInstanceID = "instance-model-target-secondary"
		firstBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, firstSelection)
		if err != nil {
			t.Fatal(err)
		}
		secondBasis, err := fixture.publication.store.ReadModuleUpgradeReviewBasis(ctx, secondSelection)
		if err != nil {
			t.Fatal(err)
		}
		first, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: firstBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, firstBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.publication.store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: secondBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, secondBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
		if err != nil {
			t.Fatal(err)
		}
		reject := moduleUpgradeDecisionCanonical(t, first.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "tenant denies exact supply tuple")
		if _, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: first.ReviewID, DecisionCanonical: reject, ConfirmTenantWideReject: true}); err != nil {
			t.Fatal(err)
		}
		approve := moduleUpgradeDecisionCanonical(t, second.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "other scope tries approval")
		if _, err := fixture.publication.store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: second.ReviewID, DecisionCanonical: approve}); !errors.Is(err, ErrModuleUpgradeSuppressed) {
			t.Fatalf("cross-scope approval after REJECT error=%v", err)
		}
	})
}

func TestModuleUpgradeRejectSuppressionIsTenantScoped(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	firstBasis, err := store.ReadModuleUpgradeReviewBasis(ctx, fixture.selection)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: firstBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, firstBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
	if err != nil {
		t.Fatal(err)
	}
	reject := moduleUpgradeDecisionCanonical(t, first.Review.CandidateID, moduleapi.ModuleCandidateDecisionRejectV1, "first tenant rejects exact tuple")
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: fixture.publication.tenantID, ReviewID: first.ReviewID, DecisionCanonical: reject, ConfirmTenantWideReject: true}); err != nil {
		t.Fatal(err)
	}
	const otherTenant = "tenant-upgrade-other"
	otherSelection := publishSecondUpgradeTenant(t, fixture, otherTenant)
	otherBasis, err := store.ReadModuleUpgradeReviewBasis(ctx, otherSelection)
	if err != nil {
		t.Fatalf("other Tenant was polluted by rejection: %v", err)
	}
	if otherBasis.CandidateID != firstBasis.CandidateID || otherBasis.Candidate.ReviewKey != firstBasis.Candidate.ReviewKey {
		t.Fatalf("cross-Tenant Candidate identities differ")
	}
	other, err := store.CommitModuleUpgradeReview(ctx, CommitModuleUpgradeReviewInput{Basis: otherBasis, ReviewCanonical: moduleUpgradeReviewCanonical(t, otherBasis, fixture.targetManifest, moduleupgrade.ConclusionWouldApplyV1), TargetManifestCanonical: fixture.targetManifest})
	if err != nil {
		t.Fatal(err)
	}
	approve := moduleUpgradeDecisionCanonical(t, other.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "other tenant independently approves")
	if _, err := store.DecideModuleCandidate(ctx, DecideModuleCandidateInput{TenantID: otherTenant, ReviewID: other.ReviewID, DecisionCanonical: approve}); err != nil {
		t.Fatal(err)
	}
}

func newModuleUpgradeStoreFixture(t *testing.T) *moduleUpgradeStoreFixture {
	t.Helper()
	p := newPublicationFixture(t)
	ctx := context.Background()
	input := p.input(t, "upgrade-control-1", 1, "upgrade-catalog-1", 1, 0, 1, nil)
	if _, err := p.store.PublishControlCatalog(ctx, input); err != nil {
		t.Fatal(err)
	}
	policyCanonical, policy, _ := moduleDiscoveryTestPolicy(t, "source.upgrade", "upgrade-origin", false, "")
	policy.AllowedModuleIDPrefixes = []string{"test"}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.RegisterModuleSource(ctx, RegisterModuleSourceInput{PolicyCanonical: policyCanonical}); err != nil {
		t.Fatal(err)
	}
	refresh, err := p.store.ReadModuleSourceRefreshBasis(ctx, "source.upgrade")
	if err != nil {
		t.Fatal(err)
	}
	target := moduleDiscoveryTestEntry("test.model", "v2", strings.Repeat("b", 64))
	snapshot, err := p.store.CommitModuleSourceRefresh(ctx, refresh, moduleDiscoveryTestIndex(t, "source.upgrade", target))
	if err != nil {
		t.Fatal(err)
	}
	return &moduleUpgradeStoreFixture{publication: p, snapshot: snapshot, targetManifest: canonicalModuleManifest(t, "test.model", "v2", moduleapi.RuntimeModeRequestTrustedInProcess, nil), selection: ReadModuleUpgradeReviewBasisInput{SourceID: "source.upgrade", SnapshotID: snapshot.SnapshotID, TenantID: p.tenantID, BindingTarget: moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: "profile-default"}, Port: p.port, PortBindingIndex: 0, TargetModule: target.Module, TargetArtifactDigest: target.ArtifactDigest, TargetInstanceID: "instance-model-target"}}
}

func addSecondModuleUpgradeProfile(t *testing.T, fixture *publicationFixture) {
	t.Helper()
	ctx := context.Background()
	_, control, catalog, err := fixture.store.LoadPublishedBasis(ctx, fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	secondary := control.Profiles[0]
	secondary.Profile.ID = "profile-secondary"
	secondary.Profile.Version = "v1"
	secondary.Profile.Digest = strings.Repeat("9", 64)
	secondary.Bindings = append([]controlcontract.BindingSpec(nil), secondary.Bindings...)
	control.SnapshotID = "upgrade-control-two-profiles"
	control.Revision = 2
	control.Digest = ""
	control.Profiles = append(control.Profiles, secondary)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "upgrade-catalog-two-profiles"
	catalog.Generation = 2
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PublishControlCatalog(ctx, PublishControlCatalogInput{ExpectedPointerRevision: 1, NewPointerRevision: 2, ControlRef: controlRef, ControlCanonical: controlCanonical, CatalogRef: catalogRef, CatalogCanonical: catalogCanonical}); err != nil {
		t.Fatal(err)
	}
}

func publishSecondUpgradeTenant(t *testing.T, fixture *moduleUpgradeStoreFixture, tenantID string) ReadModuleUpgradeReviewBasisInput {
	t.Helper()
	ctx := context.Background()
	store := fixture.publication.store
	_, control, catalog, err := store.LoadPublishedBasis(ctx, fixture.publication.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.GetModuleInstallationByIdentity(ctx, fixture.publication.activation.ModuleID, fixture.publication.activation.Version)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(ctx, ActivateModuleInput{ActivationID: "activation-model-other-tenant", TenantID: tenantID, InstanceID: "instance-model-other-tenant", InstallationID: installation.InstallationID, ActivationRevision: 1, ExecutionClass: fixture.publication.activation.ExecutionClass, AdapterIdentity: fixture.publication.activation.AdapterIdentity})
	if err != nil {
		t.Fatal(err)
	}
	provider := activatedRefFromStore(activation, installation)
	oldInstance := fixture.publication.activation.InstanceID
	control.TenantID = tenantID
	control.SnapshotID = "upgrade-control-other-tenant"
	control.Revision = 1
	control.Digest = ""
	for profileIndex := range control.Profiles {
		for bindingIndex := range control.Profiles[profileIndex].Bindings {
			if control.Profiles[profileIndex].Bindings[bindingIndex].InstanceID == oldInstance {
				control.Profiles[profileIndex].Bindings[bindingIndex].InstanceID = activation.InstanceID
			}
		}
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.TenantID = tenantID
	catalog.GenerationID = "upgrade-catalog-other-tenant"
	catalog.Generation = 1
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	for i := range catalog.Entries {
		if catalog.Entries[i].Activation.InstanceID == oldInstance {
			catalog.Entries[i].Activation = provider
		}
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishControlCatalog(ctx, PublishControlCatalogInput{ExpectedPointerRevision: 0, NewPointerRevision: 1, ControlRef: controlRef, ControlCanonical: controlCanonical, CatalogRef: catalogRef, CatalogCanonical: catalogCanonical}); err != nil {
		t.Fatal(err)
	}
	selection := fixture.selection
	selection.TenantID = tenantID
	selection.TargetInstanceID = "instance-model-target-other-tenant"
	return selection
}

func moduleUpgradeReviewCanonical(t *testing.T, basis ModuleUpgradeReviewBasis, targetManifestCanonical []byte, conclusion moduleupgrade.ConclusionV1) []byte {
	t.Helper()
	targetManifest, _, err := moduleapi.ParseModuleManifestV1(targetManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	targetManifestRef, err := ComputeContentDigest(ContentModuleManifest, moduleManifestMediaType, targetManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	currentManifest, _, err := moduleapi.ParseModuleManifestV1(basis.CurrentInstallation.ManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	handler := moduleupgrade.HandlerAssessmentV1{Status: moduleupgrade.HandlerSupportedV1, Kind: "model.generate", ExecutionClass: moduleapi.ExecutionTrustedInProcess, AdapterIdentity: "core.model.adapter"}
	reasons := []moduleupgrade.ReasonCodeV1{moduleupgrade.ReasonOperatorGrantRequiredV1}
	grants := []moduleupgrade.RequiredGrantV1{{Kind: moduleupgrade.GrantTrustedInProcessArtifactV1, ReferenceDigest: basis.TargetEntry.ArtifactDigest}}
	if conclusion == moduleupgrade.ConclusionUnsupportedV1 {
		handler = moduleupgrade.HandlerAssessmentV1{Status: moduleupgrade.HandlerUnsupportedV1}
		reasons = []moduleupgrade.ReasonCodeV1{moduleupgrade.ReasonHandlerUnsupportedV1}
		grants = nil
	} else if conclusion == moduleupgrade.ConclusionConflictV1 {
		reasons = []moduleupgrade.ReasonCodeV1{moduleupgrade.ReasonOperatorGrantRequiredV1, moduleupgrade.ReasonTargetInstanceConflictV1}
	}
	current := moduleupgrade.CurrentExactV1{Activation: activatedRefFromStore(basis.CurrentActivation, basis.CurrentInstallation), InstallationID: basis.CurrentInstallation.InstallationID, ManifestRef: basis.CurrentInstallation.ManifestRef, Manifest: manifestSummary(currentManifest)}
	review := moduleupgrade.ReviewV1{SchemaVersion: moduleupgrade.ReviewSchemaVersionV1, CandidateID: basis.CandidateID, ReviewKey: basis.Candidate.ReviewKey, TenantID: basis.Selection.TenantID, BindingTarget: basis.Selection.BindingTarget, Port: basis.Selection.Port, PortBindingIndex: basis.Selection.PortBindingIndex, TargetInstanceID: basis.Selection.TargetInstanceID, SupplyBasis: supplyBasisFromStore(basis), PublishedBasis: basis.PublishedBasis, Current: current, Target: moduleupgrade.TargetEvidenceV1{Module: basis.TargetEntry.Module, ArtifactDigest: basis.TargetEntry.ArtifactDigest, ArtifactSizeBytes: basis.TargetEntry.ArtifactSizeBytes, SignatureID: basis.TargetEntry.SignatureID, ManifestRef: targetManifestRef, Manifest: manifestSummary(targetManifest)}, Handler: handler, BindingImpacts: cloneUpgradeImpacts(basis.BindingImpacts), RequiredGrants: grants, Conclusion: conclusion, ReasonCodes: reasons}
	_, canonical, _, err := moduleupgrade.NewReviewV1(review, basis.CandidateCanonical, basis.Snapshot.SnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func moduleUpgradeDecisionCanonical(t *testing.T, candidateID string, value moduleapi.ModuleCandidateDecisionValueV1, reason string) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewModuleCandidateDecisionV1(moduleapi.ModuleCandidateDecisionV1{SchemaVersion: moduleapi.ModuleCandidateDecisionSchemaVersionV1, CandidateID: candidateID, Decision: value, OperatorPrincipalID: "operator.test", Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func moduleUpgradeSupplyCounts(t *testing.T, store *Store) [3]int {
	t.Helper()
	var v [3]int
	for i, table := range []string{"module_upgrade_candidates", "module_upgrade_reviews", "module_candidate_decisions"} {
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&v[i]); err != nil {
			t.Fatal(err)
		}
	}
	return v
}
func moduleUpgradeEffectCounts(t *testing.T, store *Store) map[string]int {
	t.Helper()
	tables := []string{"module_installations", "module_activations", "control_snapshots", "runtime_catalog_generations", "control_current", "runs", "dispatch_attempts", "model_dispatch_attempts", "model_usage"}
	out := map[string]int{}
	for _, table := range tables {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		out[table] = count
	}
	return out
}
