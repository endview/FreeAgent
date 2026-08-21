package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyOverviewObservationSemanticClosureV1 is the one full, read-only
// replay gate for the bounded projections consumed by Control Overview. The
// caller owns one coherent read transaction. Online Overview reads may trust
// the immutable projections only after this gate has succeeded for the open
// Store lifetime.
func VerifyOverviewObservationSemanticClosureV1(
	ctx context.Context,
	connection *sql.Conn,
) error {
	if ctx == nil || connection == nil {
		return fmt.Errorf("%w: nil Overview observation verifier", ErrLoopIntegrity)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyRunObservationSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: Run observation closure: %v", ErrLoopIntegrity, err)
	}
	// These authoritative verifiers close the mutable Learning and Module
	// source records before their compact observation ledgers are replayed.
	if err := VerifyLearningProposalSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: Learning Proposal observation source: %v", ErrLoopIntegrity, err)
	}
	if err := VerifyLearningCycleSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: Learning Task observation source: %v", ErrLoopIntegrity, err)
	}
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: Module Review observation source: %v", ErrLoopIntegrity, err)
	}
	if err := verifyOverviewResourceSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: resource observation closure: %v", ErrLoopIntegrity, err)
	}
	if err := verifyOverviewBasisSemanticClosureV1(ctx, connection); err != nil {
		return fmt.Errorf("%w: publication projection closure: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func verifyRunObservationSemanticClosureV1(ctx context.Context, q readQueryerV1) error {
	runIDs, err := overviewStringColumnV1(ctx, q,
		`SELECT run_id FROM runs ORDER BY run_id COLLATE BINARY`)
	if err != nil {
		return err
	}
	headIDs, err := overviewStringColumnV1(ctx, q,
		`SELECT run_id FROM run_observation_heads ORDER BY run_id COLLATE BINARY`)
	if err != nil {
		return err
	}
	if !equalOverviewStringsV1(runIDs, headIDs) {
		return fmt.Errorf("%w: Runs and observation heads differ", ErrLoopIntegrity)
	}
	var storeID string
	if err := q.QueryRowContext(ctx, `SELECT store_instance_id FROM store_meta
		WHERE singleton=1`).Scan(&storeID); err != nil || !validLeaseOpaqueID(storeID) {
		return fmt.Errorf("%w: observation Store identity: %v", ErrLoopIntegrity, err)
	}
	var expectedSnapshotCount uint64
	for _, runID := range runIDs {
		run, err := loadRunForObservationV1(ctx, q, runID)
		if err != nil {
			return fmt.Errorf("%w: authoritative Run %q: %v", ErrLoopIntegrity, runID, err)
		}
		head, err := loadRunObservationHeadV1(ctx, q, runID)
		if err != nil {
			return err
		}
		digests, err := overviewRunSnapshotDigestsV1(ctx, q, runID)
		if err != nil || len(digests) != int(head.Snapshot.ObservationSequence) ||
			len(digests) == 0 || digests[len(digests)-1] != head.Digest {
			return fmt.Errorf("%w: Run observation chain length/head: %v", ErrLoopIntegrity, err)
		}
		var previous *runObservationRecordV1
		for index, digest := range digests {
			current, err := loadRunObservationSnapshotV1(ctx, q, digest)
			if err != nil || current.Snapshot.RunID != runID ||
				current.Snapshot.ObservationSequence != uint64(index+1) ||
				current.Snapshot.StoreInstanceID != storeID {
				return fmt.Errorf("%w: Run observation chain member: %v", ErrLoopIntegrity, err)
			}
			if err := validateRunObservationAdvanceV1(previous, current); err != nil {
				return err
			}
			if current.Snapshot.TransitionKind != runObservationCancellationV1 {
				if err := verifyRunObservationEventV1(ctx, q, current.Snapshot); err != nil {
					return fmt.Errorf("%w: Run observation source event: %v", ErrLoopIntegrity, err)
				}
			}
			if err := verifyRunObservationHistoricalProjectionV1(
				ctx, q, run.Manifest, previous, current,
			); err != nil {
				return err
			}
			copy := current
			previous = &copy
		}
		expectedSnapshotCount += head.Snapshot.ObservationSequence
	}
	var actualSnapshotCount uint64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_observation_snapshots`).Scan(
		&actualSnapshotCount); err != nil || actualSnapshotCount != expectedSnapshotCount {
		return fmt.Errorf("%w: orphan Run observation snapshot: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func overviewRunSnapshotDigestsV1(
	ctx context.Context,
	q readQueryerV1,
	runID string,
) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT snapshot_digest FROM run_observation_snapshots
		WHERE run_id=? ORDER BY observation_sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, err
		}
		result = append(result, digest)
	}
	return result, rows.Err()
}

func verifyRunObservationHistoricalProjectionV1(
	ctx context.Context,
	q readQueryerV1,
	manifest corecontract.RunManifest,
	previous *runObservationRecordV1,
	current runObservationRecordV1,
) error {
	s := current.Snapshot
	budgetSequence, err := corecontract.ParseBudgetStateRefV1(s.BudgetStateRef, s.RunID)
	if err != nil {
		return fmt.Errorf("%w: historical Run budget projection: %v", ErrLoopIntegrity, err)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(current.Continuation)
	if err != nil || continued.State != s.FrameStep {
		return fmt.Errorf("%w: historical Run continuation: %v", ErrLoopIntegrity, err)
	}
	frame := LoopFrameRecord{
		RunID: s.RunID, Step: s.FrameStep, BudgetStateRef: s.BudgetStateRef,
		Continuation: current.Continuation, PendingAttemptID: s.PendingModelAttemptID,
		PendingDispatchAttemptID: s.PendingDispatchAttemptID,
		WaitingReason:            s.WaitingReason, LastAuthoritativeEvent: s.SourceEventSequence,
	}
	if err := validateLoopRunFrameProjection(s.RunState, s.Disposition, frame); err != nil {
		return fmt.Errorf("%w: historical Run/Frame projection: %v", ErrLoopIntegrity, err)
	}
	if err := validateFairTargetContinuationPointers(
		continued, nullableSQLStringV1(s.PendingModelAttemptID),
		nullableSQLStringV1(s.PendingDispatchAttemptID),
	); err != nil {
		return fmt.Errorf("%w: historical Run pending projection: %v", ErrLoopIntegrity, err)
	}

	if previous == nil {
		return verifyRunObservationAdmissionProjectionV1(
			ctx, q, manifest, current, budgetSequence,
		)
	}
	p := previous.Snapshot
	previousBudget, err := corecontract.ParseBudgetStateRefV1(p.BudgetStateRef, p.RunID)
	if err != nil {
		return fmt.Errorf("%w: previous historical Run budget projection: %v", ErrLoopIntegrity, err)
	}
	if s.TransitionKind == runObservationCancellationV1 {
		content, err := queryContent(ctx, q, s.CancelRequestRef)
		if err != nil || content.Kind != ContentRunCancellation ||
			uint64(content.CreatedAt.UnixMicro()) > s.UpdatedAtUnixMicros {
			return fmt.Errorf("%w: historical Run cancellation content/time: %v", ErrLoopIntegrity, err)
		}
		request, err := corecontract.RestoreRunCancellationRequestV1(content.CanonicalBytes)
		if err != nil || (request.Scope == corecontract.CancellationScopeRunV1 &&
			request.RootRunID != s.RunID) ||
			(request.Scope == corecontract.CancellationScopeFamilyV1 &&
				(manifest.Composite == nil || request.RootRunID != manifest.Composite.RootRunID)) {
			return fmt.Errorf("%w: historical Run cancellation identity: %v", ErrLoopIntegrity, err)
		}
		return nil
	}
	if p.CancelRequestRef != "" || s.CancelRequestRef != "" {
		return fmt.Errorf("%w: event transition after Run cancellation", ErrLoopIntegrity)
	}
	facts, err := deriveRunObservationEventFactsV1(ctx, q, s)
	if err != nil || facts.ToRevision != s.SourceEventFrameRevision ||
		facts.ToRevision != facts.FromRevision+1 ||
		facts.FromRevision < p.SourceEventFrameRevision {
		return fmt.Errorf("%w: historical Run event revision: %v", ErrLoopIntegrity, err)
	}
	if s.TransitionKind != runObservationModelBeginV1 {
		if s.RunRevision != p.RunRevision+1 || s.UpdatedAtUnixMicros != facts.CreatedAt {
			return fmt.Errorf("%w: historical Run event revision/time", ErrLoopIntegrity)
		}
	}

	modelTransition, modelResourceID, modelSemanticDigest := "", "", ""
	switch s.TransitionKind {
	case runObservationModelBeginV1:
		modelTransition, modelResourceID = overviewTransitionModelBeginV1, facts.AttemptID
		modelSemanticDigest = facts.ResourceSemanticDigest
	case runObservationModelOutcomeV1:
		modelTransition, modelResourceID = overviewTransitionModelOutcomeV1, facts.AttemptID
		modelSemanticDigest = facts.ResourceSemanticDigest
		if p.FrameStep != corecontract.ModelPendingLoopStep &&
			p.FrameStep != corecontract.WaitingReconciliationLoopStep {
			modelTransition = overviewTransitionModelExpiredV1
		}
	case runObservationActionBeginV1:
		modelTransition, modelResourceID = overviewTransitionModelActionSourceV1,
			p.PendingModelAttemptID
		modelSemanticDigest = facts.SourceModelSemanticDigest
	case runObservationChannelBeginV1:
		modelTransition, modelResourceID = overviewTransitionModelChannelSourceV1,
			p.PendingModelAttemptID
		modelSemanticDigest = facts.SourceModelSemanticDigest
	case runObservationModelRejectionV1:
		modelTransition, modelResourceID = overviewTransitionModelOutcomeV1, facts.AttemptID
		modelSemanticDigest = facts.ResourceSemanticDigest
	case runObservationStartupRecoveryV1:
		if facts.ModelState == corecontract.ModelAttemptUnknown {
			modelTransition, modelResourceID = overviewTransitionModelRecoveryV1, facts.AttemptID
			modelSemanticDigest = facts.ResourceSemanticDigest
		}
	}
	boundBudget, err := historicalRunBudgetFromModelResourceV1(
		ctx, q, current, previousBudget, modelTransition, modelResourceID,
		facts.ModelUsage, modelSemanticDigest, facts.SourceModelSemanticDigest,
	)
	if err != nil {
		return err
	}
	requireBudget := func(allowAdvance bool) error {
		if budgetSequence != boundBudget || budgetSequence < previousBudget ||
			budgetSequence > previousBudget+1 ||
			(!allowAdvance && budgetSequence != previousBudget) {
			return fmt.Errorf("%w: historical Run budget advance", ErrLoopIntegrity)
		}
		return nil
	}
	requireAttempt := func(
		step string, kind corecontract.AttemptKindV1, runState, disposition,
		pendingModel, pendingDispatch, waiting string,
	) error {
		want, err := corecontract.NewLoopContinuationForAttemptV1(
			step, kind, facts.LogicalStepID, facts.AttemptID,
		)
		if err != nil || !bytes.Equal(want, current.Continuation) ||
			s.RunState != runState || s.Disposition != disposition ||
			s.PendingModelAttemptID != pendingModel ||
			s.PendingDispatchAttemptID != pendingDispatch || s.WaitingReason != waiting {
			return fmt.Errorf("%w: historical Run transition projection: %v", ErrLoopIntegrity, err)
		}
		return nil
	}

	switch s.TransitionKind {
	case runObservationModelBeginV1:
		if facts.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			facts.ModelState != corecontract.ModelAttemptPending ||
			(p.FrameStep != corecontract.InitialLoopStep &&
				p.FrameStep != corecontract.ModelReadyAfterActionLoopStep &&
				p.FrameStep != corecontract.WaitingChildrenLoopStep) ||
			facts.AttemptID == "" {
			return fmt.Errorf("%w: historical Model begin source", ErrLoopIntegrity)
		}
		if p.FrameStep == corecontract.WaitingChildrenLoopStep {
			if s.RunRevision != p.RunRevision+1 || s.UpdatedAtUnixMicros != facts.CreatedAt {
				return fmt.Errorf("%w: coordinator Model begin Run projection", ErrLoopIntegrity)
			}
		} else if s.RunRevision != p.RunRevision ||
			s.UpdatedAtUnixMicros != p.UpdatedAtUnixMicros {
			return fmt.Errorf("%w: ordinary Model begin Run projection", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		return requireAttempt(corecontract.ModelPendingLoopStep, corecontract.AttemptKindModel,
			corecontract.InitialRunState, "", facts.AttemptID, "", "")

	case runObservationModelOutcomeV1:
		if facts.ModelState != corecontract.ModelAttemptSucceeded &&
			facts.ModelState != corecontract.ModelAttemptFailed &&
			facts.ModelState != corecontract.ModelAttemptUnknown {
			return fmt.Errorf("%w: historical Model outcome state", ErrLoopIntegrity)
		}
		expired := p.FrameStep != corecontract.ModelPendingLoopStep &&
			p.FrameStep != corecontract.WaitingReconciliationLoopStep
		if expired {
			if facts.TransitionOrigin != corecontract.DispatchTransitionExpiredBeforeNetworkV1 ||
				facts.ModelState != corecontract.ModelAttemptFailed ||
				(p.FrameStep != corecontract.InitialLoopStep &&
					p.FrameStep != corecontract.ModelReadyAfterActionLoopStep &&
					p.FrameStep != corecontract.WaitingChildrenLoopStep) {
				return fmt.Errorf("%w: expired-before-network Run transition", ErrLoopIntegrity)
			}
			if err := requireBudget(false); err != nil {
				return err
			}
		} else {
			if facts.TransitionOrigin != corecontract.DispatchTransitionOrdinaryOutcomeV1 {
				return fmt.Errorf("%w: ordinary Model outcome origin", ErrLoopIntegrity)
			}
			if err := requireBudget(true); err != nil {
				return err
			}
		}
		if facts.ModelState == corecontract.ModelAttemptUnknown {
			return requireAttempt(corecontract.WaitingReconciliationLoopStep,
				corecontract.AttemptKindModel, corecontract.WaitingReconciliationLoopStep,
				corecontract.WaitingReconciliationLoopStep, facts.AttemptID, "", modelUnknownWaitingReason)
		}
		return requireAttempt(corecontract.TerminatedLoopStep, corecontract.AttemptKindModel,
			corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, "", "", "")

	case runObservationActionBeginV1, runObservationChannelBeginV1:
		if p.FrameStep != corecontract.ModelPendingLoopStep ||
			facts.DispatchState != DispatchPending ||
			facts.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			facts.SourceModelAttemptID != p.PendingModelAttemptID {
			return fmt.Errorf("%w: historical dispatch begin source", ErrLoopIntegrity)
		}
		if err := requireBudget(true); err != nil {
			return err
		}
		kind, step := corecontract.AttemptKindAction, corecontract.ActionPendingLoopStep
		resourceKind, resourceTransition := overviewResourceActionV1, overviewTransitionActionBeginV1
		if s.TransitionKind == runObservationChannelBeginV1 {
			kind, step = corecontract.AttemptKindChannel, corecontract.ChannelPendingLoopStep
			resourceKind, resourceTransition = overviewResourceChannelV1, overviewTransitionChannelBeginV1
		}
		if err := verifyHistoricalRunDispatchResourceV1(
			ctx, q, current, facts, resourceKind, resourceTransition,
		); err != nil {
			return err
		}
		return requireAttempt(step, kind, corecontract.InitialRunState, "", "", facts.AttemptID, "")

	case runObservationActionOutcomeV1:
		if p.FrameStep != corecontract.ActionPendingLoopStep &&
			p.FrameStep != corecontract.WaitingReconciliationLoopStep {
			return fmt.Errorf("%w: historical Action outcome source", ErrLoopIntegrity)
		}
		if facts.TransitionOrigin != corecontract.DispatchTransitionOrdinaryOutcomeV1 {
			return fmt.Errorf("%w: ordinary Action outcome origin", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		if err := verifyHistoricalRunDispatchResourceV1(
			ctx, q, current, facts, overviewResourceActionV1, overviewTransitionActionOutcomeV1,
		); err != nil {
			return err
		}
		switch facts.DispatchState {
		case DispatchUnknown:
			return requireAttempt(corecontract.WaitingReconciliationLoopStep,
				corecontract.AttemptKindAction, corecontract.WaitingReconciliationLoopStep,
				corecontract.WaitingReconciliationLoopStep, "", "", actionUnknownWaitingReason)
		case DispatchSucceeded:
			if facts.ActionResult == corecontract.ActionResultAvailable {
				return requireAttempt(corecontract.ModelReadyAfterActionLoopStep,
					corecontract.AttemptKindAction, corecontract.InitialRunState, "", "", "", "")
			}
			fallthrough
		case DispatchFailed:
			return requireAttempt(corecontract.TerminatedLoopStep, corecontract.AttemptKindAction,
				corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, "", "", "")
		default:
			return fmt.Errorf("%w: historical Action outcome state", ErrLoopIntegrity)
		}

	case runObservationChannelOutcomeV1:
		if p.FrameStep != corecontract.ChannelPendingLoopStep &&
			p.FrameStep != corecontract.WaitingReconciliationLoopStep {
			return fmt.Errorf("%w: historical Channel outcome source", ErrLoopIntegrity)
		}
		if facts.TransitionOrigin != corecontract.DispatchTransitionOrdinaryOutcomeV1 {
			return fmt.Errorf("%w: ordinary Channel outcome origin", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		if err := verifyHistoricalRunDispatchResourceV1(
			ctx, q, current, facts, overviewResourceChannelV1, overviewTransitionChannelOutcomeV1,
		); err != nil {
			return err
		}
		if facts.DispatchState == DispatchUnknown {
			return requireAttempt(corecontract.WaitingReconciliationLoopStep,
				corecontract.AttemptKindChannel, corecontract.WaitingReconciliationLoopStep,
				corecontract.WaitingReconciliationLoopStep, "", "", channelUnknownWaitingReason)
		}
		if facts.DispatchState != DispatchSucceeded && facts.DispatchState != DispatchFailed {
			return fmt.Errorf("%w: historical Channel outcome state", ErrLoopIntegrity)
		}
		return requireAttempt(corecontract.TerminatedLoopStep, corecontract.AttemptKindChannel,
			corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, "", "", "")

	case runObservationModelRejectionV1:
		if p.FrameStep != corecontract.ModelPendingLoopStep {
			return fmt.Errorf("%w: historical Model rejection source", ErrLoopIntegrity)
		}
		if err := requireBudget(true); err != nil {
			return err
		}
		return requireAttempt(corecontract.TerminatedLoopStep, corecontract.AttemptKindModel,
			corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, "", "", "")

	case runObservationStartupRecoveryV1:
		if facts.TransitionOrigin != corecontract.DispatchTransitionStartupRecoveryV1 {
			return fmt.Errorf("%w: startup recovery origin", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		kind, waiting := corecontract.AttemptKindModel, modelUnknownWaitingReason
		pendingModel := facts.AttemptID
		valid := p.FrameStep == corecontract.ModelPendingLoopStep &&
			facts.ModelState == corecontract.ModelAttemptUnknown
		if facts.DispatchState == DispatchUnknown && p.FrameStep == corecontract.ActionPendingLoopStep {
			kind, waiting, pendingModel, valid = corecontract.AttemptKindAction, actionUnknownWaitingReason, "", true
		} else if facts.DispatchState == DispatchUnknown && p.FrameStep == corecontract.ChannelPendingLoopStep {
			kind, waiting, pendingModel, valid = corecontract.AttemptKindChannel, channelUnknownWaitingReason, "", true
		}
		if !valid {
			return fmt.Errorf("%w: historical startup recovery source", ErrLoopIntegrity)
		}
		if kind == corecontract.AttemptKindAction {
			if err := verifyHistoricalRunDispatchResourceV1(
				ctx, q, current, facts, overviewResourceActionV1, overviewTransitionActionRecoveryV1,
			); err != nil {
				return err
			}
		} else if kind == corecontract.AttemptKindChannel {
			if err := verifyHistoricalRunDispatchResourceV1(
				ctx, q, current, facts, overviewResourceChannelV1, overviewTransitionChannelRecoveryV1,
			); err != nil {
				return err
			}
		}
		return requireAttempt(corecontract.WaitingReconciliationLoopStep, kind,
			corecontract.WaitingReconciliationLoopStep, corecontract.WaitingReconciliationLoopStep,
			pendingModel, "", waiting)

	case runObservationCoreFailureV1:
		if p.FrameStep != corecontract.WaitingChildrenLoopStep ||
			p.Disposition != "WAITING_EXTERNAL" {
			return fmt.Errorf("%w: historical Core failure source", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		want, err := corecontract.NewCoreFailureLoopContinuationV1(s.ContinuationCoreFailureReason)
		if err != nil || !bytes.Equal(want, current.Continuation) ||
			s.RunState != corecontract.TerminatedLoopStep ||
			s.Disposition != corecontract.TerminatedLoopStep ||
			s.PendingModelAttemptID != "" || s.PendingDispatchAttemptID != "" || s.WaitingReason != "" {
			return fmt.Errorf("%w: historical Core failure projection: %v", ErrLoopIntegrity, err)
		}
		return nil

	case runObservationCompositeTransitionV1:
		if manifest.Composite == nil || manifest.Composite.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			p.FrameStep != corecontract.WaitingRepairActivationLoopStep ||
			p.Disposition != "WAITING_EXTERNAL" {
			return fmt.Errorf("%w: historical composite transition source", ErrLoopIntegrity)
		}
		if err := requireBudget(false); err != nil {
			return err
		}
		if facts.CompositeKind == corecontract.CompositeRepairSkippedEventKind {
			want, err := corecontract.NewCoreFailureLoopContinuationV1(corecontract.CompositeRepairSkippedReasonV1)
			if err != nil || !bytes.Equal(want, current.Continuation) ||
				s.RunState != corecontract.TerminatedLoopStep ||
				s.Disposition != corecontract.TerminatedLoopStep {
				return fmt.Errorf("%w: historical composite skip projection: %v", ErrLoopIntegrity, err)
			}
			return nil
		}
		var wantBudget string
		var wantContinuation []byte
		if manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 {
			wantBudget, wantContinuation, err = corecontract.NewInitialLoopState(s.RunID)
			if err == nil && (s.RunState != corecontract.InitialRunState || s.Disposition != "" ||
				s.FrameStep != corecontract.InitialLoopStep || s.WaitingReason != "") {
				err = ErrLoopIntegrity
			}
		} else if manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1 {
			wantBudget, wantContinuation, err = corecontract.NewWaitingChildrenLoopState(s.RunID)
			if err == nil && (s.RunState != corecontract.InitialRunState || s.Disposition != "WAITING_EXTERNAL" ||
				s.FrameStep != corecontract.WaitingChildrenLoopStep ||
				s.WaitingReason != compositeChildrenPendingWaitingReason) {
				err = ErrLoopIntegrity
			}
		} else {
			err = ErrLoopIntegrity
		}
		if err != nil || wantBudget != s.BudgetStateRef || !bytes.Equal(wantContinuation, current.Continuation) ||
			s.PendingModelAttemptID != "" || s.PendingDispatchAttemptID != "" {
			return fmt.Errorf("%w: historical composite activation projection: %v", ErrLoopIntegrity, err)
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported historical Run transition", ErrLoopIntegrity)
}

// verifyHistoricalRunDispatchResourceV1 closes the reverse side of the
// immutable Run-event/resource relationship.  The resource verifier proves
// every surviving resource snapshot points at an appropriate Run snapshot;
// this verifier additionally proves every Action/Channel Run transition has
// exactly one resource snapshot.  Without the reverse cardinality check a
// coordinated rewrite could remove one historical resource mutation and
// re-chain the remaining resource snapshots while leaving the immutable Run
// event in place.
func verifyHistoricalRunDispatchResourceV1(
	ctx context.Context,
	q readQueryerV1,
	run runObservationRecordV1,
	facts runObservationEventFactsV1,
	resourceKind, expectedTransition string,
) error {
	rows, err := q.QueryContext(ctx, `SELECT snapshot_digest
		FROM overview_resource_snapshots INDEXED BY overview_resource_snapshots_causal_run_idx
		WHERE resource_kind=? AND causal_run_id=?
		  AND causal_run_observation_sequence=? AND causal_run_observation_digest=?
		ORDER BY resource_id COLLATE BINARY LIMIT 2`,
		resourceKind, run.Snapshot.RunID,
		int64(run.Snapshot.ObservationSequence), run.Digest)
	if err != nil {
		return fmt.Errorf("%w: historical Run dispatch resource query: %v", ErrLoopIntegrity, err)
	}
	digests := make([]string, 0, 2)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: historical Run dispatch resource row: %v", ErrLoopIntegrity, err)
		}
		digests = append(digests, digest)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil || closeErr != nil {
		return fmt.Errorf("%w: historical Run dispatch resource rows: %v/%v",
			ErrLoopIntegrity, rowsErr, closeErr)
	}
	if len(digests) != 1 || !validLeaseOpaqueID(facts.AttemptID) ||
		!moduleapi.ValidSHA256(facts.ResourceSemanticDigest) {
		return fmt.Errorf("%w: historical Run dispatch resource cardinality", ErrLoopIntegrity)
	}
	resource, err := loadOverviewResourceSnapshotV1(ctx, q, digests[0])
	if err != nil {
		return fmt.Errorf("%w: historical Run dispatch resource projection load: %v",
			ErrLoopIntegrity, err)
	}
	projection := resource.Snapshot
	if projection.ResourceKind != resourceKind {
		return fmt.Errorf("%w: historical Run dispatch resource projection kind",
			ErrLoopIntegrity)
	}
	if projection.ResourceID != facts.AttemptID {
		return fmt.Errorf("%w: historical Run dispatch resource projection identity",
			ErrLoopIntegrity)
	}
	if projection.TransitionKind != expectedTransition {
		return fmt.Errorf("%w: historical Run dispatch resource projection transition",
			ErrLoopIntegrity)
	}
	if projection.CausalRun == nil ||
		projection.CausalRun.RunID != run.Snapshot.RunID ||
		projection.CausalRun.Sequence != run.Snapshot.ObservationSequence ||
		projection.CausalRun.Digest != run.Digest {
		return fmt.Errorf("%w: historical Run dispatch resource projection causal Run",
			ErrLoopIntegrity)
	}
	if projection.State != string(facts.DispatchState) {
		return fmt.Errorf("%w: historical Run dispatch resource projection state",
			ErrLoopIntegrity)
	}
	if projection.UpdatedAtUnixMicros != facts.CreatedAt {
		return fmt.Errorf("%w: historical Run dispatch resource projection timestamp",
			ErrLoopIntegrity)
	}
	if projection.SemanticDigest != facts.ResourceSemanticDigest {
		return fmt.Errorf("%w: historical Run dispatch resource projection semantic digest for %s",
			ErrLoopIntegrity, expectedTransition)
	}
	return nil
}

// historicalRunBudgetFromModelResourceV1 binds the Run's BudgetStateRef to
// the one MODEL resource mutation carried by the same immutable Run
// observation. A nil Usage ledger sequence means that mutation did not
// allocate a new ledger entry, so the preceding Run budget remains current.
func historicalRunBudgetFromModelResourceV1(
	ctx context.Context,
	q readQueryerV1,
	run runObservationRecordV1,
	previousBudget uint64,
	expectedTransition, expectedResourceID string,
	expectedUsage *corecontract.ModelUsageEventV1,
	expectedResourceSemanticDigest, expectedSourceModelSemanticDigest string,
) (uint64, error) {
	rows, err := q.QueryContext(ctx, `SELECT snapshot_digest
		FROM overview_resource_snapshots INDEXED BY overview_resource_snapshots_causal_run_idx
		WHERE resource_kind=? AND causal_run_id=?
		  AND causal_run_observation_sequence=? AND causal_run_observation_digest=?
		ORDER BY resource_id COLLATE BINARY LIMIT 2`,
		overviewResourceModelV1, run.Snapshot.RunID,
		int64(run.Snapshot.ObservationSequence), run.Digest)
	if err != nil {
		return 0, fmt.Errorf("%w: historical Run MODEL resource query: %v", ErrLoopIntegrity, err)
	}
	digests := make([]string, 0, 2)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("%w: historical Run MODEL resource row: %v", ErrLoopIntegrity, err)
		}
		digests = append(digests, digest)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil || closeErr != nil {
		return 0, fmt.Errorf("%w: historical Run MODEL resource rows: %v/%v",
			ErrLoopIntegrity, rowsErr, closeErr)
	}
	if expectedTransition == "" {
		if len(digests) != 0 || expectedUsage != nil ||
			expectedResourceSemanticDigest != "" || expectedSourceModelSemanticDigest != "" {
			return 0, fmt.Errorf("%w: unexpected historical Run MODEL resource", ErrLoopIntegrity)
		}
		return previousBudget, nil
	}
	if !validLeaseOpaqueID(expectedResourceID) || len(digests) != 1 {
		return 0, fmt.Errorf("%w: historical Run MODEL resource cardinality", ErrLoopIntegrity)
	}
	resource, err := loadOverviewResourceSnapshotV1(ctx, q, digests[0])
	wantModelSemanticDigest := expectedResourceSemanticDigest
	if expectedSourceModelSemanticDigest != "" {
		wantModelSemanticDigest = expectedSourceModelSemanticDigest
	}
	if err != nil || resource.Snapshot.ResourceKind != overviewResourceModelV1 ||
		resource.Snapshot.ResourceID != expectedResourceID ||
		resource.Snapshot.TransitionKind != expectedTransition ||
		resource.Snapshot.CausalRun == nil ||
		resource.Snapshot.CausalRun.RunID != run.Snapshot.RunID ||
		resource.Snapshot.CausalRun.Sequence != run.Snapshot.ObservationSequence ||
		resource.Snapshot.CausalRun.Digest != run.Digest ||
		resource.Snapshot.SemanticDigest != wantModelSemanticDigest {
		return 0, fmt.Errorf("%w: historical Run MODEL resource projection: %v",
			ErrLoopIntegrity, err)
	}
	if expectedTransition == overviewTransitionModelBeginV1 {
		if expectedUsage != nil {
			return 0, fmt.Errorf("%w: Model begin event carries Usage", ErrLoopIntegrity)
		}
	} else if !overviewModelUsageEventMatchesResourceV1(expectedUsage, resource.Snapshot) {
		return 0, fmt.Errorf("%w: historical Run MODEL Usage projection", ErrLoopIntegrity)
	}
	if expectedSourceModelSemanticDigest != "" && expectedResourceSemanticDigest == "" {
		return 0, fmt.Errorf("%w: missing dispatch resource semantic projection", ErrLoopIntegrity)
	}
	if expectedSourceModelSemanticDigest != "" &&
		expectedTransition != overviewTransitionModelActionSourceV1 &&
		expectedTransition != overviewTransitionModelChannelSourceV1 {
		return 0, fmt.Errorf("%w: unexpected source Model semantic projection", ErrLoopIntegrity)
	}
	if resource.Snapshot.UsageLedgerSequence == nil {
		return previousBudget, nil
	}
	return *resource.Snapshot.UsageLedgerSequence, nil
}

func overviewModelUsageEventMatchesResourceV1(
	usage *corecontract.ModelUsageEventV1,
	resource overviewResourceCanonicalV1,
) bool {
	return usage != nil && resource.UsageRevision != nil &&
		usage.Revision == *resource.UsageRevision &&
		overviewOptionalUintEqualV1(usage.LedgerSequence, resource.UsageLedgerSequence) &&
		usage.ReconciliationStatus == resource.UsageStatus &&
		overviewOptionalUintEqualV1(usage.Tokens.Input, resource.InputTokens) &&
		overviewOptionalUintEqualV1(usage.Tokens.CachedInput, resource.CachedInputTokens) &&
		overviewOptionalUintEqualV1(usage.Tokens.UncachedInput, resource.UncachedInputTokens) &&
		overviewOptionalUintEqualV1(usage.Tokens.Output, resource.OutputTokens) &&
		overviewOptionalUintEqualV1(usage.Tokens.Reasoning, resource.ReasoningTokens) &&
		usage.SemanticDigest == resource.UsageSemanticDigest
}

func overviewOptionalUintEqualV1(left, right *uint64) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && *left == *right)
}

func verifyRunObservationAdmissionProjectionV1(
	ctx context.Context,
	q readQueryerV1,
	manifest corecontract.RunManifest,
	current runObservationRecordV1,
	budgetSequence uint64,
) error {
	s := current.Snapshot
	facts, eventErr := deriveRunObservationEventFactsV1(ctx, q, s)
	if s.SourceEventSequence != 0 || s.SourceEventFrameRevision != 0 || s.RunRevision != 0 ||
		s.CancelRequestRef != "" || s.CreatedAtUnixMicros != s.UpdatedAtUnixMicros ||
		budgetSequence != 0 || manifest.RunID != s.RunID || manifest.ManifestDigest != s.ManifestDigest ||
		eventErr != nil || facts.FromRevision != 0 || facts.ToRevision != 0 ||
		facts.CreatedAt != s.CreatedAtUnixMicros {
		return fmt.Errorf("%w: historical admission identity", ErrLoopIntegrity)
	}
	var wantBudget string
	var wantContinuation []byte
	wantState, wantDisposition, wantStep, wantWaiting := corecontract.InitialRunState, "", corecontract.InitialLoopStep, ""
	wantBudget, wantContinuation, err := corecontract.NewInitialLoopState(s.RunID)
	if manifest.Composite != nil && manifest.Composite.RepairRound == corecontract.CompositeRepairRoundOneV1 {
		wantDisposition, wantStep, wantWaiting = "WAITING_EXTERNAL", corecontract.WaitingRepairActivationLoopStep, compositeRepairDormantWaitingReason
		wantBudget, wantContinuation, err = corecontract.NewWaitingRepairActivationLoopState(s.RunID)
	} else if manifest.Composite != nil &&
		(manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 ||
			manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) {
		wantDisposition, wantStep, wantWaiting = "WAITING_EXTERNAL", corecontract.WaitingChildrenLoopStep, compositeChildrenPendingWaitingReason
		wantBudget, wantContinuation, err = corecontract.NewWaitingChildrenLoopState(s.RunID)
	}
	if err != nil || s.RunState != wantState || s.Disposition != wantDisposition ||
		s.FrameStep != wantStep || s.WaitingReason != wantWaiting ||
		s.PendingModelAttemptID != "" || s.PendingDispatchAttemptID != "" ||
		s.BudgetStateRef != wantBudget || !bytes.Equal(current.Continuation, wantContinuation) {
		return fmt.Errorf("%w: historical admission projection: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func nullableSQLStringV1(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func verifyOverviewResourceSemanticClosureV1(ctx context.Context, q readQueryerV1) error {
	for _, item := range []struct {
		kind     string
		rawCount string
	}{
		{overviewResourceModelV1, `SELECT COUNT(*) FROM model_dispatch_attempts`},
		{overviewResourceActionV1, `SELECT COUNT(*) FROM dispatch_attempts WHERE dispatch_kind='ACTION'`},
		{overviewResourceChannelV1, `SELECT COUNT(*) FROM dispatch_attempts WHERE dispatch_kind='CHANNEL_SEND'`},
		{overviewResourceLearningProposalV1, `SELECT COUNT(*) FROM learning_proposals`},
		{overviewResourceLearningTaskV1, `SELECT COUNT(*) FROM learning_cycle_tasks`},
		{overviewResourceModuleReviewV1, `SELECT COUNT(*) FROM module_upgrade_reviews`},
	} {
		var rawCount, headCount int64
		if err := q.QueryRowContext(ctx, item.rawCount).Scan(&rawCount); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM overview_resource_heads
			WHERE resource_kind=?`, item.kind).Scan(&headCount); err != nil ||
			rawCount < 0 || rawCount != headCount {
			return fmt.Errorf("%w: raw/head count for %s: %v", ErrLoopIntegrity, item.kind, err)
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT resource_kind,resource_id,
		observation_sequence,snapshot_digest FROM overview_resource_heads
		ORDER BY resource_kind COLLATE BINARY,resource_id COLLATE BINARY`)
	if err != nil {
		return err
	}
	type headV1 struct {
		kind, id, digest string
		sequence         int64
	}
	heads := make([]headV1, 0)
	for rows.Next() {
		var head headV1
		if err := rows.Scan(&head.kind, &head.id, &head.sequence, &head.digest); err != nil {
			_ = rows.Close()
			return err
		}
		heads = append(heads, head)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var expectedSnapshotCount int64
	for _, head := range heads {
		if head.sequence <= 0 || !validOverviewResourceKindV1(head.kind) ||
			!validLeaseOpaqueID(head.id) || !moduleapi.ValidSHA256(head.digest) {
			return fmt.Errorf("%w: invalid resource head identity", ErrLoopIntegrity)
		}
		if err := verifyCurrentOverviewResourceObservationV1(ctx, q, head.kind, head.id); err != nil {
			return err
		}
		digests, err := overviewResourceSnapshotDigestsV1(ctx, q, head.kind, head.id)
		if err != nil || len(digests) != int(head.sequence) || len(digests) == 0 ||
			digests[len(digests)-1] != head.digest {
			return fmt.Errorf("%w: resource observation chain length/head: %v", ErrLoopIntegrity, err)
		}
		var previous *overviewResourceRecordV1
		for index, digest := range digests {
			current, err := loadOverviewResourceSnapshotV1(ctx, q, digest)
			if err != nil || current.Snapshot.ResourceKind != head.kind ||
				current.Snapshot.ResourceID != head.id ||
				current.Snapshot.ObservationSequence != uint64(index+1) {
				return fmt.Errorf("%w: resource observation chain member: %v", ErrLoopIntegrity, err)
			}
			if err := verifyOverviewResourceTransitionCarrierV1(ctx, q, current); err != nil {
				return err
			}
			if err := verifyOverviewResourceRunRefsOnlineV1(ctx, q, current.Snapshot); err != nil {
				return err
			}
			if err := verifyOverviewResourceCausalRunRefsV1(ctx, q, current); err != nil {
				return err
			}
			if err := validateOverviewResourceAdvanceV1(previous, current); err != nil {
				return err
			}
			copy := current
			previous = &copy
		}
		expectedSnapshotCount += head.sequence
	}
	var actualSnapshotCount int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM overview_resource_snapshots`).Scan(
		&actualSnapshotCount); err != nil || actualSnapshotCount != expectedSnapshotCount {
		return fmt.Errorf("%w: orphan resource observation snapshot: %v", ErrLoopIntegrity, err)
	}
	var carrierCount int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM overview_resource_transition_carriers`).Scan(&carrierCount); err != nil ||
		carrierCount != actualSnapshotCount {
		return fmt.Errorf("%w: resource transition carrier cardinality: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func verifyOverviewResourceTransitionCarrierV1(
	ctx context.Context,
	q readQueryerV1,
	record overviewResourceRecordV1,
) error {
	var digest, storeInstanceID string
	err := q.QueryRowContext(ctx, `SELECT snapshot_digest,store_instance_id
		FROM overview_resource_transition_carriers
		WHERE resource_kind=? AND resource_id=? AND observation_sequence=?`,
		record.Snapshot.ResourceKind, record.Snapshot.ResourceID,
		int64(record.Snapshot.ObservationSequence)).Scan(&digest, &storeInstanceID)
	if err != nil || digest != record.Digest || storeInstanceID != record.Snapshot.StoreInstanceID {
		return fmt.Errorf("%w: resource transition carrier: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func overviewResourceSnapshotDigestsV1(
	ctx context.Context,
	q readQueryerV1,
	kind, resourceID string,
) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT snapshot_digest FROM overview_resource_snapshots
		WHERE resource_kind=? AND resource_id=? ORDER BY observation_sequence`, kind, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, err
		}
		result = append(result, digest)
	}
	return result, rows.Err()
}

