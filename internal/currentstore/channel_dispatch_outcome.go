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

// CommitChannelDispatchOutcomeInput is the normalized private Gateway
// response. InvocationID and Provider are trusted wrapper facts, not fields
// accepted from an arbitrary module payload.
type CommitChannelDispatchOutcomeInput struct {
	Lease                           RunLease
	AttemptID                       string
	InvocationID                    string
	Provider                        moduleapi.ActivatedModuleRef
	ExpectedAttemptRevision         uint64
	Outcome                         moduleapi.ChannelExecutionOutcomeV1
	ProviderReceiptCanonical        []byte
	ExternalOperationID             string
	ErrorClassification             string
	UnknownReason                   string
	ReconciliationEvidenceCanonical []byte
}

type CommitChannelDispatchOutcomeResult struct {
	Record  ChannelDispatchRecord
	Lease   RunLease
	Applied bool
}

// ReconcileChannelDispatchOutcomeInput can only CAS the original UNKNOWN
// Attempt. Reconciliation never creates a replacement Attempt and never
// moves an Attempt back to PENDING.
type ReconcileChannelDispatchOutcomeInput CommitChannelDispatchOutcomeInput

type preparedChannelOutcome struct {
	state               DispatchState
	outcome             moduleapi.ChannelExecutionOutcomeV1
	receiptContent      *preparedAdmissionContent
	externalOperationID string
	errorClassification string
	unknownReason       string
	evidenceContent     *preparedAdmissionContent
	resultContent       *preparedAdmissionContent
}

func (store *Store) CommitChannelDispatchOutcome(
	ctx context.Context,
	input CommitChannelDispatchOutcomeInput,
) (CommitChannelDispatchOutcomeResult, error) {
	return store.commitChannelDispatchOutcome(ctx, input, false)
}

func (store *Store) ReconcileChannelDispatchOutcome(
	ctx context.Context,
	input ReconcileChannelDispatchOutcomeInput,
) (CommitChannelDispatchOutcomeResult, error) {
	return store.commitChannelDispatchOutcome(
		ctx,
		CommitChannelDispatchOutcomeInput(input),
		true,
	)
}

