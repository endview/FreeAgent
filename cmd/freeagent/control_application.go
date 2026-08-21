package main

import (
	"context"
	"errors"
	"reflect"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// productionControlPublishedBasisReaderV1 exposes only the two read methods
// consumed by controlapp. It owns no Store lifecycle and maps private Store
// failures before they cross the application boundary.
type productionControlPublishedBasisReaderV1 struct {
	store *currentstore.Store
}

func (reader productionControlPublishedBasisReaderV1) LoadPublishedBasis(
	ctx context.Context,
	tenantID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	if reader.store == nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, controlapp.ErrStoreUnavailable
	}
	basis, control, catalog, err := reader.store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, mapControlStoreReadErrorV1(err)
	}
	return basis, control, catalog, nil
}

func (reader productionControlPublishedBasisReaderV1) VerifyPublishedControlCatalogClosureV1(
	ctx context.Context,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if reader.store == nil {
		return controlapp.ErrStoreUnavailable
	}
	return mapControlStoreReadErrorV1(
		reader.store.VerifyPublishedControlCatalogClosureV1(ctx, control, catalog),
	)
}

func mapControlStoreReadErrorV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return controlapp.ErrCancelled
	case errors.Is(err, currentstore.ErrInvalidPublishedBasis):
		return controlapp.ErrInvalidRequest
	case errors.Is(err, currentstore.ErrPublishedBasisNotFound):
		return controlapp.ErrNotFound
	case errors.Is(err, currentstore.ErrPublishedBasisIntegrity),
		errors.Is(err, currentstore.ErrAdmissionIntegrity):
		return controlapp.ErrIntegrityFailure
	case errors.Is(err, currentstore.ErrOwnerActive):
		return controlapp.ErrStoreBusy
	case errors.Is(err, currentstore.ErrStoreClosed):
		return controlapp.ErrStoreUnavailable
	default:
		return controlapp.ErrStoreUnavailable
	}
}

type productionModuleDisableDryRunServiceV1 struct {
	store        *currentstore.Store
	artifactRoot string
}