func loadOverviewResourceSnapshotV1(
	ctx context.Context,
	q readQueryerV1,
	digest string,
) (overviewResourceRecordV1, error) {
	var out overviewResourceRecordV1
	var previous, workspace, subjectID, subjectDigest, relatedID, relatedDigest sql.NullString
	var causalID, causalDigest, binding, usageSemanticDigest, usageStatus sql.NullString
	var subjectSequence, relatedSequence, causalSequence sql.NullInt64
	var usageRevision, usageLedgerSequence, input, cached, uncached, output, reasoning sql.NullInt64
	var sequence, revision, created, updated int64
	var transition, storeID, tenantID, state, semantic string
	var canonical []byte
	err := q.QueryRowContext(ctx, `SELECT snapshot_digest,resource_kind,resource_id,
		observation_sequence,previous_snapshot_digest,transition_kind,store_instance_id,
		tenant_id,workspace_id,subject_run_id,subject_run_observation_sequence,
		subject_run_observation_digest,related_run_id,related_run_observation_sequence,
		related_run_observation_digest,causal_run_id,causal_run_observation_sequence,
		causal_run_observation_digest,state,binding_target_kind,resource_revision,
		created_at,updated_at,semantic_digest,usage_revision,usage_ledger_sequence,usage_semantic_digest,usage_status,input_tokens,
		cached_input_tokens,uncached_input_tokens,output_tokens,reasoning_tokens,canonical_json
		FROM overview_resource_snapshots WHERE snapshot_digest=?`, digest).Scan(
		&out.Digest, &out.Snapshot.ResourceKind, &out.Snapshot.ResourceID, &sequence,
		&previous, &transition, &storeID, &tenantID, &workspace, &subjectID,
		&subjectSequence, &subjectDigest, &relatedID, &relatedSequence, &relatedDigest,
		&causalID, &causalSequence, &causalDigest, &state, &binding, &revision,
		&created, &updated, &semantic, &usageRevision, &usageLedgerSequence, &usageSemanticDigest, &usageStatus, &input, &cached,
		&uncached, &output, &reasoning, &canonical)
	if err != nil || sequence <= 0 || revision < 0 || created <= 0 || updated < created {
		return out, fmt.Errorf("%w: resource observation snapshot row: %v", ErrLoopIntegrity, err)
	}
	var decoded overviewResourceCanonicalV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return out, fmt.Errorf("%w: resource observation canonical decode: %v", ErrLoopIntegrity, err)
	}
	rebuilt, rebuiltDigest, err := canonicalOverviewResourceV1(decoded)
	if err != nil || !bytes.Equal(rebuilt, canonical) || rebuiltDigest != digest ||
		out.Digest != digest || decoded.ObservationSequence != uint64(sequence) ||
		decoded.PreviousSnapshotDigest != previous.String || decoded.TransitionKind != transition ||
		decoded.StoreInstanceID != storeID || decoded.TenantID != tenantID ||
		decoded.WorkspaceID != workspace.String || decoded.State != state ||
		decoded.BindingTargetKind != binding.String || decoded.ResourceRevision != uint64(revision) ||
		decoded.CreatedAtUnixMicros != uint64(created) || decoded.UpdatedAtUnixMicros != uint64(updated) ||
		decoded.SemanticDigest != semantic || !overviewResourceRefMatchesSQLV1(
		decoded.SubjectRun, subjectID, subjectSequence, subjectDigest) ||
		!overviewResourceRefMatchesSQLV1(decoded.RelatedRun, relatedID, relatedSequence, relatedDigest) ||
		!overviewResourceRefMatchesSQLV1(decoded.CausalRun, causalID, causalSequence, causalDigest) ||
		!overviewNullableUintMatchesV1(decoded.UsageRevision, usageRevision) ||
		!overviewNullableUintMatchesV1(decoded.UsageLedgerSequence, usageLedgerSequence) ||
		decoded.UsageSemanticDigest != usageSemanticDigest.String ||
		decoded.UsageStatus != usageStatus.String ||
		!overviewNullableUintMatchesV1(decoded.InputTokens, input) ||
		!overviewNullableUintMatchesV1(decoded.CachedInputTokens, cached) ||
		!overviewNullableUintMatchesV1(decoded.UncachedInputTokens, uncached) ||
		!overviewNullableUintMatchesV1(decoded.OutputTokens, output) ||
		!overviewNullableUintMatchesV1(decoded.ReasoningTokens, reasoning) {
		return out, fmt.Errorf("%w: resource observation snapshot canonical/scalars: %v",
			ErrLoopIntegrity, err)
	}
	out.Snapshot, out.Canonical = decoded, bytes.Clone(canonical)
	return out, nil
}

