package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type secondModelDispatchSource struct {
	action       ActionDispatchRecord
	definition   corecontract.FrozenActionDefinitionV1
	sourceModel  ModelDispatchRecord
	expectedWire []byte
}

// commitSecondModelDispatch is the only model-2 admission path. The caller
// supplies the finished request bytes, but Store derives the source Action
// identity and reconstructs those bytes solely from persisted closure facts.
func commitSecondModelDispatch(
	ctx context.Context,
	connection *sql.Conn,
	input BeginModelDispatchInput,
	request moduleapi.ModelGenerateRequestV1,
	requestCanonicalBytes []byte,
	requestDigest string,
	deadline time.Time,
	run RunForLoop,
) (BeginModelDispatchResult, error) {
	if input.LogicalStepID != secondModelLogicalStepID ||
		input.ContextCompilationCanonical != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: MODEL_READY_AFTER_ACTION only admits model-2 without a new context compilation",
			ErrInvalidModelDispatch,
		)
	}
	source, err := deriveSecondModelDispatchSource(
		ctx,
		connection,
		run,
		request,
		requestCanonicalBytes,
		requestDigest,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if deadline.After(run.Manifest.Deadline) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: model-2 deadline exceeds frozen Run deadline",
			ErrInvalidModelDispatch,
		)
	}
	if err := requireNoCrossFamilyLogicalStep(
		ctx,
		connection,
		run.RunID,
		run.Member.MemberID,
		secondModelLogicalStepID,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	binding, err := exactModelBinding(run.Member)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	bindingCanonical, err := canonicalModelBinding(binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	config, err := frozenModelBindingConfig(run, binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	modelAuthorityCanonical, err := frozenModelBindingAuthority(run, binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if !bytes.Equal(request.Parameters, config.Parameters) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: model-2 parameters differ from model-1 frozen config",
			ErrInvalidModelDispatch,
		)
	}
	if _, err := validateModelParametersForRun(run, []byte(request.Parameters)); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: model-2 parameters: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	logicalKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		secondModelLogicalStepID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: model-2 logical operation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	if deadline.UnixMicro() <= nowUnixMicro() {
		return commitModelDispatchBeforeNetwork(
			ctx,
			connection,
			input,
			deadline,
			"",
			requestDigest,
			run,
			bindingCanonical,
			request.Parameters,
			config.Provider,
			config.Model,
			logicalKey,
			source.action.Attempt.AttemptID,
			corecontract.ModelReadyAfterActionLoopStep,
			nil,
			modelDeadlineExpiredBeforeDispatchClassification,
			corecontract.DispatchTransitionExpiredBeforeNetworkV1,
			overviewTransitionModelExpiredV1,
		)
	}
	if err := checkCurrentActivation(
		ctx,
		connection,
		run.RunID,
		moduleapi.PortRef{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV2,
		},
		binding.Provider,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	continuation, err := corecontract.NewLoopContinuationV1(
		corecontract.ModelPendingLoopStep,
		secondModelLogicalStepID,
		input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: model-2 continuation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	createdAt := nowUnixMicro()
	for _, content := range []preparedAdmissionContent{{
		Digest:         requestDigest,
		Kind:           ContentModelRequest,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(input.RequestCanonical),
	}} {
		if err := putAdmissionContent(ctx, connection, content, createdAt); err != nil {
			return BeginModelDispatchResult{}, err
		}
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO model_dispatch_attempts(
			attempt_id, logical_operation_key, run_id, tenant_id, workspace_id, member_id,
			logical_step_id, frame_revision, member_snapshot_digest,
			binding_json, context_compilation_ref, request_ref,
			request_digest, provider, model, parameters_json, deadline,
			usage_ledger_ref,
			source_dispatch_attempt_id, state, provider_request_id,
			provider_receipt_ref, result_ref, error_classification,
			reconciliation_evidence_ref, unknown_reason, revision,
			created_at, updated_at
		) VALUES(
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?,
			'PENDING', NULL, NULL, NULL, NULL, NULL, NULL, 0, ?, ?
		)
	`,
		input.AttemptID,
		logicalKey,
		run.RunID,
		run.Manifest.TenantID,
		run.Manifest.Workspace.ID,
		run.Member.MemberID,
		secondModelLogicalStepID,
		int64(run.Frame.Revision),
		run.Member.MemberSnapshotDigest,
		bindingCanonical,
		requestDigest,
		requestDigest,
		config.Provider,
		config.Model,
		[]byte(request.Parameters),
		deadline.UnixMicro(),
		run.Frame.UsageLedgerRef,
		source.action.Attempt.AttemptID,
		createdAt,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert model-2 PENDING Attempt: %v",
			ErrModelDispatchConflict,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO model_usage(
			attempt_id, run_id, ledger_sequence, revision,
			input_tokens, cached_input_tokens, uncached_input_tokens,
			output_tokens, reasoning_tokens,
			usage_status, raw_receipt_ref
		) VALUES(
			?, ?, NULL, 0, NULL, NULL, NULL, NULL, NULL,
			'PENDING', NULL
		)
	`, input.AttemptID, run.RunID); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert model-2 Usage placeholder: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	resource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceModelV1, input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	_, eventCanonical, err := corecontract.NewModelDispatchEventV1(corecontract.ModelDispatchEventV1{
		SchemaVersion: corecontract.ModelDispatchEventSchemaVersionV1,
		RunID:         run.RunID, AttemptID: input.AttemptID, LogicalStepID: secondModelLogicalStepID,
		LogicalOperationKey: logicalKey, RequestDigest: requestDigest,
		State:                  corecontract.ModelAttemptPending,
		TransitionOrigin:       corecontract.DispatchTransitionBeginV1,
		ResourceSemanticDigest: resource.Snapshot.SemanticDigest,
	})
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf("%w: model-2 pending event: %v", ErrInvalidModelDispatch, err)
	}
	eventDigest, err := ComputeContentDigest(ContentRunEventPayload, admissionJSONMediaType, eventCanonical)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload, MediaType: admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(eventCanonical),
	}, createdAt); err != nil {
		return BeginModelDispatchResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		run.Frame.Revision,
		"Frame revision",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET frame_revision=?, step=?, continuation=?, pending_attempt_id=?,
			pending_dispatch_attempt_id=NULL, waiting_reason=NULL,
			last_authoritative_event=?
		WHERE run_id=? AND frame_revision=? AND step=?
		  AND pending_attempt_id IS NULL
		  AND pending_dispatch_attempt_id IS NULL
		  AND waiting_reason IS NULL
		  AND last_authoritative_event=?
		  AND lease_owner=? AND lease_epoch=? AND lease_expiry>?
		  AND EXISTS(
			SELECT 1 FROM runs
			WHERE runs.run_id=loop_frames.run_id AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		corecontract.ModelPendingLoopStep,
		continuation,
		input.AttemptID,
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		corecontract.ModelReadyAfterActionLoopStep,
		int64(run.Frame.LastAuthoritativeEvent),
		input.Lease.OwnerID,
		int64(input.Lease.LeaseEpoch),
		createdAt,
		int64(run.RunRevision),
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: advance model-2 pending Frame: %w",
			err,
		)
	}
	if err := requireModelCASRow(frameUpdate, "begin model-2 dispatch"); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind, from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		corecontract.ModelDispatchPendingEventKind,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: append model-2 pending RunEvent: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := appendModelBeginRunObservationV1(ctx, connection, run.RunID); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.AttemptID, overviewTransitionModelBeginV1,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	stored, err := queryModelDispatchAttempt(ctx, connection, input.AttemptID)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if stored.SourceDispatchAttemptID != source.action.Attempt.AttemptID ||
		stored.ContextCompilation != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: persisted model-2 source or compilation projection",
			ErrModelDispatchIntegrity,
		)
	}
	_, modelConfigCanonical, err := moduleapi.NewModelBindingConfigV2(config)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: rebuild frozen model-2 Binding config: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	stored.ModelConfigCanonical = bytes.Clone(modelConfigCanonical)
	stored.ModelAuthorityCanonical = bytes.Clone(modelAuthorityCanonical)
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: commit model-2 BeginModelDispatch: %w",
			err,
		)
	}
	nextLease := input.Lease
	nextLease.FrameRevision = nextFrameRevision
	result := BeginModelDispatchResult{
		Attempt:       cloneModelDispatchAttempt(stored),
		Lease:         nextLease,
		Created:       true,
		InvokeAllowed: true,
	}
	result.permit = newModelInvocationPermit(result.Attempt, result.Lease)
	_ = source.expectedWire
	return result, nil
}

