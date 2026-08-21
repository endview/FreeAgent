package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrTerminalRunUnavailable = errors.New(
	"currentstore: terminal Run result unavailable",
)

// TerminalRunResult is the authoritative S1 result projection read after the
// Universal Loop has terminated a Run. A failed model attempt has no Output.
// The canonical output is retained so internal callers never have to rebuild
// or trust an in-memory provider response.
type TerminalRunResult struct {
	RunID               string
	FrameRevision       uint64
	ReasonCode          string
	AttemptID           string
	MemberID            string
	AttemptKind         corecontract.AttemptKindV1
	State               corecontract.ModelAttemptState
	ModelState          corecontract.ModelAttemptState
	ActionState         ActionDispatchState
	ChannelState        DispatchState
	ActionResultStatus  corecontract.ActionResultStatusV1
	Output              moduleapi.ModelGenerateOutputV1
	OutputCanonical     []byte
	ErrorClassification string
}

// GetTerminalRunResult reads the terminal Run/Frame/Attempt/History closure in
// one SQLite snapshot. It performs no lease acquisition and no writes.
func (store *Store) GetTerminalRunResult(
	ctx context.Context,
	runID string,
) (TerminalRunResult, error) {
	if ctx == nil {
		return TerminalRunResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidLoopRead,
		)
	}
	if !validLeaseOpaqueID(runID) {
		return TerminalRunResult{}, fmt.Errorf(
			"%w: invalid Run ID",
			ErrInvalidLoopRead,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return TerminalRunResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return TerminalRunResult{}, fmt.Errorf(
			"currentstore: acquire terminal Run read connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return TerminalRunResult{}, fmt.Errorf(
			"currentstore: begin GetTerminalRunResult: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	result, err := loadTerminalRunResult(ctx, connection, runID)
	if err != nil {
		return TerminalRunResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return TerminalRunResult{}, fmt.Errorf(
			"currentstore: commit GetTerminalRunResult: %w",
			err,
		)
	}
	committed = true
	result.OutputCanonical = bytes.Clone(result.OutputCanonical)
	return result, nil
}

func loadTerminalRunResult(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (TerminalRunResult, error) {
	var (
		runState      string
		disposition   sql.NullString
		runRevision   int64
		frameRevision int64
		frameStep     string
		continuation  []byte
		pending       sql.NullString
		pendingAction sql.NullString
		waiting       sql.NullString
		lastEvent     int64
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			r.state,
			r.disposition,
			r.revision,
			f.frame_revision,
			f.step,
			f.continuation,
			f.pending_attempt_id,
			f.pending_dispatch_attempt_id,
			f.waiting_reason,
			f.last_authoritative_event
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, runID).Scan(
		&runState,
		&disposition,
		&runRevision,
		&frameRevision,
		&frameStep,
		&continuation,
		&pending,
		&pendingAction,
		&waiting,
		&lastEvent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TerminalRunResult{}, fmt.Errorf(
			"%w: Run %q",
			ErrTerminalRunUnavailable,
			runID,
		)
	}
	if err != nil {
		return TerminalRunResult{}, fmt.Errorf(
			"currentstore: read terminal Run head: %w",
			err,
		)
	}
	if runState != corecontract.TerminatedLoopStep ||
		!disposition.Valid ||
		disposition.String != corecontract.TerminatedLoopStep ||
		frameStep != corecontract.TerminatedLoopStep {
		return TerminalRunResult{}, fmt.Errorf(
			"%w: Run %q is not terminal",
			ErrTerminalRunUnavailable,
			runID,
		)
	}
	if runRevision < 0 ||
		frameRevision < 0 ||
		lastEvent < 0 ||
		pending.Valid ||
		pendingAction.Valid ||
		waiting.Valid {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Run/Frame projection",
			ErrAdmissionIntegrity,
		)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || continued.State != corecontract.TerminatedLoopStep {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal continuation",
			err,
		)
	}
	if err := verifyRunEventHead(
		ctx,
		connection,
		runID,
		uint64(lastEvent),
	); err != nil {
		return TerminalRunResult{}, err
	}
	var authoritativeFrameRevision int64
	if err := connection.QueryRowContext(ctx, `
		SELECT to_revision
		FROM run_events
		WHERE run_id=? AND event_sequence=?
	`, runID, lastEvent).Scan(&authoritativeFrameRevision); err != nil ||
		authoritativeFrameRevision <= 0 ||
		authoritativeFrameRevision > frameRevision {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal authoritative Frame revision",
			err,
		)
	}

	modelIDs, err := terminalAttemptIDs(
		ctx,
		connection,
		"model_dispatch_attempts",
		runID,
	)
	if err != nil {
		return TerminalRunResult{}, err
	}
	actionIDs, err := terminalAttemptIDs(
		ctx,
		connection,
		"action_dispatch_attempts",
		runID,
	)
	if err != nil {
		return TerminalRunResult{}, err
	}
	channelIDs, err := terminalAttemptIDs(
		ctx,
		connection,
		"channel_dispatch_attempts",
		runID,
	)
	if err != nil {
		return TerminalRunResult{}, err
	}
	var historyCount int64
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM history_entries WHERE run_id=?
	`, runID).Scan(&historyCount); err != nil {
		return TerminalRunResult{}, loopReadIntegrity("terminal History count", err)
	}
	var result TerminalRunResult
	if continued.CoreFailureReason != "" {
		result, err = loadTerminalCoreDeterministicFailureResult(
			ctx,
			connection,
			runID,
			continued,
			uint64(lastEvent),
			modelIDs,
			actionIDs,
			channelIDs,
			historyCount,
		)
	} else {
		switch continued.AttemptKind {
		case corecontract.AttemptKindModel:
			result, err = loadTerminalModelFamilyResult(
				ctx,
				connection,
				runID,
				continued,
				uint64(lastEvent),
				modelIDs,
				actionIDs,
				channelIDs,
				historyCount,
			)
		case corecontract.AttemptKindAction:
			result, err = loadTerminalActionFamilyResult(
				ctx,
				connection,
				runID,
				continued,
				uint64(lastEvent),
				modelIDs,
				actionIDs,
				channelIDs,
				historyCount,
			)
		case corecontract.AttemptKindChannel:
			result, err = loadTerminalChannelFamilyResult(
				ctx,
				connection,
				runID,
				continued,
				uint64(lastEvent),
				modelIDs,
				actionIDs,
				channelIDs,
				historyCount,
			)
		default:
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal continuation AttemptKind",
				ErrAdmissionIntegrity,
			)
		}
	}
	if err != nil {
		return TerminalRunResult{}, err
	}
	// Lease acquire/renew operations may advance loop_frames.frame_revision
	// after termination. Composite evidence must bind the immutable terminal
	// transition, not that mutable lease head.
	result.FrameRevision = uint64(authoritativeFrameRevision)
	result.ReasonCode, err = terminalRunReasonCode(result)
	if err != nil {
		return TerminalRunResult{}, err
	}
	return result, nil
}

func terminalRunReasonCode(result TerminalRunResult) (string, error) {
	if result.AttemptKind == "" && result.ErrorClassification != "" {
		if err := corecontract.ValidateCoreDeterministicFailureReasonV1(
			result.ErrorClassification,
		); err != nil {
			return "", loopReadIntegrity(
				"terminal deterministic Core failure reason",
				err,
			)
		}
		return result.ErrorClassification, nil
	}
	if result.AttemptKind == corecontract.AttemptKindAction {
		if result.ErrorClassification != "" {
			return result.ErrorClassification, nil
		}
		switch result.ActionState {
		case ActionDispatchFailed:
			return "ACTION_FAILED", nil
		case ActionDispatchSucceeded:
			return "ACTION_RESULT_REJECTED", nil
		default:
			return "", loopReadIntegrity(
				"terminal Action reason",
				ErrAdmissionIntegrity,
			)
		}
	}
	if result.AttemptKind == corecontract.AttemptKindChannel {
		if result.ErrorClassification != "" {
			return result.ErrorClassification, nil
		}
		switch result.ChannelState {
		case DispatchSucceeded:
			return "CHANNEL_SUCCEEDED", nil
		case DispatchFailed:
			return "CHANNEL_FAILED", nil
		default:
			return "", loopReadIntegrity(
				"terminal Channel reason",
				ErrAdmissionIntegrity,
			)
		}
	}
	if result.ModelState == corecontract.ModelAttemptSucceeded &&
		result.ErrorClassification != "" {
		return result.ErrorClassification, nil
	}
	switch result.ModelState {
	case corecontract.ModelAttemptSucceeded:
		return "MODEL_SUCCEEDED", nil
	case corecontract.ModelAttemptFailed:
		return "MODEL_FAILED", nil
	default:
		return "", loopReadIntegrity(
			"terminal Model reason",
			ErrAdmissionIntegrity,
		)
	}
}

func loadTerminalCoreDeterministicFailureResult(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	continued corecontract.LoopContinuationV1,
	lastEvent uint64,
	modelIDs []string,
	actionIDs []string,
	channelIDs []string,
	historyCount int64,
) (TerminalRunResult, error) {
	if len(modelIDs) != 0 || len(actionIDs) != 0 ||
		len(channelIDs) != 0 || historyCount != 0 ||
		continued.AttemptKind != "" || continued.AttemptID != "" ||
		continued.LogicalStepID != "" {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal deterministic Core failure cardinality",
			ErrAdmissionIntegrity,
		)
	}
	var manifestCanonical []byte
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json FROM run_manifests WHERE run_id=?
	`, runID).Scan(&manifestCanonical); err != nil {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal deterministic Core failure Manifest",
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.RunID != runID {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal deterministic Core failure Manifest wire",
			err,
		)
	}
	reason := continued.CoreFailureReason
	if reason == corecontract.CompositeRepairSkippedReasonV1 {
		if err := validateTerminalCompositeRepairSkip(
			ctx,
			connection,
			manifest,
			lastEvent,
		); err != nil {
			return TerminalRunResult{}, err
		}
	} else {
		event, eventErr := loadCoreDeterministicFailureEvent(
			ctx,
			connection,
			runID,
			lastEvent,
		)
		if eventErr != nil || event.Reason != reason {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal deterministic Core failure event",
				eventErr,
			)
		}
	}
	validCoordinator := false
	if manifest.Composite != nil {
		switch manifest.Composite.Role {
		case corecontract.CompositeRunRoleRootV1:
			validCoordinator = manifest.Composite.Plan != nil
		case corecontract.CompositeRunRoleChildV1:
			validCoordinator =
				manifest.Composite.RepairRound ==
					corecontract.CompositeRepairRoundOneV1 &&
					reason == corecontract.CompositeRepairSkippedReasonV1 &&
					manifest.Composite.Plan == nil &&
					manifest.Composite.Assignment != nil
		case corecontract.CompositeRunRoleReviewerV1:
			validCoordinator = manifest.Composite.Plan == nil &&
				manifest.Composite.Assignment == nil &&
				((manifest.Composite.RepairRound == 0 &&
					(reason == corecontract.AllRequiredChildFailedReasonV1 ||
						reason == corecontract.CompositeReviewFailedReasonV1)) ||
					(manifest.Composite.RepairRound ==
						corecontract.CompositeRepairRoundOneV1 &&
						reason ==
							corecontract.CompositeRepairSkippedReasonV1))
		}
	}
	if !validCoordinator {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal deterministic Core failure coordinator",
			err,
		)
	}
	return TerminalRunResult{
		RunID:               runID,
		MemberID:            manifest.PrimaryMemberID,
		ErrorClassification: reason,
	}, nil
}

