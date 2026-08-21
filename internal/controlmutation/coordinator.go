// Package controlmutation coordinates the first governed online mutation.
//
// It owns no transport, listener, Store lifecycle, SQL, recovery, or backup
// policy. The coordinator only sequences an already-admitted Control permit,
// the process-local confirmation authority, and the sole Current Store owner.
package controlmutation

import (
	"bytes"
	"context"
	"errors"
	"reflect"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlconfirmation"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidConfiguration = errors.New("controlmutation: invalid configuration")
	ErrInvalidRequest       = errors.New("controlmutation: invalid request")
	ErrSessionExpired       = errors.New("controlmutation: session expired")
	ErrForbidden            = errors.New("controlmutation: forbidden")
	ErrRevisionConflict     = errors.New("controlmutation: revision conflict")
	ErrIdempotencyConflict  = errors.New("controlmutation: idempotency conflict")
	ErrConfirmationRejected = errors.New("controlmutation: confirmation rejected")
	ErrMutationIneligible   = errors.New("controlmutation: mutation ineligible")
	ErrResourceExhausted    = errors.New("controlmutation: resource exhausted")
	ErrStoreBusy            = errors.New("controlmutation: Store busy")
	ErrStoreUnavailable     = errors.New("controlmutation: Store unavailable")
	ErrIntegrityFailure     = errors.New("controlmutation: integrity failure")
	ErrCancelled            = errors.New("controlmutation: cancelled")
)

// StoreV1 is the complete Store authority used by CoordinatorV1. A production
// value is the one already-open *currentstore.Store owned by serve composition.
// No lifecycle, recovery, raw publication, or generic receipt insert is
// exposed through this boundary.
type StoreV1 interface {
	EvaluateModuleDisableControlOperationV1(
		context.Context,
		currentstore.EvaluateModuleDisableControlOperationInputV1,
	) (currentstore.ModuleDisableControlOperationEvaluationV1, error)
	ResolveControlOperationReceiptV1(
		context.Context,
		[]byte,
		string,
	) (currentstore.StoredControlOperationReceiptV1, error)
	CommitModuleDisableControlOperationV1(
		context.Context,
		currentstore.CommitModuleDisableControlOperationInputV1,
	) (currentstore.StoredControlOperationReceiptV1, bool, error)
}

// CoordinatorV1 freezes the only valid ordering between confirmation and the
// durable mutation owner. It is safe for concurrent use when its dependencies
// are safe for concurrent use.
type CoordinatorV1 struct {
	store         StoreV1
	confirmations *controlconfirmation.RegistryV1
}

type IssueModuleDisableConfirmationInputV1 struct {
	Authorization        *controlsession.PermitV1
	Scope                controlapicontract.ControlScopeV1
	ExpectedPointer      controlapicontract.ExpectedResourceRefV1
	Body                 moduledisablecontract.ModuleDisableDryRunBodyV1
	IdempotencyKeyDigest string
}

// IssueModuleDisableConfirmationResultV1 is safe confirmation material. Raw
// Proof is the caller's sole copy and must remain process-local. The canonical
// evaluation is returned so the caller can bind its later mutation to the
// exact candidate shown; a durable miss re-evaluates that candidate in Store.
type IssueModuleDisableConfirmationResultV1 struct {
	Evaluation          moduledisablecontract.ModuleDisableEvaluationV1
	EvaluationCanonical []byte
	EvaluationDigest    string

	Statement          controlapicontract.ControlConfirmationStatementV1
	StatementCanonical []byte
	StatementDigest    string

	Request          controlapicontract.ControlOperationRequestV1
	RequestCanonical []byte
	RequestDigest    string

	Proof               []byte
	ExpiresAtUnixMicros uint64
}

type MutateModuleDisableInputV1 struct {
	Authorization        *controlsession.PermitV1
	Scope                controlapicontract.ControlScopeV1
	ExpectedPointer      controlapicontract.ExpectedResourceRefV1
	Body                 moduledisablecontract.ModuleDisableDryRunBodyV1
	IdempotencyKeyDigest string

	OperationEvaluationDigest string
	Proof                     []byte
}