func overviewResourceRefMatchesSQLV1(
	ref *overviewRunObservationRefV1,
	id sql.NullString,
	sequence sql.NullInt64,
	digest sql.NullString,
) bool {
	if ref == nil {
		return !id.Valid && !sequence.Valid && !digest.Valid
	}
	return id.Valid && sequence.Valid && digest.Valid && sequence.Int64 > 0 &&
		ref.RunID == id.String && ref.Sequence == uint64(sequence.Int64) && ref.Digest == digest.String
}

// verifyCurrentOverviewResourceObservationV1 is also the exact +0 retry
// fence. It recomputes the full typed source semantic digest while retaining
// the immutable historical Run observation references recorded by the last
// resource mutation; later Run-only transitions are therefore legal.
func verifyCurrentOverviewResourceObservationV1(
	ctx context.Context,
	q readQueryerV1,
	kind, resourceID string,
) error {
	stored, err := loadOverviewResourceHeadOnlineV1(ctx, q, kind, resourceID)
	if err != nil {
		return err
	}
	fresh, err := loadCurrentOverviewResourceV1(ctx, q, kind, resourceID)
	if err != nil {
		return err
	}
	if !overviewResourceRefCanLagV1(stored.Snapshot.SubjectRun, fresh.Snapshot.SubjectRun) ||
		!overviewResourceRefCanLagV1(stored.Snapshot.RelatedRun, fresh.Snapshot.RelatedRun) ||
		!overviewResourceRefCanLagV1(stored.Snapshot.CausalRun, fresh.Snapshot.CausalRun) {
		return fmt.Errorf("%w: resource Run identity drift", ErrLoopIntegrity)
	}
	verifiedRuns := make(map[string]struct{}, 2)
	for _, ref := range []*overviewRunObservationRefV1{
		fresh.Snapshot.SubjectRun, fresh.Snapshot.RelatedRun, fresh.Snapshot.CausalRun,
	} {
		if ref == nil {
			continue
		}
		if _, found := verifiedRuns[ref.RunID]; found {
			continue
		}
		if _, err := loadRunObservationHeadV1(ctx, q, ref.RunID); err != nil {
			return fmt.Errorf("%w: current resource Run head: %v", ErrLoopIntegrity, err)
		}
		verifiedRuns[ref.RunID] = struct{}{}
	}
	expected := fresh.Snapshot
	expected.SchemaVersion = stored.Snapshot.SchemaVersion
	expected.TransitionKind = stored.Snapshot.TransitionKind
	expected.PreviousSnapshotDigest = stored.Snapshot.PreviousSnapshotDigest
	expected.StoreInstanceID = stored.Snapshot.StoreInstanceID
	expected.SubjectRun = stored.Snapshot.SubjectRun
	expected.RelatedRun = stored.Snapshot.RelatedRun
	expected.CausalRun = stored.Snapshot.CausalRun
	canonical, digest, err := canonicalOverviewResourceV1(expected)
	if err != nil || digest != stored.Digest || !bytes.Equal(canonical, stored.Canonical) {
		return fmt.Errorf("%w: current resource semantic digest/projection: %v", ErrLoopIntegrity, err)
	}
	return nil
}