func validateTerminalCompositeRepairSkip(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
	lastEvent uint64,
) error {
	if manifest.Composite == nil ||
		manifest.Composite.RepairRound !=
			corecontract.CompositeRepairRoundOneV1 {
		return loopReadIntegrity(
			"terminal repair skip Manifest",
			ErrAdmissionIntegrity,
		)
	}
	root, err := loadCompositeRootManifest(
		ctx,
		connection,
		manifest.Composite.RootRunID,
	)
	if err != nil || root.Composite.Plan.Decision == nil ||
		manifest.Composite.ParentManifestDigest != root.ManifestDigest {
		return loopReadIntegrity("terminal repair skip root", err)
	}
	var transition *compositeRepairTransition
	for _, planned := range root.Composite.Plan.Decision.RepairChildren {
		if planned.RunID == manifest.RunID {
			candidate := compositeRepairTransition{
				runID:        planned.RunID,
				parentSlotID: planned.ParentSlotID,
				role:         corecontract.CompositeRunRoleChildV1,
				kind:         compositeRepairTransitionSkip,
			}
			transition = &candidate
			break
		}
	}
	if transition == nil &&
		root.Composite.Plan.Decision.RepairReviewer.RunID == manifest.RunID {
		planned := root.Composite.Plan.Decision.RepairReviewer
		candidate := compositeRepairTransition{
			runID:        planned.RunID,
			parentSlotID: planned.ParentSlotID,
			role:         corecontract.CompositeRunRoleReviewerV1,
			kind:         compositeRepairTransitionSkip,
		}
		transition = &candidate
	}
	if transition == nil || lastEvent != 1 {
		return loopReadIntegrity(
			"terminal repair skip plan",
			ErrAdmissionIntegrity,
		)
	}
	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil {
		return err
	}
	reviewer, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewer == nil || reviewer.ResultRef == "" ||
		reviewer.CollaborationVerdict == nil {
		return loopReadIntegrity("terminal repair skip verdict", err)
	}
	if reviewer.CollaborationVerdict.Decision ==
		corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		if transition.role != corecontract.CompositeRunRoleChildV1 {
			return loopReadIntegrity(
				"terminal repair skip Reviewer decision",
				ErrAdmissionIntegrity,
			)
		}
		for _, slotID := range reviewer.CollaborationVerdict.AffectedSlotIDs {
			if manifest.Composite.Assignment != nil &&
				slotID == manifest.Composite.Assignment.SlotID {
				return loopReadIntegrity(
					"terminal affected repair Child was skipped",
					ErrAdmissionIntegrity,
				)
			}
		}
	}
	return verifyCompositeRepairTransitionEvent(
		ctx,
		connection,
		root,
		reviewer.ResultRef,
		*transition,
	)
}

