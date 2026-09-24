package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type inspectedActionAttempt struct {
	attemptID           string
	runID               string
	memberID            string
	logicalStepID       string
	sourceModelID       string
	memberDigest        string
	publicActionID      string
	providerActionID    string
	definitionDigest    string
	proposalRef         string
	resultRef           string
	state               string
	errorClassification string
	unknownReason       string
	externalOperationID string
	providerReceiptRef  string
	evidenceRef         string
	resultStatus        corecontract.ActionResultStatusV1
	member              corecontract.MemberExecutionSnapshot
	definition          corecontract.FrozenActionDefinitionV1
	proposal            corecontract.ActionProposalV1
	result              *corecontract.ActionResultV1
}

func inspectActionSemanticClosure(ctx context.Context, database semanticQueryer) error {
	rows, err := database.QueryContext(ctx, `
		SELECT attempt_id FROM dispatch_attempts
		WHERE dispatch_kind='ACTION'
		ORDER BY run_id, created_at, attempt_id
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read Action Attempt IDs: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("currentbackup: scan Action Attempt ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("currentbackup: iterate Action Attempt IDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("currentbackup: close Action Attempt IDs: %w", err)
	}
	for _, id := range ids {
		if err := inspectOneActionAttempt(ctx, database, id); err != nil {
			return err
		}
	}
	return inspectLegalModelActionRejections(ctx, database)
}

func inspectOneActionAttempt(
	ctx context.Context,
	database semanticQueryer,
	attemptID string,
) error {
	var (
		record              inspectedActionAttempt
		logicalOperationKey string
		bindingIndex        int64
		bindingCanonical    []byte
		effectClass         string
		maxResultBytes      int64
		usageLedgerRef      string
		memberCanonical     []byte
		storedMemberDigest  string
		proposalKind        string
		proposalMediaType   string
		proposalCanonical   []byte
		resultKind          sql.NullString
		resultMediaType     sql.NullString
		resultCanonical     []byte
		externalOperationID sql.NullString
		providerReceiptRef  sql.NullString
		resultRef           sql.NullString
		errorClassification sql.NullString
		evidenceRef         sql.NullString
		unknownReason       sql.NullString
	)
	err := database.QueryRowContext(ctx, `
		SELECT
			d.attempt_id, d.logical_operation_key, d.run_id, d.member_id,
			d.logical_step_id, d.source_model_attempt_id,
			d.member_snapshot_digest, d.binding_index, d.binding_json,
			d.public_action_id, d.provider_action_id, d.definition_digest,
			d.proposal_ref, d.effect_class, d.max_result_bytes,
			d.usage_ledger_ref, d.state, d.external_operation_id,
			d.provider_receipt_ref, d.result_ref, d.error_classification,
			d.reconciliation_evidence_ref, d.unknown_reason,
			m.canonical_json, m.digest,
			p.kind, p.media_type, p.canonical_bytes,
			r.kind, r.media_type, r.canonical_bytes
		FROM dispatch_attempts AS d
		JOIN member_execution_snapshots AS m
		  ON m.run_id=d.run_id AND m.member_id=d.member_id
		JOIN content_records AS p ON p.content_digest=d.proposal_ref
		LEFT JOIN content_records AS r ON r.content_digest=d.result_ref
		WHERE d.attempt_id=? AND d.dispatch_kind='ACTION'
	`, attemptID).Scan(
		&record.attemptID,
		&logicalOperationKey,
		&record.runID,
		&record.memberID,
		&record.logicalStepID,
		&record.sourceModelID,
		&record.memberDigest,
		&bindingIndex,
		&bindingCanonical,
		&record.publicActionID,
		&record.providerActionID,
		&record.definitionDigest,
		&record.proposalRef,
		&effectClass,
		&maxResultBytes,
		&usageLedgerRef,
		&record.state,
		&externalOperationID,
		&providerReceiptRef,
		&resultRef,
		&errorClassification,
		&evidenceRef,
		&unknownReason,
		&memberCanonical,
		&storedMemberDigest,
		&proposalKind,
		&proposalMediaType,
		&proposalCanonical,
		&resultKind,
		&resultMediaType,
		&resultCanonical,
	)
	if err != nil {
		return fmt.Errorf("%w: read Action Attempt %q: %v", ErrIntegrity, attemptID, err)
	}
	record.externalOperationID = externalOperationID.String
	record.providerReceiptRef = providerReceiptRef.String
	record.resultRef = resultRef.String
	record.errorClassification = errorClassification.String
	record.evidenceRef = evidenceRef.String
	record.unknownReason = unknownReason.String
	if !moduleapi.ValidSHA256(logicalOperationKey) ||
		bindingIndex < 0 || bindingIndex > int64(^uint32(0)) ||
		maxResultBytes < 1 || maxResultBytes > moduleapi.MaxActionResultBytesV1 ||
		!moduleapi.ValidSHA256(record.memberDigest) ||
		!moduleapi.ValidSHA256(record.definitionDigest) ||
		!moduleapi.ValidSHA256(record.proposalRef) {
		return fmt.Errorf("%w: invalid Action Attempt scalar projection", ErrIntegrity)
	}
	if _, err := corecontract.ParseUsageLedgerRefV1(usageLedgerRef, record.runID); err != nil {
		return fmt.Errorf("%w: invalid Action UsageLedgerRef: %v", ErrIntegrity, err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || member.MemberSnapshotDigest != storedMemberDigest ||
		storedMemberDigest != record.memberDigest || member.MemberID != record.memberID {
		return fmt.Errorf("%w: Action member snapshot closure differs", ErrIntegrity)
	}
	record.member = member
	definition, binding, err := inspectedActionDefinition(
		member,
		record,
		uint32(bindingIndex),
		moduleapi.EffectClass(effectClass),
		uint32(maxResultBytes),
	)
	if err != nil {
		return err
	}
	record.definition = definition
	encodedBinding, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("%w: marshal Action Binding: %v", ErrIntegrity, err)
	}
	encodedBinding, err = moduleapi.CanonicalJSON(encodedBinding)
	if err != nil || !bytes.Equal(encodedBinding, bindingCanonical) {
		return fmt.Errorf("%w: Action Binding canonical bytes differ", ErrIntegrity)
	}
	if proposalKind != string(currentstore.ContentActionProposal) ||
		proposalMediaType != "application/json" {
		return fmt.Errorf("%w: Action Proposal content identity differs", ErrIntegrity)
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		proposalCanonical,
		record.proposalRef,
		record.memberDigest,
		definition,
	)
	if err != nil {
		return fmt.Errorf("%w: restore Action Proposal: %v", ErrIntegrity, err)
	}
	record.proposal = proposal
	if record.resultRef != "" {
		if !resultKind.Valid || resultKind.String != string(currentstore.ContentActionResult) ||
			!resultMediaType.Valid || resultMediaType.String != "application/json" {
			return fmt.Errorf("%w: Action Result content identity differs", ErrIntegrity)
		}
		result, err := corecontract.RestoreActionResultV1(
			resultCanonical,
			record.resultRef,
			definition,
		)
		if err != nil {
			return fmt.Errorf("%w: restore Action Result: %v", ErrIntegrity, err)
		}
		record.result = &result
		record.resultStatus = result.Status
	} else if resultKind.Valid || resultMediaType.Valid || len(resultCanonical) != 0 {
		return fmt.Errorf("%w: NULL Action result has joined content", ErrIntegrity)
	}
	if err := validateInspectedActionState(record); err != nil {
		return err
	}
	if err := inspectActionSourceModel(ctx, database, record); err != nil {
		return err
	}
	if err := inspectActionModelTwo(ctx, database, record); err != nil {
		return err
	}
	return inspectActionFrameProjection(ctx, database, record)
}

func inspectedActionDefinition(
	member corecontract.MemberExecutionSnapshot,
	record inspectedActionAttempt,
	bindingIndex uint32,
	effect moduleapi.EffectClass,
	maximum uint32,
) (corecontract.FrozenActionDefinitionV1, moduleapi.PortBinding, error) {
	var plan *moduleapi.PortPlan
	for index := range member.PortPlans {
		candidate := &member.PortPlans[index]
		if candidate.Port.Name == moduleapi.PortNameActionProvider &&
			candidate.Port.ExactVersion == moduleapi.PortVersionV1 {
			if plan != nil {
				return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{},
					fmt.Errorf("%w: duplicate Action PortPlan", ErrIntegrity)
			}
			plan = candidate
		}
	}
	if plan == nil || uint64(bindingIndex) >= uint64(len(plan.Bindings)) {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{},
			fmt.Errorf("%w: missing Action Binding", ErrIntegrity)
	}
	var found *corecontract.FrozenActionDefinitionV1
	for index := range member.Actions {
		candidate := &member.Actions[index]
		if candidate.PublicActionID == record.publicActionID {
			if found != nil {
				return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{},
					fmt.Errorf("%w: duplicate frozen Action", ErrIntegrity)
			}
			found = candidate
		}
	}
	if found == nil || found.ProviderActionID != record.providerActionID ||
		found.BindingIndex != bindingIndex ||
		found.DefinitionDigest != record.definitionDigest ||
		found.EffectClass != effect || found.MaxResultBytes != maximum {
		return corecontract.FrozenActionDefinitionV1{}, moduleapi.PortBinding{},
			fmt.Errorf("%w: Action Definition closure differs", ErrIntegrity)
	}
	return *found, plan.Bindings[bindingIndex], nil
}

func validateInspectedActionState(record inspectedActionAttempt) error {
	switch record.state {
	case "PENDING":
		if record.result != nil || record.errorClassification != "" ||
			record.externalOperationID != "" || record.providerReceiptRef != "" ||
			record.evidenceRef != "" || record.unknownReason != "" {
			return fmt.Errorf("%w: PENDING Action carries outcome facts", ErrIntegrity)
		}
	case "SUCCEEDED":
		if record.result == nil || record.errorClassification != "" ||
			record.unknownReason != "" {
			return fmt.Errorf("%w: SUCCEEDED Action state/result differs", ErrIntegrity)
		}
	case "FAILED":
		if record.result != nil || record.errorClassification == "" ||
			record.unknownReason != "" {
			return fmt.Errorf("%w: FAILED Action state/error differs", ErrIntegrity)
		}
	case "UNKNOWN":
		if record.result != nil || record.errorClassification != "" ||
			(record.externalOperationID == "" && record.providerReceiptRef == "" &&
				record.evidenceRef == "" && record.unknownReason == "") {
			return fmt.Errorf("%w: UNKNOWN Action lacks durable clue", ErrIntegrity)
		}
	default:
		return fmt.Errorf("%w: unsupported Action state %q", ErrIntegrity, record.state)
	}
	return nil
}

func inspectActionSourceModel(
	ctx context.Context,
	database semanticQueryer,
	record inspectedActionAttempt,
) error {
	var (
		runID           string
		memberID        string
		logicalStepID   string
		state           string
		sourceDispatch  sql.NullString
		resultRef       sql.NullString
		resultKind      sql.NullString
		resultCanonical []byte
	)
	if err := database.QueryRowContext(ctx, `
		SELECT m.run_id, m.member_id, m.logical_step_id, m.state,
		       m.source_dispatch_attempt_id, m.result_ref,
		       c.kind, c.canonical_bytes
		FROM model_dispatch_attempts AS m
		LEFT JOIN content_records AS c ON c.content_digest=m.result_ref
		WHERE m.attempt_id=?
	`, record.sourceModelID).Scan(
		&runID,
		&memberID,
		&logicalStepID,
		&state,
		&sourceDispatch,
		&resultRef,
		&resultKind,
		&resultCanonical,
	); err != nil {
		return fmt.Errorf("%w: read Action source Model: %v", ErrIntegrity, err)
	}
	if runID != record.runID || memberID != record.memberID ||
		logicalStepID != corecontract.FirstModelLogicalStepIDV1 ||
		state != "SUCCEEDED" || sourceDispatch.Valid || !resultRef.Valid ||
		!resultKind.Valid || resultKind.String != string(currentstore.ContentModelResult) {
		return fmt.Errorf("%w: Action source Model closure differs", ErrIntegrity)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(resultCanonical)
	if err != nil || output.ActionRequest == nil ||
		output.ActionRequest.ActionID != record.publicActionID ||
		!bytes.Equal(output.ActionRequest.CanonicalInput, record.proposal.CanonicalInput) {
		return fmt.Errorf("%w: Action source Model output differs: %v", ErrIntegrity, err)
	}
	return nil
}

func inspectActionModelTwo(
	ctx context.Context,
	database semanticQueryer,
	record inspectedActionAttempt,
) error {
	var count int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM model_dispatch_attempts
		WHERE source_dispatch_attempt_id=?
	`, record.attemptID).Scan(&count); err != nil {
		return fmt.Errorf("currentbackup: count model-2 source links: %w", err)
	}
	if count == 0 {
		return nil
	}
	if count != 1 {
		return fmt.Errorf("%w: Action has multiple model-2 source links", ErrIntegrity)
	}
	var (
		attemptID      string
		runID          string
		memberID       string
		logicalStepID  string
		compilationRef sql.NullString
		requestRef     string
		kind           string
		mediaType      string
		canonical      []byte
	)
	err := database.QueryRowContext(ctx, `
		SELECT m.attempt_id, m.run_id, m.member_id, m.logical_step_id,
		       m.context_compilation_ref, m.request_ref, c.kind,
		       c.media_type, c.canonical_bytes
		FROM model_dispatch_attempts AS m
		JOIN content_records AS c ON c.content_digest=m.request_ref
		WHERE m.source_dispatch_attempt_id=?
	`, record.attemptID).Scan(
		&attemptID,
		&runID,
		&memberID,
		&logicalStepID,
		&compilationRef,
		&requestRef,
		&kind,
		&mediaType,
		&canonical,
	)
	if err != nil {
		return fmt.Errorf("currentbackup: read model-2 source links: %w", err)
	}
	if record.state != "SUCCEEDED" ||
		record.resultStatus != corecontract.ActionResultAvailable ||
		runID != record.runID || memberID != record.memberID ||
		logicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		compilationRef.Valid || requestRef == "" ||
		kind != string(currentstore.ContentModelRequest) ||
		mediaType != "application/json" {
		return fmt.Errorf("%w: model-2 source link differs", ErrIntegrity)
	}
	_ = attemptID
	return inspectModelTwoRequest(ctx, database, record, canonical)
}

