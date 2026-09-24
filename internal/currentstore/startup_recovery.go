package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidStartupRecoveryScan = errors.New(
		"currentstore: invalid startup recovery scan",
	)
	ErrStartupRecoveryIntegrity = errors.New(
		"currentstore: startup recovery integrity violation",
	)
)

// StartupRecoveryRun is the minimal deterministic startup projection for one
// non-terminal Run or one Run that owns an unsettled Model/Action Attempt.
// Empty family fields mean that no unsettled Attempt exists in that family.
type StartupRecoveryRun struct {
	RunID                        string
	FrameStep                    string
	FrameRevision                uint64
	UnknownReason                string
	UnsettledAttemptID           string
	UnsettledAttemptState        corecontract.ModelAttemptState
	UnsettledActionAttemptID     string
	UnsettledActionAttemptState  ActionDispatchState
	UnsettledChannelAttemptID    string
	UnsettledChannelAttemptState DispatchState
}

// RecoverStartupPendingInput closes exactly one crash-left PENDING Attempt.
// It deliberately carries no Binding, Config, definition, artifact or module
// identity: startup recovery is a ledger repair boundary, never a replay
// permission or a reason to reconstruct an optional module closure.
type RecoverStartupPendingInput struct {
	Lease         RunLease
	AttemptKind   corecontract.AttemptKindV1
	AttemptID     string
	UnknownReason string
}

// RecoverStartupPendingResult returns the post-CAS lease needed for exact
// release. No invocation permit can be reconstructed by this operation.
type RecoverStartupPendingResult struct {
	Lease RunLease
}

// ScanStartupRecovery returns every non-terminal Run plus every Run owning a
// PENDING/MODEL_UNKNOWN Attempt in binary RunID order. It uses one read
// transaction, takes no Run lease, and performs no recovery mutation.
func (store *Store) ScanStartupRecovery(
	ctx context.Context,
) ([]StartupRecoveryRun, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidStartupRecoveryScan,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: acquire startup recovery scan connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: begin startup recovery scan: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	runIDs, err := scanStartupRecoveryRunIDs(ctx, connection)
	if err != nil {
		return nil, err
	}
	result := make([]StartupRecoveryRun, 0, len(runIDs))
	for _, runID := range runIDs {
		item, err := loadStartupRecoveryProjection(ctx, connection, runID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: commit startup recovery scan: %w",
			err,
		)
	}
	committed = true
	return result, nil
}

