package currentstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

type CompositeDecisionFrontierStageV1 string

const (
	CompositeFrontierInitialSpecialistsV1    CompositeDecisionFrontierStageV1 = "INITIAL_SPECIALISTS"
	CompositeFrontierReviewerRoundZeroV1     CompositeDecisionFrontierStageV1 = "REVIEWER_ROUND_0"
	CompositeFrontierRootTransitionV1        CompositeDecisionFrontierStageV1 = "ROOT_TRANSITION"
	CompositeFrontierRepairSpecialistsV1     CompositeDecisionFrontierStageV1 = "REPAIR_SPECIALISTS"
	CompositeFrontierReviewerRoundOneV1      CompositeDecisionFrontierStageV1 = "REVIEWER_ROUND_1"
	CompositeFrontierWaitingReconciliationV1 CompositeDecisionFrontierStageV1 = "WAITING_RECONCILIATION"
	CompositeFrontierTerminalV1              CompositeDecisionFrontierStageV1 = "TERMINAL"
)

// CompositeDecisionFrontierV1 is a read-only, Host-derived scheduling view.
// RunnableRunIDs follow root-plan order and never include a dormant or skipped
// repair Run. It is evidence for orchestration only and grants no permit.
type CompositeDecisionFrontierV1 struct {
	RootRunID          string
	RootManifestDigest string
	Stage              CompositeDecisionFrontierStageV1
	RepairRound        uint32
	RunnableRunIDs     []string
	SourceVerdictRef   string
}

