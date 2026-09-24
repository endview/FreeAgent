package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleUpgradeReviewResultSchemaV1   = "freeagent.module-upgrade-review-command-result/v1"
	moduleUpgradeDecisionResultSchemaV1 = "freeagent.module-upgrade-decision-command-result/v1"
)

type moduleUpgradeCommandFailureCodeV1 string

const (
	moduleUpgradeFailureDisabledV1          moduleUpgradeCommandFailureCodeV1 = "UPGRADE_REVIEW_DISABLED"
	moduleUpgradeFailureInvalidFlagsV1      moduleUpgradeCommandFailureCodeV1 = "INVALID_FLAGS"
	moduleUpgradeFailureInternalV1          moduleUpgradeCommandFailureCodeV1 = "INTERNAL_ERROR"
	moduleUpgradeFailureAdmissionNotFoundV1 moduleUpgradeCommandFailureCodeV1 = "ADMISSION_NOT_FOUND"
	moduleUpgradeFailureArtifactTamperedV1  moduleUpgradeCommandFailureCodeV1 = "ARTIFACT_TAMPERED"
	moduleUpgradeFailureTenantConflictV1    moduleUpgradeCommandFailureCodeV1 = "TENANT_CONFLICT"
	moduleUpgradeFailureReviewStaleV1       moduleUpgradeCommandFailureCodeV1 = "REVIEW_STALE"
	moduleUpgradeFailureSourceStaleV1       moduleUpgradeCommandFailureCodeV1 = "SOURCE_STALE"
	moduleUpgradeFailureReviewIntegrityV1   moduleUpgradeCommandFailureCodeV1 = "REVIEW_INTEGRITY"
)

type moduleUpgradeBindingTargetFlagsV1 struct {
	Kind        string
	ProfileID   string
	WorkspaceID string
	EndpointID  string
}

// These command-local requests contain only exact identities and host-local
// input locations. They are deliberately not a second Review or Decision
// contract. The production adapter converts them to internal/moduleupgrade
// and Current Store types after those packages have reconstructed authority.
type moduleUpgradeReviewCommandRequestV1 struct {
	DatabasePath              string
	TenantID                  string
	ExpectedPointerRevision   uint64
	SourceID                  string
	SnapshotID                string
	TargetModuleID            string
	TargetExactVersion        string
	TargetArtifactDigest      string
	TargetInstanceID          string
	CurrentInstanceID         string
	CurrentActivationID       string
	CurrentModuleID           string
	CurrentExactVersion       string
	CurrentArtifactDigest     string
	Port                      moduleapi.PortRef
	PortBindingIndex          uint32
	BindingTarget             moduleUpgradeBindingTargetFlagsV1
	ArtifactDirectory         string
	SignaturePath             string
	OperatorRequestedRollback bool
}

type moduleUpgradeDecideCommandRequestV1 struct {
	DatabasePath            string
	TenantID                string
	ReviewID                string
	CandidateID             string
	Decision                moduleapi.ModuleCandidateDecisionValueV1
	OperatorPrincipalID     string
	ReasonPath              string
	ConfirmTenantWideReject bool
}

// The handlers are injected so the disabled and invalid-input paths can be
// proven to perform no Store, filesystem, network, Registry, Host, Apply, or
// runtime work. The production implementations are wired only after the
// shared U3 contract and Store admission APIs are available.
type moduleUpgradeReviewDependenciesV1 struct {
	review func(context.Context, moduleUpgradeReviewCommandRequestV1) (any, error)
}

type moduleUpgradeDecideDependenciesV1 struct {
	decide func(context.Context, moduleUpgradeDecideCommandRequestV1) (any, error)
}

type moduleUpgradeStoreV1 interface {
	ReadModuleUpgradeReviewBasis(context.Context, currentstore.ReadModuleUpgradeReviewBasisInput) (currentstore.ModuleUpgradeReviewBasis, error)
	CommitModuleUpgradeReview(context.Context, currentstore.CommitModuleUpgradeReviewInput) (currentstore.ModuleUpgradeReviewRecord, error)
	GetModuleUpgradeReview(context.Context, string) (currentstore.ModuleUpgradeReviewRecord, error)
	DecideModuleCandidate(context.Context, currentstore.DecideModuleCandidateInput) (currentstore.ModuleCandidateDecisionRecord, error)
	Close() error
}

func productionModuleUpgradeReviewDependenciesV1() moduleUpgradeReviewDependenciesV1 {
	return moduleUpgradeReviewDependenciesV1{review: executeModuleUpgradeReviewV1}
}

func productionModuleUpgradeDecideDependenciesV1() moduleUpgradeDecideDependenciesV1 {
	return moduleUpgradeDecideDependenciesV1{decide: executeModuleUpgradeDecisionV1}
}

func runModuleUpgradeReview(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runModuleUpgradeReviewWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModuleUpgradeReviewDependenciesV1(),
	)
}

func runModuleUpgradeReviewWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleUpgradeReviewDependenciesV1,
) error {
	flags := newFlagSet("module-upgrade-review", io.Discard)
	enabled := flags.Bool("enable-module-upgrade-review", false, "enable this explicit upgrade review")
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	expectedPointer := flags.Uint64("expected-pointer-revision", 0, "expected current published pointer revision")
	sourceID := flags.String("source-id", "", "exact registered Source identity")
	snapshotID := flags.String("snapshot-id", "", "exact immutable discovery Snapshot identity")
	targetModuleID := flags.String("target-module", "", "exact target Module identity")
	targetVersion := flags.String("target-version", "", "exact opaque target version")
	targetDigest := flags.String("target-artifact-digest", "", "exact target artifact digest")
	targetInstance := flags.String("target-instance", "", "new exact target Instance identity")
	currentInstance := flags.String("current-instance", "", "exact current Catalog Instance identity")
	currentActivation := flags.String("current-activation", "", "exact current Activation identity")
	currentModuleID := flags.String("current-module", "", "exact current Module identity")
	currentVersion := flags.String("current-version", "", "exact opaque current version")
	currentDigest := flags.String("current-artifact-digest", "", "exact current artifact digest")
	portName := flags.String("port", "", "exact selected Port name")
	portVersion := flags.String("port-version", "", "exact selected Port version")
	portBindingIndex := flags.Uint64("port-binding-index", 0, "zero-based selected binding ordinal for this exact Port")
	targetKind := flags.String("target-kind", "", "PROFILE or WORKSPACE_CHANNEL_ENDPOINT")
	profileID := flags.String("profile", "", "exact Profile identity for PROFILE")
	workspaceID := flags.String("workspace", "", "exact Workspace identity for WORKSPACE_CHANNEL_ENDPOINT")
	endpointID := flags.String("endpoint", "", "exact Channel Endpoint identity for WORKSPACE_CHANNEL_ENDPOINT")
	artifactDirectory := flags.String("artifact-directory", "", "explicit unpacked local target artifact")
	signaturePath := flags.String("signature", "", "explicit exact detached signature file for a signed Source")
	operatorRequestedRollback := flags.Bool("operator-requested-rollback", false, "record an explicit Operator rollback intent in the inert review")
	if err := flags.Parse(args); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureDisabledV1)
	}
	request := moduleUpgradeReviewCommandRequestV1{
		DatabasePath:            *databasePath,
		TenantID:                *tenantID,
		ExpectedPointerRevision: *expectedPointer,
		SourceID:                *sourceID,
		SnapshotID:              *snapshotID,
		TargetModuleID:          *targetModuleID,
		TargetExactVersion:      *targetVersion,
		TargetArtifactDigest:    *targetDigest,
		TargetInstanceID:        *targetInstance,
		CurrentInstanceID:       *currentInstance,
		CurrentActivationID:     *currentActivation,
		CurrentModuleID:         *currentModuleID,
		CurrentExactVersion:     *currentVersion,
		CurrentArtifactDigest:   *currentDigest,
		Port: moduleapi.PortRef{
			Name:         *portName,
			ExactVersion: *portVersion,
		},
		BindingTarget: moduleUpgradeBindingTargetFlagsV1{
			Kind:        *targetKind,
			ProfileID:   *profileID,
			WorkspaceID: *workspaceID,
			EndpointID:  *endpointID,
		},
		ArtifactDirectory:         *artifactDirectory,
		SignaturePath:             *signaturePath,
		OperatorRequestedRollback: *operatorRequestedRollback,
	}
	if *portBindingIndex > math.MaxUint32 {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	request.PortBindingIndex = uint32(*portBindingIndex)
	if err := validateModuleUpgradeReviewCommandRequestV1(ctx, flags, request, dependencies); err != nil {
		return err
	}
	result, err := dependencies.review(ctx, request)
	if err != nil || result == nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInternalV1)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInternalV1)
	}
	return nil
}

