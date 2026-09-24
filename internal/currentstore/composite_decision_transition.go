package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

var (
	ErrInvalidCompositeDecisionTransition = errors.New(
		"currentstore: invalid Composite decision transition",
	)
	ErrCompositeDecisionTransitionConflict = errors.New(
		"currentstore: Composite decision transition conflict",
	)
	ErrCompositeDecisionTransitionIntegrity = errors.New(
		"currentstore: Composite decision transition integrity violation",
	)
)

// ApplyCompositeDecisionInput intentionally contains only the leased root.
// Decision, affected slots and repair routing are recovered from the exact
// terminal Reviewer result and the immutable root plan inside one write
// transaction; callers cannot supply or widen them.
type ApplyCompositeDecisionInput struct {
	Lease RunLease
}

type ApplyCompositeDecisionResult struct {
	Applied          bool
	Decision         corecontract.CollaborationReviewDecisionV1
	SourceVerdictRef string
	ActivatedRunIDs  []string
	SkippedRunIDs    []string
}

type compositeRepairTransitionKind string

const (
	compositeRepairTransitionActivate compositeRepairTransitionKind = "ACTIVATE"
	compositeRepairTransitionSkip     compositeRepairTransitionKind = "SKIP"
)

type compositeRepairTransition struct {
	runID        string
	parentSlotID string
	role         corecontract.CompositeRunRoleV1
	kind         compositeRepairTransitionKind
}

