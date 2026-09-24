package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// UnknownAttemptKindV1 identifies the only durable external-effect attempt
// families exposed by the Control read surface.
type UnknownAttemptKindV1 string

const (
	UnknownAttemptModelV1   UnknownAttemptKindV1 = "MODEL"
	UnknownAttemptActionV1  UnknownAttemptKindV1 = "ACTION"
	UnknownAttemptChannelV1 UnknownAttemptKindV1 = "CHANNEL"

	// One extra row lets the Control service report has_more for a full page.
	maximumControlManagementQueryRowsV1 = 101
)

// UnknownAttemptProjectionV1 contains only safe reconciliation facts. It
// deliberately does not contain request bodies, receipts, secrets, paths, or
// material that can be used to construct a replacement operation.
type UnknownAttemptProjectionV1 struct {
	Kind                      UnknownAttemptKindV1
	AttemptID                 string
	RunID                     string
	TenantID                  string
	WorkspaceID               string
	State                     string
	Provider                  string
	Model                     string
	ProviderRequestID         string
	ExternalOperationID       string
	EndpointID                string
	ErrorClassification       string
	UnknownReason             string
	HasReconciliationEvidence bool
	ReconciliationEvidenceRef string
	Revision                  uint64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Usage                     UnknownUsageProjectionV1
}

// UnknownUsageProjectionV1 keeps token facts nullable. For PENDING and
// UNKNOWN attempts every pointer remains nil; nil is never converted to zero.
type UnknownUsageProjectionV1 struct {
	InputTokens         *uint64
	CachedInputTokens   *uint64
	UncachedInputTokens *uint64
	OutputTokens        *uint64
	ReasoningTokens     *uint64
}

