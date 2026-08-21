package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleUpgradeApplyResultSchemaV1 = "freeagent.module-upgrade-apply-command-result/v1"

type moduleUpgradeApplyFailureCodeV1 string

const (
	moduleUpgradeApplyFailureDisabledV1       moduleUpgradeApplyFailureCodeV1 = "UPGRADE_APPLY_DISABLED"
	moduleUpgradeApplyFailureInvalidFlagsV1   moduleUpgradeApplyFailureCodeV1 = "INVALID_FLAGS"
	moduleUpgradeApplyFailureApprovalV1       moduleUpgradeApplyFailureCodeV1 = "APPROVAL_INVALID"
	moduleUpgradeApplyFailureUnsupportedV1    moduleUpgradeApplyFailureCodeV1 = "APPLY_UNSUPPORTED"
	moduleUpgradeApplyFailureStoreBusyV1      moduleUpgradeApplyFailureCodeV1 = "STORE_BUSY"
	moduleUpgradeApplyFailurePointerV1        moduleUpgradeApplyFailureCodeV1 = "POINTER_CONFLICT"
	moduleUpgradeApplyFailureOutcomeUnknownV1 moduleUpgradeApplyFailureCodeV1 = "APPLY_OUTCOME_UNKNOWN"
	moduleUpgradeApplyFailureCancelledV1      moduleUpgradeApplyFailureCodeV1 = "CANCELLED"
	moduleUpgradeApplyFailureInternalV1       moduleUpgradeApplyFailureCodeV1 = "INTERNAL_ERROR"
)

var (
	errModuleUpgradeApplyApprovalV1    = errors.New("module upgrade apply approval is invalid")
	errModuleUpgradeApplyUnsupportedV1 = errors.New("module upgrade apply shape is unsupported")
)

// moduleUpgradeApplyApprovalV1 is the detached Store projection consumed by
// the pure Review-to-Apply mapper. It contains no Source PackagePath, URL,
// downloaded body, authority grant or mutable Store handle.
type moduleUpgradeApplyApprovalV1 struct {
	TenantID           string
	ReviewID           string
	DecisionID         string
	Review             moduleupgrade.ReviewV1
	Decision           moduleapi.ModuleCandidateDecisionV1
	ConfigCanonical    []byte
	AuthorityCanonical []byte
}

