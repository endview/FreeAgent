package coreloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/actiongateway"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	pureChatLogicalStepID                = corecontract.PureChatModelLogicalStepIDV1
	modelAttemptIDPrefix                 = "model-attempt-"
	loopOwnerIDPrefix                    = "coreloop-"
	persistenceGrace                     = 10 * time.Second
	compositeDecisionPersistenceGrace    = 15 * time.Second
	compositeDecisionFinalizationMargin  = 5 * time.Second
	compositeDecisionFinalizationReserve = 2*persistenceGrace + compositeDecisionFinalizationMargin
	// RunLeaseTailGrace is the single lease reserve shared by direct and
	// fair-scheduled Universal Loop execution. It allocates the configured
	// budget for a detached composite transition, two generic finalization
	// operations, and a scheduling margin before lease expiry. Cleanup work
	// that does not observe its context may extend beyond this reserve.
	RunLeaseTailGrace                     = compositeDecisionPersistenceGrace + compositeDecisionFinalizationReserve
	leaseTailGrace                        = RunLeaseTailGrace
	reasonModelSucceeded                  = "MODEL_SUCCEEDED"
	reasonModelFailed                     = "MODEL_FAILED"
	reasonModelUnknown                    = "MODEL_UNKNOWN"
	reasonRecoveredPending                = "RECOVERED_PENDING_AFTER_CRASH"
	reasonHostErrorAfterPending           = "HOST_ERROR_AFTER_PENDING"
	reasonInvalidProviderResult           = "INVALID_PROVIDER_RESULT"
	reasonMalformedModelOutput            = "MALFORMED_MODEL_OUTPUT"
	reasonCompositeChildrenPending        = "COMPOSITE_CHILDREN_PENDING"
	reasonCompositeChildUnknown           = "COMPOSITE_CHILD_UNKNOWN"
	reasonCompositeChildFailed            = corecontract.AllRequiredChildFailedReasonV1
	reasonCompositeChildResultOverBudget  = corecontract.CompositeChildResultOverBudgetReasonV1
	reasonCompositeReviewPending          = "COMPOSITE_REVIEW_PENDING"
	reasonCompositeReviewUnknown          = "COMPOSITE_REVIEW_UNKNOWN"
	reasonCompositeReviewRejected         = corecontract.CompositeReviewRejectedReasonV1
	reasonCompositeReviewFailed           = corecontract.CompositeReviewFailedReasonV1
	reasonCompositeReviewOutputInvalid    = corecontract.CompositeReviewOutputInvalidReasonV1
	reasonCollaborationDecisionApplied    = "COLLABORATION_DECISION_APPLIED"
	reasonCollaborationRepairPending      = "COLLABORATION_REPAIR_PENDING"
	reasonCollaborationReviewRejected     = "COLLABORATION_REVIEW_REJECTED"
	reasonCollaborationReviewFailed       = "COLLABORATION_REVIEW_FAILED"
	reasonCollaborationRepairLimitReached = "COLLABORATION_REPAIR_LIMIT_REACHED"
	reasonRunCanceled                     = "RUN_CANCELED"
	reasonCompositeFamilyCanceled         = "COMPOSITE_FAMILY_CANCELED"
	currentActivationDeniedClassification = "CURRENT_ACTIVATION_DENIED_BEFORE_ADAPTER"
)

var (
	ErrInvalidUniversalLoop = errors.New(
		"coreloop: invalid Universal Loop",
	)
	ErrUniversalLoopIntegrity = errors.New(
		"coreloop: Universal Loop integrity violation",
	)
)

// UniversalLoop is the current runtime lifecycle implementation. It owns no
// module selection or provider policy: both are already frozen in Current
// Store.
type UniversalLoop struct {
	store             *currentstore.Store
	registry          modulehost.ExactAdapterRegistry
	currentActivation modulehost.CurrentActivationChecker
	actionGateway     *actiongateway.Gateway
}

var _ loopapi.Loop = (*UniversalLoop)(nil)

func NewUniversalLoop(
	store *currentstore.Store,
	registry modulehost.ExactAdapterRegistry,
) (*UniversalLoop, error) {
	if store == nil {
		return nil, fmt.Errorf(
			"%w: Current Store is nil",
			ErrInvalidUniversalLoop,
		)
	}
	if isNilLoopDependency(registry) {
		return nil, fmt.Errorf(
			"%w: exact adapter registry is nil",
			ErrInvalidUniversalLoop,
		)
	}
	gateway, err := actiongateway.New(store, registry)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: Action Gateway: %v",
			ErrInvalidUniversalLoop,
			err,
		)
	}
	return &UniversalLoop{
		store:             store,
		registry:          registry,
		currentActivation: store,
		actionGateway:     gateway,
	}, nil
}