// MutateModuleDisableResultV1 exposes only the stable request and generic
// receipt. Domain facts remain referenced by the receipt and owned by Store.
type MutateModuleDisableResultV1 struct {
	Request          controlapicontract.ControlOperationRequestV1
	RequestCanonical []byte
	RequestDigest    string

	Receipt          controlapicontract.ControlOperationReceiptV1
	ReceiptCanonical []byte
	ReceiptDigest    string
	Created          bool
}

func NewCoordinatorV1(
	store StoreV1,
	confirmations *controlconfirmation.RegistryV1,
) (*CoordinatorV1, error) {
	if nilInterfaceV1(store) || confirmations == nil {
		return nil, ErrInvalidConfiguration
	}
	return &CoordinatorV1{store: store, confirmations: confirmations}, nil
}

// IssueModuleDisableConfirmationV1 performs one effect-free eligible-slice
// evaluation, freezes its stable Statement and final MUTATE Request, and then
// asks the process-local registry to issue an exactly bound proof.
func (coordinator *CoordinatorV1) IssueModuleDisableConfirmationV1(
	ctx context.Context,
	input IssueModuleDisableConfirmationInputV1,
) (IssueModuleDisableConfirmationResultV1, error) {
	if coordinator == nil || nilInterfaceV1(coordinator.store) ||
		coordinator.confirmations == nil || ctx == nil {
		return IssueModuleDisableConfirmationResultV1{}, ErrInvalidConfiguration
	}
	if err := ctx.Err(); err != nil {
		return IssueModuleDisableConfirmationResultV1{}, ErrCancelled
	}
	facts, err := freezeIssueInputV1(input)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	evaluated, err := coordinator.store.EvaluateModuleDisableControlOperationV1(
		ctx,
		currentstore.EvaluateModuleDisableControlOperationInputV1{
			TenantID:        facts.scope.TenantID,
			ExpectedPointer: facts.expected,
			InputCanonical:  facts.inputCanonical,
			InputDigest:     facts.inputDigest,
		},
	)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, mapStoreErrorV1(err)
	}
	if err := verifyStoreEvaluationV1(facts, evaluated); err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	statement, statementCanonical, statementDigest, request, requestCanonical,
		requestDigest, err := freezeStableMutationV1(
		facts,
		evaluated.EvaluationDigest,
	)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	issued, err := coordinator.confirmations.Issue(controlconfirmation.ExactBindingV1{
		Authority:          input.Authorization,
		StatementCanonical: statementCanonical,
		StatementDigest:    statementDigest,
		RequestCanonical:   requestCanonical,
		RequestDigest:      requestDigest,
	})
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, mapIssueErrorV1(err)
	}
	defer zeroBytesV1(issued.Proof)
	return IssueModuleDisableConfirmationResultV1{
		Evaluation:          evaluated.Evaluation,
		EvaluationCanonical: bytes.Clone(evaluated.EvaluationCanonical),
		EvaluationDigest:    evaluated.EvaluationDigest,
		Statement:           statement,
		StatementCanonical:  bytes.Clone(statementCanonical),
		StatementDigest:     statementDigest,
		Request:             request,
		RequestCanonical:    bytes.Clone(requestCanonical),
		RequestDigest:       requestDigest,
		Proof:               bytes.Clone(issued.Proof),
		ExpiresAtUnixMicros: issued.ExpiresAtUnixMicros,
	}, nil
}

