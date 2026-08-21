package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	coreJSONMediaType              = "application/json"
	coreUsagePending               = "PENDING"
	coreUsageProviderReported      = "PROVIDER_REPORTED"
	coreUsageNoReport              = "NO_USAGE_REPORTED"
	coreUsagePendingReconciliation = "PENDING_RECONCILIATION"
	coreCompositeWaitingReason     = "COMPOSITE_CHILDREN_PENDING"
)

type coreRunRow struct {
	runID                 string
	tenantID              string
	workspaceID           string
	conversationID        sql.NullString
	conversationTurnIndex sql.NullInt64
	conversationPrevious  sql.NullString
	admissionKey          string
	admissionIntentDigest string
	parentRunID           sql.NullString
	parentManifestDigest  sql.NullString
	parentSlotID          sql.NullString
	cancelRequestRef      sql.NullString
	state                 string
	disposition           sql.NullString
	revision              int64
}

type coreFrameState struct {
	revision               int64
	step                   string
	budgetStateRef         string
	continuation           corecontract.LoopContinuationV1
	pendingModelAttempt    sql.NullString
	pendingDispatchAttempt sql.NullString
	waitingReason          sql.NullString
	lastAuthoritativeEvent int64
}

type coreUsageState struct {
	attemptID            string
	runID                string
	ledgerSequence       sql.NullInt64
	revision             int64
	inputTokens          sql.NullInt64
	cachedInputTokens    sql.NullInt64
	uncachedInputTokens  sql.NullInt64
	outputTokens         sql.NullInt64
	reasoningTokens      sql.NullInt64
	estimatedCost        sql.NullString
	providerReportedCost sql.NullString
	reconciledCost       sql.NullString
	status               string
	rawReceiptRef        sql.NullString
}

type coreModelAttemptState struct {
	attemptID                 string
	logicalOperationKey       string
	runID                     string
	memberID                  string
	logicalStepID             string
	frameRevision             int64
	memberSnapshotDigest      string
	contextCompilationRef     sql.NullString
	requestRef                string
	requestDigest             string
	parametersCanonical       []byte
	state                     corecontract.ModelAttemptState
	providerRequestID         sql.NullString
	providerReceiptRef        sql.NullString
	resultRef                 sql.NullString
	errorClassification       sql.NullString
	reconciliationEvidenceRef sql.NullString
	unknownReason             sql.NullString
	revision                  int64
	request                   moduleapi.ModelGenerateRequestV1
	result                    *moduleapi.ModelGenerateOutputV1
	compilation               *corecontract.ContextCompilationV1
	usage                     coreUsageState
}

type coreRunEventState struct {
	sequence        int64
	kind            string
	fromRevision    int64
	toRevision      int64
	payload         []byte
	model           *corecontract.ModelDispatchEventV1
	coreFailure     *corecontract.CoreDeterministicFailureEventV1
	repairActivated *corecontract.CompositeRepairActivatedEventV1
	repairSkipped   *corecontract.CompositeRepairSkippedEventV1
}

type coreRunSemanticState struct {
	row              coreRunRow
	manifest         corecontract.RunManifest
	member           corecontract.MemberExecutionSnapshot
	frame            coreFrameState
	attempts         map[string]*coreModelAttemptState
	effectAttempts   int64
	events           []coreRunEventState
	historyByAttempt map[string]int
}

type coreContentState struct {
	kind      currentstore.ContentKind
	mediaType string
	canonical []byte
}