// Run advances at most the current Pure Chat semantic step. It always releases
// an acquired lease before reporting a normal disposition. Any PENDING step
// recovered after restart is converted to MODEL_UNKNOWN without invoking an
// adapter.
func (loop *UniversalLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	if err := loop.validateRunInput(ctx, input); err != nil {
		return loopapi.RunResult{}, err
	}
	ownerID, err := newLoopOwnerID()
	if err != nil {
		return loopapi.RunResult{}, err
	}
	lease, err := loop.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   input.RunID,
			OwnerID: ownerID,
			TTL:     input.MaxDuration + leaseTailGrace,
		},
	)
	if err != nil {
		return loopapi.RunResult{}, err
	}
	return loop.runClaimed(ctx, input, lease)
}

// RunClaimed advances a Run using the exact fenced lease atomically acquired
// by the internal fair Scheduler. It is an internal Core composition surface,
// not a model, module, or public SDK authority. Once called, the Universal Loop
// attempts to release the transferred lease on every exit, including invalid
// input. The caller must acquire the transferred lease with a TTL of at least
// input.MaxDuration plus RunLeaseTailGrace. Elapsed handoff time reduces the
// remaining lease window; RunClaimed does not renew it.
func (loop *UniversalLoop) RunClaimed(
	ctx context.Context,
	input loopapi.RunInput,
	lease currentstore.RunLease,
) (loopapi.RunResult, error) {
	if err := loop.validateRunInput(ctx, input); err != nil {
		return loopapi.RunResult{}, errors.Join(
			err,
			loop.releaseTransferredLease(lease),
		)
	}
	if lease.RunID != input.RunID {
		return loopapi.RunResult{}, errors.Join(
			fmt.Errorf(
				"%w: claimed lease belongs to another Run",
				ErrInvalidUniversalLoop,
			),
			loop.releaseTransferredLease(lease),
		)
	}
	return loop.runClaimed(ctx, input, lease)
}

func (loop *UniversalLoop) validateRunInput(
	ctx context.Context,
	input loopapi.RunInput,
) error {
	if loop == nil ||
		loop.store == nil ||
		isNilLoopDependency(loop.registry) ||
		isNilLoopDependency(loop.currentActivation) ||
		loop.actionGateway == nil {
		return fmt.Errorf(
			"%w: implementation is not initialized",
			ErrInvalidUniversalLoop,
		)
	}
	if ctx == nil {
		return fmt.Errorf(
			"%w: context is nil",
			ErrInvalidUniversalLoop,
		)
	}
	if err := input.Validate(); err != nil {
		return fmt.Errorf(
			"%w: %v",
			ErrInvalidUniversalLoop,
			err,
		)
	}
	if input.MaxDuration < time.Microsecond ||
		input.MaxDuration > time.Duration(1<<63-1)-leaseTailGrace {
		return fmt.Errorf(
			"%w: max duration is outside the lease domain",
			ErrInvalidUniversalLoop,
		)
	}
	return nil
}