func (store *Store) commitChannelDispatchOutcome(
	ctx context.Context,
	input CommitChannelDispatchOutcomeInput,
	reconcile bool,
) (CommitChannelDispatchOutcomeResult, error) {
	if ctx == nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: context is nil", ErrInvalidChannelDispatch,
		)
	}
	input.ProviderReceiptCanonical = bytes.Clone(input.ProviderReceiptCanonical)
	input.ReconciliationEvidenceCanonical = bytes.Clone(
		input.ReconciliationEvidenceCanonical,
	)
	if !validLeaseOpaqueID(input.AttemptID) ||
		input.InvocationID != input.AttemptID ||
		input.ExpectedAttemptRevision >= math.MaxInt64 {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid invocation identity or Attempt revision",
			ErrInvalidChannelDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid invocation Provider: %v", ErrInvalidChannelDispatch, err,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: %v", ErrInvalidChannelDispatch, err,
		)
	}
	prepared, err := prepareChannelOutcomeInput(input)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if !reconcile && prepared.evidenceContent != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: reconciliation evidence is accepted only by the original UNKNOWN reconciliation API",
			ErrInvalidChannelDispatch,
		)
	}
	if reconcile && prepared.evidenceContent == nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: reconciliation requires bounded canonical evidence",
			ErrInvalidChannelDispatch,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: acquire Channel outcome connection: %w", err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: begin Channel outcome: %w", err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := queryChannelDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if current.Attempt.RunID != input.Lease.RunID ||
		current.Attempt.Binding.Provider != input.Provider {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invocation differs from the frozen Channel Attempt",
			ErrChannelDispatchConflict,
		)
	}
	if err := finishPreparedChannelResult(&prepared, current); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}

	advanceUnknownEvidence := reconcile &&
		current.Attempt.State == DispatchUnknown &&
		prepared.state == DispatchUnknown &&
		prepared.evidenceContent != nil &&
		current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest
	if current.Attempt.State == prepared.state && !advanceUnknownEvidence {
		result, err := reopenCommittedChannelOutcome(
			ctx, connection, input, prepared, current,
		)
		if err != nil {
			return CommitChannelDispatchOutcomeResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceChannelV1, input.AttemptID,
		); err != nil {
			return CommitChannelDispatchOutcomeResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
				"currentstore: commit idempotent Channel outcome: %w", err,
			)
		}
		committed = true
		return result, nil
	}
	if current.Attempt.Revision != input.ExpectedAttemptRevision {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: expected Attempt revision %d, found %d",
			ErrChannelDispatchConflict,
			input.ExpectedAttemptRevision,
			current.Attempt.Revision,
		)
	}
	if reconcile {
		if current.Attempt.State != DispatchUnknown {
			return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: reconciliation source is not UNKNOWN",
				ErrChannelDispatchConflict,
			)
		}
		if prepared.state != DispatchUnknown &&
			!current.Attempt.State.allowsTransition(prepared.state) {
			return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: UNKNOWN cannot transition to %s",
				ErrChannelDispatchConflict,
				prepared.state,
			)
		}
	} else if current.Attempt.State != DispatchPending ||
		!current.Attempt.State.allowsTransition(prepared.state) {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Channel outcome source is not PENDING",
			ErrChannelDispatchConflict,
		)
	}

	run, err := loadRunForChannelWrite(ctx, connection, input.Lease)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if err := validateChannelOutcomeSourceFrame(current, run, reconcile); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	merged, err := mergeChannelOutcomeFacts(current, prepared, reconcile)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	nextAttemptRevision, err := incrementSQLiteUint(
		current.Attempt.Revision, "Channel Attempt revision",
	)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(run.RunRevision, "Run revision")
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(run.Frame.Revision, "Frame revision")
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent, "RunEvent sequence",
	)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	merged.Attempt.Revision = nextAttemptRevision
	updatedAt := nowUnixMicro()
	merged.Attempt.UpdatedAt = time.UnixMicro(updatedAt).UTC()
	nextStep, nextRunState, nextDisposition, waitingReason := channelOutcomeProjection(prepared.state)
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		nextStep,
		corecontract.AttemptKindChannel,
		current.Attempt.LogicalStepID,
		current.Attempt.AttemptID,
	)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
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
			return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: persist Channel outcome content: %v",
				ErrChannelDispatchIntegrity,
				err,
			)
		}
	}
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE dispatch_attempts
		SET state=?, external_operation_id=?, provider_receipt_ref=?,
			result_ref=?, error_classification=?, reconciliation_evidence_ref=?,
			unknown_reason=?, revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND dispatch_kind='CHANNEL_SEND'
		  AND state=? AND revision=?
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
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: update Channel Attempt: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "commit Channel outcome Attempt"); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	// Bind the event to the exact detached record that the resource ledger
	// will publish. The authoritative read includes immutable content size and
	// created-at metadata that cannot be reconstructed from a caller payload
	// when a content digest already existed.
	eventRecord, err := queryChannelDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	resourceSemanticDigest, err := overviewResourceSemanticDigestV1(
		overviewResourceChannelV1, eventRecord,
	)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	eventCanonical, eventDigest, err := prepareChannelDispatchEvent(
		channelDispatchEventV1{
			SchemaVersion:          channelDispatchEventSchemaV1,
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
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload,
		MediaType: admissionJSONMediaType, CanonicalBytes: eventCanonical,
	}, updatedAt); err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: persist Channel outcome event: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	if err := updateRunAndFrameForChannelOutcome(
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
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if err := appendChannelOutcomeRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if err := appendChannelResourceObservationV1(
		ctx, connection, input.AttemptID, overviewTransitionChannelOutcomeV1,
	); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	stored, err := queryChannelDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: commit Channel outcome: %w", err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return CommitChannelDispatchOutcomeResult{
		Record:  cloneChannelDispatchRecord(stored),
		Lease:   nextLease,
		Applied: true,
	}, nil
}

