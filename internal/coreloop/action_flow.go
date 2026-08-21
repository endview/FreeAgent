package coreloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/endview/freeagent/internal/actiongateway"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	actionAttemptIDPrefix      = "action-attempt-"
	reasonActionFailed         = "ACTION_FAILED"
	reasonActionUnknown        = "ACTION_UNKNOWN"
	reasonActionResultRejected = "ACTION_RESULT_REJECTED"
	reasonActionPrepared       = "ACTION_SUCCEEDED_MODEL_READY"
	reasonGatewayAfterPending  = "GATEWAY_ERROR_AFTER_PENDING"
)

var actionProviderPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}

// advanceActionReady owns the first two bounded external calls of the Action
// slice. Core requires both model-1 and action-1 capacity before it starts:
// once Action PENDING has committed, yielding before the one-shot Gateway
// call would discard the only execution permit.
func (loop *UniversalLoop) advanceActionReady(
	ctx context.Context,
	input loopapi.RunInput,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	prepared, err := loop.prepareChatRequestV1(ctx, run, lease)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	begin, err := loop.beginModelStep(
		ctx,
		run,
		lease,
		corecontract.FirstModelLogicalStepIDV1,
		prepared,
	)
	if err != nil {
		return runPermitErrorResult(run, lease, err)
	}
	lease = begin.Lease
	if !begin.InvokeAllowed {
		if begin.Created && begin.Attempt.State == corecontract.ModelAttemptFailed {
			return newRunResult(
				run.RunID,
				loopapi.DispositionTerminated,
				reasonModelFailed,
			), lease, nil
		}
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: model-1 did not create its unique invocation grant",
			ErrUniversalLoopIntegrity,
		)
	}

	modelOutcome := loop.invokeModelOutcome(ctx, begin)
	if modelOutcome.State != corecontract.ModelAttemptSucceeded {
		return loop.commitOrdinaryModelOutcome(ctx, modelOutcome)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOutcome.OutputCanonical,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: successful normalized model-1 output cannot be restored: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	if output.ActionRequest == nil {
		if run.ChannelIngress != nil {
			return loop.advanceFinalChannel(ctx, run, modelOutcome)
		}
		return loop.commitOrdinaryModelOutcome(ctx, modelOutcome)
	}

	definition, binding, rejection := resolveFrozenActionRequest(run, output)
	if rejection != "" {
		return loop.commitLegalActionRejection(ctx, modelOutcome, rejection)
	}
	if !time.Now().UTC().Before(narrowedModelDeadline(
		run.Manifest.Deadline,
		begin.Lease.ExpiresAt,
	)) {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionDeadlineExpired,
		)
	}
	if err := loop.currentActivation.CheckCurrentActivation(
		ctx,
		run.RunID,
		actionProviderPortV1,
		binding.Provider,
	); err != nil {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionAuthorityDenied,
		)
	}
	invoker, err := loop.registry.ResolveExact(
		ctx,
		binding.Provider.ArtifactDigest,
		binding.Provider.AdapterIdentity,
	)
	if err != nil || isNilLoopDependency(invoker) {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionProviderUnavailable,
		)
	}
	provider, ok := invoker.(moduleapi.ActionProviderV1)
	if !ok || isNilLoopDependency(provider) {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionProviderUnavailable,
		)
	}
	actionRequest, _, err := moduleapi.NewActionRequestV1(
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   definition.PublicActionID,
			ProviderActionID: definition.ProviderActionID,
			DefinitionDigest: definition.DefinitionDigest,
			CanonicalInput:   output.ActionRequest.CanonicalInput,
		},
	)
	if err != nil {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionInvalidInput,
		)
	}
	actionDeadline := narrowedModelDeadline(
		run.Manifest.Deadline,
		begin.Lease.ExpiresAt,
	)
	prepareContext, cancel := context.WithDeadline(ctx, actionDeadline)
	preparedPayload, prepareErr := provider.Prepare(
		prepareContext,
		actionRequest,
	)
	cancel()
	if prepareErr != nil {
		classification := currentstore.ModelActionRejectionPrepareFailed
		if !time.Now().UTC().Before(actionDeadline) {
			classification = currentstore.ModelActionRejectionDeadlineExpired
		}
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			classification,
		)
	}
	_, proposalCanonical, _, err := corecontract.NewActionProposalV1(
		run.Member.MemberSnapshotDigest,
		definition,
		actionRequest.CanonicalInput,
		preparedPayload,
	)
	if err != nil {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionPrepareFailed,
		)
	}
	budgetDecision := frozenActionBudgetDecision(run)
	if budgetDecision != currentstore.ActionBudgetAllow {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionBudgetUnknown,
		)
	}
	if !time.Now().UTC().Before(actionDeadline) {
		return loop.commitLegalActionRejection(
			ctx,
			modelOutcome,
			currentstore.ModelActionRejectionDeadlineExpired,
		)
	}
	actionAttemptID, err := deterministicAttemptID(
		actionAttemptIDPrefix,
		run.RunID,
		run.Member.MemberID,
		corecontract.FirstActionLogicalStepIDV1,
	)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	persistCtx, persistCancel := persistenceContext(ctx)
	grant, err := loop.store.CommitModelActionAndBeginDispatch(
		persistCtx,
		currentstore.CommitModelActionAndBeginDispatchInput{
			Lease:                        modelOutcome.Lease,
			ModelAttemptID:               modelOutcome.AttemptID,
			InvocationID:                 modelOutcome.InvocationID,
			Provider:                     modelOutcome.Provider,
			ExpectedModelAttemptRevision: modelOutcome.ExpectedAttemptRevision,
			OutputCanonical:              modelOutcome.OutputCanonical,
			UsageReceiptCanonical:        modelOutcome.UsageReceiptCanonical,
			ProviderRequestID:            modelOutcome.ProviderRequestID,
			DispatchAttemptID:            actionAttemptID,
			ProposalCanonical:            proposalCanonical,
			Deadline:                     actionDeadline,
			BudgetDecision:               budgetDecision,
		},
	)
	persistCancel()
	if err != nil {
		return runPermitErrorResult(run, modelOutcome.Lease, err)
	}
	lease = grant.Lease
	if !grant.Applied || !grant.GatewayAllowed {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: fresh Action PENDING transaction returned no Gateway grant",
			ErrUniversalLoopIntegrity,
		)
	}
	executed, gatewayErr := loop.actionGateway.Execute(ctx, grant)
	if gatewayErr != nil {
		executed = actionGatewayUnknownResult(grant, reasonGatewayAfterPending)
	}
	commitAction := currentstore.CommitActionDispatchOutcomeInput{
		Lease:                         grant.Lease,
		AttemptID:                     grant.Action.Attempt.AttemptID,
		InvocationID:                  executed.AttemptID,
		Provider:                      executed.Provider,
		ExpectedAttemptRevision:       grant.Action.Attempt.Revision,
		Outcome:                       executed.Outcome,
		CanonicalResult:               executed.CanonicalResult,
		ProviderReceiptCanonical:      executed.ProviderReceipt,
		ExternalOperationID:           executed.ExternalOperationID,
		ErrorClassification:           executed.ErrorClassification,
		UnknownReason:                 executed.UnknownReason,
		ResultRejectionClassification: executed.ResultRejectionClassification,
	}
	persistCtx, persistCancel = persistenceContext(ctx)
	committed, err := loop.store.CommitActionDispatchOutcome(
		persistCtx,
		commitAction,
	)
	persistCancel()
	if err != nil {
		// The executor may already have produced an effect. Leaving the durable
		// row PENDING is intentional: startup recovery converts it to UNKNOWN
		// and never replays the operation.
		return loopapi.RunResult{}, grant.Lease, err
	}
	lease = committed.Lease
	switch committed.Record.Attempt.State {
	case currentstore.ActionDispatchFailed:
		reason := committed.Record.Attempt.ErrorClassification
		if reason == "" {
			reason = reasonActionFailed
		}
		return newRunResult(run.RunID, loopapi.DispositionTerminated, reason), lease, nil
	case currentstore.ActionDispatchUnknown:
		reason := committed.Record.Attempt.UnknownReason
		if reason == "" {
			reason = reasonActionUnknown
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingReconciliation,
			reason,
		), lease, nil
	case currentstore.ActionDispatchSucceeded:
		status, classification, err := actionResultState(
			committed.Record,
			definition,
		)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		if status == corecontract.ActionResultRejected {
			if classification == "" {
				classification = reasonActionResultRejected
			}
			return newRunResult(
				run.RunID,
				loopapi.DispositionTerminated,
				classification,
			), lease, nil
		}
		requiredSteps := uint32(3)
		if run.ChannelIngress != nil {
			requiredSteps = 4
		}
		if input.MaxSteps < requiredSteps {
			return newRunResult(
				run.RunID,
				loopapi.DispositionYielded,
				reasonActionPrepared,
			), lease, nil
		}
		refreshed, err := loop.store.LoadRunForLoop(ctx, lease)
		if err != nil {
			return loopapi.RunResult{}, lease, err
		}
		return loop.advanceModelAfterAction(ctx, refreshed, lease)
	default:
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: Action outcome returned non-terminal state %q",
			ErrUniversalLoopIntegrity,
			committed.Record.Attempt.State,
		)
	}
}