func (loop *UniversalLoop) runClaimed(
	ctx context.Context,
	input loopapi.RunInput,
	lease currentstore.RunLease,
) (loopapi.RunResult, error) {
	result, currentLease, advanceErr := loop.advance(ctx, input, lease)
	releaseCtx, cancel := persistenceContext(ctx)
	releaseErr := loop.store.ReleaseRunLease(releaseCtx, currentLease)
	cancel()
	if advanceErr != nil || releaseErr != nil {
		return loopapi.RunResult{}, errors.Join(advanceErr, releaseErr)
	}
	if currentLease.FrameRevision >= uint64(1<<63-1) {
		return loopapi.RunResult{}, fmt.Errorf(
			"%w: released Frame revision exceeds SQLite domain",
			ErrUniversalLoopIntegrity,
		)
	}
	result.FrameRevision = currentLease.FrameRevision + 1
	if err := result.Validate(); err != nil {
		return loopapi.RunResult{}, fmt.Errorf(
			"%w: persisted result: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	return result, nil
}

func (loop *UniversalLoop) releaseTransferredLease(
	lease currentstore.RunLease,
) error {
	if loop == nil || loop.store == nil {
		return nil
	}
	releaseCtx, cancel := context.WithTimeout(
		context.Background(),
		persistenceGrace,
	)
	defer cancel()
	return loop.store.ReleaseRunLease(releaseCtx, lease)
}

func (loop *UniversalLoop) advance(
	ctx context.Context,
	input loopapi.RunInput,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	run, err := loop.store.LoadRunForLoop(ctx, lease)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	continued, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: continuation: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	if run.CancellationRequest != nil {
		return canceledRunResult(run, lease)
	}
	switch run.Frame.Step {
	case corecontract.WaitingChildrenLoopStep:
		return loop.advanceWaitingChildren(ctx, input, run, lease)
	case corecontract.InitialLoopStep:
		if len(run.Member.Actions) != 0 {
			if input.MaxSteps < 2 {
				return newRunResult(
					run.RunID,
					loopapi.DispositionYielded,
					"ACTION_CHAIN_STEP_BUDGET",
				), lease, nil
			}
			return loop.advanceActionReady(ctx, input, run, lease)
		}
		if run.ChannelIngress != nil && input.MaxSteps < 2 {
			return newRunResult(
				run.RunID,
				loopapi.DispositionYielded,
				"CHANNEL_CHAIN_STEP_BUDGET",
			), lease, nil
		}
		return loop.advanceReady(ctx, input, run, lease)
	case corecontract.ModelPendingLoopStep:
		return loop.recoverPending(ctx, run, continued, lease)
	case corecontract.ActionPendingLoopStep:
		return loop.recoverActionPending(ctx, run, continued, lease)
	case corecontract.ChannelPendingLoopStep:
		return loop.recoverChannelPending(ctx, run, continued, lease)
	case corecontract.ModelReadyAfterActionLoopStep:
		if run.ChannelIngress != nil && input.MaxSteps < 2 {
			return newRunResult(
				run.RunID,
				loopapi.DispositionYielded,
				"CHANNEL_CHAIN_STEP_BUDGET",
			), lease, nil
		}
		return loop.advanceModelAfterAction(ctx, run, lease)
	case corecontract.WaitingReconciliationLoopStep:
		reason, err := continuedUnknownReason(run, continued)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		if reason == "" {
			reason = reasonModelUnknown
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingReconciliation,
			reason,
		), lease, nil
	case corecontract.WaitingRepairActivationLoopStep:
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingExternal,
			"COMPOSITE_REPAIR_DORMANT",
		), lease, nil
	case corecontract.TerminatedLoopStep:
		reason, err := loop.persistedTerminalReason(ctx, run.RunID)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionTerminated,
			reason,
		), lease, nil
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: unsupported persisted Loop step %q",
			ErrUniversalLoopIntegrity,
			run.Frame.Step,
		)
	}
}