func verifyCurrentRunObservationV1(ctx context.Context, q readQueryerV1, runID string) error {
	_, err := loadRunObservationHeadV1(ctx, q, runID)
	return err
}

func overviewResourceRefCanLagV1(stored, current *overviewRunObservationRefV1) bool {
	if stored == nil || current == nil {
		return stored == nil && current == nil
	}
	return stored.RunID == current.RunID && stored.Sequence <= current.Sequence &&
		(stored.Sequence != current.Sequence || stored.Digest == current.Digest)
}

func verifyOverviewResourceCausalRunRefsV1(
	ctx context.Context,
	q readQueryerV1,
	current overviewResourceRecordV1,
) error {
	s := current.Snapshot
	load := func(ref *overviewRunObservationRefV1) (runObservationRecordV1, error) {
		if ref == nil {
			return runObservationRecordV1{}, fmt.Errorf("%w: missing causal Run ref", ErrLoopIntegrity)
		}
		record, err := loadRunObservationSnapshotV1(ctx, q, ref.Digest)
		if err != nil || record.Snapshot.RunID != ref.RunID ||
			record.Snapshot.ObservationSequence != ref.Sequence {
			return runObservationRecordV1{}, fmt.Errorf("%w: causal Run snapshot: %v", ErrLoopIntegrity, err)
		}
		return record, nil
	}
	causal, err := load(s.CausalRun)
	if s.ResourceKind == overviewResourceModuleReviewV1 ||
		(s.ResourceKind == overviewResourceLearningTaskV1 &&
			s.TransitionKind == overviewTransitionTaskCreateV1) {
		if s.CausalRun != nil {
			return fmt.Errorf("%w: unexpected causal Run ref", ErrLoopIntegrity)
		}
		return nil
	}
	if err != nil {
		return err
	}
	var facts runObservationEventFactsV1
	if s.ResourceKind == overviewResourceModelV1 ||
		s.ResourceKind == overviewResourceActionV1 ||
		s.ResourceKind == overviewResourceChannelV1 {
		facts, err = deriveRunObservationEventFactsV1(ctx, q, causal.Snapshot)
		if err != nil || facts.CreatedAt != s.UpdatedAtUnixMicros {
			return fmt.Errorf("%w: resource causal Run event facts/time: %v", ErrLoopIntegrity, err)
		}
	}
	requireAttempt := func(transition runObservationTransitionV1, attemptID string) error {
		if causal.Snapshot.TransitionKind != transition ||
			causal.Snapshot.SourceEventAttemptID != attemptID ||
			facts.ResourceSemanticDigest != s.SemanticDigest ||
			facts.AttemptID != attemptID {
			return fmt.Errorf("%w: resource/Run transition carrier", ErrLoopIntegrity)
		}
		return nil
	}
	switch s.ResourceKind {
	case overviewResourceModelV1:
		switch s.TransitionKind {
		case overviewTransitionModelBeginV1:
			if facts.ModelState != corecontract.ModelAttemptPending || s.State != string(facts.ModelState) {
				return fmt.Errorf("%w: Model begin state carrier", ErrLoopIntegrity)
			}
			return requireAttempt(runObservationModelBeginV1, s.ResourceID)
		case overviewTransitionModelExpiredV1:
			if facts.ModelState != corecontract.ModelAttemptFailed || s.State != string(facts.ModelState) {
				return fmt.Errorf("%w: expired Model state carrier", ErrLoopIntegrity)
			}
			return requireAttempt(runObservationModelOutcomeV1, s.ResourceID)
		case overviewTransitionModelOutcomeV1:
			if causal.Snapshot.SourceEventAttemptID != s.ResourceID ||
				(causal.Snapshot.TransitionKind != runObservationModelOutcomeV1 &&
					causal.Snapshot.TransitionKind != runObservationModelRejectionV1) ||
				facts.ResourceSemanticDigest != s.SemanticDigest {
				return fmt.Errorf("%w: Model outcome Run carrier", ErrLoopIntegrity)
			}
			if causal.Snapshot.TransitionKind == runObservationModelOutcomeV1 &&
				s.State != string(facts.ModelState) ||
				causal.Snapshot.TransitionKind == runObservationModelRejectionV1 &&
					s.State != string(corecontract.ModelAttemptSucceeded) {
				return fmt.Errorf("%w: Model outcome state carrier", ErrLoopIntegrity)
			}
			return nil
		case overviewTransitionModelActionSourceV1, overviewTransitionModelChannelSourceV1:
			want := runObservationActionBeginV1
			if s.TransitionKind == overviewTransitionModelChannelSourceV1 {
				want = runObservationChannelBeginV1
			}
			if causal.Snapshot.TransitionKind != want || causal.Snapshot.ObservationSequence <= 1 ||
				facts.SourceModelAttemptID != s.ResourceID ||
				facts.SourceModelSemanticDigest != s.SemanticDigest {
				return fmt.Errorf("%w: Model dispatch-source Run transition", ErrLoopIntegrity)
			}
			var predecessorDigest string
			if err := q.QueryRowContext(ctx, `SELECT snapshot_digest FROM run_observation_snapshots
				WHERE run_id=? AND observation_sequence=?`, causal.Snapshot.RunID,
				int64(causal.Snapshot.ObservationSequence-1)).Scan(&predecessorDigest); err != nil {
				return fmt.Errorf("%w: Model dispatch-source predecessor: %v", ErrLoopIntegrity, err)
			}
			predecessor, err := loadRunObservationSnapshotV1(ctx, q, predecessorDigest)
			if err != nil || predecessor.Snapshot.PendingModelAttemptID != s.ResourceID {
				return fmt.Errorf("%w: Model dispatch-source predecessor carrier: %v", ErrLoopIntegrity, err)
			}
			return nil
		case overviewTransitionModelRecoveryV1:
			if facts.ModelState != corecontract.ModelAttemptUnknown || s.State != string(facts.ModelState) {
				return fmt.Errorf("%w: recovered Model state carrier", ErrLoopIntegrity)
			}
			return requireAttempt(runObservationStartupRecoveryV1, s.ResourceID)
		}
	case overviewResourceActionV1:
		if s.State != string(facts.DispatchState) {
			return fmt.Errorf("%w: Action state carrier", ErrLoopIntegrity)
		}
		switch s.TransitionKind {
		case overviewTransitionActionBeginV1:
			return requireAttempt(runObservationActionBeginV1, s.ResourceID)
		case overviewTransitionActionOutcomeV1:
			return requireAttempt(runObservationActionOutcomeV1, s.ResourceID)
		case overviewTransitionActionRecoveryV1:
			return requireAttempt(runObservationStartupRecoveryV1, s.ResourceID)
		}
	case overviewResourceChannelV1:
		if s.State != string(facts.DispatchState) {
			return fmt.Errorf("%w: Channel state carrier", ErrLoopIntegrity)
		}
		switch s.TransitionKind {
		case overviewTransitionChannelBeginV1:
			return requireAttempt(runObservationChannelBeginV1, s.ResourceID)
		case overviewTransitionChannelOutcomeV1:
			return requireAttempt(runObservationChannelOutcomeV1, s.ResourceID)
		case overviewTransitionChannelRecoveryV1:
			return requireAttempt(runObservationStartupRecoveryV1, s.ResourceID)
		}
	case overviewResourceLearningProposalV1:
		if s.TransitionKind == overviewTransitionProposalSubmitV1 {
			if causal.Snapshot.RunState != corecontract.TerminatedLoopStep {
				return fmt.Errorf("%w: Proposal proposer Run is not terminal", ErrLoopIntegrity)
			}
			return nil
		}
		if s.TransitionKind == overviewTransitionProposalAdmissionV1 {
			if causal.Snapshot.TransitionKind != runObservationAdmissionV1 ||
				causal.Snapshot.RunState != corecontract.InitialRunState {
				return fmt.Errorf("%w: Proposal reviewer admission Run", ErrLoopIntegrity)
			}
			return nil
		}
		if s.State == "REVIEW_UNKNOWN" {
			if causal.Snapshot.RunState != corecontract.WaitingReconciliationLoopStep {
				return fmt.Errorf("%w: Proposal unknown reviewer Run", ErrLoopIntegrity)
			}
		} else if causal.Snapshot.RunState != corecontract.TerminatedLoopStep {
			return fmt.Errorf("%w: Proposal terminal reviewer Run", ErrLoopIntegrity)
		}
		return nil
	case overviewResourceLearningTaskV1:
		if s.TransitionKind == overviewTransitionTaskAdmissionV1 {
			if causal.Snapshot.TransitionKind != runObservationAdmissionV1 ||
				causal.Snapshot.RunState != corecontract.InitialRunState {
				return fmt.Errorf("%w: Learning Task admission Run", ErrLoopIntegrity)
			}
			return nil
		}
		if s.State == "UNKNOWN" {
			if causal.Snapshot.RunState != corecontract.WaitingReconciliationLoopStep {
				return fmt.Errorf("%w: Learning Task unknown Run", ErrLoopIntegrity)
			}
		} else if causal.Snapshot.RunState != corecontract.TerminatedLoopStep {
			return fmt.Errorf("%w: Learning Task terminal Run", ErrLoopIntegrity)
		}
		return nil
	}
	return fmt.Errorf("%w: unsupported resource causal Run transition", ErrLoopIntegrity)
}