// ApplyCompositeDecision atomically closes the ROOT_TRANSITION frontier. It
// either activates the exact affected round-one Specialist Runs and their
// pre-frozen Reviewer, or permanently skips every dormant repair Run. A
// process crash before COMMIT leaves every node dormant; an exact retry after
// COMMIT observes event sequence one and performs no second transition.
func (store *Store) ApplyCompositeDecision(
	ctx context.Context,
	input ApplyCompositeDecisionInput,
) (ApplyCompositeDecisionResult, error) {
	if ctx == nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidCompositeDecisionTransition,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidCompositeDecisionTransition,
			err,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return ApplyCompositeDecisionResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"currentstore: acquire Composite decision connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"currentstore: begin Composite decision transition: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	run, err := loadRunForLoop(ctx, connection, input.Lease)
	if err != nil {
		if errors.Is(err, ErrRunLeaseConflict) ||
			errors.Is(err, ErrRunLeaseUnavailable) {
			return ApplyCompositeDecisionResult{}, fmt.Errorf(
				"%w: %v",
				ErrCompositeDecisionTransitionConflict,
				err,
			)
		}
		return ApplyCompositeDecisionResult{}, err
	}
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		run.Manifest.Composite.Plan == nil ||
		run.Manifest.Composite.Plan.Decision == nil ||
		run.Frame.Step != corecontract.WaitingChildrenLoopStep ||
		run.State != corecontract.InitialRunState ||
		run.Disposition != "WAITING_EXTERNAL" ||
		run.Frame.WaitingReason != compositeChildrenPendingWaitingReason ||
		run.CancellationRef != "" {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: leased Run is not an uncancelled W5 root transition",
			ErrCompositeDecisionTransitionConflict,
		)
	}
	// The root projection follows the current decision frontier. Once affected
	// repair Specialists terminate it therefore exposes the dormant round-one
	// Reviewer, not the round-zero verdict that authorizes this transition.
	// Always recover that source verdict from the immutable initial Children so
	// an exact second Apply can activate Reviewer1 without trusting a moving
	// projection.
	initialChildren, err := loadCompositeChildResults(
		ctx,
		connection,
		run.Manifest,
	)
	if err != nil {
		return ApplyCompositeDecisionResult{}, err
	}
	reviewer, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		run.Manifest,
		initialChildren,
		0,
	)
	if err != nil {
		return ApplyCompositeDecisionResult{}, err
	}
	if reviewer == nil ||
		reviewer.RepairRound != 0 ||
		reviewer.State != CompositeReviewerSucceededV1 ||
		reviewer.ResultRef == "" ||
		reviewer.CollaborationVerdict == nil ||
		reviewer.ContributionSet == nil ||
		reviewer.ContributionSetDigest == "" {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: exact round-zero Reviewer verdict is not terminal",
			ErrCompositeDecisionTransitionConflict,
		)
	}
	verdict := *reviewer.CollaborationVerdict
	if err := verdict.ValidateForCollaborationReviewV1(
		run.Manifest,
		reviewer.ContributionSetDigest,
		0,
	); err != nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: round-zero Reviewer verdict: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	transitions, result, err := compositeRepairTransitions(
		run.Manifest,
		verdict,
		reviewer.ResultRef,
	)
	if err != nil {
		return ApplyCompositeDecisionResult{}, err
	}

	applied, err := inspectCompositeRepairTransitionEvents(
		ctx,
		connection,
		run.Manifest,
		reviewer.ResultRef,
		transitions,
	)
	if err != nil {
		return ApplyCompositeDecisionResult{}, err
	}
	if applied {
		result.Applied = false
		if verdict.Decision ==
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
			activated, activateErr := activateCompositeRepairReviewerIfReady(
				ctx,
				connection,
				run.Manifest,
				reviewer.ResultRef,
				nowUnixMicro(),
			)
			if activateErr != nil {
				return ApplyCompositeDecisionResult{}, activateErr
			}
			if activated {
				reviewerRunID := run.Manifest.Composite.Plan.Decision.
					RepairReviewer.RunID
				if err := appendCompositeTransitionRunObservationV1(
					ctx, connection, reviewerRunID,
				); err != nil {
					return ApplyCompositeDecisionResult{}, err
				}
				result.Applied = true
				result.ActivatedRunIDs = append(
					result.ActivatedRunIDs,
					reviewerRunID,
				)
			}
		}
		verifiedRunIDs := make(map[string]struct{}, len(transitions)+1)
		for _, transition := range transitions {
			verifiedRunIDs[transition.runID] = struct{}{}
		}
		if verdict.Decision == corecontract.CollaborationReviewDecisionRepairRequiredV1 {
			verifiedRunIDs[run.Manifest.Composite.Plan.Decision.RepairReviewer.RunID] = struct{}{}
		}
		for runID := range verifiedRunIDs {
			if err := verifyCurrentRunObservationV1(ctx, connection, runID); err != nil {
				return ApplyCompositeDecisionResult{}, err
			}
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return ApplyCompositeDecisionResult{}, fmt.Errorf(
				"currentstore: commit idempotent Composite decision transition: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}

	updatedAt := nowUnixMicro()
	if updatedAt <= 0 {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: invalid transition time",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	for _, transition := range transitions {
		switch transition.kind {
		case compositeRepairTransitionActivate:
			if err := activateCompositeRepairRun(
				ctx,
				connection,
				run.Manifest,
				reviewer.ResultRef,
				transition,
				updatedAt,
			); err != nil {
				return ApplyCompositeDecisionResult{}, err
			}
		case compositeRepairTransitionSkip:
			if err := skipCompositeRepairRun(
				ctx,
				connection,
				run.Manifest,
				reviewer.ResultRef,
				transition,
				updatedAt,
			); err != nil {
				return ApplyCompositeDecisionResult{}, err
			}
		default:
			return ApplyCompositeDecisionResult{}, fmt.Errorf(
				"%w: unsupported repair transition",
				ErrCompositeDecisionTransitionIntegrity,
			)
		}
		if err := appendCompositeTransitionRunObservationV1(
			ctx, connection, transition.runID,
		); err != nil {
			return ApplyCompositeDecisionResult{}, err
		}
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ApplyCompositeDecisionResult{}, fmt.Errorf(
			"currentstore: commit Composite decision transition: %w",
			err,
		)
	}
	committed = true
	result.Applied = true
	return result, nil
}

func activateCompositeRepairReviewerIfReady(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	verdictRef string,
	updatedAt int64,
) (bool, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return false, fmt.Errorf(
			"%w: repair Reviewer plan is absent",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	effective, round, err := loadCompositeEffectiveChildResults(
		ctx,
		connection,
		root,
	)
	if err != nil {
		return false, err
	}
	if round != corecontract.CompositeRepairRoundOneV1 ||
		!allCompositeChildrenSucceeded(effective) {
		return false, nil
	}
	planned := root.Composite.Plan.Decision.RepairReviewer
	transition := compositeRepairTransition{
		runID:        planned.RunID,
		parentSlotID: planned.ParentSlotID,
		role:         corecontract.CompositeRunRoleReviewerV1,
		kind:         compositeRepairTransitionActivate,
	}
	var count int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM run_events
		WHERE run_id=? AND event_sequence=1
	`, planned.RunID).Scan(&count); err != nil || count < 0 || count > 1 {
		return false, fmt.Errorf(
			"%w: inspect repair Reviewer activation: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	if count == 1 {
		if err := verifyCompositeRepairTransitionEvent(
			ctx,
			connection,
			root,
			verdictRef,
			transition,
		); err != nil {
			return false, err
		}
		return false, nil
	}
	if updatedAt <= 0 {
		return false, fmt.Errorf(
			"%w: invalid repair Reviewer activation time",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	if err := activateCompositeRepairRun(
		ctx,
		connection,
		root,
		verdictRef,
		transition,
		updatedAt,
	); err != nil {
		return false, err
	}
	return true, nil
}

func compositeRepairTransitions(
	root corecontract.RunManifest,
	verdict corecontract.CollaborationReviewVerdictV1,
	verdictRef string,
) ([]compositeRepairTransition, ApplyCompositeDecisionResult, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil ||
		verdict.ValidateForCollaborationReviewV1(
			root,
			verdict.ContributionSetDigest,
			0,
		) != nil {
		return nil, ApplyCompositeDecisionResult{}, fmt.Errorf(
			"%w: invalid frozen Composite decision",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	affected := make(map[string]struct{}, len(verdict.AffectedSlotIDs))
	for _, slotID := range verdict.AffectedSlotIDs {
		affected[slotID] = struct{}{}
	}
	decision := root.Composite.Plan.Decision
	transitions := make(
		[]compositeRepairTransition,
		0,
		len(decision.RepairChildren)+1,
	)
	result := ApplyCompositeDecisionResult{
		Decision:         verdict.Decision,
		SourceVerdictRef: verdictRef,
		ActivatedRunIDs:  []string{},
		SkippedRunIDs:    []string{},
	}
	for _, planned := range decision.RepairChildren {
		kind := compositeRepairTransitionSkip
		if verdict.Decision ==
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
			if _, ok := affected[planned.SlotID]; ok {
				kind = compositeRepairTransitionActivate
			}
		}
		transitions = append(transitions, compositeRepairTransition{
			runID:        planned.RunID,
			parentSlotID: planned.ParentSlotID,
			role:         corecontract.CompositeRunRoleChildV1,
			kind:         kind,
		})
		if kind == compositeRepairTransitionActivate {
			result.ActivatedRunIDs = append(result.ActivatedRunIDs, planned.RunID)
		} else {
			result.SkippedRunIDs = append(result.SkippedRunIDs, planned.RunID)
		}
	}
	// Round-one Reviewer stays dormant while affected Specialists execute.
	// Terminal decisions have no round one, so their unused Reviewer is
	// skipped together with every repair Child.
	if verdict.Decision !=
		corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		transitions = append(transitions, compositeRepairTransition{
			runID:        decision.RepairReviewer.RunID,
			parentSlotID: decision.RepairReviewer.ParentSlotID,
			role:         corecontract.CompositeRunRoleReviewerV1,
			kind:         compositeRepairTransitionSkip,
		})
		result.SkippedRunIDs = append(
			result.SkippedRunIDs,
			decision.RepairReviewer.RunID,
		)
	}
	return transitions, result, nil
}

func inspectCompositeRepairTransitionEvents(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	verdictRef string,
	transitions []compositeRepairTransition,
) (bool, error) {
	applied := 0
	for _, transition := range transitions {
		var count int
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM run_events
			WHERE run_id=? AND event_sequence=1
		`, transition.runID).Scan(&count); err != nil || count < 0 || count > 1 {
			return false, fmt.Errorf(
				"%w: inspect repair transition event: %v",
				ErrCompositeDecisionTransitionIntegrity,
				err,
			)
		}
		if count == 0 {
			continue
		}
		applied++
		if err := verifyCompositeRepairTransitionEvent(
			ctx,
			connection,
			root,
			verdictRef,
			transition,
		); err != nil {
			return false, err
		}
	}
	if applied != 0 && applied != len(transitions) {
		return false, fmt.Errorf(
			"%w: repair transition is partially persisted",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	return applied == len(transitions), nil
}

func verifyCompositeRepairTransitionEvent(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	verdictRef string,
	transition compositeRepairTransition,
) error {
	var (
		kind         string
		fromRevision int64
		toRevision   int64
		payload      string
		digest       string
		storedKind   string
		mediaType    string
		canonical    []byte
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT e.event_kind, e.from_revision, e.to_revision,
		       e.payload_ref, e.payload_digest,
		       c.kind, c.media_type, c.canonical_bytes
		FROM run_events AS e
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=? AND e.event_sequence=1
	`, transition.runID).Scan(
		&kind,
		&fromRevision,
		&toRevision,
		&payload,
		&digest,
		&storedKind,
		&mediaType,
		&canonical,
	); err != nil {
		return fmt.Errorf(
			"%w: load repair transition event: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	computed, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil || payload != digest || digest != computed ||
		fromRevision != 0 || toRevision != 1 ||
		storedKind != string(ContentRunEventPayload) ||
		mediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: repair transition event content differs",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	switch transition.kind {
	case compositeRepairTransitionActivate:
		event, err := corecontract.RestoreCompositeRepairActivatedEventV1(
			canonical,
		)
		if err != nil || kind != corecontract.CompositeRepairActivatedEventKind ||
			event.RunID != transition.runID ||
			event.RootRunID != root.RunID ||
			event.RootManifestDigest != root.ManifestDigest ||
			event.ParentSlotID != transition.parentSlotID ||
			event.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			event.SourceVerdictRef != verdictRef {
			return fmt.Errorf(
				"%w: repair activation event differs: %v",
				ErrCompositeDecisionTransitionIntegrity,
				err,
			)
		}
	case compositeRepairTransitionSkip:
		event, err := corecontract.RestoreCompositeRepairSkippedEventV1(
			canonical,
		)
		if err != nil || kind != corecontract.CompositeRepairSkippedEventKind ||
			event.RunID != transition.runID ||
			event.RootRunID != root.RunID ||
			event.RootManifestDigest != root.ManifestDigest ||
			event.ParentSlotID != transition.parentSlotID ||
			event.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			event.SourceVerdictRef != verdictRef ||
			event.Reason != corecontract.CompositeRepairSkippedReasonV1 {
			return fmt.Errorf(
				"%w: repair skip event differs: %v",
				ErrCompositeDecisionTransitionIntegrity,
				err,
			)
		}
	default:
		return fmt.Errorf(
			"%w: unsupported repair transition event",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	return verifyCompositeRepairPostTransitionProjection(
		ctx,
		connection,
		transition,
	)
}

func verifyCompositeRepairPostTransitionProjection(
	ctx context.Context,
	connection readQueryerV1,
	transition compositeRepairTransition,
) error {
	var (
		state           string
		disposition     sql.NullString
		runRevision     int64
		frameRevision   int64
		step            string
		continuation    []byte
		pendingModel    sql.NullString
		pendingDispatch sql.NullString
		waiting         sql.NullString
		lastEvent       int64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT r.state, r.disposition, r.revision,
		       f.frame_revision, f.step, f.continuation,
		       f.pending_attempt_id, f.pending_dispatch_attempt_id,
		       f.waiting_reason, f.last_authoritative_event
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, transition.runID).Scan(
		&state,
		&disposition,
		&runRevision,
		&frameRevision,
		&step,
		&continuation,
		&pendingModel,
		&pendingDispatch,
		&waiting,
		&lastEvent,
	); err != nil || runRevision < 1 || frameRevision < 1 || lastEvent < 1 {
		return fmt.Errorf(
			"%w: repair post-transition head differs: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil {
		return fmt.Errorf(
			"%w: repair post-transition continuation: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	switch transition.kind {
	case compositeRepairTransitionSkip:
		if runRevision != 1 || frameRevision != 1 || lastEvent != 1 ||
			state != corecontract.TerminatedLoopStep ||
			!disposition.Valid ||
			disposition.String != corecontract.TerminatedLoopStep ||
			step != corecontract.TerminatedLoopStep || pendingModel.Valid ||
			pendingDispatch.Valid || waiting.Valid ||
			continued.CoreFailureReason !=
				corecontract.CompositeRepairSkippedReasonV1 {
			return fmt.Errorf(
				"%w: skipped repair Run projection differs",
				ErrCompositeDecisionTransitionIntegrity,
			)
		}
		var modelCount, dispatchCount, historyCount int64
		if err := connection.QueryRowContext(ctx, `
			SELECT
			 (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			 (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
			 (SELECT COUNT(*) FROM history_entries WHERE run_id=?)
		`, transition.runID, transition.runID, transition.runID).Scan(
			&modelCount,
			&dispatchCount,
			&historyCount,
		); err != nil || modelCount != 0 || dispatchCount != 0 ||
			historyCount != 0 {
			return fmt.Errorf(
				"%w: skipped repair Run contains execution",
				ErrCompositeDecisionTransitionIntegrity,
			)
		}
	case compositeRepairTransitionActivate:
		var transitionEventCount, transitionEventMin, transitionEventMax int64
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*), COALESCE(MIN(event_sequence), -1),
			       COALESCE(MAX(event_sequence), -1)
			FROM run_events
			WHERE run_id=? AND event_kind IN (?, ?)
		`,
			transition.runID,
			corecontract.CompositeRepairActivatedEventKind,
			corecontract.CompositeRepairSkippedEventKind,
		).Scan(
			&transitionEventCount,
			&transitionEventMin,
			&transitionEventMax,
		); err != nil || transitionEventCount != 1 ||
			transitionEventMin != 1 || transitionEventMax != 1 {
			return fmt.Errorf(
				"%w: repair activation is not the unique sequence-one transition",
				ErrCompositeDecisionTransitionIntegrity,
			)
		}
		if step == corecontract.WaitingRepairActivationLoopStep ||
			continued.State == corecontract.WaitingRepairActivationLoopStep ||
			state == corecontract.TerminatedLoopStep &&
				continued.CoreFailureReason ==
					corecontract.CompositeRepairSkippedReasonV1 {
			return fmt.Errorf(
				"%w: activated repair Run returned to dormant or skipped state",
				ErrCompositeDecisionTransitionIntegrity,
			)
		}
		if runRevision == 1 && frameRevision == 1 && lastEvent == 1 {
			switch transition.role {
			case corecontract.CompositeRunRoleChildV1:
				if state != corecontract.InitialRunState ||
					disposition.Valid || step != corecontract.InitialLoopStep ||
					pendingModel.Valid || pendingDispatch.Valid || waiting.Valid ||
					continued.State != corecontract.InitialLoopStep {
					return fmt.Errorf(
						"%w: activated repair Child head differs",
						ErrCompositeDecisionTransitionIntegrity,
					)
				}
			case corecontract.CompositeRunRoleReviewerV1:
				if state != corecontract.InitialRunState ||
					!disposition.Valid ||
					disposition.String != "WAITING_EXTERNAL" ||
					step != corecontract.WaitingChildrenLoopStep ||
					pendingModel.Valid || pendingDispatch.Valid ||
					!waiting.Valid ||
					waiting.String != compositeChildrenPendingWaitingReason ||
					continued.State != corecontract.WaitingChildrenLoopStep {
					return fmt.Errorf(
						"%w: activated repair Reviewer head differs",
						ErrCompositeDecisionTransitionIntegrity,
					)
				}
			default:
				return fmt.Errorf(
					"%w: activated repair role differs",
					ErrCompositeDecisionTransitionIntegrity,
				)
			}
		} else {
			frame := LoopFrameRecord{
				RunID:                    transition.runID,
				Revision:                 uint64(frameRevision),
				Step:                     step,
				Continuation:             bytes.Clone(continuation),
				PendingAttemptID:         pendingModel.String,
				PendingDispatchAttemptID: pendingDispatch.String,
				WaitingReason:            waiting.String,
				LastAuthoritativeEvent:   uint64(lastEvent),
			}
			if continued.State != step {
				return fmt.Errorf(
					"%w: repair continuation does not match its Frame",
					ErrCompositeDecisionTransitionIntegrity,
				)
			}
			if err := validateLoopRunFrameProjection(
				state,
				disposition.String,
				frame,
			); err != nil {
				return fmt.Errorf(
					"%w: repair Run/Frame projection: %v",
					ErrCompositeDecisionTransitionIntegrity,
					err,
				)
			}
			if err := validateFairTargetContinuationPointers(
				continued,
				pendingModel,
				pendingDispatch,
			); err != nil {
				return fmt.Errorf(
					"%w: repair continuation projection: %v",
					ErrCompositeDecisionTransitionIntegrity,
					err,
				)
			}
			if err := verifyRunEventHead(
				ctx,
				connection,
				transition.runID,
				uint64(lastEvent),
			); err != nil {
				return fmt.Errorf(
					"%w: repair event head: %v",
					ErrCompositeDecisionTransitionIntegrity,
					err,
				)
			}
			if step == corecontract.TerminatedLoopStep {
				if _, err := loadTerminalRunResult(
					ctx,
					connection,
					transition.runID,
				); err != nil {
					return fmt.Errorf(
						"%w: repair terminal projection: %v",
						ErrCompositeDecisionTransitionIntegrity,
						err,
					)
				}
			} else if err := verifyFairTargetAttemptProjection(
				ctx,
				connection,
				transition.runID,
				continued,
			); err != nil {
				return fmt.Errorf(
					"%w: repair Attempt projection: %v",
					ErrCompositeDecisionTransitionIntegrity,
					err,
				)
			}
		}
	default:
		return fmt.Errorf(
			"%w: unsupported repair transition projection",
			ErrCompositeDecisionTransitionIntegrity,
		)
	}
	return nil
}

type dormantCompositeRepairHead struct {
	runRevision uint64
	frame       LoopFrameRecord
}

func loadDormantCompositeRepairHead(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	transition compositeRepairTransition,
) (dormantCompositeRepairHead, error) {
	var (
		state           string
		disposition     sql.NullString
		revision        int64
		parentRun       sql.NullString
		parentDigest    sql.NullString
		parentSlot      sql.NullString
		cancelRef       sql.NullString
		frameRevision   int64
		step            string
		budgetRef       string
		continuation    []byte
		pendingModel    sql.NullString
		pendingDispatch sql.NullString
		waitingReason   sql.NullString
		lastEvent       int64
		leaseOwner      sql.NullString
		leaseEpoch      int64
		leaseExpiry     sql.NullInt64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT r.state, r.disposition, r.revision,
		       r.parent_run_id, r.parent_manifest_digest, r.parent_slot_id,
		       r.cancel_request_ref,
		       f.frame_revision, f.step, f.usage_ledger_ref, f.continuation,
		       f.pending_attempt_id, f.pending_dispatch_attempt_id,
		       f.waiting_reason, f.last_authoritative_event,
		       f.lease_owner, f.lease_epoch, f.lease_expiry
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, transition.runID).Scan(
		&state,
		&disposition,
		&revision,
		&parentRun,
		&parentDigest,
		&parentSlot,
		&cancelRef,
		&frameRevision,
		&step,
		&budgetRef,
		&continuation,
		&pendingModel,
		&pendingDispatch,
		&waitingReason,
		&lastEvent,
		&leaseOwner,
		&leaseEpoch,
		&leaseExpiry,
	); err != nil {
		return dormantCompositeRepairHead{}, fmt.Errorf(
			"%w: load dormant repair Run: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || state != corecontract.InitialRunState ||
		!disposition.Valid || disposition.String != "WAITING_EXTERNAL" ||
		revision != 0 || !parentRun.Valid || parentRun.String != root.RunID ||
		!parentDigest.Valid || parentDigest.String != root.ManifestDigest ||
		!parentSlot.Valid || parentSlot.String != transition.parentSlotID ||
		cancelRef.Valid || frameRevision != 0 ||
		step != corecontract.WaitingRepairActivationLoopStep ||
		continued.State != corecontract.WaitingRepairActivationLoopStep ||
		pendingModel.Valid || pendingDispatch.Valid ||
		!waitingReason.Valid ||
		waitingReason.String != compositeRepairDormantWaitingReason ||
		lastEvent != 0 || leaseOwner.Valid || leaseEpoch != 0 || leaseExpiry.Valid {
		return dormantCompositeRepairHead{}, fmt.Errorf(
			"%w: repair Run %q is not the exact dormant admission head",
			ErrCompositeDecisionTransitionConflict,
			transition.runID,
		)
	}
	wantBudgetRef, _, err :=
		corecontract.NewWaitingRepairActivationLoopState(transition.runID)
	if err != nil || budgetRef != wantBudgetRef {
		return dormantCompositeRepairHead{}, fmt.Errorf(
			"%w: repair Run %q budget state differs",
			ErrCompositeDecisionTransitionIntegrity,
			transition.runID,
		)
	}
	var modelCount, dispatchCount, historyCount int64
	if err := connection.QueryRowContext(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
		  (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
		  (SELECT COUNT(*) FROM history_entries WHERE run_id=?)
	`, transition.runID, transition.runID, transition.runID).Scan(
		&modelCount,
		&dispatchCount,
		&historyCount,
	); err != nil || modelCount != 0 || dispatchCount != 0 || historyCount != 0 {
		return dormantCompositeRepairHead{}, fmt.Errorf(
			"%w: dormant repair Run %q contains hidden execution",
			ErrCompositeDecisionTransitionIntegrity,
			transition.runID,
		)
	}
	return dormantCompositeRepairHead{
		runRevision: uint64(revision),
		frame: LoopFrameRecord{
			RunID:                  transition.runID,
			Revision:               uint64(frameRevision),
			Step:                   step,
			UsageLedgerRef:         budgetRef,
			Continuation:           bytes.Clone(continuation),
			WaitingReason:          waitingReason.String,
			LastAuthoritativeEvent: uint64(lastEvent),
		},
	}, nil
}

func activateCompositeRepairRun(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	verdictRef string,
	transition compositeRepairTransition,
	updatedAt int64,
) error {
	head, err := loadDormantCompositeRepairHead(
		ctx,
		connection,
		root,
		transition,
	)
	if err != nil {
		return err
	}
	nextRunRevision, err := incrementSQLiteUint(
		head.runRevision,
		"Composite repair activation Run revision",
	)
	if err != nil {
		return err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		head.frame.Revision,
		"Composite repair activation Frame revision",
	)
	if err != nil {
		return err
	}
	nextEvent, err := incrementSQLiteUint(
		head.frame.LastAuthoritativeEvent,
		"Composite repair activation event sequence",
	)
	if err != nil {
		return err
	}
	var (
		nextStep        string
		nextDisposition any
		nextWaiting     any
		budgetRef       string
		continuation    []byte
	)
	switch transition.role {
	case corecontract.CompositeRunRoleChildV1:
		nextStep = corecontract.InitialLoopStep
		nextDisposition = nil
		nextWaiting = nil
		budgetRef, continuation, err =
			corecontract.NewInitialLoopState(transition.runID)
	case corecontract.CompositeRunRoleReviewerV1:
		nextStep = corecontract.WaitingChildrenLoopStep
		nextDisposition = "WAITING_EXTERNAL"
		nextWaiting = compositeChildrenPendingWaitingReason
		budgetRef, continuation, err =
			corecontract.NewWaitingChildrenLoopState(transition.runID)
	default:
		err = ErrAdmissionIntegrity
	}
	if err != nil || budgetRef != head.frame.UsageLedgerRef {
		return fmt.Errorf(
			"%w: construct repair activation continuation: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	_, eventCanonical, err := corecontract.NewCompositeRepairActivatedEventV1(
		corecontract.CompositeRepairActivatedEventV1{
			SchemaVersion:      corecontract.CompositeRepairActivatedEventSchemaVersionV1,
			RunID:              transition.runID,
			RootRunID:          root.RunID,
			RootManifestDigest: root.ManifestDigest,
			ParentSlotID:       transition.parentSlotID,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			SourceVerdictRef:   verdictRef,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"%w: construct repair activation event: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	eventDigest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		eventCanonical,
	)
	if err != nil {
		return err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest:         eventDigest,
		Kind:           ContentRunEventPayload,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, updatedAt); err != nil {
		return fmt.Errorf(
			"%w: persist repair activation event: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=? AND state=? AND disposition='WAITING_EXTERNAL'
		  AND revision=? AND cancel_request_ref IS NULL
	`,
		corecontract.InitialRunState,
		nextDisposition,
		int64(nextRunRevision),
		updatedAt,
		transition.runID,
		corecontract.InitialRunState,
		int64(head.runRevision),
	)
	if err != nil || requireModelCASRow(
		runUpdate,
		"activate Composite repair Run",
	) != nil {
		return fmt.Errorf(
			"%w: activate repair Run: %v",
			ErrCompositeDecisionTransitionConflict,
			err,
		)
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, usage_ledger_ref=?, continuation=?,
		    waiting_reason=?, last_authoritative_event=?
		WHERE run_id=? AND frame_revision=? AND step=?
		  AND pending_attempt_id IS NULL
		  AND pending_dispatch_attempt_id IS NULL
		  AND waiting_reason=? AND last_authoritative_event=0
		  AND lease_owner IS NULL AND lease_epoch=0 AND lease_expiry IS NULL
	`,
		int64(nextFrameRevision),
		nextStep,
		budgetRef,
		continuation,
		nextWaiting,
		int64(nextEvent),
		transition.runID,
		int64(head.frame.Revision),
		corecontract.WaitingRepairActivationLoopStep,
		compositeRepairDormantWaitingReason,
	)
	if err != nil || requireModelCASRow(
		frameUpdate,
		"activate Composite repair Frame",
	) != nil {
		return fmt.Errorf(
			"%w: activate repair Frame: %v",
			ErrCompositeDecisionTransitionConflict,
			err,
		)
	}
	if err := appendCompositeRepairTransitionEvent(
		ctx,
		connection,
		transition.runID,
		nextEvent,
		head.frame.Revision,
		nextFrameRevision,
		corecontract.CompositeRepairActivatedEventKind,
		eventDigest,
		updatedAt,
	); err != nil {
		return err
	}
	return nil
}

func skipCompositeRepairRun(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	verdictRef string,
	transition compositeRepairTransition,
	updatedAt int64,
) error {
	head, err := loadDormantCompositeRepairHead(
		ctx,
		connection,
		root,
		transition,
	)
	if err != nil {
		return err
	}
	nextRunRevision, err := incrementSQLiteUint(
		head.runRevision,
		"Composite repair skip Run revision",
	)
	if err != nil {
		return err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		head.frame.Revision,
		"Composite repair skip Frame revision",
	)
	if err != nil {
		return err
	}
	nextEvent, err := incrementSQLiteUint(
		head.frame.LastAuthoritativeEvent,
		"Composite repair skip event sequence",
	)
	if err != nil {
		return err
	}
	continuation, err := corecontract.NewCoreFailureLoopContinuationV1(
		corecontract.CompositeRepairSkippedReasonV1,
	)
	if err != nil {
		return err
	}
	_, eventCanonical, err := corecontract.NewCompositeRepairSkippedEventV1(
		corecontract.CompositeRepairSkippedEventV1{
			SchemaVersion:      corecontract.CompositeRepairSkippedEventSchemaVersionV1,
			RunID:              transition.runID,
			RootRunID:          root.RunID,
			RootManifestDigest: root.ManifestDigest,
			ParentSlotID:       transition.parentSlotID,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			SourceVerdictRef:   verdictRef,
			Reason:             corecontract.CompositeRepairSkippedReasonV1,
		},
	)
	if err != nil {
		return err
	}
	eventDigest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		eventCanonical,
	)
	if err != nil {
		return err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest:         eventDigest,
		Kind:           ContentRunEventPayload,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, updatedAt); err != nil {
		return err
	}
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=? AND state=? AND disposition='WAITING_EXTERNAL'
		  AND revision=? AND cancel_request_ref IS NULL
	`,
		corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		int64(nextRunRevision),
		updatedAt,
		transition.runID,
		corecontract.InitialRunState,
		int64(head.runRevision),
	)
	if err != nil || requireModelCASRow(
		runUpdate,
		"skip Composite repair Run",
	) != nil {
		return fmt.Errorf(
			"%w: skip repair Run: %v",
			ErrCompositeDecisionTransitionConflict,
			err,
		)
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, continuation=?, waiting_reason=NULL,
		    last_authoritative_event=?
		WHERE run_id=? AND frame_revision=? AND step=?
		  AND pending_attempt_id IS NULL
		  AND pending_dispatch_attempt_id IS NULL
		  AND waiting_reason=? AND last_authoritative_event=0
		  AND lease_owner IS NULL AND lease_epoch=0 AND lease_expiry IS NULL
	`,
		int64(nextFrameRevision),
		corecontract.TerminatedLoopStep,
		continuation,
		int64(nextEvent),
		transition.runID,
		int64(head.frame.Revision),
		corecontract.WaitingRepairActivationLoopStep,
		compositeRepairDormantWaitingReason,
	)
	if err != nil || requireModelCASRow(
		frameUpdate,
		"skip Composite repair Frame",
	) != nil {
		return fmt.Errorf(
			"%w: skip repair Frame: %v",
			ErrCompositeDecisionTransitionConflict,
			err,
		)
	}
	return appendCompositeRepairTransitionEvent(
		ctx,
		connection,
		transition.runID,
		nextEvent,
		head.frame.Revision,
		nextFrameRevision,
		corecontract.CompositeRepairSkippedEventKind,
		eventDigest,
		updatedAt,
	)
}

func appendCompositeRepairTransitionEvent(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
	sequence uint64,
	fromRevision uint64,
	toRevision uint64,
	kind string,
	digest string,
	createdAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind,
			from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		runID,
		int64(sequence),
		kind,
		int64(fromRevision),
		int64(toRevision),
		digest,
		digest,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: append repair transition event: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(
		result,
		"append Composite repair transition event",
	); err != nil {
		return fmt.Errorf(
			"%w: %v",
			ErrCompositeDecisionTransitionIntegrity,
			err,
		)
	}
	return nil
}