func (loop *UniversalLoop) advanceWaitingChildren(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if run.Manifest.Composite == nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: WAITING_CHILDREN has no composite identity",
			ErrUniversalLoopIntegrity,
		)
	}
	node := run.Manifest.Composite
	switch node.Role {
	case corecontract.CompositeRunRoleRootV1:
		if node.Plan == nil ||
			len(run.CompositeChildren) != len(node.Plan.Children) {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: WAITING_CHILDREN has no exact composite root closure",
				ErrUniversalLoopIntegrity,
			)
		}
	case corecontract.CompositeRunRoleReviewerV1:
		if node.Plan != nil || node.Assignment != nil ||
			len(run.CompositeChildren) < corecontract.CompositeMinChildrenV1 ||
			len(run.CompositeChildren) > corecontract.CompositeMaxChildrenV1 ||
			run.CompositeReviewer != nil {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: WAITING_CHILDREN has no exact composite Reviewer closure",
				ErrUniversalLoopIntegrity,
			)
		}
		root, collaboration, rootErr := collaborationRootForRunV1(run)
		if rootErr != nil {
			return loopapi.RunResult{}, lease, rootErr
		}
		if collaboration {
			var planned *corecontract.CompositeReviewerRunRefV1
			switch node.RepairRound {
			case 0:
				planned = root.Composite.Plan.Reviewer
			case corecontract.CompositeRepairRoundOneV1:
				repair := root.Composite.Plan.Decision.RepairReviewer
				planned = &repair
			}
			if planned == nil || planned.RunID != run.RunID ||
				node.ParentSlotID != plannedReviewerParentSlotForLoopV1(
					*planned,
					node.RepairRound,
				) {
				return loopapi.RunResult{}, lease, fmt.Errorf(
					"%w: collaboration Reviewer differs from its frozen round",
					ErrUniversalLoopIntegrity,
				)
			}
		} else if node.ParentSlotID !=
			corecontract.CompositeReviewerParentSlotIDV1 {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: legacy Reviewer parent slot differs",
				ErrUniversalLoopIntegrity,
			)
		}
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: composite role %q cannot wait on Specialists",
			ErrUniversalLoopIntegrity,
			node.Role,
		)
	}
	seenPending := false
	seenUnknown := false
	seenFailed := false
	for index, child := range run.CompositeChildren {
		if node.Role == corecontract.CompositeRunRoleRootV1 {
			planned := node.Plan.Children[index]
			if node.Plan.Decision != nil &&
				run.CompositeRepairRound ==
					corecontract.CompositeRepairRoundOneV1 &&
				child.RepairRound == corecontract.CompositeRepairRoundOneV1 {
				planned = node.Plan.Decision.RepairChildren[index]
			}
			if child.RunID != planned.RunID ||
				child.SlotID != planned.SlotID {
				return loopapi.RunResult{}, lease, fmt.Errorf(
					"%w: composite Child result order differs from frozen effective plan",
					ErrUniversalLoopIntegrity,
				)
			}
		}
		switch child.State {
		case currentstore.CompositeChildPendingV1:
			seenPending = true
		case currentstore.CompositeChildUnknownV1:
			seenUnknown = true
		case currentstore.CompositeChildFailedV1:
			seenFailed = true
		case currentstore.CompositeChildSucceededV1:
		default:
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: unsupported composite Child state %q",
				ErrUniversalLoopIntegrity,
				child.State,
			)
		}
	}
	if seenFailed {
		return loop.commitCoreDeterministicFailure(
			ctx,
			run,
			lease,
			reasonCompositeChildFailed,
		)
	}
	if seenUnknown {
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingReconciliation,
			reasonCompositeChildUnknown,
		), lease, nil
	}
	if seenPending {
		reason := reasonCompositeChildrenPending
		if run.CompositeRepairRound ==
			corecontract.CompositeRepairRoundOneV1 {
			reason = reasonCollaborationRepairPending
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingExternal,
			reason,
		), lease, nil
	}
	if node.Role == corecontract.CompositeRunRoleReviewerV1 {
		_, collaboration, err := collaborationRootForRunV1(run)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		if collaboration {
			return loop.advanceCollaborationReview(ctx, input, run, lease)
		}
		return loop.advanceCompositeReview(ctx, input, run, lease)
	}
	if node.Plan.Decision != nil {
		return loop.advanceCollaborationRoot(ctx, input, run, lease)
	}
	if node.Plan.Reviewer == nil {
		if run.CompositeReviewer != nil {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: Reviewer-disabled Root exposes Reviewer state",
				ErrUniversalLoopIntegrity,
			)
		}
		return loop.advanceCompositeMerge(ctx, input, run, lease)
	}
	reviewer := run.CompositeReviewer
	if reviewer == nil || reviewer.RunID != node.Plan.Reviewer.RunID ||
		reviewer.MemberSnapshotDigest != node.Plan.Reviewer.MemberSnapshotDigest {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: Reviewer-enabled Root lacks its exact Reviewer projection",
			ErrUniversalLoopIntegrity,
		)
	}
	switch reviewer.State {
	case currentstore.CompositeReviewerPendingV1:
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
		reason := reasonCompositeReviewFailed
		if reviewer.ErrorClassification == reasonCompositeReviewOutputInvalid {
			reason = reasonCompositeReviewOutputInvalid
		}
		return loop.commitCoreDeterministicFailure(ctx, run, lease, reason)
	case currentstore.CompositeReviewerSucceededV1:
		if reviewer.Verdict == nil {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: successful Reviewer lacks a verdict",
				ErrUniversalLoopIntegrity,
			)
		}
		switch reviewer.Verdict.Decision {
		case corecontract.ReviewDecisionRejectV1:
			return loop.commitCoreDeterministicFailure(
				ctx,
				run,
				lease,
				reasonCompositeReviewRejected,
			)
		case corecontract.ReviewDecisionApproveV1:
			return loop.advanceCompositeMerge(ctx, input, run, lease)
		default:
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: successful Reviewer has decision %q",
				ErrUniversalLoopIntegrity,
				reviewer.Verdict.Decision,
			)
		}
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: unsupported Reviewer state %q",
			ErrUniversalLoopIntegrity,
			reviewer.State,
		)
	}
}

