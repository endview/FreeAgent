package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"math"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleUpgradeServerOwnedReviewResultSchemaV1 = "freeagent.module-upgrade-review-server-owned-result/v1"

// moduleUpgradeServerOwnedReviewCommandRequestV1 contains only tenant/scope
// selectors and immutable identities. The artifact root is an application
// dependency, never a caller-selected package or artifact path.
type moduleUpgradeServerOwnedReviewCommandRequestV1 struct {
	DatabasePath        string
	TenantID            string
	AdmissionID         string
	TargetInstanceID    string
	OperatorPrincipalID string
	ReviewRequestDigest string
	Port                moduleapi.PortRef
	PortBindingIndex    uint32
	BindingTarget       moduleUpgradeBindingTargetFlagsV1
}

type moduleUpgradeServerOwnedReviewDependenciesV1 struct {
	openStore    func(context.Context, string) (*currentstore.Store, error)
	artifactRoot func(string) string
}

type moduleUpgradeServerOwnedDecisionCommandRequestV1 struct {
	DatabasePath            string
	TenantID                string
	ReviewID                string
	Decision                moduleapi.ModuleCandidateDecisionValueV1
	OperatorPrincipalID     string
	ReasonPath              string
	ConfirmTenantWideReject bool
}

func runModuleUpgradeDecisionServerOwned(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("module-upgrade-decide-server-owned", io.Discard)
	enabled := flags.Bool("enable-module-upgrade-review", false, "enable this explicit server-owned upgrade decision")
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	reviewID := flags.String("review-id", "", "exact persisted Review identity")
	decision := flags.String("decision", "", "APPROVE or REJECT")
	operator := flags.String("operator-principal", "", "operator principal attribution")
	reasonPath := flags.String("reason-file", "", "bounded local file containing the exact decision reason")
	confirmReject := flags.Bool("confirm-tenant-wide-reject", false, "confirm tenant-wide rejection")
	if err := flags.Parse(args); err != nil || !*enabled {
		if !*enabled {
			return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureDisabledV1)
		}
		return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	request := moduleUpgradeServerOwnedDecisionCommandRequestV1{
		DatabasePath: *databasePath, TenantID: *tenantID, ReviewID: *reviewID,
		Decision: moduleapi.ModuleCandidateDecisionValueV1(*decision), OperatorPrincipalID: *operator,
		ReasonPath: *reasonPath, ConfirmTenantWideReject: *confirmReject,
	}
	if ctx == nil || flags.NArg() != 0 || !exactNonEmptyFlag(request.DatabasePath) ||
		validateModuleApplyOpaqueIDV1("tenant_id", request.TenantID) != nil ||
		!moduleapi.ValidSHA256(request.ReviewID) ||
		(request.Decision != moduleapi.ModuleCandidateDecisionApproveV1 && request.Decision != moduleapi.ModuleCandidateDecisionRejectV1) ||
		validateModuleApplyOpaqueIDV1("operator_principal_id", request.OperatorPrincipalID) != nil ||
		!exactNonEmptyFlag(request.ReasonPath) {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	for _, name := range []string{"db", "tenant", "review-id", "decision", "operator-principal", "reason-file"} {
		if !flagWasExplicitlySetV1(flags, name) {
			return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInvalidFlagsV1)
		}
	}
	confirmExplicit := flagWasExplicitlySetV1(flags, "confirm-tenant-wide-reject")
	if request.Decision == moduleapi.ModuleCandidateDecisionRejectV1 {
		if !confirmExplicit || !request.ConfirmTenantWideReject {
			return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInvalidFlagsV1)
		}
	} else if confirmExplicit {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	result, err := executeModuleUpgradeServerOwnedDecisionV1(ctx, request)
	if err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeServerOwnedFailureV1(err))
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide-server-owned", moduleUpgradeFailureInternalV1)
	}
	return nil
}

