package coreloop

import (
	"context"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
)

func plannedReviewerParentSlotForLoopV1(
	planned corecontract.CompositeReviewerRunRefV1,
	repairRound uint32,
) string {
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		return planned.ParentSlotID
	}
	return corecontract.CompositeReviewerParentSlotIDV1
}

func (loop *UniversalLoop) advanceCollaborationRoot(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		run.Manifest.Composite.Plan == nil ||
		run.Manifest.Composite.Plan.Decision == nil ||
		run.CompositeDecisionFrontier == nil ||
		run.CompositeContributionSet == nil ||
		run.CompositeContributionSetDigest == "" {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration Root closure is incomplete",
			ErrUniversalLoopIntegrity,
		)
	}
	reviewer := run.CompositeReviewer
	if reviewer == nil || reviewer.RepairRound != run.CompositeRepairRound {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration Root lacks its current-round Reviewer",
			ErrUniversalLoopIntegrity,
		)
	}
	switch reviewer.State {
	case currentstore.CompositeReviewerPendingV1:
		if run.CompositeRepairRound ==
			corecontract.CompositeRepairRoundOneV1 &&
			reviewer.FrameStep ==
				corecontract.WaitingRepairActivationLoopStep &&
			run.CompositeDecisionFrontier.Stage ==
				currentstore.CompositeFrontierRootTransitionV1 {
			persistCtx, cancel, err := compositeDecisionPersistenceContext(
				ctx,
				lease.ExpiresAt,
			)
			if err != nil {
				return loopapi.RunResult{}, lease, err
			}
			transition, err := loop.store.ApplyCompositeDecision(
				persistCtx,
				currentstore.ApplyCompositeDecisionInput{Lease: lease},
			)
			cancel()
			if err != nil {
				return loopapi.RunResult{}, lease, err
			}
			if !transition.Applied ||
				run.CompositeRepairVerdict == nil ||
				transition.Decision !=
					run.CompositeRepairVerdict.Decision ||
				transition.SourceVerdictRef !=
					run.CompositeRepairVerdictRef {
				return loopapi.RunResult{}, lease, fmt.Errorf(
					"%w: repair Reviewer activation differs from round-zero verdict",
					ErrUniversalLoopIntegrity,
				)
			}
			return newRunResult(
				run.RunID,
				loopapi.DispositionWaitingExternal,
				reasonCollaborationDecisionApplied,
			), lease, nil
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingExternal,
			reasonCompositeReviewPending,
		), lease, nil
	case currentstore.CompositeReviewerUnknownV1:
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingReconciliation,
			reasonCompositeReviewUnknown,
		), lease, nil
	case currentstore.CompositeReviewerFailedV1:
		reason := reasonCollaborationReviewFailed
		if reviewer.ErrorClassification ==
			reasonCollaborationReviewOutputInvalid {
			reason = reasonCollaborationReviewOutputInvalid
		}
		return loop.commitCoreDeterministicFailure(ctx, run, lease, reason)
	case currentstore.CompositeReviewerSucceededV1:
		if reviewer.CollaborationVerdict == nil ||
			reviewer.ContributionSet == nil ||
			reviewer.ContributionSetDigest !=
				run.CompositeContributionSetDigest {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: successful collaboration Reviewer lacks its exact verdict",
				ErrUniversalLoopIntegrity,
			)
		}
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: unsupported collaboration Reviewer state %q",
			ErrUniversalLoopIntegrity,
			reviewer.State,
		)
	}

	verdict := *reviewer.CollaborationVerdict
	if err := verdict.ValidateForCollaborationReviewV1(
		run.Manifest,
		run.CompositeContributionSetDigest,
		run.CompositeRepairRound,
	); err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration Reviewer verdict: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	if run.CompositeRepairRound == 0 {
		persistCtx, cancel, err := compositeDecisionPersistenceContext(
			ctx,
			lease.ExpiresAt,
		)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		transition, err := loop.store.ApplyCompositeDecision(
			persistCtx,
			currentstore.ApplyCompositeDecisionInput{Lease: lease},
		)
		cancel()
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		if transition.Decision != verdict.Decision ||
			transition.SourceVerdictRef != reviewer.ResultRef {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: persisted collaboration transition differs from verdict",
				ErrUniversalLoopIntegrity,
			)
		}
		if transition.Applied {
			return newRunResult(
				run.RunID,
				loopapi.DispositionWaitingExternal,
				reasonCollaborationDecisionApplied,
			), lease, nil
		}
		if verdict.Decision ==
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: applied repair decision did not advance the effective round",
				ErrUniversalLoopIntegrity,
			)
		}
	}

	switch verdict.Decision {
	case corecontract.CollaborationReviewDecisionApproveV1:
		return loop.advanceCompositeMerge(ctx, input, run, lease)
	case corecontract.CollaborationReviewDecisionRejectV1:
		return loop.commitCoreDeterministicFailure(
			ctx,
			run,
			lease,
			reasonCollaborationReviewRejected,
		)
	case corecontract.CollaborationReviewDecisionRepairRequiredV1:
		return loop.commitCoreDeterministicFailure(
			ctx,
			run,
			lease,
			reasonCollaborationRepairLimitReached,
		)
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: unsupported collaboration decision %q",
			ErrUniversalLoopIntegrity,
			verdict.Decision,
		)
	}
}