func (loop *UniversalLoop) advanceCompositeReview(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if input.MaxSteps < 1 {
		return newRunResult(
			run.RunID,
			loopapi.DispositionYielded,
			"COMPOSITE_REVIEW_STEP_BUDGET",
		), lease, nil
	}
	_, _, specialistSet, specialistDigest, _, err :=
		compositeContextCompilerInputV1(run)
	if err != nil || specialistSet == nil || specialistDigest == "" {
		if err == nil {
			err = fmt.Errorf("Reviewer Specialist result set is absent")
		}
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: Reviewer result-set closure: %v",
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
				reasonCompositeReviewFailed,
			)
		}
		return loopapi.RunResult{}, lease, err
	}
	logicalStepID := corecontract.CompositeReviewLogicalStepIDV1
	operationKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		logicalStepID,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: composite review operation identity: %v",
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
				"%w: non-invocable composite review has state %q",
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
			"%w: WAITING_CHILDREN did not create the review grant",
			ErrUniversalLoopIntegrity,
		)
	}
	commit := loop.invokeModelOutcome(ctx, begin)
	if commit.State == corecontract.ModelAttemptSucceeded {
		output, restoreErr := moduleapi.RestoreModelGenerateOutputV1(
			commit.OutputCanonical,
		)
		if restoreErr != nil {
			return loopapi.RunResult{}, begin.Lease, fmt.Errorf(
				"%w: normalized composite review output cannot be restored: %v",
				ErrUniversalLoopIntegrity,
				restoreErr,
			)
		}
		verdict, verdictCanonical, verdictErr :=
			corecontract.ParseReviewVerdictV1([]byte(output.AssistantText))
		if output.ActionRequest != nil || verdictErr != nil ||
			validateReviewerVerdictForRunV1(
				run,
				*specialistSet,
				specialistDigest,
				verdict,
			) != nil {
			commit.State = corecontract.ModelAttemptFailed
			commit.OutputCanonical = nil
			commit.ErrorClassification = reasonCompositeReviewOutputInvalid
			commit.UnknownReason = ""
		} else {
			_, normalized, normalizeErr := moduleapi.NewModelGenerateOutputV1(
				moduleapi.ModelGenerateOutputV1{
					SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
					AssistantText:     string(verdictCanonical),
					ProviderRequestID: output.ProviderRequestID,
				},
			)
			if normalizeErr != nil {
				return loopapi.RunResult{}, begin.Lease, fmt.Errorf(
					"%w: normalize composite ReviewVerdict: %v",
					ErrUniversalLoopIntegrity,
					normalizeErr,
				)
			}
			commit.OutputCanonical = normalized
		}
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(persistCtx, commit)
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

func validateReviewerVerdictForRunV1(
	run currentstore.RunForLoop,
	set corecontract.CompositeSpecialistResultSetV1,
	specialistDigest string,
	verdict corecontract.ReviewVerdictV1,
) error {
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		run.Manifest.Composite.ParentManifestDigest != set.FamilyDigest ||
		verdict.FamilyDigest != set.FamilyDigest ||
		verdict.SpecialistResultDigest != specialistDigest {
		return fmt.Errorf("Reviewer verdict does not bind the frozen family")
	}
	allowed := make(map[string]struct{}, len(set.Results))
	for _, result := range set.Results {
		allowed[result.SlotID] = struct{}{}
	}
	for _, slotID := range verdict.AffectedSlotIDs {
		if _, ok := allowed[slotID]; !ok {
			return fmt.Errorf("Reviewer verdict names unknown slot %q", slotID)
		}
	}
	return nil
}

func (loop *UniversalLoop) advanceCompositeMerge(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if input.MaxSteps < 1 {
		return newRunResult(
			run.RunID,
			loopapi.DispositionYielded,
			"COMPOSITE_MERGE_STEP_BUDGET",
		), lease, nil
	}
	prepared, err := loop.prepareChatRequestV1(ctx, run, lease)
	if err != nil {
		if errors.Is(err, contextcompiler.ErrContextBudgetExceeded) &&
			strings.Contains(
				err.Error(),
				contextcompiler.CompositeChildResultOverBudgetCodeV1,
			) {
			return loop.commitCoreDeterministicFailure(
				ctx,
				run,
				lease,
				reasonCompositeChildResultOverBudget,
			)
		}
		return loopapi.RunResult{}, lease, err
	}
	logicalStepID := run.Manifest.Composite.Plan.MergeLogicalStepID
	operationKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		logicalStepID,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: composite merge operation identity: %v",
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
				"%w: non-invocable composite merge has state %q",
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
			"%w: WAITING_CHILDREN did not create the merge grant",
			ErrUniversalLoopIntegrity,
		)
	}
	commit := loop.invokeModelOutcome(ctx, begin)
	if commit.State == corecontract.ModelAttemptSucceeded {
		output, restoreErr := moduleapi.RestoreModelGenerateOutputV1(
			commit.OutputCanonical,
		)
		if restoreErr != nil {
			return loopapi.RunResult{}, begin.Lease, fmt.Errorf(
				"%w: normalized composite merge output cannot be restored: %v",
				ErrUniversalLoopIntegrity,
				restoreErr,
			)
		}
		if output.ActionRequest != nil {
			return loop.commitLegalActionRejection(
				ctx,
				commit,
				currentstore.ModelActionRejectionUnknownAction,
			)
		}
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(persistCtx, commit)
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

func (loop *UniversalLoop) commitCoreDeterministicFailure(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	reason string,
) (loopapi.RunResult, currentstore.RunLease, error) {
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitCoreDeterministicFailure(
		persistCtx,
		currentstore.CommitCoreDeterministicFailureInput{
			Lease:  lease,
			Reason: reason,
		},
	)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	return newRunResult(
		run.RunID,
		loopapi.DispositionTerminated,
		committed.Reason,
	), committed.Lease, nil
}

func (loop *UniversalLoop) advanceReady(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	prepared, err := loop.prepareChatRequestV1(ctx, run, lease)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	operationKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		pureChatLogicalStepID,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: model operation identity: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	deadline := narrowedModelDeadline(
		run.Manifest.Deadline,
		lease.ExpiresAt,
	)
	begin, err := loop.store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   modelAttemptIDPrefix + operationKey,
			LogicalStepID:               pureChatLogicalStepID,
			ContextCompilationCanonical: prepared.ContextCompilationCanonical,
			RequestCanonical:            prepared.RequestCanonical,
			Deadline:                    deadline,
		},
	)
	if err != nil {
		return runPermitErrorResult(run, lease, err)
	}
	lease = begin.Lease
	if begin.Created && !begin.InvokeAllowed {
		if begin.Attempt.State != corecontract.ModelAttemptFailed {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: non-invocable created Attempt has state %q",
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
			"%w: READY did not create the unique model dispatch grant",
			ErrUniversalLoopIntegrity,
		)
	}

	commit := loop.invokeModelOutcome(ctx, begin)
	commit, err = normalizeCollaborationSpecialistDispatchV1(
		run,
		begin,
		commit,
	)
	if err != nil {
		return loopapi.RunResult{}, begin.Lease, err
	}
	if commit.State == corecontract.ModelAttemptSucceeded {
		output, restoreErr := moduleapi.RestoreModelGenerateOutputV1(
			commit.OutputCanonical,
		)
		if restoreErr != nil {
			return loopapi.RunResult{}, begin.Lease, fmt.Errorf(
				"%w: normalized Pure Chat output cannot be restored: %v",
				ErrUniversalLoopIntegrity,
				restoreErr,
			)
		}
		if output.ActionRequest != nil {
			// The wire is valid even though this member exposes no Action.
			// Preserve Model SUCCEEDED + Usage and terminate through the same
			// authoritative zero-Dispatch legal-rejection transaction.
			return loop.commitLegalActionRejection(
				ctx,
				commit,
				currentstore.ModelActionRejectionUnknownAction,
			)
		}
		if run.ChannelIngress != nil {
			return loop.advanceFinalChannel(ctx, run, commit)
		}
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

func runPermitErrorResult(
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	err error,
) (loopapi.RunResult, currentstore.RunLease, error) {
	if !errors.Is(err, currentstore.ErrRunCanceled) {
		return loopapi.RunResult{}, lease, err
	}
	return canceledRunResult(run, lease)
}

func canceledRunResult(
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	reason := reasonRunCanceled
	if run.Manifest.Composite != nil {
		reason = reasonCompositeFamilyCanceled
	}
	return newRunResult(
		run.RunID,
		loopapi.DispositionWaitingExternal,
		reason,
	), lease, nil
}

func (loop *UniversalLoop) invokeModelOutcome(
	ctx context.Context,
	begin currentstore.BeginModelDispatchResult,
) currentstore.CommitModelDispatchOutcomeInput {
	unknown := func(reason string) currentstore.CommitModelDispatchOutcomeInput {
		return currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			UnknownReason:           reason,
		}
	}
	gate, err := ArmInvocationGate(begin)
	if err != nil {
		return unknown(reasonHostErrorAfterPending)
	}
	host, err := modulehost.NewInvocationHostWithCurrentActivation(
		gate,
		loop.registry,
		loop.currentActivation,
	)
	if err != nil {
		return unknown(reasonHostErrorAfterPending)
	}
	invocation := modulehost.ModuleInvocation{
		InvocationID:         begin.Attempt.AttemptID,
		RunID:                begin.Attempt.RunID,
		MemberID:             begin.Attempt.MemberID,
		MemberSnapshotDigest: begin.Attempt.MemberSnapshotDigest,
		Port:                 modelGeneratePortV1,
		BindingIndex:         0,
		Input:                append([]byte(nil), begin.Attempt.Request.CanonicalBytes...),
		Deadline:             begin.Attempt.Deadline,
	}
	invoked, invokeErr := host.Invoke(ctx, invocation)
	return modelOutcomeFromInvocation(begin, invoked, invokeErr)
}