func executeModuleUpgradeServerOwnedDecisionV1(
	ctx context.Context,
	request moduleUpgradeServerOwnedDecisionCommandRequestV1,
) (any, error) {
	store, err := currentstore.OpenExistingCurrentStore(ctx, request.DatabasePath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	review, err := store.GetModuleUpgradeReview(ctx, request.ReviewID)
	if err != nil {
		return nil, err
	}
	if review.Review.ArtifactAdmissionID == "" || review.Review.TenantID != request.TenantID {
		return nil, currentstore.ErrModuleUpgradeReviewConflict
	}
	reasonBytes, err := readModuleVerifyCanonicalFile(ctx, request.ReasonPath)
	if err != nil {
		return nil, err
	}
	decision, canonical, decisionID, err := moduleapi.NewModuleCandidateDecisionV1(moduleapi.ModuleCandidateDecisionV1{
		SchemaVersion: moduleapi.ModuleCandidateDecisionSchemaVersionV1,
		CandidateID:   review.Candidate.CandidateID, Decision: request.Decision,
		OperatorPrincipalID: request.OperatorPrincipalID, Reason: string(reasonBytes),
	})
	if err != nil {
		return nil, err
	}
	committed, err := store.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{
		TenantID: request.TenantID, ReviewID: request.ReviewID, DecisionCanonical: canonical,
		ConfirmTenantWideReject: request.ConfirmTenantWideReject,
	})
	if err != nil {
		return nil, err
	}
	if committed.DecisionID != decisionID || committed.Decision != decision {
		return nil, currentstore.ErrModuleUpgradeIntegrity
	}
	status := "RECORDED"
	if request.Decision == moduleapi.ModuleCandidateDecisionRejectV1 {
		status = "TENANT_WIDE_REJECT_RECORDED"
	}
	return safeModuleUpgradeCommandResultV1(moduleUpgradeDecisionResultSchemaV1, status, request.TenantID, review.Candidate.CandidateID, request.ReviewID, decisionID), nil
}

func productionModuleUpgradeServerOwnedReviewDependenciesV1() moduleUpgradeServerOwnedReviewDependenciesV1 {
	return moduleUpgradeServerOwnedReviewDependenciesV1{
		openStore: currentstore.OpenExistingCurrentStore,
		artifactRoot: func(databasePath string) string {
			return resolvedArtifactRoot(databasePath, "")
		},
	}
}

func runModuleUpgradeReviewServerOwned(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runModuleUpgradeReviewServerOwnedWithDependenciesV1(
		ctx, args, stdout, stderr,
		productionModuleUpgradeServerOwnedReviewDependenciesV1(),
	)
}

func runModuleUpgradeReviewServerOwnedWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleUpgradeServerOwnedReviewDependenciesV1,
) (returnErr error) {
	flags := newFlagSet("module-upgrade-review-server-owned", io.Discard)
	enabled := flags.Bool("enable-module-upgrade-review", false, "enable this explicit server-owned upgrade review")
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	admissionID := flags.String("admission-id", "", "exact server-owned Artifact Admission identity")
	targetInstanceID := flags.String("target-instance", "", "new exact target Instance identity")
	operatorPrincipalID := flags.String("operator-principal", "", "operator attribution for the Review")
	reviewRequestDigest := flags.String("review-request-digest", "", "explicit SHA-256 idempotency basis for this Review request")
	portName := flags.String("port", "", "exact selected Port name")
	portVersion := flags.String("port-version", "", "exact selected Port version")
	portBindingIndex := flags.Uint64("port-binding-index", 0, "zero-based selected binding ordinal for this exact Port")
	targetKind := flags.String("target-kind", "", "PROFILE or WORKSPACE_CHANNEL_ENDPOINT")
	profileID := flags.String("profile", "", "exact Profile identity for PROFILE")
	workspaceID := flags.String("workspace", "", "exact Workspace identity for WORKSPACE_CHANNEL_ENDPOINT")
	endpointID := flags.String("endpoint", "", "exact Channel Endpoint identity for WORKSPACE_CHANNEL_ENDPOINT")
	if err := flags.Parse(args); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureDisabledV1)
	}
	if *portBindingIndex > math.MaxUint32 {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	request := moduleUpgradeServerOwnedReviewCommandRequestV1{
		DatabasePath:        *databasePath,
		TenantID:            *tenantID,
		AdmissionID:         *admissionID,
		TargetInstanceID:    *targetInstanceID,
		OperatorPrincipalID: *operatorPrincipalID,
		ReviewRequestDigest: *reviewRequestDigest,
		Port: moduleapi.PortRef{
			Name:         *portName,
			ExactVersion: *portVersion,
		},
		PortBindingIndex: uint32(*portBindingIndex),
		BindingTarget: moduleUpgradeBindingTargetFlagsV1{
			Kind:        *targetKind,
			ProfileID:   *profileID,
			WorkspaceID: *workspaceID,
			EndpointID:  *endpointID,
		},
	}
	if err := validateModuleUpgradeServerOwnedReviewRequestV1(ctx, flags, request, dependencies); err != nil {
		return err
	}
	result, err := executeModuleUpgradeServerOwnedReviewV1(ctx, request, dependencies)
	if err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeServerOwnedFailureV1(err))
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInternalV1)
	}
	return nil
}