// inspectCoreRunSemanticClosure verifies the generic recovery facts that are
// shared by Pure Chat and every optional capability. Action and Channel keep
// their focused verifiers; this function closes the common Run graph and the
// optional depth-one Composite edges without constructing a Runtime or Store.
func inspectCoreRunSemanticClosure(
	ctx context.Context,
	database semanticQueryer,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			run_id, tenant_id, workspace_id,
			conversation_id, conversation_turn_index,
			conversation_predecessor_run_id, admission_key,
			admission_intent_digest, parent_run_id,
			parent_manifest_digest, parent_slot_id,
			cancel_request_ref, state, disposition, revision
		FROM runs
		ORDER BY run_id
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read core Runs: %w", err)
	}
	inputs := make([]coreRunRow, 0)
	for rows.Next() {
		var row coreRunRow
		if err := rows.Scan(
			&row.runID,
			&row.tenantID,
			&row.workspaceID,
			&row.conversationID,
			&row.conversationTurnIndex,
			&row.conversationPrevious,
			&row.admissionKey,
			&row.admissionIntentDigest,
			&row.parentRunID,
			&row.parentManifestDigest,
			&row.parentSlotID,
			&row.cancelRequestRef,
			&row.state,
			&row.disposition,
			&row.revision,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("currentbackup: scan core Run: %w", err)
		}
		inputs = append(inputs, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("currentbackup: iterate core Runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("currentbackup: close core Runs: %w", err)
	}

	states := make(map[string]*coreRunSemanticState, len(inputs))
	for _, row := range inputs {
		state, err := inspectOneCoreRun(ctx, database, row)
		if err != nil {
			return err
		}
		states[row.runID] = state
	}
	// Family limits and cancellation are checked before the per-Run event
	// projection so an over-budget graph cannot be hidden behind a second,
	// incidental corruption.
	if err := inspectCompositeFamilyClosures(ctx, database, states); err != nil {
		return err
	}
	if err := inspectWorkspaceTransferSemanticClosure(ctx, database, states); err != nil {
		return err
	}
	if err := inspectConversationClosures(ctx, database, states); err != nil {
		return err
	}
	if err := inspectConversationSummaryClosures(ctx, database, states); err != nil {
		return err
	}
	if err := inspectKnowledgeReuseSourceClosures(states); err != nil {
		return err
	}
	for _, row := range inputs {
		if err := validateCoreRunProjection(states[row.runID]); err != nil {
			return err
		}
	}
	return nil
}

func inspectOneCoreRun(
	ctx context.Context,
	database semanticQueryer,
	row coreRunRow,
) (*coreRunSemanticState, error) {
	if row.revision < 0 {
		return nil, coreIntegrity("Run %q has a negative revision", row.runID)
	}
	var manifestCanonical []byte
	var manifestDigest string
	if err := database.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, row.runID).Scan(&manifestCanonical, &manifestDigest); err != nil {
		return nil, coreIntegrity("Run %q Manifest: %v", row.runID, err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.ManifestDigest != manifestDigest ||
		manifest.RunID != row.runID || manifest.TenantID != row.tenantID ||
		manifest.Workspace.ID != row.workspaceID ||
		manifest.AdmissionKey != row.admissionKey ||
		manifest.AdmissionIntentDigest != row.admissionIntentDigest ||
		manifest.ParentRunID != row.parentRunID.String {
		return nil, coreIntegrity("Run %q Manifest projection differs: %v", row.runID, err)
	}

	member, err := inspectCoreMember(ctx, database, row.runID, manifest)
	if err != nil {
		return nil, err
	}
	if _, err := inspectCoreContent(
		ctx,
		database,
		manifest.TaskInputRef,
		currentstore.ContentTaskInput,
	); err != nil {
		return nil, coreIntegrity("Run %q TaskInput: %v", row.runID, err)
	}
	frame, err := inspectCoreFrame(ctx, database, row.runID)
	if err != nil {
		return nil, err
	}
	attempts, err := inspectCoreModelAttempts(ctx, database, row.runID, member)
	if err != nil {
		return nil, err
	}
	var effectAttempts int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, row.runID).Scan(&effectAttempts); err != nil || effectAttempts < 0 {
		return nil, coreIntegrity(
			"Run %q effect Attempt count differs: %v",
			row.runID,
			err,
		)
	}
	events, err := inspectCoreRunEvents(ctx, database, row.runID, manifest, member)
	if err != nil {
		return nil, err
	}
	history, err := inspectCoreHistory(ctx, database, row.runID, member, attempts)
	if err != nil {
		return nil, err
	}
	return &coreRunSemanticState{
		row:              row,
		manifest:         manifest,
		member:           member,
		frame:            frame,
		attempts:         attempts,
		effectAttempts:   effectAttempts,
		events:           events,
		historyByAttempt: history,
	}, nil
}

func inspectCoreMember(
	ctx context.Context,
	database semanticQueryer,
	runID string,
	manifest corecontract.RunManifest,
) (corecontract.MemberExecutionSnapshot, error) {
	var count int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM member_execution_snapshots WHERE run_id=?
	`, runID).Scan(&count); err != nil || count != 1 {
		return corecontract.MemberExecutionSnapshot{}, coreIntegrity(
			"Run %q has %d Member snapshots, want 1: %v", runID, count, err,
		)
	}
	var (
		memberID, agentID, agentVersion, agentDigest   string
		profileID, profileVersion, profileDigest       string
		workspaceID, workspaceVersion, workspaceDigest string
		controlSnapshotID, catalogGenerationID         string
		canonical                                      []byte
		digest                                         string
	)
	if err := database.QueryRowContext(ctx, `
		SELECT
			member_id,
			agent_id, agent_version, agent_digest,
			profile_id, profile_version, profile_digest,
			workspace_id, workspace_version, workspace_digest,
			control_snapshot_id, catalog_generation_id,
			canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=?
	`, runID).Scan(
		&memberID,
		&agentID,
		&agentVersion,
		&agentDigest,
		&profileID,
		&profileVersion,
		&profileDigest,
		&workspaceID,
		&workspaceVersion,
		&workspaceDigest,
		&controlSnapshotID,
		&catalogGenerationID,
		&canonical,
		&digest,
	); err != nil {
		return corecontract.MemberExecutionSnapshot{}, coreIntegrity(
			"Run %q Member snapshot: %v", runID, err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil || member.MemberSnapshotDigest != digest ||
		member.MemberID != memberID ||
		member.Agent != (corecontract.AgentRef{
			ID: agentID, Version: agentVersion, Digest: agentDigest,
		}) ||
		member.Profile != (corecontract.ProfileRef{
			ID: profileID, Version: profileVersion, Digest: profileDigest,
		}) ||
		member.Workspace != (corecontract.WorkspaceRef{
			ID: workspaceID, Version: workspaceVersion, Digest: workspaceDigest,
		}) || member.Catalog.ID != catalogGenerationID ||
		manifest.ValidateAgainstMember(member) != nil {
		return corecontract.MemberExecutionSnapshot{}, coreIntegrity(
			"Run %q Member/Manifest projection differs: %v", runID, err,
		)
	}
	var catalogControlID string
	if err := database.QueryRowContext(ctx, `
		SELECT control_snapshot_id
		FROM runtime_catalog_generations
		WHERE generation_id=?
	`, catalogGenerationID).Scan(&catalogControlID); err != nil ||
		catalogControlID != controlSnapshotID {
		return corecontract.MemberExecutionSnapshot{}, coreIntegrity(
			"Run %q Control/Catalog snapshot projection differs: %v", runID, err,
		)
	}
	return member, nil
}

func inspectCoreFrame(
	ctx context.Context,
	database semanticQueryer,
	runID string,
) (coreFrameState, error) {
	var state coreFrameState
	var continuation []byte
	if err := database.QueryRowContext(ctx, `
		SELECT
			frame_revision, step, budget_state_ref, continuation,
			pending_attempt_id, pending_dispatch_attempt_id,
			waiting_reason, last_authoritative_event
		FROM loop_frames
		WHERE run_id=?
	`, runID).Scan(
		&state.revision,
		&state.step,
		&state.budgetStateRef,
		&continuation,
		&state.pendingModelAttempt,
		&state.pendingDispatchAttempt,
		&state.waitingReason,
		&state.lastAuthoritativeEvent,
	); err != nil {
		return coreFrameState{}, coreIntegrity("Run %q LoopFrame: %v", runID, err)
	}
	restored, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || restored.State != state.step || state.revision < 0 ||
		state.lastAuthoritativeEvent < 0 ||
		(state.pendingModelAttempt.Valid && state.pendingDispatchAttempt.Valid) {
		return coreFrameState{}, coreIntegrity(
			"Run %q LoopFrame continuation/projection differs: %v", runID, err,
		)
	}
	state.continuation = restored
	return state, nil
}

func inspectCoreModelAttempts(
	ctx context.Context,
	database semanticQueryer,
	runID string,
	member corecontract.MemberExecutionSnapshot,
) (map[string]*coreModelAttemptState, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			attempt_id, logical_operation_key, run_id, member_id,
			logical_step_id, frame_revision, member_snapshot_digest,
			context_compilation_ref, request_ref, request_digest,
			parameters_json, state, provider_request_id,
			provider_receipt_ref, result_ref, error_classification,
			reconciliation_evidence_ref, unknown_reason, revision
		FROM model_dispatch_attempts
		WHERE run_id=?
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: read model Attempts: %w", err)
	}
	pending := make([]coreModelAttemptState, 0)
	for rows.Next() {
		var attempt coreModelAttemptState
		var state string
		if err := rows.Scan(
			&attempt.attemptID,
			&attempt.logicalOperationKey,
			&attempt.runID,
			&attempt.memberID,
			&attempt.logicalStepID,
			&attempt.frameRevision,
			&attempt.memberSnapshotDigest,
			&attempt.contextCompilationRef,
			&attempt.requestRef,
			&attempt.requestDigest,
			&attempt.parametersCanonical,
			&state,
			&attempt.providerRequestID,
			&attempt.providerReceiptRef,
			&attempt.resultRef,
			&attempt.errorClassification,
			&attempt.reconciliationEvidenceRef,
			&attempt.unknownReason,
			&attempt.revision,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("currentbackup: scan model Attempt: %w", err)
		}
		attempt.state = corecontract.ModelAttemptState(state)
		pending = append(pending, attempt)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("currentbackup: iterate model Attempts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentbackup: close model Attempts: %w", err)
	}

	attempts := make(map[string]*coreModelAttemptState, len(pending))
	for index := range pending {
		attempt := &pending[index]
		if attempt.runID != runID || attempt.memberID != member.MemberID ||
			attempt.memberSnapshotDigest != member.MemberSnapshotDigest ||
			attempt.frameRevision < 0 || attempt.revision < 0 ||
			attempt.state.Validate() != nil {
			return nil, coreIntegrity("Run %q model Attempt %q identity/state differs", runID, attempt.attemptID)
		}
		operationKey, err := corecontract.ModelLogicalOperationKey(
			runID,
			member.MemberID,
			attempt.logicalStepID,
		)
		if err != nil || operationKey != attempt.logicalOperationKey {
			return nil, coreIntegrity("Run %q model Attempt %q logical key differs", runID, attempt.attemptID)
		}
		requestContent, err := inspectCoreContent(
			ctx,
			database,
			attempt.requestRef,
			currentstore.ContentModelRequest,
		)
		if err != nil || attempt.requestRef != attempt.requestDigest {
			return nil, coreIntegrity("Run %q model Attempt %q request: %v", runID, attempt.attemptID, err)
		}
		request, err := moduleapi.RestoreModelGenerateRequestV1(requestContent.canonical)
		if err != nil || !bytes.Equal(request.Parameters, attempt.parametersCanonical) {
			return nil, coreIntegrity("Run %q model Attempt %q request projection differs: %v", runID, attempt.attemptID, err)
		}
		attempt.request = request
		if attempt.contextCompilationRef.Valid {
			content, err := inspectCoreContent(
				ctx,
				database,
				attempt.contextCompilationRef.String,
				currentstore.ContentContextCompilation,
			)
			if err != nil {
				return nil, coreIntegrity("Run %q model Attempt %q ContextCompilation: %v", runID, attempt.attemptID, err)
			}
			compilation, err := corecontract.RestoreContextCompilationV1(content.canonical)
			if err != nil {
				return nil, coreIntegrity("Run %q model Attempt %q ContextCompilation wire: %v", runID, attempt.attemptID, err)
			}
			if compilation.FinalRequestDigest != attempt.requestDigest ||
				compilation.WorkspaceScope != member.Workspace ||
				compilation.ContextPolicy != member.ContextPolicy {
				return nil, coreIntegrity(
					"Run %q model Attempt %q ContextCompilation request/member closure differs",
					runID,
					attempt.attemptID,
				)
			}
			attempt.compilation = &compilation
		}
		if attempt.resultRef.Valid {
			content, err := inspectCoreContent(
				ctx,
				database,
				attempt.resultRef.String,
				currentstore.ContentModelResult,
			)
			if err != nil {
				return nil, coreIntegrity("Run %q model Attempt %q result: %v", runID, attempt.attemptID, err)
			}
			result, err := moduleapi.RestoreModelGenerateOutputV1(content.canonical)
			if err != nil ||
				(result.ProviderRequestID != "" && attempt.providerRequestID.Valid &&
					result.ProviderRequestID != attempt.providerRequestID.String) {
				return nil, coreIntegrity("Run %q model Attempt %q result projection differs: %v", runID, attempt.attemptID, err)
			}
			attempt.result = &result
		}
		if attempt.reconciliationEvidenceRef.Valid {
			if _, err := inspectCoreContent(
				ctx,
				database,
				attempt.reconciliationEvidenceRef.String,
				currentstore.ContentReconciliationEvidence,
			); err != nil {
				return nil, coreIntegrity("Run %q model Attempt %q reconciliation evidence: %v", runID, attempt.attemptID, err)
			}
		}
		usage, err := inspectCoreUsage(ctx, database, *attempt)
		if err != nil {
			return nil, err
		}
		attempt.usage = usage
		attempts[attempt.attemptID] = attempt
	}
	return attempts, nil
}

func inspectCoreUsage(
	ctx context.Context,
	database semanticQueryer,
	attempt coreModelAttemptState,
) (coreUsageState, error) {
	var usage coreUsageState
	if err := database.QueryRowContext(ctx, `
		SELECT
			attempt_id, run_id, ledger_sequence, revision,
			input_tokens, cached_input_tokens, uncached_input_tokens,
			output_tokens, reasoning_tokens, estimated_cost,
			provider_reported_cost, reconciled_cost,
			reconciliation_status, raw_receipt_ref
		FROM model_usage
		WHERE attempt_id=?
	`, attempt.attemptID).Scan(
		&usage.attemptID,
		&usage.runID,
		&usage.ledgerSequence,
		&usage.revision,
		&usage.inputTokens,
		&usage.cachedInputTokens,
		&usage.uncachedInputTokens,
		&usage.outputTokens,
		&usage.reasoningTokens,
		&usage.estimatedCost,
		&usage.providerReportedCost,
		&usage.reconciledCost,
		&usage.status,
		&usage.rawReceiptRef,
	); err != nil {
		return coreUsageState{}, coreIntegrity("Run %q model Attempt %q Usage: %v", attempt.runID, attempt.attemptID, err)
	}
	if usage.attemptID != attempt.attemptID || usage.runID != attempt.runID ||
		usage.revision < 0 ||
		(usage.ledgerSequence.Valid && usage.ledgerSequence.Int64 <= 0) ||
		usage.rawReceiptRef != attempt.providerReceiptRef ||
		(usage.inputTokens.Valid && usage.cachedInputTokens.Valid &&
			usage.uncachedInputTokens.Valid &&
			usage.inputTokens.Int64 != usage.cachedInputTokens.Int64+usage.uncachedInputTokens.Int64) {
		return coreUsageState{}, coreIntegrity("Run %q model Attempt %q Usage identity/tokens differ", attempt.runID, attempt.attemptID)
	}
	if attempt.providerReceiptRef.Valid {
		content, err := inspectCoreContent(
			ctx,
			database,
			attempt.providerReceiptRef.String,
			currentstore.ContentProviderReceipt,
		)
		if err != nil {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q provider receipt: %v", attempt.runID, attempt.attemptID, err)
		}
		receipt, err := moduleapi.RestoreModelUsageReceiptV1(content.canonical)
		if err != nil || !coreUsageMatchesReceipt(usage, receipt) {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q Usage receipt differs: %v", attempt.runID, attempt.attemptID, err)
		}
	}
	hasBillableFact := coreUsageHasBillableFact(usage)
	if hasBillableFact != usage.ledgerSequence.Valid {
		return coreUsageState{}, coreIntegrity("Run %q model Attempt %q Usage ledger allocation differs", attempt.runID, attempt.attemptID)
	}
	switch attempt.state {
	case corecontract.ModelAttemptPending:
		if attempt.providerRequestID.Valid || attempt.providerReceiptRef.Valid ||
			attempt.resultRef.Valid || attempt.errorClassification.Valid ||
			attempt.reconciliationEvidenceRef.Valid || attempt.unknownReason.Valid ||
			usage.revision != 0 || usage.ledgerSequence.Valid ||
			usage.rawReceiptRef.Valid || hasBillableFact || usage.status != coreUsagePending {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q PENDING Usage closure differs", attempt.runID, attempt.attemptID)
		}
	case corecontract.ModelAttemptSucceeded:
		if !attempt.resultRef.Valid || attempt.errorClassification.Valid ||
			attempt.unknownReason.Valid || usage.status != coreTerminalUsageStatus(usage) {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q SUCCEEDED Usage closure differs", attempt.runID, attempt.attemptID)
		}
	case corecontract.ModelAttemptFailed:
		if attempt.resultRef.Valid || !attempt.errorClassification.Valid ||
			attempt.unknownReason.Valid || usage.status != coreTerminalUsageStatus(usage) {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q FAILED Usage closure differs", attempt.runID, attempt.attemptID)
		}
	case corecontract.ModelAttemptUnknown:
		if attempt.resultRef.Valid || attempt.errorClassification.Valid ||
			(!attempt.providerRequestID.Valid && !attempt.providerReceiptRef.Valid &&
				!attempt.reconciliationEvidenceRef.Valid && !attempt.unknownReason.Valid) ||
			usage.status != coreUsagePendingReconciliation {
			return coreUsageState{}, coreIntegrity("Run %q model Attempt %q MODEL_UNKNOWN Usage closure differs", attempt.runID, attempt.attemptID)
		}
	}
	return usage, nil
}

func inspectCoreRunEvents(
	ctx context.Context,
	database semanticQueryer,
	runID string,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) ([]coreRunEventState, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			e.event_sequence, e.event_kind, e.from_revision, e.to_revision,
			e.payload_ref, e.payload_digest,
			c.kind, c.media_type, c.canonical_bytes, c.size_bytes
		FROM run_events AS e
		JOIN content_records AS c ON c.content_digest=e.payload_ref
		WHERE e.run_id=?
		ORDER BY e.event_sequence
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: read RunEvents: %w", err)
	}
	events := make([]coreRunEventState, 0)
	for rows.Next() {
		var event coreRunEventState
		var payloadRef, payloadDigest, kind, mediaType string
		var size int64
		if err := rows.Scan(
			&event.sequence,
			&event.kind,
			&event.fromRevision,
			&event.toRevision,
			&payloadRef,
			&payloadDigest,
			&kind,
			&mediaType,
			&event.payload,
			&size,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("currentbackup: scan RunEvent: %w", err)
		}
		computed, digestErr := currentstore.ComputeContentDigest(
			currentstore.ContentKind(kind),
			mediaType,
			event.payload,
		)
		if event.sequence != int64(len(events)) || payloadRef != payloadDigest ||
			kind != string(currentstore.ContentRunEventPayload) ||
			mediaType != coreJSONMediaType || int64(len(event.payload)) != size ||
			digestErr != nil || computed != payloadRef {
			_ = rows.Close()
			return nil, coreIntegrity("Run %q RunEvent %d content/sequence differs", runID, event.sequence)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("currentbackup: iterate RunEvents: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentbackup: close RunEvents: %w", err)
	}
	if len(events) == 0 {
		return nil, coreIntegrity("Run %q has no RunEvent zero", runID)
	}
	admitted, err := corecontract.RestoreRunAdmittedEventV1(events[0].payload)
	if err != nil || events[0].kind != corecontract.RunAdmittedEventKind ||
		events[0].fromRevision != 0 || events[0].toRevision != 0 ||
		admitted.RunID != runID || admitted.ManifestDigest != manifest.ManifestDigest ||
		admitted.MemberSnapshotDigest != member.MemberSnapshotDigest {
		return nil, coreIntegrity("Run %q RunEvent zero differs: %v", runID, err)
	}
	for index := 1; index < len(events); index++ {
		event := &events[index]
		previous := events[index-1]
		if event.fromRevision < previous.toRevision ||
			event.toRevision != event.fromRevision+1 {
			return nil, coreIntegrity(
				"Run %q RunEvent revision chain differs at %d: previous_to=%d from=%d to=%d",
				runID,
				index,
				previous.toRevision,
				event.fromRevision,
				event.toRevision,
			)
		}
		switch event.kind {
		case corecontract.ModelDispatchPendingEventKind,
			corecontract.ModelDispatchTerminalEventKind:
			modelEvent, err := corecontract.RestoreModelDispatchEventV1(event.payload)
			if err != nil {
				return nil, coreIntegrity("Run %q model RunEvent %d: %v", runID, index, err)
			}
			event.model = &modelEvent
		case corecontract.CoreDeterministicFailureEventKind:
			failure, err := corecontract.RestoreCoreDeterministicFailureEventV1(
				event.payload,
			)
			if err != nil || failure.RunID != runID {
				return nil, coreIntegrity(
					"Run %q deterministic Core failure RunEvent %d differs: %v",
					runID,
					index,
					err,
				)
			}
			event.coreFailure = &failure
		case corecontract.CompositeRepairActivatedEventKind:
			activated, err := corecontract.RestoreCompositeRepairActivatedEventV1(
				event.payload,
			)
			if err != nil || activated.RunID != runID {
				return nil, coreIntegrity(
					"Run %q repair activation RunEvent %d differs: %v",
					runID,
					index,
					err,
				)
			}
			event.repairActivated = &activated
		case corecontract.CompositeRepairSkippedEventKind:
			skipped, err := corecontract.RestoreCompositeRepairSkippedEventV1(
				event.payload,
			)
			if err != nil || skipped.RunID != runID {
				return nil, coreIntegrity(
					"Run %q repair skip RunEvent %d differs: %v",
					runID,
					index,
					err,
				)
			}
			event.repairSkipped = &skipped
		}
	}
	return events, nil
}

func inspectCoreHistory(
	ctx context.Context,
	database semanticQueryer,
	runID string,
	member corecontract.MemberExecutionSnapshot,
	attempts map[string]*coreModelAttemptState,
) (map[string]int, error) {
	type historyRow struct {
		sequence      int64
		memberID      string
		role          string
		contentRef    string
		contentDigest string
		sourceAttempt sql.NullString
	}
	rows, err := database.QueryContext(ctx, `
		SELECT
			history_sequence, member_id, role,
			content_ref, content_digest, source_attempt_id
		FROM history_entries
		WHERE run_id=?
		ORDER BY history_sequence
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: read History: %w", err)
	}
	pending := make([]historyRow, 0)
	for rows.Next() {
		var entry historyRow
		if err := rows.Scan(
			&entry.sequence,
			&entry.memberID,
			&entry.role,
			&entry.contentRef,
			&entry.contentDigest,
			&entry.sourceAttempt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("currentbackup: scan History: %w", err)
		}
		pending = append(pending, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("currentbackup: iterate History: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentbackup: close History: %w", err)
	}
	counts := make(map[string]int)
	for index, entry := range pending {
		attempt := attempts[entry.sourceAttempt.String]
		if entry.sequence != int64(index+1) || entry.memberID != member.MemberID ||
			entry.role != string(moduleapi.ModelRoleAssistant) ||
			entry.contentRef != entry.contentDigest || !entry.sourceAttempt.Valid || attempt == nil ||
			attempt.state != corecontract.ModelAttemptSucceeded ||
			!attempt.resultRef.Valid || attempt.resultRef.String != entry.contentRef {
			return nil, coreIntegrity("Run %q History sequence/source differs", runID)
		}
		if _, err := inspectCoreContent(
			ctx,
			database,
			entry.contentRef,
			currentstore.ContentModelResult,
		); err != nil {
			return nil, coreIntegrity("Run %q History content: %v", runID, err)
		}
		counts[entry.sourceAttempt.String]++
	}
	return counts, nil
}

func inspectCompositeFamilyClosures(
	ctx context.Context,
	database semanticQueryer,
	states map[string]*coreRunSemanticState,
) error {
	for _, run := range states {
		if run.manifest.Composite != nil || !run.row.cancelRequestRef.Valid {
			continue
		}
		content, err := inspectCoreContent(
			ctx,
			database,
			run.row.cancelRequestRef.String,
			currentstore.ContentRunCancellation,
		)
		if err != nil || content.mediaType != coreJSONMediaType {
			return coreIntegrity(
				"Run %q cancellation content differs: %v",
				run.row.runID,
				err,
			)
		}
		request, err := corecontract.RestoreRunCancellationRequestV1(
			content.canonical,
		)
		if err != nil || request.RootRunID != run.row.runID ||
			request.RootManifestDigest != run.manifest.ManifestDigest ||
			request.Scope != corecontract.CancellationScopeRunV1 {
			return coreIntegrity(
				"Run %q cancellation request differs: %v",
				run.row.runID,
				err,
			)
		}
	}
	for _, child := range states {
		if child.row.parentRunID.Valid {
			parent := states[child.row.parentRunID.String]
			if parent == nil || parent.manifest.Composite == nil ||
				parent.manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 {
				return coreIntegrity("Run %q names a non-Composite parent", child.row.runID)
			}
		}
	}
	for _, root := range states {
		composite := root.manifest.Composite
		if composite == nil || composite.Role != corecontract.CompositeRunRoleRootV1 {
			continue
		}
		if root.row.parentRunID.Valid || root.row.parentManifestDigest.Valid ||
			root.row.parentSlotID.Valid || composite.Plan == nil {
			return coreIntegrity("Composite root %q row identity differs", root.row.runID)
		}
		if composite.Plan.Decision != nil {
			if err := inspectCompositeDecisionFamilyClosure(
				ctx,
				database,
				states,
				root,
			); err != nil {
				return err
			}
			continue
		}
		family := []*coreRunSemanticState{root}
		children := make(
			[]*coreRunSemanticState,
			0,
			len(composite.Plan.Children),
		)
		for _, planned := range composite.Plan.Children {
			child := states[planned.RunID]
			if child == nil || child.manifest.Composite == nil ||
				child.manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
				child.row.parentRunID.String != root.row.runID ||
				child.row.parentManifestDigest.String != root.manifest.ManifestDigest ||
				child.row.parentSlotID.String != planned.SlotID ||
				child.manifest.ParentRunID != root.row.runID ||
				child.manifest.Composite.RootRunID != root.row.runID ||
				child.manifest.Composite.ParentManifestDigest != root.manifest.ManifestDigest ||
				child.manifest.Composite.ParentSlotID != planned.SlotID ||
				child.manifest.Composite.Assignment == nil ||
				*child.manifest.Composite.Assignment != planned.Assignment ||
				child.manifest.AdmissionKey != planned.AdmissionKey ||
				child.manifest.TaskInputRef != planned.TaskInputRef ||
				child.manifest.TaskInputRef != root.manifest.TaskInputRef ||
				child.row.tenantID != root.row.tenantID ||
				child.row.workspaceID != root.row.workspaceID ||
				child.member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
				child.member.Agent != planned.Agent || child.member.Profile != planned.Profile {
				return coreIntegrity("Composite root %q Child slot %q closure differs", root.row.runID, planned.SlotID)
			}
			family = append(family, child)
			children = append(children, child)
		}
		var reviewer *coreRunSemanticState
		if planned := composite.Plan.Reviewer; planned != nil {
			reviewer = states[planned.RunID]
			if reviewer == nil || reviewer.manifest.Composite == nil ||
				reviewer.manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
				reviewer.row.parentRunID.String != root.row.runID ||
				reviewer.row.parentManifestDigest.String != root.manifest.ManifestDigest ||
				reviewer.row.parentSlotID.String != corecontract.CompositeReviewerParentSlotIDV1 ||
				reviewer.manifest.ParentRunID != root.row.runID ||
				reviewer.manifest.Composite.RootRunID != root.row.runID ||
				reviewer.manifest.Composite.ParentManifestDigest != root.manifest.ManifestDigest ||
				reviewer.manifest.Composite.ParentSlotID != corecontract.CompositeReviewerParentSlotIDV1 ||
				reviewer.manifest.Composite.Assignment != nil ||
				reviewer.manifest.Composite.Plan != nil ||
				reviewer.manifest.AdmissionKey != planned.AdmissionKey ||
				reviewer.manifest.TaskInputRef != planned.TaskInputRef ||
				reviewer.manifest.TaskInputRef != root.manifest.TaskInputRef ||
				reviewer.row.tenantID != root.row.tenantID ||
				reviewer.row.workspaceID != root.row.workspaceID ||
				reviewer.member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
				reviewer.member.Agent != planned.Agent ||
				reviewer.member.Profile != planned.Profile {
				return coreIntegrity(
					"Composite root %q Reviewer closure differs",
					root.row.runID,
				)
			}
			family = append(family, reviewer)
		}
		actualMembers := 0
		for _, candidate := range states {
			if candidate.row.parentRunID.Valid && candidate.row.parentRunID.String == root.row.runID {
				actualMembers++
			}
		}
		expectedMembers := len(composite.Plan.Children)
		if composite.Plan.Reviewer != nil {
			expectedMembers++
		}
		if actualMembers != expectedMembers {
			return coreIntegrity(
				"Composite root %q member cardinality is %d, want %d",
				root.row.runID,
				actualMembers,
				expectedMembers,
			)
		}
		if err := inspectCompositeControlClosure(
			ctx,
			database,
			root,
			children,
			reviewer,
		); err != nil {
			return err
		}
		resultSet, resultDigest, resultSetErr :=
			coreCompositeSpecialistResultSet(root, children)
		if resultSetErr != nil && coreAllRunsHaveSuccessfulTerminalResults(children) {
			return resultSetErr
		}
		var (
			reviewerVerdict      corecontract.ReviewVerdictV1
			reviewerVerdictKnown bool
			reviewerVerdictErr   error
		)
		if reviewer != nil && coreRunHasSuccessfulTerminalResult(reviewer) {
			reviewerVerdict, reviewerVerdictErr =
				coreCompositeReviewerVerdict(root, reviewer, resultDigest)
			if reviewerVerdictErr == nil {
				reviewerVerdictKnown = true
			}
		}

		switch root.frame.continuation.CoreFailureReason {
		case "":
		case corecontract.AllRequiredChildFailedReasonV1:
			failed := false
			for _, child := range children {
				failed = failed || coreRunHasFailedTerminalResult(child)
			}
			if !failed {
				return coreIntegrity(
					"Composite root %q ALL_REQUIRED_CHILD_FAILED has no failed Child",
					root.row.runID,
				)
			}
		case corecontract.CompositeChildResultOverBudgetReasonV1:
			for _, child := range children {
				if !coreRunHasSuccessfulTerminalResult(child) {
					return coreIntegrity(
						"Composite root %q COMPOSITE_CHILD_RESULT_OVER_BUDGET lacks all successful Children",
						root.row.runID,
					)
				}
			}
			if reviewer != nil && (!reviewerVerdictKnown ||
				reviewerVerdict.Decision != corecontract.ReviewDecisionApproveV1) {
				return coreIntegrity(
					"Composite root %q COMPOSITE_CHILD_RESULT_OVER_BUDGET lacks Reviewer APPROVE",
					root.row.runID,
				)
			}
		case corecontract.CompositeReviewRejectedReasonV1:
			if reviewer == nil || reviewerVerdictErr != nil ||
				!reviewerVerdictKnown ||
				reviewerVerdict.Decision != corecontract.ReviewDecisionRejectV1 {
				return coreIntegrity(
					"Composite root %q COMPOSITE_REVIEW_REJECTED lacks an exact Reviewer REJECT",
					root.row.runID,
				)
			}
		case corecontract.CompositeReviewFailedReasonV1:
			if reviewer == nil || !coreCompositeReviewerFailed(reviewer, false) {
				return coreIntegrity(
					"Composite root %q COMPOSITE_REVIEW_FAILED lacks a failed Reviewer",
					root.row.runID,
				)
			}
		case corecontract.CompositeReviewOutputInvalidReasonV1:
			if reviewer == nil || !coreCompositeReviewerFailed(reviewer, true) {
				return coreIntegrity(
					"Composite root %q COMPOSITE_REVIEW_OUTPUT_INVALID lacks an invalid Reviewer output",
					root.row.runID,
				)
			}
		default:
			return coreIntegrity(
				"Composite root %q deterministic Core failure reason differs",
				root.row.runID,
			)
		}

		attemptCount := 0
		for _, member := range family {
			attemptCount += len(member.attempts)
		}
		if attemptCount > int(composite.Plan.FamilyModelDispatchLimit) {
			return coreIntegrity("Composite root %q model Attempt count %d exceeds family limit %d", root.row.runID, attemptCount, composite.Plan.FamilyModelDispatchLimit)
		}
		if len(root.attempts) > 1 {
			return coreIntegrity("Composite root %q has more than one merge Attempt", root.row.runID)
		}
		for _, attempt := range root.attempts {
			if attempt.logicalStepID != corecontract.CompositeMergeLogicalStepIDV1 {
				return coreIntegrity("Composite root %q has a non-merge model Attempt", root.row.runID)
			}
			for _, child := range children {
				if !coreRunHasSuccessfulTerminalResult(child) {
					return coreIntegrity("Composite root %q merge precedes Child success", root.row.runID)
				}
			}
			if reviewer != nil && (!reviewerVerdictKnown ||
				reviewerVerdict.Decision != corecontract.ReviewDecisionApproveV1) {
				return coreIntegrity(
					"Composite root %q merge lacks an exact Reviewer APPROVE",
					root.row.runID,
				)
			}
			if err := inspectCompositeRootAttemptClosure(
				root,
				children,
				reviewer,
				resultDigest,
				attempt,
			); err != nil {
				return err
			}
		}
		for _, child := range children {
			if len(child.attempts) > 1 {
				return coreIntegrity("Composite Child %q has more than one model Attempt", child.row.runID)
			}
			for _, attempt := range child.attempts {
				if err := inspectCompositeChildAttemptClosure(child, attempt); err != nil {
					return err
				}
			}
		}
		if reviewer != nil {
			if len(reviewer.attempts) > 1 {
				return coreIntegrity(
					"Composite Reviewer %q has more than one model Attempt",
					reviewer.row.runID,
				)
			}
			for _, attempt := range reviewer.attempts {
				if attempt.logicalStepID != corecontract.CompositeReviewLogicalStepIDV1 {
					return coreIntegrity(
						"Composite Reviewer %q has a non-review model Attempt",
						reviewer.row.runID,
					)
				}
				if !coreAllRunsHaveSuccessfulTerminalResults(children) {
					return coreIntegrity(
						"Composite Reviewer %q Attempt precedes Specialist success",
						reviewer.row.runID,
					)
				}
				if err := inspectCompositeReviewerAttemptClosure(
					root,
					children,
					resultSet,
					resultDigest,
					reviewer,
					attempt,
				); err != nil {
					return err
				}
			}
		}

		latchRef := family[0].row.cancelRequestRef
		for _, member := range family[1:] {
			if member.row.cancelRequestRef != latchRef {
				return coreIntegrity("Composite root %q has a partial or different cancellation latch", root.row.runID)
			}
		}
		if latchRef.Valid {
			content, err := inspectCoreContent(
				ctx,
				database,
				latchRef.String,
				currentstore.ContentRunCancellation,
			)
			if err != nil || content.mediaType != coreJSONMediaType {
				return coreIntegrity("Composite root %q cancellation content differs: %v", root.row.runID, err)
			}
			request, err := corecontract.RestoreRunCancellationRequestV1(content.canonical)
			if err != nil || request.RootRunID != root.row.runID ||
				request.RootManifestDigest != root.manifest.ManifestDigest ||
				request.Scope != corecontract.CancellationScopeFamilyV1 {
				return coreIntegrity("Composite root %q cancellation request differs: %v", root.row.runID, err)
			}
		}
	}
	return nil
}

func inspectCompositeControlClosure(
	ctx context.Context,
	database semanticQueryer,
	root *coreRunSemanticState,
	children []*coreRunSemanticState,
	reviewer *coreRunSemanticState,
) error {
	if root == nil || root.manifest.Composite == nil ||
		root.manifest.Composite.Plan == nil {
		return coreIntegrity("Composite Control root closure is absent")
	}
	var (
		catalogTenant, controlSnapshotID, catalogDigest string
		catalogGeneration                               int64
		catalogCanonical                                []byte
		controlTenant, controlDigest                    string
		controlRevision                                 int64
		controlCanonical                                []byte
	)
	if err := database.QueryRowContext(ctx, `
		SELECT
			catalog.tenant_id, catalog.generation,
			catalog.control_snapshot_id, catalog.canonical_json,
			catalog.digest,
			control.tenant_id, control.revision,
			control.canonical_json, control.digest
		FROM runtime_catalog_generations AS catalog
		JOIN control_snapshots AS control
		  ON control.snapshot_id=catalog.control_snapshot_id
		WHERE catalog.generation_id=?
	`, root.member.Catalog.ID).Scan(
		&catalogTenant,
		&catalogGeneration,
		&controlSnapshotID,
		&catalogCanonical,
		&catalogDigest,
		&controlTenant,
		&controlRevision,
		&controlCanonical,
		&controlDigest,
	); err != nil {
		return coreIntegrity(
			"Composite root %q frozen Control/Catalog is absent: %v",
			root.row.runID,
			err,
		)
	}
	if catalogGeneration <= 0 || controlRevision <= 0 ||
		root.member.Catalog.Version != strconv.FormatInt(catalogGeneration, 10) ||
		root.member.Catalog.Digest != catalogDigest ||
		catalogTenant != root.row.tenantID || controlTenant != root.row.tenantID {
		return coreIntegrity(
			"Composite root %q frozen Control/Catalog identity differs",
			root.row.runID,
		)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		catalogCanonical,
		controlcontract.CatalogGenerationRef{
			GenerationID: root.member.Catalog.ID,
			Generation:   uint64(catalogGeneration),
			Digest:       catalogDigest,
		},
	)
	if err != nil || catalog.TenantID != root.row.tenantID ||
		catalog.ControlSnapshotID != controlSnapshotID ||
		catalog.ControlSnapshotDigest != controlDigest {
		return coreIntegrity(
			"Composite root %q frozen Catalog closure differs: %v",
			root.row.runID,
			err,
		)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		controlcontract.ControlSnapshotRef{
			SnapshotID: controlSnapshotID,
			Revision:   uint64(controlRevision),
			Digest:     controlDigest,
		},
	)
	if err != nil || control.TenantID != root.row.tenantID {
		return coreIntegrity(
			"Composite root %q frozen Control closure differs: %v",
			root.row.runID,
			err,
		)
	}
	definition, found := control.FindCompositeAgent(root.member.Agent.ID)
	if !found || definition.CoordinatorProfileID != root.member.Profile.ID ||
		len(definition.Members) != len(children) ||
		(definition.Decision == nil) !=
			(root.manifest.Composite.Plan.Decision == nil) {
		return coreIntegrity(
			"Composite root %q Control definition differs",
			root.row.runID,
		)
	}
	plan := root.manifest.Composite.Plan
	for index, child := range children {
		planned := plan.Children[index]
		configured, present := definition.FindMember(planned.SlotID)
		agent, agentPresent := control.FindAgent(configured.AgentID)
		profile, profilePresent := control.FindProfile(configured.ProfileID)
		if !present || !agentPresent || !profilePresent ||
			child.member.Catalog != root.member.Catalog ||
			planned.Agent != agent || planned.Profile != profile.Profile ||
			planned.Assignment != (corecontract.CompositeAssignmentV1{
				SlotID:            configured.SlotID,
				FocusID:           configured.FocusID,
				WeightBasisPoints: configured.WeightBasisPoints,
			}) {
			return coreIntegrity(
				"Composite root %q Child slot %q differs from frozen Control",
				root.row.runID,
				planned.SlotID,
			)
		}
	}
	if definition.Reviewer == nil {
		if plan.Reviewer != nil || reviewer != nil {
			return coreIntegrity(
				"Composite root %q Reviewer-disabled Control differs",
				root.row.runID,
			)
		}
		return nil
	}
	planned := plan.Reviewer
	configured := definition.Reviewer
	if planned == nil || reviewer == nil {
		return coreIntegrity(
			"Composite root %q Reviewer-enabled Control lacks its Run",
			root.row.runID,
		)
	}
	agent, agentPresent := control.FindAgent(configured.AgentID)
	profile, profilePresent := control.FindProfile(configured.ProfileID)
	if !agentPresent || !profilePresent ||
		reviewer.member.Catalog != root.member.Catalog ||
		planned.Agent != agent || planned.Profile != profile.Profile ||
		planned.Policy != configured.Policy ||
		planned.MaxOutputTokens != configured.MaxOutputTokens ||
		planned.ReviewLogicalStepID != corecontract.CompositeReviewLogicalStepIDV1 {
		return coreIntegrity(
			"Composite root %q Reviewer differs from frozen Control",
			root.row.runID,
		)
	}
	return nil
}

const (
	coreCompositeAssignmentPrefixV1        = "COMPOSITE_SPECIALIST_ASSIGNMENT_JSON:\n"
	coreCompositeAssignmentSchemaVersionV1 = "composite-specialist-assignment-context/v1"
	coreCompositeResultPrefixV1            = "UNTRUSTED_COMPOSITE_CHILD_RESULT_JSON:\n"
	coreCompositeResultSchemaVersionV1     = "composite-child-result-context/v1"
	coreCompositeReviewerPolicyPrefixV1    = "COMPOSITE_REVIEW_POLICY_JSON:\n"
	coreCompositeReviewerPolicySchemaV1    = "composite-review-policy-context/v1"
	coreCompositeReviewerResultPrefixV1    = "UNTRUSTED_COMPOSITE_SPECIALIST_RESULT_JSON:\n"
	coreCompositeReviewerResultSchemaV1    = "composite-review-specialist-result-context/v1"
	coreCompositeReviewVerdictPrefixV1     = "UNTRUSTED_COMPOSITE_REVIEW_VERDICT_JSON:\n"
)

type coreCompositeAssignmentEnvelopeV1 struct {
	SchemaVersion string `json:"schema_version"`
	SlotID        string `json:"slot_id"`
	FocusID       string `json:"focus_id"`
}

type coreCompositeResultEnvelopeV1 struct {
	SchemaVersion string `json:"schema_version"`
	SlotID        string `json:"slot_id"`
	FocusID       string `json:"focus_id"`
	Result        string `json:"result"`
}

type coreCompositeReviewerPolicyEnvelopeV1 struct {
	SchemaVersion          string   `json:"schema_version"`
	Policy                 string   `json:"policy"`
	FamilyDigest           string   `json:"family_digest"`
	SpecialistResultDigest string   `json:"specialist_result_digest"`
	OutputSchemaVersion    string   `json:"output_schema_version"`
	RequiredFields         []string `json:"required_fields"`
	AllowedDecisions       []string `json:"allowed_decisions"`
	AllowedIssueCodes      []string `json:"allowed_issue_codes"`
	OutputRules            []string `json:"output_rules"`
}

type coreCompositeReviewerResultEnvelopeV1 struct {
	SchemaVersion         string `json:"schema_version"`
	SlotID                string `json:"slot_id"`
	FocusID               string `json:"focus_id"`
	WeightBasisPoints     uint32 `json:"weight_basis_points"`
	RunID                 string `json:"run_id"`
	ManifestDigest        string `json:"manifest_digest"`
	MemberSnapshotDigest  string `json:"member_snapshot_digest"`
	ResultRef             string `json:"result_ref"`
	TerminalRunRevision   uint64 `json:"terminal_run_revision"`
	TerminalFrameRevision uint64 `json:"terminal_frame_revision"`
	Result                string `json:"result"`
}

func inspectCompositeChildAttemptClosure(
	child *coreRunSemanticState,
	attempt *coreModelAttemptState,
) error {
	if attempt.compilation == nil || attempt.compilation.Composite == nil ||
		attempt.compilation.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		attempt.compilation.Composite.Assignment == nil ||
		child.manifest.Composite == nil ||
		child.manifest.Composite.Assignment == nil ||
		*attempt.compilation.Composite.Assignment != *child.manifest.Composite.Assignment ||
		len(attempt.compilation.Composite.ChildResults) != 0 {
		return coreIntegrity(
			"Composite Child %q Attempt %q compilation assignment differs",
			child.row.runID,
			attempt.attemptID,
		)
	}
	want, err := coreCompositeAssignmentMessage(*child.manifest.Composite.Assignment)
	if err != nil {
		return coreIntegrity(
			"Composite Child %q Attempt %q assignment message: %v",
			child.row.runID,
			attempt.attemptID,
			err,
		)
	}
	if countCoreModelMessage(attempt.request.Messages, want) != 1 {
		return coreIntegrity(
			"Composite Child %q Attempt %q MODEL_REQUEST lacks its exact assignment",
			child.row.runID,
			attempt.attemptID,
		)
	}
	return nil
}

func coreCompositeSpecialistResultSet(
	root *coreRunSemanticState,
	children []*coreRunSemanticState,
) (corecontract.CompositeSpecialistResultSetV1, string, error) {
	if root == nil || root.manifest.Composite == nil ||
		root.manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.manifest.Composite.Plan == nil ||
		len(children) != len(root.manifest.Composite.Plan.Children) {
		return corecontract.CompositeSpecialistResultSetV1{}, "",
			coreIntegrity("Composite Specialist result-set root closure differs")
	}
	results := make(
		[]corecontract.CompositeSpecialistResultV1,
		len(children),
	)
	for index, child := range children {
		planned := root.manifest.Composite.Plan.Children[index]
		terminal := coreSuccessfulTerminalAttempt(child)
		terminalFrameRevision, frameErr := coreTerminalFrameRevision(child)
		if terminal == nil || child.manifest.Composite == nil ||
			child.manifest.Composite.Assignment == nil ||
			planned.RunID != child.row.runID ||
			planned.Assignment != *child.manifest.Composite.Assignment ||
			frameErr != nil {
			return corecontract.CompositeSpecialistResultSetV1{}, "",
				coreIntegrity(
					"Composite root %q Specialist result %d is not exact",
					root.row.runID,
					index,
				)
		}
		results[index] = corecontract.CompositeSpecialistResultV1{
			SlotID:                planned.SlotID,
			FocusID:               planned.Assignment.FocusID,
			WeightBasisPoints:     planned.Assignment.WeightBasisPoints,
			RunID:                 child.row.runID,
			ManifestDigest:        child.manifest.ManifestDigest,
			MemberSnapshotDigest:  child.member.MemberSnapshotDigest,
			ResultRef:             terminal.resultRef.String,
			TerminalRunRevision:   uint64(child.row.revision),
			TerminalFrameRevision: terminalFrameRevision,
		}
	}
	frozen, _, digest, err := corecontract.NewCompositeSpecialistResultSetV1(
		corecontract.CompositeSpecialistResultSetV1{
			SchemaVersion: corecontract.CompositeSpecialistResultSetSchemaVersionV1,
			FamilyDigest:  root.manifest.ManifestDigest,
			TaskInputRef:  root.manifest.TaskInputRef,
			Results:       results,
		},
	)
	if err != nil {
		return corecontract.CompositeSpecialistResultSetV1{}, "",
			coreIntegrity(
				"Composite root %q Specialist result set differs: %v",
				root.row.runID,
				err,
			)
	}
	return frozen, digest, nil
}

func coreCompositeReviewerVerdict(
	root *coreRunSemanticState,
	reviewer *coreRunSemanticState,
	specialistResultDigest string,
) (corecontract.ReviewVerdictV1, error) {
	terminal := coreSuccessfulTerminalAttempt(reviewer)
	if terminal == nil || terminal.result == nil ||
		terminal.result.ActionRequest != nil {
		return corecontract.ReviewVerdictV1{}, coreIntegrity(
			"Composite Reviewer %q has no strict successful verdict",
			reviewer.row.runID,
		)
	}
	verdict, canonical, err := corecontract.ParseReviewVerdictV1(
		[]byte(terminal.result.AssistantText),
	)
	if err != nil || !bytes.Equal(canonical, []byte(terminal.result.AssistantText)) {
		return corecontract.ReviewVerdictV1{}, coreIntegrity(
			"Composite Reviewer %q verdict is not canonical: %v",
			reviewer.row.runID,
			err,
		)
	}
	if err := verdict.ValidateForCompositeReviewV1(
		root.manifest,
		specialistResultDigest,
	); err != nil {
		return corecontract.ReviewVerdictV1{}, coreIntegrity(
			"Composite Reviewer %q verdict does not bind its family: %v",
			reviewer.row.runID,
			err,
		)
	}
	return verdict, nil
}

func coreAllRunsHaveSuccessfulTerminalResults(
	runs []*coreRunSemanticState,
) bool {
	for _, run := range runs {
		if !coreRunHasSuccessfulTerminalResult(run) {
			return false
		}
	}
	return true
}

func coreTerminalFrameRevision(
	run *coreRunSemanticState,
) (uint64, error) {
	if run == nil ||
		run.frame.continuation.State != corecontract.TerminatedLoopStep ||
		len(run.events) == 0 {
		return 0, coreIntegrity("Composite terminal Frame revision is absent")
	}
	revision := run.events[len(run.events)-1].toRevision
	if revision <= 0 || revision > run.frame.revision {
		return 0, coreIntegrity(
			"Composite Run %q terminal Frame revision differs",
			run.row.runID,
		)
	}
	return uint64(revision), nil
}

func coreCompositeReviewerFailed(
	reviewer *coreRunSemanticState,
	requireInvalidOutput bool,
) bool {
	if reviewer == nil ||
		reviewer.frame.continuation.State != corecontract.TerminatedLoopStep {
		return false
	}
	if reviewer.frame.continuation.CoreFailureReason != "" {
		return !requireInvalidOutput &&
			reviewer.frame.continuation.CoreFailureReason ==
				corecontract.CompositeReviewFailedReasonV1
	}
	if reviewer.frame.continuation.AttemptKind != corecontract.AttemptKindModel {
		return false
	}
	attempt := reviewer.attempts[reviewer.frame.continuation.AttemptID]
	if attempt == nil || attempt.state != corecontract.ModelAttemptFailed ||
		attempt.resultRef.Valid || !attempt.errorClassification.Valid {
		return false
	}
	invalid := attempt.errorClassification.String ==
		corecontract.CompositeReviewOutputInvalidReasonV1
	if requireInvalidOutput {
		return invalid
	}
	return !invalid
}

func inspectCompositeReviewerAttemptClosure(
	root *coreRunSemanticState,
	children []*coreRunSemanticState,
	set corecontract.CompositeSpecialistResultSetV1,
	setDigest string,
	reviewer *coreRunSemanticState,
	attempt *coreModelAttemptState,
) error {
	if root == nil || reviewer == nil || attempt == nil ||
		root.manifest.Composite == nil || root.manifest.Composite.Plan == nil ||
		root.manifest.Composite.Plan.Reviewer == nil ||
		attempt.compilation == nil || attempt.compilation.Composite == nil ||
		attempt.compilation.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		attempt.compilation.Composite.Assignment != nil ||
		attempt.compilation.Composite.ReviewVerdict != nil ||
		attempt.compilation.Composite.SpecialistResultDigest != setDigest ||
		len(attempt.compilation.Composite.ChildResults) != len(children) {
		return coreIntegrity(
			"Composite Reviewer %q Attempt %q compilation closure differs",
			reviewer.row.runID,
			attempt.attemptID,
		)
	}
	tightened, err := corecontract.TightenReviewerModelParametersV1(
		attempt.request.Parameters,
		root.manifest.Composite.Plan.Reviewer.MaxOutputTokens,
	)
	if err != nil || !bytes.Equal(tightened, attempt.request.Parameters) {
		return coreIntegrity(
			"Composite Reviewer %q Attempt %q output ceiling differs: %v",
			reviewer.row.runID,
			attempt.attemptID,
			err,
		)
	}
	policyMessage, err := coreCompositeReviewerPolicyMessage(set, setDigest)
	if err != nil || countCoreModelMessage(attempt.request.Messages, policyMessage) != 1 {
		return coreIntegrity(
			"Composite Reviewer %q Attempt %q lacks its exact review policy: %v",
			reviewer.row.runID,
			attempt.attemptID,
			err,
		)
	}
	wantMessages := make([]moduleapi.ModelMessageV1, len(children))
	for index, child := range children {
		terminal := coreSuccessfulTerminalAttempt(child)
		evidence := attempt.compilation.Composite.ChildResults[index]
		result := set.Results[index]
		if terminal == nil || evidence.RunID != child.row.runID ||
			evidence.ChildManifestDigest != child.manifest.ManifestDigest ||
			evidence.MemberSnapshotDigest != child.member.MemberSnapshotDigest ||
			evidence.ResultRef != terminal.resultRef.String ||
			evidence.TerminalRevision != uint64(child.row.revision) ||
			evidence.Assignment.SlotID != result.SlotID ||
			evidence.Assignment.FocusID != result.FocusID ||
			evidence.Assignment.WeightBasisPoints != result.WeightBasisPoints ||
			evidence.Truncated || evidence.OriginalBytes != evidence.RetainedBytes {
			return coreIntegrity(
				"Composite Reviewer %q Specialist evidence %d differs",
				reviewer.row.runID,
				index,
			)
		}
		message, messageErr := coreCompositeReviewerResultMessage(
			result,
			terminal.result.AssistantText,
		)
		if messageErr != nil ||
			evidence.OriginalBytes != uint64(len(message.Content)) {
			return coreIntegrity(
				"Composite Reviewer %q Specialist message %d differs: %v",
				reviewer.row.runID,
				index,
				messageErr,
			)
		}
		wantMessages[index] = message
	}
	if !containsCoreModelMessageSequenceExactlyOnce(
		attempt.request.Messages,
		wantMessages,
	) {
		return coreIntegrity(
			"Composite Reviewer %q Attempt %q lacks ordered Specialist results",
			reviewer.row.runID,
			attempt.attemptID,
		)
	}
	if attempt.state == corecontract.ModelAttemptSucceeded {
		if _, err := coreCompositeReviewerVerdict(root, reviewer, setDigest); err != nil {
			return err
		}
	}
	return nil
}

func inspectCompositeRootAttemptClosure(
	root *coreRunSemanticState,
	children []*coreRunSemanticState,
	reviewer *coreRunSemanticState,
	specialistResultDigest string,
	attempt *coreModelAttemptState,
) error {
	if attempt.compilation == nil || attempt.compilation.Composite == nil ||
		attempt.compilation.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		attempt.compilation.Composite.Assignment != nil ||
		len(attempt.compilation.Composite.ChildResults) != len(children) {
		return coreIntegrity(
			"Composite root %q Attempt %q compilation result cardinality differs",
			root.row.runID,
			attempt.attemptID,
		)
	}
	wantMessages := make([]moduleapi.ModelMessageV1, len(children))
	for index, child := range children {
		terminal := coreSuccessfulTerminalAttempt(child)
		if terminal == nil || child.frame.revision <= 0 ||
			child.manifest.Composite == nil ||
			child.manifest.Composite.Assignment == nil {
			return coreIntegrity(
				"Composite root %q Child %q has no exact terminal merge input",
				root.row.runID,
				child.row.runID,
			)
		}
		evidence := attempt.compilation.Composite.ChildResults[index]
		assignment := *child.manifest.Composite.Assignment
		if evidence.RunID != child.row.runID ||
			evidence.ChildManifestDigest != child.manifest.ManifestDigest ||
			evidence.MemberSnapshotDigest != child.member.MemberSnapshotDigest ||
			evidence.ResultRef != terminal.resultRef.String ||
			evidence.TerminalRevision != uint64(child.row.revision) ||
			evidence.Assignment != assignment || evidence.Truncated ||
			evidence.OriginalBytes != evidence.RetainedBytes {
			return coreIntegrity(
				"Composite root %q Child slot %q compilation evidence differs",
				root.row.runID,
				assignment.SlotID,
			)
		}
		message, err := coreCompositeResultMessage(
			assignment,
			terminal.result.AssistantText,
		)
		if err != nil {
			return coreIntegrity(
				"Composite root %q Child slot %q result message: %v",
				root.row.runID,
				assignment.SlotID,
				err,
			)
		}
		if evidence.OriginalBytes != uint64(len(message.Content)) {
			return coreIntegrity(
				"Composite root %q Child slot %q result byte evidence differs",
				root.row.runID,
				assignment.SlotID,
			)
		}
		wantMessages[index] = message
	}
	if reviewer == nil {
		if attempt.compilation.Composite.SpecialistResultDigest != "" ||
			attempt.compilation.Composite.ReviewVerdict != nil {
			return coreIntegrity(
				"Composite root %q Reviewer-disabled Attempt %q carries review evidence",
				root.row.runID,
				attempt.attemptID,
			)
		}
	} else {
		reviewerAttempt := coreSuccessfulTerminalAttempt(reviewer)
		reviewerTerminalFrameRevision, reviewerFrameErr :=
			coreTerminalFrameRevision(reviewer)
		evidence := attempt.compilation.Composite.ReviewVerdict
		if reviewerAttempt == nil || reviewerFrameErr != nil || evidence == nil ||
			attempt.compilation.Composite.SpecialistResultDigest != specialistResultDigest ||
			evidence.ReviewerRunID != reviewer.row.runID ||
			evidence.ReviewerManifestDigest != reviewer.manifest.ManifestDigest ||
			evidence.MemberSnapshotDigest != reviewer.member.MemberSnapshotDigest ||
			evidence.AttemptID != reviewerAttempt.attemptID ||
			evidence.LogicalStepID != corecontract.CompositeReviewLogicalStepIDV1 ||
			evidence.ResultRef != reviewerAttempt.resultRef.String ||
			evidence.TerminalRunRevision != uint64(reviewer.row.revision) ||
			evidence.TerminalFrameRevision != reviewerTerminalFrameRevision ||
			evidence.SpecialistResultDigest != specialistResultDigest ||
			evidence.Decision != corecontract.ReviewDecisionApproveV1 {
			return coreIntegrity(
				"Composite root %q Attempt %q Reviewer evidence differs",
				root.row.runID,
				attempt.attemptID,
			)
		}
		verdict, canonical, err := corecontract.ParseReviewVerdictV1(
			[]byte(reviewerAttempt.result.AssistantText),
		)
		if err != nil || !bytes.Equal(
			canonical,
			[]byte(reviewerAttempt.result.AssistantText),
		) || verdict.Decision != corecontract.ReviewDecisionApproveV1 ||
			verdict.SpecialistResultDigest != specialistResultDigest ||
			verdict.FamilyDigest != root.manifest.ManifestDigest {
			return coreIntegrity(
				"Composite root %q Attempt %q Reviewer verdict differs: %v",
				root.row.runID,
				attempt.attemptID,
				err,
			)
		}
		wantMessages = append(wantMessages, moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser,
			Content: coreCompositeReviewVerdictPrefixV1 +
				string(canonical),
		})
	}
	if !containsCoreModelMessageSequenceExactlyOnce(
		attempt.request.Messages,
		wantMessages,
	) {
		return coreIntegrity(
			"Composite root %q Attempt %q MODEL_REQUEST lacks the exact ordered Child results",
			root.row.runID,
			attempt.attemptID,
		)
	}
	return nil
}