func (loop *UniversalLoop) recoverPending(
	ctx context.Context,
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	dispatch, err := exactContinuedDispatch(run, continued)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	if dispatch.Attempt.State != corecontract.ModelAttemptPending {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: MODEL_PENDING continuation points to %q",
			ErrUniversalLoopIntegrity,
			dispatch.Attempt.State,
		)
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(
		persistCtx,
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   lease,
			AttemptID:               dispatch.Attempt.AttemptID,
			InvocationID:            dispatch.Attempt.AttemptID,
			Provider:                dispatch.Attempt.Binding.Provider,
			ExpectedAttemptRevision: dispatch.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			UnknownReason:           reasonRecoveredPending,
		},
	)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	return newRunResult(
		run.RunID,
		loopapi.DispositionWaitingReconciliation,
		reasonRecoveredPending,
	), committed.Lease, nil
}

func (loop *UniversalLoop) commitUnknown(
	ctx context.Context,
	begin currentstore.BeginModelDispatchResult,
	reason string,
) (loopapi.RunResult, currentstore.RunLease, error) {
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(
		persistCtx,
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			UnknownReason:           reason,
		},
	)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, begin.Lease, err
	}
	return newRunResult(
		begin.Attempt.RunID,
		loopapi.DispositionWaitingReconciliation,
		reason,
	), committed.Lease, nil
}

