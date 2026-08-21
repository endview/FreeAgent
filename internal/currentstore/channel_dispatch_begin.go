package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// ChannelBudgetDecision is the explicit fail-closed bridge to the current
// generic BudgetPolicy body. Channel delivery is never admitted on an
// unknown budget decision.
type ChannelBudgetDecision string

const (
	ChannelBudgetAllow   ChannelBudgetDecision = "ALLOW"
	ChannelBudgetUnknown ChannelBudgetDecision = "BUDGET_UNKNOWN"
)

// CommitModelChannelAndBeginDispatchInput carries the exact final Model
// outcome and the Core-built Channel proposal. The Channel provider,
// endpoint, ingress identity, authority and result bound are derived from the
// frozen Run and cannot be selected by the caller.
type CommitModelChannelAndBeginDispatchInput struct {
	Lease                        RunLease
	ModelAttemptID               string
	InvocationID                 string
	Provider                     moduleapi.ActivatedModuleRef
	ExpectedModelAttemptRevision uint64
	OutputCanonical              []byte
	UsageReceiptCanonical        []byte
	ProviderRequestID            string
	DispatchAttemptID            string
	ProposalCanonical            []byte
	Deadline                     time.Time
	BudgetDecision               ChannelBudgetDecision
}

// CommitModelChannelAndBeginDispatch is the only transition from a final
// Model answer to an externally visible Channel effect. Model outcome,
// Usage, assistant History, proposal, CHANNEL_SEND PENDING, Frame and Event
// commit atomically. The process-local Gateway permit exists only after that
// transaction commits and is never recreated by an idempotent re-entry.
func (store *Store) CommitModelChannelAndBeginDispatch(
	ctx context.Context,
	input CommitModelChannelAndBeginDispatchInput,
) (CommitModelChannelAndBeginDispatchResult, error) {
	if ctx == nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: context is nil", ErrInvalidChannelDispatch,
		)
	}
	input.OutputCanonical = bytes.Clone(input.OutputCanonical)
	input.UsageReceiptCanonical = bytes.Clone(input.UsageReceiptCanonical)
	input.ProposalCanonical = bytes.Clone(input.ProposalCanonical)
	if !validLeaseOpaqueID(input.ModelAttemptID) ||
		!validLeaseOpaqueID(input.DispatchAttemptID) ||
		input.InvocationID != input.ModelAttemptID ||
		input.ExpectedModelAttemptRevision >= math.MaxInt64 {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: invalid Model/Channel Attempt identity or revision",
			ErrInvalidChannelDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: invalid Model invocation Provider: %v",
			ErrInvalidChannelDispatch,
			err,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: %v", ErrInvalidChannelDispatch, err,
		)
	}
	if input.BudgetDecision != ChannelBudgetAllow {
		if input.BudgetDecision == ChannelBudgetUnknown {
			return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
				"%w: BUDGET_UNKNOWN cannot begin Channel delivery",
				ErrChannelDispatchConflict,
			)
		}
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: explicit Channel BudgetDecision=ALLOW is required",
			ErrInvalidChannelDispatch,
		)
	}
	deadline, err := normalizeModelDeadline(input.Deadline)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Channel deadline must be UTC at microsecond precision",
			ErrInvalidChannelDispatch,
		)
	}
	input.Deadline = deadline
	preparedModel, err := prepareModelDispatchOutcome(
		CommitModelDispatchOutcomeInput{
			Lease:                   input.Lease,
			AttemptID:               input.ModelAttemptID,
			InvocationID:            input.InvocationID,
			Provider:                input.Provider,
			ExpectedAttemptRevision: input.ExpectedModelAttemptRevision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         input.OutputCanonical,
			UsageReceiptCanonical:   input.UsageReceiptCanonical,
			ProviderRequestID:       input.ProviderRequestID,
		},
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if preparedModel.output.ActionRequest != nil ||
		preparedModel.output.AssistantText == "" ||
		len(input.ProposalCanonical) == 0 {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: final assistant text and Channel Proposal are required",
			ErrInvalidChannelDispatch,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: acquire model-to-Channel connection: %w", err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: begin CommitModelChannelAndBeginDispatch: %w", err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	currentModel, err := queryModelDispatchRecord(ctx, connection, input.ModelAttemptID)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if currentModel.Attempt.RunID != input.Lease.RunID ||
		currentModel.Attempt.Binding.Provider != input.Provider {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model Attempt identity differs",
			ErrChannelDispatchConflict,
		)
	}
	if currentModel.Attempt.State == corecontract.ModelAttemptSucceeded {
		result, err := reopenCommittedModelChannel(
			ctx, connection, input, preparedModel, currentModel,
		)
		if err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModelV1, input.ModelAttemptID,
		); err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceChannelV1, input.DispatchAttemptID,
		); err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
				"currentstore: commit model-to-Channel re-entry: %w", err,
			)
		}
		committed = true
		return result, nil
	}
	if currentModel.Attempt.State != corecontract.ModelAttemptPending ||
		currentModel.Attempt.Revision != input.ExpectedModelAttemptRevision {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model Attempt is not the expected PENDING revision",
			ErrChannelDispatchConflict,
		)
	}

	run, err := loadRunForChannelWrite(ctx, connection, input.Lease)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if run.CancellationRequest != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Run %q",
			ErrRunCanceled,
			run.RunID,
		)
	}
	if run.Frame.Step != corecontract.ModelPendingLoopStep ||
		run.Frame.PendingAttemptID != currentModel.Attempt.AttemptID ||
		run.Frame.PendingDispatchAttemptID != "" ||
		!memberHasChannelPort(run.Member) {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model/Frame is not a Channel-enabled final Model",
			ErrChannelDispatchIntegrity,
		)
	}
	if err := validateAttemptAgainstOutcomeRun(currentModel, run); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := validateChannelFinalModelSource(ctx, connection, currentModel); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}

	receipt, envelope, err := loadAcceptedChannelIngressForRun(
		ctx, connection, run,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	plan, err := exactChannelPlan(run.Member)
	if err != nil || len(plan.Bindings) != 1 {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: channel.transport/v1 must freeze exactly one Binding",
			ErrChannelDispatchIntegrity,
		)
	}
	binding := plan.Bindings[0]
	bindingCanonical, err := canonicalModelBinding(binding)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil || bindingDigest != receipt.EndpointBindingDigest {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: frozen Channel Binding differs from accepted ingress",
			ErrChannelDispatchIntegrity,
		)
	}
	proposalDigest, err := ComputeContentDigest(
		ContentChannelSendProposal,
		admissionJSONMediaType,
		input.ProposalCanonical,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	proposalRecord := ContentRecord{
		Digest: proposalDigest, Kind: ContentChannelSendProposal,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(input.ProposalCanonical),
	}
	proposal, _, err := restoreExactChannelProposal(
		proposalRecord,
		run.Member.MemberSnapshotDigest,
		receipt.EndpointID,
		receipt.IngressKey,
		preparedModel.output.AssistantText,
	)
	if err != nil || !bytes.Equal(proposal.ReplyTarget, envelope.ReplyTarget) {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Proposal does not close the accepted ingress reply target",
			ErrChannelDispatchIntegrity,
		)
	}
	if err := validateFrozenChannelSendAuthority(
		run, binding, receipt.EndpointID, preparedModel.output.AssistantText,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := checkCurrentActivation(
		ctx,
		connection,
		run.RunID,
		moduleapi.PortRef{
			Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1,
		},
		binding.Provider,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: deny-only current Channel Activation check: %v",
			ErrChannelDispatchConflict,
			err,
		)
	}
	if input.Deadline.UnixMicro() <= nowUnixMicro() ||
		input.Deadline.After(run.Manifest.Deadline) ||
		input.Deadline.After(input.Lease.ExpiresAt) {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Channel deadline is expired or exceeds a frozen boundary",
			ErrChannelDispatchConflict,
		)
	}
	if err := requireNoExistingChannelAttempt(
		ctx, connection, run.RunID, run.Member.MemberID,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := requireNoCrossFamilyLogicalStep(
		ctx,
		connection,
		run.RunID,
		run.Member.MemberID,
		corecontract.ChannelSendLogicalStepIDV1,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	operationKey, err := channelLogicalOperationKey(
		run.RunID, run.Member.MemberID, corecontract.ChannelSendLogicalStepIDV1,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Channel operation key: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	ledgerHead, err := validateModelUsageLedgerHead(
		ctx, connection, run.RunID, run.Frame.BudgetStateRef,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	mergedModel, err := mergeModelOutcomeFacts(currentModel, preparedModel)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if mergedModel.Usage.LedgerSequence == nil && modelUsageHasBillableFact(mergedModel.Usage) {
		if ledgerHead >= math.MaxInt64 {
			return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
				"%w: Usage ledger cannot advance", ErrChannelDispatchIntegrity,
			)
		}
		sequence := ledgerHead + 1
		mergedModel.Usage.LedgerSequence = &sequence
	}
	nextBudgetRef := run.Frame.BudgetStateRef
	if mergedModel.Usage.LedgerSequence != nil {
		nextBudgetRef, err = corecontract.NewBudgetStateRefV1(
			run.RunID, *mergedModel.Usage.LedgerSequence,
		)
		if err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, err
		}
	}
	nextModelRevision, err := incrementSQLiteUint(currentModel.Attempt.Revision, "Model Attempt revision")
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	nextUsageRevision, err := incrementSQLiteUint(currentModel.Usage.Revision, "Usage revision")
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(run.RunRevision, "Run revision")
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(run.Frame.Revision, "Frame revision")
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(run.Frame.LastAuthoritativeEvent, "RunEvent sequence")
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.ChannelPendingLoopStep,
		corecontract.AttemptKindChannel,
		corecontract.ChannelSendLogicalStepIDV1,
		input.DispatchAttemptID,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	createdAt := nowUnixMicro()
	for _, content := range []*preparedAdmissionContent{
		preparedModel.resultContent,
		preparedModel.receiptContent,
		{
			Digest: proposalDigest, Kind: ContentChannelSendProposal,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(input.ProposalCanonical),
		},
	} {
		if content == nil {
			continue
		}
		if err := putAdmissionContent(ctx, connection, *content, createdAt); err != nil {
			return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
				"%w: persist model-to-Channel content: %v",
				ErrChannelDispatchIntegrity,
				err,
			)
		}
	}
	mergedModel.Attempt.Revision = nextModelRevision
	mergedModel.Usage.Revision = nextUsageRevision
	mergedModel.Attempt.UpdatedAt = time.UnixMicro(createdAt).UTC()
	if err := updateModelOutcomeRowsForChannel(
		ctx, connection, currentModel, mergedModel, createdAt,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := appendSuccessfulModelOutcomeMemory(
		ctx,
		connection,
		run,
		mergedModel.Attempt,
		preparedModel.output.AssistantText,
		createdAt,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := appendChannelAssistantHistory(
		ctx, connection, run, mergedModel.Attempt, createdAt,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO dispatch_attempts(
			attempt_id, dispatch_kind, logical_operation_key,
			run_id, tenant_id, workspace_id, member_id, logical_step_id, source_model_attempt_id,
			frame_revision, member_snapshot_digest, binding_index, binding_json,
			channel_endpoint_id, channel_ingress_key, channel_proposal_ref,
			effect_class, max_result_bytes, deadline, budget_state_ref,
			state, revision, created_at, updated_at
		) VALUES(?, 'CHANNEL_SEND', ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', 0, ?, ?)
	`,
		input.DispatchAttemptID,
		operationKey,
		run.RunID,
		run.Manifest.TenantID,
		run.Manifest.Workspace.ID,
		run.Member.MemberID,
		corecontract.ChannelSendLogicalStepIDV1,
		currentModel.Attempt.AttemptID,
		int64(run.Frame.Revision),
		run.Member.MemberSnapshotDigest,
		bindingCanonical,
		receipt.EndpointID,
		receipt.IngressKey,
		proposalDigest,
		string(moduleapi.EffectIrreversibleWrite),
		int64(moduleapi.MaxChannelProviderReceiptBytesV1),
		input.Deadline.UnixMicro(),
		nextBudgetRef,
		createdAt,
		createdAt,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: insert Channel PENDING: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	modelResource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceModelV1, input.ModelAttemptID,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	channelResource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceChannelV1, input.DispatchAttemptID,
	)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	eventUsage, err := modelUsageEventV1(mergedModel.Usage)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	eventCanonical, eventDigest, err := prepareChannelDispatchEvent(channelDispatchEventV1{
		SchemaVersion: channelDispatchEventSchemaV1, RunID: run.RunID,
		AttemptID: input.DispatchAttemptID, LogicalStepID: corecontract.ChannelSendLogicalStepIDV1,
		LogicalOperationKey: operationKey, ProposalDigest: proposalDigest,
		State: DispatchPending, TransitionOrigin: corecontract.DispatchTransitionBeginV1,
		SourceModelAttemptID: input.ModelAttemptID, SourceModelUsage: eventUsage,
		SourceModelSemanticDigest: modelResource.Snapshot.SemanticDigest,
		ResourceSemanticDigest:    channelResource.Snapshot.SemanticDigest,
	})
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload, MediaType: admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, createdAt); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := updateRunAndFrameForChannelBegin(
		ctx,
		connection,
		input.Lease,
		run,
		nextRunRevision,
		nextFrameRevision,
		nextEvent,
		nextBudgetRef,
		continuation,
		input.DispatchAttemptID,
		eventDigest,
		createdAt,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := appendChannelBeginRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.ModelAttemptID, overviewTransitionModelChannelSourceV1,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if err := appendChannelResourceObservationV1(
		ctx, connection, input.DispatchAttemptID, overviewTransitionChannelBeginV1,
	); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	storedModel, err := queryModelDispatchRecord(ctx, connection, input.ModelAttemptID)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	storedChannel, err := queryChannelDispatchRecord(ctx, connection, input.DispatchAttemptID)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: commit model-to-Channel: %w", err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	permit := &channelGatewayPermit{closure: channelGatewayPermitClosure{
		attemptID:            storedChannel.Attempt.AttemptID,
		runID:                storedChannel.Attempt.RunID,
		memberID:             storedChannel.Attempt.MemberID,
		memberSnapshotDigest: storedChannel.Attempt.MemberSnapshotDigest,
		bindingIndex:         storedChannel.Attempt.BindingIndex,
		bindingCanonical:     bytes.Clone(storedChannel.Attempt.BindingCanonical),
		proposalDigest:       storedChannel.Attempt.ProposalRef,
		deadline:             storedChannel.Attempt.Deadline,
		lease:                nextLease,
	}}
	return CommitModelChannelAndBeginDispatchResult{
		Model:          cloneModelDispatchRecord(storedModel),
		Channel:        cloneChannelDispatchRecord(storedChannel),
		Lease:          nextLease,
		Applied:        true,
		GatewayAllowed: true,
		permit:         permit,
	}, nil
}

func validateChannelFinalModelSource(
	ctx context.Context,
	connection *sql.Conn,
	current ModelDispatchRecord,
) error {
	switch current.Attempt.LogicalStepID {
	case corecontract.PureChatModelLogicalStepIDV1:
		if current.Attempt.SourceDispatchAttemptID != "" {
			return fmt.Errorf(
				"%w: pure-chat Model unexpectedly has an Action source",
				ErrChannelDispatchIntegrity,
			)
		}
	case firstModelLogicalStepID:
		if current.Attempt.SourceDispatchAttemptID != "" {
			return fmt.Errorf(
				"%w: model-1 unexpectedly has an Action source",
				ErrChannelDispatchIntegrity,
			)
		}
	case secondModelLogicalStepID:
		if current.Attempt.SourceDispatchAttemptID == "" {
			return fmt.Errorf(
				"%w: model-2 lacks its Action source", ErrChannelDispatchIntegrity,
			)
		}
		action, err := queryActionDispatchRecord(
			ctx, connection, current.Attempt.SourceDispatchAttemptID,
		)
		if err != nil {
			return err
		}
		if err := validatePersistedSecondModelRequestClosure(
			ctx, connection, current.Attempt, action,
		); err != nil {
			return fmt.Errorf(
				"%w: model-2 Action closure: %v", ErrChannelDispatchIntegrity, err,
			)
		}
	default:
		return fmt.Errorf(
			"%w: only model-1 or model-2 can produce a Channel answer",
			ErrChannelDispatchIntegrity,
		)
	}
	return nil
}

func loadAcceptedChannelIngressForRun(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	run RunForLoop,
) (ChannelIngressReceipt, moduleapi.ChannelInboundEnvelopeV1, error) {
	receipt, found, err := scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE run_id=?
	`, run.RunID))
	if err != nil || !found {
		return ChannelIngressReceipt{}, moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf(
			"%w: accepted Channel ingress receipt is unavailable: %v",
			ErrChannelDispatchIntegrity,
			err,
		)
	}
	if err := verifyChannelReceipt(ctx, queryer, receipt); err != nil ||
		receipt.Disposition != ChannelIngressAccepted ||
		receipt.TenantID != run.Manifest.TenantID ||
		receipt.WorkspaceID != run.Member.Workspace.ID ||
		receipt.RunID != run.RunID {
		return ChannelIngressReceipt{}, moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf(
			"%w: accepted ingress does not close the Run: %v",
			ErrChannelDispatchIntegrity,
			err,
		)
	}
	content, err := queryActionContent(
		ctx, queryer, receipt.EnvelopeRef, ContentChannelIngressEnvelope,
	)
	if err != nil {
		return ChannelIngressReceipt{}, moduleapi.ChannelInboundEnvelopeV1{}, err
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(content.CanonicalBytes)
	if err != nil || envelope.EndpointID != receipt.EndpointID {
		return ChannelIngressReceipt{}, moduleapi.ChannelInboundEnvelopeV1{}, fmt.Errorf(
			"%w: accepted ingress envelope is invalid: %v",
			ErrChannelDispatchIntegrity,
			err,
		)
	}
	return receipt, envelope, nil
}

func validateFrozenChannelSendAuthority(
	run RunForLoop,
	binding moduleapi.PortBinding,
	endpointID string,
	assistantText string,
) error {
	configRecord, found := run.FindContent(binding.ConfigRef)
	if !found || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Channel Config closure is unavailable", ErrChannelDispatchIntegrity,
		)
	}
	if _, err := moduleapi.RestoreChannelBindingConfigV1(configRecord.CanonicalBytes); err != nil {
		return fmt.Errorf(
			"%w: Channel Config is invalid: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	authorityRecord, found := run.FindContent(binding.AuthorityCeilingRef)
	if !found || authorityRecord.Kind != ContentAuthorityCeiling ||
		authorityRecord.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Channel Authority closure is unavailable", ErrChannelDispatchIntegrity,
		)
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(authorityRecord.CanonicalBytes)
	if err != nil || authority.TenantID != run.Manifest.TenantID ||
		!authority.AllowSend ||
		!containsChannelString(authority.AllowedWorkspaceIDs, run.Member.Workspace.ID) ||
		!containsChannelString(authority.AllowedEndpointIDs, endpointID) ||
		uint64(len([]byte(assistantText))) > uint64(authority.MaxMessageBytes) {
		return fmt.Errorf(
			"%w: channel.send is not granted by exact Authority",
			ErrChannelDispatchConflict,
		)
	}
	return nil
}

func requireNoExistingChannelAttempt(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	memberID string,
) error {
	var count int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts
		WHERE run_id=? AND member_id=? AND dispatch_kind='CHANNEL_SEND'
	`, runID, memberID).Scan(&count); err != nil {
		return fmt.Errorf("currentstore: count Channel Attempts: %w", err)
	}
	if count != 0 {
		return fmt.Errorf(
			"%w: Run already has a Channel send Attempt", ErrChannelDispatchConflict,
		)
	}
	return nil
}

func updateModelOutcomeRowsForChannel(
	ctx context.Context,
	connection *sql.Conn,
	current ModelDispatchRecord,
	merged ModelDispatchRecord,
	updatedAt int64,
) error {
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_dispatch_attempts
		SET state=?, provider_request_id=?, provider_receipt_ref=?, result_ref=?,
			error_classification=?, reconciliation_evidence_ref=?, unknown_reason=?,
			revision=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND state=? AND revision=?
	`,
		string(merged.Attempt.State),
		nullableModelString(merged.Attempt.ProviderRequestID),
		nullableModelString(merged.Attempt.ProviderReceiptRef),
		nullableModelString(merged.Attempt.ResultRef),
		nullableModelString(merged.Attempt.ErrorClassification),
		nullableModelString(merged.Attempt.ReconciliationEvidenceRef),
		nullableModelString(merged.Attempt.UnknownReason),
		int64(merged.Attempt.Revision),
		updatedAt,
		current.Attempt.AttemptID,
		current.Attempt.RunID,
		string(current.Attempt.State),
		int64(current.Attempt.Revision),
	)
	if err != nil {
		return fmt.Errorf(
			"%w: update source Model Attempt: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	if err := requireModelCASRow(attemptUpdate, "commit model Channel source"); err != nil {
		return err
	}
	usageUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_usage
		SET ledger_sequence=?, revision=?, input_tokens=?, cached_input_tokens=?,
			uncached_input_tokens=?, output_tokens=?, reasoning_tokens=?,
			estimated_cost=?, provider_reported_cost=?, reconciled_cost=?,
			reconciliation_status=?, raw_receipt_ref=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE attempt_id=? AND run_id=? AND revision=?
	`,
		nullableModelUint(merged.Usage.LedgerSequence),
		int64(merged.Usage.Revision),
		nullableModelUint(merged.Usage.Tokens.Input),
		nullableModelUint(merged.Usage.Tokens.CachedInput),
		nullableModelUint(merged.Usage.Tokens.UncachedInput),
		nullableModelUint(merged.Usage.Tokens.Output),
		nullableModelUint(merged.Usage.Tokens.Reasoning),
		nullableModelStringPointer(merged.Usage.EstimatedCost),
		nullableModelStringPointer(merged.Usage.ProviderReportedCost),
		nullableModelStringPointer(merged.Usage.ReconciledCost),
		merged.Usage.ReconciliationStatus,
		nullableModelString(merged.Usage.RawReceiptRef),
		current.Attempt.AttemptID,
		current.Attempt.RunID,
		int64(current.Usage.Revision),
	)
	if err != nil {
		return fmt.Errorf(
			"%w: update source Model Usage: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	return requireModelCASRow(usageUpdate, "commit model Channel Usage")
}

func appendChannelAssistantHistory(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	attempt ModelDispatchAttemptRecord,
	createdAt int64,
) error {
	sequence, err := incrementSQLiteUint(uint64(len(run.History)), "History sequence")
	if err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO history_entries(
			run_id, history_sequence, member_id, role, content_ref,
			content_digest, source_attempt_id, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(sequence),
		run.Member.MemberID,
		string(moduleapi.ModelRoleAssistant),
		attempt.ResultRef,
		attempt.ResultRef,
		attempt.AttemptID,
		createdAt,
	); err != nil {
		return fmt.Errorf(
			"%w: append assistant History: %v", ErrChannelDispatchIntegrity, err,
		)
	}
	return nil
}

func updateRunAndFrameForChannelBegin(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
	run RunForLoop,
	nextRunRevision uint64,
	nextFrameRevision uint64,
	nextEvent uint64,
	nextBudgetRef string,
	continuation []byte,
	channelAttemptID string,
	eventDigest string,
	createdAt int64,
) error {
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs SET revision=?, updated_at=?
		WHERE run_id=? AND revision=? AND state=? AND disposition IS NULL
	`,
		int64(nextRunRevision),
		createdAt,
		run.RunID,
		int64(run.RunRevision),
		corecontract.InitialRunState,
	)
	if err != nil {
		return fmt.Errorf("currentstore: update model-to-Channel Run: %w", err)
	}
	if err := requireModelCASRow(runUpdate, "commit model-to-Channel Run"); err != nil {
		return err
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, budget_state_ref=?, continuation=?,
			pending_attempt_id=NULL, pending_dispatch_attempt_id=?,
			waiting_reason=NULL, last_authoritative_event=?
		WHERE run_id=? AND frame_revision=? AND step=?
		  AND pending_attempt_id=? AND pending_dispatch_attempt_id IS NULL
		  AND last_authoritative_event=?
		  AND lease_owner=? AND lease_epoch=? AND lease_expiry>?
		  AND EXISTS(
			SELECT 1 FROM runs
			WHERE runs.run_id=loop_frames.run_id AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		corecontract.ChannelPendingLoopStep,
		nextBudgetRef,
		continuation,
		channelAttemptID,
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		corecontract.ModelPendingLoopStep,
		run.Frame.PendingAttemptID,
		int64(run.Frame.LastAuthoritativeEvent),
		lease.OwnerID,
		int64(lease.LeaseEpoch),
		createdAt,
		int64(nextRunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: update model-to-Channel Frame: %w", err)
	}
	if err := requireModelCASRow(frameUpdate, "commit model-to-Channel Frame"); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind, from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		channelDispatchPendingEvent,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return fmt.Errorf(
			"%w: append Channel pending event: %v",
			ErrChannelDispatchIntegrity,
			err,
		)
	}
	return nil
}

func reopenCommittedModelChannel(
	ctx context.Context,
	connection *sql.Conn,
	input CommitModelChannelAndBeginDispatchInput,
	prepared preparedModelDispatchOutcome,
	current ModelDispatchRecord,
) (CommitModelChannelAndBeginDispatchResult, error) {
	if input.ExpectedModelAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedModelAttemptRevision == math.MaxUint64 ||
			input.ExpectedModelAttemptRevision+1 != current.Attempt.Revision) {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: model-to-Channel re-entry revision differs",
			ErrChannelDispatchConflict,
		)
	}
	if err := requireSameModelOutcome(current, prepared); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	var channelAttemptID string
	if err := connection.QueryRowContext(ctx, `
		SELECT attempt_id FROM dispatch_attempts
		WHERE source_model_attempt_id=? AND dispatch_kind='CHANNEL_SEND'
	`, current.Attempt.AttemptID).Scan(&channelAttemptID); err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: committed source Model lacks its Channel Attempt",
			ErrChannelDispatchIntegrity,
		)
	}
	if channelAttemptID != input.DispatchAttemptID {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: re-entry Channel Attempt identity differs",
			ErrChannelDispatchConflict,
		)
	}
	channel, err := queryChannelDispatchRecord(ctx, connection, channelAttemptID)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	if !bytes.Equal(channel.Proposal.CanonicalBytes, input.ProposalCanonical) ||
		!channel.Attempt.Deadline.Equal(input.Deadline) {
		return CommitModelChannelAndBeginDispatchResult{}, fmt.Errorf(
			"%w: re-entry Proposal or deadline differs",
			ErrChannelDispatchConflict,
		)
	}
	currentLease, err := loadCurrentModelOutcomeLease(ctx, connection, input.Lease)
	if err != nil {
		return CommitModelChannelAndBeginDispatchResult{}, err
	}
	return CommitModelChannelAndBeginDispatchResult{
		Model:          cloneModelDispatchRecord(current),
		Channel:        cloneChannelDispatchRecord(channel),
		Lease:          currentLease,
		Applied:        false,
		GatewayAllowed: false,
	}, nil
}

func loadRunForChannelWrite(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
) (RunForLoop, error) {
	run, err := loadRunForLoop(ctx, connection, lease)
	if err == nil {
		return run, nil
	}
	if errors.Is(err, ErrRunLeaseConflict) || errors.Is(err, ErrRunLeaseUnavailable) {
		return RunForLoop{}, fmt.Errorf("%w: %w", ErrChannelDispatchConflict, err)
	}
	return RunForLoop{}, err
}
