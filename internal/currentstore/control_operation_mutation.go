package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// CommitModuleDisableControlOperationInputV1 contains only the immutable
// authorization projection and the three exact contracts needed for a first
// MODULE_DISABLE mutation. Input and evaluation digests are recovered from
// RequestCanonical; accepting them again would create parallel identities.
//
// An exact retry needs only RequestCanonical and RequestDigest. Every other
// field is deliberately ignored after the durable identity has been found.
type CommitModuleDisableControlOperationInputV1 struct {
	AuthorizationRevision uint64
	ScopeSetDigest        string

	RequestCanonical    []byte
	RequestDigest       string
	InputCanonical      []byte
	EvaluationCanonical []byte
}

type preparedModuleDisableControlOperationV1 struct {
	metadata    controlOperationReceiptMetadataV1
	payload     controlOperationReceiptPayloadV1
	publication *preparedControlCatalogPublication
}

// CommitModuleDisableControlOperationV1 is the sole first-slice mutation
// owner. It resolves the durable identity before inspecting any fresh facts.
// On a miss it replays the confirmed operation against the exact current
// predecessor, then commits either a NO_CHANGE receipt or one adjacent
// Control/Catalog publication plus its APPLIED domain and generic receipts in
// the same BEGIN IMMEDIATE transaction.
//
// A pointer that already names the candidate without an existing exact
// receipt is never backfilled. Thus a lost response is resolved only by the
// receipt written by this transaction, not by inference from current state.
func (store *Store) CommitModuleDisableControlOperationV1(
	ctx context.Context,
	input CommitModuleDisableControlOperationInputV1,
) (
	record StoredControlOperationReceiptV1,
	created bool,
	returnErr error,
) {
	defer func() {
		if store != nil {
			returnErr = classifySQLiteOwnerContention(store.path, returnErr)
		}
	}()
	if ctx == nil {
		return StoredControlOperationReceiptV1{}, false,
			ErrInvalidControlOperationReceiptV1
	}
	frozenInput := cloneCommitModuleDisableControlOperationInputV1(input)
	request, identity, err := preflightControlOperationReceiptRequestV1(
		frozenInput.RequestCanonical,
		frozenInput.RequestDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: acquire Module Disable operation connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: begin Module Disable operation: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existing, found, err := queryControlOperationReceiptMetadataByIdentityV1(
		ctx,
		connection,
		identity,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: inspect Module Disable operation identity: %w",
			err,
		)
	}
	if found {
		if existing.requestDigest != frozenInput.RequestDigest {
			return StoredControlOperationReceiptV1{}, false,
				&ControlOperationReceiptConflictErrorV1{Identity: identity}
		}
		record, err = restoreStoredControlOperationReceiptV1(
			ctx,
			connection,
			existing,
		)
		if err != nil {
			return StoredControlOperationReceiptV1{}, false, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
				"currentstore: commit Module Disable exact retry: %w",
				err,
			)
		}
		committed = true
		return record, false, nil
	}

	prepared, err := prepareModuleDisableControlOperationV1(
		ctx,
		connection,
		frozenInput,
		request,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	if prepared.publication != nil {
		alreadyPublished, err := publishControlCatalogInTransaction(
			ctx,
			connection,
			*prepared.publication,
		)
		if err != nil {
			return StoredControlOperationReceiptV1{}, false, err
		}
		if alreadyPublished {
			return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
				"%w: Module Disable candidate exists without its exact receipt",
				ErrPublicationConflict,
			)
		}
	}
	if err := insertPreparedControlOperationReceiptV1(
		ctx,
		connection,
		prepared.metadata,
		prepared.payload,
	); err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	record, err = restoreStoredControlOperationReceiptV1(
		ctx,
		connection,
		prepared.metadata,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: commit Module Disable operation: %w",
			err,
		)
	}
	committed = true
	return record, true, nil
}

func cloneCommitModuleDisableControlOperationInputV1(
	input CommitModuleDisableControlOperationInputV1,
) CommitModuleDisableControlOperationInputV1 {
	input.RequestCanonical = bytes.Clone(input.RequestCanonical)
	input.InputCanonical = bytes.Clone(input.InputCanonical)
	input.EvaluationCanonical = bytes.Clone(input.EvaluationCanonical)
	return input
}