// buildApprovedModuleApplyPlanV1 deliberately supports only the first safe
// U4 publication shape: one approved, effect-free DECLARATIVE Context Binding
// at one exact PROFILE/Port/ordinal coordinate. The returned plan is not by
// itself permission to use ordinary additive Apply. Approved Apply must also
// pass an internal replacement constraint that atomically replaces the
// Review's current Instance at that exact coordinate with the target Instance.
// Model, Channel, Action, fan-out and unconstrained replacement are rejected.
func buildApprovedModuleApplyPlanV1(
	approval moduleUpgradeApplyApprovalV1,
) (moduleApplyPlanV1, []byte, string, error) {
	review := approval.Review
	if validateModuleApplyOpaqueIDV1("tenant_id", approval.TenantID) != nil ||
		!moduleapi.ValidSHA256(approval.ReviewID) ||
		!moduleapi.ValidSHA256(approval.DecisionID) ||
		review.TenantID != approval.TenantID ||
		review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 ||
		approval.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 ||
		approval.Decision.CandidateID != review.CandidateID {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyApprovalV1
	}
	if review.BindingTarget.Kind != moduleupgrade.BindingTargetProfileV1 ||
		review.Port != productionContextPort ||
		len(review.BindingImpacts) != 1 {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyUnsupportedV1
	}
	current := review.Current.Activation
	if current.InstanceID == review.TargetInstanceID ||
		current.ModuleID != review.Target.Module.ID ||
		current.Version == review.Target.Module.Version ||
		current.ArtifactDigest == review.Target.ArtifactDigest {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyApprovalV1
	}
	impact := review.BindingImpacts[0]
	if impact.BindingTarget != review.BindingTarget ||
		impact.Port != review.Port ||
		impact.PortBindingIndex != review.PortBindingIndex ||
		impact.CurrentInstanceID != current.InstanceID ||
		impact.TargetInstanceID != review.TargetInstanceID {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyApprovalV1
	}
	if review.Handler.Status != moduleupgrade.HandlerSupportedV1 ||
		review.Handler.Kind != string(modulehandler.HandlerDeclarativeContextV1) ||
		review.Handler.ExecutionClass != moduleapi.ExecutionDeclarative ||
		review.Handler.AdapterIdentity != modulehandler.DeclarativeAdapterIdentityV1 ||
		len(review.RequiredGrants) != 0 {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyUnsupportedV1
	}
	manifest := review.Target.Manifest
	if manifest.Module != review.Target.Module ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestDeclarative ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != productionContextPort ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return moduleApplyPlanV1{}, nil, "", errModuleUpgradeApplyUnsupportedV1
	}
	config := bytes.Clone(approval.ConfigCanonical)
	authority := bytes.Clone(approval.AuthorityCanonical)
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		moduleApplyJSONMediaType,
		config,
	)
	if err != nil || configRef != impact.ConfigRef {
		return moduleApplyPlanV1{}, nil, "", errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	authorityRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentAuthorityCeiling,
		moduleApplyJSONMediaType,
		authority,
	)
	if err != nil || authorityRef != impact.AuthorityCeilingRef {
		return moduleApplyPlanV1{}, nil, "", errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(config)
	if err != nil || contextConfig.Placement != moduleapi.ContextPlacementTrustedInstruction ||
		!bytes.Equal(authority, modulehandler.DenyAllAuthorityCanonicalV1()) {
		return moduleApplyPlanV1{}, nil, "", errors.Join(
			errModuleUpgradeApplyUnsupportedV1,
			err,
		)
	}

	plan := moduleApplyPlanV1{
		SchemaVersion:           moduleApplyPlanSchemaV1,
		DesiredState:            moduleApplyEnabledV1,
		TenantID:                review.TenantID,
		ExpectedPointerRevision: review.PublishedBasis.PointerRevision,
		BindingTarget: moduleApplyBindingTargetV1{
			Kind:      moduleApplyBindingTargetProfileV1,
			ProfileID: review.BindingTarget.ProfileID,
		},
		InstanceID:               review.TargetInstanceID,
		ReplaceCurrentInstanceID: current.InstanceID,
		Port:                     review.Port,
		Module: &moduleApplyModuleV1{
			ID:                review.Target.Module.ID,
			ExactVersion:      review.Target.Module.Version,
			ArtifactDigest:    review.Target.ArtifactDigest,
			ArtifactSizeBytes: review.Target.ArtifactSizeBytes,
			ExpectedRuntimeRequest: moduleApplyExpectedRuntimeRequestV1{
				Mode:     manifest.Runtime.Mode,
				Protocol: manifest.Runtime.Protocol,
			},
		},
		Binding: &moduleApplyBindingV1{
			PortBindingIndex: impact.PortBindingIndex,
			Config:           config,
			AuthorityCeiling: authority,
			FailurePolicy:    impact.FailurePolicy,
		},
	}
	frozen, exact, digest, err := freezeModuleApplyPlanValueV1(plan)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	return frozen, exact, digest, nil
}

func moduleUpgradeApplyApprovalFromStoreV1(
	approval currentstore.ModuleUpgradeApplyApproval,
) moduleUpgradeApplyApprovalV1 {
	return moduleUpgradeApplyApprovalV1{
		TenantID:           approval.Input.TenantID,
		ReviewID:           approval.Input.ReviewID,
		DecisionID:         approval.Input.DecisionID,
		Review:             approval.Review.Review,
		Decision:           approval.Decision.Decision,
		ConfigCanonical:    bytes.Clone(approval.BindingConfig.CanonicalBytes),
		AuthorityCanonical: bytes.Clone(approval.AuthorityCeiling.CanonicalBytes),
	}
}