func prepareChannelOutcomeInput(
	input CommitChannelDispatchOutcomeInput,
) (preparedChannelOutcome, error) {
	prepared := preparedChannelOutcome{
		outcome:             input.Outcome,
		externalOperationID: input.ExternalOperationID,
		errorClassification: input.ErrorClassification,
		unknownReason:       input.UnknownReason,
	}
	if err := input.Outcome.Validate(); err != nil {
		return preparedChannelOutcome{}, fmt.Errorf(
			"%w: %v", ErrInvalidChannelDispatch, err,
		)
	}
	for name, value := range map[string]string{
		"external operation ID": input.ExternalOperationID,
		"error classification":  input.ErrorClassification,
		"unknown reason":        input.UnknownReason,
	} {
		if value != "" && !validLeaseOpaqueID(value) {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: invalid %s", ErrInvalidChannelDispatch, name,
			)
		}
	}
	if len(input.ProviderReceiptCanonical) != 0 {
		canonical, err := canonicalActionObject(
			input.ProviderReceiptCanonical,
			moduleapi.MaxChannelProviderReceiptBytesV1,
		)
		if err != nil || !bytes.Equal(canonical, input.ProviderReceiptCanonical) {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: Provider receipt is not a bounded canonical object",
				ErrInvalidChannelDispatch,
			)
		}
		digest, err := ComputeContentDigest(
			ContentProviderReceipt, admissionJSONMediaType, canonical,
		)
		if err != nil {
			return preparedChannelOutcome{}, err
		}
		prepared.receiptContent = &preparedAdmissionContent{
			Digest: digest, Kind: ContentProviderReceipt,
			MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
		}
	}
	if len(input.ReconciliationEvidenceCanonical) != 0 {
		canonical, err := canonicalActionObject(
			input.ReconciliationEvidenceCanonical,
			moduleapi.MaxChannelProviderReceiptBytesV1,
		)
		if err != nil || !bytes.Equal(canonical, input.ReconciliationEvidenceCanonical) {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: reconciliation evidence is not a bounded canonical object",
				ErrInvalidChannelDispatch,
			)
		}
		digest, err := ComputeContentDigest(
			ContentReconciliationEvidence, admissionJSONMediaType, canonical,
		)
		if err != nil {
			return preparedChannelOutcome{}, err
		}
		prepared.evidenceContent = &preparedAdmissionContent{
			Digest: digest, Kind: ContentReconciliationEvidence,
			MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
		}
	}
	switch input.Outcome {
	case moduleapi.ChannelExecutionSucceeded:
		if input.ErrorClassification != "" || input.UnknownReason != "" {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: SUCCEEDED forbids error and unknown reason",
				ErrInvalidChannelDispatch,
			)
		}
		prepared.state = DispatchSucceeded
	case moduleapi.ChannelExecutionFailed:
		if input.ErrorClassification == "" || input.UnknownReason != "" ||
			input.ExternalOperationID != "" {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: FAILED requires an error and cannot claim an external operation",
				ErrInvalidChannelDispatch,
			)
		}
		prepared.state = DispatchFailed
	case moduleapi.ChannelExecutionUnknown:
		if input.ErrorClassification != "" ||
			(input.ExternalOperationID == "" && prepared.receiptContent == nil &&
				input.UnknownReason == "" && prepared.evidenceContent == nil) {
			return preparedChannelOutcome{}, fmt.Errorf(
				"%w: UNKNOWN requires one durable clue and no final error",
				ErrInvalidChannelDispatch,
			)
		}
		prepared.state = DispatchUnknown
	}
	return prepared, nil
}

func finishPreparedChannelResult(
	prepared *preparedChannelOutcome,
	current ChannelDispatchRecord,
) error {
	if prepared.state != DispatchSucceeded {
		return nil
	}
	externalOperationID := prepared.externalOperationID
	if externalOperationID == "" {
		externalOperationID = current.Attempt.ExternalOperationID
	}
	var receipt []byte
	if prepared.receiptContent != nil {
		receipt = prepared.receiptContent.CanonicalBytes
	} else if current.ProviderReceipt != nil {
		receipt = current.ProviderReceipt.CanonicalBytes
	}
	_, canonical, err := moduleapi.NewChannelExecutionResultV1(
		moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           current.Attempt.AttemptID,
			Outcome:             moduleapi.ChannelExecutionSucceeded,
			ProviderReceipt:     bytes.Clone(receipt),
			ExternalOperationID: externalOperationID,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"%w: invalid SUCCEEDED Channel result: %v",
			ErrInvalidChannelDispatch,
			err,
		)
	}
	digest, err := ComputeContentDigest(
		ContentChannelSendResult, admissionJSONMediaType, canonical,
	)
	if err != nil {
		return err
	}
	prepared.resultContent = &preparedAdmissionContent{
		Digest: digest, Kind: ContentChannelSendResult,
		MediaType: admissionJSONMediaType, CanonicalBytes: canonical,
	}
	return nil
}