func validateOverviewResourceAdvanceV1(
	previous *overviewResourceRecordV1,
	current overviewResourceRecordV1,
) error {
	s := current.Snapshot
	if previous == nil {
		if s.ObservationSequence != 1 || s.PreviousSnapshotDigest != "" ||
			!overviewResourceGenesisTransitionV1(s.ResourceKind, s.TransitionKind) {
			return fmt.Errorf("%w: resource observation genesis", ErrLoopIntegrity)
		}
		if err := validateOverviewResourceTransitionShapeV1(s); err != nil {
			return err
		}
		return validateOverviewResourceGenesisShapeV1(s)
	}
	p := previous.Snapshot
	if s.ObservationSequence != p.ObservationSequence+1 ||
		s.PreviousSnapshotDigest != previous.Digest || s.StoreInstanceID != p.StoreInstanceID ||
		s.ResourceKind != p.ResourceKind || s.ResourceID != p.ResourceID ||
		s.TenantID != p.TenantID || s.WorkspaceID != p.WorkspaceID ||
		s.CreatedAtUnixMicros != p.CreatedAtUnixMicros ||
		s.UpdatedAtUnixMicros < p.UpdatedAtUnixMicros || s.SemanticDigest == p.SemanticDigest ||
		s.ProposalKind != p.ProposalKind || s.BindingTargetKind != p.BindingTargetKind {
		return fmt.Errorf("%w: resource observation immutable/delta facts", ErrLoopIntegrity)
	}
	if !overviewResourceRefAdvanceV1(p.SubjectRun, s.SubjectRun, s.ResourceKind == overviewResourceLearningTaskV1) ||
		!overviewResourceRefAdvanceV1(p.RelatedRun, s.RelatedRun, s.ResourceKind == overviewResourceLearningProposalV1) {
		return fmt.Errorf("%w: resource observation Run-ref advance", ErrLoopIntegrity)
	}
	switch s.ResourceKind {
	case overviewResourceModelV1:
		if s.ResourceRevision != p.ResourceRevision+1 || s.UsageRevision == nil ||
			p.UsageRevision == nil || *s.UsageRevision != *p.UsageRevision+1 {
			return fmt.Errorf("%w: Model observation revision advance", ErrLoopIntegrity)
		}
		if p.UsageLedgerSequence != nil &&
			(s.UsageLedgerSequence == nil || *s.UsageLedgerSequence != *p.UsageLedgerSequence) {
			return fmt.Errorf("%w: Model Usage ledger identity advance", ErrLoopIntegrity)
		}
		switch s.TransitionKind {
		case overviewTransitionModelOutcomeV1:
			if (p.State != "PENDING" && p.State != "MODEL_UNKNOWN") ||
				(s.State != "SUCCEEDED" && s.State != "FAILED" && s.State != "MODEL_UNKNOWN") ||
				(p.State == "MODEL_UNKNOWN" && s.State == "MODEL_UNKNOWN") {
				return fmt.Errorf("%w: Model outcome transition", ErrLoopIntegrity)
			}
		case overviewTransitionModelActionSourceV1, overviewTransitionModelChannelSourceV1:
			if p.State != "PENDING" || s.State != "SUCCEEDED" {
				return fmt.Errorf("%w: Model dispatch-source transition", ErrLoopIntegrity)
			}
		case overviewTransitionModelRecoveryV1:
			if p.State != "PENDING" || s.State != "MODEL_UNKNOWN" ||
				s.UsageLedgerSequence != nil || s.InputTokens != nil ||
				s.CachedInputTokens != nil || s.UncachedInputTokens != nil ||
				s.OutputTokens != nil || s.ReasoningTokens != nil {
				return fmt.Errorf("%w: Model recovery transition", ErrLoopIntegrity)
			}
		default:
			return fmt.Errorf("%w: non-genesis Model transition", ErrLoopIntegrity)
		}
	case overviewResourceActionV1, overviewResourceChannelV1:
		if p.CausalRun == nil || s.CausalRun == nil ||
			s.CausalRun.RunID != p.CausalRun.RunID ||
			s.CausalRun.Sequence <= p.CausalRun.Sequence {
			return fmt.Errorf("%w: dispatch causal Run ref did not advance", ErrLoopIntegrity)
		}
		if s.ResourceRevision != p.ResourceRevision+1 {
			return fmt.Errorf("%w: dispatch observation revision advance", ErrLoopIntegrity)
		}
		recovery := overviewTransitionActionRecoveryV1
		outcome := overviewTransitionActionOutcomeV1
		if s.ResourceKind == overviewResourceChannelV1 {
			recovery, outcome = overviewTransitionChannelRecoveryV1, overviewTransitionChannelOutcomeV1
		}
		if s.TransitionKind == recovery {
			if p.State != "PENDING" || s.State != "UNKNOWN" {
				return fmt.Errorf("%w: dispatch recovery transition", ErrLoopIntegrity)
			}
		} else if s.TransitionKind != outcome ||
			(p.State != "PENDING" && p.State != "UNKNOWN") ||
			(s.State != "SUCCEEDED" && s.State != "FAILED" && s.State != "UNKNOWN") {
			return fmt.Errorf("%w: dispatch outcome transition", ErrLoopIntegrity)
		}
	case overviewResourceLearningProposalV1:
		switch s.TransitionKind {
		case overviewTransitionProposalAdmissionV1:
			if p.State != "SUBMITTED" || p.ResourceRevision != 0 ||
				s.State != "REVIEW_PENDING" || s.ResourceRevision != 1 || p.RelatedRun != nil ||
				s.RelatedRun == nil {
				return fmt.Errorf("%w: Proposal admission transition", ErrLoopIntegrity)
			}
		case overviewTransitionProposalFinalizeV1:
			if (p.State != "REVIEW_PENDING" && p.State != "REVIEW_UNKNOWN") ||
				s.ResourceRevision != p.ResourceRevision+1 ||
				(s.State != "APPROVED" && s.State != "REJECTED" &&
					s.State != "REVIEW_FAILED" && s.State != "REVIEW_UNKNOWN") {
				return fmt.Errorf("%w: Proposal finalization transition", ErrLoopIntegrity)
			}
		case overviewTransitionProposalMaterializeV1:
			if p.State != "APPROVED" || s.State != p.State ||
				s.ResourceRevision != p.ResourceRevision || s.UpdatedAtUnixMicros != p.UpdatedAtUnixMicros ||
				p.MaterializedVersionID != "" || s.MaterializedVersionID == "" {
				return fmt.Errorf("%w: Proposal materialization transition", ErrLoopIntegrity)
			}
		default:
			return fmt.Errorf("%w: non-genesis Proposal transition", ErrLoopIntegrity)
		}
	case overviewResourceLearningTaskV1:
		switch s.TransitionKind {
		case overviewTransitionTaskAdmissionV1:
			if p.State != "PENDING" || p.ResourceRevision != 0 || p.SubjectRun != nil ||
				s.State != "RUN_ADMITTED" || s.ResourceRevision != 1 || s.SubjectRun == nil {
				return fmt.Errorf("%w: Task admission transition", ErrLoopIntegrity)
			}
		case overviewTransitionTaskFinalizeV1:
			if (p.State != "RUN_ADMITTED" && p.State != "UNKNOWN") ||
				s.ResourceRevision != p.ResourceRevision+1 ||
				s.State == "PENDING" || s.State == "RUN_ADMITTED" {
				return fmt.Errorf("%w: Task finalization transition", ErrLoopIntegrity)
			}
		default:
			return fmt.Errorf("%w: non-genesis Task transition", ErrLoopIntegrity)
		}
	case overviewResourceModuleReviewV1:
		return fmt.Errorf("%w: Module Review cannot advance", ErrLoopIntegrity)
	default:
		return fmt.Errorf("%w: unsupported resource observation advance", ErrLoopIntegrity)
	}
	return validateOverviewResourceTransitionShapeV1(s)
}