// MutateModuleDisableV1 reconstructs the stable Request before any current
// basis evaluation. It first resolves the durable identity. An exact hit is
// returned without inspecting proof or evaluation bytes; only a miss may claim
// the current proof and enter the Store mutation boundary.
func (coordinator *CoordinatorV1) MutateModuleDisableV1(
	ctx context.Context,
	input MutateModuleDisableInputV1,
) (MutateModuleDisableResultV1, error) {
	if coordinator == nil || nilInterfaceV1(coordinator.store) ||
		coordinator.confirmations == nil || ctx == nil {
		return MutateModuleDisableResultV1{}, ErrInvalidConfiguration
	}
	if err := ctx.Err(); err != nil {
		return MutateModuleDisableResultV1{}, ErrCancelled
	}
	facts, err := freezeMutationInputV1(input)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	defer zeroBytesV1(facts.proof)
	_, statementCanonical, statementDigest, request, requestCanonical,
		requestDigest, err := freezeStableMutationV1(
		facts.issueFactsV1,
		facts.evaluationDigest,
	)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}

	record, err := coordinator.store.ResolveControlOperationReceiptV1(
		ctx,
		requestCanonical,
		requestDigest,
	)
	if err == nil {
		return mutationResultFromStoredV1(request, requestCanonical, requestDigest, record, false)
	}
	if !errors.Is(err, currentstore.ErrControlOperationReceiptNotFoundV1) {
		return MutateModuleDisableResultV1{}, mapStoreErrorV1(err)
	}

	// A durable miss must first prove exact operator confirmation. Missing or
	// wrong proof therefore cannot trigger even the effect-free Store evaluator.
	claim, err := coordinator.confirmations.Claim(
		facts.proof,
		controlconfirmation.ExactBindingV1{
			Authority:          input.Authorization,
			StatementCanonical: statementCanonical,
			StatementDigest:    statementDigest,
			RequestCanonical:   requestCanonical,
			RequestDigest:      requestDigest,
		},
	)
	if err != nil {
		return MutateModuleDisableResultV1{}, mapClaimErrorV1(err)
	}
	resolvedClaim := false
	defer func() {
		if !resolvedClaim {
			claim.ReleaseNoEffect()
		}
	}()

	session, scopeSetDigest, err := authorizeCurrentV1(input.Authorization, facts.scope)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	if session.PrincipalID != request.PrincipalID ||
		session.ScopeSetDigest != scopeSetDigest {
		return MutateModuleDisableResultV1{}, ErrForbidden
	}
	if err := ctx.Err(); err != nil {
		return MutateModuleDisableResultV1{}, ErrCancelled
	}

	// Only an exactly claimed and currently authorized request may inspect the
	// current basis. The sole Store evaluator must reproduce the digest already
	// bound into the stable Request; its canonical result is then passed to
	// Commit without accepting canonical evaluation bytes from transport.
	evaluated, err := coordinator.store.EvaluateModuleDisableControlOperationV1(
		ctx,
		currentstore.EvaluateModuleDisableControlOperationInputV1{
			TenantID:        facts.scope.TenantID,
			ExpectedPointer: facts.expected,
			InputCanonical:  facts.inputCanonical,
			InputDigest:     facts.inputDigest,
		},
	)
	if err != nil {
		// Another exact request may have committed after the first durable miss.
		// Resolve once more before releasing this still-zero-effect claim.
		concurrent, resolveErr := coordinator.store.ResolveControlOperationReceiptV1(
			ctx,
			requestCanonical,
			requestDigest,
		)
		if resolveErr == nil {
			claim.Consume()
			resolvedClaim = true
			return mutationResultFromStoredV1(
				request, requestCanonical, requestDigest, concurrent, false,
			)
		}
		if !errors.Is(resolveErr, currentstore.ErrControlOperationReceiptNotFoundV1) {
			return MutateModuleDisableResultV1{}, mapStoreErrorV1(resolveErr)
		}
		return MutateModuleDisableResultV1{}, mapStoreErrorV1(err)
	}
	if err := verifyStoreEvaluationV1(facts.issueFactsV1, evaluated); err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	if evaluated.EvaluationDigest != facts.evaluationDigest {
		return MutateModuleDisableResultV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return MutateModuleDisableResultV1{}, ErrCancelled
	}

	// Evaluate may be non-trivial. Revalidate the exact admission immediately
	// before the effect boundary and use only this final authorization basis.
	session, scopeSetDigest, err = authorizeCurrentV1(input.Authorization, facts.scope)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	if session.PrincipalID != request.PrincipalID ||
		session.ScopeSetDigest != scopeSetDigest {
		return MutateModuleDisableResultV1{}, ErrForbidden
	}

	// From this point a commit may have happened even if its response is lost.
	// Consume before crossing that boundary; errors and commit ambiguity must be
	// resolved only by a later exact durable lookup, never by reopening proof.
	claim.Consume()
	resolvedClaim = true
	record, created, err := coordinator.store.CommitModuleDisableControlOperationV1(
		ctx,
		currentstore.CommitModuleDisableControlOperationInputV1{
			AuthorizationRevision: session.AuthorizationRevision,
			ScopeSetDigest:        scopeSetDigest,
			RequestCanonical:      requestCanonical,
			RequestDigest:         requestDigest,
			InputCanonical:        facts.inputCanonical,
			EvaluationCanonical:   evaluated.EvaluationCanonical,
		},
	)
	if err != nil {
		return MutateModuleDisableResultV1{}, mapStoreErrorV1(err)
	}
	return mutationResultFromStoredV1(
		request,
		requestCanonical,
		requestDigest,
		record,
		created,
	)
}