func terminalAttemptIDs(
	ctx context.Context,
	connection readQueryerV1,
	table string,
	runID string,
) ([]string, error) {
	query := ""
	switch table {
	case "model_dispatch_attempts":
		query = `
			SELECT attempt_id FROM model_dispatch_attempts
			WHERE run_id=? ORDER BY created_at, attempt_id
		`
	case "action_dispatch_attempts":
		query = `
			SELECT attempt_id FROM dispatch_attempts
			WHERE run_id=? AND dispatch_kind='ACTION'
			ORDER BY created_at, attempt_id
		`
	case "channel_dispatch_attempts":
		query = `
			SELECT attempt_id FROM dispatch_attempts
			WHERE run_id=? AND dispatch_kind='CHANNEL_SEND'
			ORDER BY created_at, attempt_id
		`
	default:
		return nil, loopReadIntegrity("terminal Attempt table", ErrAdmissionIntegrity)
	}
	rows, err := connection.QueryContext(ctx, query, runID)
	if err != nil {
		return nil, loopReadIntegrity("terminal Attempt IDs", err)
	}
	defer rows.Close()
	ids := make([]string, 0, 2)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, loopReadIntegrity("terminal Attempt ID", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, loopReadIntegrity("terminal Attempt rows", err)
	}
	return ids, nil
}