func mergeChannelOutcomeFacts(
	current ChannelDispatchRecord,
	prepared preparedChannelOutcome,
	reconcile bool,
) (ChannelDispatchRecord, error) {
	merged := cloneChannelDispatchRecord(current)
	merged.Attempt.State = prepared.state
	var err error
	merged.Attempt.ExternalOperationID, err = mergeChannelStringFact(
		current.Attempt.ExternalOperationID,
		prepared.externalOperationID,
		"external operation ID",
	)
	if err != nil {
		return ChannelDispatchRecord{}, err
	}
	if prepared.receiptContent != nil {
		if current.Attempt.ProviderReceiptRef != "" &&
			current.Attempt.ProviderReceiptRef != prepared.receiptContent.Digest {
			return ChannelDispatchRecord{}, fmt.Errorf(
				"%w: Provider receipt cannot replace a known receipt",
				ErrChannelDispatchConflict,
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
			current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest &&
			!reconcile {
			return ChannelDispatchRecord{}, fmt.Errorf(
				"%w: reconciliation evidence can advance only during reconciliation",
				ErrChannelDispatchConflict,
			)
		}
		merged.Attempt.ReconciliationEvidenceRef = prepared.evidenceContent.Digest
		content := ContentRecord{
			Digest:         prepared.evidenceContent.Digest,
			Kind:           prepared.evidenceContent.Kind,
			MediaType:      prepared.evidenceContent.MediaType,
			CanonicalBytes: bytes.Clone(prepared.evidenceContent.CanonicalBytes),
		}
		merged.ReconciliationEvidence = &content
	}
	switch prepared.state {
	case DispatchSucceeded:
		merged.Attempt.ResultRef = prepared.resultContent.Digest
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason = ""
	case DispatchFailed:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification = prepared.errorClassification
		merged.Attempt.UnknownReason = ""
	case DispatchUnknown:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason, err = mergeChannelStringFact(
			current.Attempt.UnknownReason, prepared.unknownReason, "unknown reason",
		)
		if err != nil {
			return ChannelDispatchRecord{}, err
		}
	}
	return merged, nil
}

func mergeChannelStringFact(current, incoming, name string) (string, error) {
	if incoming == "" {
		return current, nil
	}
	if current != "" && current != incoming {
		return "", fmt.Errorf(
			"%w: %s would overwrite a known fact",
			ErrChannelDispatchConflict,
			name,
		)
	}
	return incoming, nil
}

func channelOutcomeProjection(
	state DispatchState,
) (step, runState, disposition, waitingReason string) {
	if state == DispatchUnknown {
		return corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			channelUnknownWaitingReason
	}
	return corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		""
}

func validateChannelOutcomeSourceFrame(
	current ChannelDispatchRecord,
	run RunForLoop,
	reconcile bool,
) error {
	if current.Attempt.RunID != run.RunID ||
		current.Attempt.MemberID != run.Member.MemberID ||
		current.Attempt.MemberSnapshotDigest != run.Member.MemberSnapshotDigest {
		return fmt.Errorf(
			"%w: Channel Attempt differs from Run closure",
			ErrChannelDispatchIntegrity,
		)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(run.Frame.Continuation)
	if err != nil || continuation.AttemptKind != corecontract.AttemptKindChannel ||
		continuation.AttemptID != current.Attempt.AttemptID ||
		continuation.LogicalStepID != current.Attempt.LogicalStepID {
		return fmt.Errorf(
			"%w: Channel Attempt differs from continuation",
			ErrChannelDispatchIntegrity,
		)
	}
	if reconcile {
		if run.Frame.Step != corecontract.WaitingReconciliationLoopStep ||
			run.Frame.PendingAttemptID != "" ||
			run.Frame.PendingDispatchAttemptID != "" ||
			run.Frame.WaitingReason != channelUnknownWaitingReason {
			return fmt.Errorf(
				"%w: UNKNOWN Channel Frame is not waiting for reconciliation",
				ErrChannelDispatchIntegrity,
			)
		}
	} else if run.Frame.Step != corecontract.ChannelPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != current.Attempt.AttemptID {
		return fmt.Errorf(
			"%w: PENDING Channel is not the Frame pending identity",
			ErrChannelDispatchIntegrity,
		)
	}
	return nil
}

func updateRunAndFrameForChannelOutcome(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
	run RunForLoop,
	current ChannelDispatchRecord,
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
		WHERE run_id=? AND revision=? AND state=?
		  AND COALESCE(disposition, '')=?
	`,
		nextRunState,
		nullableModelString(nextDisposition),
		int64(nextRunRevision),
		updatedAt,
		run.RunID,
		int64(run.RunRevision),
		run.State,
		run.Disposition,
	)
	if err != nil {
		return fmt.Errorf("currentstore: update Channel outcome Run: %w", err)
	}
	if err := requireModelCASRow(runUpdate, "commit Channel outcome Run"); err != nil {
		return err
	}
	whereStep := corecontract.ChannelPendingLoopStep
	wherePending := current.Attempt.AttemptID
	if reconcile {
		whereStep = corecontract.WaitingReconciliationLoopStep
		wherePending = ""
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, continuation=?, pending_attempt_id=NULL,
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
		return fmt.Errorf("currentstore: update Channel outcome Frame: %w", err)
	}
	if err := requireModelCASRow(frameUpdate, "commit Channel outcome Frame"); err != nil {
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
		channelDispatchTerminalEvent,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		updatedAt,
	); err != nil {
		return fmt.Errorf(
			"%w: append Channel outcome event: %v",
			ErrChannelDispatchIntegrity,
			err,
		)
	}
	return nil
}

func reopenCommittedChannelOutcome(
	ctx context.Context,
	connection *sql.Conn,
	input CommitChannelDispatchOutcomeInput,
	prepared preparedChannelOutcome,
	current ChannelDispatchRecord,
) (CommitChannelDispatchOutcomeResult, error) {
	if input.ExpectedAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedAttemptRevision == math.MaxUint64 ||
			input.ExpectedAttemptRevision+1 != current.Attempt.Revision) {
		return CommitChannelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent Channel revision differs",
			ErrChannelDispatchConflict,
		)
	}
	if err := requireSameChannelOutcome(current, prepared); err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	currentLease, err := loadCurrentModelOutcomeLease(ctx, connection, input.Lease)
	if err != nil {
		return CommitChannelDispatchOutcomeResult{}, err
	}
	return CommitChannelDispatchOutcomeResult{
		Record:  cloneChannelDispatchRecord(current),
		Lease:   currentLease,
		Applied: false,
	}, nil
}

func requireSameChannelOutcome(
	current ChannelDispatchRecord,
	prepared preparedChannelOutcome,
) error {
	if current.Attempt.State != prepared.state ||
		(prepared.externalOperationID != "" &&
			current.Attempt.ExternalOperationID != prepared.externalOperationID) ||
		current.Attempt.ErrorClassification != prepared.errorClassification ||
		(prepared.unknownReason != "" && current.Attempt.UnknownReason != prepared.unknownReason) {
		return fmt.Errorf(
			"%w: persisted Channel outcome differs",
			ErrChannelDispatchConflict,
		)
	}
	if prepared.resultContent != nil &&
		current.Attempt.ResultRef != prepared.resultContent.Digest {
		return fmt.Errorf(
			"%w: persisted Channel result differs", ErrChannelDispatchConflict,
		)
	}
	if prepared.receiptContent != nil &&
		current.Attempt.ProviderReceiptRef != prepared.receiptContent.Digest {
		return fmt.Errorf(
			"%w: persisted Channel receipt differs", ErrChannelDispatchConflict,
		)
	}
	if prepared.evidenceContent != nil &&
		current.Attempt.ReconciliationEvidenceRef != prepared.evidenceContent.Digest {
		return fmt.Errorf(
			"%w: persisted Channel evidence differs", ErrChannelDispatchConflict,
		)
	}
	return nil
}