func modelOutcomeFromInvocation(
	begin currentstore.BeginModelDispatchResult,
	result modulehost.InvocationResult,
	invokeErr error,
) currentstore.CommitModelDispatchOutcomeInput {
	input := currentstore.CommitModelDispatchOutcomeInput{
		Lease:                   begin.Lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Attempt.Revision,
	}
	if invokeErr != nil {
		if errors.Is(invokeErr, modulehost.ErrCurrentActivationDenied) {
			input.State = corecontract.ModelAttemptFailed
			input.ErrorClassification =
				currentActivationDeniedClassification
			return input
		}
		input.State = corecontract.ModelAttemptUnknown
		input.UnknownReason = reasonHostErrorAfterPending
		return input
	}
	if result.InvocationID != begin.Attempt.AttemptID ||
		result.Provider != begin.Attempt.Binding.Provider {
		input.State = corecontract.ModelAttemptUnknown
		input.UnknownReason = reasonInvalidProviderResult
		return input
	}
	if err := modulehost.ValidateInvocationUnknownClass(
		result.Outcome,
		result.UnknownClass,
	); err != nil {
		input.State = corecontract.ModelAttemptUnknown
		input.UnknownReason = reasonInvalidProviderResult
		return input
	}
	usageValid := true
	if len(result.UsageReceipt) != 0 {
		_, err := moduleapi.RestoreModelUsageReceiptV1(result.UsageReceipt)
		usageValid = err == nil
	}
	switch result.Outcome {
	case modulehost.InvocationSucceeded:
		output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
		if err != nil {
			input.State = corecontract.ModelAttemptFailed
			input.ErrorClassification = reasonMalformedModelOutput
			if usageValid {
				input.UsageReceiptCanonical = append(
					[]byte(nil),
					result.UsageReceipt...,
				)
			}
			return input
		}
		if !usageValid {
			input.State = corecontract.ModelAttemptUnknown
			input.UnknownReason = reasonInvalidProviderResult
			return input
		}
		input.State = corecontract.ModelAttemptSucceeded
		input.OutputCanonical = append([]byte(nil), result.Output...)
		input.UsageReceiptCanonical = append(
			[]byte(nil),
			result.UsageReceipt...,
		)
		input.ProviderRequestID = output.ProviderRequestID
	case modulehost.InvocationFailed:
		if len(result.Output) != 0 || !usageValid {
			input.State = corecontract.ModelAttemptUnknown
			input.UnknownReason = reasonInvalidProviderResult
			return input
		}
		input.State = corecontract.ModelAttemptFailed
		input.ErrorClassification = "PROVIDER_REPORTED_FAILURE"
		input.UsageReceiptCanonical = append(
			[]byte(nil),
			result.UsageReceipt...,
		)
	case modulehost.InvocationUnknown:
		input.State = corecontract.ModelAttemptUnknown
		input.UnknownReason = reasonModelUnknown
		if result.UnknownClass != "" {
			input.UnknownReason = string(result.UnknownClass)
		}
		if usageValid {
			input.UsageReceiptCanonical = append(
				[]byte(nil),
				result.UsageReceipt...,
			)
		}
	default:
		input.State = corecontract.ModelAttemptUnknown
		input.UnknownReason = reasonInvalidProviderResult
	}
	return input
}