func (loop *UniversalLoop) advanceModelAfterAction(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	prepared, err := prepareModelAfterActionRequestV1(run)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	begin, err := loop.beginModelStep(
		ctx,
		run,
		lease,
		corecontract.SecondModelLogicalStepIDV1,
		prepared,
	)
	if err != nil {
		return runPermitErrorResult(run, lease, err)
	}
	lease = begin.Lease
	if !begin.InvokeAllowed {
		if begin.Created && begin.Attempt.State == corecontract.ModelAttemptFailed {
			return newRunResult(
				run.RunID,
				loopapi.DispositionTerminated,
				reasonModelFailed,
			), lease, nil
		}
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: model-2 did not create its unique invocation grant",
			ErrUniversalLoopIntegrity,
		)
	}
	outcome := loop.invokeModelOutcome(ctx, begin)
	if outcome.State == corecontract.ModelAttemptSucceeded {
		output, restoreErr := moduleapi.RestoreModelGenerateOutputV1(
			outcome.OutputCanonical,
		)
		if restoreErr != nil {
			return loopapi.RunResult{}, lease, fmt.Errorf(
				"%w: successful normalized model-2 output cannot be restored: %v",
				ErrUniversalLoopIntegrity,
				restoreErr,
			)
		}
		if output.ActionRequest != nil {
			return loop.commitLegalActionRejection(
				ctx,
				outcome,
				currentstore.ModelActionRejectionLimitReached,
			)
		}
		if run.ChannelIngress != nil {
			return loop.advanceFinalChannel(ctx, run, outcome)
		}
	}
	return loop.commitOrdinaryModelOutcome(ctx, outcome)
}