type moduleUpgradeApplySourcePolicyV1 struct {
	PolicyID        string
	PolicyCanonical []byte
}

func historicalModuleUpgradeApplySourcePolicyV1(
	approval currentstore.ModuleUpgradeApplyApproval,
) moduleUpgradeApplySourcePolicyV1 {
	return moduleUpgradeApplySourcePolicyV1{
		PolicyID:        approval.Snapshot.SourcePolicyID,
		PolicyCanonical: bytes.Clone(approval.Snapshot.SourcePolicyCanonical),
	}
}

func currentModuleUpgradeApplySourcePolicyV1(
	approval currentstore.ModuleUpgradeApplyApproval,
) moduleUpgradeApplySourcePolicyV1 {
	return moduleUpgradeApplySourcePolicyV1{
		PolicyID:        approval.Source.PolicyID,
		PolicyCanonical: bytes.Clone(approval.Source.PolicyCanonical),
	}
}

// verifyApprovedModuleUpgradeArtifactV1 applies the complete U1
// LOCAL_DIRECTORY verifier to one explicit source root and artifact path. It
// never consults Discovery PackagePath, downloads a package, or turns a
// successful report into a reusable grant. The caller invokes it once over
// historical Review policy and again after current Store revalidation.
func verifyApprovedModuleUpgradeArtifactV1(
	ctx context.Context,
	request moduleUpgradeApplyCommandRequestV1,
	approval currentstore.ModuleUpgradeApplyApproval,
	policyEvidence moduleUpgradeApplySourcePolicyV1,
) (moduleconformance.Report, error) {
	policy, err := moduleapi.RestoreModuleSourcePolicyV1(
		policyEvidence.PolicyCanonical,
		policyEvidence.PolicyID,
	)
	reviewTarget := approval.Review.Review.Target
	if err != nil || policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
		policy.Network != moduleapi.ModuleSourceNetworkDenyV1 ||
		approval.TargetEntry.Module != reviewTarget.Module ||
		approval.TargetEntry.ArtifactDigest != reviewTarget.ArtifactDigest ||
		approval.TargetEntry.ArtifactSizeBytes != reviewTarget.ArtifactSizeBytes ||
		approval.TargetEntry.SignatureID != reviewTarget.SignatureID {
		return moduleconformance.Report{}, errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(
		policy,
		approval.TargetEntry.Module,
		approval.TargetEntry.ArtifactSizeBytes,
		approval.TargetEntry.SignatureID != "",
	); err != nil {
		return moduleconformance.Report{}, errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}

	input := moduleconformance.SourceCandidateInput{
		ArtifactRoot:          request.ArtifactDirectory,
		SourceRoot:            request.SourceRoot,
		SourcePolicyID:        policyEvidence.PolicyID,
		SourcePolicyCanonical: bytes.Clone(policyEvidence.PolicyCanonical),
	}
	if policy.SignatureRequired {
		if approval.PublisherKey == nil || approval.TargetEntry.SignatureID == "" ||
			!exactNonEmptyFlag(request.SignaturePath) {
			return moduleconformance.Report{}, errModuleUpgradeApplyApprovalV1
		}
		signatureCanonical, readErr := readModuleVerifyCanonicalFile(
			ctx,
			request.SignaturePath,
		)
		if readErr != nil {
			return moduleconformance.Report{}, errors.Join(
				errModuleUpgradeApplyApprovalV1,
				readErr,
			)
		}
		input.PublisherKeyID = approval.PublisherKey.PublisherKeyID
		input.PublisherKeyCanonical = bytes.Clone(approval.PublisherKey.Canonical)
		input.SignatureID = approval.TargetEntry.SignatureID
		input.SignatureCanonical = signatureCanonical
		if approval.PublisherKey.RevokedAt != nil {
			input.RevokedPublisherKeyIDs = []string{
				approval.PublisherKey.PublisherKeyID,
			}
		}
	} else if request.SignaturePath != "" || approval.PublisherKey != nil ||
		approval.TargetEntry.SignatureID != "" {
		return moduleconformance.Report{}, errModuleUpgradeApplyApprovalV1
	}
	report, err := moduleconformance.VerifySourceCandidateDirectory(ctx, input)
	if err != nil {
		return moduleconformance.Report{}, errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	if report.Module.ID != approval.TargetEntry.Module.ID ||
		report.Module.ExactVersion != approval.TargetEntry.Module.Version ||
		report.ArtifactDigest != approval.TargetEntry.ArtifactDigest ||
		report.ArtifactSizeBytes != approval.TargetEntry.ArtifactSizeBytes {
		return moduleconformance.Report{}, errModuleUpgradeApplyApprovalV1
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		request.ArtifactDirectory,
	)
	if err != nil || approval.TargetManifest.Kind != currentstore.ContentModuleManifest ||
		approval.TargetManifest.MediaType != moduleApplyJSONMediaType ||
		!bytes.Equal(manifestCanonical, approval.TargetManifest.CanonicalBytes) {
		return moduleconformance.Report{}, errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
		)
	}
	return report, nil
}

type moduleUpgradeApplyCommandRequestV1 struct {
	DatabasePath                  string
	ArtifactRoot                  string
	SourceRoot                    string
	TenantID                      string
	ReviewID                      string
	DecisionID                    string
	ArtifactDirectory             string
	SignaturePath                 string
	LocalMCPArtifactGrant         string
	TrustedInProcessArtifactGrant string
	RemoteActionArtifactGrant     string
	WASMActionArtifactGrant       string
	RemoteActionEndpointGrant     string
	RemoteActionSecretRefGrant    string
	ModelSecretRefGrant           string
}

type moduleUpgradeApplyDependenciesV1 struct {
	apply func(
		context.Context,
		moduleUpgradeApplyCommandRequestV1,
	) (moduleUpgradeApplyCommandResultV1, error)
}

type moduleUpgradeApplyCommandResultV1 struct {
	SchemaVersion       string              `json:"schema_version"`
	Status              moduleApplyStatusV1 `json:"status"`
	TenantID            string              `json:"tenant_id"`
	ReviewID            string              `json:"review_id"`
	DecisionID          string              `json:"decision_id"`
	PlanDigest          string              `json:"plan_digest"`
	PointerRevision     uint64              `json:"pointer_revision"`
	ControlSnapshotID   string              `json:"control_snapshot_id"`
	CatalogGenerationID string              `json:"catalog_generation_id"`
}

func productionModuleUpgradeApplyDependenciesV1() moduleUpgradeApplyDependenciesV1 {
	return moduleUpgradeApplyDependenciesV1{apply: executeModuleUpgradeApplyV1}
}

func runModuleUpgradeApply(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runModuleUpgradeApplyWithDependenciesV1(
		ctx,
		args,
		stdout,
		stderr,
		productionModuleUpgradeApplyDependenciesV1(),
	)
}

func runModuleUpgradeApplyWithDependenciesV1(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	_ io.Writer,
	dependencies moduleUpgradeApplyDependenciesV1,
) error {
	flags := newFlagSet("module-upgrade-apply", io.Discard)
	enabled := flags.Bool("enable-module-upgrade-apply", false, "enable this explicit approved upgrade publication")
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "existing content-addressed artifact root")
	sourceRoot := flags.String("source-root", "", "absolute LOCAL_DIRECTORY root governed by the approved Source Policy")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	reviewID := flags.String("review-id", "", "exact approved Review identity")
	decisionID := flags.String("decision-id", "", "exact APPROVE Decision identity")
	artifactDirectory := flags.String("artifact", "", "explicit unpacked local artifact; never a discovered PackagePath")
	signaturePath := flags.String("signature", "", "explicit detached signature path, or explicit empty value for an unsigned Source")
	localGrant := flags.String("allow-local-mcp-artifact", "", "exact LOCAL_PROCESS artifact grant")
	trustedGrant := flags.String("allow-trusted-in-process-artifact", "", "exact TRUSTED_IN_PROCESS artifact grant")
	remoteArtifactGrant := flags.String("allow-remote-action-artifact", "", "exact REMOTE artifact grant")
	wasmGrant := flags.String("allow-wasm-action-artifact", "", "exact WASM artifact grant")
	remoteEndpointGrant := flags.String("allow-remote-action-endpoint", "", "exact REMOTE endpoint grant")
	remoteAuthorityReferenceGrantFlag := flags.String("allow-remote-action-secret-ref", "", "exact REMOTE SecretRef grant")
	modelAuthorityReferenceGrantFlag := flags.String("allow-model-secret-ref", "", "exact Model SecretRef grant")
	if err := flags.Parse(args); err != nil {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureInvalidFlagsV1)
	}
	if !*enabled {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureDisabledV1)
	}
	remoteAuthorityReferenceGrant := *remoteAuthorityReferenceGrantFlag
	modelAuthorityReferenceGrant := *modelAuthorityReferenceGrantFlag
	request := moduleUpgradeApplyCommandRequestV1{
		DatabasePath:                  *databasePath,
		ArtifactRoot:                  *artifactRoot,
		SourceRoot:                    *sourceRoot,
		TenantID:                      *tenantID,
		ReviewID:                      *reviewID,
		DecisionID:                    *decisionID,
		ArtifactDirectory:             *artifactDirectory,
		SignaturePath:                 *signaturePath,
		LocalMCPArtifactGrant:         *localGrant,
		TrustedInProcessArtifactGrant: *trustedGrant,
		RemoteActionArtifactGrant:     *remoteArtifactGrant,
		WASMActionArtifactGrant:       *wasmGrant,
		RemoteActionEndpointGrant:     *remoteEndpointGrant,
		RemoteActionSecretRefGrant:    remoteAuthorityReferenceGrant,
		ModelSecretRefGrant:           modelAuthorityReferenceGrant,
	}
	for _, name := range [...]string{
		"db", "artifact-root", "source-root", "tenant", "review-id", "decision-id",
		"artifact", "signature", "allow-local-mcp-artifact",
		"allow-trusted-in-process-artifact", "allow-remote-action-artifact",
		"allow-wasm-action-artifact", "allow-remote-action-endpoint",
		"allow-remote-action-secret-ref", "allow-model-secret-ref",
	} {
		if !flagWasExplicitlySetV1(flags, name) {
			return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureInvalidFlagsV1)
		}
	}
	if ctx == nil || flags.NArg() != 0 || dependencies.apply == nil ||
		!exactNonEmptyFlag(request.DatabasePath) ||
		!exactNonEmptyFlag(request.ArtifactRoot) ||
		!exactNonEmptyFlag(request.SourceRoot) ||
		validateModuleApplyOpaqueIDV1("tenant_id", request.TenantID) != nil ||
		!moduleapi.ValidSHA256(request.ReviewID) ||
		!moduleapi.ValidSHA256(request.DecisionID) ||
		!exactNonEmptyFlag(request.ArtifactDirectory) ||
		(request.SignaturePath != "" && !exactNonEmptyFlag(request.SignaturePath)) {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureInvalidFlagsV1)
	}
	// This first slice has no grant-bearing handler. Requiring every grant flag
	// to be explicit while accepting only empty values prevents an implicit or
	// accidentally inherited grant from widening the approved Review.
	if request.LocalMCPArtifactGrant != "" ||
		request.TrustedInProcessArtifactGrant != "" ||
		request.RemoteActionArtifactGrant != "" ||
		request.WASMActionArtifactGrant != "" ||
		request.RemoteActionEndpointGrant != "" ||
		request.RemoteActionSecretRefGrant != "" ||
		request.ModelSecretRefGrant != "" {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureUnsupportedV1)
	}
	result, err := dependencies.apply(ctx, request)
	if err != nil {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureCodeOfV1(err))
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return moduleUpgradeApplyCommandFailureV1(moduleUpgradeApplyFailureInternalV1)
	}
	return nil
}