func inspectModelTwoRequest(
	ctx context.Context,
	database semanticQueryer,
	record inspectedActionAttempt,
	modelTwoCanonical []byte,
) error {
	var (
		requestKind       string
		modelOneCanonical []byte
	)
	if err := database.QueryRowContext(ctx, `
		SELECT c.kind, c.canonical_bytes
		FROM model_dispatch_attempts AS m
		JOIN content_records AS c ON c.content_digest=m.request_ref
		WHERE m.attempt_id=?
	`, record.sourceModelID).Scan(&requestKind, &modelOneCanonical); err != nil {
		return fmt.Errorf("%w: read model-1 request: %v", ErrIntegrity, err)
	}
	if requestKind != string(currentstore.ContentModelRequest) || record.result == nil {
		return fmt.Errorf("%w: model-1 request or Action result is unavailable", ErrIntegrity)
	}
	modelOne, err := moduleapi.RestoreModelGenerateRequestV1(modelOneCanonical)
	if err != nil {
		return fmt.Errorf("%w: restore model-1 request: %v", ErrIntegrity, err)
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		*record.result,
		record.definition,
	)
	if err != nil {
		return fmt.Errorf("%w: build Action result envelope: %v", ErrIntegrity, err)
	}
	expected := modelOne
	expected.Messages = append(
		append([]moduleapi.ModelMessageV1(nil), modelOne.Messages...),
		envelope,
	)
	_, expectedCanonical, err := moduleapi.NewModelGenerateRequestV1(expected)
	if err != nil || !bytes.Equal(expectedCanonical, modelTwoCanonical) {
		return fmt.Errorf("%w: model-2 request is not exact model-1+ActionResult: %v", ErrIntegrity, err)
	}
	return nil
}

