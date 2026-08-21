package main

import (
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
)

// evaluateDisabledModulePlanOnViewV1 is the sole cmd composition adapter for
// the neutral MODULE_DISABLE evaluator. It projects an already-restored plan;
// it does not parse or recalculate module-apply-plan/v1 canonical bytes or its
// digest.
func evaluateDisabledModulePlanOnViewV1(
	ctx context.Context,
	view moduledisabledryrun.ReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	planDigest string,
) (moduledisabledryrun.ResultV1, error) {
	input, err := moduleDisableDryRunInputV1(plan, planDigest)
	if err != nil {
		return moduledisabledryrun.ResultV1{}, err
	}
	result, err := moduledisabledryrun.EvaluateV1(
		ctx,
		view,
		func(
			ctx context.Context,
			catalog controlCatalogGenerationAliasV1,
			excludedInstanceID string,
		) error {
			return verifyCurrentCatalogArtifactRootV1(
				ctx,
				artifactRoot,
				catalog,
				excludedInstanceID,
			)
		},
		input,
	)
	if err != nil {
		return moduledisabledryrun.ResultV1{}, mapModuleDisableDryRunFailureV1(err)
	}
	return result, nil
}

// Alias keeps the closure signature readable without weakening its exact
// compile-time match to the shared contract.
type controlCatalogGenerationAliasV1 = controlcontract.CatalogGeneration

func moduleDisableDryRunInputV1(
	plan moduleApplyPlanV1,
	planDigest string,
) (moduledisabledryrun.InputV1, error) {
	if plan.DesiredState != moduleApplyDisabledV1 {
		return moduledisabledryrun.InputV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("shared MODULE_DISABLE evaluator requires a DISABLED plan"),
		)
	}
	candidateIDs, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		return moduledisabledryrun.InputV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.Join(
				err,
				errors.New("MODULE_DISABLE candidate identity is invalid"),
			),
		)
	}
	targetKind := moduledisabledryrun.TargetKindV1(plan.BindingTarget.Kind)
	return moduledisabledryrun.InputV1{
		TenantID:                     plan.TenantID,
		ExpectedPointerRevision:      plan.ExpectedPointerRevision,
		TargetKind:                   targetKind,
		ProfileID:                    plan.BindingTarget.ProfileID,
		WorkspaceID:                  plan.BindingTarget.WorkspaceID,
		EndpointID:                   plan.BindingTarget.EndpointID,
		InstanceID:                   plan.InstanceID,
		Port:                         plan.Port,
		PlanDigest:                   planDigest,
		CandidateControlSnapshotID:   candidateIDs.ControlSnapshotID,
		CandidateCatalogGenerationID: candidateIDs.CatalogGenerationID,
	}, nil
}

func mapModuleDisableDryRunFailureV1(err error) error {
	var failure *moduledisabledryrun.FailureV1
	if !errors.As(err, &failure) {
		return newModuleApplyFailureV1(moduleApplyFailureInternal, err)
	}
	code := moduleApplyFailureInternal
	switch failure.Code() {
	case moduledisabledryrun.FailureInvalidInputV1:
		code = moduleApplyFailurePlanInvalid
	case moduledisabledryrun.FailureArtifactInvalidV1:
		code = moduleApplyFailureArtifact
	case moduledisabledryrun.FailureStoreInvalidV1:
		code = moduleApplyFailureStore
	case moduledisabledryrun.FailurePointerConflictV1:
		code = moduleApplyFailurePointer
	case moduledisabledryrun.FailureTargetConflictV1:
		code = moduleApplyFailureTarget
	case moduledisabledryrun.FailureCancelledV1:
		code = moduleApplyFailureCancelled
	case moduledisabledryrun.FailureInternalV1:
		code = moduleApplyFailureInternal
	}
	return newModuleApplyFailureV1(code, err)
}

func moduleDisablePublicationV1(
	publication *moduledisabledryrun.PublicationV1,
) (currentstore.PublishControlCatalogInput, error) {
	if publication == nil {
		return currentstore.PublishControlCatalogInput{}, newModuleApplyFailureV1(
			moduleApplyFailureInternal,
			errors.New("MODULE_DISABLE WOULD_APPLY result has no publication"),
		)
	}
	return currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: publication.ExpectedPointerRevision,
		NewPointerRevision:      publication.NewPointerRevision,
		ControlRef:              publication.ControlRef,
		ControlCanonical:        append([]byte(nil), publication.ControlCanonical...),
		CatalogRef:              publication.CatalogRef,
		CatalogCanonical:        append([]byte(nil), publication.CatalogCanonical...),
	}, nil
}

func moduleApplyStatusFromDisableDryRunV1(
	status moduledisabledryrun.StatusV1,
) (moduleApplyStatusV1, error) {
	switch status {
	case moduledisabledryrun.StatusAlreadyAppliedV1:
		return moduleApplyStatusAlreadyApplied, nil
	case moduledisabledryrun.StatusNoChangeV1:
		return moduleApplyStatusNoChange, nil
	case moduledisabledryrun.StatusWouldApplyV1:
		return moduleApplyStatusWouldApply, nil
	default:
		return "", newModuleApplyFailureV1(
			moduleApplyFailureInternal,
			errors.New("shared MODULE_DISABLE evaluator returned an unknown status"),
		)
	}
}

func moduleDryRunBindingFromDisableV1(
	binding moduledisabledryrun.BindingRemovalV1,
) (moduleDryRunBindingProjectionV1, error) {
	projection := moduleDryRunBindingProjectionV1{
		Change:              moduleDryRunBindingRemoveV1,
		ConfigRef:           binding.ConfigRef,
		AuthorityCeilingRef: binding.AuthorityCeilingRef,
		StaticContextRefs:   append([]string(nil), binding.StaticContextRefs...),
	}
	if binding.PortBindingIndex != nil {
		index := *binding.PortBindingIndex
		projection.PortBindingIndex = &index
	}
	if binding.FailurePolicy != nil {
		policy := *binding.FailurePolicy
		projection.FailurePolicy = &policy
	}
	return projection, nil
}

func moduleDryRunCatalogFromDisableV1(
	change moduledisabledryrun.CatalogChangeV1,
) (moduleDryRunCatalogChangeV1, error) {
	switch change {
	case moduledisabledryrun.CatalogChangeNoneV1:
		return moduleDryRunCatalogNoneV1, nil
	case moduledisabledryrun.CatalogChangeRetainInstanceV1:
		return moduleDryRunCatalogRetainInstanceV1, nil
	case moduledisabledryrun.CatalogChangeRemoveInstanceV1:
		return moduleDryRunCatalogRemoveInstanceV1, nil
	default:
		return "", newModuleApplyFailureV1(
			moduleApplyFailureInternal,
			errors.New("shared MODULE_DISABLE evaluator returned an unknown Catalog change"),
		)
	}
}

var _ moduledisabledryrun.ReadViewV1 = (*currentstore.Store)(nil)
var _ moduledisabledryrun.ReadViewV1 = (*currentstore.ReadOnlyObserver)(nil)