func validateModuleUpgradeReviewCommandRequestV1(
	ctx context.Context,
	flags *flag.FlagSet,
	request moduleUpgradeReviewCommandRequestV1,
	dependencies moduleUpgradeReviewDependenciesV1,
) error {
	invalid := ctx == nil || flags.NArg() != 0 || dependencies.review == nil ||
		!exactNonEmptyFlag(request.DatabasePath) ||
		validateModuleApplyOpaqueIDV1("tenant_id", request.TenantID) != nil ||
		request.ExpectedPointerRevision == 0 ||
		validateModuleApplyOpaqueIDV1("source_id", request.SourceID) != nil ||
		!moduleapi.ValidSHA256(request.SnapshotID) ||
		(moduleapi.Ref{ID: request.TargetModuleID, Version: request.TargetExactVersion}).Validate() != nil ||
		!moduleapi.ValidSHA256(request.TargetArtifactDigest) ||
		validateModuleApplyOpaqueIDV1("target_instance_id", request.TargetInstanceID) != nil ||
		validateModuleApplyOpaqueIDV1("current_instance_id", request.CurrentInstanceID) != nil ||
		validateModuleApplyOpaqueIDV1("current_activation_id", request.CurrentActivationID) != nil ||
		(moduleapi.Ref{ID: request.CurrentModuleID, Version: request.CurrentExactVersion}).Validate() != nil ||
		!moduleapi.ValidSHA256(request.CurrentArtifactDigest) ||
		request.TargetModuleID != request.CurrentModuleID ||
		request.TargetExactVersion == request.CurrentExactVersion ||
		request.TargetArtifactDigest == request.CurrentArtifactDigest ||
		request.TargetInstanceID == request.CurrentInstanceID ||
		request.Port.Validate() != nil ||
		!exactNonEmptyFlag(request.ArtifactDirectory) ||
		(runtime.GOOS == "windows" && unsafeModuleVerifyWindowsPath(request.ArtifactDirectory))
	if invalid || !allModuleUpgradeReviewFlagsExplicitV1(flags) {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	switch request.BindingTarget.Kind {
	case "PROFILE":
		if validateModuleApplyOpaqueIDV1("profile_id", request.BindingTarget.ProfileID) != nil ||
			request.BindingTarget.WorkspaceID != "" || request.BindingTarget.EndpointID != "" ||
			flagWasExplicitlySetV1(flags, "workspace") || flagWasExplicitlySetV1(flags, "endpoint") {
			return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
		}
	case "WORKSPACE_CHANNEL_ENDPOINT":
		if request.BindingTarget.ProfileID != "" || flagWasExplicitlySetV1(flags, "profile") ||
			validateModuleApplyOpaqueIDV1("workspace_id", request.BindingTarget.WorkspaceID) != nil ||
			validateModuleApplyOpaqueIDV1("endpoint_id", request.BindingTarget.EndpointID) != nil ||
			request.PortBindingIndex != 0 {
			return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
		}
	default:
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	if request.SignaturePath != "" && !exactNonEmptyFlag(request.SignaturePath) {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	if runtime.GOOS == "windows" && request.SignaturePath != "" && unsafeModuleVerifyWindowsPath(request.SignaturePath) {
		return moduleUpgradeCommandFailureV1("module-upgrade-review", moduleUpgradeFailureInvalidFlagsV1)
	}
	return nil
}

func allModuleUpgradeReviewFlagsExplicitV1(flags *flag.FlagSet) bool {
	required := [...]string{
		"db", "tenant", "expected-pointer-revision", "source-id", "snapshot-id",
		"target-module", "target-version", "target-artifact-digest", "target-instance",
		"current-instance", "current-activation", "current-module", "current-version",
		"current-artifact-digest", "port", "port-version", "port-binding-index",
		"target-kind", "artifact-directory",
	}
	for _, name := range required {
		if !flagWasExplicitlySetV1(flags, name) {
			return false
		}
	}
	return true
}

func runModuleUpgradeDecide(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runModuleUpgradeDecideWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModuleUpgradeDecideDependenciesV1(),
	)
}

func runModuleUpgradeDecideWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleUpgradeDecideDependenciesV1,
) error {
	flags := newFlagSet("module-upgrade-decide", io.Discard)
	enabled := flags.Bool("enable-module-upgrade-review", false, "enable this explicit upgrade decision")
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	reviewID := flags.String("review-id", "", "exact review identity")
	candidateID := flags.String("candidate-id", "", "exact Candidate identity")
	decision := flags.String("decision", "", "APPROVE or REJECT")
	operator := flags.String("operator-principal", "", "Operator principal attribution; not authorization")
	reasonPath := flags.String("reason-file", "", "bounded local file containing the exact decision reason")
	confirmTenantWideReject := flags.Bool("confirm-tenant-wide-reject", false, "confirm that REJECT suppresses this ReviewKey across every scope in this Tenant")
	if err := flags.Parse(args); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureDisabledV1)
	}
	request := moduleUpgradeDecideCommandRequestV1{
		DatabasePath:            *databasePath,
		TenantID:                *tenantID,
		ReviewID:                *reviewID,
		CandidateID:             *candidateID,
		Decision:                moduleapi.ModuleCandidateDecisionValueV1(*decision),
		OperatorPrincipalID:     *operator,
		ReasonPath:              *reasonPath,
		ConfirmTenantWideReject: *confirmTenantWideReject,
	}
	required := [...]string{"db", "tenant", "review-id", "candidate-id", "decision", "operator-principal", "reason-file"}
	for _, name := range required {
		if !flagWasExplicitlySetV1(flags, name) {
			return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInvalidFlagsV1)
		}
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.decide == nil ||
		!exactNonEmptyFlag(request.DatabasePath) ||
		validateModuleApplyOpaqueIDV1("tenant_id", request.TenantID) != nil ||
		!moduleapi.ValidSHA256(request.ReviewID) || !moduleapi.ValidSHA256(request.CandidateID) ||
		(request.Decision != moduleapi.ModuleCandidateDecisionApproveV1 && request.Decision != moduleapi.ModuleCandidateDecisionRejectV1) ||
		validateModuleApplyOpaqueIDV1("operator_principal_id", request.OperatorPrincipalID) != nil ||
		!exactNonEmptyFlag(request.ReasonPath) {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInvalidFlagsV1)
	}
	confirmExplicit := flagWasExplicitlySetV1(flags, "confirm-tenant-wide-reject")
	if request.Decision == moduleapi.ModuleCandidateDecisionRejectV1 {
		if !confirmExplicit || !request.ConfirmTenantWideReject {
			return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInvalidFlagsV1)
		}
	} else if confirmExplicit {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInvalidFlagsV1)
	}
	result, err := dependencies.decide(ctx, request)
	if err != nil || result == nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInternalV1)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return moduleUpgradeCommandFailureV1("module-upgrade-decide", moduleUpgradeFailureInternalV1)
	}
	return nil
}

