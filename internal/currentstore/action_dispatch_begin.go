package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	firstModelLogicalStepID  = corecontract.FirstModelLogicalStepIDV1
	firstActionLogicalStepID = corecontract.FirstActionLogicalStepIDV1
	secondModelLogicalStepID = corecontract.SecondModelLogicalStepIDV1

	actionDispatchEventSchemaV1 = "action-dispatch-event/v1"
	actionDispatchPendingEvent  = "ACTION_DISPATCH_PENDING"
	actionDispatchTerminalEvent = "ACTION_DISPATCH_TERMINAL"
)

// CommitModelActionAndBeginDispatchInput carries the exact model invocation
// facts plus the Core-built Proposal. The Action logical step, member,
// Binding, definition, effect and result bound are all derived from
// the frozen Run and cannot be selected by the caller.
type CommitModelActionAndBeginDispatchInput struct {
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
}

// CommitModelActionAndBeginDispatch atomically closes model one as
// SUCCEEDED and creates the sole first-slice Action PENDING row. Executor
// permission exists only after COMMIT through the returned one-time permit.
func (store *Store) CommitModelActionAndBeginDispatch(
	ctx context.Context,
	input CommitModelActionAndBeginDispatchInput,
) (CommitModelActionAndBeginDispatchResult, error) {
	if ctx == nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidActionDispatch,
		)
	}
	input.OutputCanonical = bytes.Clone(input.OutputCanonical)
	input.UsageReceiptCanonical = bytes.Clone(input.UsageReceiptCanonical)
	input.ProposalCanonical = bytes.Clone(input.ProposalCanonical)
	if !validLeaseOpaqueID(input.ModelAttemptID) ||
		!validLeaseOpaqueID(input.DispatchAttemptID) ||
		input.InvocationID != input.ModelAttemptID ||
		input.ExpectedModelAttemptRevision >= math.MaxInt64 {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: invalid Model/Action Attempt identity or revision",
			ErrInvalidActionDispatch,
		)
	}
	if err := input.Provider.Validate(); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: invalid Model invocation Provider: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidActionDispatch,
			err,
		)
	}
	deadline, err := normalizeModelDeadline(input.Deadline)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Action deadline must be UTC at microsecond precision",
			ErrInvalidActionDispatch,
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
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if preparedModel.output.ActionRequest == nil ||
		preparedModel.output.AssistantText != "" {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: model output is not one legal Action request",
			ErrInvalidActionDispatch,
		)
	}
	if len(input.ProposalCanonical) == 0 {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Action Proposal is required",
			ErrInvalidActionDispatch,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: acquire model-to-Action connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: begin CommitModelActionAndBeginDispatch: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	currentModel, err := queryModelDispatchRecord(
		ctx,
		connection,
		input.ModelAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if currentModel.Attempt.RunID != input.Lease.RunID ||
		currentModel.Attempt.Binding.Provider != input.Provider {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model Attempt identity differs",
			ErrActionDispatchConflict,
		)
	}
	if currentModel.Attempt.State == corecontract.ModelAttemptSucceeded {
		result, err := reopenCommittedModelAction(
			ctx,
			connection,
			input,
			preparedModel,
			currentModel,
		)
		if err != nil {
			return CommitModelActionAndBeginDispatchResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModelV1, input.ModelAttemptID,
		); err != nil {
			return CommitModelActionAndBeginDispatchResult{}, err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceActionV1, input.DispatchAttemptID,
		); err != nil {
			return CommitModelActionAndBeginDispatchResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
				"currentstore: commit model-to-Action re-entry: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}
	if currentModel.Attempt.State != corecontract.ModelAttemptPending ||
		currentModel.Attempt.Revision != input.ExpectedModelAttemptRevision {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model Attempt is not the expected PENDING revision",
			ErrActionDispatchConflict,
		)
	}

	run, err := loadRunForActionWrite(ctx, connection, input.Lease)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if run.CancellationRequest != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Run %q",
			ErrRunCanceled,
			run.RunID,
		)
	}
	if run.Frame.Step != corecontract.ModelPendingLoopStep ||
		run.Frame.PendingAttemptID != currentModel.Attempt.AttemptID ||
		run.Frame.PendingDispatchAttemptID != "" ||
		currentModel.Attempt.SourceDispatchAttemptID != "" ||
		currentModel.Attempt.LogicalStepID != firstModelLogicalStepID ||
		!memberHasActionPort(run.Member) {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: source Model/Frame is not model-1 of an Action-enabled Run",
			ErrActionDispatchIntegrity,
		)
	}
	if err := validateAttemptAgainstOutcomeRun(currentModel, run); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	definition, binding, bindingCanonical, proposal, proposalDigest, err :=
		prepareFrozenActionBegin(run, currentModel, preparedModel, input)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := validateFrozenActionAuthority(
		run,
		binding,
		definition,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := checkCurrentActivation(
		ctx,
		connection,
		run.RunID,
		moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
		binding.Provider,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: deny-only current Activation check: %v",
			ErrActionDispatchConflict,
			err,
		)
	}
	if input.Deadline.UnixMicro() <= nowUnixMicro() ||
		input.Deadline.After(run.Manifest.Deadline) ||
		input.Deadline.After(input.Lease.ExpiresAt) {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Action deadline is expired or exceeds a frozen boundary",
			ErrActionDispatchConflict,
		)
	}
	if err := requireNoExistingActionAttempt(
		ctx,
		connection,
		run.RunID,
		run.Member.MemberID,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := requireNoCrossFamilyLogicalStep(
		ctx,
		connection,
		run.RunID,
		run.Member.MemberID,
		firstActionLogicalStepID,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	operationKey, err := actionLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		firstActionLogicalStepID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: Action operation key: %v",
			ErrActionDispatchIntegrity,
			err,
		)
	}
	ledgerHead, err := validateModelUsageLedgerHead(
		ctx,
		connection,
		run.RunID,
		run.Frame.UsageLedgerRef,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	mergedModel, err := mergeModelOutcomeFacts(currentModel, preparedModel)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if mergedModel.Usage.LedgerSequence == nil &&
		modelUsageHasReportedTokens(mergedModel.Usage) {
		if ledgerHead >= math.MaxInt64 {
			return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
				"%w: Usage ledger cannot advance",
				ErrActionDispatchIntegrity,
			)
		}
		sequence := ledgerHead + 1
		mergedModel.Usage.LedgerSequence = &sequence
	}
	nextBudgetRef := run.Frame.UsageLedgerRef
	if mergedModel.Usage.LedgerSequence != nil {
		nextBudgetRef, err = corecontract.NewUsageLedgerRefV1(
			run.RunID,
			*mergedModel.Usage.LedgerSequence,
		)
		if err != nil {
			return CommitModelActionAndBeginDispatchResult{}, err
		}
	}
	nextModelRevision, err := incrementSQLiteUint(
		currentModel.Attempt.Revision,
		"Model Attempt revision",
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	nextUsageRevision, err := incrementSQLiteUint(
		currentModel.Usage.Revision,
		"Usage revision",
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	nextRunRevision, err := incrementSQLiteUint(run.RunRevision, "Run revision")
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(run.Frame.Revision, "Frame revision")
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.ActionPendingLoopStep,
		corecontract.AttemptKindAction,
		firstActionLogicalStepID,
		input.DispatchAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	createdAt := nowUnixMicro()
	for _, content := range []*preparedAdmissionContent{
		preparedModel.resultContent,
		preparedModel.receiptContent,
		{
			Digest:         proposalDigest,
			Kind:           ContentActionProposal,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(input.ProposalCanonical),
		},
	} {
		if content == nil {
			continue
		}
		if err := putAdmissionContent(ctx, connection, *content, createdAt); err != nil {
			return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
				"%w: persist model-to-Action content: %v",
				ErrActionDispatchIntegrity,
				err,
			)
		}
	}
	mergedModel.Attempt.Revision = nextModelRevision
	mergedModel.Usage.Revision = nextUsageRevision
	mergedModel.Attempt.UpdatedAt = time.UnixMicro(createdAt).UTC()
	if err := updateModelOutcomeRowsForAction(
		ctx,
		connection,
		currentModel,
		mergedModel,
		createdAt,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO dispatch_attempts(
			attempt_id,
			dispatch_kind,
			logical_operation_key,
			run_id,
			tenant_id,
			workspace_id,
			member_id,
			logical_step_id,
			source_model_attempt_id,
			frame_revision,
			member_snapshot_digest,
			binding_index,
			binding_json,
			public_action_id,
			provider_action_id,
			definition_digest,
			proposal_ref,
			effect_class,
			max_result_bytes,
			deadline,
			usage_ledger_ref,
			state,
			revision,
			created_at,
			updated_at
		) VALUES(?, 'ACTION', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', 0, ?, ?)
	`,
		input.DispatchAttemptID,
		operationKey,
		run.RunID,
		run.Manifest.TenantID,
		run.Manifest.Workspace.ID,
		run.Member.MemberID,
		firstActionLogicalStepID,
		currentModel.Attempt.AttemptID,
		int64(run.Frame.Revision),
		run.Member.MemberSnapshotDigest,
		int64(definition.BindingIndex),
		bindingCanonical,
		definition.PublicActionID,
		definition.ProviderActionID,
		definition.DefinitionDigest,
		proposalDigest,
		string(definition.EffectClass),
		int64(definition.MaxResultBytes),
		input.Deadline.UnixMicro(),
		nextBudgetRef,
		createdAt,
		createdAt,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: insert Action PENDING: %v",
			ErrActionDispatchIntegrity,
			err,
		)
	}
	modelResource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceModelV1, input.ModelAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	actionResource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceActionV1, input.DispatchAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	eventUsage, err := modelUsageEventV1(mergedModel.Usage)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	eventCanonical, eventDigest, err := prepareActionDispatchEvent(actionDispatchEventV1{
		SchemaVersion: actionDispatchEventSchemaV1, RunID: run.RunID,
		AttemptID: input.DispatchAttemptID, LogicalStepID: firstActionLogicalStepID,
		LogicalOperationKey: operationKey, ProposalDigest: proposalDigest,
		State: ActionDispatchPending, TransitionOrigin: corecontract.DispatchTransitionBeginV1,
		SourceModelAttemptID: input.ModelAttemptID, SourceModelUsage: eventUsage,
		SourceModelSemanticDigest: modelResource.Snapshot.SemanticDigest,
		ResourceSemanticDigest:    actionResource.Snapshot.SemanticDigest,
	})
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload, MediaType: admissionJSONMediaType,
		CanonicalBytes: eventCanonical,
	}, createdAt); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := updateRunAndFrameForActionBegin(
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
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := appendActionBeginRunObservationV1(ctx, connection, run.RunID); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.ModelAttemptID, overviewTransitionModelActionSourceV1,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if err := appendActionResourceObservationV1(
		ctx, connection, input.DispatchAttemptID, overviewTransitionActionBeginV1,
	); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	storedModel, err := queryModelDispatchRecord(
		ctx,
		connection,
		input.ModelAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	storedAction, err := queryActionDispatchRecord(
		ctx,
		connection,
		input.DispatchAttemptID,
	)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"currentstore: commit model-to-Action: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	permit := &actionGatewayPermit{closure: actionGatewayPermitClosure{
		attemptID:            storedAction.Attempt.AttemptID,
		runID:                storedAction.Attempt.RunID,
		memberID:             storedAction.Attempt.MemberID,
		memberSnapshotDigest: storedAction.Attempt.MemberSnapshotDigest,
		bindingIndex:         storedAction.Attempt.BindingIndex,
		bindingCanonical:     bytes.Clone(storedAction.Attempt.BindingCanonical),
		proposalDigest:       storedAction.Attempt.ProposalRef,
		deadline:             storedAction.Attempt.Deadline,
		lease:                nextLease,
	}}
	_ = proposal
	_ = binding
	return CommitModelActionAndBeginDispatchResult{
		Model:          cloneModelDispatchRecord(storedModel),
		Action:         cloneActionDispatchRecord(storedAction),
		Lease:          nextLease,
		Applied:        true,
		GatewayAllowed: true,
		permit:         permit,
	}, nil
}

func prepareFrozenActionBegin(
	run RunForLoop,
	currentModel ModelDispatchRecord,
	preparedModel preparedModelDispatchOutcome,
	input CommitModelActionAndBeginDispatchInput,
) (
	corecontract.FrozenActionDefinitionV1,
	moduleapi.PortBinding,
	[]byte,
	corecontract.ActionProposalV1,
	string,
	error,
) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		currentModel.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", err
	}
	expectedActions, err := corecontract.ModelActionDefinitionsV1(run.Member.Actions)
	if err != nil || !sameModelActionDefinitions(request.Actions, expectedActions) {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", fmt.Errorf(
				"%w: model request Action projection differs from frozen member",
				ErrActionDispatchIntegrity,
			)
	}
	actionRequest := preparedModel.output.ActionRequest
	var definition *corecontract.FrozenActionDefinitionV1
	for index := range run.Member.Actions {
		candidate := &run.Member.Actions[index]
		if candidate.PublicActionID == actionRequest.ActionID {
			definition = candidate
			break
		}
	}
	if definition == nil {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", fmt.Errorf(
				"%w: model requested an unknown Action",
				ErrActionDispatchConflict,
			)
	}
	proposalDigest, err := ComputeContentDigest(
		ContentActionProposal,
		admissionJSONMediaType,
		input.ProposalCanonical,
	)
	if err != nil {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", err
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		input.ProposalCanonical,
		proposalDigest,
		run.Member.MemberSnapshotDigest,
		*definition,
	)
	if err != nil || proposal.PublicActionID != actionRequest.ActionID ||
		!bytes.Equal(proposal.CanonicalInput, actionRequest.CanonicalInput) {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", fmt.Errorf(
				"%w: Proposal does not close the exact model Action request",
				ErrActionDispatchIntegrity,
			)
	}
	plan, err := exactActionPlan(run.Member)
	if err != nil || uint64(definition.BindingIndex) >= uint64(len(plan.Bindings)) {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", fmt.Errorf(
				"%w: frozen Action BindingIndex",
				ErrActionDispatchIntegrity,
			)
	}
	binding := plan.Bindings[definition.BindingIndex]
	bindingCanonical, err := canonicalModelBinding(binding)
	if err != nil {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{}, nil,
			corecontract.ActionProposalV1{}, "", err
	}
	return cloneFrozenActionDefinition(*definition), binding,
		bindingCanonical, proposal, proposalDigest, nil
}

func sameModelActionDefinitions(
	left []moduleapi.ModelActionDefinitionV1,
	right []moduleapi.ModelActionDefinitionV1,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ActionID != right[index].ActionID ||
			left[index].Description != right[index].Description ||
			!bytes.Equal(left[index].InputSchema, right[index].InputSchema) {
			return false
		}
	}
	return true
}

func validateFrozenActionAuthority(
	run RunForLoop,
	binding moduleapi.PortBinding,
	definition corecontract.FrozenActionDefinitionV1,
) error {
	configRecord, found := run.FindContent(binding.ConfigRef)
	if !found || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Action Config closure is unavailable",
			ErrActionDispatchIntegrity,
		)
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: Action Config is not action-binding-config/v1",
			ErrActionDispatchIntegrity,
		)
	}
	var mapping *moduleapi.ActionBindingMappingV1
	for index := range config.Actions {
		candidate := &config.Actions[index]
		if candidate.PublicActionID == definition.PublicActionID {
			mapping = candidate
			break
		}
	}
	if mapping == nil || mapping.ProviderActionID != definition.ProviderActionID ||
		mapping.MaxResultBytes < definition.MaxResultBytes {
		return fmt.Errorf(
			"%w: Action definition is not closed by Binding Config",
			ErrActionDispatchIntegrity,
		)
	}
	localAllowed, err := moduleapi.ActionEffectAtMostV1(
		mapping.LocalEffectClass,
		definition.EffectClass,
	)
	if err != nil || !localAllowed {
		return fmt.Errorf(
			"%w: effective Action Effect undercuts local classification",
			ErrActionDispatchIntegrity,
		)
	}
	authorityRecord, found := run.FindContent(binding.AuthorityCeilingRef)
	if !found || authorityRecord.Kind != ContentAuthorityCeiling ||
		authorityRecord.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: Action Authority closure is unavailable",
			ErrActionDispatchIntegrity,
		)
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.TenantID != run.Manifest.TenantID ||
		!actionStringSetContains(authority.AllowedWorkspaceIDs, run.Member.Workspace.ID, true) ||
		!actionStringSetContains(authority.AllowedProviderActionIDs, definition.ProviderActionID, false) ||
		definition.MaxResultBytes > authority.MaxResultBytes {
		return fmt.Errorf(
			"%w: Action definition exceeds frozen Authority ceiling",
			ErrActionDispatchIntegrity,
		)
	}
	allowed, err := moduleapi.ActionEffectAtMostV1(
		definition.EffectClass,
		authority.MaxEffectClass,
	)
	if err != nil || !allowed {
		return fmt.Errorf(
			"%w: Action Effect exceeds frozen Authority ceiling",
			ErrActionDispatchIntegrity,
		)
	}
	return nil
}

func actionStringSetContains(values []string, want string, wildcard bool) bool {
	for _, value := range values {
		if value == want || (wildcard && value == "*") {
			return true
		}
	}
	return false
}

func requireNoExistingActionAttempt(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	memberID string,
) error {
	var count int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM dispatch_attempts
		WHERE run_id=? AND member_id=? AND dispatch_kind='ACTION'
	`, runID, memberID).Scan(&count); err != nil {
		return fmt.Errorf("currentstore: count Action Attempts: %w", err)
	}
	if count != 0 {
		return fmt.Errorf(
			"%w: first-slice Run already has an Action Attempt",
			ErrActionDispatchConflict,
		)
	}
	return nil
}

func requireNoCrossFamilyLogicalStep(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	memberID string,
	logicalStepID string,
) error {
	var count int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT (
			SELECT COUNT(*) FROM model_dispatch_attempts
			WHERE run_id=? AND member_id=? AND logical_step_id=?
		) + (
			SELECT COUNT(*) FROM dispatch_attempts
			WHERE run_id=? AND member_id=? AND logical_step_id=?
		)
	`, runID, memberID, logicalStepID, runID, memberID, logicalStepID).Scan(&count); err != nil {
		return fmt.Errorf("currentstore: check cross-family logical step: %w", err)
	}
	if count != 0 {
		return fmt.Errorf(
			"%w: logical step already exists in a dispatch family",
			ErrActionDispatchConflict,
		)
	}
	return nil
}

func updateModelOutcomeRowsForAction(
	ctx context.Context,
	connection *sql.Conn,
	current ModelDispatchRecord,
	merged ModelDispatchRecord,
	updatedAt int64,
) error {
	attemptUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_dispatch_attempts
		SET
			state=?,
			provider_request_id=?,
			provider_receipt_ref=?,
			result_ref=?,
			error_classification=?,
			reconciliation_evidence_ref=?,
			unknown_reason=?,
			revision=?,
			updated_at=?,
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
		return fmt.Errorf("%w: update source Model Attempt: %v", ErrActionDispatchIntegrity, err)
	}
	if err := requireModelCASRow(attemptUpdate, "commit model Action source"); err != nil {
		return err
	}
	usageUpdate, err := connection.ExecContext(ctx, `
		UPDATE model_usage
		SET
			ledger_sequence=?, revision=?, input_tokens=?, cached_input_tokens=?,
			uncached_input_tokens=?, output_tokens=?, reasoning_tokens=?,
			usage_status=?, raw_receipt_ref=?,
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
		merged.Usage.UsageStatus,
		nullableModelString(merged.Usage.RawReceiptRef),
		current.Attempt.AttemptID,
		current.Attempt.RunID,
		int64(current.Usage.Revision),
	)
	if err != nil {
		return fmt.Errorf("%w: update source Model Usage: %v", ErrActionDispatchIntegrity, err)
	}
	return requireModelCASRow(usageUpdate, "commit model Action Usage")
}

