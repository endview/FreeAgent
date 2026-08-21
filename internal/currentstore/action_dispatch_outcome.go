package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	actionUnknownWaitingReason                = "ACTION_UNKNOWN"
	defaultActionResultRejectedClassification = "RESULT_NONCANONICAL_OR_OVERSIZE"
)

// CommitActionDispatchOutcomeInput is the normalized private Host/Gateway
// response. InvocationID and Provider are wrapper facts, not values trusted
// from a module payload. A reliably identified SUCCEEDED effect whose result
// is unsafe is committed as SUCCEEDED+RESULT_REJECTED.
type CommitActionDispatchOutcomeInput struct {
	Lease                           RunLease
	AttemptID                       string
	InvocationID                    string
	Provider                        moduleapi.ActivatedModuleRef
	ExpectedAttemptRevision         uint64
	Outcome                         moduleapi.ActionExecutionOutcomeV1
	CanonicalResult                 []byte
	ProviderReceiptCanonical        []byte
	ExternalOperationID             string
	ErrorClassification             string
	UnknownReason                   string
	ResultRejectionClassification   string
	ReconciliationEvidenceCanonical []byte
}

type CommitActionDispatchOutcomeResult struct {
	Record  ActionDispatchRecord
	Lease   RunLease
	Applied bool
}

// ReconcileActionDispatchOutcomeInput deliberately mirrors the normalized
// outcome facts but can only update the original UNKNOWN row and always
// requires canonical reconciliation evidence.
type ReconcileActionDispatchOutcomeInput CommitActionDispatchOutcomeInput

type preparedActionOutcome struct {
	state                         ActionDispatchState
	outcome                       moduleapi.ActionExecutionOutcomeV1
	canonicalResult               []byte
	receiptContent                *preparedAdmissionContent
	externalOperationID           string
	errorClassification           string
	unknownReason                 string
	resultRejectionClassification string
	evidenceContent               *preparedAdmissionContent
	resultStatus                  corecontract.ActionResultStatusV1
	resultContent                 *preparedAdmissionContent
}

func (store *Store) CommitActionDispatchOutcome(
	ctx context.Context,
	input CommitActionDispatchOutcomeInput,
) (CommitActionDispatchOutcomeResult, error) {
	return store.commitActionDispatchOutcome(ctx, input, false)
}

func (store *Store) ReconcileActionDispatchOutcome(
	ctx context.Context,
	input ReconcileActionDispatchOutcomeInput,
) (CommitActionDispatchOutcomeResult, error) {
	return store.commitActionDispatchOutcome(
		ctx,
		CommitActionDispatchOutcomeInput(input),
		true,
	)
}

