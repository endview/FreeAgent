package actiongateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	classificationChannelClosureDenied      = "CHANNEL_GATEWAY_CLOSURE_DENIED"
	classificationChannelActivationDenied   = "CURRENT_ACTIVATION_DENIED_BEFORE_CHANNEL_SEND"
	classificationChannelAdapterUnavailable = "CHANNEL_EXECUTOR_UNAVAILABLE"
	classificationChannelContextExpired     = "CHANNEL_DEADLINE_EXPIRED_BEFORE_EXECUTOR"
	unknownChannelExecutorError             = "CHANNEL_EXECUTOR_ERROR_AFTER_DISPATCH"
	unknownInvalidChannelExecutorResult     = "INVALID_CHANNEL_EXECUTOR_RESULT"
)

var channelPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameChannelTransport,
	ExactVersion: moduleapi.PortVersionV1,
}

// ChannelResultV1 reports the exact terminal certainty returned by the sole
// Gateway call. It is intentionally not persisted here; Current Store owns
// the CAS from the original PENDING DispatchAttempt.
type ChannelResultV1 struct {
	AttemptID           string
	Provider            moduleapi.ActivatedModuleRef
	Outcome             moduleapi.ChannelExecutionOutcomeV1
	ProviderReceipt     json.RawMessage
	ExternalOperationID string
	ErrorClassification string
	UnknownReason       string
}