func updateRunAndFrameForActionBegin(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
	run RunForLoop,
	nextRunRevision uint64,
	nextFrameRevision uint64,
	nextEvent uint64,
	nextBudgetRef string,
	continuation []byte,
	actionAttemptID string,
	eventDigest string,
	createdAt int64,
) error {
	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET revision=?, updated_at=?
		WHERE run_id=? AND revision=? AND state=? AND disposition IS NULL
	`,
		int64(nextRunRevision), createdAt, run.RunID, int64(run.RunRevision),
		corecontract.InitialRunState,
	)
	if err != nil {
		return fmt.Errorf("currentstore: update model-to-Action Run: %w", err)
	}
	if err := requireModelCASRow(runUpdate, "commit model-to-Action Run"); err != nil {
		return err
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=?, step=?, usage_ledger_ref=?, continuation=?,
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
		corecontract.ActionPendingLoopStep,
		nextBudgetRef,
		continuation,
		actionAttemptID,
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
		return fmt.Errorf("currentstore: update model-to-Action Frame: %w", err)
	}
	if err := requireModelCASRow(frameUpdate, "commit model-to-Action Frame"); err != nil {
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
		actionDispatchPendingEvent,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return fmt.Errorf("%w: append Action pending event: %v", ErrActionDispatchIntegrity, err)
	}
	return nil
}

func reopenCommittedModelAction(
	ctx context.Context,
	connection *sql.Conn,
	input CommitModelActionAndBeginDispatchInput,
	prepared preparedModelDispatchOutcome,
	current ModelDispatchRecord,
) (CommitModelActionAndBeginDispatchResult, error) {
	if input.ExpectedModelAttemptRevision != current.Attempt.Revision &&
		(input.ExpectedModelAttemptRevision == math.MaxUint64 ||
			input.ExpectedModelAttemptRevision+1 != current.Attempt.Revision) {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: model-to-Action re-entry revision differs",
			ErrActionDispatchConflict,
		)
	}
	if err := requireSameModelOutcome(current, prepared); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	var actionAttemptID string
	if err := connection.QueryRowContext(ctx, `
		SELECT attempt_id
		FROM dispatch_attempts
		WHERE source_model_attempt_id=? AND dispatch_kind='ACTION'
	`, current.Attempt.AttemptID).Scan(&actionAttemptID); err != nil {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: committed source Model lacks its Action Attempt",
			ErrActionDispatchIntegrity,
		)
	}
	if actionAttemptID != input.DispatchAttemptID {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: re-entry Action Attempt identity differs",
			ErrActionDispatchConflict,
		)
	}
	action, err := queryActionDispatchRecord(ctx, connection, actionAttemptID)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	if !bytes.Equal(action.Proposal.CanonicalBytes, input.ProposalCanonical) ||
		!action.Attempt.Deadline.Equal(input.Deadline) {
		return CommitModelActionAndBeginDispatchResult{}, fmt.Errorf(
			"%w: re-entry Proposal or deadline differs",
			ErrActionDispatchConflict,
		)
	}
	currentLease, err := loadCurrentModelOutcomeLease(ctx, connection, input.Lease)
	if err != nil {
		return CommitModelActionAndBeginDispatchResult{}, err
	}
	return CommitModelActionAndBeginDispatchResult{
		Model:          cloneModelDispatchRecord(current),
		Action:         cloneActionDispatchRecord(action),
		Lease:          currentLease,
		Applied:        false,
		GatewayAllowed: false,
	}, nil
}

func loadRunForActionWrite(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
) (RunForLoop, error) {
	run, err := loadRunForLoop(ctx, connection, lease)
	if err == nil {
		return run, nil
	}
	if errors.Is(err, ErrRunLeaseConflict) ||
		errors.Is(err, ErrRunLeaseUnavailable) {
		return RunForLoop{}, fmt.Errorf("%w: %w", ErrActionDispatchConflict, err)
	}
	return RunForLoop{}, err
}

type actionDispatchEventV1 struct {
	SchemaVersion             string                                  `json:"schema_version"`
	RunID                     string                                  `json:"run_id"`
	AttemptID                 string                                  `json:"attempt_id"`
	LogicalStepID             string                                  `json:"logical_step_id"`
	LogicalOperationKey       string                                  `json:"logical_operation_key"`
	ProposalDigest            string                                  `json:"proposal_digest"`
	State                     ActionDispatchState                     `json:"state"`
	ResultDigest              string                                  `json:"result_digest,omitempty"`
	TransitionOrigin          corecontract.DispatchTransitionOriginV1 `json:"transition_origin"`
	SourceModelAttemptID      string                                  `json:"source_model_attempt_id,omitempty"`
	SourceModelUsage          *corecontract.ModelUsageEventV1         `json:"source_model_usage,omitempty"`
	ResourceSemanticDigest    string                                  `json:"resource_semantic_digest"`
	SourceModelSemanticDigest string                                  `json:"source_model_semantic_digest,omitempty"`
}

func prepareActionDispatchEvent(
	event actionDispatchEventV1,
) ([]byte, string, error) {
	if event.SchemaVersion != actionDispatchEventSchemaV1 ||
		!validLeaseOpaqueID(event.RunID) ||
		!validLeaseOpaqueID(event.AttemptID) ||
		!validLeaseOpaqueID(event.LogicalStepID) ||
		!moduleapi.ValidSHA256(event.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(event.ProposalDigest) ||
		!moduleapi.ValidSHA256(event.ResourceSemanticDigest) ||
		event.State.validate() != nil || event.TransitionOrigin.Validate() != nil {
		return nil, "", fmt.Errorf(
			"%w: invalid Action dispatch event",
			ErrActionDispatchIntegrity,
		)
	}
	if event.State == ActionDispatchPending {
		if event.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			!validLeaseOpaqueID(event.SourceModelAttemptID) || event.SourceModelUsage == nil ||
			event.SourceModelUsage.Validate() != nil ||
			!moduleapi.ValidSHA256(event.SourceModelSemanticDigest) {
			return nil, "", fmt.Errorf("%w: invalid Action begin provenance", ErrActionDispatchIntegrity)
		}
	} else if event.TransitionOrigin == corecontract.DispatchTransitionBeginV1 ||
		event.SourceModelAttemptID != "" || event.SourceModelUsage != nil ||
		event.SourceModelSemanticDigest != "" {
		return nil, "", fmt.Errorf("%w: invalid Action terminal provenance", ErrActionDispatchIntegrity)
	}
	if event.State == ActionDispatchSucceeded {
		if !moduleapi.ValidSHA256(event.ResultDigest) {
			return nil, "", fmt.Errorf(
				"%w: successful Action event requires result",
				ErrActionDispatchIntegrity,
			)
		}
	} else if event.ResultDigest != "" {
		return nil, "", fmt.Errorf(
			"%w: non-success Action event cannot carry result",
			ErrActionDispatchIntegrity,
		)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}
	encoded, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return nil, "", err
	}
	digest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		encoded,
	)
	if err != nil {
		return nil, "", err
	}
	return bytes.Clone(encoded), digest, nil
}