// GetCompositeDecisionFrontier lets an orchestrator inspect the next bounded
// family stage without owning a lease. Every value is re-derived in one read
// transaction from the root plan, terminal results and authoritative events.
func (store *Store) GetCompositeDecisionFrontier(
	ctx context.Context,
	rootRunID string,
) (CompositeDecisionFrontierV1, error) {
	if ctx == nil || !validLeaseOpaqueID(rootRunID) {
		return CompositeDecisionFrontierV1{}, fmt.Errorf(
			"%w: invalid Composite frontier read",
			ErrInvalidLoopRead,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return CompositeDecisionFrontierV1{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CompositeDecisionFrontierV1{}, fmt.Errorf(
			"currentstore: acquire Composite frontier connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return CompositeDecisionFrontierV1{}, fmt.Errorf(
			"currentstore: begin Composite frontier read: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	root, err := loadCompositeRootManifest(ctx, connection, rootRunID)
	if err != nil || root.Composite.Plan.Decision == nil {
		return CompositeDecisionFrontierV1{}, fmt.Errorf(
			"%w: Run is not a W5 decision root",
			ErrInvalidLoopRead,
		)
	}
	frontier, err := loadCompositeDecisionFrontier(
		ctx,
		connection,
		root,
	)
	if err != nil {
		return CompositeDecisionFrontierV1{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CompositeDecisionFrontierV1{}, fmt.Errorf(
			"currentstore: commit Composite frontier read: %w",
			err,
		)
	}
	committed = true
	frontier.RunnableRunIDs = append([]string(nil), frontier.RunnableRunIDs...)
	return frontier, nil
}

func loadCompositeDecisionFrontier(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
) (CompositeDecisionFrontierV1, error) {
	frontier := CompositeDecisionFrontierV1{
		RootRunID:          root.RunID,
		RootManifestDigest: root.ManifestDigest,
		RunnableRunIDs:     []string{},
	}
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return CompositeDecisionFrontierV1{}, loopReadIntegrity(
			"Composite decision frontier root",
			ErrAdmissionIntegrity,
		)
	}
	var state string
	var disposition sql.NullString
	if err := connection.QueryRowContext(ctx, `
		SELECT state, disposition FROM runs WHERE run_id=?
	`, root.RunID).Scan(&state, &disposition); err != nil {
		return CompositeDecisionFrontierV1{}, loopReadIntegrity(
			"Composite decision frontier root head",
			err,
		)
	}
	if state == corecontract.TerminatedLoopStep {
		if !disposition.Valid ||
			disposition.String != corecontract.TerminatedLoopStep {
			return CompositeDecisionFrontierV1{}, loopReadIntegrity(
				"Composite terminal frontier root",
				ErrAdmissionIntegrity,
			)
		}
		frontier.Stage = CompositeFrontierTerminalV1
		return frontier, nil
	}

	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil {
		return CompositeDecisionFrontierV1{}, err
	}
	// A required Child failure already determines the Root outcome. Preserve
	// the Universal Loop's long-standing FAILED-before-UNKNOWN precedence so a
	// sibling UNKNOWN cannot unnecessarily strand a terminal family in
	// reconciliation.
	if compositeChildrenContainState(initial, CompositeChildFailedV1) {
		frontier.Stage = CompositeFrontierRootTransitionV1
		frontier.RunnableRunIDs = []string{root.RunID}
		return frontier, nil
	}
	if compositeChildrenContainState(initial, CompositeChildUnknownV1) {
		frontier.Stage = CompositeFrontierWaitingReconciliationV1
		return frontier, nil
	}
	if !allCompositeChildrenSucceeded(initial) {
		for _, child := range initial {
			if child.State == CompositeChildPendingV1 &&
				child.FrameStep == corecontract.InitialLoopStep {
				frontier.RunnableRunIDs = append(
					frontier.RunnableRunIDs,
					child.RunID,
				)
			}
		}
		if len(frontier.RunnableRunIDs) != 0 {
			frontier.Stage = CompositeFrontierInitialSpecialistsV1
		} else {
			frontier.Stage = CompositeFrontierRootTransitionV1
			frontier.RunnableRunIDs = []string{root.RunID}
		}
		return frontier, nil
	}

	reviewerZero, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewerZero == nil {
		return CompositeDecisionFrontierV1{}, err
	}
	switch reviewerZero.State {
	case CompositeReviewerUnknownV1:
		frontier.Stage = CompositeFrontierWaitingReconciliationV1
		return frontier, nil
	case CompositeReviewerPendingV1:
		frontier.Stage = CompositeFrontierReviewerRoundZeroV1
		if reviewerZero.FrameStep == corecontract.WaitingChildrenLoopStep ||
			reviewerZero.FrameStep == corecontract.InitialLoopStep {
			frontier.RunnableRunIDs = []string{reviewerZero.RunID}
		}
		return frontier, nil
	case CompositeReviewerFailedV1:
		frontier.Stage = CompositeFrontierRootTransitionV1
		frontier.RunnableRunIDs = []string{root.RunID}
		return frontier, nil
	case CompositeReviewerSucceededV1:
		if reviewerZero.CollaborationVerdict == nil ||
			reviewerZero.ResultRef == "" {
			return CompositeDecisionFrontierV1{}, loopReadIntegrity(
				"Composite round-zero frontier verdict",
				ErrAdmissionIntegrity,
			)
		}
	default:
		return CompositeDecisionFrontierV1{}, loopReadIntegrity(
			"Composite round-zero frontier state",
			ErrAdmissionIntegrity,
		)
	}
	frontier.SourceVerdictRef = reviewerZero.ResultRef
	transitions, _, err := compositeRepairTransitions(
		root,
		*reviewerZero.CollaborationVerdict,
		reviewerZero.ResultRef,
	)
	if err != nil {
		return CompositeDecisionFrontierV1{}, err
	}
	applied, err := inspectCompositeRepairTransitionEvents(
		ctx,
		connection,
		root,
		reviewerZero.ResultRef,
		transitions,
	)
	if err != nil {
		return CompositeDecisionFrontierV1{}, err
	}
	if !applied || reviewerZero.CollaborationVerdict.Decision !=
		corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		frontier.Stage = CompositeFrontierRootTransitionV1
		frontier.RunnableRunIDs = []string{root.RunID}
		return frontier, nil
	}

	effective, round, err := loadCompositeEffectiveChildResults(
		ctx,
		connection,
		root,
	)
	if err != nil || round != corecontract.CompositeRepairRoundOneV1 {
		return CompositeDecisionFrontierV1{}, err
	}
	frontier.RepairRound = round
	if compositeChildrenContainState(effective, CompositeChildFailedV1) {
		frontier.Stage = CompositeFrontierRootTransitionV1
		frontier.RunnableRunIDs = []string{root.RunID}
		return frontier, nil
	}
	if compositeChildrenContainState(effective, CompositeChildUnknownV1) {
		frontier.Stage = CompositeFrontierWaitingReconciliationV1
		return frontier, nil
	}
	if !allCompositeChildrenSucceeded(effective) {
		for _, child := range effective {
			if child.RepairRound == corecontract.CompositeRepairRoundOneV1 &&
				child.State == CompositeChildPendingV1 &&
				child.FrameStep == corecontract.InitialLoopStep {
				frontier.RunnableRunIDs = append(
					frontier.RunnableRunIDs,
					child.RunID,
				)
			}
		}
		if len(frontier.RunnableRunIDs) != 0 {
			frontier.Stage = CompositeFrontierRepairSpecialistsV1
		} else {
			frontier.Stage = CompositeFrontierRootTransitionV1
			frontier.RunnableRunIDs = []string{root.RunID}
		}
		return frontier, nil
	}
	reviewerOne, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		effective,
		corecontract.CompositeRepairRoundOneV1,
	)
	if err != nil || reviewerOne == nil {
		return CompositeDecisionFrontierV1{}, err
	}
	switch reviewerOne.State {
	case CompositeReviewerUnknownV1:
		frontier.Stage = CompositeFrontierWaitingReconciliationV1
	case CompositeReviewerPendingV1:
		if reviewerOne.FrameStep ==
			corecontract.WaitingRepairActivationLoopStep {
			frontier.Stage = CompositeFrontierRootTransitionV1
			frontier.RunnableRunIDs = []string{root.RunID}
			break
		}
		frontier.Stage = CompositeFrontierReviewerRoundOneV1
		if reviewerOne.FrameStep == corecontract.WaitingChildrenLoopStep ||
			reviewerOne.FrameStep == corecontract.InitialLoopStep {
			frontier.RunnableRunIDs = []string{reviewerOne.RunID}
		}
	case CompositeReviewerSucceededV1, CompositeReviewerFailedV1:
		frontier.Stage = CompositeFrontierRootTransitionV1
		frontier.RunnableRunIDs = []string{root.RunID}
	default:
		return CompositeDecisionFrontierV1{}, loopReadIntegrity(
			"Composite round-one frontier state",
			ErrAdmissionIntegrity,
		)
	}
	return frontier, nil
}

func compositeChildrenContainState(
	children []CompositeChildResultRecordV1,
	state CompositeChildStateV1,
) bool {
	for _, child := range children {
		if child.State == state {
			return true
		}
	}
	return false
}

func cloneCompositeDecisionFrontier(
	input *CompositeDecisionFrontierV1,
) *CompositeDecisionFrontierV1 {
	if input == nil {
		return nil
	}
	cloned := *input
	cloned.RunnableRunIDs = append([]string(nil), input.RunnableRunIDs...)
	return &cloned
}