func coreCompositeAssignmentMessage(
	assignment corecontract.CompositeAssignmentV1,
) (moduleapi.ModelMessageV1, error) {
	return coreCompositeJSONMessage(
		moduleapi.ModelRoleSystem,
		coreCompositeAssignmentPrefixV1,
		coreCompositeAssignmentEnvelopeV1{
			SchemaVersion: coreCompositeAssignmentSchemaVersionV1,
			SlotID:        assignment.SlotID,
			FocusID:       assignment.FocusID,
		},
	)
}

func coreCompositeResultMessage(
	assignment corecontract.CompositeAssignmentV1,
	result string,
) (moduleapi.ModelMessageV1, error) {
	return coreCompositeJSONMessage(
		moduleapi.ModelRoleUser,
		coreCompositeResultPrefixV1,
		coreCompositeResultEnvelopeV1{
			SchemaVersion: coreCompositeResultSchemaVersionV1,
			SlotID:        assignment.SlotID,
			FocusID:       assignment.FocusID,
			Result:        result,
		},
	)
}

func coreCompositeReviewerPolicyMessage(
	set corecontract.CompositeSpecialistResultSetV1,
	digest string,
) (moduleapi.ModelMessageV1, error) {
	return coreCompositeJSONMessage(
		moduleapi.ModelRoleSystem,
		coreCompositeReviewerPolicyPrefixV1,
		coreCompositeReviewerPolicyEnvelopeV1{
			SchemaVersion:          coreCompositeReviewerPolicySchemaV1,
			Policy:                 corecontract.CompositeReviewerPolicyResultsGateV1,
			FamilyDigest:           set.FamilyDigest,
			SpecialistResultDigest: digest,
			OutputSchemaVersion:    corecontract.ReviewVerdictSchemaVersionV1,
			RequiredFields: []string{
				"schema_version",
				"family_digest",
				"specialist_result_digest",
				"decision",
				"issue_codes",
				"affected_slot_ids",
				"bounded_reason",
			},
			AllowedDecisions: []string{
				string(corecontract.ReviewDecisionApproveV1),
				string(corecontract.ReviewDecisionRejectV1),
			},
			AllowedIssueCodes: []string{
				string(corecontract.ReviewIssueContradictionV1),
				string(corecontract.ReviewIssueIncompleteCoverageV1),
				string(corecontract.ReviewIssueMissingEvidenceV1),
				string(corecontract.ReviewIssueScopeMismatchV1),
				string(corecontract.ReviewIssueSecurityConcernV1),
				string(corecontract.ReviewIssueUnsupportedClaimV1),
			},
			OutputRules: []string{
				"Return exactly one JSON object with no code fence, prefix, suffix, or additional text.",
				"Copy family_digest and specialist_result_digest exactly from this policy.",
				"Use a non-empty bounded_reason of at most 4096 UTF-8 bytes.",
				"For APPROVE, issue_codes and affected_slot_ids must both be empty arrays.",
				"For REJECT, issue_codes and affected_slot_ids must both be non-empty arrays.",
				"Sort both arrays by binary string order, remove duplicates, and use only listed issue codes and Specialist slot_id values.",
			},
		},
	)
}