func inspectActionFrameProjection(
	ctx context.Context,
	database semanticQueryer,
	record inspectedActionAttempt,
) error {
	var (
		step          string
		continuation  []byte
		pendingModel  sql.NullString
		pendingAction sql.NullString
		waitingReason sql.NullString
	)
	if err := database.QueryRowContext(ctx, `
		SELECT step, continuation, pending_attempt_id,
		       pending_dispatch_attempt_id, waiting_reason
		FROM loop_frames WHERE run_id=?
	`, record.runID).Scan(
		&step,
		&continuation,
		&pendingModel,
		&pendingAction,
		&waitingReason,
	); err != nil {
		return fmt.Errorf("%w: read Action Frame: %v", ErrIntegrity, err)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil {
		return fmt.Errorf("%w: restore Action continuation: %v", ErrIntegrity, err)
	}
	switch record.state {
	case "PENDING":
		if step != corecontract.ActionPendingLoopStep || pendingModel.Valid ||
			!pendingAction.Valid || pendingAction.String != record.attemptID ||
			waitingReason.Valid || continued.AttemptKind != corecontract.AttemptKindAction ||
			continued.AttemptID != record.attemptID {
			return fmt.Errorf("%w: PENDING Action Frame projection differs", ErrIntegrity)
		}
	case "UNKNOWN":
		if step != corecontract.WaitingReconciliationLoopStep || pendingModel.Valid ||
			pendingAction.Valid || !waitingReason.Valid ||
			continued.AttemptKind != corecontract.AttemptKindAction ||
			continued.AttemptID != record.attemptID {
			return fmt.Errorf("%w: UNKNOWN Action Frame projection differs", ErrIntegrity)
		}
	case "FAILED":
		if step != corecontract.TerminatedLoopStep ||
			continued.AttemptKind != corecontract.AttemptKindAction ||
			continued.AttemptID != record.attemptID {
			return fmt.Errorf("%w: FAILED Action terminal projection differs", ErrIntegrity)
		}
	case "SUCCEEDED":
		if record.resultStatus == corecontract.ActionResultRejected &&
			(step != corecontract.TerminatedLoopStep ||
				continued.AttemptKind != corecontract.AttemptKindAction ||
				continued.AttemptID != record.attemptID) {
			return fmt.Errorf("%w: RESULT_REJECTED Action terminal projection differs", ErrIntegrity)
		}
	}
	return nil
}

type inspectedModelActionRejectionEvent struct {
	SchemaVersion       string `json:"schema_version"`
	RunID               string `json:"run_id"`
	ModelAttemptID      string `json:"model_attempt_id"`
	LogicalStepID       string `json:"logical_step_id"`
	LogicalOperationKey string `json:"logical_operation_key"`
	ResultDigest        string `json:"result_digest"`
	ErrorClassification string `json:"error_classification"`
}

func inspectLegalModelActionRejections(ctx context.Context, database semanticQueryer) error {
	rows, err := database.QueryContext(ctx, `
		SELECT m.attempt_id, m.run_id, m.logical_step_id,
		       m.logical_operation_key, m.result_ref, c.canonical_bytes,
		       f.step, f.continuation, f.last_authoritative_event,
		       e.event_kind, e.payload_ref, e.payload_digest,
		       p.kind, p.media_type, p.canonical_bytes,
		       (SELECT COUNT(*) FROM history_entries h
		        WHERE h.run_id=m.run_id AND h.source_attempt_id=m.attempt_id)
		FROM model_dispatch_attempts AS m
		JOIN content_records AS c ON c.content_digest=m.result_ref
		JOIN loop_frames AS f ON f.run_id=m.run_id
		JOIN run_events AS e
		  ON e.run_id=m.run_id AND e.event_sequence=f.last_authoritative_event
		JOIN content_records AS p ON p.content_digest=e.payload_ref
		WHERE m.state='SUCCEEDED'
		  AND NOT EXISTS(
			SELECT 1 FROM dispatch_attempts d
			WHERE d.source_model_attempt_id=m.attempt_id
			  AND d.dispatch_kind='ACTION'
		  )
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read possible legal Action rejections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			attemptID        string
			runID            string
			logicalStepID    string
			logicalKey       string
			resultRef        string
			resultCanonical  []byte
			frameStep        string
			continuation     []byte
			lastEvent        int64
			eventKind        string
			payloadRef       string
			payloadDigest    string
			payloadKind      string
			payloadMedia     string
			payloadCanonical []byte
			historyCount     int64
		)
		if err := rows.Scan(
			&attemptID,
			&runID,
			&logicalStepID,
			&logicalKey,
			&resultRef,
			&resultCanonical,
			&frameStep,
			&continuation,
			&lastEvent,
			&eventKind,
			&payloadRef,
			&payloadDigest,
			&payloadKind,
			&payloadMedia,
			&payloadCanonical,
			&historyCount,
		); err != nil {
			return fmt.Errorf("currentbackup: scan legal Action rejection: %w", err)
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(resultCanonical)
		if err != nil {
			return fmt.Errorf("%w: restore successful Model result: %v", ErrIntegrity, err)
		}
		if output.ActionRequest == nil {
			continue
		}
		continued, err := corecontract.RestoreLoopContinuationV1(continuation)
		computed, digestErr := currentstore.ComputeContentDigest(
			currentstore.ContentRunEventPayload,
			"application/json",
			payloadCanonical,
		)
		var event inspectedModelActionRejectionEvent
		decodeErr := json.Unmarshal(payloadCanonical, &event)
		rebuiltEvent, marshalErr := json.Marshal(event)
		canonical, canonicalErr := moduleapi.CanonicalJSON(rebuiltEvent)
		if err != nil || digestErr != nil || decodeErr != nil || canonicalErr != nil ||
			marshalErr != nil ||
			frameStep != corecontract.TerminatedLoopStep || historyCount != 0 ||
			continued.AttemptKind != corecontract.AttemptKindModel ||
			continued.AttemptID != attemptID || continued.LogicalStepID != logicalStepID ||
			eventKind != "MODEL_ACTION_REJECTED" || lastEvent < 1 ||
			payloadRef != payloadDigest || payloadDigest != computed ||
			payloadKind != string(currentstore.ContentRunEventPayload) ||
			payloadMedia != "application/json" || !bytes.Equal(canonical, payloadCanonical) ||
			event.SchemaVersion != "model-action-rejection-event/v1" ||
			event.RunID != runID || event.ModelAttemptID != attemptID ||
			event.LogicalStepID != logicalStepID ||
			event.LogicalOperationKey != logicalKey ||
			event.ResultDigest != resultRef || event.ErrorClassification == "" {
			return fmt.Errorf("%w: legal Model Action rejection closure differs", ErrIntegrity)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate legal Action rejections: %w", err)
	}
	return nil
}
