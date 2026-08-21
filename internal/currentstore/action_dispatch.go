package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidActionDispatch = errors.New(
		"currentstore: invalid Action dispatch",
	)
	ErrActionDispatchConflict = errors.New(
		"currentstore: Action dispatch conflict",
	)
	ErrActionDispatchIntegrity = errors.New(
		"currentstore: Action dispatch integrity violation",
	)
)

const actionOperationDigestDomain = "freeagent.action-operation/v1"

// DispatchKind identifies the consumer of one row in the single external
// effect ledger. Channel Outbox is a projection of CHANNEL_SEND rows, not a
// second queue or ledger.
type DispatchKind string

const (
	DispatchKindAction      DispatchKind = "ACTION"
	DispatchKindChannelSend DispatchKind = "CHANNEL_SEND"
)

func (kind DispatchKind) validate() error {
	switch kind {
	case DispatchKindAction, DispatchKindChannelSend:
		return nil
	default:
		return fmt.Errorf("unsupported dispatch kind %q", kind)
	}
}

// DispatchState is the complete authoritative external-effect Attempt state
// set. UNKNOWN is deliberately distinct from a retryable failure: it may only
// be reconciled on the original row and never returns to PENDING.
type DispatchState string

const (
	DispatchPending   DispatchState = "PENDING"
	DispatchSucceeded DispatchState = "SUCCEEDED"
	DispatchFailed    DispatchState = "FAILED"
	DispatchUnknown   DispatchState = "UNKNOWN"

	ActionDispatchPending   = DispatchPending
	ActionDispatchSucceeded = DispatchSucceeded
	ActionDispatchFailed    = DispatchFailed
	ActionDispatchUnknown   = DispatchUnknown
)

// ActionDispatchState is retained as a source-compatible alias for the Action
// APIs while both Action and Channel use the same four-state ledger.
type ActionDispatchState = DispatchState

func (state DispatchState) validate() error {
	switch state {
	case ActionDispatchPending,
		ActionDispatchSucceeded,
		ActionDispatchFailed,
		ActionDispatchUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported Action dispatch state %q", state)
	}
}

func (state DispatchState) allowsTransition(next DispatchState) bool {
	switch state {
	case ActionDispatchPending:
		return next == ActionDispatchSucceeded ||
			next == ActionDispatchFailed ||
			next == ActionDispatchUnknown
	case ActionDispatchUnknown:
		return next == ActionDispatchSucceeded ||
			next == ActionDispatchFailed
	default:
		return false
	}
}