func loadTerminalModelFamilyResult(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	continued corecontract.LoopContinuationV1,
	lastEvent uint64,
	modelIDs []string,
	actionIDs []string,
	channelIDs []string,
	historyCount int64,
) (TerminalRunResult, error) {
	if len(modelIDs) == 0 || len(modelIDs) > 2 || len(actionIDs) > 1 ||
		len(channelIDs) != 0 {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Model family cardinality",
			ErrAdmissionIntegrity,
		)
	}
	record, err := queryModelDispatchRecord(ctx, connection, continued.AttemptID)
	if err != nil {
		return TerminalRunResult{}, loopReadIntegrity("terminal model Attempt", err)
	}
	attempt := record.Attempt
	if attempt.RunID != runID || attempt.LogicalStepID != continued.LogicalStepID {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal continuation model identity",
			ErrAdmissionIntegrity,
		)
	}
	if err := validateTerminalModelFamilyClosure(
		ctx,
		connection,
		attempt,
		modelIDs,
		actionIDs,
		channelIDs,
	); err != nil {
		return TerminalRunResult{}, err
	}
	result := TerminalRunResult{
		RunID:               runID,
		AttemptID:           attempt.AttemptID,
		MemberID:            attempt.MemberID,
		AttemptKind:         corecontract.AttemptKindModel,
		State:               attempt.State,
		ModelState:          attempt.State,
		ErrorClassification: attempt.ErrorClassification,
	}
	eventKind, err := terminalLastEventKind(ctx, connection, runID, lastEvent)
	if err != nil {
		return TerminalRunResult{}, err
	}
	if eventKind == modelActionRejectionEventKind {
		event, err := loadModelActionRejectionEvent(
			ctx,
			connection,
			runID,
			lastEvent,
		)
		if err != nil {
			return TerminalRunResult{}, err
		}
		content, err := queryContent(ctx, connection, attempt.ResultRef)
		if err != nil || content.Kind != ContentModelResult {
			return TerminalRunResult{}, loopReadIntegrity(
				"legal Action rejection model result",
				err,
			)
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(content.CanonicalBytes)
		if attempt.State != corecontract.ModelAttemptSucceeded ||
			attempt.ErrorClassification != "" ||
			historyCount != 0 ||
			err != nil || output.ActionRequest == nil ||
			event.ModelAttemptID != attempt.AttemptID ||
			event.LogicalStepID != attempt.LogicalStepID ||
			event.LogicalOperationKey != attempt.LogicalOperationKey ||
			event.ResultDigest != attempt.ResultRef {
			return TerminalRunResult{}, loopReadIntegrity(
				"legal Action rejection terminal closure",
				ErrAdmissionIntegrity,
			)
		}
		result.ErrorClassification = event.ErrorClassification
		return result, nil
	}
	switch attempt.State {
	case corecontract.ModelAttemptSucceeded:
		if eventKind != corecontract.ModelDispatchTerminalEventKind ||
			historyCount != 1 || attempt.ResultRef == "" ||
			attempt.ErrorClassification != "" {
			return TerminalRunResult{}, loopReadIntegrity(
				"successful terminal model projection",
				ErrAdmissionIntegrity,
			)
		}
		content, err := loadTerminalHistoryContent(ctx, connection, runID, attempt)
		if err != nil {
			return TerminalRunResult{}, err
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(content.CanonicalBytes)
		if err != nil || output.ProviderRequestID != attempt.ProviderRequestID ||
			output.ActionRequest != nil {
			return TerminalRunResult{}, loopReadIntegrity("terminal model output", err)
		}
		result.Output = output
		result.OutputCanonical = bytes.Clone(content.CanonicalBytes)
	case corecontract.ModelAttemptFailed:
		if eventKind != corecontract.ModelDispatchTerminalEventKind ||
			historyCount != 0 || attempt.ResultRef != "" ||
			attempt.ErrorClassification == "" {
			return TerminalRunResult{}, loopReadIntegrity(
				"failed terminal model projection",
				ErrAdmissionIntegrity,
			)
		}
	default:
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal model state",
			ErrAdmissionIntegrity,
		)
	}
	return result, nil
}