func (loop *UniversalLoop) recoverActionPending(
	ctx context.Context,
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	dispatch, err := exactContinuedActionDispatch(run, continued)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	if dispatch.Attempt.State != currentstore.ActionDispatchPending {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: ACTION_PENDING continuation points to %q",
			ErrUniversalLoopIntegrity,
			dispatch.Attempt.State,
		)
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitActionDispatchOutcome(
		persistCtx,
		currentstore.CommitActionDispatchOutcomeInput{
			Lease:                   lease,
			AttemptID:               dispatch.Attempt.AttemptID,
			InvocationID:            dispatch.Attempt.AttemptID,
			Provider:                dispatch.Attempt.Binding.Provider,
			ExpectedAttemptRevision: dispatch.Attempt.Revision,
			Outcome:                 moduleapi.ActionExecutionUnknown,
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

func continuedUnknownReason(
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
) (string, error) {
	switch continued.AttemptKind {
	case corecontract.AttemptKindModel:
		dispatch, err := exactContinuedDispatch(run, continued)
		if err != nil {
			return "", err
		}
		if dispatch.Attempt.State != corecontract.ModelAttemptUnknown {
			return "", fmt.Errorf(
				"%w: waiting Model continuation points to %q",
				ErrUniversalLoopIntegrity,
				dispatch.Attempt.State,
			)
		}
		return dispatch.Attempt.UnknownReason, nil
	case corecontract.AttemptKindAction:
		dispatch, err := exactContinuedActionDispatch(run, continued)
		if err != nil {
			return "", err
		}
		if dispatch.Attempt.State != currentstore.ActionDispatchUnknown {
			return "", fmt.Errorf(
				"%w: waiting Action continuation points to %q",
				ErrUniversalLoopIntegrity,
				dispatch.Attempt.State,
			)
		}
		return dispatch.Attempt.UnknownReason, nil
	case corecontract.AttemptKindChannel:
		dispatch, err := exactContinuedChannelDispatch(run, continued)
		if err != nil {
			return "", err
		}
		if dispatch.Attempt.State != currentstore.DispatchUnknown {
			return "", fmt.Errorf(
				"%w: waiting Channel continuation points to %q",
				ErrUniversalLoopIntegrity,
				dispatch.Attempt.State,
			)
		}
		return dispatch.Attempt.UnknownReason, nil
	default:
		return "", fmt.Errorf(
			"%w: waiting continuation has unsupported AttemptKind %q",
			ErrUniversalLoopIntegrity,
			continued.AttemptKind,
		)
	}
}

func exactContinuedActionDispatch(
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
) (currentstore.ActionDispatchRecord, error) {
	if continued.AttemptKind != corecontract.AttemptKindAction {
		return currentstore.ActionDispatchRecord{}, fmt.Errorf(
			"%w: continuation does not name an Action Attempt",
			ErrUniversalLoopIntegrity,
		)
	}
	var found *currentstore.ActionDispatchRecord
	for index := range run.ActionDispatches {
		dispatch := &run.ActionDispatches[index]
		if dispatch.Attempt.AttemptID != continued.AttemptID {
			continue
		}
		if found != nil {
			return currentstore.ActionDispatchRecord{}, fmt.Errorf(
				"%w: duplicate continued Action Attempt",
				ErrUniversalLoopIntegrity,
			)
		}
		found = dispatch
	}
	if found == nil || found.Attempt.LogicalStepID != continued.LogicalStepID {
		return currentstore.ActionDispatchRecord{}, fmt.Errorf(
			"%w: continued Action Attempt is absent or mismatched",
			ErrUniversalLoopIntegrity,
		)
	}
	return *found, nil
}

func exactContinuedChannelDispatch(
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
) (currentstore.ChannelDispatchRecord, error) {
	if continued.AttemptKind != corecontract.AttemptKindChannel {
		return currentstore.ChannelDispatchRecord{}, fmt.Errorf(
			"%w: continuation does not name a Channel Attempt",
			ErrUniversalLoopIntegrity,
		)
	}
	var found *currentstore.ChannelDispatchRecord
	for index := range run.ChannelDispatches {
		dispatch := &run.ChannelDispatches[index]
		if dispatch.Attempt.AttemptID != continued.AttemptID {
			continue
		}
		if found != nil {
			return currentstore.ChannelDispatchRecord{}, fmt.Errorf(
				"%w: duplicate continued Channel Attempt",
				ErrUniversalLoopIntegrity,
			)
		}
		found = dispatch
	}
	if found == nil || found.Attempt.LogicalStepID != continued.LogicalStepID {
		return currentstore.ChannelDispatchRecord{}, fmt.Errorf(
			"%w: continued Channel Attempt is absent or mismatched",
			ErrUniversalLoopIntegrity,
		)
	}
	return *found, nil
}

func (loop *UniversalLoop) persistedTerminalReason(
	ctx context.Context,
	runID string,
) (string, error) {
	terminal, err := loop.store.GetTerminalRunResult(ctx, runID)
	if err != nil {
		return "", err
	}
	if terminal.AttemptKind == "" && terminal.ReasonCode != "" {
		return terminal.ReasonCode, nil
	}
	if terminal.AttemptKind == corecontract.AttemptKindAction {
		if terminal.ErrorClassification != "" {
			return terminal.ErrorClassification, nil
		}
		switch terminal.ActionState {
		case currentstore.ActionDispatchFailed:
			return reasonActionFailed, nil
		case currentstore.ActionDispatchSucceeded:
			return reasonActionResultRejected, nil
		default:
			return "", fmt.Errorf(
				"%w: terminal Action state is %q",
				ErrUniversalLoopIntegrity,
				terminal.ActionState,
			)
		}
	}
	if terminal.AttemptKind == corecontract.AttemptKindChannel {
		if terminal.ErrorClassification != "" {
			return terminal.ErrorClassification, nil
		}
		switch terminal.ChannelState {
		case currentstore.DispatchSucceeded:
			return "CHANNEL_SUCCEEDED", nil
		case currentstore.DispatchFailed:
			return "CHANNEL_FAILED", nil
		default:
			return "", fmt.Errorf(
				"%w: terminal Channel state is %q",
				ErrUniversalLoopIntegrity,
				terminal.ChannelState,
			)
		}
	}
	if terminal.ModelState == corecontract.ModelAttemptSucceeded &&
		terminal.ErrorClassification != "" {
		// A successful model carrying a terminal classification is the
		// durable legal-Action-rejection event projection.
		return terminal.ErrorClassification, nil
	}
	return terminalReason(terminal.ModelState)
}

func (loop *UniversalLoop) beginModelStep(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	logicalStepID string,
	prepared preparedPureChatRequestV1,
) (currentstore.BeginModelDispatchResult, error) {
	attemptID, err := deterministicAttemptID(
		modelAttemptIDPrefix,
		run.RunID,
		run.Member.MemberID,
		logicalStepID,
	)
	if err != nil {
		return currentstore.BeginModelDispatchResult{}, err
	}
	return loop.store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   attemptID,
			LogicalStepID:               logicalStepID,
			ContextCompilationCanonical: prepared.ContextCompilationCanonical,
			RequestCanonical:            prepared.RequestCanonical,
			Deadline: narrowedModelDeadline(
				run.Manifest.Deadline,
				lease.ExpiresAt,
			),
		},
	)
}

func deterministicAttemptID(
	prefix string,
	runID string,
	memberID string,
	logicalStepID string,
) (string, error) {
	key, err := corecontract.ModelLogicalOperationKey(
		runID,
		memberID,
		logicalStepID,
	)
	if err != nil {
		return "", fmt.Errorf(
			"%w: derive %s Attempt identity: %v",
			ErrUniversalLoopIntegrity,
			logicalStepID,
			err,
		)
	}
	return prefix + key, nil
}

func resolveFrozenActionRequest(
	run currentstore.RunForLoop,
	output moduleapi.ModelGenerateOutputV1,
) (
	corecontract.FrozenActionDefinitionV1,
	moduleapi.PortBinding,
	string,
) {
	if output.ActionRequest == nil {
		return corecontract.FrozenActionDefinitionV1{},
			moduleapi.PortBinding{},
			currentstore.ModelActionRejectionUnknownAction
	}
	var definition *corecontract.FrozenActionDefinitionV1
	for index := range run.Member.Actions {
		candidate := &run.Member.Actions[index]
		if candidate.PublicActionID == output.ActionRequest.ActionID {
			if definition != nil {
				return corecontract.FrozenActionDefinitionV1{},
					moduleapi.PortBinding{},
					currentstore.ModelActionRejectionUnknownAction
			}
			definition = candidate
		}
	}
	if definition == nil {
		return corecontract.FrozenActionDefinitionV1{},
			moduleapi.PortBinding{},
			currentstore.ModelActionRejectionUnknownAction
	}
	if err := moduleapi.ValidateActionInputV1(
		definition.InputSchema,
		output.ActionRequest.CanonicalInput,
	); err != nil {
		return corecontract.FrozenActionDefinitionV1{},
			moduleapi.PortBinding{},
			currentstore.ModelActionRejectionInvalidInput
	}
	var actionPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		candidate := &run.Member.PortPlans[index]
		if candidate.Port == actionProviderPortV1 {
			if actionPlan != nil {
				return corecontract.FrozenActionDefinitionV1{},
					moduleapi.PortBinding{},
					currentstore.ModelActionRejectionAuthorityDenied
			}
			actionPlan = candidate
		}
	}
	if actionPlan == nil ||
		uint64(definition.BindingIndex) >= uint64(len(actionPlan.Bindings)) {
		return corecontract.FrozenActionDefinitionV1{},
			moduleapi.PortBinding{},
			currentstore.ModelActionRejectionAuthorityDenied
	}
	return *definition, actionPlan.Bindings[definition.BindingIndex], ""
}