// ActionDispatchAttemptRecord is the detached authoritative Action ledger
// row. The exact proposal/result/receipt/evidence content is carried by the
// enclosing ActionDispatchRecord.
type ActionDispatchAttemptRecord struct {
	AttemptID                 string
	LogicalOperationKey       string
	RunID                     string
	TenantID                  string
	WorkspaceID               string
	MemberID                  string
	LogicalStepID             string
	SourceModelAttemptID      string
	FrameRevision             uint64
	MemberSnapshotDigest      string
	BindingIndex              uint32
	Binding                   moduleapi.PortBinding
	BindingCanonical          []byte
	PublicActionID            string
	ProviderActionID          string
	DefinitionDigest          string
	ProposalRef               string
	EffectClass               moduleapi.EffectClass
	MaxResultBytes            uint32
	Deadline                  time.Time
	BudgetStateRef            string
	State                     ActionDispatchState
	ExternalOperationID       string
	ProviderReceiptRef        string
	ResultRef                 string
	ErrorClassification       string
	ReconciliationEvidenceRef string
	UnknownReason             string
	Revision                  uint64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// ActionDispatchRecord is the narrow content-verified recovery projection for
// one Action Attempt. Optional records are nil exactly when their reference is
// absent on the authoritative row.
type ActionDispatchRecord struct {
	Attempt                ActionDispatchAttemptRecord
	Proposal               ContentRecord
	Result                 *ContentRecord
	ProviderReceipt        *ContentRecord
	ReconciliationEvidence *ContentRecord
}

// CommitModelActionAndBeginDispatchResult is the only pre-effect grant. A
// copied result shares the same private one-time permit. Re-entry and process
// restart can recover the record but cannot recover execution permission.
type CommitModelActionAndBeginDispatchResult struct {
	Model          ModelDispatchRecord
	Action         ActionDispatchRecord
	Lease          RunLease
	Applied        bool
	GatewayAllowed bool
	permit         *actionGatewayPermit
}

type actionGatewayPermit struct {
	consumed atomic.Bool
	closure  actionGatewayPermitClosure
}

type actionGatewayPermitClosure struct {
	attemptID            string
	runID                string
	memberID             string
	memberSnapshotDigest string
	bindingIndex         uint32
	bindingCanonical     []byte
	proposalDigest       string
	deadline             time.Time
	lease                RunLease
}

// ConsumeActionGatewayPermit atomically consumes the process-local grant
// produced by the transaction that first persisted Action PENDING.
func (result CommitModelActionAndBeginDispatchResult) ConsumeActionGatewayPermit() bool {
	if result.permit == nil || !result.Applied || !result.GatewayAllowed ||
		!result.permit.matches(result.Action.Attempt, result.Lease) {
		return false
	}
	return result.permit.consumed.CompareAndSwap(false, true)
}

func (permit *actionGatewayPermit) matches(
	attempt ActionDispatchAttemptRecord,
	lease RunLease,
) bool {
	if permit == nil {
		return false
	}
	closure := permit.closure
	return closure.attemptID == attempt.AttemptID &&
		closure.runID == attempt.RunID &&
		closure.memberID == attempt.MemberID &&
		closure.memberSnapshotDigest == attempt.MemberSnapshotDigest &&
		closure.bindingIndex == attempt.BindingIndex &&
		bytes.Equal(closure.bindingCanonical, attempt.BindingCanonical) &&
		closure.proposalDigest == attempt.ProposalRef &&
		closure.deadline.Equal(attempt.Deadline) &&
		closure.lease == lease
}

// GetActionDispatchRecord reads one coherent, immutable-content-verified
// Action Attempt. It does not grant Gateway permission.
func (store *Store) GetActionDispatchRecord(
	ctx context.Context,
	attemptID string,
) (ActionDispatchRecord, error) {
	if ctx == nil {
		return ActionDispatchRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidActionDispatch,
		)
	}
	if !validLeaseOpaqueID(attemptID) {
		return ActionDispatchRecord{}, fmt.Errorf(
			"%w: invalid Attempt ID",
			ErrInvalidActionDispatch,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ActionDispatchRecord{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ActionDispatchRecord{}, fmt.Errorf(
			"currentstore: acquire Action record connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ActionDispatchRecord{}, fmt.Errorf(
			"currentstore: begin GetActionDispatchRecord: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	record, err := queryActionDispatchRecord(ctx, connection, attemptID)
	if err != nil {
		return ActionDispatchRecord{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ActionDispatchRecord{}, fmt.Errorf(
			"currentstore: commit GetActionDispatchRecord: %w",
			err,
		)
	}
	committed = true
	return cloneActionDispatchRecord(record), nil
}

// ScanUnsettledActionDispatchRecords returns only PENDING and UNKNOWN Action
// Attempts for one Run in deterministic creation order.
func (store *Store) ScanUnsettledActionDispatchRecords(
	ctx context.Context,
	runID string,
) ([]ActionDispatchRecord, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidActionDispatch,
		)
	}
	if !validLeaseOpaqueID(runID) {
		return nil, fmt.Errorf(
			"%w: invalid Run ID",
			ErrInvalidActionDispatch,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: acquire Action scan connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: begin Action Attempt scan: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind='ACTION'
		  AND state IN ('PENDING', 'UNKNOWN')
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: scan unsettled Action Attempts: %w",
			err,
		)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"currentstore: scan unsettled Action Attempt ID: %w",
				err,
			)
		}
		ids = append(ids, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf(
			"currentstore: iterate unsettled Action Attempts: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: close unsettled Action Attempt rows: %w",
			err,
		)
	}
	records := make([]ActionDispatchRecord, 0, len(ids))
	for _, attemptID := range ids {
		record, err := queryActionDispatchRecord(ctx, connection, attemptID)
		if err != nil {
			return nil, err
		}
		if record.Attempt.RunID != runID {
			return nil, fmt.Errorf(
				"%w: scanned Attempt crossed Run identity",
				ErrActionDispatchIntegrity,
			)
		}
		records = append(records, cloneActionDispatchRecord(record))
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, fmt.Errorf(
			"currentstore: commit Action Attempt scan: %w",
			err,
		)
	}
	committed = true
	return records, nil
}

func queryActionDispatchRecord(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attemptID string,
) (ActionDispatchRecord, error) {
	var (
		record                 ActionDispatchAttemptRecord
		bindingIndex           int64
		maxResultBytes         int64
		deadline               int64
		state                  string
		externalOperationID    sql.NullString
		providerReceiptRef     sql.NullString
		resultRef              sql.NullString
		errorClassification    sql.NullString
		reconciliationEvidence sql.NullString
		unknownReason          sql.NullString
		revision               int64
		createdAt              int64
		updatedAt              int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			attempt_id,
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
			budget_state_ref,
			state,
			external_operation_id,
			provider_receipt_ref,
			result_ref,
			error_classification,
			reconciliation_evidence_ref,
			unknown_reason,
			revision,
			created_at,
			updated_at
		FROM dispatch_attempts
		WHERE attempt_id=? AND dispatch_kind='ACTION'
	`, attemptID).Scan(
		&record.AttemptID,
		&record.LogicalOperationKey,
		&record.RunID,
		&record.TenantID,
		&record.WorkspaceID,
		&record.MemberID,
		&record.LogicalStepID,
		&record.SourceModelAttemptID,
		&record.FrameRevision,
		&record.MemberSnapshotDigest,
		&bindingIndex,
		&record.BindingCanonical,
		&record.PublicActionID,
		&record.ProviderActionID,
		&record.DefinitionDigest,
		&record.ProposalRef,
		&record.EffectClass,
		&maxResultBytes,
		&deadline,
		&record.BudgetStateRef,
		&state,
		&externalOperationID,
		&providerReceiptRef,
		&resultRef,
		&errorClassification,
		&reconciliationEvidence,
		&unknownReason,
		&revision,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionDispatchRecord{}, fmt.Errorf(
			"%w: Action Attempt %q does not exist",
			ErrActionDispatchConflict,
			attemptID,
		)
	}
	if err != nil {
		return ActionDispatchRecord{}, fmt.Errorf(
			"currentstore: query Action Attempt %q: %w",
			attemptID,
			err,
		)
	}
	if record.FrameRevision > math.MaxInt64 || bindingIndex < 0 ||
		bindingIndex > math.MaxUint32 || maxResultBytes < 1 ||
		maxResultBytes > int64(moduleapi.MaxActionResultBytesV1) ||
		deadline <= 0 || revision < 0 || createdAt <= 0 || updatedAt <= 0 {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"numeric projection",
		)
	}
	record.BindingIndex = uint32(bindingIndex)
	record.MaxResultBytes = uint32(maxResultBytes)
	record.State = ActionDispatchState(state)
	if err := record.State.validate(); err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"state",
		)
	}
	for name, value := range map[string]string{
		"Attempt ID":              record.AttemptID,
		"Run ID":                  record.RunID,
		"Tenant ID":               record.TenantID,
		"Workspace ID":            record.WorkspaceID,
		"Member ID":               record.MemberID,
		"logical step ID":         record.LogicalStepID,
		"source Model Attempt ID": record.SourceModelAttemptID,
		"public Action ID":        record.PublicActionID,
		"provider Action ID":      record.ProviderActionID,
	} {
		if !validLeaseOpaqueID(value) {
			return ActionDispatchRecord{}, actionAttemptIntegrity(
				record.AttemptID,
				name,
			)
		}
	}
	if !moduleapi.ValidSHA256(record.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(record.MemberSnapshotDigest) ||
		!moduleapi.ValidSHA256(record.DefinitionDigest) ||
		!moduleapi.ValidSHA256(record.ProposalRef) {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"digest projection",
		)
	}
	expectedKey, err := actionLogicalOperationKey(
		record.RunID,
		record.MemberID,
		record.LogicalStepID,
	)
	if err != nil || expectedKey != record.LogicalOperationKey {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"logical operation key",
		)
	}
	binding, err := restoreModelBinding(record.BindingCanonical)
	if err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"frozen Binding",
		)
	}
	record.Binding = binding
	if err := record.EffectClass.Validate(); err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"EffectClass",
		)
	}
	parsedDeadline, err := timeFromUnixMicro(deadline)
	if err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"deadline",
		)
	}
	record.Deadline = parsedDeadline
	if _, err := corecontract.ParseBudgetStateRefV1(
		record.BudgetStateRef,
		record.RunID,
	); err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"BudgetStateRef",
		)
	}
	record.ExternalOperationID = externalOperationID.String
	record.ProviderReceiptRef = providerReceiptRef.String
	record.ResultRef = resultRef.String
	record.ErrorClassification = errorClassification.String
	record.ReconciliationEvidenceRef = reconciliationEvidence.String
	record.UnknownReason = unknownReason.String
	record.Revision = uint64(revision)
	record.CreatedAt, err = timeFromUnixMicro(createdAt)
	if err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"created_at",
		)
	}
	record.UpdatedAt, err = timeFromUnixMicro(updatedAt)
	if err != nil || record.UpdatedAt.Before(record.CreatedAt) {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"updated_at",
		)
	}
	if err := validateActionStateFacts(record); err != nil {
		return ActionDispatchRecord{}, err
	}
	member, definition, err := loadActionAttemptFrozenDefinition(
		ctx,
		queryer,
		record,
	)
	if err != nil {
		return ActionDispatchRecord{}, err
	}
	if member.MemberSnapshotDigest != record.MemberSnapshotDigest {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"MemberSnapshotDigest",
		)
	}

	proposal, err := queryActionContent(
		ctx,
		queryer,
		record.ProposalRef,
		ContentActionProposal,
	)
	if err != nil {
		return ActionDispatchRecord{}, err
	}
	if _, err := corecontract.RestoreActionProposalV1(
		proposal.CanonicalBytes,
		proposal.Digest,
		record.MemberSnapshotDigest,
		definition,
	); err != nil {
		return ActionDispatchRecord{}, actionAttemptIntegrity(
			record.AttemptID,
			"Action Proposal closure",
		)
	}
	result := ActionDispatchRecord{
		Attempt:  record,
		Proposal: proposal,
	}
	if record.ResultRef != "" {
		content, err := queryActionContent(
			ctx,
			queryer,
			record.ResultRef,
			ContentActionResult,
		)
		if err != nil {
			return ActionDispatchRecord{}, err
		}
		if _, err := corecontract.RestoreActionResultV1(
			content.CanonicalBytes,
			content.Digest,
			definition,
		); err != nil {
			return ActionDispatchRecord{}, actionAttemptIntegrity(
				record.AttemptID,
				"Action Result closure",
			)
		}
		result.Result = &content
	}
	if record.ProviderReceiptRef != "" {
		content, err := queryActionContent(
			ctx,
			queryer,
			record.ProviderReceiptRef,
			ContentProviderReceipt,
		)
		if err != nil {
			return ActionDispatchRecord{}, err
		}
		if len(content.CanonicalBytes) == 0 ||
			content.CanonicalBytes[0] != '{' ||
			len(content.CanonicalBytes) > moduleapi.MaxActionReceiptBytesV1 {
			return ActionDispatchRecord{}, actionAttemptIntegrity(
				record.AttemptID,
				"Provider receipt closure",
			)
		}
		result.ProviderReceipt = &content
	}
	if record.ReconciliationEvidenceRef != "" {
		content, err := queryActionContent(
			ctx,
			queryer,
			record.ReconciliationEvidenceRef,
			ContentReconciliationEvidence,
		)
		if err != nil {
			return ActionDispatchRecord{}, err
		}
		result.ReconciliationEvidence = &content
	}
	return result, nil
}

func loadActionAttemptFrozenDefinition(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	record ActionDispatchAttemptRecord,
) (
	corecontract.MemberExecutionSnapshot,
	corecontract.FrozenActionDefinitionV1,
	error,
) {
	var canonical []byte
	var digest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, record.RunID, record.MemberID).Scan(&canonical, &digest); err != nil {
		return corecontract.MemberExecutionSnapshot{},
			corecontract.FrozenActionDefinitionV1{},
			actionAttemptIntegrity(record.AttemptID, "member snapshot")
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil || digest != member.MemberSnapshotDigest ||
		digest != record.MemberSnapshotDigest {
		return corecontract.MemberExecutionSnapshot{},
			corecontract.FrozenActionDefinitionV1{},
			actionAttemptIntegrity(record.AttemptID, "member snapshot closure")
	}
	var definition *corecontract.FrozenActionDefinitionV1
	for index := range member.Actions {
		candidate := &member.Actions[index]
		if candidate.PublicActionID == record.PublicActionID {
			if definition != nil {
				return corecontract.MemberExecutionSnapshot{},
					corecontract.FrozenActionDefinitionV1{},
					actionAttemptIntegrity(record.AttemptID, "duplicate frozen Action")
			}
			definition = candidate
		}
	}
	if definition == nil ||
		definition.ProviderActionID != record.ProviderActionID ||
		definition.BindingIndex != record.BindingIndex ||
		definition.DefinitionDigest != record.DefinitionDigest ||
		definition.EffectClass != record.EffectClass ||
		definition.MaxResultBytes != record.MaxResultBytes {
		return corecontract.MemberExecutionSnapshot{},
			corecontract.FrozenActionDefinitionV1{},
			actionAttemptIntegrity(record.AttemptID, "frozen Action definition")
	}
	plan, err := exactActionPlan(member)
	if err != nil || uint64(record.BindingIndex) >= uint64(len(plan.Bindings)) {
		return corecontract.MemberExecutionSnapshot{},
			corecontract.FrozenActionDefinitionV1{},
			actionAttemptIntegrity(record.AttemptID, "Action BindingIndex")
	}
	bindingCanonical, err := canonicalModelBinding(
		plan.Bindings[record.BindingIndex],
	)
	if err != nil || !bytes.Equal(bindingCanonical, record.BindingCanonical) {
		return corecontract.MemberExecutionSnapshot{},
			corecontract.FrozenActionDefinitionV1{},
			actionAttemptIntegrity(record.AttemptID, "Action Binding closure")
	}
	return member, cloneFrozenActionDefinition(*definition), nil
}

func cloneFrozenActionDefinition(
	definition corecontract.FrozenActionDefinitionV1,
) corecontract.FrozenActionDefinitionV1 {
	definition.InputSchema = bytes.Clone(definition.InputSchema)
	return definition
}

func queryActionContent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	digest string,
	kind ContentKind,
) (ContentRecord, error) {
	record, err := queryContent(ctx, queryer, digest)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: Action content %s: %v",
			ErrActionDispatchIntegrity,
			digest,
			err,
		)
	}
	if record.Kind != kind || record.MediaType != admissionJSONMediaType {
		return ContentRecord{}, fmt.Errorf(
			"%w: Action content %s has kind/media drift",
			ErrActionDispatchIntegrity,
			digest,
		)
	}
	return record, nil
}

func validateActionStateFacts(record ActionDispatchAttemptRecord) error {
	switch record.State {
	case ActionDispatchPending:
		if record.ExternalOperationID != "" ||
			record.ProviderReceiptRef != "" ||
			record.ResultRef != "" ||
			record.ErrorClassification != "" ||
			record.ReconciliationEvidenceRef != "" ||
			record.UnknownReason != "" {
			return actionAttemptIntegrity(record.AttemptID, "PENDING facts")
		}
	case ActionDispatchSucceeded:
		if record.ResultRef == "" || record.ErrorClassification != "" ||
			record.UnknownReason != "" {
			return actionAttemptIntegrity(record.AttemptID, "SUCCEEDED facts")
		}
	case ActionDispatchFailed:
		if record.ResultRef != "" || record.ErrorClassification == "" ||
			record.UnknownReason != "" {
			return actionAttemptIntegrity(record.AttemptID, "FAILED facts")
		}
	case ActionDispatchUnknown:
		if record.ResultRef != "" || record.ErrorClassification != "" ||
			(record.ExternalOperationID == "" &&
				record.ProviderReceiptRef == "" &&
				record.ReconciliationEvidenceRef == "" &&
				record.UnknownReason == "") {
			return actionAttemptIntegrity(record.AttemptID, "UNKNOWN facts")
		}
	default:
		return actionAttemptIntegrity(record.AttemptID, "state")
	}
	return nil
}

func actionLogicalOperationKey(
	runID string,
	memberID string,
	logicalStepID string,
) (string, error) {
	for name, value := range map[string]string{
		"run ID":          runID,
		"member ID":       memberID,
		"logical step ID": logicalStepID,
	} {
		if !validLeaseOpaqueID(value) {
			return "", fmt.Errorf("invalid %s", name)
		}
	}
	identity := struct {
		RunID         string `json:"run_id"`
		MemberID      string `json:"member_id"`
		LogicalStepID string `json:"logical_step_id"`
	}{
		RunID:         runID,
		MemberID:      memberID,
		LogicalStepID: logicalStepID,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(actionOperationDigestDomain, canonical), nil
}

func actionAttemptIntegrity(attemptID string, subject string) error {
	return fmt.Errorf(
		"%w: Action Attempt %q has invalid %s",
		ErrActionDispatchIntegrity,
		attemptID,
		subject,
	)
}

func cloneActionDispatchRecord(record ActionDispatchRecord) ActionDispatchRecord {
	record.Attempt = cloneActionDispatchAttempt(record.Attempt)
	record.Proposal = cloneContentRecord(record.Proposal)
	if record.Result != nil {
		content := cloneContentRecord(*record.Result)
		record.Result = &content
	}
	if record.ProviderReceipt != nil {
		content := cloneContentRecord(*record.ProviderReceipt)
		record.ProviderReceipt = &content
	}
	if record.ReconciliationEvidence != nil {
		content := cloneContentRecord(*record.ReconciliationEvidence)
		record.ReconciliationEvidence = &content
	}
	return record
}

func cloneActionDispatchAttempt(
	record ActionDispatchAttemptRecord,
) ActionDispatchAttemptRecord {
	record.Binding.StaticContextRefs = append(
		[]string{},
		record.Binding.StaticContextRefs...,
	)
	record.BindingCanonical = bytes.Clone(record.BindingCanonical)
	return record
}

func sortedActionDispatches(
	records []ActionDispatchRecord,
) []ActionDispatchRecord {
	cloned := make([]ActionDispatchRecord, len(records))
	for index := range records {
		cloned[index] = cloneActionDispatchRecord(records[index])
	}
	sort.Slice(cloned, func(left, right int) bool {
		if cloned[left].Attempt.CreatedAt.Equal(cloned[right].Attempt.CreatedAt) {
			return cloned[left].Attempt.AttemptID < cloned[right].Attempt.AttemptID
		}
		return cloned[left].Attempt.CreatedAt.Before(cloned[right].Attempt.CreatedAt)
	})
	return cloned
}