func validateModuleUpgradeServerOwnedReviewRequestV1(
	ctx context.Context,
	flags *flag.FlagSet,
	request moduleUpgradeServerOwnedReviewCommandRequestV1,
	dependencies moduleUpgradeServerOwnedReviewDependenciesV1,
) error {
	invalid := ctx == nil || flags.NArg() != 0 || dependencies.openStore == nil ||
		dependencies.artifactRoot == nil || !exactNonEmptyFlag(request.DatabasePath) ||
		validateModuleApplyOpaqueIDV1("tenant_id", request.TenantID) != nil ||
		!moduleapi.ValidSHA256(request.AdmissionID) ||
		validateModuleApplyOpaqueIDV1("target_instance_id", request.TargetInstanceID) != nil ||
		validateModuleApplyOpaqueIDV1("operator_principal_id", request.OperatorPrincipalID) != nil ||
		!moduleapi.ValidSHA256(request.ReviewRequestDigest) || request.Port.Validate() != nil
	if invalid {
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	for _, name := range []string{
		"db", "tenant", "admission-id", "target-instance", "operator-principal",
		"review-request-digest", "port", "port-version", "port-binding-index", "target-kind",
	} {
		if !flagWasExplicitlySetV1(flags, name) {
			return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
		}
	}
	switch request.BindingTarget.Kind {
	case "PROFILE":
		if validateModuleApplyOpaqueIDV1("profile_id", request.BindingTarget.ProfileID) != nil ||
			request.BindingTarget.WorkspaceID != "" || request.BindingTarget.EndpointID != "" ||
			flagWasExplicitlySetV1(flags, "workspace") || flagWasExplicitlySetV1(flags, "endpoint") {
			return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
		}
	case "WORKSPACE_CHANNEL_ENDPOINT":
		if request.BindingTarget.ProfileID != "" || flagWasExplicitlySetV1(flags, "profile") ||
			validateModuleApplyOpaqueIDV1("workspace_id", request.BindingTarget.WorkspaceID) != nil ||
			validateModuleApplyOpaqueIDV1("endpoint_id", request.BindingTarget.EndpointID) != nil ||
			request.PortBindingIndex != 0 {
			return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
		}
	default:
		return moduleUpgradeCommandFailureV1("module-upgrade-review-server-owned", moduleUpgradeFailureInvalidFlagsV1)
	}
	return nil
}

func executeModuleUpgradeServerOwnedReviewV1(
	ctx context.Context,
	request moduleUpgradeServerOwnedReviewCommandRequestV1,
	dependencies moduleUpgradeServerOwnedReviewDependenciesV1,
) (result moduleUpgradeReviewCommandResultV1, returnErr error) {
	store, err := dependencies.openStore(ctx, request.DatabasePath)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()

	admission, err := store.GetModuleArtifactAdmissionV1(ctx, request.AdmissionID)
	if err != nil {
		return result, err
	}
	if admission.Record.Module != admission.Artifact.Module ||
		admission.Record.ArtifactDigest != admission.Artifact.ArtifactDigest {
		return result, currentstore.ErrModuleArtifactIngressIntegrity
	}
	if err := moduleartifactstore.VerifyExistingV1(
		ctx,
		dependencies.artifactRoot(request.DatabasePath),
		admission.Artifact.ArtifactDigest,
		admission.Artifact.ArtifactSizeBytes,
		admission.Artifact.CoveredFileCount,
		moduleartifactstore.ExistingModeInertOnlyV1,
	); err != nil {
		return result, errors.Join(currentstore.ErrModuleArtifactIngressIntegrity, err)
	}

	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, currentstore.ReadModuleUpgradeReviewBasisInput{
		SourceID:             admission.Record.SourceID,
		SnapshotID:           admission.Record.SnapshotID,
		TenantID:             request.TenantID,
		ArtifactAdmissionID:  admission.AdmissionID,
		BindingTarget:        moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetKindV1(request.BindingTarget.Kind), ProfileID: request.BindingTarget.ProfileID, WorkspaceID: request.BindingTarget.WorkspaceID, EndpointID: request.BindingTarget.EndpointID},
		Port:                 request.Port,
		PortBindingIndex:     request.PortBindingIndex,
		TargetModule:         admission.Artifact.Module,
		TargetArtifactDigest: admission.Artifact.ArtifactDigest,
		TargetInstanceID:     request.TargetInstanceID,
	})
	if err != nil {
		return result, err
	}
	targetManifest, targetCanonical, err := moduleapi.ParseModuleManifestV1(bytes.Clone(admission.Artifact.ManifestCanonical))
	if err != nil || !bytes.Equal(targetCanonical, admission.Artifact.ManifestCanonical) {
		return result, errors.Join(currentstore.ErrModuleArtifactIngressIntegrity, err)
	}
	currentManifest, currentCanonical, err := moduleapi.ParseModuleManifestV1(bytes.Clone(basis.CurrentInstallation.ManifestBytes))
	if err != nil || !bytes.Equal(currentCanonical, basis.CurrentInstallation.ManifestBytes) {
		return result, errors.Join(currentstore.ErrModuleUpgradeIntegrity, err)
	}
	handler, grants, conclusion, reasons, err := assessModuleUpgradeHandlerV1(ctx, store, basis, targetManifest, request.Port)
	if err != nil {
		return result, err
	}
	targetManifestRef := admission.Artifact.ManifestRef
	reviewInput := moduleupgrade.ReviewV1{
		SchemaVersion:       moduleupgrade.ReviewSchemaVersionV1,
		CandidateID:         basis.CandidateID,
		ReviewKey:           basis.Candidate.ReviewKey,
		TenantID:            request.TenantID,
		ArtifactAdmissionID: admission.AdmissionID,
		OperatorPrincipalID: request.OperatorPrincipalID,
		ReviewRequestDigest: request.ReviewRequestDigest,
		BindingTarget:       basis.Selection.BindingTarget,
		Port:                request.Port,
		PortBindingIndex:    request.PortBindingIndex,
		TargetInstanceID:    request.TargetInstanceID,
		SupplyBasis:         moduleUpgradeSupplyBasisV1(basis),
		PublishedBasis:      basis.PublishedBasis,
		Current:             moduleupgrade.CurrentExactV1{Activation: moduleUpgradeCurrentActivationV1(basis), InstallationID: basis.CurrentInstallation.InstallationID, ManifestRef: basis.CurrentInstallation.ManifestRef, Manifest: moduleUpgradeManifestSummaryV1(currentManifest)},
		Target:              moduleupgrade.TargetEvidenceV1{Module: admission.Artifact.Module, ArtifactDigest: admission.Artifact.ArtifactDigest, ArtifactSizeBytes: admission.Artifact.ArtifactSizeBytes, SignatureID: basis.TargetEntry.SignatureID, ManifestRef: targetManifestRef, Manifest: moduleUpgradeManifestSummaryV1(targetManifest)},
		Handler:             handler, BindingImpacts: append([]moduleupgrade.BindingImpactV1(nil), basis.BindingImpacts...), RequiredGrants: grants, Conclusion: conclusion, ReasonCodes: reasons,
	}
	_, reviewCanonical, reviewID, err := moduleupgrade.NewReviewV1(reviewInput, basis.CandidateCanonical, basis.Snapshot.SnapshotCanonical)
	if err != nil {
		return result, err
	}
	committed, err := store.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{Basis: basis, ReviewCanonical: reviewCanonical, TargetManifestCanonical: admission.Artifact.ManifestCanonical})
	if err != nil {
		return result, err
	}
	if committed.ReviewID != reviewID || committed.Review.ArtifactAdmissionID != admission.AdmissionID {
		return result, currentstore.ErrModuleUpgradeIntegrity
	}
	return moduleUpgradeReviewCommandResultV1{SchemaVersion: moduleUpgradeServerOwnedReviewResultSchemaV1, Status: "RECORDED", CandidateID: committed.Candidate.CandidateID, ReviewID: committed.ReviewID, Review: committed.Review}, nil
}