type issueFactsV1 struct {
	session        controlapicontract.ControlSessionV1
	scope          controlapicontract.ControlScopeV1
	scopeDigest    string
	expected       controlapicontract.ExpectedResourceRefV1
	body           moduledisablecontract.ModuleDisableDryRunBodyV1
	inputCanonical []byte
	inputDigest    string
	keyDigest      string
}

type mutationFactsV1 struct {
	issueFactsV1
	evaluationDigest string
	proof            []byte
}

func freezeIssueInputV1(
	input IssueModuleDisableConfirmationInputV1,
) (issueFactsV1, error) {
	scope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(input.Scope)
	if err != nil || scope.Kind != controlapicontract.ScopeTenantV1 ||
		scope.WorkspaceID != "" || !moduleapi.ValidSHA256(input.IdempotencyKeyDigest) ||
		input.ExpectedPointer.Validate() != nil ||
		input.ExpectedPointer.Kind != controlapicontract.ResourcePublishedPointerV1 ||
		input.ExpectedPointer.ResourceID != scope.TenantID {
		return issueFactsV1{}, ErrInvalidRequest
	}
	body, canonical, digest, err := moduledisablecontract.NewModuleDisableDryRunBodyV1(input.Body)
	if err != nil || body.BindingTarget.Kind != moduledisablecontract.ModuleBindingTargetProfileV1 ||
		body.BindingTarget.ProfileID == "" || body.BindingTarget.WorkspaceID != "" ||
		body.BindingTarget.EndpointID != "" ||
		body.Port != (moduleapi.PortRef{
			Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
		}) || body.ExpectedPointerRevision != input.ExpectedPointer.Revision {
		return issueFactsV1{}, ErrInvalidRequest
	}
	session, currentScopeSetDigest, err := authorizeCurrentV1(input.Authorization, scope)
	if err != nil {
		return issueFactsV1{}, err
	}
	if currentScopeSetDigest != session.ScopeSetDigest || session.PrincipalID == "" {
		return issueFactsV1{}, ErrForbidden
	}
	return issueFactsV1{
		session:        session,
		scope:          scope,
		scopeDigest:    scopeDigest,
		expected:       input.ExpectedPointer,
		body:           body,
		inputCanonical: bytes.Clone(canonical),
		inputDigest:    digest,
		keyDigest:      input.IdempotencyKeyDigest,
	}, nil
}

func freezeMutationInputV1(input MutateModuleDisableInputV1) (mutationFactsV1, error) {
	base, err := freezeIssueInputV1(IssueModuleDisableConfirmationInputV1{
		Authorization:        input.Authorization,
		Scope:                input.Scope,
		ExpectedPointer:      input.ExpectedPointer,
		Body:                 input.Body,
		IdempotencyKeyDigest: input.IdempotencyKeyDigest,
	})
	if err != nil {
		return mutationFactsV1{}, err
	}
	if !moduleapi.ValidSHA256(input.OperationEvaluationDigest) {
		return mutationFactsV1{}, ErrInvalidRequest
	}
	return mutationFactsV1{
		issueFactsV1:     base,
		evaluationDigest: input.OperationEvaluationDigest,
		proof:            bytes.Clone(input.Proof),
	}, nil
}

