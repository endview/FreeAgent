package currentstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

// checkNewModelDispatchFamilyPermit runs inside the same BEGIN IMMEDIATE that
// may create PENDING. Thus cancellation, the optional review gate, and the
// frozen legacy N+1/N+2 or W5 decision 2N+3 family cap cannot race a newly
// authorized provider call. Exact retries return before this check and remain
// non-invokable.
func checkNewModelDispatchFamilyPermit(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	logicalStepID string,
) error {
	if run.Manifest.Composite == nil {
		var cancelRef sql.NullString
		if err := connection.QueryRowContext(ctx, `
			SELECT cancel_request_ref FROM runs WHERE run_id=?
		`, run.RunID).Scan(&cancelRef); err != nil {
			return fmt.Errorf("currentstore: read Run cancellation latch: %w", err)
		}
		if cancelRef.Valid {
			return fmt.Errorf("%w: Run %q", ErrRunCanceled, run.RunID)
		}
		return nil
	}

	var root corecontract.RunManifest
	switch run.Manifest.Composite.Role {
	case corecontract.CompositeRunRoleRootV1:
		root = run.Manifest
		if logicalStepID != root.Composite.Plan.MergeLogicalStepID {
			return fmt.Errorf(
				"%w: composite root model step must be %q",
				ErrInvalidModelDispatch,
				root.Composite.Plan.MergeLogicalStepID,
			)
		}
		children, repairRound, err := loadCompositeEffectiveChildResults(
			ctx,
			connection,
			root,
		)
		if err != nil {
			return err
		}
		if !allCompositeChildrenSucceeded(children) {
			return fmt.Errorf(
				"%w: composite root cannot merge before every Specialist succeeds",
				ErrModelDispatchConflict,
			)
		}
		reviewer, err := loadCompositeReviewerResultForRound(
			ctx,
			connection,
			root,
			children,
			repairRound,
		)
		if err != nil {
			return err
		}
		if root.Composite.Plan.Decision == nil {
			fresh := run
			fresh.CompositeChildren = children
			fresh.CompositeReviewer = reviewer
			if err := checkCompositeRootReviewerGate(fresh); err != nil {
				return err
			}
		} else if err := checkCompositeDecisionRootReviewerGate(
			ctx,
			connection,
			root,
			children,
			reviewer,
			repairRound,
		); err != nil {
			return err
		}
	case corecontract.CompositeRunRoleChildV1:
		if logicalStepID == corecontract.CompositeMergeLogicalStepIDV1 ||
			logicalStepID == corecontract.CompositeReviewLogicalStepIDV1 {
			return fmt.Errorf(
				"%w: composite Specialist cannot execute a family coordination step",
				ErrInvalidModelDispatch,
			)
		}
		loaded, err := loadCompositeRootManifest(
			ctx, connection, run.Manifest.Composite.RootRunID,
		)
		if err != nil {
			return err
		}
		root = loaded
		if run.Manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 {
			if err := verifyCompositeRepairParticipantActivation(
				ctx,
				connection,
				root,
				run.Manifest,
			); err != nil {
				return err
			}
		}
	case corecontract.CompositeRunRoleReviewerV1:
		loaded, err := loadCompositeRootManifest(
			ctx, connection, run.Manifest.Composite.RootRunID,
		)
		if err != nil {
			return err
		}
		root = loaded
		if root.Composite == nil || root.Composite.Plan == nil ||
			root.Composite.Plan.Reviewer == nil {
			return fmt.Errorf(
				"%w: Reviewer is absent from the frozen root plan",
				ErrModelDispatchIntegrity,
			)
		}
		planned := root.Composite.Plan.Reviewer
		if err := validateCompositeReviewerRunAgainstRoot(
			root,
			run.Manifest,
			run.Member,
		); err != nil {
			return fmt.Errorf(
				"%w: Reviewer does not close the frozen root plan",
				ErrModelDispatchIntegrity,
			)
		}
		if logicalStepID != planned.ReviewLogicalStepID ||
			logicalStepID != corecontract.CompositeReviewLogicalStepIDV1 {
			return fmt.Errorf(
				"%w: composite Reviewer model step must be %q",
				ErrInvalidModelDispatch,
				planned.ReviewLogicalStepID,
			)
		}
		var (
			children    []CompositeChildResultRecordV1
			repairRound uint32
		)
		if run.Manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 {
			children, repairRound, err = loadCompositeEffectiveChildResults(
				ctx,
				connection,
				root,
			)
		} else {
			children, err = loadCompositeChildResults(ctx, connection, root)
		}
		if err != nil {
			return err
		}
		if repairRound != run.Manifest.Composite.RepairRound {
			return fmt.Errorf(
				"%w: Reviewer round differs from the effective contribution set",
				ErrModelDispatchConflict,
			)
		}
		if !allCompositeChildrenSucceeded(children) {
			return fmt.Errorf(
				"%w: composite Reviewer cannot run before every Specialist succeeds",
				ErrModelDispatchConflict,
			)
		}
		if root.Composite.Plan.Decision != nil {
			if _, _, err := buildCollaborationContributionSet(
				ctx,
				connection,
				root,
				children,
				repairRound,
			); err != nil {
				return err
			}
		}
		if run.Manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 {
			if err := verifyCompositeRepairParticipantActivation(
				ctx,
				connection,
				root,
				run.Manifest,
			); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf(
			"%w: unsupported composite Run role %q",
			ErrModelDispatchIntegrity,
			run.Manifest.Composite.Role,
		)
	}
	rows, err := loadCompositeFamilyLatchRows(ctx, connection, root)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.cancelRef.Valid {
			return fmt.Errorf(
				"%w: root Run %q",
				ErrRunCanceled,
				root.RunID,
			)
		}
	}
	var attempts int64
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS family_run ON family_run.run_id=attempt.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&attempts); err != nil {
		return fmt.Errorf(
			"currentstore: count composite model dispatches: %w", err,
		)
	}
	limit := int64(root.Composite.Plan.FamilyModelDispatchLimit)
	if attempts < 0 || attempts >= limit {
		return fmt.Errorf(
			"%w: composite family model-dispatch cap %d is exhausted",
			ErrModelDispatchConflict,
			limit,
		)
	}
	return nil
}