func coreCompositeReviewerResultMessage(
	result corecontract.CompositeSpecialistResultV1,
	assistantText string,
) (moduleapi.ModelMessageV1, error) {
	return coreCompositeJSONMessage(
		moduleapi.ModelRoleUser,
		coreCompositeReviewerResultPrefixV1,
		coreCompositeReviewerResultEnvelopeV1{
			SchemaVersion:         coreCompositeReviewerResultSchemaV1,
			SlotID:                result.SlotID,
			FocusID:               result.FocusID,
			WeightBasisPoints:     result.WeightBasisPoints,
			RunID:                 result.RunID,
			ManifestDigest:        result.ManifestDigest,
			MemberSnapshotDigest:  result.MemberSnapshotDigest,
			ResultRef:             result.ResultRef,
			TerminalRunRevision:   result.TerminalRunRevision,
			TerminalFrameRevision: result.TerminalFrameRevision,
			Result:                assistantText,
		},
	)
}

func coreCompositeJSONMessage(
	role moduleapi.ModelMessageRole,
	prefix string,
	envelope any,
) (moduleapi.ModelMessageV1, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return moduleapi.ModelMessageV1{}, err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return moduleapi.ModelMessageV1{}, err
	}
	return moduleapi.ModelMessageV1{
		Role:    role,
		Content: prefix + string(canonical),
	}, nil
}