func deriveSecondModelDispatchSource(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	requestCanonicalBytes []byte,
	requestDigest string,
) (secondModelDispatchSource, error) {
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil || continuation.State != corecontract.ModelReadyAfterActionLoopStep ||
		continuation.AttemptKind != corecontract.AttemptKindAction ||
		continuation.LogicalStepID != firstActionLogicalStepID {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: MODEL_READY_AFTER_ACTION continuation does not name action-1",
			ErrModelDispatchIntegrity,
		)
	}
	action, err := queryActionDispatchRecord(
		ctx,
		connection,
		continuation.AttemptID,
	)
	if err != nil {
		return secondModelDispatchSource{}, err
	}
	if action.Attempt.RunID != run.RunID ||
		action.Attempt.MemberID != run.Member.MemberID ||
		action.Attempt.MemberSnapshotDigest != run.Member.MemberSnapshotDigest ||
		action.Attempt.State != ActionDispatchSucceeded ||
		action.Attempt.LogicalStepID != firstActionLogicalStepID ||
		action.Result == nil {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-2 source Action is not the exact successful action-1",
			ErrModelDispatchIntegrity,
		)
	}
	_, definition, err := loadActionAttemptFrozenDefinition(
		ctx,
		connection,
		action.Attempt,
	)
	if err != nil {
		return secondModelDispatchSource{}, err
	}
	actionResult, err := corecontract.RestoreActionResultV1(
		action.Result.CanonicalBytes,
		action.Result.Digest,
		definition,
	)
	if err != nil || actionResult.Status != corecontract.ActionResultAvailable {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-2 requires one exact AVAILABLE Action result",
			ErrModelDispatchIntegrity,
		)
	}
	sourceModel, err := queryModelDispatchRecord(
		ctx,
		connection,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil {
		return secondModelDispatchSource{}, err
	}
	if sourceModel.Attempt.RunID != run.RunID ||
		sourceModel.Attempt.MemberID != run.Member.MemberID ||
		sourceModel.Attempt.LogicalStepID != firstModelLogicalStepID ||
		sourceModel.Attempt.SourceDispatchAttemptID != "" ||
		sourceModel.Attempt.State != corecontract.ModelAttemptSucceeded ||
		sourceModel.Attempt.ResultRef == "" ||
		sourceModel.Attempt.ContextCompilation == nil {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-2 source model-1 closure is incomplete",
			ErrModelDispatchIntegrity,
		)
	}
	modelOneRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		sourceModel.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 request wire: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	expectedActions, err := corecontract.ModelActionDefinitionsV1(run.Member.Actions)
	if err != nil || !sameModelActionDefinitions(modelOneRequest.Actions, expectedActions) {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 Action projection differs from frozen member",
			ErrModelDispatchIntegrity,
		)
	}
	modelOneResult, err := queryContent(
		ctx,
		connection,
		sourceModel.Attempt.ResultRef,
	)
	if err != nil || modelOneResult.Kind != ContentModelResult {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 result content is unavailable",
			ErrModelDispatchIntegrity,
		)
	}
	modelOneOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOneResult.CanonicalBytes,
	)
	if err != nil || modelOneOutput.ActionRequest == nil ||
		modelOneOutput.ActionRequest.ActionID != definition.PublicActionID {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 result is not the source Action request",
			ErrModelDispatchIntegrity,
		)
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		action.Proposal.CanonicalBytes,
		action.Proposal.Digest,
		run.Member.MemberSnapshotDigest,
		definition,
	)
	if err != nil || !bytes.Equal(
		proposal.CanonicalInput,
		modelOneOutput.ActionRequest.CanonicalInput,
	) {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: Action Proposal differs from model-1 output",
			ErrModelDispatchIntegrity,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		sourceModel.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || compilation.ActionResultReservation == nil {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 lacks a frozen Action result reservation",
			ErrModelDispatchIntegrity,
		)
	}
	expectedReservation, err := corecontract.NewActionResultReservationV1(
		run.Member.Actions,
	)
	if err != nil || *compilation.ActionResultReservation != expectedReservation {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-1 Action result reservation differs from frozen definitions",
			ErrModelDispatchIntegrity,
		)
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		actionResult,
		definition,
	)
	if err != nil {
		return secondModelDispatchSource{}, err
	}
	envelopeWire, err := json.Marshal(envelope)
	if err != nil || uint64(len(envelope.Content)) > expectedReservation.MaxEnvelopeBytes ||
		uint64(len(envelopeWire)+1) > expectedReservation.EstimatedTokens {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: actual Action result exceeds the frozen reservation",
			ErrModelDispatchIntegrity,
		)
	}
	expectedRequest := modelOneRequest
	expectedRequest.Messages = append(
		append([]moduleapi.ModelMessageV1(nil), modelOneRequest.Messages...),
		envelope,
	)
	_, expectedWire, err := moduleapi.NewModelGenerateRequestV1(expectedRequest)
	computedDigest, digestErr := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		requestCanonicalBytes,
	)
	if err != nil || digestErr != nil || computedDigest != requestDigest ||
		!bytes.Equal(expectedWire, requestCanonicalBytes) ||
		!bytes.Equal(expectedWire, mustCanonicalModelRequest(request)) {
		return secondModelDispatchSource{}, fmt.Errorf(
			"%w: model-2 request is not model-1 plus the sole Action result envelope",
			ErrInvalidModelDispatch,
		)
	}
	return secondModelDispatchSource{
		action:       cloneActionDispatchRecord(action),
		definition:   cloneFrozenActionDefinition(definition),
		sourceModel:  cloneModelDispatchRecord(sourceModel),
		expectedWire: bytes.Clone(expectedWire),
	}, nil
}