func prepareModuleDisableControlOperationV1(
	ctx context.Context,
	connection *sql.Conn,
	input CommitModuleDisableControlOperationInputV1,
	request controlapicontract.ControlOperationRequestV1,
) (preparedModuleDisableControlOperationV1, error) {
	if input.AuthorizationRevision == 0 ||
		input.AuthorizationRevision > uint64(1<<53-1) ||
		!moduleapi.ValidSHA256(input.ScopeSetDigest) {
		return preparedModuleDisableControlOperationV1{}, fmt.Errorf(
			"%w: invalid authorization projection",
			ErrInvalidControlOperationReceiptV1,
		)
	}
	body, plan, planCanonical, planDigest, replayInput, err :=
		prepareModuleDisableControlOperationReplayInputV1(
			request.Scope.TenantID,
			request.ExpectedRef,
			input.InputCanonical,
			request.InputDigest,
		)
	if err != nil {
		return preparedModuleDisableControlOperationV1{}, err
	}
	evaluation, err := moduledisablecontract.RestoreModuleDisableEvaluationV1(
		input.EvaluationCanonical,
		request.OperationEvaluationDigest,
	)
	if err != nil {
		return preparedModuleDisableControlOperationV1{}, fmt.Errorf(
			"%w: restore Module Disable evaluation",
			ErrInvalidControlOperationReceiptV1,
		)
	}
	preBasis, preControl, preCatalog, err :=
		loadCurrentModuleDisableBasisInTransactionV1(
			ctx,
			connection,
			request.Scope.TenantID,
		)
	if err != nil {
		return preparedModuleDisableControlOperationV1{}, err
	}
	preBasisRef := apiBasisFromControlBasisV1(preBasis)
	_, preBasisCanonical, preBasisDigest, err :=
		controlapicontract.NewPublishedBasisRefV1(preBasisRef)
	if err != nil {
		return preparedModuleDisableControlOperationV1{}, controlReceiptIntegrityV1(
			"freeze current Module Disable basis",
			err,
		)
	}
	preExpected := expectedPublishedPointerRefV1(preBasisRef, preBasisDigest)
	if request.Scope.Kind != controlapicontract.ScopeTenantV1 ||
		request.ExpectedRef != preExpected ||
		body.ExpectedPointerRevision != preBasis.PointerRevision ||
		plan.TenantID != preBasis.TenantID ||
		plan.ExpectedPointerRevision != preBasis.PointerRevision {
		return preparedModuleDisableControlOperationV1{}, fmt.Errorf(
			"%w: Module Disable precondition is not the exact current pointer",
			ErrPublicationConflict,
		)
	}

	result, err := moduledisabledryrun.EvaluateExactBasisV1(
		replayInput,
		preBasis,
		preControl,
		preCatalog,
	)
	if err != nil {
		return preparedModuleDisableControlOperationV1{},
			classifyModuleDisableControlOperationReplayV1(err)
	}
	if err := verifyModuleDisableControlOperationEligibilityV1(
		ctx,
		connection,
		preControl,
		preCatalog,
		plan,
		result,
	); err != nil {
		return preparedModuleDisableControlOperationV1{}, err
	}
	expectedProjection, err := moduleDisableProjectionFromReplayV1(
		planDigest,
		plan.InstanceID,
		result,
	)
	if err != nil {
		return preparedModuleDisableControlOperationV1{}, err
	}
	_, expectedEvaluationCanonical, expectedEvaluationDigest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(
			moduledisablecontract.ModuleDisableEvaluationV1{
				SchemaVersion: moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   request.InputDigest,
				ExpectedRef:   preExpected,
				Projection:    expectedProjection,
			},
		)
	if err != nil || expectedEvaluationDigest != request.OperationEvaluationDigest ||
		!bytes.Equal(expectedEvaluationCanonical, input.EvaluationCanonical) ||
		evaluation.InputDigest != request.InputDigest ||
		evaluation.ExpectedRef != preExpected ||
		evaluation.Projection.PlanDigest != planDigest {
		return preparedModuleDisableControlOperationV1{}, fmt.Errorf(
			"%w: confirmed Module Disable evaluation differs from exact replay",
			ErrInvalidControlOperationReceiptV1,
		)
	}

	postBasisRef := preBasisRef
	postBasisCanonical := bytes.Clone(preBasisCanonical)
	postBasisDigest := preBasisDigest
	status := controlapicontract.OperationStatusNoChangeV1
	var domainCanonical []byte
	var domainRef *controlapicontract.DomainReceiptRefV1
	var publication *preparedControlCatalogPublication

	switch result.Status {
	case moduledisabledryrun.StatusNoChangeV1:
		if result.Publication != nil {
			return preparedModuleDisableControlOperationV1{},
				controlReceiptIntegrityV1("NO_CHANGE replay carries a publication", nil)
		}
	case moduledisabledryrun.StatusWouldApplyV1:
		if result.Publication == nil {
			return preparedModuleDisableControlOperationV1{},
				controlReceiptIntegrityV1("APPLIED replay has no publication", nil)
		}
		status = controlapicontract.OperationStatusAppliedV1
		postBasisRef = apiBasisFromControlBasisV1(result.CandidateBasis)
		_, postBasisCanonical, postBasisDigest, err =
			controlapicontract.NewPublishedBasisRefV1(postBasisRef)
		if err != nil {
			return preparedModuleDisableControlOperationV1{},
				controlReceiptIntegrityV1("freeze candidate Module Disable basis", err)
		}
		domain := domainReceiptFromReplayV1(
			planCanonical,
			planDigest,
			preBasisRef,
			postBasisRef,
			plan.InstanceID,
			result,
		)
		_, domainBytes, frozenDomainRef, err :=
			moduledisablecontract.NewModuleDisablePublicationReceiptV1(domain)
		if err != nil {
			return preparedModuleDisableControlOperationV1{},
				controlReceiptIntegrityV1("freeze Module Disable domain receipt", err)
		}
		domainCanonical = domainBytes
		domainRef = &frozenDomainRef
		preparedPublication, err := prepareControlCatalogPublication(
			PublishControlCatalogInput{
				ExpectedPointerRevision: result.Publication.ExpectedPointerRevision,
				NewPointerRevision:      result.Publication.NewPointerRevision,
				ControlRef:              result.Publication.ControlRef,
				ControlCanonical:        result.Publication.ControlCanonical,
				CatalogRef:              result.Publication.CatalogRef,
				CatalogCanonical:        result.Publication.CatalogCanonical,
			},
		)
		if err != nil || preparedPublication.basis != result.CandidateBasis {
			return preparedModuleDisableControlOperationV1{},
				controlReceiptIntegrityV1("prepare Module Disable publication", err)
		}
		publication = &preparedPublication
	default:
		return preparedModuleDisableControlOperationV1{}, fmt.Errorf(
			"%w: Module Disable miss cannot be backfilled from %q",
			ErrPublicationConflict,
			result.Status,
		)
	}

	completedAt := nowUnixMicro()
	if completedAt <= 0 {
		return preparedModuleDisableControlOperationV1{},
			controlReceiptIntegrityV1("invalid operation completion time", nil)
	}
	preReceiptRef := preExpected
	postReceiptRef := expectedPublishedPointerRefV1(
		postBasisRef,
		postBasisDigest,
	)
	_, controlReceiptCanonical, controlReceiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(
			controlapicontract.ControlOperationReceiptV1{
				SchemaVersion:         controlapicontract.ControlOperationReceiptSchemaVersionV1,
				RequestDigest:         input.RequestDigest,
				Intent:                request.Intent,
				IdempotencyKeyDigest:  request.IdempotencyKeyDigest,
				PrincipalID:           request.PrincipalID,
				ScopeDigest:           request.ScopeDigest,
				Operation:             request.Operation,
				Status:                status,
				ErrorCode:             controlapicontract.ErrorNoneV1,
				PreRef:                &preReceiptRef,
				PostRef:               &postReceiptRef,
				DomainReceipt:         domainRef,
				ReplayDisposition:     controlapicontract.ReplayReturnExactReceiptV1,
				CompletedAtUnixMicros: uint64(completedAt),
			},
		)
	if err != nil {
		return preparedModuleDisableControlOperationV1{},
			controlReceiptIntegrityV1("freeze generic Module Disable receipt", err)
	}

	metadata := controlOperationReceiptMetadataV1{
		receiptDigest:        controlReceiptDigest,
		tenantID:             request.Scope.TenantID,
		principalID:          request.PrincipalID,
		scopeDigest:          request.ScopeDigest,
		operation:            request.Operation,
		idempotencyKeyDigest: request.IdempotencyKeyDigest,
		policyRevision:       int64(input.AuthorizationRevision),
		scopeSetDigest:       input.ScopeSetDigest,
		status:               status,
		requestDigest:        input.RequestDigest,
		requestSize:          int64(len(input.RequestCanonical)),
		inputDigest:          request.InputDigest,
		inputSize:            int64(len(input.InputCanonical)),
		evaluationDigest:     request.OperationEvaluationDigest,
		evaluationSize:       int64(len(input.EvaluationCanonical)),
		controlReceiptSize:   int64(len(controlReceiptCanonical)),
		preBasisDigest:       preBasisDigest,
		preBasisSize:         int64(len(preBasisCanonical)),
		postBasisDigest:      postBasisDigest,
		postBasisSize:        int64(len(postBasisCanonical)),
		preControlID:         preBasisRef.Control.ID,
		preCatalogID:         preBasisRef.Catalog.ID,
		postControlID:        postBasisRef.Control.ID,
		postCatalogID:        postBasisRef.Catalog.ID,
	}
	payload := controlOperationReceiptPayloadV1{
		request:        bytes.Clone(input.RequestCanonical),
		input:          bytes.Clone(input.InputCanonical),
		evaluation:     bytes.Clone(input.EvaluationCanonical),
		controlReceipt: bytes.Clone(controlReceiptCanonical),
		preBasis:       bytes.Clone(preBasisCanonical),
		postBasis:      bytes.Clone(postBasisCanonical),
		domainReceipt:  bytes.Clone(domainCanonical),
	}
	if domainRef != nil {
		metadata.domainKind = sql.NullString{String: string(domainRef.Kind), Valid: true}
		metadata.domainID = sql.NullString{String: domainRef.ID, Valid: true}
		metadata.domainDigest = sql.NullString{String: domainRef.Digest, Valid: true}
		metadata.domainSize = sql.NullInt64{Int64: int64(len(domainCanonical)), Valid: true}
	}
	metadata.totalSize = metadata.requestSize + metadata.inputSize +
		metadata.evaluationSize + metadata.controlReceiptSize +
		metadata.preBasisSize + metadata.postBasisSize
	if metadata.domainSize.Valid {
		metadata.totalSize += metadata.domainSize.Int64
	}
	if err := validateControlOperationReceiptMetadataV1(metadata); err != nil {
		return preparedModuleDisableControlOperationV1{}, err
	}
	return preparedModuleDisableControlOperationV1{
		metadata:    metadata,
		payload:     payload,
		publication: publication,
	}, nil
}