func moduleUpgradeCommandFailureV1(command string, code moduleUpgradeCommandFailureCodeV1) error {
	return fmt.Errorf("freeagent %s: failed (%s)", command, code)
}

func moduleUpgradeServerOwnedFailureV1(err error) moduleUpgradeCommandFailureCodeV1 {
	switch {
	case errors.Is(err, currentstore.ErrModuleArtifactAdmissionNotFound):
		return moduleUpgradeFailureAdmissionNotFoundV1
	case errors.Is(err, currentstore.ErrModuleArtifactIngressIntegrity):
		return moduleUpgradeFailureArtifactTamperedV1
	case errors.Is(err, currentstore.ErrModuleUpgradeReviewStale):
		return moduleUpgradeFailureReviewStaleV1
	case errors.Is(err, currentstore.ErrModuleSourceNotFound),
		errors.Is(err, currentstore.ErrModuleDiscoverySnapshotNotFound),
		errors.Is(err, currentstore.ErrModulePublisherKeyRevoked):
		return moduleUpgradeFailureSourceStaleV1
	case errors.Is(err, currentstore.ErrModuleUpgradeReviewConflict),
		errors.Is(err, currentstore.ErrModuleUpgradeSuppressed):
		return moduleUpgradeFailureTenantConflictV1
	case errors.Is(err, currentstore.ErrModuleUpgradeIntegrity),
		errors.Is(err, currentstore.ErrInvalidModuleUpgradeReview):
		return moduleUpgradeFailureReviewIntegrityV1
	default:
		return moduleUpgradeFailureInternalV1
	}
}

func executeModuleUpgradeReviewV1(
	ctx context.Context,
	request moduleUpgradeReviewCommandRequestV1,
) (result any, returnErr error) {
	store, err := currentstore.OpenExistingCurrentStore(ctx, request.DatabasePath)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	target := moduleupgrade.BindingTargetV1{
		Kind:        moduleupgrade.BindingTargetKindV1(request.BindingTarget.Kind),
		ProfileID:   request.BindingTarget.ProfileID,
		WorkspaceID: request.BindingTarget.WorkspaceID,
		EndpointID:  request.BindingTarget.EndpointID,
	}
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, currentstore.ReadModuleUpgradeReviewBasisInput{
		SourceID:             request.SourceID,
		SnapshotID:           request.SnapshotID,
		TenantID:             request.TenantID,
		BindingTarget:        target,
		Port:                 request.Port,
		PortBindingIndex:     request.PortBindingIndex,
		TargetModule:         moduleapi.Ref{ID: request.TargetModuleID, Version: request.TargetExactVersion},
		TargetArtifactDigest: request.TargetArtifactDigest,
		TargetInstanceID:     request.TargetInstanceID,
	})
	if err != nil {
		return nil, err
	}
	if basis.PublishedBasis.PointerRevision != request.ExpectedPointerRevision ||
		basis.CurrentActivation.InstanceID != request.CurrentInstanceID ||
		basis.CurrentActivation.ActivationID != request.CurrentActivationID ||
		basis.CurrentInstallation.ModuleID != request.CurrentModuleID ||
		basis.CurrentInstallation.ExactVersion != request.CurrentExactVersion ||
		basis.CurrentInstallation.ArtifactDigest != request.CurrentArtifactDigest {
		return nil, currentstore.ErrModuleUpgradeReviewStale
	}

	first, targetManifest, targetManifestCanonical, err := verifyModuleUpgradeTargetArtifactV1(
		ctx, request.ArtifactDirectory, basis.Source.Policy.MaxPackageBytes,
		moduleconformance.VerifyDirectoryWithPackageLimitEvidence,
	)
	if err != nil {
		return nil, err
	}
	if first.Module.ID != basis.TargetEntry.Module.ID ||
		first.Module.ExactVersion != basis.TargetEntry.Module.Version ||
		first.ArtifactDigest != basis.TargetEntry.ArtifactDigest ||
		first.ArtifactSizeBytes != basis.TargetEntry.ArtifactSizeBytes {
		return nil, errors.New("target artifact differs from exact Store observation")
	}
	if err := verifyModuleUpgradeDetachedSignatureV1(ctx, request.SignaturePath, basis, first); err != nil {
		return nil, err
	}
	targetManifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		targetManifestCanonical,
	)
	if err != nil {
		return nil, err
	}
	currentManifest, currentCanonical, err := moduleapi.ParseModuleManifestV1(
		basis.CurrentInstallation.ManifestBytes,
	)
	if err != nil || !bytes.Equal(currentCanonical, basis.CurrentInstallation.ManifestBytes) {
		return nil, errors.Join(err, errors.New("current manifest is invalid"))
	}
	handler, grants, conclusion, reasons, err := assessModuleUpgradeHandlerV1(
		ctx,
		store,
		basis,
		targetManifest,
		request.Port,
	)
	if err != nil {
		return nil, err
	}
	if request.OperatorRequestedRollback {
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonOperatorRequestedRollbackV1)
	}
	reviewInput := moduleupgrade.ReviewV1{
		SchemaVersion:    moduleupgrade.ReviewSchemaVersionV1,
		CandidateID:      basis.CandidateID,
		ReviewKey:        basis.Candidate.ReviewKey,
		TenantID:         request.TenantID,
		BindingTarget:    target,
		Port:             request.Port,
		PortBindingIndex: request.PortBindingIndex,
		TargetInstanceID: request.TargetInstanceID,
		SupplyBasis:      moduleUpgradeSupplyBasisV1(basis),
		PublishedBasis:   basis.PublishedBasis,
		Current: moduleupgrade.CurrentExactV1{
			Activation:     moduleUpgradeCurrentActivationV1(basis),
			InstallationID: basis.CurrentInstallation.InstallationID,
			ManifestRef:    basis.CurrentInstallation.ManifestRef,
			Manifest:       moduleUpgradeManifestSummaryV1(currentManifest),
		},
		Target: moduleupgrade.TargetEvidenceV1{
			Module:            basis.TargetEntry.Module,
			ArtifactDigest:    basis.TargetEntry.ArtifactDigest,
			ArtifactSizeBytes: basis.TargetEntry.ArtifactSizeBytes,
			SignatureID:       basis.TargetEntry.SignatureID,
			ManifestRef:       targetManifestRef,
			Manifest:          moduleUpgradeManifestSummaryV1(targetManifest),
		},
		Handler:        handler,
		BindingImpacts: append([]moduleupgrade.BindingImpactV1(nil), basis.BindingImpacts...),
		RequiredGrants: grants,
		Conclusion:     conclusion,
		ReasonCodes:    reasons,
	}
	_, reviewCanonical, reviewID, err := moduleupgrade.NewReviewV1(
		reviewInput,
		basis.CandidateCanonical,
		basis.Snapshot.SnapshotCanonical,
	)
	if err != nil {
		return nil, err
	}
	committed, err := store.CommitModuleUpgradeReview(ctx, currentstore.CommitModuleUpgradeReviewInput{
		Basis:                   basis,
		ReviewCanonical:         reviewCanonical,
		TargetManifestCanonical: targetManifestCanonical,
	})
	if err != nil {
		return nil, err
	}
	if committed.ReviewID != reviewID || committed.Candidate.CandidateID != basis.CandidateID {
		return nil, currentstore.ErrModuleUpgradeIntegrity
	}
	return moduleUpgradeReviewCommandResultV1{
		SchemaVersion: moduleUpgradeReviewResultSchemaV1,
		Status:        "RECORDED",
		CandidateID:   basis.CandidateID,
		ReviewID:      reviewID,
		Review:        committed.Review,
	}, nil
}