func validateTerminalModelFamilyClosure(
	ctx context.Context,
	connection readQueryerV1,
	final ModelDispatchAttemptRecord,
	modelIDs []string,
	actionIDs []string,
	channelIDs []string,
) error {
	if len(channelIDs) != 0 {
		return loopReadIntegrity(
			"terminal Model family contains Channel Attempt",
			ErrAdmissionIntegrity,
		)
	}
	containsFinal := false
	for _, id := range modelIDs {
		if id == final.AttemptID {
			containsFinal = true
		}
	}
	if !containsFinal {
		return loopReadIntegrity("terminal model membership", ErrAdmissionIntegrity)
	}
	if final.SourceDispatchAttemptID == "" {
		if len(modelIDs) != 1 || len(actionIDs) != 0 {
			return loopReadIntegrity(
				"terminal model-1 family cardinality",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}
	if len(modelIDs) != 2 || len(actionIDs) != 1 ||
		actionIDs[0] != final.SourceDispatchAttemptID {
		return loopReadIntegrity(
			"terminal model-2 family cardinality",
			ErrAdmissionIntegrity,
		)
	}
	action, err := queryActionDispatchRecord(ctx, connection, final.SourceDispatchAttemptID)
	if err != nil {
		return loopReadIntegrity("terminal model-2 Action source", err)
	}
	if action.Attempt.State != ActionDispatchSucceeded ||
		actionRecordResultStatus(action) != corecontract.ActionResultAvailable ||
		final.LogicalStepID != secondModelLogicalStepID ||
		final.ContextCompilation != nil {
		return loopReadIntegrity("terminal model-2 Action state", ErrAdmissionIntegrity)
	}
	return validatePersistedSecondModelRequestClosure(ctx, connection, final, action)
}

func loadTerminalActionFamilyResult(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	continued corecontract.LoopContinuationV1,
	lastEvent uint64,
	modelIDs []string,
	actionIDs []string,
	channelIDs []string,
	historyCount int64,
) (TerminalRunResult, error) {
	if len(modelIDs) != 1 || len(actionIDs) != 1 ||
		len(channelIDs) != 0 || actionIDs[0] != continued.AttemptID ||
		historyCount != 0 {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action family cardinality",
			ErrAdmissionIntegrity,
		)
	}
	action, err := queryActionDispatchRecord(ctx, connection, continued.AttemptID)
	if err != nil {
		return TerminalRunResult{}, loopReadIntegrity("terminal Action Attempt", err)
	}
	if action.Attempt.RunID != runID ||
		action.Attempt.LogicalStepID != continued.LogicalStepID ||
		action.Attempt.LogicalStepID != firstActionLogicalStepID {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action continuation identity",
			ErrAdmissionIntegrity,
		)
	}
	model, err := queryModelDispatchRecord(
		ctx,
		connection,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil || model.Attempt.AttemptID != modelIDs[0] ||
		model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		model.Attempt.SourceDispatchAttemptID != "" ||
		model.Attempt.LogicalStepID != firstModelLogicalStepID {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action source Model",
			err,
		)
	}
	modelResult, err := queryContent(ctx, connection, model.Attempt.ResultRef)
	if err != nil || modelResult.Kind != ContentModelResult {
		return TerminalRunResult{}, loopReadIntegrity("terminal Action model result", err)
	}
	modelOutput, err := moduleapi.RestoreModelGenerateOutputV1(modelResult.CanonicalBytes)
	if err != nil || modelOutput.ActionRequest == nil {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action model output",
			err,
		)
	}
	event, err := loadActionTerminalEvent(ctx, connection, runID, lastEvent)
	if err != nil || event.AttemptID != action.Attempt.AttemptID ||
		event.LogicalStepID != action.Attempt.LogicalStepID ||
		event.LogicalOperationKey != action.Attempt.LogicalOperationKey ||
		event.ProposalDigest != action.Attempt.ProposalRef ||
		event.State != action.Attempt.State ||
		event.ResultDigest != action.Attempt.ResultRef {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action event closure",
			err,
		)
	}
	result := TerminalRunResult{
		RunID:       runID,
		AttemptID:   action.Attempt.AttemptID,
		MemberID:    action.Attempt.MemberID,
		AttemptKind: corecontract.AttemptKindAction,
		State:       model.Attempt.State,
		ModelState:  model.Attempt.State,
		ActionState: action.Attempt.State,
	}
	switch action.Attempt.State {
	case ActionDispatchFailed:
		if action.Attempt.ErrorClassification == "" || action.Result != nil {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal failed Action projection",
				ErrAdmissionIntegrity,
			)
		}
		result.ErrorClassification = action.Attempt.ErrorClassification
	case ActionDispatchSucceeded:
		status := actionRecordResultStatus(action)
		if status != corecontract.ActionResultRejected || action.Result == nil {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal successful Action result status",
				ErrAdmissionIntegrity,
			)
		}
		_, definition, err := loadActionAttemptFrozenDefinition(
			ctx,
			connection,
			action.Attempt,
		)
		if err != nil {
			return TerminalRunResult{}, err
		}
		restored, err := corecontract.RestoreActionResultV1(
			action.Result.CanonicalBytes,
			action.Result.Digest,
			definition,
		)
		if err != nil || restored.ErrorClassification == "" {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal RESULT_REJECTED closure",
				err,
			)
		}
		result.ActionResultStatus = restored.Status
		result.ErrorClassification = restored.ErrorClassification
	default:
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Action state",
			ErrAdmissionIntegrity,
		)
	}
	return result, nil
}