func moduleUpgradeApplyFailureCodeOfV1(err error) moduleUpgradeApplyFailureCodeV1 {
	// Preserve the typed Apply conclusion before inspecting any wrapped cause.
	// UNKNOWN, POINTER, CANCELLED, and STORE_BUSY carry stronger commit/admission
	// meaning than approval or cancellation sentinels joined beneath them.
	var failure *moduleApplyFailureV1
	if errors.As(err, &failure) && failure != nil {
		switch failure.code {
		case moduleApplyFailureOutcomeUnknown:
			return moduleUpgradeApplyFailureOutcomeUnknownV1
		case moduleApplyFailurePointer:
			return moduleUpgradeApplyFailurePointerV1
		case moduleApplyFailureCancelled:
			return moduleUpgradeApplyFailureCancelledV1
		case moduleApplyFailureStoreBusy:
			return moduleUpgradeApplyFailureStoreBusyV1
		case moduleApplyFailureArtifact,
			moduleApplyFailureTarget,
			moduleApplyFailurePlanInvalid:
			return moduleUpgradeApplyFailureApprovalV1
		}
	}
	switch {
	case errors.Is(err, errModuleUpgradeApplyApprovalV1):
		return moduleUpgradeApplyFailureApprovalV1
	case errors.Is(err, errModuleUpgradeApplyUnsupportedV1):
		return moduleUpgradeApplyFailureUnsupportedV1
	case errors.Is(err, currentstore.ErrOwnerActive):
		return moduleUpgradeApplyFailureStoreBusyV1
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return moduleUpgradeApplyFailureCancelledV1
	default:
		return moduleUpgradeApplyFailureInternalV1
	}
}