func executeModuleUpgradeDecisionV1(
	ctx context.Context,
	request moduleUpgradeDecideCommandRequestV1,
) (result any, returnErr error) {
	store, err := currentstore.OpenExistingCurrentStore(ctx, request.DatabasePath)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	review, err := store.GetModuleUpgradeReview(ctx, request.ReviewID)
	if err != nil {
		return nil, err
	}
	if review.Review.TenantID != request.TenantID || review.Candidate.CandidateID != request.CandidateID {
		return nil, currentstore.ErrModuleUpgradeReviewConflict
	}
	reasonBytes, err := readModuleVerifyCanonicalFile(ctx, request.ReasonPath)
	if err != nil {
		return nil, err
	}
	reason := string(reasonBytes)
	decision, canonical, decisionID, err := moduleapi.NewModuleCandidateDecisionV1(
		moduleapi.ModuleCandidateDecisionV1{
			SchemaVersion:       moduleapi.ModuleCandidateDecisionSchemaVersionV1,
			CandidateID:         request.CandidateID,
			Decision:            request.Decision,
			OperatorPrincipalID: request.OperatorPrincipalID,
			Reason:              reason,
		},
	)
	if err != nil {
		return nil, err
	}
	committed, err := store.DecideModuleCandidate(ctx, currentstore.DecideModuleCandidateInput{
		TenantID:                request.TenantID,
		ReviewID:                request.ReviewID,
		DecisionCanonical:       canonical,
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
	return safeModuleUpgradeCommandResultV1(
		moduleUpgradeDecisionResultSchemaV1,
		status,
		request.TenantID,
		request.CandidateID,
		request.ReviewID,
		decisionID,
	), nil
}

func moduleUpgradeCurrentActivationV1(
	basis currentstore.ModuleUpgradeReviewBasis,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           basis.CurrentInstallation.ModuleID,
		Version:            basis.CurrentInstallation.ExactVersion,
		ArtifactDigest:     basis.CurrentInstallation.ArtifactDigest,
		InstanceID:         basis.CurrentActivation.InstanceID,
		ExecutionClass:     basis.CurrentActivation.ExecutionClass,
		AdapterIdentity:    basis.CurrentActivation.AdapterIdentity,
		ActivationRevision: basis.CurrentActivation.ActivationRevision,
	}
}

func moduleUpgradeSupplyBasisV1(
	basis currentstore.ModuleUpgradeReviewBasis,
) moduleupgrade.SupplyBasisV1 {
	result := moduleupgrade.SupplyBasisV1{
		SourceID:             basis.Source.SourceID,
		SourcePolicyID:       basis.Source.PolicyID,
		SourcePolicyRevision: basis.Source.PolicyRevision,
		SnapshotID:           basis.Snapshot.SnapshotID,
		IndexID:              basis.Snapshot.IndexID,
		ObservationRevision:  basis.Snapshot.ObservationRevision,
		SignatureStatus:      moduleupgrade.SignatureNotRequiredV1,
	}
	if basis.Source.Policy.SignatureRequired && basis.PublisherKey != nil {
		result.PublisherKeyID = basis.PublisherKey.PublisherKeyID
		result.PublisherKeyRevision = basis.PublisherKey.Revision
		result.SignatureStatus = moduleupgrade.SignatureVerifiedV1
	}
	return result
}

func moduleUpgradeManifestSummaryV1(
	manifest moduleapi.ModuleManifestV1,
) moduleupgrade.ManifestSummaryV1 {
	return moduleupgrade.ManifestSummaryV1{
		Module: moduleapi.Ref{
			ID:      manifest.ID,
			Version: manifest.Version,
		},
		Runtime: moduleupgrade.RuntimeSummaryV1{
			Mode:       manifest.Runtime.Mode,
			Protocol:   manifest.Runtime.Protocol,
			Entrypoint: manifest.Runtime.Entrypoint,
		},
		Provides:             append([]moduleapi.PortRef(nil), manifest.Provides...),
		Requires:             append([]moduleapi.PortRef(nil), manifest.Requires...),
		RequestedPermissions: append([]moduleapi.Permission(nil), manifest.RequestedPermissions...),
	}
}

type moduleUpgradePackageEvidenceVerifyV1 func(
	context.Context,
	string,
	uint64,
) (moduleconformance.Report, []byte, error)

func verifyModuleUpgradeTargetArtifactV1(
	ctx context.Context,
	artifactDirectory string,
	maxPackageBytes uint64,
	verify moduleUpgradePackageEvidenceVerifyV1,
) (moduleconformance.Report, moduleapi.ModuleManifestV1, []byte, error) {
	if ctx == nil || verify == nil {
		return moduleconformance.Report{}, moduleapi.ModuleManifestV1{}, nil,
			errors.New("module upgrade target verification dependency is nil")
	}
	first, firstManifestCanonical, err := verify(
		ctx,
		artifactDirectory,
		maxPackageBytes,
	)
	if err != nil {
		return moduleconformance.Report{}, moduleapi.ModuleManifestV1{}, nil, err
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(firstManifestCanonical)
	if err != nil || !bytes.Equal(canonical, firstManifestCanonical) {
		return moduleconformance.Report{}, moduleapi.ModuleManifestV1{}, nil,
			errors.Join(err, errors.New("target manifest is not exact canonical v1"))
	}
	second, secondManifestCanonical, err := verify(
		ctx,
		artifactDirectory,
		maxPackageBytes,
	)
	if err != nil || !reflect.DeepEqual(first, second) ||
		!bytes.Equal(firstManifestCanonical, secondManifestCanonical) {
		return moduleconformance.Report{}, moduleapi.ModuleManifestV1{}, nil,
			errors.Join(err, errors.New("target artifact changed between independent scans"))
	}
	if manifest.ID != second.Module.ID || manifest.Version != second.Module.ExactVersion {
		return moduleconformance.Report{}, moduleapi.ModuleManifestV1{}, nil,
			errors.New("target manifest changed between independent scans")
	}
	return second, manifest, bytes.Clone(secondManifestCanonical), nil
}

func verifyModuleUpgradeDetachedSignatureV1(
	ctx context.Context,
	signaturePath string,
	basis currentstore.ModuleUpgradeReviewBasis,
	report moduleconformance.Report,
) error {
	if !basis.Source.Policy.SignatureRequired {
		if signaturePath != "" || basis.TargetEntry.SignatureID != "" || basis.PublisherKey != nil {
			return errors.New("unsigned Source forbids detached signature input")
		}
		return nil
	}
	if !exactNonEmptyFlag(signaturePath) ||
		basis.PublisherKey == nil || basis.PublisherKey.RevokedAt != nil ||
		basis.PublisherKey.PublisherKeyID != basis.Source.Policy.PublisherKeyID ||
		basis.TargetEntry.SignatureID == "" {
		return errors.New("signed Source requires an exact live key and detached signature")
	}
	canonical, err := readModuleVerifyCanonicalFile(ctx, signaturePath)
	if err != nil {
		return err
	}
	signature, ownedSignature, signatureID, err := moduleapi.ParseModuleSignatureV1(canonical)
	if err != nil || signatureID != basis.TargetEntry.SignatureID ||
		!bytes.Equal(ownedSignature, canonical) ||
		signature.ArtifactDigest != report.ArtifactDigest ||
		signature.PublisherKeyID != basis.PublisherKey.PublisherKeyID {
		return errors.Join(err, errors.New("detached signature differs from exact Store basis"))
	}
	publisher, ownedPublisher, publisherID, err := moduleapi.ParseModulePublisherKeyV1(
		basis.PublisherKey.Canonical,
	)
	if err != nil || publisherID != basis.PublisherKey.PublisherKeyID ||
		!bytes.Equal(ownedPublisher, basis.PublisherKey.Canonical) {
		return errors.Join(err, errors.New("publisher key differs from exact Store basis"))
	}
	if err := moduleapi.VerifyModuleSignatureV1(signature, publisher); err != nil {
		return errors.New("detached signature verification failed")
	}
	return nil
}

func assessModuleUpgradeHandlerV1(
	ctx context.Context,
	store *currentstore.Store,
	basis currentstore.ModuleUpgradeReviewBasis,
	targetManifest moduleapi.ModuleManifestV1,
	port moduleapi.PortRef,
) (
	moduleupgrade.HandlerAssessmentV1,
	[]moduleupgrade.RequiredGrantV1,
	moduleupgrade.ConclusionV1,
	[]moduleupgrade.ReasonCodeV1,
	error,
) {
	targetModule := &moduleApplyModuleV1{
		ID:                targetManifest.ID,
		ExactVersion:      targetManifest.Version,
		ArtifactDigest:    basis.TargetEntry.ArtifactDigest,
		ArtifactSizeBytes: basis.TargetEntry.ArtifactSizeBytes,
		ExpectedRuntimeRequest: moduleApplyExpectedRuntimeRequestV1{
			Mode:     targetManifest.Runtime.Mode,
			Protocol: targetManifest.Runtime.Protocol,
		},
	}
	var (
		handler                  moduleupgrade.HandlerAssessmentV1
		grants                   []moduleupgrade.RequiredGrantV1
		selectedSeen             bool
		unsupported              bool
		publishedBindingConflict bool
	)
	for _, impact := range basis.BindingImpacts {
		selected := impact.BindingTarget == basis.Selection.BindingTarget &&
			impact.Port == port && impact.PortBindingIndex == basis.Selection.PortBindingIndex
		if selected {
			selectedSeen = true
		}
		config, err := store.GetContent(ctx, impact.ConfigRef)
		if err != nil {
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil, err
		}
		if config.Kind != currentstore.ContentConfig {
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil,
				currentstore.ErrModuleUpgradeIntegrity
		}
		authority, err := store.GetContent(ctx, impact.AuthorityCeilingRef)
		if err != nil {
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil, err
		}
		if authority.Kind != currentstore.ContentAuthorityCeiling {
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil,
				currentstore.ErrModuleUpgradeIntegrity
		}
		binding := moduleApplyBindingV1{
			PortBindingIndex: impact.PortBindingIndex,
			Config:           config.CanonicalBytes,
			AuthorityCeiling: authority.CanonicalBytes,
			FailurePolicy:    impact.FailurePolicy,
		}
		sharedInput := modulehandler.BindingV1{
			TenantID: basis.Selection.TenantID, Port: impact.Port,
			ConfigCanonical: binding.Config, AuthorityCanonical: binding.AuthorityCeiling,
			FailurePolicy: binding.FailurePolicy,
			RuntimeRequest: modulehandler.RuntimeRequestV1{
				Mode: targetModule.ExpectedRuntimeRequest.Mode, Protocol: targetModule.ExpectedRuntimeRequest.Protocol,
			},
			Module: sharedModuleApplyIdentityV1(targetModule),
		}
		assessment, assessErr := modulehandler.AssessBindingV1(sharedInput)
		if assessErr != nil {
			if !selected {
				publishedBindingConflict = true
				continue
			}
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil, assessErr
		}
		policy := moduleApplyLocalPolicyV1(assessment.Policy)
		switch assessment.Status {
		case modulehandler.AssessmentUnsupportedV1:
			unsupported = true
			continue
		case modulehandler.AssessmentConflictV1:
			publishedBindingConflict = true
			if selected {
				handler = moduleUpgradeHandlerAssessmentV1(policy, moduleupgrade.HandlerConflictV1)
			}
		case modulehandler.AssessmentSupportedV1:
			if selected {
				handler = moduleUpgradeHandlerAssessmentV1(policy, moduleupgrade.HandlerSupportedV1)
			}
		default:
			return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil,
				errors.New("shared handler assessment returned an invalid status")
		}
		grants = append(grants, moduleUpgradeGrantsFromSharedV1(
			modulehandler.RequiredGrantsV1(assessment.Policy, sharedInput, basis.TargetEntry.ArtifactDigest),
		)...)
	}
	if !selectedSeen {
		return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil,
			errors.New("selected Binding impact is absent")
	}
	grants = normalizeModuleUpgradeGrantsV1(grants)
	reasons := make([]moduleupgrade.ReasonCodeV1, 0, 8)
	conclusion := moduleupgrade.ConclusionWouldApplyV1
	if unsupported {
		handler = moduleupgrade.HandlerAssessmentV1{Status: moduleupgrade.HandlerUnsupportedV1}
		grants = []moduleupgrade.RequiredGrantV1{}
		conclusion = moduleupgrade.ConclusionUnsupportedV1
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonHandlerUnsupportedV1)
	} else if handler.Status == moduleupgrade.HandlerConflictV1 {
		conclusion = moduleupgrade.ConclusionConflictV1
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonHandlerConflictV1)
	}
	if publishedBindingConflict {
		if conclusion != moduleupgrade.ConclusionUnsupportedV1 {
			conclusion = moduleupgrade.ConclusionConflictV1
			reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonPublishedBindingConflictV1)
		}
	}
	if len(grants) != 0 {
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonOperatorGrantRequiredV1)
	}
	if _, occupied := basis.Catalog.FindInstance(basis.Selection.TargetInstanceID); occupied {
		if conclusion != moduleupgrade.ConclusionUnsupportedV1 {
			conclusion = moduleupgrade.ConclusionConflictV1
		}
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetInstanceConflictV1)
	}
	portRemoved := false
	for _, impact := range basis.BindingImpacts {
		if !moduleUpgradeManifestHasPortV1(targetManifest, impact.Port) {
			portRemoved = true
			break
		}
	}
	if portRemoved {
		if conclusion != moduleupgrade.ConclusionUnsupportedV1 {
			conclusion = moduleupgrade.ConclusionConflictV1
		}
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetPortRemovedV1)
	}
	currentManifest, _, parseErr := moduleapi.ParseModuleManifestV1(basis.CurrentInstallation.ManifestBytes)
	if parseErr != nil {
		return moduleupgrade.HandlerAssessmentV1{}, nil, "", nil, parseErr
	}
	if !reflect.DeepEqual(currentManifest.Requires, targetManifest.Requires) {
		if conclusion != moduleupgrade.ConclusionUnsupportedV1 {
			conclusion = moduleupgrade.ConclusionConflictV1
		}
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetRequirementsChangedV1)
	}
	if moduleUpgradePermissionsExpandedV1(currentManifest.RequestedPermissions, targetManifest.RequestedPermissions) {
		if conclusion != moduleupgrade.ConclusionUnsupportedV1 {
			conclusion = moduleupgrade.ConclusionConflictV1
		}
		reasons = appendUniqueModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonRequestedPermissionsExpandedV1)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	return handler, grants, conclusion, reasons, nil
}

