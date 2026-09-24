package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	modelActionRejectionEventSchemaV1 = "model-action-rejection-event/v1"
	modelActionRejectionEventKind     = "MODEL_ACTION_REJECTED"

	ModelActionRejectionUnknownAction       = "UNKNOWN_ACTION"
	ModelActionRejectionInvalidInput        = "ACTION_INPUT_INVALID"
	ModelActionRejectionLimitReached        = "ACTION_LIMIT_REACHED"
	ModelActionRejectionPrepareFailed       = "ACTION_PREPARE_FAILED"
	ModelActionRejectionDeadlineExpired     = "ACTION_DEADLINE_EXPIRED"
	ModelActionRejectionAuthorityDenied     = "ACTION_AUTHORITY_DENIED"
	ModelActionRejectionProviderUnavailable = "ACTION_PROVIDER_UNAVAILABLE"
)

// CommitLegalModelActionRejectionInput records a wire-valid Action request
// that Core has deterministically refused before an external Action effect.
// The model remains SUCCEEDED; ErrorClassification is persisted in the
// authoritative RunEvent, not in ModelDispatchAttempt.error_classification.
type CommitLegalModelActionRejectionInput struct {
	Lease                        RunLease
	ModelAttemptID               string
	InvocationID                 string
	Provider                     moduleapi.ActivatedModuleRef
	ExpectedModelAttemptRevision uint64
	OutputCanonical              []byte
	UsageReceiptCanonical        []byte
	ProviderRequestID            string
	ErrorClassification          string
}

type CommitLegalModelActionRejectionResult struct {
	Model               ModelDispatchRecord
	Lease               RunLease
	Applied             bool
	ErrorClassification string
}

type modelActionRejectionEventV1 struct {
	SchemaVersion               string                          `json:"schema_version"`
	RunID                       string                          `json:"run_id"`
	ModelAttemptID              string                          `json:"model_attempt_id"`
	LogicalStepID               string                          `json:"logical_step_id"`
	LogicalOperationKey         string                          `json:"logical_operation_key"`
	ResultDigest                string                          `json:"result_digest"`
	ErrorClassification         string                          `json:"error_classification"`
	ModelUsage                  *corecontract.ModelUsageEventV1 `json:"model_usage"`
	ModelResourceSemanticDigest string                          `json:"model_resource_semantic_digest"`
}