// ExecuteChannel consumes the Store's private one-shot permit before it
// resolves a private ChannelExecutor. Every denial before ExecutePrepared is
// a confirmed FAILED result. Once ExecutePrepared is called, an error or an
// invalid/ambiguous result is conservatively UNKNOWN and is never replayed.
func (gateway *Gateway) ExecuteChannel(
	ctx context.Context,
	grant currentstore.CommitModelChannelAndBeginDispatchResult,
) (ChannelResultV1, error) {
	if gateway == nil || isNilDependency(gateway.store) ||
		isNilDependency(gateway.registry) || gateway.now == nil {
		return ChannelResultV1{}, fmt.Errorf(
			"%w: Gateway is not initialized",
			ErrInvalidGateway,
		)
	}
	if ctx == nil {
		return ChannelResultV1{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidGateway,
		)
	}
	if !grant.Applied || !grant.GatewayAllowed ||
		grant.Channel.Attempt.AttemptID == "" {
		return ChannelResultV1{}, fmt.Errorf(
			"%w: Store result grants no Channel execution",
			ErrInvalidGateway,
		)
	}
	if !grant.ConsumeChannelGatewayPermit() {
		return ChannelResultV1{}, fmt.Errorf(
			"%w: Channel execution permit is absent or consumed",
			ErrInvalidGateway,
		)
	}

	attemptID := grant.Channel.Attempt.AttemptID
	provider := grant.Channel.Attempt.Binding.Provider
	fail := func(classification string) (ChannelResultV1, error) {
		return failedChannelResult(
			attemptID,
			provider,
			classification,
		), nil
	}
	if err := ctx.Err(); err != nil {
		return fail(classificationChannelContextExpired)
	}
	now := gateway.now().UTC()
	if !now.Before(grant.Channel.Attempt.Deadline) {
		return fail(classificationChannelContextExpired)
	}
	run, err := gateway.store.LoadRunForLoop(ctx, grant.Lease)
	if err != nil {
		return fail(classificationChannelClosureDenied)
	}
	record, proposal, err := validateChannelGrantClosure(
		run,
		grant,
		now,
	)
	if err != nil {
		return fail(classificationChannelClosureDenied)
	}
	provider = record.Attempt.Binding.Provider
	if err := gateway.store.CheckCurrentActivation(
		ctx,
		record.Attempt.RunID,
		channelPortV1,
		provider,
	); err != nil {
		return fail(classificationChannelActivationDenied)
	}
	if err := ctx.Err(); err != nil {
		return fail(classificationChannelContextExpired)
	}
	invoker, err := gateway.registry.ResolveExact(
		ctx,
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || isNilDependency(invoker) {
		return fail(classificationChannelAdapterUnavailable)
	}
	executor, ok := invoker.(modulehost.ChannelExecutor)
	if !ok || isNilDependency(executor) {
		return fail(classificationChannelAdapterUnavailable)
	}
	request, _, err := moduleapi.NewChannelExecutionRequestV1(
		moduleapi.ChannelExecutionRequestV1{
			SchemaVersion:       moduleapi.ChannelExecutionRequestSchemaV1,
			AttemptID:           record.Attempt.AttemptID,
			EndpointID:          record.Attempt.EndpointID,
			IngressKey:          record.Attempt.IngressKey,
			ProposalDigest:      record.Attempt.ProposalRef,
			AssistantTextDigest: proposal.AssistantTextDigest,
			ReplyTarget:         bytes.Clone(proposal.ReplyTarget),
			PreparedPayload:     bytes.Clone(proposal.PreparedPayload),
		},
	)
	if err != nil {
		return fail(classificationChannelClosureDenied)
	}
	callContext, cancel := context.WithDeadline(ctx, record.Attempt.Deadline)
	defer cancel()
	if err := callContext.Err(); err != nil {
		return fail(classificationChannelContextExpired)
	}
	executed, executeErr := executor.ExecutePrepared(callContext, request)
	return normalizeChannelExecutorResult(
		request,
		provider,
		executed,
		executeErr,
	), nil
}

func validateChannelGrantClosure(
	run currentstore.RunForLoop,
	grant currentstore.CommitModelChannelAndBeginDispatchResult,
	now time.Time,
) (
	currentstore.ChannelDispatchRecord,
	moduleapi.ChannelSendProposalV1,
	error,
) {
	want := grant.Channel.Attempt
	if run.RunID != want.RunID ||
		run.Member.MemberID != want.MemberID ||
		run.Member.MemberSnapshotDigest != want.MemberSnapshotDigest ||
		run.Frame.Step != corecontract.ChannelPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != want.AttemptID ||
		run.Frame.BudgetStateRef != want.BudgetStateRef ||
		run.Frame.Revision != grant.Lease.FrameRevision ||
		run.RunRevision != grant.Lease.RunRevision ||
		!now.Before(want.Deadline) ||
		want.Deadline.After(run.Manifest.Deadline) ||
		want.Deadline.After(grant.Lease.ExpiresAt) {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("Channel grant no longer matches the fenced Run")
	}
	continued, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continued.AttemptKind != corecontract.AttemptKindChannel ||
		continued.AttemptID != want.AttemptID ||
		continued.LogicalStepID != want.LogicalStepID {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("Channel continuation differs from grant")
	}
	var persisted *currentstore.ChannelDispatchRecord
	for index := range run.ChannelDispatches {
		candidate := &run.ChannelDispatches[index]
		if candidate.Attempt.AttemptID != want.AttemptID {
			continue
		}
		if persisted != nil {
			return currentstore.ChannelDispatchRecord{},
				moduleapi.ChannelSendProposalV1{},
				fmt.Errorf("duplicate Channel Attempt")
		}
		persisted = candidate
	}
	if persisted == nil || persisted.Attempt.State != currentstore.DispatchPending ||
		!sameChannelAttemptClosure(persisted.Attempt, want) ||
		!bytes.Equal(
			persisted.Proposal.CanonicalBytes,
			grant.Channel.Proposal.CanonicalBytes,
		) {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("persisted Channel Attempt differs from grant")
	}
	plan, err := exactChannelPlan(run.Member)
	if err != nil || uint64(want.BindingIndex) >= uint64(len(plan.Bindings)) {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("frozen Channel Binding is absent")
	}
	binding := plan.Bindings[want.BindingIndex]
	bindingCanonical, _, err :=
		moduleapi.CanonicalChannelEndpointBindingV1(binding)
	if err != nil || !bytes.Equal(bindingCanonical, want.BindingCanonical) {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("frozen Channel Binding differs from grant")
	}
	contentDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentChannelSendProposal,
		persisted.Proposal.MediaType,
		persisted.Proposal.CanonicalBytes,
	)
	if err != nil || contentDigest != persisted.Proposal.Digest ||
		contentDigest != want.ProposalRef {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("Channel Proposal content identity differs from grant")
	}
	wireDigest, err := moduleapi.ChannelSendProposalDigestV1(
		persisted.Proposal.CanonicalBytes,
	)
	if err != nil {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("Channel Proposal wire identity is invalid")
	}
	proposal, err := moduleapi.RestoreChannelSendProposalV1(
		persisted.Proposal.CanonicalBytes,
		wireDigest,
	)
	if err != nil || proposal.MemberSnapshotDigest != want.MemberSnapshotDigest ||
		proposal.EndpointID != want.EndpointID ||
		proposal.IngressKey != want.IngressKey {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			fmt.Errorf("Channel Proposal identity differs from grant")
	}
	if err := validateChannelIngressReplyClosure(run, want, proposal); err != nil {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			err
	}
	if err := validateChannelLocalAuthority(
		run,
		binding,
		want,
		proposal,
	); err != nil {
		return currentstore.ChannelDispatchRecord{},
			moduleapi.ChannelSendProposalV1{},
			err
	}
	return *persisted, proposal, nil
}