func loadTerminalChannelFamilyResult(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	continued corecontract.LoopContinuationV1,
	lastEvent uint64,
	modelIDs []string,
	actionIDs []string,
	channelIDs []string,
	historyCount int64,
) (TerminalRunResult, error) {
	if len(channelIDs) != 1 || channelIDs[0] != continued.AttemptID ||
		len(modelIDs) == 0 || len(modelIDs) > 2 || len(actionIDs) > 1 ||
		historyCount != 1 {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Channel family cardinality",
			ErrAdmissionIntegrity,
		)
	}
	channel, err := queryChannelDispatchRecord(ctx, connection, continued.AttemptID)
	if err != nil {
		return TerminalRunResult{}, loopReadIntegrity("terminal Channel Attempt", err)
	}
	if channel.Attempt.RunID != runID ||
		channel.Attempt.LogicalStepID != continued.LogicalStepID ||
		channel.Attempt.LogicalStepID != corecontract.ChannelSendLogicalStepIDV1 {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Channel continuation identity",
			ErrAdmissionIntegrity,
		)
	}
	model, err := queryModelDispatchRecord(
		ctx,
		connection,
		channel.Attempt.SourceModelAttemptID,
	)
	if err != nil || model.Attempt.RunID != runID ||
		model.Attempt.MemberID != channel.Attempt.MemberID ||
		model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		model.Attempt.ResultRef == "" || !containsTerminalID(modelIDs, model.Attempt.AttemptID) {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Channel source Model",
			err,
		)
	}
	if model.Attempt.SourceDispatchAttemptID == "" {
		if len(modelIDs) != 1 || len(actionIDs) != 0 {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal Channel direct-model family",
				ErrAdmissionIntegrity,
			)
		}
	} else {
		if len(modelIDs) != 2 || len(actionIDs) != 1 ||
			actionIDs[0] != model.Attempt.SourceDispatchAttemptID ||
			model.Attempt.LogicalStepID != secondModelLogicalStepID ||
			model.Attempt.ContextCompilation != nil {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal Channel model-2 family",
				ErrAdmissionIntegrity,
			)
		}
		action, actionErr := queryActionDispatchRecord(
			ctx,
			connection,
			model.Attempt.SourceDispatchAttemptID,
		)
		if actionErr != nil || action.Attempt.State != ActionDispatchSucceeded ||
			actionRecordResultStatus(action) != corecontract.ActionResultAvailable {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal Channel model-2 Action source",
				actionErr,
			)
		}
		if err := validatePersistedSecondModelRequestClosure(
			ctx,
			connection,
			model.Attempt,
			action,
		); err != nil {
			return TerminalRunResult{}, err
		}
	}
	history, err := loadTerminalHistoryContent(ctx, connection, runID, model.Attempt)
	if err != nil {
		return TerminalRunResult{}, err
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(history.CanonicalBytes)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" ||
		output.ProviderRequestID != model.Attempt.ProviderRequestID {
		return TerminalRunResult{}, loopReadIntegrity("terminal Channel model output", err)
	}
	event, err := loadChannelTerminalEvent(ctx, connection, runID, lastEvent)
	if err != nil || event.AttemptID != channel.Attempt.AttemptID ||
		event.LogicalStepID != channel.Attempt.LogicalStepID ||
		event.LogicalOperationKey != channel.Attempt.LogicalOperationKey ||
		event.ProposalDigest != channel.Attempt.ProposalRef ||
		event.State != channel.Attempt.State ||
		event.ResultDigest != channel.Attempt.ResultRef {
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Channel event closure",
			err,
		)
	}
	result := TerminalRunResult{
		RunID:           runID,
		AttemptID:       channel.Attempt.AttemptID,
		MemberID:        channel.Attempt.MemberID,
		AttemptKind:     corecontract.AttemptKindChannel,
		State:           model.Attempt.State,
		ModelState:      model.Attempt.State,
		ChannelState:    channel.Attempt.State,
		Output:          output,
		OutputCanonical: bytes.Clone(history.CanonicalBytes),
	}
	switch channel.Attempt.State {
	case DispatchSucceeded:
		if channel.Result == nil || channel.Attempt.ErrorClassification != "" {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal successful Channel projection",
				ErrAdmissionIntegrity,
			)
		}
		executed, err := moduleapi.RestoreChannelExecutionResultV1(
			channel.Result.CanonicalBytes,
		)
		if err != nil || executed.AttemptID != channel.Attempt.AttemptID ||
			executed.Outcome != moduleapi.ChannelExecutionSucceeded {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal successful Channel result",
				err,
			)
		}
	case DispatchFailed:
		if channel.Result != nil || channel.Attempt.ErrorClassification == "" {
			return TerminalRunResult{}, loopReadIntegrity(
				"terminal failed Channel projection",
				ErrAdmissionIntegrity,
			)
		}
		result.ErrorClassification = channel.Attempt.ErrorClassification
	default:
		return TerminalRunResult{}, loopReadIntegrity(
			"terminal Channel state",
			ErrAdmissionIntegrity,
		)
	}
	return result, nil
}

