package localchat

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
)

const compositeDecisionMaximumOrchestrationPasses = 20

// chatDecisionFamily follows only the Host-derived Current Store frontier.
// It never parses a model verdict, computes affected slots, or invokes a
// dormant repair Run. A refreshed frontier is the sole scheduling source
// after every bounded group.
func (service *CompositeChatService) chatDecisionFamily(
	ctx context.Context,
	family currentstore.CompositeRunFamilyAdmissionResult,
	result CompositeChatResult,
) (CompositeChatResult, error) {
	if family.Reviewer == nil || family.RepairReviewer == nil ||
		len(family.Children) < corecontract.CompositeMinChildrenV1 ||
		len(family.Children) > corecontract.CompositeMaxChildrenV1 ||
		len(family.RepairChildren) != len(family.Children) {
		return result, fmt.Errorf(
			"%w: W5 family admission closure is incomplete",
			ErrChatIntegrity,
		)
	}
	for index, child := range family.Children {
		result.Children[index].RunID = child.RunID
	}
	result.Reviewer = &CompositeReviewerChatResult{
		RunID: family.Reviewer.RunID,
	}
	result.RepairChildren = make(
		[]CompositeChildChatResult,
		len(family.RepairChildren),
	)
	for index, child := range family.RepairChildren {
		result.RepairChildren[index].RunID = child.RunID
	}
	result.RepairReviewer = &CompositeReviewerChatResult{
		RunID: family.RepairReviewer.RunID,
	}

	for pass := 0; pass < compositeDecisionMaximumOrchestrationPasses; pass++ {
		frontier, err := service.store.GetCompositeDecisionFrontier(
			ctx,
			family.Parent.RunID,
		)
		if err != nil {
			return result, err
		}
		if frontier.RootRunID != family.Parent.RunID ||
			frontier.RootManifestDigest != family.Parent.ManifestDigest {
			return result, fmt.Errorf(
				"%w: Composite frontier differs from admitted root",
				ErrChatIntegrity,
			)
		}

		switch frontier.Stage {
		case currentstore.CompositeFrontierInitialSpecialistsV1,
			currentstore.CompositeFrontierRepairSpecialistsV1:
			if len(frontier.RunnableRunIDs) == 0 {
				return service.projectDecisionRoot(ctx, result)
			}
			advanced, contended, err := service.runDecisionParticipants(
				ctx,
				frontier.RunnableRunIDs,
			)
			if err != nil {
				return result, err
			}
			if err := applyDecisionParticipantResults(
				&result,
				advanced,
			); err != nil {
				return result, err
			}
			if contended && len(advanced) == 0 {
				return service.projectDecisionRoot(ctx, result)
			}

		case currentstore.CompositeFrontierReviewerRoundZeroV1,
			currentstore.CompositeFrontierReviewerRoundOneV1:
			if len(frontier.RunnableRunIDs) == 0 {
				return service.projectDecisionRoot(ctx, result)
			}
			if len(frontier.RunnableRunIDs) != 1 {
				return result, fmt.Errorf(
					"%w: Reviewer frontier contains %d runnable Runs",
					ErrChatIntegrity,
					len(frontier.RunnableRunIDs),
				)
			}
			advanced, contended, err := service.runDecisionParticipants(
				ctx,
				frontier.RunnableRunIDs,
			)
			if err != nil {
				return result, err
			}
			if err := applyDecisionParticipantResults(
				&result,
				advanced,
			); err != nil {
				return result, err
			}
			if contended && len(advanced) == 0 {
				return service.projectDecisionRoot(ctx, result)
			}

		case currentstore.CompositeFrontierRootTransitionV1:
			if len(frontier.RunnableRunIDs) != 1 ||
				frontier.RunnableRunIDs[0] != family.Parent.RunID {
				return result, fmt.Errorf(
					"%w: root transition frontier is not exact",
					ErrChatIntegrity,
				)
			}
			advanced, err := service.loop.Run(ctx, loopapi.RunInput{
				RunID:       family.Parent.RunID,
				MaxSteps:    compositeParentLoopMaxSteps,
				MaxDuration: chatLoopMaxDuration,
			})
			if isDecisionLeaseContention(err) {
				return result, nil
			}
			if err != nil {
				return result, err
			}
			if err := validateDecisionLoopResult(
				advanced,
				family.Parent.RunID,
			); err != nil {
				return result, err
			}
			result.LoopResult = advanced
			switch advanced.Disposition {
			case loopapi.DispositionTerminated:
				return service.finishDecisionTerminal(ctx, result)
			case loopapi.DispositionWaitingReconciliation:
				return result, nil
			case loopapi.DispositionWaitingExternal:
				if advanced.ReasonCode != "COLLABORATION_DECISION_APPLIED" {
					return result, nil
				}
			}

		case currentstore.CompositeFrontierWaitingReconciliationV1:
			return service.projectDecisionRoot(ctx, result)

		case currentstore.CompositeFrontierTerminalV1:
			return service.projectDecisionRoot(ctx, result)

		default:
			return result, fmt.Errorf(
				"%w: unsupported Composite frontier stage %q",
				ErrChatIntegrity,
				frontier.Stage,
			)
		}
	}
	return result, fmt.Errorf(
		"%w: Composite decision orchestration did not converge in %d passes",
		ErrChatIntegrity,
		compositeDecisionMaximumOrchestrationPasses,
	)
}