func countCoreModelMessage(
	messages []moduleapi.ModelMessageV1,
	want moduleapi.ModelMessageV1,
) int {
	count := 0
	for _, message := range messages {
		if message == want {
			count++
		}
	}
	return count
}

func containsCoreModelMessageSequenceExactlyOnce(
	messages []moduleapi.ModelMessageV1,
	want []moduleapi.ModelMessageV1,
) bool {
	if len(want) == 0 || len(messages) < len(want) {
		return false
	}
	matches := 0
	for index := 0; index+len(want) <= len(messages); index++ {
		matched := true
		for offset := range want {
			if messages[index+offset] != want[offset] {
				matched = false
				break
			}
		}
		if matched {
			matches++
		}
	}
	return matches == 1
}

func validateCoreRunProjection(run *coreRunSemanticState) error {
	if len(run.events) == 0 ||
		run.frame.lastAuthoritativeEvent != int64(len(run.events)-1) {
		return coreIntegrity(
			"Run %q RunEvent head differs: event_count=%d last_event=%d",
			run.row.runID,
			len(run.events),
			run.frame.lastAuthoritativeEvent,
		)
	}
	statesByAttempt := make(map[string]corecontract.ModelAttemptState)
	seenPending := make(map[string]bool)
	for _, event := range run.events[1:] {
		if event.model == nil {
			continue
		}
		modelEvent := *event.model
		attempt := run.attempts[modelEvent.AttemptID]
		if attempt == nil || modelEvent.RunID != run.row.runID ||
			modelEvent.LogicalStepID != attempt.logicalStepID ||
			modelEvent.LogicalOperationKey != attempt.logicalOperationKey ||
			modelEvent.RequestDigest != attempt.requestDigest ||
			(modelEvent.ResultDigest != "" &&
				(!attempt.resultRef.Valid || modelEvent.ResultDigest != attempt.resultRef.String)) {
			return coreIntegrity("Run %q model RunEvent %q closure differs", run.row.runID, modelEvent.AttemptID)
		}
		previous, found := statesByAttempt[modelEvent.AttemptID]
		if !found {
			if modelEvent.State != corecontract.ModelAttemptPending ||
				event.kind != corecontract.ModelDispatchPendingEventKind ||
				event.fromRevision != attempt.frameRevision {
				return coreIntegrity("Run %q model Attempt %q has no canonical pending event", run.row.runID, modelEvent.AttemptID)
			}
			seenPending[modelEvent.AttemptID] = true
		} else if event.kind != corecontract.ModelDispatchTerminalEventKind ||
			!previous.AllowsTransition(modelEvent.State) {
			return coreIntegrity("Run %q model Attempt %q event transition differs", run.row.runID, modelEvent.AttemptID)
		}
		statesByAttempt[modelEvent.AttemptID] = modelEvent.State
	}
	for attemptID, attempt := range run.attempts {
		combinedExternalTransition :=
			statesByAttempt[attemptID] == corecontract.ModelAttemptPending &&
				attempt.state == corecontract.ModelAttemptSucceeded &&
				(coreMemberHasPort(run.member, moduleapi.PortNameChannelTransport) ||
					(attempt.result != nil && attempt.result.ActionRequest != nil &&
						coreMemberHasPort(run.member, moduleapi.PortNameActionProvider)))
		if !seenPending[attemptID] ||
			(statesByAttempt[attemptID] != attempt.state &&
				!combinedExternalTransition) {
			return coreIntegrity(
				"Run %q model Attempt %q event head differs: seen_pending=%v event_state=%q row_state=%q",
				run.row.runID,
				attemptID,
				seenPending[attemptID],
				statesByAttempt[attemptID],
				attempt.state,
			)
		}
		expectedHistory := 0
		if attempt.state == corecontract.ModelAttemptSucceeded && attempt.result != nil &&
			attempt.result.ActionRequest == nil {
			expectedHistory = 1
		}
		if run.historyByAttempt[attemptID] != expectedHistory {
			return coreIntegrity("Run %q model Attempt %q History closure differs", run.row.runID, attemptID)
		}
	}
	sequences := make([]int, 0)
	for _, attempt := range run.attempts {
		if attempt.usage.ledgerSequence.Valid {
			sequences = append(sequences, int(attempt.usage.ledgerSequence.Int64))
		}
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			return coreIntegrity("Run %q Usage ledger sequence is not contiguous", run.row.runID)
		}
	}
	ledgerHead, err := corecontract.ParseBudgetStateRefV1(
		run.frame.budgetStateRef,
		run.row.runID,
	)
	if err != nil || ledgerHead != uint64(len(sequences)) {
		return coreIntegrity("Run %q BudgetStateRef differs from Usage head: %v", run.row.runID, err)
	}
	continuation := run.frame.continuation
	switch continuation.State {
	case corecontract.InitialLoopStep:
		if run.frame.pendingModelAttempt.Valid || run.frame.pendingDispatchAttempt.Valid {
			return coreIntegrity("Run %q READY names a pending Attempt", run.row.runID)
		}
	case corecontract.WaitingChildrenLoopStep:
		if run.manifest.Composite == nil ||
			(run.manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 &&
				run.manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1) ||
			run.frame.pendingModelAttempt.Valid || run.frame.pendingDispatchAttempt.Valid ||
			run.row.state != corecontract.InitialRunState ||
			run.row.disposition.String != "WAITING_EXTERNAL" ||
			run.frame.waitingReason.String != coreCompositeWaitingReason {
			return coreIntegrity("Run %q WAITING_CHILDREN projection differs", run.row.runID)
		}
	case corecontract.WaitingRepairActivationLoopStep:
		if run.manifest.Composite == nil ||
			run.manifest.Composite.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			(run.manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 &&
				run.manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1) ||
			run.frame.pendingModelAttempt.Valid || run.frame.pendingDispatchAttempt.Valid ||
			run.row.state != corecontract.InitialRunState ||
			run.row.disposition.String != "WAITING_EXTERNAL" ||
			run.frame.waitingReason.String != "COMPOSITE_REPAIR_DORMANT" ||
			run.row.revision != 0 || run.frame.revision != 0 ||
			len(run.events) != 1 || len(run.attempts) != 0 ||
			run.effectAttempts != 0 || len(run.historyByAttempt) != 0 {
			return coreIntegrity(
				"Run %q WAITING_REPAIR_ACTIVATION projection differs",
				run.row.runID,
			)
		}
	case corecontract.ModelPendingLoopStep:
		attempt := run.attempts[continuation.AttemptID]
		if attempt == nil || attempt.state != corecontract.ModelAttemptPending ||
			!run.frame.pendingModelAttempt.Valid ||
			run.frame.pendingModelAttempt.String != continuation.AttemptID ||
			run.frame.pendingDispatchAttempt.Valid {
			return coreIntegrity("Run %q MODEL_PENDING projection differs", run.row.runID)
		}
	case corecontract.WaitingReconciliationLoopStep:
		if continuation.AttemptKind == corecontract.AttemptKindModel {
			attempt := run.attempts[continuation.AttemptID]
			if attempt == nil || attempt.state != corecontract.ModelAttemptUnknown ||
				!run.frame.pendingModelAttempt.Valid ||
				run.frame.pendingModelAttempt.String != continuation.AttemptID ||
				run.frame.pendingDispatchAttempt.Valid {
				return coreIntegrity("Run %q MODEL_UNKNOWN projection differs", run.row.runID)
			}
		}
	case corecontract.TerminatedLoopStep:
		if run.frame.pendingModelAttempt.Valid || run.frame.pendingDispatchAttempt.Valid {
			return coreIntegrity("Run %q TERMINATED names a pending Attempt", run.row.runID)
		}
		if continuation.CoreFailureReason != "" {
			if err := validateCoreDeterministicFailureProjection(run); err != nil {
				return err
			}
		} else if continuation.AttemptKind == corecontract.AttemptKindModel {
			attempt := run.attempts[continuation.AttemptID]
			if attempt == nil ||
				(attempt.state != corecontract.ModelAttemptSucceeded &&
					attempt.state != corecontract.ModelAttemptFailed) {
				return coreIntegrity("Run %q TERMINATED model projection differs", run.row.runID)
			}
		}
	}
	if continuation.CoreFailureReason == "" {
		for _, event := range run.events {
			if event.coreFailure != nil {
				return coreIntegrity(
					"Run %q contains a deterministic Core failure event without its terminal continuation",
					run.row.runID,
				)
			}
		}
	}
	if run.manifest.Composite == nil {
		if run.row.parentRunID.Valid || run.row.parentManifestDigest.Valid ||
			run.row.parentSlotID.Valid {
			return coreIntegrity("non-Composite Run %q contains family projection", run.row.runID)
		}
	} else if run.manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 ||
		run.manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1 {
		if !run.row.parentRunID.Valid || !run.row.parentManifestDigest.Valid ||
			!run.row.parentSlotID.Valid {
			return coreIntegrity("Composite member %q lacks parent projection", run.row.runID)
		}
	}
	return nil
}