func (loop *UniversalLoop) advanceCollaborationReview(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if input.MaxSteps < 1 {
		return newRunResult(
			run.RunID,
			loopapi.DispositionYielded,
			"COLLABORATION_REVIEW_STEP_BUDGET",
		), lease, nil
	}
	root, enabled, err := collaborationRootForRunV1(run)
	if err != nil || !enabled ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		run.CompositeContributionSet == nil ||
		run.CompositeContributionSetDigest == "" ||
		run.CompositeRepairRound != run.Manifest.Composite.RepairRound {
		if err == nil {
			err = fmt.Errorf("Reviewer collaboration closure is absent")
		}
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration Reviewer closure: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	prepared, err := loop.prepareChatRequestV1(ctx, run, lease)
	if err != nil {
		if errors.Is(err, contextcompiler.ErrContextBudgetExceeded) {
			return loop.commitCoreDeterministicFailure(
				ctx,
				run,
				lease,
				reasonCollaborationReviewFailed,
			)
		}
		return loopapi.RunResult{}, lease, err
	}
	var planned *corecontract.CompositeReviewerRunRefV1
	if run.CompositeRepairRound == 0 {
		planned = root.Composite.Plan.Reviewer
	} else {
		repair := root.Composite.Plan.Decision.RepairReviewer
		planned = &repair
	}
	if planned == nil || planned.RunID != run.RunID {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration Reviewer is not frozen for this round",
			ErrUniversalLoopIntegrity,
		)
	}
	logicalStepID := planned.ReviewLogicalStepID
	operationKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		logicalStepID,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: collaboration review operation identity: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	begin, err := loop.store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   modelAttemptIDPrefix + operationKey,
			LogicalStepID:               logicalStepID,
			ContextCompilationCanonical: prepared.ContextCompilationCanonical,
			RequestCanonical:            prepared.RequestCanonical,
			Deadline: narrowedModelDeadline(
				run.Manifest.Deadline,
				lease.ExpiresAt,
			),
		},
	)
	if err != nil {
		return runPermitErrorResult(run, lease, err)
	}
	lease = begin.Lease
	if begin.Created && !begin.InvokeAllowed {
		if begin.Attempt.State != corecontract.ModelAttemptFailed {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: non-invocable collaboration review has state %q",
				ErrUniversalLoopIntegrity,
				begin.Attempt.State,
			)
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionTerminated,
			reasonModelFailed,
		), lease, nil
	}
	if !begin.Created || !begin.InvokeAllowed {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: WAITING_CHILDREN did not create the collaboration review grant",
			ErrUniversalLoopIntegrity,
		)
	}
	commit := loop.invokeModelOutcome(ctx, begin)
	commit, _, err = normalizeCollaborationReviewerOutcomeV1(
		commit,
		root,
		run.CompositeContributionSetDigest,
		run.CompositeRepairRound,
	)
	if err != nil {
		return loopapi.RunResult{}, begin.Lease, fmt.Errorf(
			"%w: normalize collaboration Reviewer outcome: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(
		persistCtx,
		commit,
	)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, begin.Lease, err
	}
	return runResultForOutcome(
		begin.Attempt.RunID,
		committed.Record.Attempt.State,
		commit.UnknownReason,
		committed.Lease,
	)
}

func normalizeCollaborationSpecialistDispatchV1(
	run currentstore.RunForLoop,
	begin currentstore.BeginModelDispatchResult,
	commit currentstore.CommitModelDispatchOutcomeInput,
) (currentstore.CommitModelDispatchOutcomeInput, error) {
	_, enabled, err := collaborationRootForRunV1(run)
	if err != nil || !enabled || run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 {
		return commit, err
	}
	if commit.State != corecontract.ModelAttemptSucceeded {
		return commit, nil
	}
	record := begin.Attempt.ContextCompilation
	if record == nil || record.Kind != currentstore.ContentContextCompilation ||
		len(record.CanonicalBytes) == 0 {
		return commit, fmt.Errorf(
			"%w: collaboration Specialist Attempt lacks exact ContextCompilation",
			ErrUniversalLoopIntegrity,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return commit, fmt.Errorf(
			"%w: restore collaboration Specialist ContextCompilation: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	normalized, _, err := normalizeCollaborationSpecialistOutcomeV1(
		commit,
		&compilation,
	)
	if err != nil {
		return commit, fmt.Errorf(
			"%w: normalize collaboration Specialist outcome: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	return normalized, nil
}