func runResultForOutcome(
	runID string,
	state corecontract.ModelAttemptState,
	unknownReason string,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	switch state {
	case corecontract.ModelAttemptSucceeded:
		return newRunResult(
			runID,
			loopapi.DispositionTerminated,
			reasonModelSucceeded,
		), lease, nil
	case corecontract.ModelAttemptFailed:
		return newRunResult(
			runID,
			loopapi.DispositionTerminated,
			reasonModelFailed,
		), lease, nil
	case corecontract.ModelAttemptUnknown:
		if unknownReason == "" {
			unknownReason = reasonModelUnknown
		}
		return newRunResult(
			runID,
			loopapi.DispositionWaitingReconciliation,
			unknownReason,
		), lease, nil
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: commit returned non-terminal model state %q",
			ErrUniversalLoopIntegrity,
			state,
		)
	}
}

func exactContinuedDispatch(
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
) (currentstore.ModelDispatchRecord, error) {
	var found *currentstore.ModelDispatchRecord
	for index := range run.ModelDispatches {
		dispatch := run.ModelDispatches[index]
		if dispatch.Attempt.AttemptID != continued.AttemptID {
			continue
		}
		if found != nil {
			return currentstore.ModelDispatchRecord{}, fmt.Errorf(
				"%w: duplicate continued model Attempt",
				ErrUniversalLoopIntegrity,
			)
		}
		copy := dispatch
		found = &copy
	}
	if found == nil ||
		found.Attempt.LogicalStepID != continued.LogicalStepID {
		return currentstore.ModelDispatchRecord{}, fmt.Errorf(
			"%w: continued model Attempt is absent or mismatched",
			ErrUniversalLoopIntegrity,
		)
	}
	return *found, nil
}

func terminalReason(
	state corecontract.ModelAttemptState,
) (string, error) {
	switch state {
	case corecontract.ModelAttemptSucceeded:
		return reasonModelSucceeded, nil
	case corecontract.ModelAttemptFailed:
		return reasonModelFailed, nil
	default:
		return "", fmt.Errorf(
			"%w: terminal continuation points to %q",
			ErrUniversalLoopIntegrity,
			state,
		)
	}
}

func newRunResult(
	runID string,
	disposition loopapi.Disposition,
	reason string,
) loopapi.RunResult {
	return loopapi.RunResult{
		RunID:       runID,
		Disposition: disposition,
		ReasonCode:  reason,
	}
}

func narrowedModelDeadline(
	manifestDeadline time.Time,
	leaseExpiry time.Time,
) time.Time {
	deadline := leaseExpiry.Add(-leaseTailGrace)
	if manifestDeadline.Before(deadline) {
		deadline = manifestDeadline
	}
	return deadline.UTC().Truncate(time.Microsecond)
}

func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), persistenceGrace)
}

func compositeDecisionPersistenceContext(
	ctx context.Context,
	leaseExpiry time.Time,
) (context.Context, context.CancelFunc, error) {
	now := time.Now()
	deadline, err := compositeDecisionPersistenceDeadline(now, leaseExpiry)
	if err != nil {
		return nil, nil, err
	}
	persistCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	return persistCtx, cancel, nil
}

func compositeDecisionPersistenceDeadline(
	now time.Time,
	leaseExpiry time.Time,
) (time.Time, error) {
	latestDeadline := leaseExpiry.Add(-compositeDecisionFinalizationReserve)
	if !latestDeadline.After(now) {
		return time.Time{}, fmt.Errorf(
			"%w: composite decision persistence budget is exhausted",
			context.DeadlineExceeded,
		)
	}
	deadline := now.Add(compositeDecisionPersistenceGrace)
	if latestDeadline.Before(deadline) {
		deadline = latestDeadline
	}
	return deadline, nil
}

func newLoopOwnerID() (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", fmt.Errorf(
			"%w: generate lease owner: %v",
			ErrInvalidUniversalLoop,
			err,
		)
	}
	return loopOwnerIDPrefix + hex.EncodeToString(identity[:]), nil
}

func isNilLoopDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
