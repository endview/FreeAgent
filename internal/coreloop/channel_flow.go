package coreloop

import (
	"context"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/actiongateway"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	channelAttemptIDPrefix      = "channel-attempt-"
	reasonChannelSucceeded      = "CHANNEL_SUCCEEDED"
	reasonChannelFailed         = "CHANNEL_FAILED"
	reasonChannelUnknown        = "CHANNEL_UNKNOWN"
	reasonChannelGatewayUnknown = "CHANNEL_GATEWAY_ERROR_AFTER_PENDING"
)

var channelTransportPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameChannelTransport,
	ExactVersion: moduleapi.PortVersionV1,
}

// advanceFinalChannel converts one already-normalized final model answer into
// the sole Channel effect chain. PrepareSend is effect-free; the private
// executor is unreachable until Model outcome, History, proposal and the
// shared-ledger PENDING row commit atomically.
func (loop *UniversalLoop) advanceFinalChannel(
	ctx context.Context,
	run currentstore.RunForLoop,
	modelOutcome currentstore.CommitModelDispatchOutcomeInput,
) (loopapi.RunResult, currentstore.RunLease, error) {
	output, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOutcome.OutputCanonical,
	)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: Channel source is not one final assistant answer: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	binding, envelope, err := exactChannelPreparationClosure(run)
	if err != nil {
		return loopapi.RunResult{}, modelOutcome.Lease, err
	}
	deadline := narrowedModelDeadline(
		run.Manifest.Deadline,
		modelOutcome.Lease.ExpiresAt,
	)
	if !time.Now().UTC().Before(deadline) {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: Channel deadline expired before PrepareSend",
			ErrUniversalLoopIntegrity,
		)
	}
	if err := loop.currentActivation.CheckCurrentActivation(
		ctx,
		run.RunID,
		channelTransportPortV1,
		binding.Provider,
	); err != nil {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: Channel activation denied before PrepareSend: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	invoker, err := loop.registry.ResolveExact(
		ctx,
		binding.Provider.ArtifactDigest,
		binding.Provider.AdapterIdentity,
	)
	if err != nil || isNilLoopDependency(invoker) {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: Channel adapter is unavailable before PrepareSend",
			ErrUniversalLoopIntegrity,
		)
	}
	provider, ok := invoker.(moduleapi.ChannelTransportV1)
	if !ok || isNilLoopDependency(provider) {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: exact adapter does not implement channel.transport/v1",
			ErrUniversalLoopIntegrity,
		)
	}
	prepareRequest, _, err := moduleapi.NewChannelPrepareSendRequestV1(
		moduleapi.ChannelPrepareSendRequestV1{
			SchemaVersion: moduleapi.ChannelPrepareSendRequestSchemaV1,
			EndpointID:    run.ChannelIngress.EndpointID,
			ReplyTarget:   envelope.ReplyTarget,
			AssistantText: output.AssistantText,
		},
	)
	if err != nil {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: build Channel PrepareSend request: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	prepareContext, cancel := context.WithDeadline(ctx, deadline)
	preparedPayload, prepareErr := provider.PrepareSend(
		prepareContext,
		prepareRequest,
	)
	cancel()
	if prepareErr != nil {
		// Model remains PENDING. The next Loop/startup safety pass converts the
		// original Attempt to MODEL_UNKNOWN; no Channel PENDING or permit exists.
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: Channel PrepareSend failed before durable effect grant: %v",
			ErrUniversalLoopIntegrity,
			prepareErr,
		)
	}
	_, proposalCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		run.Member.MemberSnapshotDigest,
		run.ChannelIngress.EndpointID,
		run.ChannelIngress.IngressKey,
		envelope.ReplyTarget,
		output.AssistantText,
		preparedPayload,
	)
	if err != nil {
		return loopapi.RunResult{}, modelOutcome.Lease, fmt.Errorf(
			"%w: freeze Channel Proposal: %v",
			ErrUniversalLoopIntegrity,
			err,
		)
	}
	attemptID, err := deterministicAttemptID(
		channelAttemptIDPrefix,
		run.RunID,
		run.Member.MemberID,
		corecontract.ChannelSendLogicalStepIDV1,
	)
	if err != nil {
		return loopapi.RunResult{}, modelOutcome.Lease, err
	}
	persistCtx, persistCancel := persistenceContext(ctx)
	grant, err := loop.store.CommitModelChannelAndBeginDispatch(
		persistCtx,
		currentstore.CommitModelChannelAndBeginDispatchInput{
			Lease:                        modelOutcome.Lease,
			ModelAttemptID:               modelOutcome.AttemptID,
			InvocationID:                 modelOutcome.InvocationID,
			Provider:                     modelOutcome.Provider,
			ExpectedModelAttemptRevision: modelOutcome.ExpectedAttemptRevision,
			OutputCanonical:              modelOutcome.OutputCanonical,
			UsageReceiptCanonical:        modelOutcome.UsageReceiptCanonical,
			ProviderRequestID:            modelOutcome.ProviderRequestID,
			DispatchAttemptID:            attemptID,
			ProposalCanonical:            proposalCanonical,
			Deadline:                     deadline,
		},
	)
	persistCancel()
	if err != nil {
		return runPermitErrorResult(run, modelOutcome.Lease, err)
	}
	if !grant.Applied || !grant.GatewayAllowed {
		return loopapi.RunResult{}, grant.Lease, fmt.Errorf(
			"%w: fresh Channel PENDING transaction returned no Gateway grant",
			ErrUniversalLoopIntegrity,
		)
	}
	executed, gatewayErr := loop.actionGateway.ExecuteChannel(ctx, grant)
	if gatewayErr != nil {
		executed = actiongateway.ChannelResultV1{
			AttemptID:     grant.Channel.Attempt.AttemptID,
			Provider:      grant.Channel.Attempt.Binding.Provider,
			Outcome:       moduleapi.ChannelExecutionUnknown,
			UnknownReason: reasonChannelGatewayUnknown,
		}
	}
	persistCtx, persistCancel = persistenceContext(ctx)
	committed, err := loop.store.CommitChannelDispatchOutcome(
		persistCtx,
		currentstore.CommitChannelDispatchOutcomeInput{
			Lease:                    grant.Lease,
			AttemptID:                grant.Channel.Attempt.AttemptID,
			InvocationID:             executed.AttemptID,
			Provider:                 executed.Provider,
			ExpectedAttemptRevision:  grant.Channel.Attempt.Revision,
			Outcome:                  executed.Outcome,
			ProviderReceiptCanonical: executed.ProviderReceipt,
			ExternalOperationID:      executed.ExternalOperationID,
			ErrorClassification:      executed.ErrorClassification,
			UnknownReason:            executed.UnknownReason,
		},
	)
	persistCancel()
	if err != nil {
		// The effect may already exist. The durable row intentionally remains
		// PENDING so startup recovery can close that same row to UNKNOWN.
		return loopapi.RunResult{}, grant.Lease, err
	}
	switch committed.Record.Attempt.State {
	case currentstore.DispatchSucceeded:
		return newRunResult(
			run.RunID,
			loopapi.DispositionTerminated,
			reasonChannelSucceeded,
		), committed.Lease, nil
	case currentstore.DispatchFailed:
		reason := committed.Record.Attempt.ErrorClassification
		if reason == "" {
			reason = reasonChannelFailed
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionTerminated,
			reason,
		), committed.Lease, nil
	case currentstore.DispatchUnknown:
		reason := committed.Record.Attempt.UnknownReason
		if reason == "" {
			reason = reasonChannelUnknown
		}
		return newRunResult(
			run.RunID,
			loopapi.DispositionWaitingReconciliation,
			reason,
		), committed.Lease, nil
	default:
		return loopapi.RunResult{}, committed.Lease, fmt.Errorf(
			"%w: Channel outcome returned non-terminal state %q",
			ErrUniversalLoopIntegrity,
			committed.Record.Attempt.State,
		)
	}
}

