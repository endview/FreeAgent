package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
)

var (
	// ErrModuleDisableControlOperationIneligibleV1 identifies a valid current
	// publication that is outside the first controlled mutation slice. It is
	// distinct from Store corruption: broader Context placement, authority,
	// failure policy, or execution classes may be valid runtime facts while
	// remaining ineligible for this operation.
	ErrModuleDisableControlOperationIneligibleV1 = errors.New(
		"currentstore: Module Disable operation is outside the first eligible slice",
	)
)

// EvaluateModuleDisableControlOperationInputV1 is request-independent input
// for confirmation issue. Tenant and the full expected pointer are injected
// by the authenticated application boundary; the body remains exact canonical
// caller input. No idempotency, confirmation, session, proof, or receipt fact
// is accepted here.
type EvaluateModuleDisableControlOperationInputV1 struct {
	TenantID        string
	ExpectedPointer controlapicontract.ExpectedResourceRefV1
	InputCanonical  []byte
	InputDigest     string
}

// ModuleDisableControlOperationEvaluationV1 is a detached effect-free result
// suitable for constructing a confirmation statement. It carries no Request,
// receipt, proof, clock, path, grant, provider, or mutation capability.
type ModuleDisableControlOperationEvaluationV1 struct {
	Input          moduledisablecontract.ModuleDisableDryRunBodyV1
	InputCanonical []byte
	InputDigest    string

	Plan          moduleapplyplan.ProfileContextDisablePlanV1
	PlanCanonical []byte
	PlanDigest    string

	Evaluation          moduledisablecontract.ModuleDisableEvaluationV1
	EvaluationCanonical []byte
	EvaluationDigest    string
}

// EvaluateModuleDisableControlOperationV1 evaluates the exact current basis
// in one coherent read transaction and enforces the complete first-slice
// eligibility gate: PROFILE context.provide/v1, OPTIONAL, DECLARATIVE/static,
// trusted-instruction without summary/drop, and the exact deny-all authority.
// It never writes or constructs a Request, receipt, proof, or timestamp.
func (store *Store) EvaluateModuleDisableControlOperationV1(
	ctx context.Context,
	input EvaluateModuleDisableControlOperationInputV1,
) (
	result ModuleDisableControlOperationEvaluationV1,
	returnErr error,
) {
	defer func() {
		if store != nil {
			returnErr = classifySQLiteOwnerContention(store.path, returnErr)
		}
	}()
	if ctx == nil {
		return ModuleDisableControlOperationEvaluationV1{},
			ErrInvalidControlOperationReceiptV1
	}
	frozenInput := input
	frozenInput.InputCanonical = bytes.Clone(input.InputCanonical)
	if err := validatePublishedBasisTenantID(frozenInput.TenantID); err != nil ||
		frozenInput.ExpectedPointer.Validate() != nil ||
		frozenInput.ExpectedPointer.Kind != controlapicontract.ResourcePublishedPointerV1 ||
		frozenInput.ExpectedPointer.ResourceID != frozenInput.TenantID {
		return ModuleDisableControlOperationEvaluationV1{}, fmt.Errorf(
			"%w: invalid Module Disable evaluation boundary",
			ErrInvalidControlOperationReceiptV1,
		)
	}
	body, plan, planCanonical, planDigest, replayInput, err :=
		prepareModuleDisableControlOperationReplayInputV1(
			frozenInput.TenantID,
			frozenInput.ExpectedPointer,
			frozenInput.InputCanonical,
			frozenInput.InputDigest,
		)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, fmt.Errorf(
			"currentstore: acquire Module Disable evaluation connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, fmt.Errorf(
			"currentstore: begin Module Disable evaluation: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	preBasis, preControl, preCatalog, err :=
		loadCurrentModuleDisableBasisInTransactionV1(
			ctx,
			connection,
			frozenInput.TenantID,
		)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, err
	}
	preBasisRef := apiBasisFromControlBasisV1(preBasis)
	_, _, preBasisDigest, err := controlapicontract.NewPublishedBasisRefV1(preBasisRef)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{},
			controlReceiptIntegrityV1("freeze Module Disable evaluation basis", err)
	}
	if frozenInput.ExpectedPointer !=
		expectedPublishedPointerRefV1(preBasisRef, preBasisDigest) ||
		body.ExpectedPointerRevision != preBasis.PointerRevision {
		return ModuleDisableControlOperationEvaluationV1{}, fmt.Errorf(
			"%w: Module Disable evaluation precondition is not current",
			ErrPublicationConflict,
		)
	}
	replay, err := moduledisabledryrun.EvaluateExactBasisV1(
		replayInput,
		preBasis,
		preControl,
		preCatalog,
	)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{},
			classifyModuleDisableControlOperationReplayV1(err)
	}
	if err := verifyModuleDisableControlOperationEligibilityV1(
		ctx,
		connection,
		preControl,
		preCatalog,
		plan,
		replay,
	); err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, err
	}
	projection, err := moduleDisableProjectionFromReplayV1(
		planDigest,
		plan.InstanceID,
		replay,
	)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, err
	}
	evaluation, evaluationCanonical, evaluationDigest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(
			moduledisablecontract.ModuleDisableEvaluationV1{
				SchemaVersion: moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   frozenInput.InputDigest,
				ExpectedRef:   frozenInput.ExpectedPointer,
				Projection:    projection,
			},
		)
	if err != nil {
		return ModuleDisableControlOperationEvaluationV1{},
			controlReceiptIntegrityV1("freeze Module Disable evaluation", err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleDisableControlOperationEvaluationV1{}, fmt.Errorf(
			"currentstore: commit Module Disable evaluation read: %w",
			err,
		)
	}
	committed = true
	return ModuleDisableControlOperationEvaluationV1{
		Input:               body,
		InputCanonical:      bytes.Clone(frozenInput.InputCanonical),
		InputDigest:         frozenInput.InputDigest,
		Plan:                plan,
		PlanCanonical:       bytes.Clone(planCanonical),
		PlanDigest:          planDigest,
		Evaluation:          evaluation,
		EvaluationCanonical: bytes.Clone(evaluationCanonical),
		EvaluationDigest:    evaluationDigest,
	}, nil
}