func validateChannelIngressReplyClosure(
	run currentstore.RunForLoop,
	attempt currentstore.ChannelDispatchAttemptRecord,
	proposal moduleapi.ChannelSendProposalV1,
) error {
	if run.ChannelIngress == nil ||
		run.ChannelIngressEnvelope == nil ||
		run.ChannelIngress.Disposition != currentstore.ChannelIngressAccepted ||
		run.ChannelIngress.RunID != run.RunID ||
		run.ChannelIngress.EndpointID != attempt.EndpointID ||
		run.ChannelIngress.IngressKey != attempt.IngressKey ||
		run.ChannelIngress.EnvelopeRef != run.ChannelIngressEnvelope.Digest ||
		run.ChannelIngressEnvelope.Kind != currentstore.ContentChannelIngressEnvelope {
		return fmt.Errorf("Channel ingress closure differs from grant")
	}
	envelopeDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentChannelIngressEnvelope,
		run.ChannelIngressEnvelope.MediaType,
		run.ChannelIngressEnvelope.CanonicalBytes,
	)
	if err != nil || envelopeDigest != run.ChannelIngressEnvelope.Digest {
		return fmt.Errorf("Channel ingress envelope content identity is invalid")
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		run.ChannelIngressEnvelope.CanonicalBytes,
	)
	if err != nil || envelope.EndpointID != attempt.EndpointID ||
		!bytes.Equal(envelope.ReplyTarget, proposal.ReplyTarget) {
		return fmt.Errorf("Channel reply target differs from accepted ingress")
	}
	return nil
}

func validateChannelLocalAuthority(
	run currentstore.RunForLoop,
	binding moduleapi.PortBinding,
	attempt currentstore.ChannelDispatchAttemptRecord,
	proposal moduleapi.ChannelSendProposalV1,
) error {
	configContent, found := run.FindContent(binding.ConfigRef)
	if !found || configContent.Kind != currentstore.ContentConfig {
		return fmt.Errorf("Channel Config is absent")
	}
	authorityContent, found := run.FindContent(binding.AuthorityCeilingRef)
	if !found || authorityContent.Kind != currentstore.ContentAuthorityCeiling {
		return fmt.Errorf("Channel Authority is absent")
	}
	if _, err := moduleapi.RestoreChannelBindingConfigV1(
		configContent.CanonicalBytes,
	); err != nil {
		return err
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(
		authorityContent.CanonicalBytes,
	)
	if err != nil {
		return err
	}
	if authority.TenantID != run.Manifest.TenantID ||
		!contains(authority.AllowedWorkspaceIDs, run.Member.Workspace.ID) ||
		!contains(authority.AllowedEndpointIDs, attempt.EndpointID) ||
		!authority.AllowSend ||
		attempt.EffectClass != moduleapi.EffectIrreversibleWrite {
		return fmt.Errorf("Channel Authority scope does not close")
	}
	assistantText, err := sourceChannelAssistantText(
		run,
		attempt.SourceModelAttemptID,
	)
	if err != nil || uint32(len(assistantText)) > authority.MaxMessageBytes {
		return fmt.Errorf("Channel Authority message limit does not close")
	}
	assistantDigest, err := moduleapi.ChannelAssistantTextDigestV1(assistantText)
	if err != nil || assistantDigest != proposal.AssistantTextDigest {
		return fmt.Errorf("Channel assistant text does not close")
	}
	return nil
}

func sourceChannelAssistantText(
	run currentstore.RunForLoop,
	attemptID string,
) (string, error) {
	var source *currentstore.ModelDispatchRecord
	for index := range run.ModelDispatches {
		candidate := &run.ModelDispatches[index]
		if candidate.Attempt.AttemptID != attemptID {
			continue
		}
		if source != nil {
			return "", fmt.Errorf("duplicate source Model Attempt")
		}
		source = candidate
	}
	if source == nil ||
		source.Attempt.State != corecontract.ModelAttemptSucceeded ||
		source.Attempt.ResultRef == "" {
		return "", fmt.Errorf("source Model Attempt is absent")
	}
	var result *currentstore.ContentRecord
	for index := range run.History {
		entry := &run.History[index]
		if entry.SourceAttemptID != attemptID {
			continue
		}
		if result != nil {
			return "", fmt.Errorf("duplicate source Model History")
		}
		if entry.MemberID != run.Member.MemberID ||
			entry.Role != string(moduleapi.ModelRoleAssistant) ||
			entry.Content.Digest != source.Attempt.ResultRef ||
			entry.Content.Kind != currentstore.ContentModelResult {
			return "", fmt.Errorf("source Model History does not close")
		}
		candidate := entry.Content
		result = &candidate
	}
	if result == nil {
		return "", fmt.Errorf("source Model result is absent")
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.CanonicalBytes)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" {
		return "", fmt.Errorf("source Model output is not a final answer")
	}
	return output.AssistantText, nil
}