func (service *CompositeChatService) runDecisionParticipants(
	ctx context.Context,
	runIDs []string,
) (map[string]loopapi.RunResult, bool, error) {
	results := make(map[string]loopapi.RunResult, len(runIDs))
	seen := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		if _, duplicate := seen[runID]; duplicate {
			return nil, false, fmt.Errorf(
				"%w: Composite frontier repeats Run %q",
				ErrChatIntegrity,
				runID,
			)
		}
		seen[runID] = struct{}{}
	}
	var wait sync.WaitGroup
	var lock sync.Mutex
	var hardErrors []error
	contended := false
	semaphore := make(chan struct{}, compositeMaximumParallelRuns)
	for _, runID := range runIDs {
		runID := runID
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			advanced, err := service.loop.Run(ctx, loopapi.RunInput{
				RunID:       runID,
				MaxSteps:    compositeChildLoopMaxSteps,
				MaxDuration: chatLoopMaxDuration,
			})
			lock.Lock()
			defer lock.Unlock()
			if isDecisionLeaseContention(err) {
				contended = true
				return
			}
			if err != nil {
				hardErrors = append(hardErrors, fmt.Errorf(
					"Composite participant %s: %w",
					runID,
					err,
				))
				return
			}
			if err := validateDecisionLoopResult(advanced, runID); err != nil {
				hardErrors = append(hardErrors, err)
				return
			}
			results[runID] = advanced
		}()
	}
	wait.Wait()
	if len(hardErrors) != 0 {
		return nil, contended, errors.Join(hardErrors...)
	}
	return results, contended, nil
}

func applyDecisionParticipantResults(
	result *CompositeChatResult,
	advanced map[string]loopapi.RunResult,
) error {
	matched := 0
	for index := range result.Children {
		if value, ok := advanced[result.Children[index].RunID]; ok {
			result.Children[index].LoopResult = value
			matched++
		}
	}
	if result.Reviewer != nil {
		if value, ok := advanced[result.Reviewer.RunID]; ok {
			result.Reviewer.LoopResult = value
			matched++
		}
	}
	for index := range result.RepairChildren {
		if value, ok := advanced[result.RepairChildren[index].RunID]; ok {
			result.RepairChildren[index].LoopResult = value
			matched++
		}
	}
	if result.RepairReviewer != nil {
		if value, ok := advanced[result.RepairReviewer.RunID]; ok {
			result.RepairReviewer.LoopResult = value
			matched++
		}
	}
	if matched != len(advanced) {
		return fmt.Errorf(
			"%w: frontier returned a Run outside the admitted family",
			ErrChatIntegrity,
		)
	}
	return nil
}

func (service *CompositeChatService) projectDecisionRoot(
	ctx context.Context,
	result CompositeChatResult,
) (CompositeChatResult, error) {
	advanced, err := service.loop.Run(ctx, loopapi.RunInput{
		RunID:       result.RootRunID,
		MaxSteps:    compositeParentLoopMaxSteps,
		MaxDuration: chatLoopMaxDuration,
	})
	if isDecisionLeaseContention(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err := validateDecisionLoopResult(advanced, result.RootRunID); err != nil {
		return result, err
	}
	result.LoopResult = advanced
	if advanced.Disposition == loopapi.DispositionTerminated {
		return service.finishDecisionTerminal(ctx, result)
	}
	return result, nil
}

func (service *CompositeChatService) finishDecisionTerminal(
	ctx context.Context,
	result CompositeChatResult,
) (CompositeChatResult, error) {
	terminal, err := service.store.GetTerminalRunResult(ctx, result.RootRunID)
	if err != nil {
		return result, err
	}
	result.TerminalResult = &terminal
	if terminal.ErrorClassification != "" {
		result.FailureCode = terminal.ErrorClassification
		return result, nil
	}
	switch terminal.State {
	case corecontract.ModelAttemptSucceeded:
		result.Reply = terminal.Output.AssistantText
	case corecontract.ModelAttemptFailed:
		result.FailureCode = terminal.ErrorClassification
	default:
		return result, fmt.Errorf(
			"%w: Composite terminal result has model state %q",
			ErrChatIntegrity,
			terminal.State,
		)
	}
	return result, nil
}

func validateDecisionLoopResult(
	result loopapi.RunResult,
	runID string,
) error {
	if err := result.Validate(); err != nil || result.RunID != runID {
		return fmt.Errorf(
			"%w: Run %q returned another or invalid result: %v",
			ErrChatIntegrity,
			runID,
			err,
		)
	}
	return nil
}

func isDecisionLeaseContention(err error) bool {
	return errors.Is(err, currentstore.ErrRunLeaseUnavailable) ||
		errors.Is(err, currentstore.ErrRunLeaseConflict)
}
