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
	ErrInvalidCoreDeterministicFailure = errors.New(
		"currentstore: invalid deterministic Core failure",
	)
	ErrCoreDeterministicFailureConflict = errors.New(
		"currentstore: deterministic Core failure conflict",
	)
	ErrCoreDeterministicFailureIntegrity = errors.New(
		"currentstore: deterministic Core failure integrity violation",
	)
)

type CommitCoreDeterministicFailureInput struct {
	Lease  RunLease
	Reason string
}

type CommitCoreDeterministicFailureResult struct {
	Lease   RunLease
	Applied bool
	Reason  string
}

// CommitCoreDeterministicFailure atomically terminates a Composite coordinator
// on a frozen Core decision made before any model or effect dispatch permit.
// The terminal fact is one RunEvent; no synthetic Attempt or History row
// exists.
func (store *Store) CommitCoreDeterministicFailure(
	ctx context.Context,
	input CommitCoreDeterministicFailureInput,
) (CommitCoreDeterministicFailureResult, error) {
	if ctx == nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidCoreDeterministicFailure,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidCoreDeterministicFailure,
			err,
		)
	}
	if err := corecontract.ValidateCoreDeterministicFailureReasonV1(
		input.Reason,
	); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidCoreDeterministicFailure,
			err,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"currentstore: acquire deterministic Core failure connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"currentstore: begin deterministic Core failure: %w",
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
			return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
				"%w: %v",
				ErrCoreDeterministicFailureConflict,
				err,
			)
		}
		return CommitCoreDeterministicFailureResult{}, err
	}
	if run.Frame.Step == corecontract.TerminatedLoopStep {
		result, err := reopenCoreDeterministicFailure(
			ctx,
			connection,
			input,
			run,
		)
		if err != nil {
			return CommitCoreDeterministicFailureResult{}, err
		}
		if err := verifyCurrentRunObservationV1(ctx, connection, run.Manifest.RunID); err != nil {
			return CommitCoreDeterministicFailureResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
				"currentstore: commit deterministic Core failure re-entry: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}
	if err := validateCoreDeterministicFailureSource(run, input.Reason); err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}

	nextRunRevision, err := incrementSQLiteUint(
		run.RunRevision,
		"deterministic Core failure Run revision",
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		run.Frame.Revision,
		"deterministic Core failure Frame revision",
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"deterministic Core failure RunEvent sequence",
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	continuation, err := corecontract.NewCoreFailureLoopContinuationV1(
		input.Reason,
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	_, eventCanonical, err := corecontract.NewCoreDeterministicFailureEventV1(
		run.RunID,
		input.Reason,
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	eventDigest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		eventCanonical,
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	updatedAt := nowUnixMicro()
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest:         eventDigest,
		Kind:           ContentRunEventPayload,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, updatedAt); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: persist event content: %v",
			ErrCoreDeterministicFailureIntegrity,
			err,
		)
	}

	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET state=?, disposition=?, revision=?, updated_at=?
		WHERE run_id=?
		  AND state=?
		  AND disposition='WAITING_EXTERNAL'
		  AND revision=?
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
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: update Run: %v",
			ErrCoreDeterministicFailureIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(runUpdate, "terminate deterministic Core failure Run"); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: %v",
			ErrCoreDeterministicFailureConflict,
			err,
		)
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, continuation=?,
		    pending_attempt_id=NULL,
		    pending_dispatch_attempt_id=NULL,
		    waiting_reason=NULL,
		    last_authoritative_event=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND step=?
		  AND pending_attempt_id IS NULL
		  AND pending_dispatch_attempt_id IS NULL
		  AND waiting_reason=?
		  AND last_authoritative_event=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND lease_expiry>?
		  AND EXISTS(
		      SELECT 1 FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		        AND runs.state=?
		        AND runs.disposition=?
		  )
	`,
		int64(nextFrameRevision),
		corecontract.TerminatedLoopStep,
		continuation,
		int64(nextEvent),
		run.RunID,
		int64(input.Lease.FrameRevision),
		corecontract.WaitingChildrenLoopStep,
		compositeChildrenPendingWaitingReason,
		int64(run.Frame.LastAuthoritativeEvent),
		input.Lease.OwnerID,
		int64(input.Lease.LeaseEpoch),
		updatedAt,
		int64(nextRunRevision),
		corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
	)
	if err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: update Frame: %v",
			ErrCoreDeterministicFailureIntegrity,
			err,
		)
	}
	if err := requireModelCASRow(frameUpdate, "terminate deterministic Core failure Frame"); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: %v",
			ErrCoreDeterministicFailureConflict,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind,
			from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		corecontract.CoreDeterministicFailureEventKind,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		updatedAt,
	); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: append RunEvent: %v",
			ErrCoreDeterministicFailureIntegrity,
			err,
		)
	}
	if err := appendCoreFailureRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitCoreDeterministicFailureResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"currentstore: commit deterministic Core failure: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return CommitCoreDeterministicFailureResult{
		Lease:   nextLease,
		Applied: true,
		Reason:  input.Reason,
	}, nil
}

func validateCoreDeterministicFailureSource(
	run RunForLoop,
	reason string,
) error {
	if run.Manifest.Composite == nil {
		return fmt.Errorf(
			"%w: source is not an attempt-free WAITING_CHILDREN coordinator",
			ErrCoreDeterministicFailureConflict,
		)
	}
	role := run.Manifest.Composite.Role
	validCoordinator :=
		(role == corecontract.CompositeRunRoleRootV1 &&
			run.Manifest.Composite.Plan != nil) ||
			(role == corecontract.CompositeRunRoleReviewerV1 &&
				run.Manifest.Composite.Plan == nil &&
				run.Manifest.Composite.Assignment == nil)
	if !validCoordinator ||
		run.Frame.Step != corecontract.WaitingChildrenLoopStep ||
		run.State != corecontract.InitialRunState ||
		run.Disposition != "WAITING_EXTERNAL" ||
		run.Frame.WaitingReason != compositeChildrenPendingWaitingReason ||
		len(run.ModelDispatches) != 0 ||
		len(run.ActionDispatches) != 0 ||
		len(run.ChannelDispatches) != 0 ||
		len(run.History) != 0 {
		return fmt.Errorf(
			"%w: source is not an attempt-free WAITING_CHILDREN coordinator",
			ErrCoreDeterministicFailureConflict,
		)
	}
	switch reason {
	case corecontract.AllRequiredChildFailedReasonV1:
		for _, child := range run.CompositeChildren {
			if child.State == CompositeChildFailedV1 {
				return nil
			}
		}
		return fmt.Errorf(
			"%w: ALL_REQUIRED child policy has no failed Child",
			ErrCoreDeterministicFailureConflict,
		)
	case corecontract.CompositeChildResultOverBudgetReasonV1:
		if role == corecontract.CompositeRunRoleRootV1 &&
			allCompositeChildrenSucceeded(run.CompositeChildren) {
			return nil
		}
		return fmt.Errorf(
			"%w: over-budget source does not have all successful Children",
			ErrCoreDeterministicFailureConflict,
		)
	case corecontract.CompositeReviewRejectedReasonV1,
		corecontract.CompositeReviewFailedReasonV1,
		corecontract.CompositeReviewOutputInvalidReasonV1:
		if role == corecontract.CompositeRunRoleReviewerV1 &&
			reason == corecontract.CompositeReviewFailedReasonV1 &&
			allCompositeChildrenSucceeded(run.CompositeChildren) {
			// Reviewer context compilation (for example, protected
			// Specialist results exceeding its frozen budget) can fail
			// deterministically before a model Attempt exists.
			return nil
		}
		if role != corecontract.CompositeRunRoleRootV1 ||
			run.Manifest.Composite.Plan.Reviewer == nil ||
			!allCompositeChildrenSucceeded(run.CompositeChildren) ||
			run.CompositeReviewer == nil {
			return fmt.Errorf(
				"%w: review failure source does not close a Reviewer-enabled root",
				ErrCoreDeterministicFailureConflict,
			)
		}
		reviewer := run.CompositeReviewer
		switch reason {
		case corecontract.CompositeReviewRejectedReasonV1:
			if reviewer.State == CompositeReviewerSucceededV1 &&
				reviewer.Verdict != nil &&
				reviewer.Verdict.Decision == corecontract.ReviewDecisionRejectV1 {
				return nil
			}
		case corecontract.CompositeReviewOutputInvalidReasonV1:
			if reviewer.State == CompositeReviewerFailedV1 &&
				(reviewer.ErrorClassification ==
					"COMPOSITE_REVIEWER_VERDICT_INVALID" ||
					reviewer.ErrorClassification ==
						"COMPOSITE_REVIEWER_OUTPUT_INVALID" ||
					reviewer.ErrorClassification ==
						corecontract.CompositeReviewOutputInvalidReasonV1) {
				return nil
			}
		case corecontract.CompositeReviewFailedReasonV1:
			if reviewer.State == CompositeReviewerFailedV1 &&
				reviewer.ErrorClassification !=
					"COMPOSITE_REVIEWER_VERDICT_INVALID" &&
				reviewer.ErrorClassification !=
					"COMPOSITE_REVIEWER_OUTPUT_INVALID" &&
				reviewer.ErrorClassification !=
					corecontract.CompositeReviewOutputInvalidReasonV1 {
				return nil
			}
		}
		return fmt.Errorf(
			"%w: Reviewer projection does not prove reason %q",
			ErrCoreDeterministicFailureConflict,
			reason,
		)
	case corecontract.CollaborationReviewRejectedReasonV1,
		corecontract.CollaborationReviewFailedReasonV1,
		corecontract.CollaborationReviewOutputInvalidReasonV1,
		corecontract.CollaborationRepairLimitReachedReasonV1:
		decisionEnabled := role == corecontract.CompositeRunRoleRootV1 &&
			run.Manifest.Composite.Plan != nil &&
			run.Manifest.Composite.Plan.Decision != nil
		if role == corecontract.CompositeRunRoleReviewerV1 {
			decisionEnabled = run.CompositeRoot != nil &&
				run.CompositeRoot.Composite != nil &&
				run.CompositeRoot.Composite.Plan != nil &&
				run.CompositeRoot.Composite.Plan.Decision != nil
		}
		if !decisionEnabled {
			return fmt.Errorf(
				"%w: collaboration failure source lacks a Decision plan",
				ErrCoreDeterministicFailureConflict,
			)
		}
		if role == corecontract.CompositeRunRoleReviewerV1 &&
			reason == corecontract.CollaborationReviewFailedReasonV1 &&
			allCompositeChildrenSucceeded(run.CompositeChildren) {
			// A Reviewer can fail before dispatch when its exact structured
			// contribution set exceeds the frozen context budget.
			return nil
		}
		if role != corecontract.CompositeRunRoleRootV1 ||
			!allCompositeChildrenSucceeded(run.CompositeChildren) ||
			run.CompositeReviewer == nil {
			return fmt.Errorf(
				"%w: collaboration review failure does not close a Decision root",
				ErrCoreDeterministicFailureConflict,
			)
		}
		reviewer := run.CompositeReviewer
		invalidOutput := reviewer.ErrorClassification ==
			corecontract.CollaborationReviewOutputInvalidReasonV1 ||
			reviewer.ErrorClassification ==
				"COMPOSITE_REVIEWER_VERDICT_INVALID" ||
			reviewer.ErrorClassification ==
				"COMPOSITE_REVIEWER_OUTPUT_INVALID"
		switch reason {
		case corecontract.CollaborationReviewRejectedReasonV1:
			if reviewer.State == CompositeReviewerSucceededV1 &&
				reviewer.CollaborationVerdict != nil &&
				reviewer.CollaborationVerdict.Decision ==
					corecontract.CollaborationReviewDecisionRejectV1 {
				return nil
			}
		case corecontract.CollaborationReviewOutputInvalidReasonV1:
			if reviewer.State == CompositeReviewerFailedV1 && invalidOutput {
				return nil
			}
		case corecontract.CollaborationReviewFailedReasonV1:
			if reviewer.State == CompositeReviewerFailedV1 && !invalidOutput {
				return nil
			}
		case corecontract.CollaborationRepairLimitReachedReasonV1:
			if reviewer.State == CompositeReviewerSucceededV1 &&
				reviewer.CollaborationVerdict != nil &&
				reviewer.CollaborationVerdict.Decision ==
					corecontract.CollaborationReviewDecisionRepairRequiredV1 {
				return nil
			}
		}
		return fmt.Errorf(
			"%w: collaboration Reviewer projection does not prove reason %q",
			ErrCoreDeterministicFailureConflict,
			reason,
		)
	default:
		return fmt.Errorf(
			"%w: unsupported reason",
			ErrInvalidCoreDeterministicFailure,
		)
	}
}

func reopenCoreDeterministicFailure(
	ctx context.Context,
	connection *sql.Conn,
	input CommitCoreDeterministicFailureInput,
	run RunForLoop,
) (CommitCoreDeterministicFailureResult, error) {
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continuation.CoreFailureReason != input.Reason {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: terminal continuation differs from requested failure",
			ErrCoreDeterministicFailureConflict,
		)
	}
	event, err := loadCoreDeterministicFailureEvent(
		ctx,
		connection,
		run.RunID,
		run.Frame.LastAuthoritativeEvent,
	)
	if err != nil || event.Reason != input.Reason {
		return CommitCoreDeterministicFailureResult{}, fmt.Errorf(
			"%w: terminal event differs from requested failure: %v",
			ErrCoreDeterministicFailureIntegrity,
			err,
		)
	}
	return CommitCoreDeterministicFailureResult{
		Lease:   input.Lease,
		Applied: false,
		Reason:  input.Reason,
	}, nil
}

func loadCoreDeterministicFailureEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	sequence uint64,
) (corecontract.CoreDeterministicFailureEventV1, error) {
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
		return corecontract.CoreDeterministicFailureEventV1{}, err
	}
	computed, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil || kind != corecontract.CoreDeterministicFailureEventKind ||
		payloadRef != digest || digest != computed ||
		contentKind != string(ContentRunEventPayload) ||
		mediaType != admissionJSONMediaType {
		return corecontract.CoreDeterministicFailureEventV1{}, loopReadIntegrity(
			"deterministic Core failure event content",
			ErrAdmissionIntegrity,
		)
	}
	event, err := corecontract.RestoreCoreDeterministicFailureEventV1(canonical)
	if err != nil || event.RunID != runID {
		return corecontract.CoreDeterministicFailureEventV1{}, loopReadIntegrity(
			"deterministic Core failure event wire",
			err,
		)
	}
	_, rebuilt, err := corecontract.NewCoreDeterministicFailureEventV1(
		event.RunID,
		event.Reason,
	)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		return corecontract.CoreDeterministicFailureEventV1{}, loopReadIntegrity(
			"deterministic Core failure event canonical closure",
			err,
		)
	}
	return event, nil
}
