package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

func TestModuleUpgradeBackupRoundTripSuppressionAndSemanticTamper(t *testing.T) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	basis, reviewCanonical, targetManifestCanonical, _ := buildBackupModuleUpgradeReview(t, store, "backup-pure", false)
	review, err := store.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical, TargetManifestCanonical: targetManifestCanonical})
	if err != nil {
		t.Fatal(err)
	}
	_, decisionCanonical, _, err := moduleapi.NewModuleCandidateDecisionV1(moduleapi.ModuleCandidateDecisionV1{
		SchemaVersion:       moduleapi.ModuleCandidateDecisionSchemaVersionV1,
		CandidateID:         review.Review.CandidateID,
		Decision:            moduleapi.ModuleCandidateDecisionRejectV1,
		OperatorPrincipalID: "backup.operator",
		Reason:              "preserve tenant-scoped exact rejection",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{TenantID: "backup-pure", ReviewID: review.ReviewID, DecisionCanonical: decisionCanonical, ConfirmTenantWideReject: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "module-upgrade.bundle")
	if _, err := CreateBundle(ctx, fixture.databasePath, fixture.artifactRoot, bundle, "module-upgrade-test/v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	restoreRoot := t.TempDir()
	restoredDB := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
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
	restoredReview, err := restored.GetModuleUpgradeReview(ctx, review.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	restoredDecision, err := restored.GetModuleCandidateDecision(ctx, review.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredReview.ReviewID != review.ReviewID || restoredReview.Review.CandidateID != review.Review.CandidateID ||
		restoredDecision.DecisionID != decision.DecisionID || restoredDecision.ReviewKey != decision.ReviewKey {
		t.Fatalf("restored Review/Decision = %+v / %+v", restoredReview, restoredDecision)
	}
	selection := basis.Selection
	selection.TargetInstanceID = "backup-upgrade-other-target"
	if _, err := restored.ReadModuleUpgradeReviewBasis(ctx, selection); !errors.Is(err, currentstore.ErrModuleUpgradeSuppressed) {
		t.Fatalf("restored rejection suppression error=%v", err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite", restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE module_candidate_decisions SET review_key=? WHERE review_id=?`, strings.Repeat("f", 64), review.ReviewID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered Module Upgrade backup error=%v", err)
	}
}

func TestModuleUpgradeBackupPreservesSignedReviewAcrossKeyRevocation(t *testing.T) {
	ctx := context.Background()
	fixture := newBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, canonical, targetManifestCanonical, keyID := buildBackupModuleUpgradeReview(t, store, "backup-pure", true)
	first, err := store.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: canonical, TargetManifestCanonical: targetManifestCanonical})
	if err != nil {
		t.Fatal(err)
	}
	approvalBasis, approvalCanonical, approvalTargetManifestCanonical, approvalKeyID := buildBackupModuleUpgradeReviewVariant(t, store, "backup-pure", true, "approval")
	if approvalKeyID != keyID {
		t.Fatal("signed Review variants use different PublisherKeys")
	}
	openReview, err := store.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{Basis: approvalBasis, ReviewCanonical: approvalCanonical, TargetManifestCanonical: approvalTargetManifestCanonical})
	if err != nil {
		t.Fatal(err)
	}
	makeAlternate := func(targetInstanceID string) (currentstore.ModuleUpgradeReviewBasis, []byte) {
		selection := approvalBasis.Selection
		selection.TargetInstanceID = targetInstanceID
		otherBasis, readErr := store.ReadModuleUpgradeReviewBasis(ctx, selection)
		if readErr != nil {
			t.Fatal(readErr)
		}
		review, restoreErr := moduleupgrade.RestoreReviewV1(approvalCanonical, openReview.ReviewID, approvalBasis.CandidateCanonical, approvalBasis.Snapshot.SnapshotCanonical)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		review.TargetInstanceID = targetInstanceID
		review.BindingImpacts = otherBasis.BindingImpacts
		_, otherCanonical, _, buildErr := moduleupgrade.NewReviewV1(review, otherBasis.CandidateCanonical, otherBasis.Snapshot.SnapshotCanonical)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return otherBasis, otherCanonical
	}
	uncommittedBasis, uncommittedCanonical := makeAlternate("backup-signed-uncommitted-target")
	_, rejectCanonical, _, err := moduleapi.NewModuleCandidateDecisionV1(moduleapi.ModuleCandidateDecisionV1{SchemaVersion: moduleapi.ModuleCandidateDecisionSchemaVersionV1, CandidateID: first.Review.CandidateID, Decision: moduleapi.ModuleCandidateDecisionRejectV1, OperatorPrincipalID: "backup.operator", Reason: "explicit tenant-wide signed supply rejection"})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := store.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{TenantID: "backup-pure", ReviewID: first.ReviewID, DecisionCanonical: rejectCanonical, ConfirmTenantWideReject: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeModulePublisherKey(ctx, keyID, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "module-upgrade-signed-revoked.bundle")
	if _, err := CreateBundle(ctx, fixture.databasePath, fixture.artifactRoot, bundle, "module-upgrade-signed-revoked/v1"); err != nil {
		t.Fatal(err)
	}
	restoreRoot := t.TempDir()
	restoredDB := filepath.Join(restoreRoot, "current.sqlite")
	if err := RestoreBundle(ctx, bundle, restoredDB, filepath.Join(restoreRoot, "artifacts")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); err != nil {
		t.Fatal(err)
	}
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.GetModuleUpgradeReview(ctx, first.ReviewID); err != nil {
		t.Fatal(err)
	}
	retry, err := restored.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{TenantID: "backup-pure", ReviewID: first.ReviewID, DecisionCanonical: bytes.Clone(rejectCanonical)})
	if err != nil || retry.DecisionID != rejected.DecisionID {
		t.Fatalf("historical REJECT exact retry=%+v err=%v", retry, err)
	}
	approve := moduleUpgradeDecisionCanonicalForBackup(t, openReview.Review.CandidateID, moduleapi.ModuleCandidateDecisionApproveV1, "must fail after key revocation")
	if _, err := restored.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{TenantID: "backup-pure", ReviewID: openReview.ReviewID, DecisionCanonical: approve}); !errors.Is(err, currentstore.ErrModulePublisherKeyRevoked) {
		t.Fatalf("new APPROVE after restored revocation error=%v", err)
	}
	if _, err := restored.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{Basis: uncommittedBasis, ReviewCanonical: uncommittedCanonical, TargetManifestCanonical: approvalTargetManifestCanonical}); !errors.Is(err, currentstore.ErrModulePublisherKeyRevoked) {
		t.Fatalf("new Review after restored revocation error=%v", err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE module_discovery_snapshots SET publisher_key_revision=2 WHERE snapshot_id=?`, basis.Snapshot.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDB); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("signed Review parent tamper error=%v", err)
	}
}

func moduleUpgradeDecisionCanonicalForBackup(t *testing.T, candidateID string, decision moduleapi.ModuleCandidateDecisionValueV1, reason string) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewModuleCandidateDecisionV1(moduleapi.ModuleCandidateDecisionV1{SchemaVersion: moduleapi.ModuleCandidateDecisionSchemaVersionV1, CandidateID: candidateID, Decision: decision, OperatorPrincipalID: "backup.operator", Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func buildBackupModuleUpgradeReview(t *testing.T, store *currentstore.Store, tenantID string, signed bool) (currentstore.ModuleUpgradeReviewBasis, []byte, []byte, string) {
	return buildBackupModuleUpgradeReviewVariant(t, store, tenantID, signed, "")
}

func buildBackupModuleUpgradeReviewVariant(t *testing.T, store *currentstore.Store, tenantID string, signed bool, variant string) (currentstore.ModuleUpgradeReviewBasis, []byte, []byte, string) {
	t.Helper()
	ctx := context.Background()
	_, control, catalog, err := store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var target moduleupgrade.BindingTargetV1
	var port moduleapi.PortRef
	var portIndex uint32
	var current moduleapi.ActivatedModuleRef
	found := false
	for _, profile := range control.Profiles {
		ordinals := map[moduleapi.PortRef]uint32{}
		for _, binding := range profile.Bindings {
			ordinal := ordinals[binding.Port]
			ordinals[binding.Port]++
			entry, exists := catalog.FindInstance(binding.InstanceID)
			if !exists {
				continue
			}
			target = moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: profile.Profile.ID}
			port, portIndex, current, found = binding.Port, ordinal, entry.Activation, true
			break
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("backup fixture has no current Profile binding")
	}
	installation, err := store.GetModuleInstallationByIdentity(ctx, current.ModuleID, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(installation.ManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	targetVersion := "backup-upgrade-v2"
	targetDigest := strings.Repeat("d", 64)
	sourceID := "backup.upgrade.source"
	if variant != "" {
		targetVersion += "-" + variant
		targetDigest = strings.Repeat("e", 64)
		sourceID += "." + variant
	}
	targetManifest := manifest
	targetManifest.Version = targetVersion
	targetManifestBytes, err := json.Marshal(targetManifest)
	if err != nil {
		t.Fatal(err)
	}
	targetManifestCanonical, err := moduleapi.CanonicalJSON(targetManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(targetManifestCanonical); err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(moduleapi.ModuleSourceKindLocalDirectoryV1, []byte("backup-upgrade-origin-"+variant))
	if err != nil {
		t.Fatal(err)
	}
	var keyCanonical []byte
	var keyID string
	if signed {
		_, keyCanonical, keyID, err = moduleapi.NewModulePublisherKeyV1(moduleapi.ModulePublisherKeyV1{
			SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
			Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                sourceID,
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		SignatureRequired:       signed,
		PublisherKeyID:          keyID,
		AllowedModuleIDPrefixes: []string{current.ModuleID},
		MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
		MaxPackageBytes:         1 << 20,
		MaxCandidates:           8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{PolicyCanonical: policyCanonical, PublisherKeyCanonical: keyCanonical}); err != nil {
		t.Fatal(err)
	}
	refresh, err := store.ReadModuleSourceRefreshBasis(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	targetEntry := moduleapi.ModuleDiscoveryEntryV1{Module: moduleapi.Ref{ID: current.ModuleID, Version: targetVersion}, ArtifactDigest: targetDigest, ArtifactSizeBytes: 512, PackagePath: "packages/upgrade.zip"}
	if signed {
		targetEntry.SignatureID = strings.Repeat("c", 64)
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1, SourceID: sourceID, Entries: []moduleapi.ModuleDiscoveryEntryV1{targetEntry}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(ctx, refresh, indexCanonical)
	if err != nil {
		t.Fatal(err)
	}
	selection := currentstore.ReadModuleUpgradeReviewBasisInput{SourceID: sourceID, SnapshotID: snapshot.SnapshotID, TenantID: tenantID, BindingTarget: target, Port: port, PortBindingIndex: portIndex, TargetModule: targetEntry.Module, TargetArtifactDigest: targetDigest, TargetInstanceID: "backup-upgrade-target-" + targetVersion}
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	targetManifestRef, err := currentstore.ComputeContentDigest(currentstore.ContentModuleManifest, "application/json", targetManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	currentManifest, _, err := moduleapi.ParseModuleManifestV1(basis.CurrentInstallation.ManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	var grants []moduleupgrade.RequiredGrantV1
	var reasons []moduleupgrade.ReasonCodeV1
	var grantKind moduleupgrade.RequiredGrantKindV1
	switch current.ExecutionClass {
	case moduleapi.ExecutionTrustedInProcess:
		grantKind = moduleupgrade.GrantTrustedInProcessArtifactV1
	case moduleapi.ExecutionLocalProcess:
		grantKind = moduleupgrade.GrantLocalProcessArtifactV1
	case moduleapi.ExecutionRemote:
		grantKind = moduleupgrade.GrantRemoteActionArtifactV1
	case moduleapi.ExecutionWASM:
		grantKind = moduleupgrade.GrantWASMActionArtifactV1
	}
	if grantKind != "" {
		grants = []moduleupgrade.RequiredGrantV1{{Kind: grantKind, ReferenceDigest: targetDigest}}
		reasons = []moduleupgrade.ReasonCodeV1{moduleupgrade.ReasonOperatorGrantRequiredV1}
	}
	supply := moduleupgrade.SupplyBasisV1{SourceID: basis.Source.SourceID, SourcePolicyID: basis.Source.PolicyID, SourcePolicyRevision: basis.Source.PolicyRevision, SnapshotID: basis.Snapshot.SnapshotID, IndexID: basis.Snapshot.IndexID, ObservationRevision: basis.Snapshot.ObservationRevision, SignatureStatus: moduleupgrade.SignatureNotRequiredV1}
	if signed {
		supply.SignatureStatus = moduleupgrade.SignatureVerifiedV1
		supply.PublisherKeyID = keyID
		supply.PublisherKeyRevision = basis.PublisherKey.Revision
	}
	review := moduleupgrade.ReviewV1{SchemaVersion: moduleupgrade.ReviewSchemaVersionV1, CandidateID: basis.CandidateID, ReviewKey: basis.Candidate.ReviewKey, TenantID: tenantID, BindingTarget: target, Port: port, PortBindingIndex: portIndex, TargetInstanceID: selection.TargetInstanceID, SupplyBasis: supply, PublishedBasis: basis.PublishedBasis, Current: moduleupgrade.CurrentExactV1{Activation: current, InstallationID: installation.InstallationID, ManifestRef: installation.ManifestRef, Manifest: backupManifestSummary(currentManifest)}, Target: moduleupgrade.TargetEvidenceV1{Module: targetEntry.Module, ArtifactDigest: targetDigest, ArtifactSizeBytes: targetEntry.ArtifactSizeBytes, SignatureID: targetEntry.SignatureID, ManifestRef: targetManifestRef, Manifest: backupManifestSummary(targetManifest)}, Handler: moduleupgrade.HandlerAssessmentV1{Status: moduleupgrade.HandlerSupportedV1, Kind: "backup.handler", ExecutionClass: current.ExecutionClass, AdapterIdentity: current.AdapterIdentity}, BindingImpacts: basis.BindingImpacts, RequiredGrants: grants, Conclusion: moduleupgrade.ConclusionWouldApplyV1, ReasonCodes: reasons}
	_, canonical, _, err := moduleupgrade.NewReviewV1(review, basis.CandidateCanonical, basis.Snapshot.SnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return basis, canonical, targetManifestCanonical, keyID
}

func backupManifestSummary(manifest moduleapi.ModuleManifestV1) moduleupgrade.ManifestSummaryV1 {
	provides := append([]moduleapi.PortRef(nil), manifest.Provides...)
	requires := append([]moduleapi.PortRef(nil), manifest.Requires...)
	permissions := append([]moduleapi.Permission(nil), manifest.RequestedPermissions...)
	sort.Slice(provides, func(i, j int) bool {
		return provides[i].Name+"\x00"+provides[i].ExactVersion < provides[j].Name+"\x00"+provides[j].ExactVersion
	})
	sort.Slice(requires, func(i, j int) bool {
		return requires[i].Name+"\x00"+requires[i].ExactVersion < requires[j].Name+"\x00"+requires[j].ExactVersion
	})
	sort.Slice(permissions, func(i, j int) bool { return permissions[i] < permissions[j] })
	if provides == nil {
		provides = []moduleapi.PortRef{}
	}
	if requires == nil {
		requires = []moduleapi.PortRef{}
	}
	if permissions == nil {
		permissions = []moduleapi.Permission{}
	}
	return moduleupgrade.ManifestSummaryV1{Module: moduleapi.Ref{ID: manifest.ID, Version: manifest.Version}, Runtime: moduleupgrade.RuntimeSummaryV1{Mode: manifest.Runtime.Mode, Protocol: manifest.Runtime.Protocol, Entrypoint: manifest.Runtime.Entrypoint}, Provides: provides, Requires: requires, RequestedPermissions: permissions}
}