func containsTerminalID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func terminalLastEventKind(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	sequence uint64,
) (string, error) {
	var kind string
	if err := queryer.QueryRowContext(ctx, `
		SELECT event_kind FROM run_events
		WHERE run_id=? AND event_sequence=?
	`, runID, int64(sequence)).Scan(&kind); err != nil {
		return "", loopReadIntegrity("terminal event kind", err)
	}
	return kind, nil
}

func loadActionTerminalEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	sequence uint64,
) (actionDispatchEventV1, error) {
	var (
		kind        string
		payloadRef  string
		digest      string
		contentKind string
		mediaType   string
		canonical   []byte
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT e.event_kind, e.payload_ref, e.payload_digest,
		       c.kind, c.media_type, c.canonical_bytes
		FROM run_events AS e
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=? AND e.event_sequence=?
	`, runID, int64(sequence)).Scan(
		&kind,
		&payloadRef,
		&digest,
		&contentKind,
		&mediaType,
		&canonical,
	); err != nil {
		return actionDispatchEventV1{}, err
	}
	computed, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil || kind != actionDispatchTerminalEvent ||
		payloadRef != digest || digest != computed ||
		contentKind != string(ContentRunEventPayload) ||
		mediaType != admissionJSONMediaType {
		return actionDispatchEventV1{}, loopReadIntegrity(
			"Action terminal event content",
			ErrAdmissionIntegrity,
		)
	}
	var event actionDispatchEventV1
	if err := json.Unmarshal(canonical, &event); err != nil {
		return actionDispatchEventV1{}, err
	}
	rebuilt, rebuiltDigest, err := prepareActionDispatchEvent(event)
	if err != nil || rebuiltDigest != digest || !bytes.Equal(rebuilt, canonical) ||
		event.RunID != runID {
		return actionDispatchEventV1{}, loopReadIntegrity(
			"Action terminal event wire",
			err,
		)
	}
	return event, nil
}

func loadChannelTerminalEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	sequence uint64,
) (channelDispatchEventV1, error) {
	var (
		kind        string
		payloadRef  string
		digest      string
		contentKind string
		mediaType   string
		canonical   []byte
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT e.event_kind, e.payload_ref, e.payload_digest,
		       c.kind, c.media_type, c.canonical_bytes
		FROM run_events AS e
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=? AND e.event_sequence=?
	`, runID, int64(sequence)).Scan(
		&kind,
		&payloadRef,
		&digest,
		&contentKind,
		&mediaType,
		&canonical,
	); err != nil {
		return channelDispatchEventV1{}, err
	}
	computed, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil || kind != channelDispatchTerminalEvent ||
		payloadRef != digest || digest != computed ||
		contentKind != string(ContentRunEventPayload) ||
		mediaType != admissionJSONMediaType {
		return channelDispatchEventV1{}, loopReadIntegrity(
			"Channel terminal event content",
			ErrAdmissionIntegrity,
		)
	}
	var event channelDispatchEventV1
	if err := json.Unmarshal(canonical, &event); err != nil {
		return channelDispatchEventV1{}, err
	}
	rebuilt, rebuiltDigest, err := prepareChannelDispatchEvent(event)
	if err != nil || rebuiltDigest != digest || !bytes.Equal(rebuilt, canonical) ||
		event.RunID != runID {
		return channelDispatchEventV1{}, loopReadIntegrity(
			"Channel terminal event wire",
			err,
		)
	}
	return event, nil
}