func prepareModuleDisableControlOperationReplayInputV1(
	tenantID string,
	expected controlapicontract.ExpectedResourceRefV1,
	inputCanonical []byte,
	inputDigest string,
) (
	moduledisablecontract.ModuleDisableDryRunBodyV1,
	moduleapplyplan.ProfileContextDisablePlanV1,
	[]byte,
	string,
	moduledisabledryrun.InputV1,
	error,
) {
	body, err := moduledisablecontract.RestoreModuleDisableDryRunBodyV1(
		inputCanonical,
		inputDigest,
	)
	if err != nil ||
		body.BindingTarget.Kind != moduledisablecontract.ModuleBindingTargetProfileV1 ||
		body.BindingTarget.ProfileID == "" ||
		body.BindingTarget.WorkspaceID != "" || body.BindingTarget.EndpointID != "" ||
		expected.Kind != controlapicontract.ResourcePublishedPointerV1 ||
		expected.ResourceID != tenantID ||
		expected.Revision != body.ExpectedPointerRevision {
		return moduledisablecontract.ModuleDisableDryRunBodyV1{},
			moduleapplyplan.ProfileContextDisablePlanV1{}, nil, "",
			moduledisabledryrun.InputV1{}, fmt.Errorf(
				"%w: restore narrow Module Disable input",
				ErrInvalidControlOperationReceiptV1,
			)
	}
	plan, planCanonical, planDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(
			moduleapplyplan.ProfileContextDisableInputV1{
				TenantID:                tenantID,
				ExpectedPointerRevision: body.ExpectedPointerRevision,
				ProfileID:               body.BindingTarget.ProfileID,
				InstanceID:              body.InstanceID,
			},
		)
	if err != nil || body.Port != plan.Port {
		return moduledisablecontract.ModuleDisableDryRunBodyV1{},
			moduleapplyplan.ProfileContextDisablePlanV1{}, nil, "",
			moduledisabledryrun.InputV1{}, fmt.Errorf(
				"%w: derive narrow Module Disable plan",
				ErrInvalidControlOperationReceiptV1,
			)
	}
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		return moduledisablecontract.ModuleDisableDryRunBodyV1{},
			moduleapplyplan.ProfileContextDisablePlanV1{}, nil, "",
			moduledisabledryrun.InputV1{}, fmt.Errorf(
				"%w: derive Module Disable candidate identity",
				ErrInvalidControlOperationReceiptV1,
			)
	}
	return body, plan, bytes.Clone(planCanonical), planDigest,
		moduledisabledryrun.InputV1{
			TenantID:                     plan.TenantID,
			ExpectedPointerRevision:      plan.ExpectedPointerRevision,
			TargetKind:                   moduledisabledryrun.TargetProfileV1,
			ProfileID:                    plan.BindingTarget.ProfileID,
			InstanceID:                   plan.InstanceID,
			Port:                         plan.Port,
			PlanDigest:                   planDigest,
			CandidateControlSnapshotID:   candidates.ControlSnapshotID,
			CandidateCatalogGenerationID: candidates.CatalogGenerationID,
		}, nil
}

func verifyModuleDisableControlOperationEligibilityV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	plan moduleapplyplan.ProfileContextDisablePlanV1,
	replay moduledisabledryrun.ResultV1,
) error {
	switch replay.Status {
	case moduledisabledryrun.StatusNoChangeV1:
		if replay.Publication != nil {
			return controlReceiptIntegrityV1(
				"NO_CHANGE eligibility replay carries a publication",
				nil,
			)
		}
		return nil
	case moduledisabledryrun.StatusWouldApplyV1:
		if replay.Publication == nil {
			return controlReceiptIntegrityV1(
				"APPLIED eligibility replay has no publication",
				nil,
			)
		}
		if err := verifyFirstSliceRemovedBindingV1(
			ctx,
			q,
			control,
			catalog,
			plan,
			replay.BindingRemoval,
		); err != nil {
			return fmt.Errorf(
				"%w: %v",
				ErrModuleDisableControlOperationIneligibleV1,
				err,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: Module Disable result %q cannot issue or commit",
			ErrPublicationConflict,
			replay.Status,
		)
	}
}