func validateOverviewResourceGenesisShapeV1(s overviewResourceCanonicalV1) error {
	valid := false
	switch s.TransitionKind {
	case overviewTransitionModelBeginV1:
		valid = s.ResourceKind == overviewResourceModelV1 && s.State == "PENDING" &&
			s.ResourceRevision == 0 && s.UsageRevision != nil && *s.UsageRevision == 0 &&
			s.UsageLedgerSequence == nil &&
			s.UsageStatus == modelUsageStatusPending && s.InputTokens == nil &&
			s.CachedInputTokens == nil && s.UncachedInputTokens == nil &&
			s.OutputTokens == nil && s.ReasoningTokens == nil
	case overviewTransitionModelExpiredV1:
		valid = s.ResourceKind == overviewResourceModelV1 && s.State == "FAILED" &&
			s.ResourceRevision == 0 && s.UsageRevision != nil && *s.UsageRevision == 0 &&
			s.UsageLedgerSequence == nil &&
			s.UsageStatus == modelUsageStatusNoReport && s.InputTokens == nil &&
			s.CachedInputTokens == nil && s.UncachedInputTokens == nil &&
			s.OutputTokens == nil && s.ReasoningTokens == nil
	case overviewTransitionActionBeginV1:
		valid = s.ResourceKind == overviewResourceActionV1 && s.State == "PENDING" &&
			s.ResourceRevision == 0
	case overviewTransitionChannelBeginV1:
		valid = s.ResourceKind == overviewResourceChannelV1 && s.State == "PENDING" &&
			s.ResourceRevision == 0
	case overviewTransitionProposalSubmitV1:
		valid = s.ResourceKind == overviewResourceLearningProposalV1 &&
			s.State == "SUBMITTED" && s.ResourceRevision == 0
	case overviewTransitionTaskCreateV1:
		valid = s.ResourceKind == overviewResourceLearningTaskV1 &&
			s.State == "PENDING" && s.ResourceRevision == 0
	case overviewTransitionModuleReviewV1:
		valid = s.ResourceKind == overviewResourceModuleReviewV1 &&
			s.ResourceRevision == 0
	}
	if !valid {
		return fmt.Errorf("%w: resource genesis transition shape", ErrLoopIntegrity)
	}
	return nil
}