func zeroBytesV1(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func authorizeCurrentV1(
	permit *controlsession.PermitV1,
	scope controlapicontract.ControlScopeV1,
) (controlapicontract.ControlSessionV1, string, error) {
	if permit == nil {
		return controlapicontract.ControlSessionV1{}, "", ErrForbidden
	}
	session, scopeSetDigest, err := permit.AuthorizeCurrentV1(
		controlapicontract.CapabilityOperateModulesV1,
		scope,
		true,
	)
	if err != nil {
		return controlapicontract.ControlSessionV1{}, "", mapSessionErrorV1(err)
	}
	return session, scopeSetDigest, nil
}

func freezeStableMutationV1(
	facts issueFactsV1,
	evaluationDigest string,
) (
	controlapicontract.ControlConfirmationStatementV1,
	[]byte,
	string,
	controlapicontract.ControlOperationRequestV1,
	[]byte,
	string,
	error,
) {
	statement, statementCanonical, statementDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(
			controlapicontract.ControlConfirmationStatementV1{
				SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
				PrincipalID:               facts.session.PrincipalID,
				Capability:                controlapicontract.CapabilityOperateModulesV1,
				Intent:                    controlapicontract.OperationIntentMutateV1,
				Operation:                 controlapicontract.OperationModuleDisableV1,
				Scope:                     facts.scope,
				ScopeDigest:               facts.scopeDigest,
				IdempotencyKeyDigest:      facts.keyDigest,
				InputDigest:               facts.inputDigest,
				OperationEvaluationDigest: evaluationDigest,
				ExpectedRef:               facts.expected,
			},
		)
	if err != nil {
		return controlapicontract.ControlConfirmationStatementV1{}, nil, "",
			controlapicontract.ControlOperationRequestV1{}, nil, "", ErrIntegrityFailure
	}
	request, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(
			controlapicontract.ControlOperationRequestV1{
				SchemaVersion:             controlapicontract.ControlOperationRequestSchemaVersionV1,
				PrincipalID:               facts.session.PrincipalID,
				Capability:                controlapicontract.CapabilityOperateModulesV1,
				Scope:                     facts.scope,
				ScopeDigest:               facts.scopeDigest,
				Operation:                 controlapicontract.OperationModuleDisableV1,
				Intent:                    controlapicontract.OperationIntentMutateV1,
				IdempotencyKeyDigest:      facts.keyDigest,
				InputDigest:               facts.inputDigest,
				OperationEvaluationDigest: evaluationDigest,
				ExpectedRef:               facts.expected,
				ConfirmationDigest:        statementDigest,
			},
		)
	if err != nil {
		return controlapicontract.ControlConfirmationStatementV1{}, nil, "",
			controlapicontract.ControlOperationRequestV1{}, nil, "", ErrIntegrityFailure
	}
	return statement, statementCanonical, statementDigest,
		request, requestCanonical, requestDigest, nil
}

func verifyStoreEvaluationV1(
	facts issueFactsV1,
	result currentstore.ModuleDisableControlOperationEvaluationV1,
) error {
	plan, err := moduleapplyplan.RestoreProfileContextDisableV1(
		result.PlanCanonical,
		result.PlanDigest,
	)
	if err != nil || plan != result.Plan || plan.TenantID != facts.scope.TenantID ||
		plan.ExpectedPointerRevision != facts.expected.Revision ||
		plan.BindingTarget.ProfileID != facts.body.BindingTarget.ProfileID ||
		plan.InstanceID != facts.body.InstanceID || plan.Port != facts.body.Port ||
		result.Input != facts.body || result.InputDigest != facts.inputDigest ||
		!bytes.Equal(result.InputCanonical, facts.inputCanonical) {
		return ErrIntegrityFailure
	}
	evaluation, err := moduledisablecontract.RestoreModuleDisableEvaluationV1(
		result.EvaluationCanonical,
		result.EvaluationDigest,
	)
	if err != nil || !reflect.DeepEqual(evaluation, result.Evaluation) ||
		evaluation.Operation != controlapicontract.OperationModuleDisableV1 ||
		evaluation.InputDigest != facts.inputDigest ||
		evaluation.ExpectedRef != facts.expected ||
		evaluation.Projection.PlanDigest != result.PlanDigest {
		return ErrIntegrityFailure
	}
	return nil
}

func mutationResultFromStoredV1(
	request controlapicontract.ControlOperationRequestV1,
	requestCanonical []byte,
	requestDigest string,
	record currentstore.StoredControlOperationReceiptV1,
	created bool,
) (MutateModuleDisableResultV1, error) {
	if record.Request != request || record.RequestDigest != requestDigest ||
		!bytes.Equal(record.RequestCanonical, requestCanonical) ||
		record.ControlReceipt.RequestDigest != requestDigest ||
		record.ControlReceipt.Status != record.Status {
		return MutateModuleDisableResultV1{}, ErrIntegrityFailure
	}
	receipt, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(record.ControlReceipt)
	if err != nil || receiptDigest != record.ReceiptDigest ||
		!bytes.Equal(receiptCanonical, record.ControlReceiptCanonical) {
		return MutateModuleDisableResultV1{}, ErrIntegrityFailure
	}
	return MutateModuleDisableResultV1{
		Request:          request,
		RequestCanonical: bytes.Clone(requestCanonical),
		RequestDigest:    requestDigest,
		Receipt:          receipt,
		ReceiptCanonical: bytes.Clone(receiptCanonical),
		ReceiptDigest:    receiptDigest,
		Created:          created,
	}, nil
}

func mapSessionErrorV1(err error) error {
	switch {
	case errors.Is(err, controlsession.ErrSessionExpired),
		errors.Is(err, controlsession.ErrUnauthenticated),
		errors.Is(err, controlsession.ErrClosed):
		return ErrSessionExpired
	case errors.Is(err, controlsession.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, controlsession.ErrResourceExhausted):
		return ErrResourceExhausted
	default:
		return ErrForbidden
	}
}

func mapIssueErrorV1(err error) error {
	switch {
	case errors.Is(err, controlconfirmation.ErrResourceExhausted):
		return ErrResourceExhausted
	case errors.Is(err, controlconfirmation.ErrInvalidInput):
		return ErrForbidden
	case errors.Is(err, controlconfirmation.ErrClosed):
		return ErrStoreUnavailable
	case errors.Is(err, controlconfirmation.ErrEntropyUnavailable),
		errors.Is(err, controlconfirmation.ErrInvalidConfiguration):
		return ErrStoreUnavailable
	default:
		return ErrStoreUnavailable
	}
}

func mapClaimErrorV1(err error) error {
	switch {
	case errors.Is(err, controlconfirmation.ErrProofRejected):
		return ErrConfirmationRejected
	case errors.Is(err, controlconfirmation.ErrInvalidConfiguration):
		return ErrStoreUnavailable
	default:
		return ErrConfirmationRejected
	}
}

func mapStoreErrorV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrCancelled
	case errors.Is(err, currentstore.ErrControlOperationReceiptConflictV1):
		return ErrIdempotencyConflict
	case errors.Is(err, currentstore.ErrControlOperationReceiptCapacityV1):
		return ErrResourceExhausted
	case errors.Is(err, currentstore.ErrModuleDisableControlOperationIneligibleV1):
		return ErrMutationIneligible
	case errors.Is(err, currentstore.ErrPublicationConflict):
		return ErrRevisionConflict
	case errors.Is(err, currentstore.ErrInvalidControlOperationReceiptV1),
		errors.Is(err, currentstore.ErrInvalidPublishedBasis),
		errors.Is(err, currentstore.ErrInvalidPublication):
		return ErrInvalidRequest
	case errors.Is(err, currentstore.ErrControlOperationReceiptIntegrityV1),
		errors.Is(err, currentstore.ErrPublishedBasisIntegrity),
		errors.Is(err, currentstore.ErrAdmissionIntegrity):
		return ErrIntegrityFailure
	case errors.Is(err, currentstore.ErrOwnerActive):
		return ErrStoreBusy
	case errors.Is(err, currentstore.ErrStoreClosed):
		return ErrStoreUnavailable
	default:
		return ErrStoreUnavailable
	}
}

func nilInterfaceV1(value any) bool {
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