func loadTerminalHistoryContent(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	attempt ModelDispatchAttemptRecord,
) (ContentRecord, error) {
	var (
		sequence      int64
		memberID      string
		role          string
		contentRef    string
		contentDigest string
		sourceAttempt sql.NullString
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			history_sequence,
			member_id,
			role,
			content_ref,
			content_digest,
			source_attempt_id
		FROM history_entries
		WHERE run_id=?
	`, runID).Scan(
		&sequence,
		&memberID,
		&role,
		&contentRef,
		&contentDigest,
		&sourceAttempt,
	)
	if err != nil {
		return ContentRecord{}, loopReadIntegrity(
			"terminal History",
			err,
		)
	}
	if sequence != 1 ||
		memberID != attempt.MemberID ||
		role != string(moduleapi.ModelRoleAssistant) ||
		contentRef != contentDigest ||
		contentRef != attempt.ResultRef ||
		!sourceAttempt.Valid ||
		sourceAttempt.String != attempt.AttemptID {
		return ContentRecord{}, loopReadIntegrity(
			"terminal History identity",
			ErrAdmissionIntegrity,
		)
	}
	content, err := queryContent(ctx, connection, contentRef)
	if err != nil || content.Kind != ContentModelResult {
		return ContentRecord{}, loopReadIntegrity(
			"terminal History content",
			err,
		)
	}
	return content, nil
}