func moduleUpgradeApplyCommandFailureV1(code moduleUpgradeApplyFailureCodeV1) error {
	return fmt.Errorf("freeagent module-upgrade-apply: failed (%s)", code)
}

func executeModuleUpgradeApplyV1(
	ctx context.Context,
	request moduleUpgradeApplyCommandRequestV1,
) (moduleUpgradeApplyCommandResultV1, error) {
	input := currentstore.ModuleUpgradeApplyApprovalInput{
		TenantID: request.TenantID, ReviewID: request.ReviewID,
		DecisionID: request.DecisionID,
	}
	approval, err := loadModuleUpgradeApplyApprovalV1(ctx, request.DatabasePath, input)
	if err != nil {
		return moduleUpgradeApplyCommandResultV1{}, err
	}
	plan, canonical, planDigest, err := buildApprovedModuleApplyPlanV1(
		moduleUpgradeApplyApprovalFromStoreV1(approval),
	)
	if err != nil {
		return moduleUpgradeApplyCommandResultV1{}, err
	}

	applyInput := moduleApplyCommandInputV1{
		DatabasePath:      request.DatabasePath,
		ArtifactRoot:      request.ArtifactRoot,
		ArtifactDirectory: request.ArtifactDirectory,
		Plan:              plan,
		PlanCanonical:     bytes.Clone(canonical),
		PlanDigest:        planDigest,
	}
	applyInput.PreStageCheck = func(
		hookCtx context.Context,
		store *currentstore.Store,
		basis controlcontract.PublishedBasis,
	) error {
		// Historical Source evidence is immutable and belongs to the exact
		// Review. Revalidate mutable Store state only after that artifact has
		// passed the same governed U1 verifier used when it was reviewed.
		if _, verifyErr := verifyApprovedModuleUpgradeArtifactV1(
			hookCtx,
			request,
			approval,
			historicalModuleUpgradeApplySourcePolicyV1(approval),
		); verifyErr != nil {
			return verifyErr
		}
		latest, revalidateErr := store.RevalidateCurrentModuleUpgradeApplyApproval(
			hookCtx,
			input,
		)
		if revalidateErr != nil {
			return errors.Join(errModuleUpgradeApplyApprovalV1, revalidateErr)
		}
		if latest.Review.Review.PublishedBasis != basis {
			return errors.Join(
				errModuleUpgradeApplyApprovalV1,
				errors.New("revalidated approval does not match the Apply admission basis"),
			)
		}
		if compareErr := requireSameApprovedModuleApplyPlanV1(
			latest,
			canonical,
			planDigest,
		); compareErr != nil {
			return compareErr
		}
		_, verifyErr := verifyApprovedModuleUpgradeArtifactV1(
			hookCtx,
			request,
			latest,
			currentModuleUpgradeApplySourcePolicyV1(latest),
		)
		return verifyErr
	}
	applied, err := applyModulePlanV1(ctx, applyInput)
	if err != nil {
		return moduleUpgradeApplyCommandResultV1{}, err
	}
	return newModuleUpgradeApplyCommandResultV1(
		request,
		planDigest,
		applied.Status,
		controlcontract.PublishedBasis{
			TenantID:        request.TenantID,
			PointerRevision: applied.PointerRevision,
			Control: controlcontract.ControlSnapshotRef{
				SnapshotID: applied.ControlSnapshotID,
			},
			Catalog: controlcontract.CatalogGenerationRef{
				GenerationID: applied.CatalogGenerationID,
			},
		},
	), nil
}