func (store *Store) commitActionDispatchOutcome(
	ctx context.Context,
	input CommitActionDispatchOutcomeInput,
	reconcile bool,
) (CommitActionDispatchOutcomeResult, error) {
	if ctx == nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidActionDispatch,
		)
	}
	input.CanonicalResult = bytes.Clone(input.CanonicalResult)
	input.ProviderReceiptCanonical = bytes.Clone(input.ProviderReceiptCanonical)
	input.ReconciliationEvidenceCanonical =
		bytes.Clone(input.ReconciliationEvidenceCanonical)
	if !validLeaseOpaqueID(input.AttemptID) ||
		input.InvocationID != input.AttemptID ||
		input.ExpectedAttemptRevision >= math.MaxInt64 {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid invocation identity or Attempt revision",
			ErrInvalidActionDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid invocation Provider: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	prepared, err := prepareActionOutcomeInput(input)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if reconcile && prepared.evidenceContent == nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: reconciliation requires canonical evidence",
			ErrInvalidActionDispatch,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: acquire Action outcome connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: begin Action outcome: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := queryActionDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if current.Attempt.RunID != input.Lease.RunID ||
		current.Attempt.Binding.Provider != input.Provider {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invocation differs from the frozen Action Attempt",
			ErrActionDispatchConflict,
		)
	}
	_, definition, err := loadActionAttemptFrozenDefinition(
		ctx,
		connection,
		current.Attempt,
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if err := finishPreparedActionResult(&prepared, definition); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}

	advanceUnknownEvidence := reconcile &&
		current.Attempt.State == ActionDispatchUnknown &&
		prepared.state == ActionDispatchUnknown &&
		prepared.evidenceContent != nil &&
		current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest
	if current.Attempt.State == prepared.state && !advanceUnknownEvidence {
		result, err := reopenCommittedActionOutcome(
			ctx,
			connection,
			input,
			prepared,
			current,
		)
		if err != nil {
			return CommitActionDispatchOutcomeResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceActionV1, input.AttemptID,
		); err != nil {
			return CommitActionDispatchOutcomeResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
				"currentstore: commit idempotent Action outcome: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}
	if current.Attempt.Revision != input.ExpectedAttemptRevision {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: expected Attempt revision %d, found %d",
			ErrActionDispatchConflict,
			input.ExpectedAttemptRevision,
			current.Attempt.Revision,
		)
	}
	if reconcile {
		if current.Attempt.State != ActionDispatchUnknown {
			return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
				"%w: reconciliation source is not UNKNOWN",
				ErrActionDispatchConflict,
			)
		}
		if prepared.state == ActionDispatchUnknown {
			// A new reliable evidence record may retain UNKNOWN without ever
			// returning the Attempt to PENDING.
		} else if !current.Attempt.State.allowsTransition(prepared.state) {
			return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
				"%w: UNKNOWN cannot transition to %s",
				ErrActionDispatchConflict,
				prepared.state,
			)
		}
	} else if current.Attempt.State != ActionDispatchPending ||
		!current.Attempt.State.allowsTransition(prepared.state) {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Action outcome source is not PENDING",
			ErrActionDispatchConflict,
		)
	}

	run, err := loadRunForActionWrite(ctx, connection, input.Lease)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if err := validateActionOutcomeSourceFrame(current, run, reconcile); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	merged, err := mergeActionOutcomeFacts(current, prepared)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	nextAttemptRevision, err := incrementSQLiteUint(
		current.Attempt.Revision,
		"Action Attempt revision",
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(run.RunRevision, "Run revision")
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(run.Frame.Revision, "Frame revision")
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	merged.Attempt.Revision = nextAttemptRevision
	updatedAt := nowUnixMicro()
	merged.Attempt.UpdatedAt = time.UnixMicro(updatedAt).UTC()
	nextStep, nextRunState, nextDisposition, waitingReason :=
		actionOutcomeProjection(prepared)
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		nextStep,
		corecontract.AttemptKindAction,
		current.Attempt.LogicalStepID,
		current.Attempt.AttemptID,
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	for _, content := range []*preparedAdmissionContent{
		prepared.resultContent,
		prepared.receiptContent,
		prepared.evidenceContent,
	} {
		if content == nil {
			continue
		}
		if err := putAdmissionContent(ctx, connection, *content, updatedAt); err != nil {
			return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
				"%w: persist Action outcome content: %v",
				ErrActionDispatchIntegrity,
				err,
			)
		}
	}
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE dispatch_attempts
		SET
			state=?, external_operation_id=?, provider_receipt_ref=?,
			result_ref=?, error_classification=?, reconciliation_evidence_ref=?,
			unknown_reason=?, revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND state=? AND revision=?
	`,
		string(merged.Attempt.State),
		nullableModelString(merged.Attempt.ExternalOperationID),
		nullableModelString(merged.Attempt.ProviderReceiptRef),
		nullableModelString(merged.Attempt.ResultRef),
		nullableModelString(merged.Attempt.ErrorClassification),
		nullableModelString(merged.Attempt.ReconciliationEvidenceRef),
		nullableModelString(merged.Attempt.UnknownReason),
		int64(nextAttemptRevision),
		updatedAt,
		current.Attempt.AttemptID,
		current.Attempt.RunID,
		string(current.Attempt.State),
		int64(current.Attempt.Revision),
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: update Action Attempt: %v",
			ErrActionDispatchIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "commit Action outcome Attempt"); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	// The event binds the exact detached record that the resource ledger will
	// publish. Re-read after the immutable contents and Attempt update so the
	// digest includes authoritative ContentRecord size/created-at metadata,
	// including the earlier timestamp of an already-present content digest.
	eventRecord, err := queryActionDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	resourceSemanticDigest, err := overviewResourceSemanticDigestV1(
		overviewResourceActionV1, eventRecord,
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	eventCanonical, eventDigest, err := prepareActionDispatchEvent(
		actionDispatchEventV1{
			SchemaVersion:          actionDispatchEventSchemaV1,
			RunID:                  current.Attempt.RunID,
			AttemptID:              current.Attempt.AttemptID,
			LogicalStepID:          current.Attempt.LogicalStepID,
			LogicalOperationKey:    current.Attempt.LogicalOperationKey,
			ProposalDigest:         current.Attempt.ProposalRef,
			State:                  prepared.state,
			ResultDigest:           eventRecord.Attempt.ResultRef,
			TransitionOrigin:       corecontract.DispatchTransitionOrdinaryOutcomeV1,
			ResourceSemanticDigest: resourceSemanticDigest,
		},
	)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest:         eventDigest,
		Kind:           ContentRunEventPayload,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, updatedAt); err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: persist Action outcome event: %v", ErrActionDispatchIntegrity, err,
		)
	}
	if err := updateRunAndFrameForActionOutcome(
		ctx,
		connection,
		input.Lease,
		run,
		current,
		nextRunRevision,
		nextFrameRevision,
		nextEvent,
		nextStep,
		nextRunState,
		nextDisposition,
		waitingReason,
		continuation,
		eventDigest,
		updatedAt,
		reconcile,
	); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if err := appendActionOutcomeRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if err := appendActionResourceObservationV1(
		ctx, connection, input.AttemptID, overviewTransitionActionOutcomeV1,
	); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	stored, err := queryActionDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: commit Action outcome: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return CommitActionDispatchOutcomeResult{
		Record:  cloneActionDispatchRecord(stored),
		Lease:   nextLease,
		Applied: true,
	}, nil
}

func prepareActionOutcomeInput(
	input CommitActionDispatchOutcomeInput,
) (preparedActionOutcome, error) {
	prepared := preparedActionOutcome{
		outcome:                       input.Outcome,
		canonicalResult:               bytes.Clone(input.CanonicalResult),
		externalOperationID:           input.ExternalOperationID,
		errorClassification:           input.ErrorClassification,
		unknownReason:                 input.UnknownReason,
		resultRejectionClassification: input.ResultRejectionClassification,
	}
	if err := input.Outcome.Validate(); err != nil {
		return preparedActionOutcome{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	for name, value := range map[string]string{
		"external operation ID": input.ExternalOperationID,
		"error classification":  input.ErrorClassification,
		"unknown reason":        input.UnknownReason,
		"result rejection":      input.ResultRejectionClassification,
	} {
		if value != "" && !validLeaseOpaqueID(value) {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: invalid %s",
				ErrInvalidActionDispatch,
				name,
			)
		}
	}
	if len(input.ProviderReceiptCanonical) != 0 {
		canonical, err := canonicalActionObject(
			input.ProviderReceiptCanonical,
			moduleapi.MaxActionReceiptBytesV1,
		)
		if err != nil || !bytes.Equal(canonical, input.ProviderReceiptCanonical) {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: Provider receipt is not bounded canonical object JSON",
				ErrInvalidActionDispatch,
			)
		}
		digest, err := ComputeContentDigest(
			ContentProviderReceipt,
			admissionJSONMediaType,
			canonical,
		)
		if err != nil {
			return preparedActionOutcome{}, err
		}
		prepared.receiptContent = &preparedAdmissionContent{
			Digest: digest, Kind: ContentProviderReceipt,
			MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
		}
	}
	if len(input.ReconciliationEvidenceCanonical) != 0 {
		canonical, err := moduleapi.CanonicalJSONWithLimits(
			input.ReconciliationEvidenceCanonical,
			moduleapi.CanonicalJSONLimits{
				MaxBytes: moduleapi.MaxTextBytes,
				MaxDepth: 128,
				MaxNodes: moduleapi.MaxTextBytes,
			},
		)
		if err != nil || !bytes.Equal(canonical, input.ReconciliationEvidenceCanonical) {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: reconciliation evidence is not canonical JSON",
				ErrInvalidActionDispatch,
			)
		}
		digest, err := ComputeContentDigest(
			ContentReconciliationEvidence,
			admissionJSONMediaType,
			canonical,
		)
		if err != nil {
			return preparedActionOutcome{}, err
		}
		prepared.evidenceContent = &preparedAdmissionContent{
			Digest: digest, Kind: ContentReconciliationEvidence,
			MediaType: admissionJSONMediaType, CanonicalBytes: bytes.Clone(canonical),
		}
	}
	switch input.Outcome {
	case moduleapi.ActionExecutionSucceeded:
		if input.ErrorClassification != "" || input.UnknownReason != "" {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: SUCCEEDED forbids error and unknown reason",
				ErrInvalidActionDispatch,
			)
		}
		prepared.state = ActionDispatchSucceeded
	case moduleapi.ActionExecutionFailed:
		if len(input.CanonicalResult) != 0 || input.ErrorClassification == "" ||
			input.UnknownReason != "" || input.ResultRejectionClassification != "" {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: FAILED requires only an error classification",
				ErrInvalidActionDispatch,
			)
		}
		prepared.state = ActionDispatchFailed
	case moduleapi.ActionExecutionUnknown:
		if len(input.CanonicalResult) != 0 || input.ErrorClassification != "" ||
			input.ResultRejectionClassification != "" ||
			(input.ExternalOperationID == "" && prepared.receiptContent == nil &&
				input.UnknownReason == "" && prepared.evidenceContent == nil) {
			return preparedActionOutcome{}, fmt.Errorf(
				"%w: UNKNOWN requires one durable clue and no final result/error",
				ErrInvalidActionDispatch,
			)
		}
		prepared.state = ActionDispatchUnknown
	}
	return prepared, nil
}

func finishPreparedActionResult(
	prepared *preparedActionOutcome,
	definition corecontract.FrozenActionDefinitionV1,
) error {
	if prepared.state != ActionDispatchSucceeded {
		return nil
	}
	_, canonical, digest, err := corecontract.NewAvailableActionResultV1(
		definition,
		prepared.canonicalResult,
	)
	if err == nil {
		prepared.resultStatus = corecontract.ActionResultAvailable
		prepared.resultContent = &preparedAdmissionContent{
			Digest: digest, Kind: ContentActionResult,
			MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
		}
		return nil
	}
	classification := prepared.resultRejectionClassification
	if classification == "" {
		classification = defaultActionResultRejectedClassification
	}
	_, canonical, digest, rejectedErr := corecontract.NewRejectedActionResultV1(
		definition,
		classification,
	)
	if rejectedErr != nil {
		return fmt.Errorf(
			"%w: build RESULT_REJECTED: %v",
			ErrInvalidActionDispatch,
			rejectedErr,
		)
	}
	prepared.resultStatus = corecontract.ActionResultRejected
	prepared.resultRejectionClassification = classification
	prepared.resultContent = &preparedAdmissionContent{
		Digest: digest, Kind: ContentActionResult,
		MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
	}
	return nil
}

func canonicalActionObject(input []byte, maximum int) ([]byte, error) {
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		input,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 32,
			MaxNodes: 64 << 10,
		},
	)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return nil, fmt.Errorf("not a bounded canonical object")
	}
	return bytes.Clone(canonical), nil
}

func mergeActionOutcomeFacts(
	current ActionDispatchRecord,
	prepared preparedActionOutcome,
) (ActionDispatchRecord, error) {
	merged := cloneActionDispatchRecord(current)
	merged.Attempt.State = prepared.state
	var err error
	merged.Attempt.ExternalOperationID, err = mergeActionStringFact(
		current.Attempt.ExternalOperationID,
		prepared.externalOperationID,
		"external operation ID",
	)
	if err != nil {
		return ActionDispatchRecord{}, err
	}
	if prepared.receiptContent != nil {
		if current.Attempt.ProviderReceiptRef != "" &&
			current.Attempt.ProviderReceiptRef != prepared.receiptContent.Digest {
			return ActionDispatchRecord{}, fmt.Errorf(
				"%w: Provider receipt cannot replace a known receipt",
				ErrActionDispatchConflict,
			)
		}
		merged.Attempt.ProviderReceiptRef = prepared.receiptContent.Digest
		content := ContentRecord{
			Digest:         prepared.receiptContent.Digest,
			Kind:           prepared.receiptContent.Kind,
			MediaType:      prepared.receiptContent.MediaType,
			CanonicalBytes: bytes.Clone(prepared.receiptContent.CanonicalBytes),
		}
		merged.ProviderReceipt = &content
	}
	if prepared.evidenceContent != nil {
		if current.Attempt.ReconciliationEvidenceRef != "" &&
			current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest {
			return ActionDispatchRecord{}, fmt.Errorf(
				"%w: reconciliation evidence cannot replace known evidence",
				ErrActionDispatchConflict,
			)
		}
		merged.Attempt.ReconciliationEvidenceRef = prepared.evidenceContent.Digest
	}
	switch prepared.state {
	case ActionDispatchSucceeded:
		merged.Attempt.ResultRef = prepared.resultContent.Digest
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason = ""
	case ActionDispatchFailed:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification = prepared.errorClassification
		merged.Attempt.UnknownReason = ""
	case ActionDispatchUnknown:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason = prepared.unknownReason
	}
	return merged, nil
}

func mergeActionStringFact(current, incoming, name string) (string, error) {
	if incoming == "" {
		return current, nil
	}
	if current != "" && current != incoming {
		return "", fmt.Errorf(
			"%w: %s would overwrite a known fact",
			ErrActionDispatchConflict,
			name,
		)
	}
	return incoming, nil
}

func actionOutcomeProjection(
	prepared preparedActionOutcome,
) (step, runState, disposition, waitingReason string) {
	switch prepared.state {
	case ActionDispatchSucceeded:
		if prepared.resultStatus == corecontract.ActionResultAvailable {
			return corecontract.ModelReadyAfterActionLoopStep,
				corecontract.InitialRunState, "", ""
		}
		return corecontract.TerminatedLoopStep,
			corecontract.TerminatedLoopStep,
			corecontract.TerminatedLoopStep,
			""
	case ActionDispatchFailed:
		return corecontract.TerminatedLoopStep,
			corecontract.TerminatedLoopStep,
			corecontract.TerminatedLoopStep,
			""
	default:
		return corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			actionUnknownWaitingReason
	}
}

func validateActionOutcomeSourceFrame(
	current ActionDispatchRecord,
	run RunForLoop,
	reconcile bool,
) error {
	if current.Attempt.RunID != run.RunID ||
		current.Attempt.MemberID != run.Member.MemberID ||
		current.Attempt.MemberSnapshotDigest != run.Member.MemberSnapshotDigest {
		return fmt.Errorf(
			"%w: Action Attempt differs from Run closure",
			ErrActionDispatchIntegrity,
		)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continuation.AttemptKind != corecontract.AttemptKindAction ||
		continuation.AttemptID != current.Attempt.AttemptID ||
		continuation.LogicalStepID != current.Attempt.LogicalStepID {
		return fmt.Errorf(
			"%w: Action Attempt differs from continuation",
			ErrActionDispatchIntegrity,
		)
	}
	if reconcile {
		if run.Frame.Step != corecontract.WaitingReconciliationLoopStep ||
			run.Frame.PendingAttemptID != "" ||
			run.Frame.PendingDispatchAttemptID != "" ||
			run.Frame.WaitingReason != actionUnknownWaitingReason {
			return fmt.Errorf(
				"%w: UNKNOWN Action Frame is not waiting for reconciliation",
				ErrActionDispatchIntegrity,
			)
		}
	} else if run.Frame.Step != corecontract.ActionPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != current.Attempt.AttemptID {
		return fmt.Errorf(
			"%w: PENDING Action is not the Frame pending identity",
			ErrActionDispatchIntegrity,
		)
	}
	return nil
}

func updateRunAndFrameForActionOutcome(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
	run RunForLoop,
	current ActionDispatchRecord,
	nextRunRevision uint64,
	nextFrameRevision uint64,
	nextEvent uint64,
	nextStep string,
	nextRunState string,
	nextDisposition string,
	waitingReason string,
	continuation []byte,
	eventDigest string,
	updatedAt int64,
	reconcile bool,
) error {
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=? AND revision=?
	`,
		nextRunState,
		nullableModelString(nextDisposition),
		int64(nextRunRevision),
		updatedAt,
		run.RunID,
		int64(run.RunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: update Action outcome Run: %w", err)
	}
	if err := requireModelCASRow(runUpdate, "commit Action outcome Run"); err != nil {
		return err
	}
	whereStep := corecontract.ActionPendingLoopStep
	wherePending := current.Attempt.AttemptID
	if reconcile {
		whereStep = corecontract.WaitingReconciliationLoopStep
		wherePending = ""
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=?, step=?, continuation=?, pending_attempt_id=NULL,
			pending_dispatch_attempt_id=NULL, waiting_reason=?,
			last_authoritative_event=?
		WHERE run_id=? AND frame_revision=? AND step=?
		  AND pending_attempt_id IS NULL
		  AND COALESCE(pending_dispatch_attempt_id, '')=?
		  AND last_authoritative_event=?
		  AND lease_owner=? AND lease_epoch=? AND lease_expiry>?
		  AND EXISTS(
			SELECT 1 FROM runs
			WHERE runs.run_id=loop_frames.run_id AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		nextStep,
		continuation,
		nullableModelString(waitingReason),
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		whereStep,
		wherePending,
		int64(run.Frame.LastAuthoritativeEvent),
		lease.OwnerID,
		int64(lease.LeaseEpoch),
		updatedAt,
		int64(nextRunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: update Action outcome Frame: %w", err)
	}
	if err := requireModelCASRow(frameUpdate, "commit Action outcome Frame"); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind, from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		actionDispatchTerminalEvent,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		updatedAt,
	); err != nil {
		return fmt.Errorf("%w: append Action outcome event: %v", ErrActionDispatchIntegrity, err)
	}
	return nil
}