func moduleUpgradeHandlerAssessmentV1(
	policy moduleApplyLocalPolicyV1,
	status moduleupgrade.HandlerStatusV1,
) moduleupgrade.HandlerAssessmentV1 {
	return moduleupgrade.HandlerAssessmentV1{
		Status: status, Kind: string(policy.HandlerKind),
		ExecutionClass: policy.ExecutionClass, AdapterIdentity: policy.AdapterIdentity,
	}
}

func appendUniqueModuleUpgradeReasonV1(
	values []moduleupgrade.ReasonCodeV1,
	value moduleupgrade.ReasonCodeV1,
) []moduleupgrade.ReasonCodeV1 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalizeModuleUpgradeGrantsV1(
	values []moduleupgrade.RequiredGrantV1,
) []moduleupgrade.RequiredGrantV1 {
	seen := make(map[string]struct{}, len(values))
	result := make([]moduleupgrade.RequiredGrantV1, 0, len(values))
	for _, value := range values {
		key := string(value.Kind) + "\x00" + value.ReferenceDigest
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].ReferenceDigest < result[j].ReferenceDigest
	})
	if result == nil {
		return []moduleupgrade.RequiredGrantV1{}
	}
	return result
}

func moduleUpgradeGrantsFromSharedV1(
	values []modulehandler.GrantRequirementV1,
) []moduleupgrade.RequiredGrantV1 {
	result := make([]moduleupgrade.RequiredGrantV1, 0, len(values))
	for _, value := range values {
		result = append(result, moduleupgrade.RequiredGrantV1{
			Kind:            moduleupgrade.RequiredGrantKindV1(value.Kind),
			ReferenceDigest: value.ReferenceDigest,
		})
	}
	return result
}