func (service *productionModuleDisableDryRunServiceV1) DryRunModuleDisableV1(
	ctx context.Context,
	input controlapp.ModuleDisableDryRunInputV1,
) (controlapp.ModuleDisableDryRunResultV1, error) {
	if service == nil || service.store == nil || ctx == nil ||
		nilControlAuthorizationV1(input.Authorization) || input.ObservedAt == 0 ||
		input.ObservedAt > uint64(1<<53-1) {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrCancelled
	}
	requestSession := input.Authorization.Session()
	frozenSession, sessionCanonical, _, err := controlapicontract.NewControlSessionV1(
		requestSession,
	)
	clear(sessionCanonical)
	if err != nil || !reflect.DeepEqual(frozenSession, requestSession) {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	if input.ObservedAt < frozenSession.IssuedAtUnixMicros ||
		input.ObservedAt >= frozenSession.ExpiresAtUnixMicros {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrSessionExpired
	}
	requestSession = frozenSession
	requestScope, scopeCanonical, requestScopeDigest, err := controlapicontract.NewControlScopeV1(
		input.Scope,
	)
	clear(scopeCanonical)
	if err != nil || !reflect.DeepEqual(requestScope, input.Scope) {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	if !validControlAuthorizationForDisableV1(
		input.Authorization,
		requestSession,
		requestScope,
		requestScopeDigest,
	) {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrForbidden
	}
	body, bodyCanonical, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(
		input.Body,
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	defer clear(bodyCanonical)
	if err := input.ExpectedPointer.Validate(); err != nil ||
		input.ExpectedPointer.Kind != controlapicontract.ResourcePublishedPointerV1 ||
		input.ExpectedPointer.ResourceID != requestScope.TenantID ||
		input.ExpectedPointer.Revision != body.ExpectedPointerRevision {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	if err := authorizeModuleDisableTargetV1(requestScope, body.BindingTarget); err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}

	plan := moduleApplyPlanV1{
		SchemaVersion:           moduleApplyPlanSchemaV1,
		DesiredState:            moduleApplyDisabledV1,
		TenantID:                requestScope.TenantID,
		ExpectedPointerRevision: body.ExpectedPointerRevision,
		BindingTarget: moduleApplyBindingTargetV1{
			Kind:        moduleApplyBindingTargetKindV1(body.BindingTarget.Kind),
			ProfileID:   body.BindingTarget.ProfileID,
			WorkspaceID: body.BindingTarget.WorkspaceID,
			EndpointID:  body.BindingTarget.EndpointID,
		},
		InstanceID: body.InstanceID,
		Port:       body.Port,
	}
	frozenPlan, _, planDigest, err := freezeModuleApplyPlanValueV1(plan)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrInvalidRequest
	}
	evaluated, err := evaluateDisabledModulePlanOnViewV1(
		ctx,
		service.store,
		service.artifactRoot,
		frozenPlan,
		planDigest,
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, mapControlDisableFailureV1(err)
	}
	preconditionRef, err := controlapp.PublishedPointerRefV1(
		controlapp.PublishedBasisRefV1(evaluated.PreconditionBasis),
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrIntegrityFailure
	}
	if preconditionRef != input.ExpectedPointer {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrRevisionConflict
	}
	projection, err := controlDisableProjectionV1(evaluated, planDigest, frozenPlan)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}
	request, requestCanonical, requestDigest, err := controlapicontract.NewControlOperationRequestV1(
		controlapicontract.ControlOperationRequestV1{
			SchemaVersion: controlapicontract.ControlOperationRequestSchemaVersionV1,
			PrincipalID:   requestSession.PrincipalID,
			Capability:    controlapicontract.CapabilityOperateModulesV1,
			Scope:         requestScope,
			ScopeDigest:   requestScopeDigest,
			Operation:     controlapicontract.OperationModuleDisableV1,
			Intent:        controlapicontract.OperationIntentDryRunV1,
			InputDigest:   inputDigest,
			ExpectedRef:   input.ExpectedPointer,
		},
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrIntegrityFailure
	}
	receipt, _, receiptDigest, err := controlapp.NewModuleDisableDryRunReceiptV1(
		requestCanonical,
		requestDigest,
		input.ObservedAt,
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}
	if !validControlAuthorizationForDisableV1(
		input.Authorization,
		requestSession,
		requestScope,
		requestScopeDigest,
	) {
		return controlapp.ModuleDisableDryRunResultV1{}, controlapp.ErrForbidden
	}
	return controlapp.ModuleDisableDryRunResultV1{
		SchemaVersion: controlapp.ModuleDisableDryRunResultSchemaVersionV1,
		Request:       request,
		RequestDigest: requestDigest,
		Receipt:       receipt,
		ReceiptDigest: receiptDigest,
		Projection:    projection,
	}, nil
}

func nilControlAuthorizationV1(value controlapp.AuthorizationContextV1) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func validControlAuthorizationForDisableV1(
	authorization controlapp.AuthorizationContextV1,
	session controlapicontract.ControlSessionV1,
	scope controlapicontract.ControlScopeV1,
	scopeDigest string,
) bool {
	if nilControlAuthorizationV1(authorization) {
		return false
	}
	digest := authorization.Digest()
	if !reflect.DeepEqual(authorization.Session(), session) ||
		!moduleapi.ValidSHA256(digest) || digest != session.ScopeSetDigest ||
		!authorization.Allows(scope) {
		return false
	}
	for _, capability := range session.Capabilities {
		if capability == controlapicontract.CapabilityOperateModulesV1 {
			_, _, actualScopeDigest, err := controlapicontract.NewControlScopeV1(scope)
			return err == nil && actualScopeDigest == scopeDigest
		}
	}
	return false
}

func authorizeModuleDisableTargetV1(
	scope controlapicontract.ControlScopeV1,
	target controlapp.ModuleBindingTargetV1,
) error {
	switch target.Kind {
	case controlapp.ModuleBindingTargetProfileV1:
		if scope.Kind != controlapicontract.ScopeTenantV1 {
			return controlapp.ErrNotFound
		}
	case controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1:
		if scope.Kind == controlapicontract.ScopeWorkspaceV1 &&
			target.WorkspaceID != scope.WorkspaceID {
			return controlapp.ErrNotFound
		}
	default:
		return controlapp.ErrInvalidRequest
	}
	return nil
}

func mapControlDisableFailureV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return controlapp.ErrCancelled
	case errors.Is(err, currentstore.ErrOwnerActive):
		return controlapp.ErrStoreBusy
	case errors.Is(err, currentstore.ErrStoreClosed):
		return controlapp.ErrStoreUnavailable
	case errors.Is(err, currentstore.ErrPublishedBasisIntegrity),
		errors.Is(err, currentstore.ErrAdmissionIntegrity):
		return controlapp.ErrIntegrityFailure
	case errors.Is(err, currentstore.ErrInvalidPublishedBasis):
		return controlapp.ErrInvalidRequest
	}
	switch moduleApplyFailureCodeOfV1(err) {
	case moduleApplyFailurePlanInvalid:
		return controlapp.ErrInvalidRequest
	case moduleApplyFailureArtifact, moduleApplyFailureStore:
		return controlapp.ErrIntegrityFailure
	case moduleApplyFailurePointer:
		return controlapp.ErrRevisionConflict
	case moduleApplyFailureTarget:
		return controlapp.ErrNotFound
	case moduleApplyFailureCancelled:
		return controlapp.ErrCancelled
	default:
		return controlapp.ErrStoreUnavailable
	}
}