func exactChannelPreparationClosure(
	run currentstore.RunForLoop,
) (moduleapi.PortBinding, moduleapi.ChannelInboundEnvelopeV1, error) {
	if run.ChannelIngress == nil || run.ChannelIngressEnvelope == nil ||
		run.ChannelIngress.Disposition != currentstore.ChannelIngressAccepted ||
		run.ChannelIngress.RunID != run.RunID {
		return moduleapi.PortBinding{}, moduleapi.ChannelInboundEnvelopeV1{},
			fmt.Errorf("%w: accepted Channel ingress closure is absent", ErrUniversalLoopIntegrity)
	}
	var plan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		candidate := &run.Member.PortPlans[index]
		if candidate.Port != channelTransportPortV1 {
			continue
		}
		if plan != nil {
			return moduleapi.PortBinding{}, moduleapi.ChannelInboundEnvelopeV1{},
				fmt.Errorf("%w: duplicate Channel PortPlan", ErrUniversalLoopIntegrity)
		}
		plan = candidate
	}
	if plan == nil || len(plan.Bindings) != 1 {
		return moduleapi.PortBinding{}, moduleapi.ChannelInboundEnvelopeV1{},
			fmt.Errorf("%w: Channel PortPlan must have one Binding", ErrUniversalLoopIntegrity)
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		run.ChannelIngressEnvelope.CanonicalBytes,
	)
	if err != nil || envelope.EndpointID != run.ChannelIngress.EndpointID ||
		run.ChannelIngressEnvelope.Digest != run.ChannelIngress.EnvelopeRef {
		return moduleapi.PortBinding{}, moduleapi.ChannelInboundEnvelopeV1{},
			fmt.Errorf("%w: Channel ingress envelope drift: %v", ErrUniversalLoopIntegrity, err)
	}
	return plan.Bindings[0], envelope, nil
}

func (loop *UniversalLoop) recoverChannelPending(
	ctx context.Context,
	run currentstore.RunForLoop,
	continued corecontract.LoopContinuationV1,
	lease currentstore.RunLease,
) (loopapi.RunResult, currentstore.RunLease, error) {
	dispatch, err := exactContinuedChannelDispatch(run, continued)
	if err != nil {
		return loopapi.RunResult{}, lease, err
	}
	if dispatch.Attempt.State != currentstore.DispatchPending {
		return loopapi.RunResult{}, lease, fmt.Errorf(
			"%w: CHANNEL_PENDING continuation points to %q",
			ErrUniversalLoopIntegrity,
			dispatch.Attempt.State,
		)
	}
	persistCtx, cancel := persistenceContext(ctx)
	committed, err := loop.store.CommitChannelDispatchOutcome(
		persistCtx,
		currentstore.CommitChannelDispatchOutcomeInput{
			Lease:                   lease,
			AttemptID:               dispatch.Attempt.AttemptID,
			InvocationID:            dispatch.Attempt.AttemptID,
			Provider:                dispatch.Attempt.Binding.Provider,
			ExpectedAttemptRevision: dispatch.Attempt.Revision,
			Outcome:                 moduleapi.ChannelExecutionUnknown,
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