func requiredModuleUpgradeGrantsV1(
	policy moduleApplyLocalPolicyV1,
	binding moduleApplyBindingV1,
	tenantID string,
	targetDigest string,
) []moduleupgrade.RequiredGrantV1 {
	input := modulehandler.BindingV1{
		TenantID: tenantID, Port: policy.Port,
		ConfigCanonical: binding.Config, AuthorityCanonical: binding.AuthorityCeiling,
		FailurePolicy:  binding.FailurePolicy,
		RuntimeRequest: modulehandler.RuntimeRequestV1{Mode: policy.RuntimeMode, Protocol: policy.RuntimeProtocol},
	}
	return moduleUpgradeGrantsFromSharedV1(
		modulehandler.RequiredGrantsV1(modulehandler.PolicyV1(policy), input, targetDigest),
	)
}

func moduleUpgradeManifestHasPortV1(manifest moduleapi.ModuleManifestV1, wanted moduleapi.PortRef) bool {
	for _, port := range manifest.Provides {
		if port == wanted {
			return true
		}
	}
	return false
}

func moduleUpgradePermissionsExpandedV1(current, target []moduleapi.Permission) bool {
	known := make(map[moduleapi.Permission]struct{}, len(current))
	for _, permission := range current {
		known[permission] = struct{}{}
	}
	for _, permission := range target {
		if _, found := known[permission]; !found {
			return true
		}
	}
	return false
}

type moduleUpgradeReviewCommandResultV1 struct {
	SchemaVersion string                 `json:"schema_version"`
	Status        string                 `json:"status"`
	CandidateID   string                 `json:"candidate_id"`
	ReviewID      string                 `json:"review_id"`
	Review        moduleupgrade.ReviewV1 `json:"review"`
}

func safeModuleUpgradeCommandResultV1(schemaVersion, status, tenantID, candidateID, reviewID, decisionID string) any {
	return struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		TenantID      string `json:"tenant_id"`
		CandidateID   string `json:"candidate_id"`
		ReviewID      string `json:"review_id"`
		DecisionID    string `json:"decision_id,omitempty"`
	}{
		SchemaVersion: schemaVersion,
		Status:        strings.TrimSpace(status),
		TenantID:      tenantID,
		CandidateID:   candidateID,
		ReviewID:      reviewID,
		DecisionID:    decisionID,
	}
}