func validateCoreDeterministicFailureProjection(
	run *coreRunSemanticState,
) error {
	reason := run.frame.continuation.CoreFailureReason
	if err := corecontract.ValidateCoreDeterministicFailureReasonV1(reason); err != nil {
		return coreIntegrity(
			"Run %q deterministic Core failure reason differs: %v",
			run.row.runID,
			err,
		)
	}
	if reason == corecontract.CompositeRepairSkippedReasonV1 {
		if run.frame.continuation.AttemptKind != "" ||
			run.frame.continuation.AttemptID != "" ||
			run.frame.continuation.LogicalStepID != "" ||
			len(run.attempts) != 0 || run.effectAttempts != 0 ||
			len(run.historyByAttempt) != 0 ||
			run.row.state != corecontract.TerminatedLoopStep ||
			!run.row.disposition.Valid ||
			run.row.disposition.String != corecontract.TerminatedLoopStep ||
			run.row.revision != 1 || run.frame.revision != 1 ||
			run.frame.waitingReason.Valid || run.manifest.Composite == nil ||
			run.manifest.Composite.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			(run.manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 &&
				run.manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1) ||
			len(run.events) != 2 {
			return coreIntegrity(
				"Run %q skipped repair terminal/attempt cardinality differs",
				run.row.runID,
			)
		}
		event := run.events[1]
		if event.kind != corecontract.CompositeRepairSkippedEventKind ||
			event.repairSkipped == nil ||
			event.repairSkipped.RunID != run.row.runID ||
			event.repairSkipped.Reason != reason || event.sequence != 1 ||
			event.fromRevision != 0 || event.toRevision != 1 {
			return coreIntegrity(
				"Run %q skipped repair event/continuation closure differs",
				run.row.runID,
			)
		}
		return nil
	}
	if run.frame.continuation.AttemptKind != "" ||
		run.frame.continuation.AttemptID != "" ||
		run.frame.continuation.LogicalStepID != "" ||
		len(run.attempts) != 0 || run.effectAttempts != 0 ||
		len(run.historyByAttempt) != 0 ||
		run.row.state != corecontract.TerminatedLoopStep ||
		!run.row.disposition.Valid ||
		run.row.disposition.String != corecontract.TerminatedLoopStep ||
		run.row.revision <= 0 || run.frame.waitingReason.Valid ||
		run.manifest.Composite == nil || len(run.events) != 2 {
		return coreIntegrity(
			"Run %q deterministic Core failure terminal/attempt cardinality differs",
			run.row.runID,
		)
	}
	switch run.manifest.Composite.Role {
	case corecontract.CompositeRunRoleRootV1:
		if run.manifest.Composite.Plan == nil {
			return coreIntegrity(
				"Run %q deterministic Core failure root plan differs",
				run.row.runID,
			)
		}
	case corecontract.CompositeRunRoleReviewerV1:
		if run.manifest.Composite.Plan != nil ||
			(reason != corecontract.AllRequiredChildFailedReasonV1 &&
				reason != corecontract.CompositeReviewFailedReasonV1) {
			return coreIntegrity(
				"Run %q deterministic Reviewer failure reason differs",
				run.row.runID,
			)
		}
	default:
		return coreIntegrity(
			"Run %q deterministic Core failure role differs",
			run.row.runID,
		)
	}
	event := run.events[len(run.events)-1]
	if event.kind != corecontract.CoreDeterministicFailureEventKind ||
		event.coreFailure == nil ||
		event.coreFailure.RunID != run.row.runID ||
		event.coreFailure.Reason != reason || event.sequence != 1 ||
		event.toRevision != event.fromRevision+1 ||
		event.toRevision > run.frame.revision {
		return coreIntegrity(
			"Run %q deterministic Core failure event/continuation closure differs",
			run.row.runID,
		)
	}
	return nil
}

