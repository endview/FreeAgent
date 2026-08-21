package currentstore

import (
	"context"
	"fmt"
	"reflect"

	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyModuleUpgradeSemanticClosureV1 reconstructs every immutable
// Candidate -> Review -> Decision edge from one coherent Store snapshot. It
// is read-only and never opens a Source, artifact, Host, Gateway, or Adapter.
func VerifyModuleUpgradeSemanticClosureV1(ctx context.Context, q moduleDiscoveryQueryer) error {
	if ctx == nil || q == nil {
		return fmt.Errorf("%w: nil semantic verifier input", ErrModuleUpgradeIntegrity)
	}
	if err := VerifyModuleDiscoverySemanticClosureV1(ctx, q); err != nil {
		return fmt.Errorf("%w: Module Discovery parent closure: %v", ErrModuleUpgradeIntegrity, err)
	}
	candidateIDs, err := readDiscoveryIdentityColumn(ctx, q,
		`SELECT candidate_id FROM module_upgrade_candidates ORDER BY candidate_id COLLATE BINARY`)
	if err != nil {
		return fmt.Errorf("%w: list Candidates: %v", ErrModuleUpgradeIntegrity, err)
	}
	for _, id := range candidateIDs {
		candidate, found, err := queryModuleUpgradeCandidate(ctx, q, id)
		if err != nil || !found {
			return fmt.Errorf("%w: Candidate %s: %v", ErrModuleUpgradeIntegrity, id, err)
		}
		var reviews int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM module_upgrade_reviews WHERE candidate_id=?`,
			candidate.CandidateID).Scan(&reviews); err != nil || reviews == 0 {
			return fmt.Errorf("%w: Candidate %s is orphaned", ErrModuleUpgradeIntegrity, id)
		}
	}

	reviewIDs, err := readDiscoveryIdentityColumn(ctx, q,
		`SELECT review_id FROM module_upgrade_reviews ORDER BY tenant_id COLLATE BINARY,candidate_id COLLATE BINARY,review_id COLLATE BINARY`)
	if err != nil {
		return fmt.Errorf("%w: list Reviews: %v", ErrModuleUpgradeIntegrity, err)
	}
	verifiedPublications := make(map[string]struct{})
	for _, id := range reviewIDs {
		record, found, err := queryModuleUpgradeReview(ctx, q, id)
		if err != nil || !found {
			return fmt.Errorf("%w: Review %s: %v", ErrModuleUpgradeIntegrity, id, err)
		}
		if err := verifyHistoricalModuleUpgradeReview(ctx, q, record, verifiedPublications); err != nil {
			return err
		}
	}

	decisionReviewIDs, err := readDiscoveryIdentityColumn(ctx, q,
		`SELECT review_id FROM module_candidate_decisions ORDER BY tenant_id COLLATE BINARY,review_key COLLATE BINARY,review_id COLLATE BINARY`)
	if err != nil {
		return fmt.Errorf("%w: list Decisions: %v", ErrModuleUpgradeIntegrity, err)
	}
	for _, reviewID := range decisionReviewIDs {
		decision, found, err := queryModuleCandidateDecisionByReview(ctx, q, reviewID)
		if err != nil || !found {
			return fmt.Errorf("%w: Decision for Review %s: %v", ErrModuleUpgradeIntegrity, reviewID, err)
		}
		review, found, err := queryModuleUpgradeReview(ctx, q, reviewID)
		if err != nil || !found {
			return fmt.Errorf("%w: Decision Review parent %s: %v", ErrModuleUpgradeIntegrity, reviewID, err)
		}
		if decision.TenantID != review.Review.TenantID ||
			decision.ReviewKey != review.Review.ReviewKey ||
			decision.Decision.CandidateID != review.Review.CandidateID {
			return fmt.Errorf("%w: Decision %s parent projection differs", ErrModuleUpgradeIntegrity, decision.DecisionID)
		}
		if decision.Decision.Decision == moduleapi.ModuleCandidateDecisionApproveV1 &&
			review.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 {
			return fmt.Errorf("%w: non-WOULD_APPLY Review is approved", ErrModuleUpgradeIntegrity)
		}
	}
	return nil
}

func verifyHistoricalModuleUpgradeReview(ctx context.Context, q moduleDiscoveryQueryer, record ModuleUpgradeReviewRecord, verifiedPublications map[string]struct{}) error {
	review := record.Review
	targetManifest, err := queryContent(ctx, q, review.Target.ManifestRef)
	if err != nil || targetManifest.Kind != ContentModuleManifest || targetManifest.MediaType != moduleManifestMediaType ||
		requireTargetManifestMatchesReview(review, targetManifest.CanonicalBytes) != nil {
		return fmt.Errorf("%w: Review %s target Manifest evidence differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, q, record.Candidate.SnapshotID)
	if err != nil || !found {
		return fmt.Errorf("%w: Review %s Snapshot parent: %v", ErrModuleUpgradeIntegrity, record.ReviewID, err)
	}
	if review.SupplyBasis.SourceID != snapshot.SourceID ||
		review.SupplyBasis.SourcePolicyID != snapshot.SourcePolicyID ||
		review.SupplyBasis.SourcePolicyRevision != snapshot.SourcePolicyRevision ||
		review.SupplyBasis.SnapshotID != snapshot.SnapshotID ||
		review.SupplyBasis.IndexID != snapshot.IndexID ||
		review.SupplyBasis.ObservationRevision != snapshot.ObservationRevision {
		return fmt.Errorf("%w: Review %s historical supply basis differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	if snapshot.PublisherKeyID == "" {
		if review.SupplyBasis.SignatureStatus != moduleupgrade.SignatureNotRequiredV1 ||
			review.SupplyBasis.PublisherKeyID != "" || review.SupplyBasis.PublisherKeyRevision != 0 {
			return fmt.Errorf("%w: Review %s unsigned basis differs", ErrModuleUpgradeIntegrity, record.ReviewID)
		}
	} else {
		key, found, err := queryModulePublisherKey(ctx, q, snapshot.PublisherKeyID)
		if err != nil || !found || review.SupplyBasis.SignatureStatus != moduleupgrade.SignatureVerifiedV1 ||
			review.SupplyBasis.PublisherKeyID != snapshot.PublisherKeyID ||
			review.SupplyBasis.PublisherKeyRevision != snapshot.PublisherKeyRevision ||
			key.Revision < snapshot.PublisherKeyRevision {
			return fmt.Errorf("%w: Review %s signed basis differs", ErrModuleUpgradeIntegrity, record.ReviewID)
		}
	}

	control, catalog, err := loadAdmissionControlCatalog(ctx, q, review.TenantID,
		review.PublishedBasis.Control.SnapshotID,
		review.PublishedBasis.Catalog.GenerationID)
	if err != nil {
		return fmt.Errorf("%w: Review %s historical publication: %v", ErrModuleUpgradeIntegrity, record.ReviewID, err)
	}
	if control.Revision != review.PublishedBasis.Control.Revision || control.Digest != review.PublishedBasis.Control.Digest ||
		catalog.Generation != review.PublishedBasis.Catalog.Generation || catalog.Digest != review.PublishedBasis.Catalog.Digest {
		return fmt.Errorf("%w: Review %s publication refs differ", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	publicationKey := review.TenantID + "\x00" + control.SnapshotID + "\x00" + catalog.GenerationID
	if _, verified := verifiedPublications[publicationKey]; !verified {
		if err := VerifyPublishedControlCatalogClosureV1(ctx, q, control, catalog); err != nil {
			return fmt.Errorf("%w: Review %s publication closure: %v", ErrModuleUpgradeIntegrity, record.ReviewID, err)
		}
		verifiedPublications[publicationKey] = struct{}{}
	}
	// PointerRevision is an observation of the current pointer at admission.
	// Historical Control/Catalog rows intentionally carry no reverse mutable
	// pointer edge; require only that this was a legal positive revision.
	if review.PublishedBasis.PointerRevision == 0 {
		return fmt.Errorf("%w: Review %s pointer revision is zero", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	selected, err := selectUpgradeBinding(control, review.BindingTarget, review.Port, review.PortBindingIndex)
	if err != nil || selected.InstanceID != review.Current.Activation.InstanceID {
		return fmt.Errorf("%w: Review %s selected historical Binding differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	entry, found := catalog.FindInstance(selected.InstanceID)
	if !found || entry.Activation != review.Current.Activation || !currentCatalogProvidesPort(entry.Provides, review.Port) {
		return fmt.Errorf("%w: Review %s Catalog activation differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	activation, err := queryModuleActivationByIdentity(ctx, q, review.TenantID,
		review.Current.Activation.InstanceID, review.Current.Activation.ActivationRevision)
	if err != nil || activation.InstallationID != review.Current.InstallationID ||
		activation.ExecutionClass != review.Current.Activation.ExecutionClass ||
		activation.AdapterIdentity != review.Current.Activation.AdapterIdentity {
		return fmt.Errorf("%w: Review %s Activation differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	installation, err := queryModuleInstallationByID(ctx, q, activation.InstallationID)
	if err != nil || installation.ManifestRef != review.Current.ManifestRef ||
		installation.ModuleID != review.Current.Activation.ModuleID ||
		installation.ExactVersion != review.Current.Activation.Version ||
		installation.ArtifactDigest != review.Current.Activation.ArtifactDigest {
		return fmt.Errorf("%w: Review %s Installation differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(installation.ManifestBytes)
	if err != nil || !reflect.DeepEqual(review.Current.Manifest, manifestSummary(manifest)) {
		return fmt.Errorf("%w: Review %s current Manifest differs", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	expectedImpacts := allUpgradeBindingImpacts(control, review.Current.Activation.InstanceID, review.TargetInstanceID)
	if !reflect.DeepEqual(review.BindingImpacts, expectedImpacts) {
		return fmt.Errorf("%w: Review %s omits or changes published Binding impacts", ErrModuleUpgradeIntegrity, record.ReviewID)
	}
	return nil
}