// RecoverStartupPending atomically converts one crash-left PENDING model or
// Action Attempt to the matching UNKNOWN state. The transaction reads only
// Run/Frame heads, dispatch ledger columns and the Usage placeholder needed
// for the transition. It never reads a Member snapshot, Action definition,
// CONFIG, AUTHORITY_CEILING or module artifact.
func (store *Store) RecoverStartupPending(
	ctx context.Context,
	input RecoverStartupPendingInput,
) (RecoverStartupPendingResult, error) {
	if ctx == nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidStartupRecoveryScan,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidStartupRecoveryScan,
			err,
		)
	}
	if err := input.AttemptKind.Validate(); err != nil ||
		!validLeaseOpaqueID(input.AttemptID) ||
		!validLeaseOpaqueID(input.UnknownReason) {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: invalid pending Attempt recovery identity",
			ErrInvalidStartupRecoveryScan,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"currentstore: acquire startup recovery connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"currentstore: begin pending startup recovery: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	runRevision, frame, err := loadStartupRecoveryWriteHead(
		ctx,
		connection,
		input.Lease,
	)
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		frame.Continuation,
	)
	if err != nil || continuation.AttemptKind != input.AttemptKind ||
		continuation.AttemptID != input.AttemptID {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: pending Attempt differs from the Loop continuation",
			ErrStartupRecoveryIntegrity,
		)
	}

	nextRunRevision, err := incrementSQLiteUint(
		runRevision,
		"startup recovery Run revision",
	)
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		frame.Revision,
		"startup recovery Frame revision",
	)
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		frame.LastAuthoritativeEvent,
		"startup recovery RunEvent sequence",
	)
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	continuationCanonical, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.WaitingReconciliationLoopStep,
		input.AttemptKind,
		continuation.LogicalStepID,
		input.AttemptID,
	)
	if err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: build startup UNKNOWN continuation: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	updatedAt := nowUnixMicro()

	var eventCanonical []byte
	var eventDigest string
	switch input.AttemptKind {
	case corecontract.AttemptKindModel:
		eventCanonical, eventDigest, err = recoverStartupModelPending(
			ctx,
			connection,
			input,
			frame,
			continuation,
			updatedAt,
		)
	case corecontract.AttemptKindAction:
		eventCanonical, eventDigest, err = recoverStartupActionPending(
			ctx,
			connection,
			input,
			frame,
			continuation,
			updatedAt,
		)
	case corecontract.AttemptKindChannel:
		eventCanonical, eventDigest, err = recoverStartupChannelPending(
			ctx,
			connection,
			input,
			frame,
			continuation,
			updatedAt,
		)
	}
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	if err := putAdmissionContent(
		ctx,
		connection,
		preparedAdmissionContent{
			Digest:         eventDigest,
			Kind:           ContentRunEventPayload,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: eventCanonical,
		},
		updatedAt,
	); err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: persist startup recovery event: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}

	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=? AND revision=? AND state=? AND disposition IS NULL
	`,
		corecontract.WaitingReconciliationLoopStep,
		corecontract.WaitingReconciliationLoopStep,
		int64(nextRunRevision),
		updatedAt,
		input.Lease.RunID,
		int64(runRevision),
		corecontract.InitialRunState,
	)
	if err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: update startup recovery Run: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(runUpdate, "recover startup Run"); err != nil {
		return RecoverStartupPendingResult{}, err
	}

	var frameUpdate sql.Result
	if input.AttemptKind == corecontract.AttemptKindModel {
		frameUpdate, err = connection.ExecContext(ctx, `
			UPDATE loop_frames
			SET frame_revision=?, step=?, continuation=?,
				pending_attempt_id=?, pending_dispatch_attempt_id=NULL,
				waiting_reason=?, last_authoritative_event=?
			WHERE run_id=? AND frame_revision=? AND step=?
			  AND pending_attempt_id=? AND pending_dispatch_attempt_id IS NULL
			  AND last_authoritative_event=?
			  AND lease_owner=? AND lease_epoch=? AND lease_expiry>?
			  AND EXISTS(
				SELECT 1 FROM runs
				WHERE runs.run_id=loop_frames.run_id AND runs.revision=?
			  )
		`,
			int64(nextFrameRevision),
			corecontract.WaitingReconciliationLoopStep,
			continuationCanonical,
			input.AttemptID,
			modelUnknownWaitingReason,
			int64(nextEvent),
			input.Lease.RunID,
			int64(frame.Revision),
			corecontract.ModelPendingLoopStep,
			input.AttemptID,
			int64(frame.LastAuthoritativeEvent),
			input.Lease.OwnerID,
			int64(input.Lease.LeaseEpoch),
			updatedAt,
			int64(nextRunRevision),
		)
	} else {
		pendingStep := corecontract.ActionPendingLoopStep
		waiting := actionUnknownWaitingReason
		if input.AttemptKind == corecontract.AttemptKindChannel {
			pendingStep = corecontract.ChannelPendingLoopStep
			waiting = channelUnknownWaitingReason
		}
		frameUpdate, err = connection.ExecContext(ctx, `
			UPDATE loop_frames
			SET frame_revision=?, step=?, continuation=?,
				pending_attempt_id=NULL, pending_dispatch_attempt_id=NULL,
				waiting_reason=?, last_authoritative_event=?
			WHERE run_id=? AND frame_revision=? AND step=?
			  AND pending_attempt_id IS NULL
			  AND pending_dispatch_attempt_id=?
			  AND last_authoritative_event=?
			  AND lease_owner=? AND lease_epoch=? AND lease_expiry>?
			  AND EXISTS(
				SELECT 1 FROM runs
				WHERE runs.run_id=loop_frames.run_id AND runs.revision=?
			  )
		`,
			int64(nextFrameRevision),
			corecontract.WaitingReconciliationLoopStep,
			continuationCanonical,
			waiting,
			int64(nextEvent),
			input.Lease.RunID,
			int64(frame.Revision),
			pendingStep,
			input.AttemptID,
			int64(frame.LastAuthoritativeEvent),
			input.Lease.OwnerID,
			int64(input.Lease.LeaseEpoch),
			updatedAt,
			int64(nextRunRevision),
		)
	}
	if err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: update startup recovery Frame: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(frameUpdate, "recover startup Frame"); err != nil {
		return RecoverStartupPendingResult{}, err
	}

	eventKind := corecontract.ModelDispatchTerminalEventKind
	if input.AttemptKind == corecontract.AttemptKindAction {
		eventKind = actionDispatchTerminalEvent
	} else if input.AttemptKind == corecontract.AttemptKindChannel {
		eventKind = channelDispatchTerminalEvent
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind, from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		input.Lease.RunID,
		int64(nextEvent),
		eventKind,
		int64(frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		updatedAt,
	); err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"%w: append startup recovery event: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := appendStartupRecoveryRunObservationV1(
		ctx, connection, input.Lease.RunID,
	); err != nil {
		return RecoverStartupPendingResult{}, err
	}
	switch input.AttemptKind {
	case corecontract.AttemptKindModel:
		err = appendModelResourceObservationV1(
			ctx, connection, input.AttemptID, overviewTransitionModelRecoveryV1,
		)
	case corecontract.AttemptKindAction:
		err = appendActionResourceObservationV1(
			ctx, connection, input.AttemptID, overviewTransitionActionRecoveryV1,
		)
	case corecontract.AttemptKindChannel:
		err = appendChannelResourceObservationV1(
			ctx, connection, input.AttemptID, overviewTransitionChannelRecoveryV1,
		)
	}
	if err != nil {
		return RecoverStartupPendingResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RecoverStartupPendingResult{}, fmt.Errorf(
			"currentstore: commit pending startup recovery: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return RecoverStartupPendingResult{Lease: nextLease}, nil
}

func loadStartupRecoveryWriteHead(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
) (uint64, LoopFrameRecord, error) {
	var (
		runState       string
		runDisposition sql.NullString
		runRevision    int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT state, disposition, revision
		FROM runs
		WHERE run_id=?
	`, lease.RunID).Scan(
		&runState,
		&runDisposition,
		&runRevision,
	); err != nil {
		return 0, LoopFrameRecord{}, fmt.Errorf(
			"%w: load startup recovery Run head: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if runRevision < 0 || uint64(runRevision) != lease.RunRevision ||
		runState != corecontract.InitialRunState || runDisposition.Valid {
		return 0, LoopFrameRecord{}, fmt.Errorf(
			"%w: pending startup Run head is not exact",
			ErrStartupRecoveryIntegrity,
		)
	}
	frame, err := loadLoopFrame(ctx, connection, lease)
	if err != nil {
		return 0, LoopFrameRecord{}, err
	}
	return uint64(runRevision), frame, nil
}

func recoverStartupModelPending(
	ctx context.Context,
	connection *sql.Conn,
	input RecoverStartupPendingInput,
	frame LoopFrameRecord,
	continuation corecontract.LoopContinuationV1,
	updatedAt int64,
) ([]byte, string, error) {
	var (
		logicalStepID       string
		logicalOperationKey string
		requestDigest       string
		state               string
		attemptRevision     int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT logical_step_id, logical_operation_key, request_digest,
			state, revision
		FROM model_dispatch_attempts
		WHERE attempt_id=? AND run_id=?
	`, input.AttemptID, input.Lease.RunID).Scan(
		&logicalStepID,
		&logicalOperationKey,
		&requestDigest,
		&state,
		&attemptRevision,
	); err != nil {
		return nil, "", fmt.Errorf(
			"%w: load startup model Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if state != string(corecontract.ModelAttemptPending) ||
		attemptRevision < 0 || continuation.LogicalStepID != logicalStepID ||
		frame.Step != corecontract.ModelPendingLoopStep ||
		frame.PendingAttemptID != input.AttemptID ||
		frame.PendingDispatchAttemptID != "" ||
		!validLeaseOpaqueID(logicalStepID) ||
		!moduleapi.ValidSHA256(logicalOperationKey) ||
		!moduleapi.ValidSHA256(requestDigest) {
		return nil, "", fmt.Errorf(
			"%w: model PENDING ledger projection is inconsistent",
			ErrStartupRecoveryIntegrity,
		)
	}
	var (
		usageRevision int64
		usageStatus   string
		ledger        sql.NullInt64
		inputTokens   sql.NullInt64
		cachedTokens  sql.NullInt64
		uncached      sql.NullInt64
		outputTokens  sql.NullInt64
		reasoning     sql.NullInt64
		rawReceipt    sql.NullString
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT revision, usage_status, ledger_sequence,
			input_tokens, cached_input_tokens, uncached_input_tokens,
			output_tokens, reasoning_tokens, raw_receipt_ref
		FROM model_usage
		WHERE attempt_id=? AND run_id=?
	`, input.AttemptID, input.Lease.RunID).Scan(
		&usageRevision,
		&usageStatus,
		&ledger,
		&inputTokens,
		&cachedTokens,
		&uncached,
		&outputTokens,
		&reasoning,
		&rawReceipt,
	); err != nil {
		return nil, "", fmt.Errorf(
			"%w: load startup model Usage: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if usageRevision < 0 || usageStatus != modelUsageStatusPending ||
		ledger.Valid || inputTokens.Valid || cachedTokens.Valid || uncached.Valid ||
		outputTokens.Valid || reasoning.Valid || rawReceipt.Valid {
		return nil, "", fmt.Errorf(
			"%w: PENDING model Usage contains post-call facts",
			ErrStartupRecoveryIntegrity,
		)
	}
	nextAttemptRevision, err := incrementSQLiteUint(
		uint64(attemptRevision),
		"startup model Attempt revision",
	)
	if err != nil {
		return nil, "", err
	}
	nextUsageRevision, err := incrementSQLiteUint(
		uint64(usageRevision),
		"startup model Usage revision",
	)
	if err != nil {
		return nil, "", err
	}
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_dispatch_attempts
		SET state=?, unknown_reason=?, revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND state=? AND revision=?
	`,
		string(corecontract.ModelAttemptUnknown),
		input.UnknownReason,
		int64(nextAttemptRevision),
		updatedAt,
		input.AttemptID,
		input.Lease.RunID,
		string(corecontract.ModelAttemptPending),
		attemptRevision,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: update startup model Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "recover startup model Attempt"); err != nil {
		return nil, "", err
	}
	usageUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_usage
		SET revision=?, usage_status=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND revision=?
		  AND usage_status=?
	`,
		int64(nextUsageRevision),
		modelUsageStatusReconciliationPending,
		input.AttemptID,
		input.Lease.RunID,
		usageRevision,
		modelUsageStatusPending,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: update startup model Usage: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(usageUpdate, "recover startup model Usage"); err != nil {
		return nil, "", err
	}
	current, err := queryModelDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return nil, "", err
	}
	resourceSemanticDigest, err := overviewResourceSemanticDigestV1(
		overviewResourceModelV1, current,
	)
	if err != nil {
		return nil, "", err
	}
	return prepareModelOutcomeEvent(
		ModelDispatchAttemptRecord{
			AttemptID:           input.AttemptID,
			LogicalOperationKey: logicalOperationKey,
			RunID:               input.Lease.RunID,
			LogicalStepID:       logicalStepID,
			Request:             ContentRecord{Digest: requestDigest},
		},
		corecontract.ModelAttemptUnknown,
		"",
		ModelUsageRecord{
			AttemptID: input.AttemptID, RunID: input.Lease.RunID,
			Revision: nextUsageRevision, UsageStatus: modelUsageStatusReconciliationPending,
		},
		corecontract.DispatchTransitionStartupRecoveryV1,
		resourceSemanticDigest,
	)
}

func recoverStartupActionPending(
	ctx context.Context,
	connection *sql.Conn,
	input RecoverStartupPendingInput,
	frame LoopFrameRecord,
	continuation corecontract.LoopContinuationV1,
	updatedAt int64,
) ([]byte, string, error) {
	var (
		logicalStepID       string
		logicalOperationKey string
		proposalDigest      string
		state               string
		attemptRevision     int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT logical_step_id, logical_operation_key, proposal_ref,
			state, revision
		FROM dispatch_attempts
		WHERE attempt_id=? AND run_id=? AND dispatch_kind='ACTION'
	`, input.AttemptID, input.Lease.RunID).Scan(
		&logicalStepID,
		&logicalOperationKey,
		&proposalDigest,
		&state,
		&attemptRevision,
	); err != nil {
		return nil, "", fmt.Errorf(
			"%w: load startup Action Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if state != string(ActionDispatchPending) || attemptRevision < 0 ||
		continuation.LogicalStepID != logicalStepID ||
		frame.Step != corecontract.ActionPendingLoopStep ||
		frame.PendingAttemptID != "" ||
		frame.PendingDispatchAttemptID != input.AttemptID ||
		!validLeaseOpaqueID(logicalStepID) ||
		!moduleapi.ValidSHA256(logicalOperationKey) ||
		!moduleapi.ValidSHA256(proposalDigest) {
		return nil, "", fmt.Errorf(
			"%w: Action PENDING ledger projection is inconsistent",
			ErrStartupRecoveryIntegrity,
		)
	}
	nextAttemptRevision, err := incrementSQLiteUint(
		uint64(attemptRevision),
		"startup Action Attempt revision",
	)
	if err != nil {
		return nil, "", err
	}
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE dispatch_attempts
		SET state=?, unknown_reason=?, revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND dispatch_kind='ACTION'
		  AND state=? AND revision=?
	`,
		string(ActionDispatchUnknown),
		input.UnknownReason,
		int64(nextAttemptRevision),
		updatedAt,
		input.AttemptID,
		input.Lease.RunID,
		string(ActionDispatchPending),
		attemptRevision,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: update startup Action Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "recover startup Action Attempt"); err != nil {
		return nil, "", err
	}
	record, err := queryActionDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return nil, "", err
	}
	semanticDigest, err := overviewResourceSemanticDigestV1(overviewResourceActionV1, record)
	if err != nil {
		return nil, "", err
	}
	return prepareActionDispatchEvent(actionDispatchEventV1{
		SchemaVersion:          actionDispatchEventSchemaV1,
		RunID:                  input.Lease.RunID,
		AttemptID:              input.AttemptID,
		LogicalStepID:          logicalStepID,
		LogicalOperationKey:    logicalOperationKey,
		ProposalDigest:         proposalDigest,
		State:                  ActionDispatchUnknown,
		TransitionOrigin:       corecontract.DispatchTransitionStartupRecoveryV1,
		ResourceSemanticDigest: semanticDigest,
	})
}

func recoverStartupChannelPending(
	ctx context.Context,
	connection *sql.Conn,
	input RecoverStartupPendingInput,
	frame LoopFrameRecord,
	continuation corecontract.LoopContinuationV1,
	updatedAt int64,
) ([]byte, string, error) {
	var (
		logicalStepID       string
		logicalOperationKey string
		proposalDigest      string
		state               string
		attemptRevision     int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT logical_step_id, logical_operation_key, channel_proposal_ref,
			state, revision
		FROM dispatch_attempts
		WHERE attempt_id=? AND run_id=? AND dispatch_kind='CHANNEL_SEND'
	`, input.AttemptID, input.Lease.RunID).Scan(
		&logicalStepID,
		&logicalOperationKey,
		&proposalDigest,
		&state,
		&attemptRevision,
	); err != nil {
		return nil, "", fmt.Errorf(
			"%w: load startup Channel Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if state != string(DispatchPending) || attemptRevision < 0 ||
		continuation.LogicalStepID != logicalStepID ||
		frame.Step != corecontract.ChannelPendingLoopStep ||
		frame.PendingAttemptID != "" ||
		frame.PendingDispatchAttemptID != input.AttemptID ||
		!validLeaseOpaqueID(logicalStepID) ||
		!moduleapi.ValidSHA256(logicalOperationKey) ||
		!moduleapi.ValidSHA256(proposalDigest) {
		return nil, "", fmt.Errorf(
			"%w: Channel PENDING ledger projection is inconsistent",
			ErrStartupRecoveryIntegrity,
		)
	}
	nextAttemptRevision, err := incrementSQLiteUint(
		uint64(attemptRevision),
		"startup Channel Attempt revision",
	)
	if err != nil {
		return nil, "", err
	}
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE dispatch_attempts
		SET state=?, unknown_reason=?, revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND dispatch_kind='CHANNEL_SEND'
		  AND state=? AND revision=?
	`,
		string(DispatchUnknown),
		input.UnknownReason,
		int64(nextAttemptRevision),
		updatedAt,
		input.AttemptID,
		input.Lease.RunID,
		string(DispatchPending),
		attemptRevision,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: update startup Channel Attempt: %v",
			ErrStartupRecoveryIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "recover startup Channel Attempt"); err != nil {
		return nil, "", err
	}
	record, err := queryChannelDispatchRecord(ctx, connection, input.AttemptID)
	if err != nil {
		return nil, "", err
	}
	semanticDigest, err := overviewResourceSemanticDigestV1(overviewResourceChannelV1, record)
	if err != nil {
		return nil, "", err
	}
	return prepareChannelDispatchEvent(channelDispatchEventV1{
		SchemaVersion:          channelDispatchEventSchemaV1,
		RunID:                  input.Lease.RunID,
		AttemptID:              input.AttemptID,
		LogicalStepID:          logicalStepID,
		LogicalOperationKey:    logicalOperationKey,
		ProposalDigest:         proposalDigest,
		State:                  DispatchUnknown,
		TransitionOrigin:       corecontract.DispatchTransitionStartupRecoveryV1,
		ResourceSemanticDigest: semanticDigest,
	})
}

func scanStartupRecoveryRunIDs(
	ctx context.Context,
	connection *sql.Conn,
) ([]string, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT run_id
		FROM runs
		WHERE state<>?
		   OR disposition IS NULL
		   OR disposition<>?
		UNION
		SELECT run_id
		FROM model_dispatch_attempts
		WHERE state IN ('PENDING', 'MODEL_UNKNOWN')
		UNION
		SELECT run_id
		FROM dispatch_attempts
		WHERE state IN ('PENDING', 'UNKNOWN')
		ORDER BY run_id
	`, corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: enumerate startup recovery Runs: %w",
			err,
		)
	}
	defer rows.Close()

	runIDs := make([]string, 0)
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf(
				"currentstore: scan startup recovery Run ID: %w",
				err,
			)
		}
		if !validLeaseOpaqueID(runID) {
			return nil, fmt.Errorf(
				"%w: invalid Run ID",
				ErrStartupRecoveryIntegrity,
			)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: iterate startup recovery Runs: %w",
			err,
		)
	}
	return runIDs, nil
}

func loadStartupRecoveryProjection(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
) (StartupRecoveryRun, error) {
	item := StartupRecoveryRun{RunID: runID}
	var (
		continuationCanonical []byte
		pendingModel          sql.NullString
		pendingAction         sql.NullString
		waitingReason         sql.NullString
		runState              string
		runDisposition        sql.NullString
		frameRevision         int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT f.step, f.frame_revision, f.continuation, f.pending_attempt_id,
			f.pending_dispatch_attempt_id, f.waiting_reason,
			r.state, r.disposition
		FROM loop_frames AS f
		JOIN runs AS r ON r.run_id=f.run_id
		WHERE f.run_id=?
	`, runID).Scan(
		&item.FrameStep,
		&frameRevision,
		&continuationCanonical,
		&pendingModel,
		&pendingAction,
		&waitingReason,
		&runState,
		&runDisposition,
	); err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"%w: Run %q has no readable LoopFrame: %v",
			ErrStartupRecoveryIntegrity,
			runID,
			err,
		)
	}
	if frameRevision < 0 {
		return StartupRecoveryRun{}, fmt.Errorf(
			"%w: Run %q has an invalid Frame revision",
			ErrStartupRecoveryIntegrity,
			runID,
		)
	}
	item.FrameRevision = uint64(frameRevision)

	var unsettledModelLogicalStep string
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id, state, logical_step_id
		FROM model_dispatch_attempts
		WHERE run_id=?
		  AND state IN ('PENDING', 'MODEL_UNKNOWN')
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: scan startup recovery Attempts: %w",
			err,
		)
	}
	count := 0
	for rows.Next() {
		var state string
		if err := rows.Scan(
			&item.UnsettledAttemptID,
			&state,
			&unsettledModelLogicalStep,
		); err != nil {
			return StartupRecoveryRun{}, fmt.Errorf(
				"currentstore: scan startup recovery Attempt: %w",
				err,
			)
		}
		count++
		if count > 1 {
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has multiple unsettled model Attempts",
				ErrStartupRecoveryIntegrity,
				runID,
			)
		}
		if !validLeaseOpaqueID(item.UnsettledAttemptID) {
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has an invalid unsettled Attempt ID",
				ErrStartupRecoveryIntegrity,
				runID,
			)
		}
		item.UnsettledAttemptState = corecontract.ModelAttemptState(state)
		switch item.UnsettledAttemptState {
		case corecontract.ModelAttemptPending,
			corecontract.ModelAttemptUnknown:
		default:
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has unsupported unsettled Attempt state %q",
				ErrStartupRecoveryIntegrity,
				runID,
				state,
			)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: iterate startup recovery Attempts: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: close startup recovery Attempts: %w",
			err,
		)
	}
	var unsettledActionLogicalStep string
	actionRows, err := connection.QueryContext(ctx, `
		SELECT attempt_id, state, logical_step_id
		FROM dispatch_attempts
		WHERE run_id=?
		  AND dispatch_kind='ACTION'
		  AND state IN ('PENDING', 'UNKNOWN')
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: scan startup recovery Action Attempts: %w",
			err,
		)
	}
	actionCount := 0
	for actionRows.Next() {
		var state string
		if err := actionRows.Scan(
			&item.UnsettledActionAttemptID,
			&state,
			&unsettledActionLogicalStep,
		); err != nil {
			_ = actionRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"currentstore: scan startup recovery Action Attempt: %w",
				err,
			)
		}
		actionCount++
		if actionCount > 1 {
			_ = actionRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has multiple unsettled Action Attempts",
				ErrStartupRecoveryIntegrity,
				runID,
			)
		}
		if !validLeaseOpaqueID(item.UnsettledActionAttemptID) {
			_ = actionRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has an invalid unsettled Action Attempt ID",
				ErrStartupRecoveryIntegrity,
				runID,
			)
		}
		item.UnsettledActionAttemptState = ActionDispatchState(state)
		if item.UnsettledActionAttemptState != ActionDispatchPending &&
			item.UnsettledActionAttemptState != ActionDispatchUnknown {
			_ = actionRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has unsupported unsettled Action state %q",
				ErrStartupRecoveryIntegrity,
				runID,
				state,
			)
		}
	}
	if err := actionRows.Err(); err != nil {
		_ = actionRows.Close()
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: iterate startup recovery Action Attempts: %w",
			err,
		)
	}
	if err := actionRows.Close(); err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: close startup recovery Action Attempts: %w",
			err,
		)
	}
	var unsettledChannelLogicalStep string
	channelRows, err := connection.QueryContext(ctx, `
		SELECT attempt_id, state, logical_step_id
		FROM dispatch_attempts
		WHERE run_id=?
		  AND dispatch_kind='CHANNEL_SEND'
		  AND state IN ('PENDING', 'UNKNOWN')
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: scan startup recovery Channel Attempts: %w",
			err,
		)
	}
	channelCount := 0
	for channelRows.Next() {
		var state string
		if err := channelRows.Scan(
			&item.UnsettledChannelAttemptID,
			&state,
			&unsettledChannelLogicalStep,
		); err != nil {
			_ = channelRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"currentstore: scan startup recovery Channel Attempt: %w",
				err,
			)
		}
		channelCount++
		if channelCount > 1 || !validLeaseOpaqueID(item.UnsettledChannelAttemptID) {
			_ = channelRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has invalid or multiple unsettled Channel Attempts",
				ErrStartupRecoveryIntegrity,
				runID,
			)
		}
		item.UnsettledChannelAttemptState = DispatchState(state)
		if item.UnsettledChannelAttemptState != DispatchPending &&
			item.UnsettledChannelAttemptState != DispatchUnknown {
			_ = channelRows.Close()
			return StartupRecoveryRun{}, fmt.Errorf(
				"%w: Run %q has unsupported unsettled Channel state %q",
				ErrStartupRecoveryIntegrity,
				runID,
				state,
			)
		}
	}
	if err := channelRows.Err(); err != nil {
		_ = channelRows.Close()
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: iterate startup recovery Channel Attempts: %w",
			err,
		)
	}
	if err := channelRows.Close(); err != nil {
		return StartupRecoveryRun{}, fmt.Errorf(
			"currentstore: close startup recovery Channel Attempts: %w",
			err,
		)
	}
	if count+actionCount+channelCount > 1 {
		return StartupRecoveryRun{}, fmt.Errorf(
			"%w: Run %q has unsettled Attempts in multiple dispatch families",
			ErrStartupRecoveryIntegrity,
			runID,
		)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		continuationCanonical,
	)
	if err != nil || continuation.State != item.FrameStep {
		return StartupRecoveryRun{}, fmt.Errorf(
			"%w: Run %q has an invalid minimal Loop continuation",
			ErrStartupRecoveryIntegrity,
			runID,
		)
	}
	if err := validateStartupRecoveryProjection(
		ctx,
		connection,
		item,
		continuation,
		pendingModel,
		pendingAction,
		waitingReason,
		runState,
		runDisposition,
		unsettledModelLogicalStep,
		unsettledActionLogicalStep,
		unsettledChannelLogicalStep,
	); err != nil {
		return StartupRecoveryRun{}, err
	}
	if item.FrameStep == corecontract.WaitingReconciliationLoopStep {
		item.UnknownReason, err = loadStartupRecoveryUnknownReason(
			ctx,
			connection,
			continuation,
		)
		if err != nil {
			return StartupRecoveryRun{}, err
		}
	}
	return item, nil
}