// CommitLegalModelActionRejection atomically commits the successful model
// fact and terminates the Run without creating an Action Attempt or permit.
func (store *Store) CommitLegalModelActionRejection(
	ctx context.Context,
	input CommitLegalModelActionRejectionInput,
) (CommitLegalModelActionRejectionResult, error) {
	if ctx == nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidActionDispatch,
		)
	}
	input.OutputCanonical = bytes.Clone(input.OutputCanonical)
	input.UsageReceiptCanonical = bytes.Clone(input.UsageReceiptCanonical)
	if !validLeaseOpaqueID(input.ModelAttemptID) ||
		input.InvocationID != input.ModelAttemptID ||
		input.ExpectedModelAttemptRevision >= math.MaxInt64 ||
		!validModelOutcomeLabel(input.ErrorClassification) {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: invalid Model identity, revision, or rejection classification",
			ErrInvalidActionDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: invalid Model Provider: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	prepared, err := prepareModelDispatchOutcome(
		CommitModelDispatchOutcomeInput{
			Lease:                   input.Lease,
			AttemptID:               input.ModelAttemptID,
			InvocationID:            input.InvocationID,
			Provider:                input.Provider,
			ExpectedAttemptRevision: input.ExpectedModelAttemptRevision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         input.OutputCanonical,
			UsageReceiptCanonical:   input.UsageReceiptCanonical,
			ProviderRequestID:       input.ProviderRequestID,
		},
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if prepared.output.ActionRequest == nil || prepared.output.AssistantText != "" {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal Action rejection requires one wire-valid Action request",
			ErrInvalidActionDispatch,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"currentstore: acquire legal Action rejection connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"currentstore: begin legal Action rejection: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := queryModelDispatchRecord(ctx, connection, input.ModelAttemptID)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if current.Attempt.RunID != input.Lease.RunID ||
		current.Attempt.Binding.Provider != input.Provider {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: Model invocation differs from frozen Attempt",
			ErrActionDispatchConflict,
		)
	}
	if current.Attempt.State == corecontract.ModelAttemptSucceeded {
		result, err := reopenLegalModelActionRejection(
			ctx,
			connection,
			input,
			prepared,
			current,
		)
		if err != nil {
			return CommitLegalModelActionRejectionResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModelV1, input.ModelAttemptID,
		); err != nil {
			return CommitLegalModelActionRejectionResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
				"currentstore: commit legal Action rejection re-entry: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}
	if current.Attempt.State != corecontract.ModelAttemptPending ||
		current.Attempt.Revision != input.ExpectedModelAttemptRevision {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: source Model Attempt is not the expected PENDING revision",
			ErrActionDispatchConflict,
		)
	}
	run, err := loadRunForActionWrite(ctx, connection, input.Lease)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if run.Frame.Step != corecontract.ModelPendingLoopStep ||
		run.Frame.PendingAttemptID != current.Attempt.AttemptID ||
		run.Frame.PendingDispatchAttemptID != "" {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: rejection source is not the current PENDING Model",
			ErrActionDispatchIntegrity,
		)
	}
	if err := validateAttemptAgainstOutcomeRun(current, run); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if err := validateLegalActionRejectionStep(ctx, connection, current, run); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if err := requireNoActionSourcedByModel(
		ctx,
		connection,
		current.Attempt.AttemptID,
	); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	ledgerHead, err := validateModelUsageLedgerHead(
		ctx,
		connection,
		run.RunID,
		run.Frame.UsageLedgerRef,
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	merged, err := mergeModelOutcomeFacts(current, prepared)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if merged.Usage.LedgerSequence == nil && modelUsageHasReportedTokens(merged.Usage) {
		if ledgerHead >= math.MaxInt64 {
			return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
				"%w: Usage ledger cannot advance",
				ErrActionDispatchIntegrity,
			)
		}
		sequence := ledgerHead + 1
		merged.Usage.LedgerSequence = &sequence
	}
	nextBudgetRef := run.Frame.UsageLedgerRef
	if merged.Usage.LedgerSequence != nil {
		nextBudgetRef, err = corecontract.NewUsageLedgerRefV1(
			run.RunID,
			*merged.Usage.LedgerSequence,
		)
		if err != nil {
			return CommitLegalModelActionRejectionResult{}, err
		}
	}
	nextModelRevision, err := incrementSQLiteUint(
		current.Attempt.Revision,
		"Model Attempt revision",
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	nextUsageRevision, err := incrementSQLiteUint(current.Usage.Revision, "Usage revision")
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(run.RunRevision, "Run revision")
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(run.Frame.Revision, "Frame revision")
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	merged.Attempt.Revision = nextModelRevision
	merged.Usage.Revision = nextUsageRevision
	updatedAt := nowUnixMicro()
	merged.Attempt.UpdatedAt = time.UnixMicro(updatedAt).UTC()
	continuation, err := corecontract.NewLoopContinuationV1(
		corecontract.TerminatedLoopStep,
		current.Attempt.LogicalStepID,
		current.Attempt.AttemptID,
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	eventUsage, err := modelUsageEventV1(merged.Usage)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	resourceSemanticDigest, err := overviewResourceSemanticDigestV1(
		overviewResourceModelV1, merged,
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	eventCanonical, eventDigest, err := prepareModelActionRejectionEvent(
		modelActionRejectionEventV1{
			SchemaVersion:               modelActionRejectionEventSchemaV1,
			RunID:                       run.RunID,
			ModelAttemptID:              current.Attempt.AttemptID,
			LogicalStepID:               current.Attempt.LogicalStepID,
			LogicalOperationKey:         current.Attempt.LogicalOperationKey,
			ResultDigest:                merged.Attempt.ResultRef,
			ErrorClassification:         input.ErrorClassification,
			ModelUsage:                  eventUsage,
			ModelResourceSemanticDigest: resourceSemanticDigest,
		},
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	for _, content := range []*preparedAdmissionContent{
		prepared.resultContent,
		prepared.receiptContent,
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
		if err := putAdmissionContent(ctx, connection, *content, updatedAt); err != nil {
			return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
				"%w: persist legal Action rejection content: %v",
				ErrActionDispatchIntegrity,
				err,
			)
		}
	}
	if err := updateModelOutcomeRowsForAction(
		ctx,
		connection,
		current,
		merged,
		updatedAt,
	); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if err := updateRunAndFrameForLegalActionRejection(
		ctx,
		connection,
		input.Lease,
		run,
		nextRunRevision,
		nextFrameRevision,
		nextEvent,
		nextBudgetRef,
		continuation,
		eventDigest,
		updatedAt,
	); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if err := appendModelRejectionRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.ModelAttemptID, overviewTransitionModelOutcomeV1,
	); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	stored, err := queryModelDispatchRecord(ctx, connection, input.ModelAttemptID)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if stored.Attempt.State != corecontract.ModelAttemptSucceeded ||
		stored.Attempt.ErrorClassification != "" {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection polluted the successful Model Attempt",
			ErrActionDispatchIntegrity,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"currentstore: commit legal Action rejection: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return CommitLegalModelActionRejectionResult{
		Model:               cloneModelDispatchRecord(stored),
		Lease:               nextLease,
		Applied:             true,
		ErrorClassification: input.ErrorClassification,
	}, nil
}

func validateLegalActionRejectionStep(
	ctx context.Context,
	connection *sql.Conn,
	current ModelDispatchRecord,
	run RunForLoop,
) error {
	switch current.Attempt.LogicalStepID {
	case corecontract.PureChatModelLogicalStepIDV1,
		firstModelLogicalStepID:
		if current.Attempt.SourceDispatchAttemptID != "" {
			return fmt.Errorf(
				"%w: model-1 cannot name an Action source",
				ErrActionDispatchIntegrity,
			)
		}
	case secondModelLogicalStepID:
		if current.Attempt.SourceDispatchAttemptID == "" {
			return fmt.Errorf(
				"%w: model-2 lacks its Action source",
				ErrActionDispatchIntegrity,
			)
		}
		action, err := queryActionDispatchRecord(
			ctx,
			connection,
			current.Attempt.SourceDispatchAttemptID,
		)
		if err != nil {
			return err
		}
		if action.Attempt.RunID != run.RunID ||
			action.Attempt.MemberID != run.Member.MemberID ||
			action.Attempt.State != ActionDispatchSucceeded ||
			actionRecordResultStatus(action) != corecontract.ActionResultAvailable {
			return fmt.Errorf(
				"%w: model-2 Action source is not AVAILABLE",
				ErrActionDispatchIntegrity,
			)
		}
	default:
		return fmt.Errorf(
			"%w: legal Action rejection is outside the frozen first slice",
			ErrActionDispatchConflict,
		)
	}
	return nil
}

func requireNoActionSourcedByModel(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	modelAttemptID string,
) error {
	var count int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts
		WHERE source_model_attempt_id=? AND dispatch_kind='ACTION'
	`, modelAttemptID).Scan(&count); err != nil {
		return fmt.Errorf("currentstore: count rejected Model Action Attempts: %w", err)
	}
	if count != 0 {
		return fmt.Errorf(
			"%w: rejected Model already owns an Action Attempt",
			ErrActionDispatchConflict,
		)
	}
	return nil
}

func updateRunAndFrameForLegalActionRejection(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
	run RunForLoop,
	nextRunRevision uint64,
	nextFrameRevision uint64,
	nextEvent uint64,
	nextBudgetRef string,
	continuation []byte,
	eventDigest string,
	updatedAt int64,
) error {
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=? AND state=? AND disposition IS NULL AND revision=?
	`,
		corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		int64(nextRunRevision),
		updatedAt,
		run.RunID,
		corecontract.InitialRunState,
		int64(run.RunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: terminate legal Action rejection Run: %w", err)
	}
	if err := requireModelCASRow(runUpdate, "terminate legal Action rejection Run"); err != nil {
		return err
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, usage_ledger_ref=?, continuation=?,
			pending_attempt_id=NULL, pending_dispatch_attempt_id=NULL,
			waiting_reason=NULL, last_authoritative_event=?
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
		corecontract.TerminatedLoopStep,
		nextBudgetRef,
		continuation,
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		corecontract.ModelPendingLoopStep,
		run.Frame.PendingAttemptID,
		int64(run.Frame.LastAuthoritativeEvent),
		lease.OwnerID,
		int64(lease.LeaseEpoch),
		updatedAt,
		int64(nextRunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: terminate legal Action rejection Frame: %w", err)
	}
	if err := requireModelCASRow(frameUpdate, "terminate legal Action rejection Frame"); err != nil {
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
		modelActionRejectionEventKind,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		updatedAt,
	); err != nil {
		return fmt.Errorf(
			"%w: append legal Action rejection event: %v",
			ErrActionDispatchIntegrity,
			err,
		)
	}
	return nil
}

func prepareModelActionRejectionEvent(
	event modelActionRejectionEventV1,
) ([]byte, string, error) {
	if event.SchemaVersion != modelActionRejectionEventSchemaV1 ||
		!validLeaseOpaqueID(event.RunID) ||
		!validLeaseOpaqueID(event.ModelAttemptID) ||
		!validLeaseOpaqueID(event.LogicalStepID) ||
		!moduleapi.ValidSHA256(event.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(event.ResultDigest) ||
		!validModelOutcomeLabel(event.ErrorClassification) || event.ModelUsage == nil ||
		event.ModelUsage.Validate() != nil ||
		!moduleapi.ValidSHA256(event.ModelResourceSemanticDigest) {
		return nil, "", fmt.Errorf(
			"%w: invalid legal Action rejection event",
			ErrActionDispatchIntegrity,
		)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return nil, "", err
	}
	digest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		return nil, "", err
	}
	return bytes.Clone(canonical), digest, nil
}

func loadModelActionRejectionEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	eventSequence uint64,
) (modelActionRejectionEventV1, error) {
	var (
		kind        string
		payloadRef  string
		digest      string
		canonical   []byte
		contentKind string
		mediaType   string
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT e.event_kind, e.payload_ref, e.payload_digest,
		       c.kind, c.media_type, c.canonical_bytes
		FROM run_events AS e
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=? AND e.event_sequence=?
	`, runID, int64(eventSequence)).Scan(
		&kind,
		&payloadRef,
		&digest,
		&contentKind,
		&mediaType,
		&canonical,
	); err != nil {
		return modelActionRejectionEventV1{}, fmt.Errorf(
			"%w: load legal Action rejection event: %v",
			ErrActionDispatchIntegrity,
			err,
		)
	}
	computed, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil || kind != modelActionRejectionEventKind ||
		payloadRef != digest || digest != computed ||
		contentKind != string(ContentRunEventPayload) ||
		mediaType != admissionJSONMediaType {
		return modelActionRejectionEventV1{}, fmt.Errorf(
			"%w: legal Action rejection event content closure",
			ErrActionDispatchIntegrity,
		)
	}
	var event modelActionRejectionEventV1
	if err := json.Unmarshal(canonical, &event); err != nil {
		return modelActionRejectionEventV1{}, err
	}
	rebuilt, rebuiltDigest, err := prepareModelActionRejectionEvent(event)
	if err != nil || rebuiltDigest != digest || !bytes.Equal(rebuilt, canonical) ||
		event.RunID != runID {
		return modelActionRejectionEventV1{}, fmt.Errorf(
			"%w: legal Action rejection event wire",
			ErrActionDispatchIntegrity,
		)
	}
	return event, nil
}

func reopenLegalModelActionRejection(
	ctx context.Context,
	connection *sql.Conn,
	input CommitLegalModelActionRejectionInput,
	prepared preparedModelDispatchOutcome,
	current ModelDispatchRecord,
) (CommitLegalModelActionRejectionResult, error) {
	if input.ExpectedModelAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedModelAttemptRevision == math.MaxUint64 ||
			input.ExpectedModelAttemptRevision+1 != current.Attempt.Revision) {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection re-entry revision differs",
			ErrActionDispatchConflict,
		)
	}
	if err := requireSameModelOutcome(current, prepared); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if current.Attempt.ErrorClassification != "" {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection polluted Model error classification",
			ErrActionDispatchIntegrity,
		)
	}
	if err := requireNoActionSourcedByModel(
		ctx,
		connection,
		current.Attempt.AttemptID,
	); err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	currentLease, err := loadCurrentModelOutcomeLease(ctx, connection, input.Lease)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	exact := input.Lease.RunRevision == currentLease.RunRevision &&
		input.Lease.FrameRevision == currentLease.FrameRevision
	oneBehind := input.Lease.RunRevision < math.MaxUint64 &&
		input.Lease.FrameRevision < math.MaxUint64 &&
		input.Lease.RunRevision+1 == currentLease.RunRevision &&
		input.Lease.FrameRevision+1 == currentLease.FrameRevision
	if !exact && !oneBehind {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection re-entry lease differs",
			ErrActionDispatchConflict,
		)
	}
	run, err := loadRunForActionWrite(ctx, connection, currentLease)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if run.Frame.Step != corecontract.TerminatedLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != "" ||
		run.State != corecontract.TerminatedLoopStep ||
		run.Disposition != corecontract.TerminatedLoopStep {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection terminal projection",
			ErrActionDispatchIntegrity,
		)
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continuation.AttemptKind != corecontract.AttemptKindModel ||
		continuation.AttemptID != current.Attempt.AttemptID ||
		continuation.LogicalStepID != current.Attempt.LogicalStepID {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection continuation",
			ErrActionDispatchIntegrity,
		)
	}
	event, err := loadModelActionRejectionEvent(
		ctx,
		connection,
		run.RunID,
		run.Frame.LastAuthoritativeEvent,
	)
	if err != nil {
		return CommitLegalModelActionRejectionResult{}, err
	}
	if event.ModelAttemptID != current.Attempt.AttemptID ||
		event.LogicalStepID != current.Attempt.LogicalStepID ||
		event.LogicalOperationKey != current.Attempt.LogicalOperationKey ||
		event.ResultDigest != current.Attempt.ResultRef ||
		event.ErrorClassification != input.ErrorClassification {
		return CommitLegalModelActionRejectionResult{}, fmt.Errorf(
			"%w: legal rejection event facts differ",
			ErrActionDispatchConflict,
		)
	}
	return CommitLegalModelActionRejectionResult{
		Model:               cloneModelDispatchRecord(current),
		Lease:               currentLease,
		Applied:             false,
		ErrorClassification: event.ErrorClassification,
	}, nil
}