func reopenCommittedActionOutcome(
	ctx context.Context,
	connection *sql.Conn,
	input CommitActionDispatchOutcomeInput,
	prepared preparedActionOutcome,
	current ActionDispatchRecord,
) (CommitActionDispatchOutcomeResult, error) {
	if input.ExpectedAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedAttemptRevision == math.MaxUint64 ||
			input.ExpectedAttemptRevision+1 != current.Attempt.Revision) {
		return CommitActionDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent Action revision differs",
			ErrActionDispatchConflict,
		)
	}
	if err := requireSameActionOutcome(current, prepared); err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	currentLease, err := loadCurrentModelOutcomeLease(ctx, connection, input.Lease)
	if err != nil {
		return CommitActionDispatchOutcomeResult{}, err
	}
	return CommitActionDispatchOutcomeResult{
		Record:  cloneActionDispatchRecord(current),
		Lease:   currentLease,
		Applied: false,
	}, nil
}

func requireSameActionOutcome(
	current ActionDispatchRecord,
	prepared preparedActionOutcome,
) error {
	if current.Attempt.State != prepared.state ||
		(prepared.externalOperationID != "" &&
			current.Attempt.ExternalOperationID != prepared.externalOperationID) ||
		current.Attempt.ErrorClassification != prepared.errorClassification ||
		current.Attempt.UnknownReason != prepared.unknownReason {
		return fmt.Errorf(
			"%w: persisted Action outcome differs",
			ErrActionDispatchConflict,
		)
	}
	if prepared.resultContent != nil &&
		current.Attempt.ResultRef != prepared.resultContent.Digest {
		return fmt.Errorf("%w: persisted Action result differs", ErrActionDispatchConflict)
	}
	if prepared.receiptContent != nil &&
		current.Attempt.ProviderReceiptRef != prepared.receiptContent.Digest {
		return fmt.Errorf("%w: persisted Action receipt differs", ErrActionDispatchConflict)
	}
	if prepared.evidenceContent != nil &&
		current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest {
		return fmt.Errorf("%w: persisted Action evidence differs", ErrActionDispatchConflict)
	}
	return nil
}
