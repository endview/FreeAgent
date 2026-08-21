package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	modelUsageStatusPending        = "PENDING"
	modelUsageStatusReported       = "PROVIDER_REPORTED"
	modelUsageStatusNoReport       = "NO_USAGE_REPORTED"
	modelUsageStatusReconciliation = "PENDING_RECONCILIATION"
	modelUnknownWaitingReason      = "MODEL_UNKNOWN"
)

// ModelUsageRecord is the detached authoritative Usage row for one model
// Attempt. Nil token and cost pointers are UNKNOWN; a non-nil zero is known.
type ModelUsageRecord struct {
	AttemptID            string
	RunID                string
	LedgerSequence       *uint64
	Revision             uint64
	Tokens               corecontract.UsageTokens
	EstimatedCost        *string
	ProviderReportedCost *string
	ReconciledCost       *string
	ReconciliationStatus string
	RawReceiptRef        string
}

// ModelDispatchRecord is the narrow typed recovery projection for an Attempt
// and its mandatory one-to-one Usage row.
type ModelDispatchRecord struct {
	Attempt ModelDispatchAttemptRecord
	Usage   ModelUsageRecord
}

// CommitModelDispatchOutcomeInput contains the exact fencing and Attempt CAS
// plus canonical provider facts. Empty optional canonical byte slices mean no
// new fact; they never mean a known zero.
type CommitModelDispatchOutcomeInput struct {
	Lease                           RunLease
	AttemptID                       string
	InvocationID                    string
	Provider                        moduleapi.ActivatedModuleRef
	ExpectedAttemptRevision         uint64
	State                           corecontract.ModelAttemptState
	OutputCanonical                 []byte
	UsageReceiptCanonical           []byte
	ProviderRequestID               string
	ErrorClassification             string
	UnknownReason                   string
	ReconciliationEvidenceCanonical []byte
}

// CommitModelDispatchOutcomeResult returns the exact post-commit fencing
// token. Applied is false only when an already committed, identical outcome
// was recovered after a lost response.
type CommitModelDispatchOutcomeResult struct {
	Record  ModelDispatchRecord
	Lease   RunLease
	Applied bool
}

type preparedModelDispatchOutcome struct {
	state               corecontract.ModelAttemptState
	output              moduleapi.ModelGenerateOutputV1
	outputCanonical     []byte
	resultContent       *preparedAdmissionContent
	usage               moduleapi.ModelUsageReceiptV1
	usageCanonical      []byte
	receiptContent      *preparedAdmissionContent
	providerRequestID   string
	errorClassification string
	unknownReason       string
	evidenceCanonical   []byte
	evidenceContent     *preparedAdmissionContent
}