// ListUnknownAttemptProjectionsV1 returns a bounded, scope-fenced projection.
// The second result is true when more matching Attempts exist.
func (store *Store) ListUnknownAttemptProjectionsV1(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]UnknownAttemptProjectionV1, bool, error) {
	if ctx == nil || limit < 1 || limit > maximumControlManagementQueryRowsV1 ||
		validatePublishedBasisTenantID(tenantID) != nil ||
		(workspaceID != "" && !validLeaseOpaqueID(workspaceID)) {
		return nil, false, ErrInvalidModelDispatch
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("currentstore: acquire UNKNOWN query connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return nil, false, fmt.Errorf("currentstore: begin UNKNOWN query: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	type candidate struct {
		kind UnknownAttemptKindV1
		id   string
	}
	candidates := make([]candidate, 0, limit+1)
	appendIDs := func(query string, args ...any) error {
		rows, queryErr := connection.QueryContext(ctx, query, args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				return scanErr
			}
			candidates = append(candidates, candidate{id: id})
		}
		return rows.Err()
	}
	modelQuery := `SELECT attempt_id FROM model_dispatch_attempts
		WHERE tenant_id=? AND state IN ('PENDING','MODEL_UNKNOWN')`
	modelArgs := []any{tenantID}
	if workspaceID != "" {
		modelQuery += ` AND workspace_id=?`
		modelArgs = append(modelArgs, workspaceID)
	}
	modelQuery += ` ORDER BY updated_at DESC,attempt_id COLLATE BINARY DESC LIMIT ?`
	modelArgs = append(modelArgs, limit+1)
	start := len(candidates)
	if err := appendIDs(modelQuery, modelArgs...); err != nil {
		return nil, false, fmt.Errorf("currentstore: query Model UNKNOWN attempts: %w", err)
	}
	for i := start; i < len(candidates); i++ {
		candidates[i].kind = UnknownAttemptModelV1
	}
	actionQuery := `SELECT attempt_id FROM dispatch_attempts
		WHERE tenant_id=? AND dispatch_kind='ACTION'
		AND state IN ('PENDING','UNKNOWN')`
	actionArgs := []any{tenantID}
	if workspaceID != "" {
		actionQuery += ` AND workspace_id=?`
		actionArgs = append(actionArgs, workspaceID)
	}
	actionQuery += ` ORDER BY updated_at DESC,attempt_id COLLATE BINARY DESC LIMIT ?`
	actionArgs = append(actionArgs, limit+1)
	start = len(candidates)
	if err := appendIDs(actionQuery, actionArgs...); err != nil {
		return nil, false, fmt.Errorf("currentstore: query Action UNKNOWN attempts: %w", err)
	}
	for i := start; i < len(candidates); i++ {
		candidates[i].kind = UnknownAttemptActionV1
	}
	channelQuery := `SELECT attempt_id FROM dispatch_attempts
		WHERE tenant_id=? AND dispatch_kind='CHANNEL_SEND'
		AND state IN ('PENDING','UNKNOWN')`
	channelArgs := []any{tenantID}
	if workspaceID != "" {
		channelQuery += ` AND workspace_id=?`
		channelArgs = append(channelArgs, workspaceID)
	}
	channelQuery += ` ORDER BY updated_at DESC,attempt_id COLLATE BINARY DESC LIMIT ?`
	channelArgs = append(channelArgs, limit+1)
	start = len(candidates)
	if err := appendIDs(channelQuery, channelArgs...); err != nil {
		return nil, false, fmt.Errorf("currentstore: query Channel UNKNOWN attempts: %w", err)
	}
	for i := start; i < len(candidates); i++ {
		candidates[i].kind = UnknownAttemptChannelV1
	}

	type loaded struct {
		item UnknownAttemptProjectionV1
	}
	loadedItems := make([]loaded, 0, len(candidates))
	for _, candidate := range candidates {
		var item UnknownAttemptProjectionV1
		switch candidate.kind {
		case UnknownAttemptModelV1:
			record, loadErr := queryModelDispatchRecord(ctx, connection, candidate.id)
			if loadErr != nil {
				return nil, false, loadErr
			}
			item = projectUnknownModelV1(record)
		case UnknownAttemptActionV1:
			record, loadErr := queryActionDispatchRecord(ctx, connection, candidate.id)
			if loadErr != nil {
				return nil, false, loadErr
			}
			item = projectUnknownActionV1(record)
		case UnknownAttemptChannelV1:
			record, loadErr := queryChannelDispatchRecord(ctx, connection, candidate.id)
			if loadErr != nil {
				return nil, false, loadErr
			}
			item = projectUnknownChannelV1(record)
		default:
			return nil, false, errors.New("currentstore: invalid UNKNOWN attempt kind")
		}
		if item.TenantID != tenantID ||
			(workspaceID != "" && item.WorkspaceID != workspaceID) {
			return nil, false, errors.New("currentstore: UNKNOWN query crossed scope")
		}
		loadedItems = append(loadedItems, loaded{item: item})
	}
	sort.Slice(loadedItems, func(left, right int) bool {
		a, b := loadedItems[left].item, loadedItems[right].item
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.AttemptID != b.AttemptID {
			return a.AttemptID > b.AttemptID
		}
		return a.Kind > b.Kind
	})
	hasMore := len(loadedItems) > limit
	if hasMore {
		loadedItems = loadedItems[:limit]
	}
	result := make([]UnknownAttemptProjectionV1, len(loadedItems))
	for i := range loadedItems {
		result[i] = loadedItems[i].item
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, false, fmt.Errorf("currentstore: commit UNKNOWN query: %w", err)
	}
	committed = true
	return result, hasMore, nil
}

// GetUnknownAttemptProjectionV1 resolves one exact Attempt and rechecks the
// tenant/workspace fence before returning its safe projection.
func (store *Store) GetUnknownAttemptProjectionV1(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	kind UnknownAttemptKindV1,
	attemptID string,
) (UnknownAttemptProjectionV1, error) {
	if ctx == nil || validatePublishedBasisTenantID(tenantID) != nil ||
		(workspaceID != "" && !validLeaseOpaqueID(workspaceID)) ||
		!validLeaseOpaqueID(attemptID) {
		return UnknownAttemptProjectionV1{}, ErrInvalidModelDispatch
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return UnknownAttemptProjectionV1{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return UnknownAttemptProjectionV1{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return UnknownAttemptProjectionV1{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	var item UnknownAttemptProjectionV1
	switch kind {
	case UnknownAttemptModelV1:
		record, loadErr := queryModelDispatchRecord(ctx, connection, attemptID)
		if loadErr != nil {
			return UnknownAttemptProjectionV1{}, loadErr
		}
		item = projectUnknownModelV1(record)
	case UnknownAttemptActionV1:
		record, loadErr := queryActionDispatchRecord(ctx, connection, attemptID)
		if loadErr != nil {
			return UnknownAttemptProjectionV1{}, loadErr
		}
		item = projectUnknownActionV1(record)
	case UnknownAttemptChannelV1:
		record, loadErr := queryChannelDispatchRecord(ctx, connection, attemptID)
		if loadErr != nil {
			return UnknownAttemptProjectionV1{}, loadErr
		}
		item = projectUnknownChannelV1(record)
	default:
		return UnknownAttemptProjectionV1{}, ErrInvalidModelDispatch
	}
	if item.TenantID != tenantID ||
		(workspaceID != "" && item.WorkspaceID != workspaceID) {
		return UnknownAttemptProjectionV1{}, sql.ErrNoRows
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return UnknownAttemptProjectionV1{}, err
	}
	committed = true
	return item, nil
}

func projectUnknownModelV1(record ModelDispatchRecord) UnknownAttemptProjectionV1 {
	attempt := record.Attempt
	return UnknownAttemptProjectionV1{
		Kind: UnknownAttemptModelV1, AttemptID: attempt.AttemptID,
		RunID: attempt.RunID, TenantID: attempt.TenantID, WorkspaceID: attempt.WorkspaceID,
		State: string(attempt.State), Provider: attempt.Provider, Model: attempt.Model,
		ProviderRequestID:   attempt.ProviderRequestID,
		ErrorClassification: attempt.ErrorClassification, UnknownReason: attempt.UnknownReason,
		HasReconciliationEvidence: attempt.ReconciliationEvidenceRef != "",
		ReconciliationEvidenceRef: attempt.ReconciliationEvidenceRef,
		Revision:                  attempt.Revision, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
		Usage: UnknownUsageProjectionV1{
			InputTokens:         record.Usage.Tokens.Input,
			CachedInputTokens:   record.Usage.Tokens.CachedInput,
			UncachedInputTokens: record.Usage.Tokens.UncachedInput,
			OutputTokens:        record.Usage.Tokens.Output,
			ReasoningTokens:     record.Usage.Tokens.Reasoning,
		},
	}
}

func projectUnknownActionV1(record ActionDispatchRecord) UnknownAttemptProjectionV1 {
	attempt := record.Attempt
	return UnknownAttemptProjectionV1{
		Kind: UnknownAttemptActionV1, AttemptID: attempt.AttemptID,
		RunID: attempt.RunID, TenantID: attempt.TenantID, WorkspaceID: attempt.WorkspaceID,
		State: string(attempt.State), ExternalOperationID: attempt.ExternalOperationID,
		ErrorClassification: attempt.ErrorClassification, UnknownReason: attempt.UnknownReason,
		HasReconciliationEvidence: attempt.ReconciliationEvidenceRef != "",
		ReconciliationEvidenceRef: attempt.ReconciliationEvidenceRef,
		Revision:                  attempt.Revision, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
	}
}

func projectUnknownChannelV1(record ChannelDispatchRecord) UnknownAttemptProjectionV1 {
	attempt := record.Attempt
	return UnknownAttemptProjectionV1{
		Kind: UnknownAttemptChannelV1, AttemptID: attempt.AttemptID,
		RunID: attempt.RunID, TenantID: attempt.TenantID, WorkspaceID: attempt.WorkspaceID,
		EndpointID: attempt.EndpointID, ExternalOperationID: attempt.ExternalOperationID,
		ErrorClassification: attempt.ErrorClassification, UnknownReason: attempt.UnknownReason,
		HasReconciliationEvidence: attempt.ReconciliationEvidenceRef != "",
		ReconciliationEvidenceRef: attempt.ReconciliationEvidenceRef,
		Revision:                  attempt.Revision, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
	}
}

// ListModuleArtifactAdmissionsForTenantV1 uses Review ownership as the
// tenant/workspace fence. Artifact admissions remain globally immutable facts.
func (store *Store) ListModuleArtifactAdmissionsForTenantV1(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]ModuleArtifactAdmissionV1, bool, error) {
	if ctx == nil || limit < 1 || limit > maximumControlManagementQueryRowsV1 ||
		validatePublishedBasisTenantID(tenantID) != nil ||
		(workspaceID != "" && !validLeaseOpaqueID(workspaceID)) {
		return nil, false, ErrInvalidModuleArtifactIngress
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return nil, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	query := `SELECT DISTINCT artifact_admission_id
		FROM module_upgrade_reviews
		WHERE tenant_id=? AND artifact_admission_id IS NOT NULL`
	args := []any{tenantID}
	if workspaceID != "" {
		query += ` AND binding_target_kind='WORKSPACE_CHANNEL_ENDPOINT' AND workspace_id=?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY created_at DESC,artifact_admission_id COLLATE BINARY DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := connection.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	result := make([]ModuleArtifactAdmissionV1, 0, len(ids))
	for _, id := range ids {
		admission, found, loadErr := queryModuleArtifactAdmissionV1(ctx, connection, id)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if !found {
			return nil, false, ErrModuleArtifactIngressIntegrity
		}
		result = append(result, detachModuleArtifactAdmissionV1(admission))
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, false, err
	}
	committed = true
	return result, hasMore, nil
}

// VerifyReadOnlyV1 is a Store-owned read-only identity check. Callers receive
// the existing verification facts but never the physical database path.
func (store *Store) VerifyReadOnlyV1(ctx context.Context) (Verification, error) {
	if store == nil {
		return Verification{}, ErrStoreClosed
	}
	return VerifyCurrentStoreReadOnly(ctx, store.path)
}