func overviewResourceGenesisTransitionV1(kind, transition string) bool {
	switch kind {
	case overviewResourceModelV1:
		return transition == overviewTransitionModelBeginV1 || transition == overviewTransitionModelExpiredV1
	case overviewResourceActionV1:
		return transition == overviewTransitionActionBeginV1
	case overviewResourceChannelV1:
		return transition == overviewTransitionChannelBeginV1
	case overviewResourceLearningProposalV1:
		return transition == overviewTransitionProposalSubmitV1
	case overviewResourceLearningTaskV1:
		return transition == overviewTransitionTaskCreateV1
	case overviewResourceModuleReviewV1:
		return transition == overviewTransitionModuleReviewV1
	}
	return false
}

func overviewResourceRefAdvanceV1(
	previous, current *overviewRunObservationRefV1,
	allowInitialSet bool,
) bool {
	if previous == nil {
		return current == nil || allowInitialSet
	}
	return current != nil && previous.RunID == current.RunID && previous.Sequence <= current.Sequence
}

func validateOverviewResourceTransitionShapeV1(s overviewResourceCanonicalV1) error {
	if err := validateOverviewResourceCanonicalV1(s); err != nil {
		return err
	}
	switch s.ResourceKind {
	case overviewResourceModelV1:
		if (s.State == "PENDING" && (s.ResourceRevision != 0 || s.TransitionKind != overviewTransitionModelBeginV1)) ||
			(s.State == "MODEL_UNKNOWN" && s.ResourceRevision != 1) ||
			(s.State != "PENDING" && s.State != "MODEL_UNKNOWN" &&
				s.State != "SUCCEEDED" && s.State != "FAILED") {
			return fmt.Errorf("%w: Model observation state/revision", ErrLoopIntegrity)
		}
		if s.UsageRevision == nil || *s.UsageRevision != s.ResourceRevision {
			return fmt.Errorf("%w: Model/Usage observation revision", ErrLoopIntegrity)
		}
		allTokensNil := s.InputTokens == nil && s.CachedInputTokens == nil &&
			s.UncachedInputTokens == nil && s.OutputTokens == nil && s.ReasoningTokens == nil
		switch s.State {
		case "PENDING":
			if s.UsageStatus != modelUsageStatusPending || !allTokensNil {
				return fmt.Errorf("%w: pending Model Usage observation", ErrLoopIntegrity)
			}
		case "MODEL_UNKNOWN":
			if s.UsageStatus != modelUsageStatusReconciliation {
				return fmt.Errorf("%w: unknown Model Usage observation", ErrLoopIntegrity)
			}
		case "SUCCEEDED", "FAILED":
			if s.UsageStatus != modelUsageStatusReported &&
				s.UsageStatus != modelUsageStatusNoReport {
				return fmt.Errorf("%w: terminal Model Usage observation", ErrLoopIntegrity)
			}
			if s.UsageStatus == modelUsageStatusNoReport && !allTokensNil {
				return fmt.Errorf("%w: no-report Model Usage observation", ErrLoopIntegrity)
			}
		}
	case overviewResourceActionV1:
		if (s.State == "PENDING" && s.ResourceRevision != 0) ||
			(s.State == "UNKNOWN" && (s.ResourceRevision < 1 || s.ResourceRevision > 2)) ||
			(s.State != "PENDING" && s.State != "UNKNOWN" && s.State != "SUCCEEDED" && s.State != "FAILED") {
			return fmt.Errorf("%w: Action observation state/revision", ErrLoopIntegrity)
		}
	case overviewResourceChannelV1:
		if (s.State == "PENDING" && s.ResourceRevision != 0) ||
			(s.State == "UNKNOWN" && s.ResourceRevision < 1) ||
			(s.State != "PENDING" && s.State != "UNKNOWN" && s.State != "SUCCEEDED" && s.State != "FAILED") {
			return fmt.Errorf("%w: Channel observation state/revision", ErrLoopIntegrity)
		}
	case overviewResourceLearningProposalV1:
		valid := s.State == "SUBMITTED" && s.ResourceRevision == 0 ||
			s.State == "REVIEW_PENDING" && s.ResourceRevision == 1 ||
			s.State == "REVIEW_UNKNOWN" && s.ResourceRevision == 2 ||
			(s.State == "APPROVED" || s.State == "REJECTED" || s.State == "REVIEW_FAILED") &&
				(s.ResourceRevision == 2 || s.ResourceRevision == 3)
		if !valid {
			return fmt.Errorf("%w: Proposal observation state/revision", ErrLoopIntegrity)
		}
	case overviewResourceLearningTaskV1:
		valid := s.State == "PENDING" && s.ResourceRevision == 0 ||
			s.State == "RUN_ADMITTED" && s.ResourceRevision == 1 ||
			s.State == "UNKNOWN" && s.ResourceRevision == 2 ||
			(s.State != "PENDING" && s.State != "RUN_ADMITTED" && s.State != "UNKNOWN") &&
				(s.ResourceRevision == 2 || s.ResourceRevision == 3)
		if !valid {
			return fmt.Errorf("%w: Task observation state/revision", ErrLoopIntegrity)
		}
	case overviewResourceModuleReviewV1:
		if s.ObservationSequence != 1 || s.ResourceRevision != 0 {
			return fmt.Errorf("%w: Module Review observation revision", ErrLoopIntegrity)
		}
	}
	return nil
}