func controlDisableProjectionV1(
	result moduledisabledryrun.ResultV1,
	planDigest string,
	plan moduleApplyPlanV1,
) (controlapp.ModuleDisableProjectionV1, error) {
	if !moduleapi.ValidSHA256(planDigest) || plan.InstanceID == "" ||
		result.PreconditionBasis.Validate() != nil ||
		result.ObservedBasis.Validate() != nil ||
		result.CandidateBasis.Validate() != nil {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	disposition := controlapp.ModuleDisableDispositionV1(result.Status)
	switch disposition {
	case controlapp.ModuleDisableAlreadyAppliedV1,
		controlapp.ModuleDisableNoChangeV1,
		controlapp.ModuleDisableWouldApplyV1:
	default:
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	catalogChange := controlapp.ModuleDisableCatalogChangeV1(result.CatalogChange)
	switch catalogChange {
	case controlapp.ModuleDisableCatalogNoneV1,
		controlapp.ModuleDisableCatalogRetainInstanceV1,
		controlapp.ModuleDisableCatalogRemoveInstanceV1:
	default:
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	projection := controlapp.ModuleDisableProjectionV1{
		Disposition:       disposition,
		PlanDigest:        planDigest,
		InstanceID:        plan.InstanceID,
		PreconditionBasis: controlapp.PublishedBasisRefV1(result.PreconditionBasis),
		ObservedBasis:     controlapp.PublishedBasisRefV1(result.ObservedBasis),
		CandidateBasis:    controlapp.PublishedBasisRefV1(result.CandidateBasis),
		CandidateState:    controlapp.ModuleDisableProjectedNotReservedV1,
		CatalogChange:     catalogChange,
	}
	if disposition == controlapp.ModuleDisableNoChangeV1 {
		if result.PreconditionBasis != result.ObservedBasis ||
			result.ObservedBasis != result.CandidateBasis ||
			catalogChange != controlapp.ModuleDisableCatalogNoneV1 {
			return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
		}
	}
	if disposition == controlapp.ModuleDisableAlreadyAppliedV1 {
		if !nextChangedControlPublishedBasisV1(
			result.PreconditionBasis,
			result.ObservedBasis,
		) || result.ObservedBasis != result.CandidateBasis ||
			catalogChange != controlapp.ModuleDisableCatalogNoneV1 {
			return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
		}
	}
	if disposition != controlapp.ModuleDisableWouldApplyV1 {
		if result.Publication != nil || result.BindingRemoval.PortBindingIndex != nil ||
			result.BindingRemoval.ConfigRef != "" ||
			result.BindingRemoval.AuthorityCeilingRef != "" ||
			len(result.BindingRemoval.StaticContextRefs) != 0 {
			return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
		}
		return projection, nil
	}
	if result.PreconditionBasis != result.ObservedBasis ||
		!nextChangedControlPublishedBasisV1(
			result.ObservedBasis,
			result.CandidateBasis,
		) || catalogChange == controlapp.ModuleDisableCatalogNoneV1 ||
		result.Publication == nil || result.BindingRemoval.PortBindingIndex == nil ||
		result.BindingRemoval.FailurePolicy == nil ||
		result.BindingRemoval.Port.Validate() != nil ||
		result.BindingRemoval.FailurePolicy.Validate() != nil ||
		!moduleapi.ValidSHA256(result.BindingRemoval.ConfigRef) ||
		!moduleapi.ValidSHA256(result.BindingRemoval.AuthorityCeilingRef) {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	publication := result.Publication
	if publication.ExpectedPointerRevision != result.PreconditionBasis.PointerRevision ||
		publication.NewPointerRevision != result.CandidateBasis.PointerRevision ||
		publication.ControlRef != result.CandidateBasis.Control ||
		publication.CatalogRef != result.CandidateBasis.Catalog {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	control, err := controlcontract.RestoreControlSnapshot(
		publication.ControlCanonical,
		publication.ControlRef,
	)
	if err != nil || control.TenantID != plan.TenantID {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		publication.CatalogCanonical,
		publication.CatalogRef,
	)
	if err != nil || catalog.TenantID != plan.TenantID ||
		catalog.ControlSnapshotID != publication.ControlRef.SnapshotID ||
		catalog.ControlSnapshotDigest != publication.ControlRef.Digest {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	_, candidateHasInstance := catalog.FindInstance(plan.InstanceID)
	if (catalogChange == controlapp.ModuleDisableCatalogRetainInstanceV1 &&
		!candidateHasInstance) ||
		(catalogChange == controlapp.ModuleDisableCatalogRemoveInstanceV1 &&
			candidateHasInstance) {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	wantTargetKind := moduledisabledryrun.TargetKindV1(plan.BindingTarget.Kind)
	if result.BindingRemoval.TargetKind != wantTargetKind ||
		result.BindingRemoval.ProfileID != plan.BindingTarget.ProfileID ||
		result.BindingRemoval.WorkspaceID != plan.BindingTarget.WorkspaceID ||
		result.BindingRemoval.EndpointID != plan.BindingTarget.EndpointID ||
		result.BindingRemoval.Port != plan.Port {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	if result.BindingRemoval.TargetKind ==
		moduledisabledryrun.TargetWorkspaceChannelEndpointV1 &&
		*result.BindingRemoval.PortBindingIndex != 0 {
		return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
	}
	for _, reference := range result.BindingRemoval.StaticContextRefs {
		if !moduleapi.ValidSHA256(reference) {
			return controlapp.ModuleDisableProjectionV1{}, controlapp.ErrIntegrityFailure
		}
	}
	projection.BindingRemoval = &controlapp.ModuleDisableBindingRemovalV1{
		Target: controlapp.ModuleBindingTargetV1{
			Kind:        controlapp.ModuleBindingTargetKindV1(result.BindingRemoval.TargetKind),
			ProfileID:   result.BindingRemoval.ProfileID,
			WorkspaceID: result.BindingRemoval.WorkspaceID,
			EndpointID:  result.BindingRemoval.EndpointID,
		},
		Port:                result.BindingRemoval.Port,
		PortBindingIndex:    *result.BindingRemoval.PortBindingIndex,
		ConfigRef:           result.BindingRemoval.ConfigRef,
		AuthorityCeilingRef: result.BindingRemoval.AuthorityCeilingRef,
		StaticContextRefs: append(
			[]string(nil),
			result.BindingRemoval.StaticContextRefs...,
		),
		FailurePolicy: *result.BindingRemoval.FailurePolicy,
	}
	return projection, nil
}

func nextChangedControlPublishedBasisV1(
	before controlcontract.PublishedBasis,
	after controlcontract.PublishedBasis,
) bool {
	return before.TenantID == after.TenantID &&
		after.PointerRevision == before.PointerRevision+1 &&
		after.Control.Revision == before.Control.Revision+1 &&
		before.Control.SnapshotID != after.Control.SnapshotID &&
		before.Control.Digest != after.Control.Digest &&
		after.Catalog.Generation == before.Catalog.Generation+1 &&
		before.Catalog.GenerationID != after.Catalog.GenerationID &&
		before.Catalog.Digest != after.Catalog.Digest
}

var _ controlapp.PublishedBasisReaderV1 = productionControlPublishedBasisReaderV1{}