func loadModuleUpgradeApplyApprovalV1(
	ctx context.Context,
	databasePath string,
	input currentstore.ModuleUpgradeApplyApprovalInput,
) (result currentstore.ModuleUpgradeApplyApproval, returnErr error) {
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	approval, loadErr := store.LoadModuleUpgradeApplyApproval(ctx, input)
	if loadErr != nil && isModuleUpgradeApplyApprovalSemanticErrorV1(loadErr) {
		return result, errors.Join(errModuleUpgradeApplyApprovalV1, loadErr)
	}
	return approval, loadErr
}

func isModuleUpgradeApplyApprovalSemanticErrorV1(err error) bool {
	return errors.Is(err, currentstore.ErrInvalidModuleUpgradeReview) ||
		errors.Is(err, currentstore.ErrModuleUpgradeReviewNotFound) ||
		errors.Is(err, currentstore.ErrModuleUpgradeReviewConflict) ||
		errors.Is(err, currentstore.ErrModuleUpgradeReviewStale) ||
		errors.Is(err, currentstore.ErrModuleUpgradeSuppressed) ||
		errors.Is(err, currentstore.ErrModuleUpgradeIntegrity)
}

func requireSameApprovedModuleApplyPlanV1(
	approval currentstore.ModuleUpgradeApplyApproval,
	expectedCanonical []byte,
	expectedDigest string,
) error {
	_, canonical, digest, err := buildApprovedModuleApplyPlanV1(
		moduleUpgradeApplyApprovalFromStoreV1(approval),
	)
	if err != nil || digest != expectedDigest ||
		!bytes.Equal(canonical, expectedCanonical) {
		return errors.Join(
			errModuleUpgradeApplyApprovalV1,
			err,
			errors.New("revalidated approval maps to a different exact plan"),
		)
	}
	return nil
}

func newModuleUpgradeApplyCommandResultV1(
	request moduleUpgradeApplyCommandRequestV1,
	planDigest string,
	status moduleApplyStatusV1,
	basis controlcontract.PublishedBasis,
) moduleUpgradeApplyCommandResultV1 {
	return moduleUpgradeApplyCommandResultV1{
		SchemaVersion: moduleUpgradeApplyResultSchemaV1,
		Status:        status, TenantID: request.TenantID,
		ReviewID: request.ReviewID, DecisionID: request.DecisionID,
		PlanDigest: planDigest, PointerRevision: basis.PointerRevision,
		ControlSnapshotID:   basis.Control.SnapshotID,
		CatalogGenerationID: basis.Catalog.GenerationID,
	}
}