func loadCurrentModuleDisableBasisInTransactionV1(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	current, found, err := queryCurrentControlPointer(ctx, connection, tenantID)
	if err != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	if !found || current.TenantID != tenantID {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, fmt.Errorf(
				"%w: Module Disable tenant has no exact current pointer",
				ErrPublicationConflict,
			)
	}
	control, catalog, err := loadAdmissionControlCatalog(
		ctx,
		connection,
		tenantID,
		current.SnapshotID,
		current.CatalogGenerationID,
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, controlReceiptIntegrityV1(
				"load current Module Disable parents",
				err,
			)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		ctx,
		connection,
		control,
		catalog,
	); err != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, controlReceiptIntegrityV1(
				"verify current Module Disable parents",
				err,
			)
	}
	basis := controlcontract.PublishedBasis{
		TenantID:        tenantID,
		PointerRevision: current.PointerRevision,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: control.SnapshotID,
			Revision:   control.Revision,
			Digest:     control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: catalog.GenerationID,
			Generation:   catalog.Generation,
			Digest:       catalog.Digest,
		},
	}
	if err := basis.Validate(); err != nil ||
		current.SnapshotID != basis.Control.SnapshotID ||
		current.CatalogGenerationID != basis.Catalog.GenerationID ||
		catalog.ControlSnapshotID != basis.Control.SnapshotID ||
		catalog.ControlSnapshotDigest != basis.Control.Digest {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, controlReceiptIntegrityV1(
				"current Module Disable basis differs",
				err,
			)
	}
	return basis, control, catalog, nil
}

func classifyModuleDisableControlOperationReplayV1(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var failure *moduledisabledryrun.FailureV1
	if !errors.As(err, &failure) {
		return controlReceiptIntegrityV1("Module Disable replay failed", err)
	}
	switch failure.Code() {
	case moduledisabledryrun.FailureInvalidInputV1:
		return fmt.Errorf(
			"%w: Module Disable replay input is invalid",
			ErrInvalidControlOperationReceiptV1,
		)
	case moduledisabledryrun.FailurePointerConflictV1,
		moduledisabledryrun.FailureTargetConflictV1:
		return fmt.Errorf(
			"%w: Module Disable replay no longer matches current state",
			ErrPublicationConflict,
		)
	default:
		return controlReceiptIntegrityV1("Module Disable replay failed", err)
	}
}