// mustCanonicalModelRequest rebuilds the unique request encoding. An invalid
// value yields nil so closure comparison fails closed.
func mustCanonicalModelRequest(request moduleapi.ModelGenerateRequestV1) []byte {
	_, canonical, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		return nil
	}
	return canonical
}

func reopenSecondModelDispatch(
	ctx context.Context,
	connection *sql.Conn,
	input BeginModelDispatchInput,
	existing ModelDispatchAttemptRecord,
	request moduleapi.ModelGenerateRequestV1,
	requestDigest string,
	deadline time.Time,
) (BeginModelDispatchResult, error) {
	if input.LogicalStepID != secondModelLogicalStepID ||
		input.ContextCompilationCanonical != nil ||
		existing.ContextCompilation != nil ||
		existing.LogicalStepID != secondModelLogicalStepID ||
		existing.SourceDispatchAttemptID == "" {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: existing model-2 shape differs",
			ErrModelDispatchConflict,
		)
	}
	currentLease, run, err := loadExactRetryRun(
		ctx,
		connection,
		input.Lease,
		existing,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if existing.State == corecontract.ModelAttemptPending {
		if run.Frame.Step != corecontract.ModelPendingLoopStep ||
			run.Frame.PendingAttemptID != existing.AttemptID ||
			run.Frame.PendingDispatchAttemptID != "" {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing model-2 PENDING is not the Frame identity",
				ErrModelDispatchIntegrity,
			)
		}
		if err := verifyBeginPendingUsagePlaceholder(ctx, connection, existing); err != nil {
			return BeginModelDispatchResult{}, err
		}
	}
	action, err := queryActionDispatchRecord(
		ctx,
		connection,
		existing.SourceDispatchAttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if action.Attempt.State != ActionDispatchSucceeded || action.Result == nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: existing model-2 source Action is unavailable",
			ErrModelDispatchIntegrity,
		)
	}
	_, definition, err := loadActionAttemptFrozenDefinition(
		ctx,
		connection,
		action.Attempt,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	result, err := corecontract.RestoreActionResultV1(
		action.Result.CanonicalBytes,
		action.Result.Digest,
		definition,
	)
	if err != nil || result.Status != corecontract.ActionResultAvailable {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: existing model-2 source result is not AVAILABLE",
			ErrModelDispatchIntegrity,
		)
	}
	sourceModel, err := queryModelDispatchRecord(
		ctx,
		connection,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	modelOneRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		sourceModel.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(result, definition)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	expected := modelOneRequest
	expected.Messages = append(
		append([]moduleapi.ModelMessageV1(nil), modelOneRequest.Messages...),
		envelope,
	)
	_, expectedWire, err := moduleapi.NewModelGenerateRequestV1(expected)
	if err != nil || !bytes.Equal(expectedWire, mustCanonicalModelRequest(request)) ||
		existing.Request.Digest != requestDigest ||
		!bytes.Equal(existing.Request.CanonicalBytes, expectedWire) ||
		!existing.Deadline.Equal(deadline) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: existing model-2 request closure differs",
			ErrModelDispatchConflict,
		)
	}
	binding, err := exactModelBinding(run.Member)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	bindingCanonical, err := canonicalModelBinding(binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	config, err := frozenModelBindingConfig(run, binding)
	if err != nil || existing.MemberID != run.Member.MemberID ||
		existing.MemberSnapshotDigest != run.Member.MemberSnapshotDigest ||
		!bytes.Equal(existing.BindingCanonical, bindingCanonical) ||
		existing.Provider != config.Provider || existing.Model != config.Model ||
		!bytes.Equal(existing.ParametersCanonical, config.Parameters) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: existing model-2 is not closed by the frozen model Binding",
			ErrModelDispatchIntegrity,
		)
	}
	return BeginModelDispatchResult{
		Attempt:       cloneModelDispatchAttempt(existing),
		Lease:         currentLease,
		Created:       false,
		InvokeAllowed: false,
	}, nil
}