func coreRunHasSuccessfulTerminalResult(run *coreRunSemanticState) bool {
	return coreSuccessfulTerminalAttempt(run) != nil
}

func coreRunHasFailedTerminalResult(run *coreRunSemanticState) bool {
	if run.frame.continuation.State != corecontract.TerminatedLoopStep ||
		run.frame.continuation.AttemptKind != corecontract.AttemptKindModel {
		return false
	}
	attempt := run.attempts[run.frame.continuation.AttemptID]
	return attempt != nil && attempt.state == corecontract.ModelAttemptFailed &&
		!attempt.resultRef.Valid && attempt.errorClassification.Valid &&
		run.historyByAttempt[attempt.attemptID] == 0
}

func coreSuccessfulTerminalAttempt(
	run *coreRunSemanticState,
) *coreModelAttemptState {
	if run.frame.continuation.State != corecontract.TerminatedLoopStep ||
		run.frame.continuation.AttemptKind != corecontract.AttemptKindModel {
		return nil
	}
	attempt := run.attempts[run.frame.continuation.AttemptID]
	if attempt == nil || attempt.state != corecontract.ModelAttemptSucceeded ||
		!attempt.resultRef.Valid || attempt.result == nil ||
		run.historyByAttempt[attempt.attemptID] != 1 {
		return nil
	}
	return attempt
}