func normalizeChannelExecutorResult(
	request moduleapi.ChannelExecutionRequestV1,
	provider moduleapi.ActivatedModuleRef,
	executed moduleapi.ChannelExecutionResultV1,
	executeErr error,
) ChannelResultV1 {
	if executeErr != nil {
		return unknownChannelResult(
			request.AttemptID,
			provider,
			unknownChannelExecutorError,
		)
	}
	if executed.AttemptID != request.AttemptID ||
		!exactChannelExecutorReceipt(executed.ProviderReceipt) {
		return unknownChannelResult(
			request.AttemptID,
			provider,
			unknownInvalidChannelExecutorResult,
		)
	}
	frozen, _, err := moduleapi.NewChannelExecutionResultV1(executed)
	if err == nil {
		err = moduleapi.ValidateChannelExecutionResultForRequestV1(
			request,
			frozen,
		)
	}
	if err != nil {
		return unknownChannelResult(
			request.AttemptID,
			provider,
			unknownInvalidChannelExecutorResult,
		)
	}
	return ChannelResultV1{
		AttemptID:           request.AttemptID,
		Provider:            provider,
		Outcome:             frozen.Outcome,
		ProviderReceipt:     bytes.Clone(frozen.ProviderReceipt),
		ExternalOperationID: frozen.ExternalOperationID,
		ErrorClassification: frozen.ErrorClassification,
		UnknownReason:       frozen.UnknownReason,
	}
}

func exactChannelExecutorReceipt(receipt json.RawMessage) bool {
	if len(receipt) == 0 {
		return true
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		receipt,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxChannelProviderReceiptBytesV1,
			MaxDepth: 64,
			MaxNodes: 64 << 10,
		},
	)
	return err == nil && len(canonical) != 0 && canonical[0] == '{' &&
		bytes.Equal(canonical, receipt)
}

func failedChannelResult(
	attemptID string,
	provider moduleapi.ActivatedModuleRef,
	classification string,
) ChannelResultV1 {
	return ChannelResultV1{
		AttemptID:           attemptID,
		Provider:            provider,
		Outcome:             moduleapi.ChannelExecutionFailed,
		ErrorClassification: classification,
	}
}

func unknownChannelResult(
	attemptID string,
	provider moduleapi.ActivatedModuleRef,
	reason string,
) ChannelResultV1 {
	return ChannelResultV1{
		AttemptID:     attemptID,
		Provider:      provider,
		Outcome:       moduleapi.ChannelExecutionUnknown,
		UnknownReason: reason,
	}
}

func exactChannelPlan(
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortPlan, error) {
	for _, plan := range member.PortPlans {
		if plan.Port == channelPortV1 {
			return moduleapi.NewPortPlan(plan)
		}
	}
	return moduleapi.PortPlan{}, fmt.Errorf(
		"channel.transport/v1 PortPlan is absent",
	)
}

func sameChannelAttemptClosure(
	left currentstore.ChannelDispatchAttemptRecord,
	right currentstore.ChannelDispatchAttemptRecord,
) bool {
	return left.AttemptID == right.AttemptID &&
		left.LogicalOperationKey == right.LogicalOperationKey &&
		left.RunID == right.RunID &&
		left.MemberID == right.MemberID &&
		left.LogicalStepID == right.LogicalStepID &&
		left.SourceModelAttemptID == right.SourceModelAttemptID &&
		left.FrameRevision == right.FrameRevision &&
		left.MemberSnapshotDigest == right.MemberSnapshotDigest &&
		left.BindingIndex == right.BindingIndex &&
		bytes.Equal(left.BindingCanonical, right.BindingCanonical) &&
		left.EndpointID == right.EndpointID &&
		left.IngressKey == right.IngressKey &&
		left.ProposalRef == right.ProposalRef &&
		left.EffectClass == right.EffectClass &&
		left.MaxResultBytes == right.MaxResultBytes &&
		left.Deadline.Equal(right.Deadline) &&
		left.BudgetStateRef == right.BudgetStateRef &&
		left.State == right.State &&
		left.Revision == right.Revision
}