// frozenActionBudgetDecision is intentionally narrow while BudgetPolicy is
// still a generic canonical object. An empty policy and the exact built-in
// zero-cost-development schema have no unknown required cost fact; every
// other configured rule fails closed as BUDGET_UNKNOWN.
func frozenActionBudgetDecision(
	run currentstore.RunForLoop,
) currentstore.ActionBudgetDecision {
	policy, found := run.FindContent(run.Manifest.BudgetPolicy.Digest)
	if !found || policy.Kind != currentstore.ContentPolicy {
		return currentstore.ActionBudgetUnknown
	}
	document, err := corecontract.RestorePolicyDocument(
		policy.CanonicalBytes,
		run.Manifest.BudgetPolicy,
	)
	if err != nil || document.PolicyType != corecontract.PolicyCost ||
		!actionBudgetBodyHasNoUnknownCost(document.Body) {
		return currentstore.ActionBudgetUnknown
	}
	return currentstore.ActionBudgetAllow
}

func actionBudgetBodyHasNoUnknownCost(body json.RawMessage) bool {
	if bytes.Equal(body, []byte(`{}`)) {
		return true
	}
	var zeroCost struct {
		Currency             *string `json:"currency"`
		MaxRunCostMicrounits *uint64 `json:"max_run_cost_microunits"`
		PricingMode          *string `json:"pricing_mode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&zeroCost); err != nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false
	}
	return zeroCost.Currency != nil && *zeroCost.Currency == "USD" &&
		zeroCost.MaxRunCostMicrounits != nil &&
		*zeroCost.MaxRunCostMicrounits == 0 &&
		zeroCost.PricingMode != nil &&
		*zeroCost.PricingMode == "ZERO_COST_DEVELOPMENT"
}

func (loop *UniversalLoop) commitLegalActionRejection(
	ctx context.Context,
	outcome currentstore.CommitModelDispatchOutcomeInput,
	classification string,
) (loopapi.RunResult, currentstore.RunLease, error) {
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitLegalModelActionRejection(
		persistCtx,
		currentstore.CommitLegalModelActionRejectionInput{
			Lease:                        outcome.Lease,
			ModelAttemptID:               outcome.AttemptID,
			InvocationID:                 outcome.InvocationID,
			Provider:                     outcome.Provider,
			ExpectedModelAttemptRevision: outcome.ExpectedAttemptRevision,
			OutputCanonical:              outcome.OutputCanonical,
			UsageReceiptCanonical:        outcome.UsageReceiptCanonical,
			ProviderRequestID:            outcome.ProviderRequestID,
			ErrorClassification:          classification,
		},
	)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, outcome.Lease, err
	}
	return newRunResult(
		outcome.Lease.RunID,
		loopapi.DispositionTerminated,
		committed.ErrorClassification,
	), committed.Lease, nil
}

func (loop *UniversalLoop) commitOrdinaryModelOutcome(
	ctx context.Context,
	outcome currentstore.CommitModelDispatchOutcomeInput,
) (loopapi.RunResult, currentstore.RunLease, error) {
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitModelDispatchOutcome(persistCtx, outcome)
	cancel()
	if err != nil {
		return loopapi.RunResult{}, outcome.Lease, err
	}
	return runResultForOutcome(
		outcome.Lease.RunID,
		committed.Record.Attempt.State,
		outcome.UnknownReason,
		committed.Lease,
	)
}

func actionResultState(
	record currentstore.ActionDispatchRecord,
	definition corecontract.FrozenActionDefinitionV1,
) (corecontract.ActionResultStatusV1, string, error) {
	if record.Result == nil {
		return "", "", fmt.Errorf(
			"%w: successful Action has no result record",
			ErrUniversalLoopIntegrity,
		)
	}
	result, err := corecontract.RestoreActionResultV1(
		record.Result.CanonicalBytes,
		record.Result.Digest,
		definition,
	)
	if err != nil {
		return "", "", fmt.Errorf(
			"%w: restore committed Action result: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	return result.Status, result.ErrorClassification, nil
}

func actionGatewayUnknownResult(
	grant currentstore.CommitModelActionAndBeginDispatchResult,
	reason string,
) actiongateway.ResultV1 {
	return actiongateway.ResultV1{
		AttemptID:     grant.Action.Attempt.AttemptID,
		Provider:      grant.Action.Attempt.Binding.Provider,
		Outcome:       moduleapi.ActionExecutionUnknown,
		UnknownReason: reason,
	}
}