func coreMemberHasPort(
	member corecontract.MemberExecutionSnapshot,
	name string,
) bool {
	for _, plan := range member.PortPlans {
		if plan.Port.ExactVersion == moduleapi.PortVersionV1 &&
			plan.Port.Name == name {
			return true
		}
	}
	return false
}

func inspectCoreContent(
	ctx context.Context,
	database semanticQueryer,
	digest string,
	want currentstore.ContentKind,
) (coreContentState, error) {
	var kind string
	var state coreContentState
	var size int64
	if err := database.QueryRowContext(ctx, `
		SELECT kind, media_type, canonical_bytes, size_bytes
		FROM content_records
		WHERE content_digest=?
	`, digest).Scan(&kind, &state.mediaType, &state.canonical, &size); err != nil {
		return coreContentState{}, err
	}
	state.kind = currentstore.ContentKind(kind)
	computed, err := currentstore.ComputeContentDigest(
		state.kind,
		state.mediaType,
		state.canonical,
	)
	if err != nil || state.kind != want || int64(len(state.canonical)) != size ||
		computed != digest {
		return coreContentState{}, fmt.Errorf("content %q kind/digest differs: %v", digest, err)
	}
	return state, nil
}

func coreUsageMatchesReceipt(
	usage coreUsageState,
	receipt moduleapi.ModelUsageReceiptV1,
) bool {
	return equalCoreUint(usage.inputTokens, receipt.InputTokens) &&
		equalCoreUint(usage.cachedInputTokens, receipt.CachedInputTokens) &&
		equalCoreUint(usage.uncachedInputTokens, receipt.UncachedInputTokens) &&
		equalCoreUint(usage.outputTokens, receipt.OutputTokens) &&
		equalCoreUint(usage.reasoningTokens, receipt.ReasoningTokens) &&
		equalCoreString(usage.providerReportedCost, receipt.ProviderReportedCost)
}

func equalCoreUint(value sql.NullInt64, expected *uint64) bool {
	if !value.Valid || expected == nil {
		return value.Valid == (expected != nil)
	}
	return uint64(value.Int64) == *expected
}

func equalCoreString(value sql.NullString, expected *string) bool {
	if !value.Valid || expected == nil {
		return value.Valid == (expected != nil)
	}
	return value.String == *expected
}

func coreUsageHasBillableFact(usage coreUsageState) bool {
	return usage.inputTokens.Valid || usage.cachedInputTokens.Valid ||
		usage.uncachedInputTokens.Valid || usage.outputTokens.Valid ||
		usage.reasoningTokens.Valid || usage.estimatedCost.Valid ||
		usage.providerReportedCost.Valid || usage.reconciledCost.Valid
}

func coreTerminalUsageStatus(usage coreUsageState) string {
	if usage.rawReceiptRef.Valid {
		return coreUsageProviderReported
	}
	return coreUsageNoReport
}

func coreIntegrity(format string, arguments ...any) error {
	return fmt.Errorf("%w: core Run semantic closure: %s", ErrIntegrity, fmt.Sprintf(format, arguments...))
}