// checkCompositeRootReviewerGate admits the merge only when the optional
// Reviewer has produced one exact, family-bound APPROVE verdict. PENDING,
// UNKNOWN, malformed output, provider failure, and REJECT are all closed
// states for merge admission; none is converted into a semantic retry.
func checkCompositeRootReviewerGate(run RunForLoop) error {
	plan := run.Manifest.Composite.Plan
	if plan.Reviewer == nil {
		if run.CompositeReviewer != nil {
			return fmt.Errorf(
				"%w: unplanned Reviewer projection",
				ErrModelDispatchIntegrity,
			)
		}
		return nil
	}
	reviewer := run.CompositeReviewer
	if reviewer == nil || reviewer.RunID != plan.Reviewer.RunID ||
		reviewer.MemberSnapshotDigest != plan.Reviewer.MemberSnapshotDigest {
		return fmt.Errorf(
			"%w: Reviewer projection does not close the frozen root plan",
			ErrModelDispatchIntegrity,
		)
	}
	if reviewer.State != CompositeReviewerSucceededV1 ||
		reviewer.Verdict == nil {
		return fmt.Errorf(
			"%w: composite root cannot merge while Reviewer is %q",
			ErrModelDispatchConflict,
			reviewer.State,
		)
	}
	_, _, specialistDigest, err := buildCompositeSpecialistResultSet(
		run.Manifest,
		run.CompositeChildren,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: Specialist result set: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := reviewer.Verdict.ValidateForCompositeReviewV1(
		run.Manifest,
		specialistDigest,
	); err != nil {
		return fmt.Errorf(
			"%w: Reviewer verdict: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if reviewer.Verdict.Decision != corecontract.ReviewDecisionApproveV1 {
		return fmt.Errorf(
			"%w: Reviewer rejected the Specialist result set",
			ErrModelDispatchConflict,
		)
	}
	return nil
}

func checkCompositeDecisionRootReviewerGate(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	children []CompositeChildResultRecordV1,
	reviewer *CompositeReviewerResultRecordV1,
	repairRound uint32,
) error {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil || reviewer == nil ||
		reviewer.RepairRound != repairRound ||
		reviewer.State != CompositeReviewerSucceededV1 ||
		reviewer.CollaborationVerdict == nil ||
		reviewer.ContributionSet == nil ||
		reviewer.ContributionSetDigest == "" {
		return fmt.Errorf(
			"%w: W5 Reviewer projection is not mergeable",
			ErrModelDispatchConflict,
		)
	}
	_, setDigest, err := buildCollaborationContributionSet(
		ctx,
		connection,
		root,
		children,
		repairRound,
	)
	_, _, projectedSetDigest, projectedErr :=
		corecontract.NewCollaborationContributionSetV1(
			*reviewer.ContributionSet,
		)
	if err != nil || projectedErr != nil ||
		setDigest != reviewer.ContributionSetDigest ||
		projectedSetDigest != setDigest ||
		reviewer.CollaborationVerdict.ValidateForCollaborationReviewV1(
			root,
			setDigest,
			repairRound,
		) != nil {
		return fmt.Errorf(
			"%w: W5 Reviewer verdict does not close the effective contribution set",
			ErrModelDispatchIntegrity,
		)
	}
	if reviewer.CollaborationVerdict.Decision !=
		corecontract.CollaborationReviewDecisionApproveV1 {
		return fmt.Errorf(
			"%w: W5 Reviewer did not approve the effective contribution set",
			ErrModelDispatchConflict,
		)
	}
	if repairRound == 0 {
		transitions, _, err := compositeRepairTransitions(
			root,
			*reviewer.CollaborationVerdict,
			reviewer.ResultRef,
		)
		if err != nil {
			return err
		}
		applied, err := inspectCompositeRepairTransitionEvents(
			ctx,
			connection,
			root,
			reviewer.ResultRef,
			transitions,
		)
		if err != nil || !applied {
			return fmt.Errorf(
				"%w: unused repair Runs are not durably skipped",
				ErrModelDispatchConflict,
			)
		}
		return nil
	}
	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil {
		return err
	}
	reviewerZero, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewerZero == nil ||
		reviewerZero.CollaborationVerdict == nil ||
		reviewerZero.CollaborationVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 ||
		reviewerZero.ResultRef == "" {
		return fmt.Errorf(
			"%w: repaired merge lacks its round-zero repair verdict",
			ErrModelDispatchIntegrity,
		)
	}
	return verifyCompositeRepairTransitionEvent(
		ctx,
		connection,
		root,
		reviewerZero.ResultRef,
		compositeRepairTransition{
			runID: root.Composite.Plan.Decision.RepairReviewer.RunID,
			parentSlotID: root.Composite.Plan.Decision.
				RepairReviewer.ParentSlotID,
			role: corecontract.CompositeRunRoleReviewerV1,
			kind: compositeRepairTransitionActivate,
		},
	)
}

func verifyCompositeRepairParticipantActivation(
	ctx context.Context,
	connection *sql.Conn,
	root corecontract.RunManifest,
	participant corecontract.RunManifest,
) error {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil || participant.Composite == nil ||
		participant.Composite.RepairRound !=
			corecontract.CompositeRepairRoundOneV1 {
		return fmt.Errorf(
			"%w: participant is not in the round-one decision plan",
			ErrModelDispatchIntegrity,
		)
	}
	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil {
		return err
	}
	reviewerZero, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewerZero == nil ||
		reviewerZero.ResultRef == "" ||
		reviewerZero.CollaborationVerdict == nil ||
		reviewerZero.CollaborationVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		return fmt.Errorf(
			"%w: round-one participant lacks a repair verdict",
			ErrModelDispatchConflict,
		)
	}
	transition := compositeRepairTransition{
		runID:        participant.RunID,
		parentSlotID: participant.Composite.ParentSlotID,
		role:         participant.Composite.Role,
		kind:         compositeRepairTransitionActivate,
	}
	if participant.Composite.Role == corecontract.CompositeRunRoleChildV1 {
		affected := false
		for _, slotID := range reviewerZero.CollaborationVerdict.AffectedSlotIDs {
			if participant.Composite.Assignment != nil &&
				slotID == participant.Composite.Assignment.SlotID {
				affected = true
				break
			}
		}
		if !affected {
			return fmt.Errorf(
				"%w: unaffected repair Child cannot dispatch",
				ErrModelDispatchConflict,
			)
		}
	}
	return verifyCompositeRepairTransitionEvent(
		ctx,
		connection,
		root,
		reviewerZero.ResultRef,
		transition,
	)
}