// CommitModelDispatchOutcome atomically commits the post-network boundary for
// the original Attempt. A successful cross-Workspace Specialist also commits
// its strict RESULT envelope in this transaction. A persistence failure rolls
// every authoritative row back to its preceding PENDING or MODEL_UNKNOWN
// state.
func (store *Store) CommitModelDispatchOutcome(
	ctx context.Context,
	input CommitModelDispatchOutcomeInput,
) (CommitModelDispatchOutcomeResult, error) {
	if ctx == nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelDispatch,
		)
	}
	input.OutputCanonical = bytes.Clone(input.OutputCanonical)
	input.UsageReceiptCanonical =
		bytes.Clone(input.UsageReceiptCanonical)
	input.ReconciliationEvidenceCanonical =
		bytes.Clone(input.ReconciliationEvidenceCanonical)
	if !validLeaseOpaqueID(input.AttemptID) {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid Attempt ID",
			ErrInvalidModelDispatch,
		)
	}
	if input.InvocationID != input.AttemptID {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invocation identity does not match Attempt",
			ErrInvalidModelDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invalid invocation Provider: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	if input.ExpectedAttemptRevision >= math.MaxInt64 {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Attempt revision cannot advance in SQLite INTEGER",
			ErrInvalidModelDispatch,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	prepared, err := prepareModelDispatchOutcome(input)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if prepared.state == corecontract.ModelAttemptSucceeded &&
		prepared.output.ActionRequest != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: wire-valid Action output must use the atomic Action begin or legal rejection transaction",
			ErrModelDispatchConflict,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: acquire model outcome connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: begin CommitModelDispatchOutcome: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := queryModelDispatchRecord(
		ctx,
		connection,
		input.AttemptID,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if current.Attempt.RunID != input.Lease.RunID {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Attempt does not belong to the leased Run",
			ErrModelDispatchConflict,
		)
	}
	if input.Provider != current.Attempt.Binding.Provider {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: invocation Provider differs from frozen Binding",
			ErrModelDispatchConflict,
		)
	}

	if current.Attempt.State == prepared.state {
		result, err := commitIdempotentModelOutcome(
			ctx,
			connection,
			input,
			prepared,
			current,
		)
		if err != nil {
			return CommitModelDispatchOutcomeResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModelV1, input.AttemptID,
		); err != nil {
			return CommitModelDispatchOutcomeResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"currentstore: commit idempotent model outcome: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}
	if current.Attempt.Revision != input.ExpectedAttemptRevision {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: expected Attempt revision %d, found %d",
			ErrModelDispatchConflict,
			input.ExpectedAttemptRevision,
			current.Attempt.Revision,
		)
	}
	if !current.Attempt.State.AllowsTransition(prepared.state) {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Attempt state %s cannot transition to %s",
			ErrModelDispatchConflict,
			current.Attempt.State,
			prepared.state,
		)
	}
	if current.Attempt.State == corecontract.ModelAttemptUnknown &&
		prepared.evidenceContent == nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: MODEL_UNKNOWN reconciliation requires canonical evidence",
			ErrInvalidModelDispatch,
		)
	}

	run, err := loadRunForModelOutcome(ctx, connection, input.Lease)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	workspaceRequest, err := loadWorkspaceTransferRequestV1(
		ctx,
		connection,
		run,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Workspace transfer request closure: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	run, err = runWithWorkspaceTransferRecordV1(run, workspaceRequest)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: Workspace transfer recovery contents: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := validateAttemptAgainstOutcomeRun(current, run); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if err := validateModelOutcomeSourceState(current.Attempt.State, run); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	var workspaceResult *WorkspaceTransferRecordV1
	if prepared.state == corecontract.ModelAttemptSucceeded &&
		run.WorkspaceTransfer != nil {
		payload := ContentRecord{
			Digest:         prepared.resultContent.Digest,
			Kind:           prepared.resultContent.Kind,
			MediaType:      prepared.resultContent.MediaType,
			CanonicalBytes: bytes.Clone(prepared.resultContent.CanonicalBytes),
			SizeBytes:      int64(len(prepared.resultContent.CanonicalBytes)),
		}
		workspaceResult, err = prepareWorkspaceTransferResultV1(run, payload)
		if err != nil {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: Workspace transfer Specialist result: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
	}
	ledgerHead, err := validateModelUsageLedgerHead(
		ctx,
		connection,
		run.RunID,
		run.Frame.BudgetStateRef,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	nextAttemptRevision, err := incrementSQLiteUint(
		current.Attempt.Revision,
		"Attempt revision",
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	nextUsageRevision, err := incrementSQLiteUint(
		current.Usage.Revision,
		"Usage revision",
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(
		run.RunRevision,
		"Run revision",
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		run.Frame.Revision,
		"Frame revision",
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	merged, err := mergeModelOutcomeFacts(current, prepared)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if err := applyFrozenModelEstimatedCost(
		ctx,
		connection,
		current,
		&merged,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: estimated cost: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if merged.Usage.LedgerSequence == nil &&
		modelUsageHasBillableFact(merged.Usage) {
		if ledgerHead >= math.MaxInt64 {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: Usage ledger sequence cannot advance",
				ErrModelDispatchIntegrity,
			)
		}
		sequence := ledgerHead + 1
		merged.Usage.LedgerSequence = &sequence
	}
	nextBudgetRef := run.Frame.BudgetStateRef
	if merged.Usage.LedgerSequence != nil {
		nextBudgetRef, err = corecontract.NewBudgetStateRefV1(
			run.RunID,
			*merged.Usage.LedgerSequence,
		)
		if err != nil {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: next BudgetStateRef: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
	}
	merged.Attempt.Revision = nextAttemptRevision
	merged.Usage.Revision = nextUsageRevision
	createdAt := nowUnixMicro()
	merged.Attempt.UpdatedAt = time.UnixMicro(createdAt).UTC()
	resourceSemanticDigest, err := overviewResourceSemanticDigestV1(
		overviewResourceModelV1, merged,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	nextStep, nextDisposition, pendingAttempt, waitingReason :=
		modelOutcomeFrameProjection(prepared.state, input.AttemptID)
	continuation, err := corecontract.NewLoopContinuationV1(
		nextStep,
		current.Attempt.LogicalStepID,
		current.Attempt.AttemptID,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: outcome continuation: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	eventCanonical, eventDigest, err := prepareModelOutcomeEvent(
		current.Attempt,
		prepared.state,
		merged.Attempt.ResultRef,
		merged.Usage,
		corecontract.DispatchTransitionOrdinaryOutcomeV1,
		resourceSemanticDigest,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	for _, content := range []*preparedAdmissionContent{
		prepared.resultContent,
		prepared.receiptContent,
		prepared.evidenceContent,
		{
			Digest:         eventDigest,
			Kind:           ContentRunEventPayload,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: eventCanonical,
		},
	} {
		if content == nil {
			continue
		}
		if err := putAdmissionContent(
			ctx,
			connection,
			*content,
			createdAt,
		); err != nil {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: persist outcome content: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
	}
	if err := putWorkspaceTransferRecordV1(
		ctx,
		connection,
		workspaceResult,
		false,
		createdAt,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: persist Workspace transfer result: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}

	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_dispatch_attempts
		SET
			state=?,
			provider_request_id=?,
			provider_receipt_ref=?,
			result_ref=?,
			error_classification=?,
			reconciliation_evidence_ref=?,
			unknown_reason=?,
			revision=?,
			updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=?
		  AND run_id=?
		  AND state=?
		  AND revision=?
	`,
		string(merged.Attempt.State),
		nullableModelString(merged.Attempt.ProviderRequestID),
		nullableModelString(merged.Attempt.ProviderReceiptRef),
		nullableModelString(merged.Attempt.ResultRef),
		nullableModelString(merged.Attempt.ErrorClassification),
		nullableModelString(
			merged.Attempt.ReconciliationEvidenceRef,
		),
		nullableModelString(merged.Attempt.UnknownReason),
		int64(nextAttemptRevision),
		createdAt,
		current.Attempt.AttemptID,
		run.RunID,
		string(current.Attempt.State),
		int64(current.Attempt.Revision),
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: update model Attempt: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(
		attemptUpdate,
		"commit model outcome Attempt",
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	usageUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_usage
		SET
			ledger_sequence=?,
			revision=?,
			input_tokens=?,
			cached_input_tokens=?,
			uncached_input_tokens=?,
			output_tokens=?,
			reasoning_tokens=?,
			estimated_cost=?,
			provider_reported_cost=?,
			reconciled_cost=?,
			reconciliation_status=?,
			raw_receipt_ref=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=?
		  AND run_id=?
		  AND revision=?
	`,
		nullableModelUint(merged.Usage.LedgerSequence),
		int64(nextUsageRevision),
		nullableModelUint(merged.Usage.Tokens.Input),
		nullableModelUint(merged.Usage.Tokens.CachedInput),
		nullableModelUint(merged.Usage.Tokens.UncachedInput),
		nullableModelUint(merged.Usage.Tokens.Output),
		nullableModelUint(merged.Usage.Tokens.Reasoning),
		nullableModelStringPointer(merged.Usage.EstimatedCost),
		nullableModelStringPointer(
			merged.Usage.ProviderReportedCost,
		),
		nullableModelStringPointer(merged.Usage.ReconciledCost),
		merged.Usage.ReconciliationStatus,
		nullableModelString(merged.Usage.RawReceiptRef),
		current.Attempt.AttemptID,
		run.RunID,
		int64(current.Usage.Revision),
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: update model Usage: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(
		usageUpdate,
		"commit model outcome Usage",
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	if prepared.state == corecontract.ModelAttemptSucceeded {
		if err := appendSuccessfulModelOutcomeMemory(
			ctx,
			connection,
			run,
			merged.Attempt,
			prepared.output.AssistantText,
			createdAt,
		); err != nil {
			return CommitModelDispatchOutcomeResult{}, err
		}

		historySequence, err := incrementSQLiteUint(
			uint64(len(run.History)),
			"History sequence",
		)
		if err != nil {
			return CommitModelDispatchOutcomeResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO history_entries(
				run_id,
				history_sequence,
				member_id,
				role,
				content_ref,
				content_digest,
				source_attempt_id,
				created_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		`,
			run.RunID,
			int64(historySequence),
			run.Member.MemberID,
			string(moduleapi.ModelRoleAssistant),
			merged.Attempt.ResultRef,
			merged.Attempt.ResultRef,
			current.Attempt.AttemptID,
			createdAt,
		); err != nil {
			return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
				"%w: append assistant History: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
	}

	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET
			state=?,
			disposition=?,
			revision=?,
			updated_at=?
		WHERE run_id=?
		  AND revision=?
	`,
		nextStep,
		nextDisposition,
		int64(nextRunRevision),
		createdAt,
		run.RunID,
		int64(run.RunRevision),
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: update model outcome Run: %w",
			err,
		)
	}
	if err := requireModelCASRow(
		runUpdate,
		"commit model outcome Run",
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=?,
			step=?,
			budget_state_ref=?,
			continuation=?,
			pending_attempt_id=?,
			waiting_reason=?,
			last_authoritative_event=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND step=?
		  AND pending_attempt_id=?
		  AND last_authoritative_event=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND lease_expiry>?
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		nextStep,
		nextBudgetRef,
		continuation,
		nullableModelString(pendingAttempt),
		nullableModelString(waitingReason),
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		run.Frame.Step,
		current.Attempt.AttemptID,
		int64(run.Frame.LastAuthoritativeEvent),
		input.Lease.OwnerID,
		int64(input.Lease.LeaseEpoch),
		createdAt,
		int64(nextRunRevision),
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: update model outcome Frame: %w",
			err,
		)
	}
	if err := requireModelCASRow(
		frameUpdate,
		"commit model outcome Frame",
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id,
			event_sequence,
			event_kind,
			from_revision,
			to_revision,
			payload_ref,
			payload_digest,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		corecontract.ModelDispatchTerminalEventKind,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: append model outcome RunEvent: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := appendModelOutcomeRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.AttemptID, overviewTransitionModelOutcomeV1,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}

	stored, err := queryModelDispatchRecord(
		ctx,
		connection,
		input.AttemptID,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if stored.Attempt.Revision != nextAttemptRevision ||
		stored.Usage.Revision != nextUsageRevision {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: post-update Attempt or Usage revision",
			ErrModelDispatchIntegrity,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"currentstore: commit model outcome: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return CommitModelDispatchOutcomeResult{
		Record:  cloneModelDispatchRecord(stored),
		Lease:   nextLease,
		Applied: true,
	}, nil
}

// GetModelDispatchRecord reads one coherent, content-verified Attempt and
// Usage pair. It does not expose a SQL handle or permit arbitrary queries.
func (store *Store) GetModelDispatchRecord(
	ctx context.Context,
	attemptID string,
) (ModelDispatchRecord, error) {
	if ctx == nil {
		return ModelDispatchRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelDispatch,
		)
	}
	if !validLeaseOpaqueID(attemptID) {
		return ModelDispatchRecord{}, fmt.Errorf(
			"%w: invalid Attempt ID",
			ErrInvalidModelDispatch,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModelDispatchRecord{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModelDispatchRecord{}, fmt.Errorf(
			"currentstore: acquire model record connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModelDispatchRecord{}, fmt.Errorf(
			"currentstore: begin GetModelDispatchRecord: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	record, err := queryModelDispatchRecord(ctx, connection, attemptID)
	if err != nil {
		return ModelDispatchRecord{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModelDispatchRecord{}, fmt.Errorf(
			"currentstore: commit GetModelDispatchRecord: %w",
			err,
		)
	}
	committed = true
	return cloneModelDispatchRecord(record), nil
}

// ScanUnsettledModelDispatchRecords returns only PENDING and MODEL_UNKNOWN
// Attempts for one Run in deterministic creation order.
func (store *Store) ScanUnsettledModelDispatchRecords(
	ctx context.Context,
	runID string,
) ([]ModelDispatchRecord, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelDispatch,
		)
	}
	if !validLeaseOpaqueID(runID) {
		return nil, fmt.Errorf(
			"%w: invalid Run ID",
			ErrInvalidModelDispatch,
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
			"currentstore: acquire model scan connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: begin model Attempt scan: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM model_dispatch_attempts
		WHERE run_id=?
		  AND state IN ('PENDING', 'MODEL_UNKNOWN')
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: scan unsettled model Attempts: %w",
			err,
		)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"currentstore: scan unsettled Attempt ID: %w",
				err,
			)
		}
		ids = append(ids, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf(
			"currentstore: scan unsettled Attempt rows: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: close unsettled Attempt rows: %w",
			err,
		)
	}
	records := make([]ModelDispatchRecord, 0, len(ids))
	for _, attemptID := range ids {
		record, err := queryModelDispatchRecord(
			ctx,
			connection,
			attemptID,
		)
		if err != nil {
			return nil, err
		}
		if record.Attempt.RunID != runID {
			return nil, fmt.Errorf(
				"%w: scanned Attempt crossed Run identity",
				ErrModelDispatchIntegrity,
			)
		}
		records = append(records, cloneModelDispatchRecord(record))
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: commit model Attempt scan: %w",
			err,
		)
	}
	committed = true
	return records, nil
}

func prepareModelDispatchOutcome(
	input CommitModelDispatchOutcomeInput,
) (preparedModelDispatchOutcome, error) {
	prepared := preparedModelDispatchOutcome{
		state:               input.State,
		providerRequestID:   input.ProviderRequestID,
		errorClassification: input.ErrorClassification,
		unknownReason:       input.UnknownReason,
	}
	if input.State != corecontract.ModelAttemptSucceeded &&
		input.State != corecontract.ModelAttemptFailed &&
		input.State != corecontract.ModelAttemptUnknown {
		return preparedModelDispatchOutcome{}, fmt.Errorf(
			"%w: outcome state must be SUCCEEDED, FAILED, or MODEL_UNKNOWN",
			ErrInvalidModelDispatch,
		)
	}
	if prepared.providerRequestID != "" &&
		!validLeaseOpaqueID(prepared.providerRequestID) {
		return preparedModelDispatchOutcome{}, fmt.Errorf(
			"%w: invalid provider request ID",
			ErrInvalidModelDispatch,
		)
	}
	if prepared.errorClassification != "" &&
		!validModelOutcomeLabel(prepared.errorClassification) {
		return preparedModelDispatchOutcome{}, fmt.Errorf(
			"%w: invalid error classification",
			ErrInvalidModelDispatch,
		)
	}
	if prepared.unknownReason != "" &&
		!validModelOutcomeReason(prepared.unknownReason) {
		return preparedModelDispatchOutcome{}, fmt.Errorf(
			"%w: invalid unknown reason",
			ErrInvalidModelDispatch,
		)
	}

	if len(input.OutputCanonical) != 0 {
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			input.OutputCanonical,
		)
		if err != nil {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: model output: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
		if prepared.providerRequestID != "" &&
			output.ProviderRequestID != "" &&
			prepared.providerRequestID != output.ProviderRequestID {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: provider request IDs disagree",
				ErrInvalidModelDispatch,
			)
		}
		if prepared.providerRequestID == "" {
			prepared.providerRequestID = output.ProviderRequestID
		}
		digest, err := ComputeContentDigest(
			ContentModelResult,
			admissionJSONMediaType,
			input.OutputCanonical,
		)
		if err != nil {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: model result content: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
		prepared.output = output
		prepared.outputCanonical = bytes.Clone(input.OutputCanonical)
		prepared.resultContent = &preparedAdmissionContent{
			Digest:         digest,
			Kind:           ContentModelResult,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(input.OutputCanonical),
		}
	}
	if len(input.UsageReceiptCanonical) != 0 {
		usage, err := moduleapi.RestoreModelUsageReceiptV1(
			input.UsageReceiptCanonical,
		)
		if err != nil {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: model Usage receipt: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
		digest, err := ComputeContentDigest(
			ContentProviderReceipt,
			admissionJSONMediaType,
			input.UsageReceiptCanonical,
		)
		if err != nil {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: provider receipt content: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
		prepared.usage = usage
		prepared.usageCanonical =
			bytes.Clone(input.UsageReceiptCanonical)
		prepared.receiptContent = &preparedAdmissionContent{
			Digest:         digest,
			Kind:           ContentProviderReceipt,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(input.UsageReceiptCanonical),
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
		if err != nil ||
			!bytes.Equal(
				canonical,
				input.ReconciliationEvidenceCanonical,
			) {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: reconciliation evidence is not canonical JSON",
				ErrInvalidModelDispatch,
			)
		}
		digest, err := ComputeContentDigest(
			ContentReconciliationEvidence,
			admissionJSONMediaType,
			canonical,
		)
		if err != nil {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: reconciliation evidence content: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
		prepared.evidenceCanonical = bytes.Clone(canonical)
		prepared.evidenceContent = &preparedAdmissionContent{
			Digest:         digest,
			Kind:           ContentReconciliationEvidence,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(canonical),
		}
	}

	switch input.State {
	case corecontract.ModelAttemptSucceeded:
		if prepared.resultContent == nil ||
			prepared.errorClassification != "" ||
			prepared.unknownReason != "" {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: SUCCEEDED requires output and forbids error/unknown reason",
				ErrInvalidModelDispatch,
			)
		}
	case corecontract.ModelAttemptFailed:
		if prepared.resultContent != nil ||
			prepared.errorClassification == "" ||
			prepared.unknownReason != "" {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: FAILED requires error classification and no output/unknown reason",
				ErrInvalidModelDispatch,
			)
		}
	case corecontract.ModelAttemptUnknown:
		if prepared.resultContent != nil ||
			prepared.errorClassification != "" ||
			(prepared.providerRequestID == "" &&
				prepared.receiptContent == nil &&
				prepared.evidenceContent == nil &&
				prepared.unknownReason == "") {
			return preparedModelDispatchOutcome{}, fmt.Errorf(
				"%w: MODEL_UNKNOWN requires a receipt, request ID, evidence, or reason",
				ErrInvalidModelDispatch,
			)
		}
	}
	return prepared, nil
}

func commitIdempotentModelOutcome(
	ctx context.Context,
	connection *sql.Conn,
	input CommitModelDispatchOutcomeInput,
	prepared preparedModelDispatchOutcome,
	current ModelDispatchRecord,
) (CommitModelDispatchOutcomeResult, error) {
	if input.ExpectedAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedAttemptRevision == math.MaxUint64 ||
			input.ExpectedAttemptRevision+1 != current.Attempt.Revision) {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent Attempt revision is not exact or one transition behind",
			ErrModelDispatchConflict,
		)
	}
	if err := requireSameModelOutcome(current, prepared); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if err := validateFrozenModelEstimatedCost(
		ctx,
		connection,
		current,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent estimated cost: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	currentLease, err := loadCurrentModelOutcomeLease(
		ctx,
		connection,
		input.Lease,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	exact := input.Lease.RunRevision == currentLease.RunRevision &&
		input.Lease.FrameRevision == currentLease.FrameRevision
	oneOutcomeBehind := input.Lease.RunRevision < math.MaxUint64 &&
		input.Lease.FrameRevision < math.MaxUint64 &&
		input.Lease.RunRevision+1 == currentLease.RunRevision &&
		input.Lease.FrameRevision+1 == currentLease.FrameRevision
	if !exact && !oneOutcomeBehind {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent lease revisions are not exact or one outcome behind",
			ErrModelDispatchConflict,
		)
	}
	run, err := loadRunForModelOutcome(ctx, connection, currentLease)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	workspaceRequest, err := loadWorkspaceTransferRequestV1(
		ctx,
		connection,
		run,
	)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent Workspace transfer request: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	run, err = runWithWorkspaceTransferRecordV1(run, workspaceRequest)
	if err != nil {
		return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
			"%w: idempotent Workspace transfer recovery contents: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := validateAttemptAgainstOutcomeRun(current, run); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if err := validateModelOutcomeFinalState(prepared.state, run); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if _, err := validateModelUsageLedgerHead(
		ctx,
		connection,
		run.RunID,
		run.Frame.BudgetStateRef,
	); err != nil {
		return CommitModelDispatchOutcomeResult{}, err
	}
	if prepared.state == corecontract.ModelAttemptSucceeded {
		if err := verifyIdempotentSuccessfulModelOutcomeMemory(
			ctx,
			connection,
			run,
			current.Attempt,
			prepared.output.AssistantText,
		); err != nil {
			return CommitModelDispatchOutcomeResult{}, err
		}
		if run.WorkspaceTransfer != nil {
			payload, err := queryContent(
				ctx,
				connection,
				current.Attempt.ResultRef,
			)
			if err != nil {
				return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
					"%w: idempotent Workspace transfer result payload: %v",
					ErrModelDispatchIntegrity,
					err,
				)
			}
			if _, err := loadWorkspaceTransferResultV1(
				ctx,
				connection,
				run,
				payload,
			); err != nil {
				return CommitModelDispatchOutcomeResult{}, fmt.Errorf(
					"%w: idempotent Workspace transfer result: %v",
					ErrModelDispatchIntegrity,
					err,
				)
			}
		}
	}
	return CommitModelDispatchOutcomeResult{
		Record:  cloneModelDispatchRecord(current),
		Lease:   currentLease,
		Applied: false,
	}, nil
}

func loadRunForModelOutcome(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
) (RunForLoop, error) {
	run, err := loadRunForLoop(ctx, connection, lease)
	if err == nil {
		return run, nil
	}
	if errors.Is(err, ErrRunLeaseConflict) ||
		errors.Is(err, ErrRunLeaseUnavailable) {
		return RunForLoop{}, fmt.Errorf(
			"%w: %w",
			ErrModelDispatchConflict,
			err,
		)
	}
	return RunForLoop{}, err
}

func validateAttemptAgainstOutcomeRun(
	record ModelDispatchRecord,
	run RunForLoop,
) error {
	attempt := record.Attempt
	if attempt.RunID != run.RunID ||
		record.Usage.RunID != run.RunID ||
		attempt.MemberID != run.Member.MemberID ||
		attempt.MemberSnapshotDigest !=
			run.Member.MemberSnapshotDigest {
		return fmt.Errorf(
			"%w: Attempt/Usage/Frame recovery identities differ",
			ErrModelDispatchIntegrity,
		)
	}
	if (attempt.State == corecontract.ModelAttemptSucceeded ||
		attempt.State == corecontract.ModelAttemptFailed) &&
		run.Frame.PendingAttemptID != "" {
		return fmt.Errorf(
			"%w: terminal Attempt remains pending in Frame",
			ErrModelDispatchIntegrity,
		)
	}
	if (attempt.State == corecontract.ModelAttemptPending ||
		attempt.State == corecontract.ModelAttemptUnknown) &&
		run.Frame.PendingAttemptID != attempt.AttemptID {
		return fmt.Errorf(
			"%w: unsettled Attempt is not pending in Frame",
			ErrModelDispatchIntegrity,
		)
	}
	binding, err := exactModelBinding(run.Member)
	if err != nil {
		return err
	}
	canonical, err := canonicalModelBinding(binding)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, attempt.BindingCanonical) {
		return fmt.Errorf(
			"%w: Attempt binding differs from frozen Member",
			ErrModelDispatchIntegrity,
		)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil ||
		continuation.AttemptID != attempt.AttemptID ||
		continuation.LogicalStepID != attempt.LogicalStepID {
		return fmt.Errorf(
			"%w: Attempt differs from frozen continuation",
			ErrModelDispatchIntegrity,
		)
	}
	return nil
}

func validateModelOutcomeSourceState(
	state corecontract.ModelAttemptState,
	run RunForLoop,
) error {
	switch state {
	case corecontract.ModelAttemptPending:
		if run.Frame.Step != corecontract.ModelPendingLoopStep ||
			run.State != corecontract.InitialRunState ||
			run.Disposition != "" {
			return fmt.Errorf(
				"%w: PENDING Attempt has inconsistent Run/Frame state",
				ErrModelDispatchIntegrity,
			)
		}
	case corecontract.ModelAttemptUnknown:
		if run.Frame.Step !=
			corecontract.WaitingReconciliationLoopStep ||
			run.State !=
				corecontract.WaitingReconciliationLoopStep ||
			run.Disposition !=
				corecontract.WaitingReconciliationLoopStep {
			return fmt.Errorf(
				"%w: MODEL_UNKNOWN Attempt has inconsistent Run/Frame state",
				ErrModelDispatchIntegrity,
			)
		}
	default:
		return fmt.Errorf(
			"%w: source Attempt is already terminal",
			ErrModelDispatchConflict,
		)
	}
	return nil
}

func validateModelOutcomeFinalState(
	state corecontract.ModelAttemptState,
	run RunForLoop,
) error {
	switch state {
	case corecontract.ModelAttemptSucceeded,
		corecontract.ModelAttemptFailed:
		if run.Frame.Step != corecontract.TerminatedLoopStep ||
			run.Frame.PendingAttemptID != "" ||
			run.State != corecontract.TerminatedLoopStep ||
			run.Disposition != corecontract.TerminatedLoopStep {
			return fmt.Errorf(
				"%w: terminal Attempt has inconsistent Run/Frame state",
				ErrModelDispatchIntegrity,
			)
		}
	case corecontract.ModelAttemptUnknown:
		if run.Frame.Step !=
			corecontract.WaitingReconciliationLoopStep ||
			run.Frame.PendingAttemptID == "" ||
			run.State !=
				corecontract.WaitingReconciliationLoopStep ||
			run.Disposition !=
				corecontract.WaitingReconciliationLoopStep {
			return fmt.Errorf(
				"%w: MODEL_UNKNOWN Attempt has inconsistent Run/Frame state",
				ErrModelDispatchIntegrity,
			)
		}
	default:
		return fmt.Errorf(
			"%w: invalid final Attempt state",
			ErrModelDispatchIntegrity,
		)
	}
	return nil
}

func modelOutcomeFrameProjection(
	state corecontract.ModelAttemptState,
	attemptID string,
) (string, string, string, string) {
	if state == corecontract.ModelAttemptUnknown {
		return corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			attemptID,
			modelUnknownWaitingReason
	}
	return corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		"",
		""
}

func prepareModelOutcomeEvent(
	attempt ModelDispatchAttemptRecord,
	state corecontract.ModelAttemptState,
	resultDigest string,
	usage ModelUsageRecord,
	origin corecontract.DispatchTransitionOriginV1,
	resourceSemanticDigest string,
) ([]byte, string, error) {
	usageEvent, err := modelUsageEventV1(usage)
	if err != nil {
		return nil, "", err
	}
	_, canonical, err := corecontract.NewModelDispatchEventV1(
		corecontract.ModelDispatchEventV1{
			SchemaVersion:          corecontract.ModelDispatchEventSchemaVersionV1,
			RunID:                  attempt.RunID,
			AttemptID:              attempt.AttemptID,
			LogicalStepID:          attempt.LogicalStepID,
			LogicalOperationKey:    attempt.LogicalOperationKey,
			RequestDigest:          attempt.Request.Digest,
			State:                  state,
			ResultDigest:           resultDigest,
			TransitionOrigin:       origin,
			Usage:                  usageEvent,
			ResourceSemanticDigest: resourceSemanticDigest,
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: model outcome event: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	digest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: model outcome event content: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	return bytes.Clone(canonical), digest, nil
}

func mergeModelOutcomeFacts(
	current ModelDispatchRecord,
	prepared preparedModelDispatchOutcome,
) (ModelDispatchRecord, error) {
	merged := cloneModelDispatchRecord(current)
	merged.Attempt.State = prepared.state
	providerRequestID, err := mergeModelStringFact(
		current.Attempt.ProviderRequestID,
		prepared.providerRequestID,
		"provider request ID",
	)
	if err != nil {
		return ModelDispatchRecord{}, err
	}
	merged.Attempt.ProviderRequestID = providerRequestID
	if prepared.receiptContent != nil {
		if current.Usage.RawReceiptRef != "" &&
			current.Usage.RawReceiptRef !=
				prepared.receiptContent.Digest {
			return ModelDispatchRecord{}, fmt.Errorf(
				"%w: provider receipt cannot replace a previously known raw receipt",
				ErrModelDispatchConflict,
			)
		}
		if err := requireReceiptRetainsKnownFacts(
			current.Usage,
			prepared.usage,
		); err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Attempt.ProviderReceiptRef =
			prepared.receiptContent.Digest
		merged.Usage.RawReceiptRef = prepared.receiptContent.Digest
	}
	if prepared.evidenceContent != nil {
		merged.Attempt.ReconciliationEvidenceRef =
			prepared.evidenceContent.Digest
	}
	if prepared.receiptContent != nil {
		merged.Usage.Tokens.Input, err = mergeModelUintFact(
			current.Usage.Tokens.Input,
			prepared.usage.InputTokens,
			"input tokens",
		)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Usage.Tokens.CachedInput, err = mergeModelUintFact(
			current.Usage.Tokens.CachedInput,
			prepared.usage.CachedInputTokens,
			"cached input tokens",
		)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Usage.Tokens.UncachedInput, err = mergeModelUintFact(
			current.Usage.Tokens.UncachedInput,
			prepared.usage.UncachedInputTokens,
			"uncached input tokens",
		)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Usage.Tokens.Output, err = mergeModelUintFact(
			current.Usage.Tokens.Output,
			prepared.usage.OutputTokens,
			"output tokens",
		)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Usage.Tokens.Reasoning, err = mergeModelUintFact(
			current.Usage.Tokens.Reasoning,
			prepared.usage.ReasoningTokens,
			"reasoning tokens",
		)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
		merged.Usage.ProviderReportedCost, err =
			mergeModelCostFact(
				current.Usage.ProviderReportedCost,
				prepared.usage.ProviderReportedCost,
				"provider reported cost",
			)
		if err != nil {
			return ModelDispatchRecord{}, err
		}
	}
	if err := merged.Usage.Tokens.Validate(); err != nil {
		return ModelDispatchRecord{}, fmt.Errorf(
			"%w: merged Usage: %v",
			ErrModelDispatchConflict,
			err,
		)
	}

	switch prepared.state {
	case corecontract.ModelAttemptSucceeded:
		merged.Attempt.ResultRef = prepared.resultContent.Digest
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason = ""
	case corecontract.ModelAttemptFailed:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification =
			prepared.errorClassification
		merged.Attempt.UnknownReason = ""
	case corecontract.ModelAttemptUnknown:
		merged.Attempt.ResultRef = ""
		merged.Attempt.ErrorClassification = ""
		merged.Attempt.UnknownReason = prepared.unknownReason
	}
	if prepared.state == corecontract.ModelAttemptUnknown {
		merged.Usage.ReconciliationStatus =
			modelUsageStatusReconciliation
	} else if merged.Usage.RawReceiptRef != "" {
		merged.Usage.ReconciliationStatus = modelUsageStatusReported
	} else {
		merged.Usage.ReconciliationStatus = modelUsageStatusNoReport
	}
	return merged, nil
}

func requireReceiptRetainsKnownFacts(
	current ModelUsageRecord,
	incoming moduleapi.ModelUsageReceiptV1,
) error {
	for _, fact := range []struct {
		name     string
		current  *uint64
		incoming *uint64
	}{
		{"input tokens", current.Tokens.Input, incoming.InputTokens},
		{
			"cached input tokens",
			current.Tokens.CachedInput,
			incoming.CachedInputTokens,
		},
		{
			"uncached input tokens",
			current.Tokens.UncachedInput,
			incoming.UncachedInputTokens,
		},
		{"output tokens", current.Tokens.Output, incoming.OutputTokens},
		{
			"reasoning tokens",
			current.Tokens.Reasoning,
			incoming.ReasoningTokens,
		},
	} {
		if fact.current != nil &&
			(fact.incoming == nil ||
				*fact.current != *fact.incoming) {
			return fmt.Errorf(
				"%w: new receipt does not retain known %s",
				ErrModelDispatchConflict,
				fact.name,
			)
		}
	}
	if current.ProviderReportedCost != nil &&
		(incoming.ProviderReportedCost == nil ||
			*current.ProviderReportedCost !=
				*incoming.ProviderReportedCost) {
		return fmt.Errorf(
			"%w: new receipt does not retain known provider cost",
			ErrModelDispatchConflict,
		)
	}
	return nil
}

func requireSameModelOutcome(
	current ModelDispatchRecord,
	prepared preparedModelDispatchOutcome,
) error {
	attempt := current.Attempt
	if attempt.State != prepared.state {
		return fmt.Errorf(
			"%w: persisted outcome state differs",
			ErrModelDispatchConflict,
		)
	}
	if prepared.providerRequestID != "" &&
		attempt.ProviderRequestID != prepared.providerRequestID {
		return fmt.Errorf(
			"%w: persisted provider request ID differs",
			ErrModelDispatchConflict,
		)
	}
	if prepared.resultContent != nil &&
		attempt.ResultRef != prepared.resultContent.Digest {
		return fmt.Errorf(
			"%w: persisted model result differs",
			ErrModelDispatchConflict,
		)
	}
	if prepared.state == corecontract.ModelAttemptSucceeded &&
		prepared.resultContent == nil {
		return fmt.Errorf(
			"%w: successful replay lacks the exact model result",
			ErrModelDispatchConflict,
		)
	}
	if attempt.ErrorClassification != prepared.errorClassification ||
		attempt.UnknownReason != prepared.unknownReason {
		return fmt.Errorf(
			"%w: persisted error or unknown reason differs",
			ErrModelDispatchConflict,
		)
	}
	if prepared.receiptContent != nil {
		if attempt.ProviderReceiptRef !=
			prepared.receiptContent.Digest ||
			current.Usage.RawReceiptRef !=
				prepared.receiptContent.Digest ||
			!sameUsageReceiptFacts(current.Usage, prepared.usage) {
			return fmt.Errorf(
				"%w: persisted provider receipt or Usage differs",
				ErrModelDispatchConflict,
			)
		}
	} else if prepared.state == corecontract.ModelAttemptUnknown &&
		(attempt.ProviderReceiptRef != "" ||
			current.Usage.RawReceiptRef != "") {
		return fmt.Errorf(
			"%w: persisted UNKNOWN receipt differs",
			ErrModelDispatchConflict,
		)
	}
	if prepared.evidenceContent != nil {
		if attempt.ReconciliationEvidenceRef !=
			prepared.evidenceContent.Digest {
			return fmt.Errorf(
				"%w: persisted reconciliation evidence differs",
				ErrModelDispatchConflict,
			)
		}
	} else if prepared.state == corecontract.ModelAttemptUnknown &&
		attempt.ReconciliationEvidenceRef != "" {
		return fmt.Errorf(
			"%w: persisted UNKNOWN evidence differs",
			ErrModelDispatchConflict,
		)
	}
	return nil
}

func sameUsageReceiptFacts(
	record ModelUsageRecord,
	receipt moduleapi.ModelUsageReceiptV1,
) bool {
	return equalModelUint(record.Tokens.Input, receipt.InputTokens) &&
		equalModelUint(
			record.Tokens.CachedInput,
			receipt.CachedInputTokens,
		) &&
		equalModelUint(
			record.Tokens.UncachedInput,
			receipt.UncachedInputTokens,
		) &&
		equalModelUint(record.Tokens.Output, receipt.OutputTokens) &&
		equalModelUint(
			record.Tokens.Reasoning,
			receipt.ReasoningTokens,
		) &&
		equalModelStringPointer(
			record.ProviderReportedCost,
			receipt.ProviderReportedCost,
		)
}

func queryModelDispatchRecord(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attemptID string,
) (ModelDispatchRecord, error) {
	attempt, err := queryModelDispatchAttempt(ctx, queryer, attemptID)
	if err != nil {
		return ModelDispatchRecord{}, err
	}
	usage, err := queryModelUsage(ctx, queryer, attemptID)
	if err != nil {
		return ModelDispatchRecord{}, err
	}
	record := ModelDispatchRecord{Attempt: attempt, Usage: usage}
	if err := validateModelDispatchRecord(ctx, queryer, record); err != nil {
		return ModelDispatchRecord{}, err
	}
	return record, nil
}

// queryModelUsage is package-internal so Loop recovery can validate Attempt,
// Usage and BudgetStateRef in one SQLite read transaction.
func queryModelUsage(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attemptID string,
) (ModelUsageRecord, error) {
	var (
		record               ModelUsageRecord
		ledgerSequence       sql.NullInt64
		revision             int64
		inputTokens          sql.NullInt64
		cachedInputTokens    sql.NullInt64
		uncachedInputTokens  sql.NullInt64
		outputTokens         sql.NullInt64
		reasoningTokens      sql.NullInt64
		estimatedCost        sql.NullString
		providerReportedCost sql.NullString
		reconciledCost       sql.NullString
		rawReceiptRef        sql.NullString
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			attempt_id,
			run_id,
			ledger_sequence,
			revision,
			input_tokens,
			cached_input_tokens,
			uncached_input_tokens,
			output_tokens,
			reasoning_tokens,
			estimated_cost,
			provider_reported_cost,
			reconciled_cost,
			reconciliation_status,
			raw_receipt_ref
		FROM model_usage
		WHERE attempt_id=?
	`, attemptID).Scan(
		&record.AttemptID,
		&record.RunID,
		&ledgerSequence,
		&revision,
		&inputTokens,
		&cachedInputTokens,
		&uncachedInputTokens,
		&outputTokens,
		&reasoningTokens,
		&estimatedCost,
		&providerReportedCost,
		&reconciledCost,
		&record.ReconciliationStatus,
		&rawReceiptRef,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelUsageRecord{}, fmt.Errorf(
			"%w: Usage for Attempt %q",
			ErrModelDispatchIntegrity,
			attemptID,
		)
	}
	if err != nil {
		return ModelUsageRecord{}, fmt.Errorf(
			"currentstore: query model Usage: %w",
			err,
		)
	}
	if revision < 0 ||
		(ledgerSequence.Valid && ledgerSequence.Int64 <= 0) {
		return ModelUsageRecord{}, modelAttemptIntegrity(
			attemptID,
			"Usage revision or ledger sequence",
		)
	}
	record.Revision = uint64(revision)
	record.LedgerSequence = modelUintFromNull(ledgerSequence)
	record.Tokens = corecontract.UsageTokens{
		Input:         modelUintFromNull(inputTokens),
		CachedInput:   modelUintFromNull(cachedInputTokens),
		UncachedInput: modelUintFromNull(uncachedInputTokens),
		Output:        modelUintFromNull(outputTokens),
		Reasoning:     modelUintFromNull(reasoningTokens),
	}
	record.EstimatedCost = modelStringFromNull(estimatedCost)
	record.ProviderReportedCost =
		modelStringFromNull(providerReportedCost)
	record.ReconciledCost = modelStringFromNull(reconciledCost)
	record.RawReceiptRef = rawReceiptRef.String
	if err := record.Tokens.Validate(); err != nil {
		return ModelUsageRecord{}, modelAttemptIntegrity(
			attemptID,
			"Usage token facts",
		)
	}
	if strings.TrimSpace(record.ReconciliationStatus) == "" {
		return ModelUsageRecord{}, modelAttemptIntegrity(
			attemptID,
			"Usage reconciliation status",
		)
	}
	return record, nil
}

func validateModelDispatchRecord(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	record ModelDispatchRecord,
) error {
	attempt := record.Attempt
	usage := record.Usage
	if usage.AttemptID != attempt.AttemptID ||
		usage.RunID != attempt.RunID ||
		attempt.ProviderReceiptRef != usage.RawReceiptRef {
		return modelAttemptIntegrity(
			attempt.AttemptID,
			"Attempt/Usage identity or receipt ref",
		)
	}
	if attempt.ResultRef != "" {
		content, err := queryContent(ctx, queryer, attempt.ResultRef)
		if err != nil ||
			content.Kind != ContentModelResult {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"result content",
			)
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			content.CanonicalBytes,
		)
		if err != nil ||
			(output.ProviderRequestID != "" &&
				attempt.ProviderRequestID != "" &&
				output.ProviderRequestID !=
					attempt.ProviderRequestID) {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"result projection",
			)
		}
	}
	if attempt.ProviderReceiptRef != "" {
		content, err := queryContent(
			ctx,
			queryer,
			attempt.ProviderReceiptRef,
		)
		if err != nil ||
			content.Kind != ContentProviderReceipt {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"provider receipt content",
			)
		}
		receipt, err := moduleapi.RestoreModelUsageReceiptV1(
			content.CanonicalBytes,
		)
		if err != nil || !sameUsageReceiptFacts(usage, receipt) {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"provider receipt projection",
			)
		}
	}
	if attempt.ReconciliationEvidenceRef != "" {
		content, err := queryContent(
			ctx,
			queryer,
			attempt.ReconciliationEvidenceRef,
		)
		if err != nil ||
			content.Kind != ContentReconciliationEvidence {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"reconciliation evidence",
			)
		}
	}
	hasBillableFact := modelUsageHasBillableFact(usage)
	if hasBillableFact != (usage.LedgerSequence != nil) {
		return modelAttemptIntegrity(
			attempt.AttemptID,
			"Usage ledger allocation",
		)
	}
	switch attempt.State {
	case corecontract.ModelAttemptPending:
		if attempt.ProviderRequestID != "" ||
			attempt.ProviderReceiptRef != "" ||
			attempt.ResultRef != "" ||
			attempt.ErrorClassification != "" ||
			attempt.ReconciliationEvidenceRef != "" ||
			attempt.UnknownReason != "" ||
			usage.Revision != 0 ||
			usage.LedgerSequence != nil ||
			usage.RawReceiptRef != "" ||
			modelUsageHasAnyFact(usage) ||
			usage.ReconciliationStatus != modelUsageStatusPending {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"PENDING closure",
			)
		}
	case corecontract.ModelAttemptSucceeded:
		if attempt.ResultRef == "" ||
			attempt.ErrorClassification != "" ||
			attempt.UnknownReason != "" ||
			usage.ReconciliationStatus !=
				expectedTerminalUsageStatus(usage) {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"SUCCEEDED closure",
			)
		}
	case corecontract.ModelAttemptFailed:
		if attempt.ResultRef != "" ||
			attempt.ErrorClassification == "" ||
			attempt.UnknownReason != "" ||
			usage.ReconciliationStatus !=
				expectedTerminalUsageStatus(usage) {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"FAILED closure",
			)
		}
	case corecontract.ModelAttemptUnknown:
		if attempt.ResultRef != "" ||
			attempt.ErrorClassification != "" ||
			(attempt.ProviderRequestID == "" &&
				attempt.ProviderReceiptRef == "" &&
				attempt.ReconciliationEvidenceRef == "" &&
				attempt.UnknownReason == "") ||
			usage.ReconciliationStatus !=
				modelUsageStatusReconciliation {
			return modelAttemptIntegrity(
				attempt.AttemptID,
				"MODEL_UNKNOWN closure",
			)
		}
	}
	return nil
}

func expectedTerminalUsageStatus(usage ModelUsageRecord) string {
	if usage.RawReceiptRef != "" {
		return modelUsageStatusReported
	}
	return modelUsageStatusNoReport
}

func validateModelUsageLedgerHead(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	budgetStateRef string,
) (uint64, error) {
	referencedHead, err := corecontract.ParseBudgetStateRefV1(
		budgetStateRef,
		runID,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"%w: invalid BudgetStateRef: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	var (
		count   int64
		maximum sql.NullInt64
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(ledger_sequence)
		FROM model_usage
		WHERE run_id=? AND ledger_sequence IS NOT NULL
	`, runID).Scan(&count, &maximum); err != nil {
		return 0, fmt.Errorf(
			"currentstore: query Usage ledger head: %w",
			err,
		)
	}
	var head uint64
	if count == 0 {
		if maximum.Valid {
			return 0, fmt.Errorf(
				"%w: empty Usage ledger has a maximum",
				ErrModelDispatchIntegrity,
			)
		}
	} else {
		if !maximum.Valid ||
			maximum.Int64 <= 0 ||
			count != maximum.Int64 {
			return 0, fmt.Errorf(
				"%w: Usage ledger sequence is not contiguous",
				ErrModelDispatchIntegrity,
			)
		}
		head = uint64(maximum.Int64)
	}
	if referencedHead != head {
		return 0, fmt.Errorf(
			"%w: BudgetStateRef does not match Usage ledger head",
			ErrModelDispatchIntegrity,
		)
	}
	return head, nil
}

func loadCurrentModelOutcomeLease(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	input RunLease,
) (RunLease, error) {
	var (
		runRevision   int64
		frameRevision int64
		owner         sql.NullString
		epoch         int64
		expiry        sql.NullInt64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			runs.revision,
			loop_frames.frame_revision,
			loop_frames.lease_owner,
			loop_frames.lease_epoch,
			loop_frames.lease_expiry
		FROM runs
		JOIN loop_frames ON loop_frames.run_id=runs.run_id
		WHERE runs.run_id=?
	`, input.RunID).Scan(
		&runRevision,
		&frameRevision,
		&owner,
		&epoch,
		&expiry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RunLease{}, fmt.Errorf(
			"%w: Run does not exist",
			ErrModelDispatchConflict,
		)
	}
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: load idempotent model outcome lease: %w",
			err,
		)
	}
	if runRevision < 0 ||
		frameRevision < 0 ||
		!owner.Valid ||
		owner.String != input.OwnerID ||
		epoch <= 0 ||
		uint64(epoch) != input.LeaseEpoch ||
		!expiry.Valid ||
		expiry.Int64 <= nowUnixMicro() {
		return RunLease{}, fmt.Errorf(
			"%w: idempotent outcome no longer owns the exact lease epoch",
			ErrModelDispatchConflict,
		)
	}
	return newRunLease(
		input.RunID,
		owner.String,
		epoch,
		runRevision,
		frameRevision,
		expiry.Int64,
	)
}

func mergeModelStringFact(
	current string,
	incoming string,
	name string,
) (string, error) {
	if incoming == "" {
		return current, nil
	}
	if current != "" && current != incoming {
		return "", fmt.Errorf(
			"%w: %s would overwrite a known fact",
			ErrModelDispatchConflict,
			name,
		)
	}
	return incoming, nil
}

func mergeModelUintFact(
	current *uint64,
	incoming *uint64,
	name string,
) (*uint64, error) {
	if incoming == nil {
		return cloneModelUint(current), nil
	}
	if current != nil && *current != *incoming {
		return nil, fmt.Errorf(
			"%w: %s would overwrite a known fact",
			ErrModelDispatchConflict,
			name,
		)
	}
	return cloneModelUint(incoming), nil
}

func mergeModelCostFact(
	current *string,
	incoming *string,
	name string,
) (*string, error) {
	if incoming == nil {
		return cloneModelString(current), nil
	}
	if current != nil && *current != *incoming {
		return nil, fmt.Errorf(
			"%w: %s would overwrite a known fact",
			ErrModelDispatchConflict,
			name,
		)
	}
	return cloneModelString(incoming), nil
}

func modelUsageHasBillableFact(usage ModelUsageRecord) bool {
	return usage.Tokens.Input != nil ||
		usage.Tokens.CachedInput != nil ||
		usage.Tokens.UncachedInput != nil ||
		usage.Tokens.Output != nil ||
		usage.Tokens.Reasoning != nil ||
		usage.ProviderReportedCost != nil ||
		usage.EstimatedCost != nil ||
		usage.ReconciledCost != nil
}

func modelUsageHasAnyFact(usage ModelUsageRecord) bool {
	return modelUsageHasBillableFact(usage)
}

func validModelOutcomeLabel(value string) bool {
	return validLeaseOpaqueID(value)
}

func validModelOutcomeReason(value string) bool {
	if value == "" ||
		len(value) > moduleapi.MaxTextBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) &&
			character != '\n' &&
			character != '\t' {
			return false
		}
	}
	return true
}

func nullableModelString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableModelStringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableModelUint(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func modelUintFromNull(value sql.NullInt64) *uint64 {
	if !value.Valid {
		return nil
	}
	converted := uint64(value.Int64)
	return &converted
}

func modelStringFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	converted := value.String
	return &converted
}

func cloneModelUint(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneModelString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func equalModelUint(left *uint64, right *uint64) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func equalModelStringPointer(left *string, right *string) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func cloneModelUsageRecord(record ModelUsageRecord) ModelUsageRecord {
	record.LedgerSequence = cloneModelUint(record.LedgerSequence)
	record.Tokens = record.Tokens.Clone()
	record.EstimatedCost = cloneModelString(record.EstimatedCost)
	record.ProviderReportedCost =
		cloneModelString(record.ProviderReportedCost)
	record.ReconciledCost = cloneModelString(record.ReconciledCost)
	return record
}

func cloneModelDispatchRecord(record ModelDispatchRecord) ModelDispatchRecord {
	record.Attempt = cloneModelDispatchAttempt(record.Attempt)
	record.Usage = cloneModelUsageRecord(record.Usage)
	return record
}