func verifyOverviewBasisSemanticClosureV1(ctx context.Context, q readQueryerV1) error {
	currentTenants, err := overviewStringColumnV1(ctx, q,
		`SELECT tenant_id FROM control_current ORDER BY tenant_id COLLATE BINARY`)
	if err != nil {
		return err
	}
	headTenants, err := overviewStringColumnV1(ctx, q,
		`SELECT tenant_id FROM overview_basis_heads ORDER BY tenant_id COLLATE BINARY`)
	if err != nil {
		return err
	}
	if !equalOverviewStringsV1(currentTenants, headTenants) {
		return fmt.Errorf("%w: current publications and Overview basis heads differ", ErrPublicationConflict)
	}
	var expectedSnapshots int64
	for _, tenantID := range currentTenants {
		if err := verifyCurrentOverviewBasisProjectionV1(ctx, q, tenantID); err != nil {
			return err
		}
		var headRevision int64
		var headDigest string
		if err := q.QueryRowContext(ctx, `SELECT pointer_revision,projection_digest
			FROM overview_basis_heads WHERE tenant_id=?`, tenantID).Scan(
			&headRevision, &headDigest); err != nil || headRevision <= 0 {
			return fmt.Errorf("%w: Overview basis head: %v", ErrPublicationConflict, err)
		}
		rows, err := q.QueryContext(ctx, `SELECT projection_digest FROM overview_basis_snapshots
			WHERE tenant_id=? ORDER BY pointer_revision`, tenantID)
		if err != nil {
			return err
		}
		digests := make([]string, 0, int(headRevision))
		for rows.Next() {
			var digest string
			if err := rows.Scan(&digest); err != nil {
				_ = rows.Close()
				return err
			}
			digests = append(digests, digest)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		if len(digests) != int(headRevision) || len(digests) == 0 || digests[len(digests)-1] != headDigest {
			return fmt.Errorf("%w: Overview basis revision chain", ErrPublicationConflict)
		}
		for index, digest := range digests {
			projection, err := loadOverviewBasisSnapshotSemanticV1(ctx, q, digest)
			if err != nil || projection.Basis.TenantID != tenantID ||
				projection.Basis.PointerRevision != uint64(index+1) {
				return fmt.Errorf("%w: Overview basis chain member: %v", ErrPublicationConflict, err)
			}
			if err := verifyOverviewBasisSourceV1(ctx, q, projection); err != nil {
				return err
			}
		}
		expectedSnapshots += headRevision
	}
	var actualSnapshots int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM overview_basis_snapshots`).Scan(
		&actualSnapshots); err != nil || actualSnapshots != expectedSnapshots {
		return fmt.Errorf("%w: orphan Overview basis snapshot: %v", ErrPublicationConflict, err)
	}
	return nil
}

func loadOverviewBasisSnapshotSemanticV1(
	ctx context.Context,
	q readQueryerV1,
	digest string,
) (overviewBasisProjectionV1, error) {
	var out overviewBasisProjectionV1
	var storedDigest, storeID, tenantID, snapshotID, controlDigest string
	var catalogID, catalogDigest string
	var pointer, controlRevision, controlPublishedAt int64
	var catalogGeneration, catalogPublishedAt, sourceUpdatedAt, workspaceCount int64
	var canonical []byte
	err := q.QueryRowContext(ctx, `SELECT projection_digest,store_instance_id,tenant_id,
		pointer_revision,snapshot_id,control_revision,control_digest,control_published_at,
		catalog_generation_id,catalog_generation,catalog_digest,catalog_published_at,
		source_updated_at,workspace_count,canonical_json FROM overview_basis_snapshots
		WHERE projection_digest=?`, digest).Scan(&storedDigest, &storeID, &tenantID,
		&pointer, &snapshotID, &controlRevision, &controlDigest, &controlPublishedAt,
		&catalogID, &catalogGeneration, &catalogDigest, &catalogPublishedAt,
		&sourceUpdatedAt, &workspaceCount, &canonical)
	if err != nil || pointer <= 0 || controlRevision <= 0 || controlPublishedAt <= 0 ||
		catalogGeneration <= 0 || catalogPublishedAt <= 0 || sourceUpdatedAt <= 0 ||
		workspaceCount < 0 || workspaceCount > overviewBasisMaximumWorkspacesV1 {
		return out, fmt.Errorf("%w: Overview basis snapshot row: %v", ErrPublicationConflict, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, fmt.Errorf("%w: Overview basis canonical decode: %v", ErrPublicationConflict, err)
	}
	rebuilt, rebuiltDigest, err := canonicalOverviewBasisProjectionV1(out)
	if err != nil || storedDigest != digest || rebuiltDigest != digest || !bytes.Equal(rebuilt, canonical) ||
		out.StoreInstanceID != storeID || out.Basis.TenantID != tenantID ||
		out.Basis.PointerRevision != uint64(pointer) || out.Basis.Control.SnapshotID != snapshotID ||
		out.Basis.Control.Revision != uint64(controlRevision) || out.Basis.Control.Digest != controlDigest ||
		out.ControlPublishedAtMicros != uint64(controlPublishedAt) ||
		out.Basis.Catalog.GenerationID != catalogID || out.Basis.Catalog.Generation != uint64(catalogGeneration) ||
		out.Basis.Catalog.Digest != catalogDigest || out.CatalogPublishedAtMicros != uint64(catalogPublishedAt) ||
		out.SourceUpdatedAtUnixMicros != uint64(sourceUpdatedAt) || len(out.Workspaces) != int(workspaceCount) {
		return out, fmt.Errorf("%w: Overview basis canonical/scalars: %v", ErrPublicationConflict, err)
	}
	var metaStoreID string
	if err := q.QueryRowContext(ctx, `SELECT store_instance_id FROM store_meta WHERE singleton=1`).Scan(
		&metaStoreID); err != nil || metaStoreID != out.StoreInstanceID {
		return out, fmt.Errorf("%w: Overview basis Store identity: %v", ErrPublicationConflict, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT ordinal,workspace_id,workspace_version,
		workspace_digest FROM overview_basis_workspaces WHERE projection_digest=? ORDER BY ordinal`, digest)
	if err != nil {
		return out, err
	}
	ordinal := 0
	for rows.Next() {
		var gotOrdinal int
		var got corecontract.WorkspaceRef
		if err := rows.Scan(&gotOrdinal, &got.ID, &got.Version, &got.Digest); err != nil ||
			ordinal >= len(out.Workspaces) || gotOrdinal != ordinal || got != out.Workspaces[ordinal] {
			_ = rows.Close()
			return out, fmt.Errorf("%w: Overview basis Workspace child", ErrPublicationConflict)
		}
		ordinal++
	}
	if err := rows.Err(); err != nil || ordinal != len(out.Workspaces) {
		_ = rows.Close()
		return out, fmt.Errorf("%w: Overview basis Workspace count: %v", ErrPublicationConflict, err)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	return out, nil
}

func verifyOverviewBasisSourceV1(
	ctx context.Context,
	q readQueryerV1,
	projection overviewBasisProjectionV1,
) error {
	control, catalog, err := loadAdmissionControlCatalog(ctx, q, projection.Basis.TenantID,
		projection.Basis.Control.SnapshotID, projection.Basis.Catalog.GenerationID)
	if err != nil {
		return fmt.Errorf("%w: load Overview basis source: %v", ErrPublicationConflict, err)
	}
	if err := VerifyPublishedControlCatalogClosureV1(ctx, q, control, catalog); err != nil {
		return fmt.Errorf("%w: Overview basis publication closure: %v", ErrPublicationConflict, err)
	}
	if control.SnapshotID != projection.Basis.Control.SnapshotID ||
		control.Revision != projection.Basis.Control.Revision || control.Digest != projection.Basis.Control.Digest ||
		catalog.GenerationID != projection.Basis.Catalog.GenerationID ||
		catalog.Generation != projection.Basis.Catalog.Generation || catalog.Digest != projection.Basis.Catalog.Digest {
		return fmt.Errorf("%w: Overview basis source references", ErrPublicationConflict)
	}
	workspaces := make([]corecontract.WorkspaceRef, len(control.Workspaces))
	for index, definition := range control.Workspaces {
		workspaces[index] = definition.Workspace
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].ID < workspaces[j].ID })
	if len(workspaces) != len(projection.Workspaces) {
		return fmt.Errorf("%w: Overview basis source Workspace count", ErrPublicationConflict)
	}
	for index := range workspaces {
		if workspaces[index] != projection.Workspaces[index] {
			return fmt.Errorf("%w: Overview basis source Workspace", ErrPublicationConflict)
		}
	}
	return nil
}

func overviewStringColumnV1(
	ctx context.Context,
	q readQueryerV1,
	query string,
) ([]string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func equalOverviewStringsV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ = controlcontract.PublishedBasis{}