func loadStartupRecoveryUnknownReason(
	ctx context.Context,
	connection *sql.Conn,
	continuation corecontract.LoopContinuationV1,
) (string, error) {
	query := ""
	switch continuation.AttemptKind {
	case corecontract.AttemptKindModel:
		query = `SELECT unknown_reason FROM model_dispatch_attempts WHERE attempt_id=?`
	case corecontract.AttemptKindAction, corecontract.AttemptKindChannel:
		query = `SELECT unknown_reason FROM dispatch_attempts WHERE attempt_id=?`
	default:
		return "", fmt.Errorf(
			"%w: unsupported reconciliation AttemptKind %q",
			ErrStartupRecoveryIntegrity,
			continuation.AttemptKind,
		)
	}
	var reason sql.NullString
	if err := connection.QueryRowContext(
		ctx,
		query,
		continuation.AttemptID,
	).Scan(&reason); err != nil || !reason.Valid || !validLeaseOpaqueID(reason.String) {
		return "", fmt.Errorf(
			"%w: reconciliation Attempt has no valid UNKNOWN reason",
			ErrStartupRecoveryIntegrity,
		)
	}
	return reason.String, nil
}

func validateStartupRecoveryProjection(
	ctx context.Context,
	connection *sql.Conn,
	item StartupRecoveryRun,
	continuation corecontract.LoopContinuationV1,
	pendingModel sql.NullString,
	pendingAction sql.NullString,
	waitingReason sql.NullString,
	runState string,
	runDisposition sql.NullString,
	unsettledModelLogicalStep string,
	unsettledActionLogicalStep string,
	unsettledChannelLogicalStep string,
) error {
	fail := func(reason string) error {
		return fmt.Errorf(
			"%w: Run %q %s",
			ErrStartupRecoveryIntegrity,
			item.RunID,
			reason,
		)
	}
	admitted := func() bool {
		return runState == corecontract.InitialRunState && !runDisposition.Valid
	}
	waiting := func() bool {
		return runState == corecontract.WaitingReconciliationLoopStep &&
			runDisposition.Valid &&
			runDisposition.String == corecontract.WaitingReconciliationLoopStep
	}
	modelMatches := func(state corecontract.ModelAttemptState) bool {
		return item.UnsettledAttemptState == state &&
			item.UnsettledActionAttemptState == "" &&
			item.UnsettledChannelAttemptState == "" &&
			continuation.AttemptKind == corecontract.AttemptKindModel &&
			continuation.AttemptID == item.UnsettledAttemptID &&
			continuation.LogicalStepID == unsettledModelLogicalStep
	}
	actionMatches := func(state ActionDispatchState) bool {
		return item.UnsettledAttemptState == "" &&
			item.UnsettledActionAttemptState == state &&
			item.UnsettledChannelAttemptState == "" &&
			continuation.AttemptKind == corecontract.AttemptKindAction &&
			continuation.AttemptID == item.UnsettledActionAttemptID &&
			continuation.LogicalStepID == unsettledActionLogicalStep
	}
	channelMatches := func(state DispatchState) bool {
		return item.UnsettledAttemptState == "" &&
			item.UnsettledActionAttemptState == "" &&
			item.UnsettledChannelAttemptState == state &&
			continuation.AttemptKind == corecontract.AttemptKindChannel &&
			continuation.AttemptID == item.UnsettledChannelAttemptID &&
			continuation.LogicalStepID == unsettledChannelLogicalStep
	}

	switch item.FrameStep {
	case corecontract.InitialLoopStep:
		if !admitted() || pendingModel.Valid || pendingAction.Valid ||
			waitingReason.Valid || item.UnsettledAttemptState != "" ||
			item.UnsettledActionAttemptState != "" ||
			item.UnsettledChannelAttemptState != "" ||
			continuation.AttemptKind != "" || continuation.AttemptID != "" ||
			continuation.LogicalStepID != "" {
			return fail("READY ledger projection is inconsistent")
		}
		var modelCount, actionCount int64
		if err := connection.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
				(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?)
		`, item.RunID, item.RunID).Scan(&modelCount, &actionCount); err != nil {
			return fail("cannot count READY Attempts")
		}
		if modelCount != 0 || actionCount != 0 {
			return fail("READY contains hidden dispatch Attempts")
		}
	case corecontract.WaitingRepairActivationLoopStep:
		if runState != corecontract.InitialRunState ||
			!runDisposition.Valid ||
			runDisposition.String != "WAITING_EXTERNAL" ||
			pendingModel.Valid || pendingAction.Valid ||
			!waitingReason.Valid ||
			waitingReason.String != compositeRepairDormantWaitingReason ||
			item.UnsettledAttemptState != "" ||
			item.UnsettledActionAttemptState != "" ||
			item.UnsettledChannelAttemptState != "" ||
			continuation.State !=
				corecontract.WaitingRepairActivationLoopStep ||
			continuation.AttemptKind != "" || continuation.AttemptID != "" ||
			continuation.LogicalStepID != "" {
			return fail("WAITING_REPAIR_ACTIVATION ledger projection is inconsistent")
		}
		var manifestCanonical []byte
		if err := connection.QueryRowContext(ctx, `
			SELECT canonical_json FROM run_manifests WHERE run_id=?
		`, item.RunID).Scan(&manifestCanonical); err != nil {
			return fail("cannot load dormant repair Manifest")
		}
		manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
		if err != nil || manifest.RunID != item.RunID ||
			manifest.Composite == nil ||
			manifest.Composite.RepairRound !=
				corecontract.CompositeRepairRoundOneV1 {
			return fail("dormant repair Manifest identity differs")
		}
		root, err := loadCompositeRootManifest(
			ctx,
			connection,
			manifest.Composite.RootRunID,
		)
		if err != nil || root.Composite.Plan.Decision == nil ||
			manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
			!compositeRepairManifestInDecisionPlan(root, manifest) {
			return fail("dormant repair does not close its Decision plan")
		}
		var modelCount, dispatchCount int64
		if err := connection.QueryRowContext(ctx, `
			SELECT
			 (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			 (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?)
		`, item.RunID, item.RunID).Scan(
			&modelCount,
			&dispatchCount,
		); err != nil || modelCount != 0 || dispatchCount != 0 {
			return fail("dormant repair contains hidden dispatch Attempts")
		}
	case corecontract.WaitingChildrenLoopStep:
		if runState != corecontract.InitialRunState ||
			!runDisposition.Valid ||
			runDisposition.String != "WAITING_EXTERNAL" ||
			pendingModel.Valid || pendingAction.Valid ||
			!waitingReason.Valid ||
			waitingReason.String != compositeChildrenPendingWaitingReason ||
			item.UnsettledAttemptState != "" ||
			item.UnsettledActionAttemptState != "" ||
			item.UnsettledChannelAttemptState != "" ||
			continuation.AttemptKind != "" || continuation.AttemptID != "" ||
			continuation.LogicalStepID != "" {
			return fail("WAITING_CHILDREN ledger projection is inconsistent")
		}
		var manifestCanonical []byte
		if err := connection.QueryRowContext(ctx, `
			SELECT canonical_json
			FROM run_manifests
			WHERE run_id=?
		`, item.RunID).Scan(&manifestCanonical); err != nil {
			return fail("cannot load WAITING_CHILDREN Manifest")
		}
		manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
		if err != nil || manifest.RunID != item.RunID ||
			manifest.Composite == nil {
			return fail("WAITING_CHILDREN does not belong to a Composite coordinator")
		}
		switch manifest.Composite.Role {
		case corecontract.CompositeRunRoleRootV1:
			if manifest.Composite.Plan == nil {
				return fail("WAITING_CHILDREN root has no Composite plan")
			}
		case corecontract.CompositeRunRoleReviewerV1:
			root, loadErr := loadCompositeRootManifest(
				ctx,
				connection,
				manifest.Composite.RootRunID,
			)
			if loadErr != nil || root.Composite == nil ||
				root.Composite.Plan == nil ||
				!compositeReviewerManifestInPlan(root, manifest) ||
				manifest.Composite.ParentManifestDigest != root.ManifestDigest {
				return fail("WAITING_CHILDREN Reviewer does not close its root")
			}
		default:
			return fail("WAITING_CHILDREN role is not a Composite coordinator")
		}
		var modelCount, dispatchCount int64
		if err := connection.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
				(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?)
		`, item.RunID, item.RunID).Scan(
			&modelCount,
			&dispatchCount,
		); err != nil {
			return fail("cannot count WAITING_CHILDREN Attempts")
		}
		if modelCount != 0 || dispatchCount != 0 {
			return fail("WAITING_CHILDREN contains hidden dispatch Attempts")
		}
	case corecontract.ModelPendingLoopStep:
		if !admitted() || !modelMatches(corecontract.ModelAttemptPending) ||
			!pendingModel.Valid ||
			pendingModel.String != item.UnsettledAttemptID ||
			pendingAction.Valid || waitingReason.Valid {
			return fail("MODEL_PENDING ledger projection is inconsistent")
		}
	case corecontract.ActionPendingLoopStep:
		if !admitted() || !actionMatches(ActionDispatchPending) ||
			pendingModel.Valid || !pendingAction.Valid ||
			pendingAction.String != item.UnsettledActionAttemptID ||
			waitingReason.Valid {
			return fail("ACTION_PENDING ledger projection is inconsistent")
		}
	case corecontract.ChannelPendingLoopStep:
		if !admitted() || !channelMatches(DispatchPending) ||
			pendingModel.Valid || !pendingAction.Valid ||
			pendingAction.String != item.UnsettledChannelAttemptID ||
			waitingReason.Valid {
			return fail("CHANNEL_PENDING ledger projection is inconsistent")
		}
	case corecontract.WaitingReconciliationLoopStep:
		if !waiting() || pendingAction.Valid || !waitingReason.Valid {
			return fail("WAITING_RECONCILIATION Run head is inconsistent")
		}
		switch continuation.AttemptKind {
		case corecontract.AttemptKindModel:
			if !modelMatches(corecontract.ModelAttemptUnknown) ||
				!pendingModel.Valid ||
				pendingModel.String != item.UnsettledAttemptID ||
				waitingReason.String != modelUnknownWaitingReason {
				return fail("MODEL_UNKNOWN ledger projection is inconsistent")
			}
		case corecontract.AttemptKindAction:
			if !actionMatches(ActionDispatchUnknown) || pendingModel.Valid ||
				waitingReason.String != actionUnknownWaitingReason {
				return fail("Action UNKNOWN ledger projection is inconsistent")
			}
		case corecontract.AttemptKindChannel:
			if !channelMatches(DispatchUnknown) || pendingModel.Valid ||
				waitingReason.String != channelUnknownWaitingReason {
				return fail("Channel UNKNOWN ledger projection is inconsistent")
			}
		default:
			return fail("reconciliation AttemptKind is unsupported")
		}
	case corecontract.ModelReadyAfterActionLoopStep:
		if !admitted() || pendingModel.Valid || pendingAction.Valid ||
			waitingReason.Valid || item.UnsettledAttemptState != "" ||
			item.UnsettledActionAttemptState != "" ||
			item.UnsettledChannelAttemptState != "" ||
			continuation.AttemptKind != corecontract.AttemptKindAction {
			return fail("MODEL_READY_AFTER_ACTION head is inconsistent")
		}
		var (
			modelSucceeded int64
			actionState    string
			actionStep     string
		)
		if err := connection.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM model_dispatch_attempts
				 WHERE run_id=? AND state='SUCCEEDED'),
				state,
				logical_step_id
			FROM dispatch_attempts
			WHERE run_id=? AND attempt_id=?
		`,
			item.RunID,
			item.RunID,
			continuation.AttemptID,
		).Scan(
			&modelSucceeded,
			&actionState,
			&actionStep,
		); err != nil {
			return fail("cannot load MODEL_READY_AFTER_ACTION ledger source")
		}
		if modelSucceeded != 1 || actionState != string(ActionDispatchSucceeded) ||
			actionStep != continuation.LogicalStepID {
			return fail("MODEL_READY_AFTER_ACTION source is inconsistent")
		}
	default:
		return fail("has an unsupported startup Frame step")
	}
	return nil
}