// validatePersistedSecondModelRequestClosure is the read-side proof used by
// terminal recovery. It reconstructs model-2 without invoking any compiler,
// retriever, Memory reader, Describe, or Prepare path.
func validatePersistedSecondModelRequestClosure(
	ctx context.Context,
	connection readQueryerV1,
	final ModelDispatchAttemptRecord,
	action ActionDispatchRecord,
) error {
	if final.SourceDispatchAttemptID != action.Attempt.AttemptID ||
		final.RunID != action.Attempt.RunID ||
		final.MemberID != action.Attempt.MemberID ||
		final.MemberSnapshotDigest != action.Attempt.MemberSnapshotDigest ||
		final.LogicalStepID != secondModelLogicalStepID ||
		final.ContextCompilation != nil ||
		action.Attempt.State != ActionDispatchSucceeded ||
		action.Result == nil {
		return loopReadIntegrity(
			"persisted model-2 identities",
			ErrAdmissionIntegrity,
		)
	}
	member, definition, err := loadActionAttemptFrozenDefinition(
		ctx,
		connection,
		action.Attempt,
	)
	if err != nil {
		return err
	}
	actionResult, err := corecontract.RestoreActionResultV1(
		action.Result.CanonicalBytes,
		action.Result.Digest,
		definition,
	)
	if err != nil || actionResult.Status != corecontract.ActionResultAvailable {
		return loopReadIntegrity("persisted model-2 Action result", err)
	}
	source, err := queryModelDispatchRecord(
		ctx,
		connection,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil {
		return err
	}
	if source.Attempt.RunID != final.RunID ||
		source.Attempt.MemberID != final.MemberID ||
		source.Attempt.MemberSnapshotDigest != final.MemberSnapshotDigest ||
		source.Attempt.LogicalStepID != firstModelLogicalStepID ||
		source.Attempt.SourceDispatchAttemptID != "" ||
		source.Attempt.State != corecontract.ModelAttemptSucceeded ||
		source.Attempt.ResultRef == "" ||
		source.Attempt.ContextCompilation == nil {
		return loopReadIntegrity(
			"persisted model-2 source model-1",
			ErrAdmissionIntegrity,
		)
	}
	modelOneRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		source.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		return loopReadIntegrity("persisted model-1 request", err)
	}
	expectedActions, err := corecontract.ModelActionDefinitionsV1(member.Actions)
	if err != nil || !sameModelActionDefinitions(modelOneRequest.Actions, expectedActions) {
		return loopReadIntegrity(
			"persisted model-1 Action projection",
			ErrAdmissionIntegrity,
		)
	}
	modelOneResult, err := queryContent(ctx, connection, source.Attempt.ResultRef)
	if err != nil || modelOneResult.Kind != ContentModelResult {
		return loopReadIntegrity("persisted model-1 result", err)
	}
	modelOneOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOneResult.CanonicalBytes,
	)
	if err != nil || modelOneOutput.ActionRequest == nil ||
		modelOneOutput.ActionRequest.ActionID != definition.PublicActionID {
		return loopReadIntegrity("persisted model-1 Action output", err)
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		action.Proposal.CanonicalBytes,
		action.Proposal.Digest,
		member.MemberSnapshotDigest,
		definition,
	)
	if err != nil || !bytes.Equal(
		proposal.CanonicalInput,
		modelOneOutput.ActionRequest.CanonicalInput,
	) {
		return loopReadIntegrity("persisted Action Proposal source", err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		source.Attempt.ContextCompilation.CanonicalBytes,
	)
	expectedReservation, reservationErr := corecontract.NewActionResultReservationV1(
		member.Actions,
	)
	if err != nil || reservationErr != nil ||
		compilation.ActionResultReservation == nil ||
		*compilation.ActionResultReservation != expectedReservation {
		return loopReadIntegrity(
			"persisted model-1 Action reservation",
			ErrAdmissionIntegrity,
		)
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		actionResult,
		definition,
	)
	if err != nil {
		return loopReadIntegrity("persisted Action result envelope", err)
	}
	expected := modelOneRequest
	expected.Messages = append(
		append([]moduleapi.ModelMessageV1(nil), modelOneRequest.Messages...),
		envelope,
	)
	_, expectedWire, err := moduleapi.NewModelGenerateRequestV1(expected)
	if err != nil || !bytes.Equal(expectedWire, final.Request.CanonicalBytes) ||
		!bytes.Equal(final.ParametersCanonical, modelOneRequest.Parameters) {
		return loopReadIntegrity(
			"persisted model-2 request bytes",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}
