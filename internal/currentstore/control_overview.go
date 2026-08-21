package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const overviewMaximumJSONIntegerV1 = uint64(1<<53 - 1)

// LoadControlOverviewV1 returns all first-shell Overview sections from one
// coherent read-only transaction on the already-open sole Current Store.  An
// empty workspaceID selects exact TENANT scope; a non-empty value selects that
// exact current Workspace.  Every SQL collection is bounded by limit+1.
func (store *Store) LoadControlOverviewV1(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit uint16,
) (result controloverview.SnapshotV1, returnErr error) {
	defer func() {
		if returnErr != nil && store != nil {
			returnErr = classifySQLiteOwnerContention(store.path, returnErr)
		}
	}()
	if ctx == nil || limit == 0 || limit > controloverview.MaximumItemsV1 {
		return result, controloverview.ErrInvalidRequest
	}
	if err := validatePublishedBasisTenantID(tenantID); err != nil ||
		(workspaceID != "" && !validLeaseOpaqueID(workspaceID)) {
		return result, controloverview.ErrInvalidRequest
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("currentstore: begin Control Overview: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	basis, allWorkspaceRefs, sourceUpdatedAt, err :=
		loadOverviewBasisProjectionOnlineV1(ctx, tx, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, controloverview.ErrNotFound
	}
	if err != nil {
		return result, fmt.Errorf("%w: current Overview basis", controloverview.ErrIntegrity)
	}
	basisSourceUpdatedAt := sourceUpdatedAt
	workspaceRefs, workspaceTruncated, err :=
		projectOverviewWorkspacesV1(allWorkspaceRefs, workspaceID)
	if err != nil {
		return result, err
	}
	closures := newOverviewRunClosureMemoV1(tx)
	runs, runsTruncated, err := loadOverviewRunsV1(
		ctx, tx, closures, tenantID, workspaceID, int(limit),
	)
	if err != nil {
		return result, err
	}
	unknown, unknownTruncated, err := loadOverviewUnknownV1(
		ctx, tx, closures, nil, tenantID, workspaceID, int(limit),
	)
	if err != nil {
		return result, err
	}
	learning, learningTruncated, err := loadOverviewLearningV1(
		ctx, tx, tenantID, workspaceID, int(limit),
	)
	if err != nil {
		return result, err
	}
	candidates, candidatesTruncated, err := loadOverviewModuleCandidatesV1(
		ctx, tx, tenantID, workspaceID, int(limit),
	)
	if err != nil {
		return result, err
	}
	usage, usageTruncated, err := loadOverviewUsageV1(
		ctx, tx, closures, tenantID, workspaceID, int(limit),
	)
	if err != nil {
		return result, err
	}
	for _, item := range runs {
		sourceUpdatedAt = maxOverviewMicrosV1(sourceUpdatedAt, item.UpdatedAtUnixMicros)
	}
	for _, item := range unknown {
		sourceUpdatedAt = maxOverviewMicrosV1(sourceUpdatedAt, item.UpdatedAtUnixMicros)
	}
	for _, item := range learning {
		sourceUpdatedAt = maxOverviewMicrosV1(sourceUpdatedAt, item.UpdatedAtUnixMicros)
	}
	for _, item := range candidates {
		sourceUpdatedAt = maxOverviewMicrosV1(sourceUpdatedAt, item.CreatedAtUnixMicros)
	}
	for _, item := range usage {
		sourceUpdatedAt = maxOverviewMicrosV1(sourceUpdatedAt, item.UpdatedAtUnixMicros)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("currentstore: commit Control Overview: %w", err)
	}
	committed = true
	return controloverview.SnapshotV1{
		Basis:                          basis,
		BasisSourceUpdatedAtUnixMicros: basisSourceUpdatedAt,
		SourceUpdatedAtUnixMicros:      sourceUpdatedAt,
		Workspaces:                     workspaceRefs,
		WorkspacesTruncated:            workspaceTruncated,
		Runs:                           runs,
		RunsTruncated:                  runsTruncated,
		Unknown:                        unknown,
		UnknownTruncated:               unknownTruncated,
		Learning:                       learning,
		LearningTruncated:              learningTruncated,
		ModuleCandidates:               candidates,
		ModuleCandidatesTruncated:      candidatesTruncated,
		Usage:                          usage,
		UsageTruncated:                 usageTruncated,
	}, nil
}

func maxOverviewMicrosV1(left, right uint64) uint64 {
	if right > left {
		return right
	}
	return left
}

func projectOverviewWorkspacesV1(
	current []corecontract.WorkspaceRef,
	workspaceID string,
) ([]controloverview.WorkspaceRefV1, bool, error) {
	all := make(map[string]controloverview.WorkspaceRefV1, len(current))
	for _, ref := range current {
		if err := ref.Validate(); err != nil {
			return nil, false, fmt.Errorf("%w: Workspace ref", controloverview.ErrIntegrity)
		}
		if _, exists := all[ref.ID]; exists {
			return nil, false, fmt.Errorf("%w: duplicate Workspace", controloverview.ErrIntegrity)
		}
		all[ref.ID] = ref
	}
	if workspaceID != "" {
		ref, found := all[workspaceID]
		if !found {
			// Selector options describe only the current Control. Historical facts
			// retain their frozen Workspace even after that definition is removed.
			return []controloverview.WorkspaceRefV1{}, false, nil
		}
		return []controloverview.WorkspaceRefV1{ref}, false, nil
	}
	refs := make([]controloverview.WorkspaceRefV1, 0, len(all))
	for _, ref := range all {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	if len(refs) > int(controloverview.MaximumWorkspacesV1) {
		return nil, false, fmt.Errorf("%w: too many Workspaces", controloverview.ErrIntegrity)
	}
	return refs, false, nil
}

type overviewRunCandidateV1 struct {
	item controloverview.RunV1
}

type overviewRunClosureMemoEntryV1 struct {
	run runObservationRecordV1
	err error
}

type overviewRunClosureMemoV1 struct {
	tx      *sql.Tx
	entries map[string]overviewRunClosureMemoEntryV1
}

func newOverviewRunClosureMemoV1(tx *sql.Tx) *overviewRunClosureMemoV1 {
	return &overviewRunClosureMemoV1{
		tx: tx, entries: make(map[string]overviewRunClosureMemoEntryV1),
	}
}

func (memo *overviewRunClosureMemoV1) load(
	ctx context.Context,
	runID string,
) (runObservationRecordV1, error) {
	if memo == nil || memo.tx == nil || !validLeaseOpaqueID(runID) {
		return runObservationRecordV1{}, overviewClosureErrorV1(ctx, "Run closure memo", ErrLoopIntegrity)
	}
	if entry, ok := memo.entries[runID]; ok {
		return entry.run, entry.err
	}
	run, err := loadRunObservationHeadForOverviewV1(ctx, memo.tx, runID)
	if err != nil {
		err = overviewClosureErrorV1(ctx, "Run semantic closure", err)
	}
	memo.entries[runID] = overviewRunClosureMemoEntryV1{run: run, err: err}
	return run, err
}

func loadOverviewRunsV1(
	ctx context.Context,
	tx *sql.Tx,
	closures *overviewRunClosureMemoV1,
	tenantID, workspaceID string,
	limit int,
) ([]controloverview.RunV1, bool, error) {
	query := `SELECT tenant_id,workspace_id,run_id,run_state,run_revision,created_at,updated_at
		FROM run_observation_heads INDEXED BY run_observation_heads_overview_tenant_recent_idx
		WHERE tenant_id=? ORDER BY updated_at DESC,run_id COLLATE BINARY DESC LIMIT ?`
	args := []any{tenantID, limit + 1}
	if workspaceID != "" {
		query = `SELECT tenant_id,workspace_id,run_id,run_state,run_revision,created_at,updated_at
			FROM run_observation_heads INDEXED BY run_observation_heads_overview_workspace_recent_idx
			WHERE tenant_id=? AND workspace_id=?
			ORDER BY updated_at DESC,run_id COLLATE BINARY DESC LIMIT ?`
		args = []any{tenantID, workspaceID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("currentstore: query Overview Runs: %w", err)
	}
	defer rows.Close()
	candidates := make([]overviewRunCandidateV1, 0, limit+1)
	for rows.Next() {
		var candidate overviewRunCandidateV1
		var revision, created, updated int64
		if err := rows.Scan(&candidate.item.TenantID, &candidate.item.WorkspaceID,
			&candidate.item.RunID, &candidate.item.State,
			&revision, &created, &updated); err != nil {
			return nil, false, fmt.Errorf("currentstore: scan Overview Run: %w", err)
		}
		if !validOverviewResourceV1(candidate.item.TenantID, candidate.item.WorkspaceID,
			candidate.item.RunID, tenantID, workspaceID) ||
			!validOverviewTextV1(candidate.item.State) ||
			!validOverviewNumbersV1(revision, created, updated) || updated < created {
			return nil, false, fmt.Errorf("%w: Run projection", controloverview.ErrIntegrity)
		}
		candidate.item.Revision, candidate.item.CreatedAtUnixMicros,
			candidate.item.UpdatedAtUnixMicros =
			uint64(revision), uint64(created), uint64(updated)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("currentstore: iterate Overview Runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("currentstore: close Overview Runs: %w", err)
	}
	items := make([]controloverview.RunV1, 0, len(candidates))
	for _, candidate := range candidates {
		run, err := closures.load(ctx, candidate.item.RunID)
		if err != nil {
			return nil, false, err
		}
		item := candidate.item
		snapshot := run.Snapshot
		item.Disposition = snapshot.Disposition
		if snapshot.RunID != item.RunID || snapshot.RunState != item.State ||
			snapshot.Disposition != item.Disposition || snapshot.RunRevision != item.Revision ||
			snapshot.TenantID != item.TenantID || snapshot.WorkspaceID != item.WorkspaceID ||
			snapshot.CreatedAtUnixMicros != item.CreatedAtUnixMicros ||
			snapshot.UpdatedAtUnixMicros != item.UpdatedAtUnixMicros {
			return nil, false, overviewClosureErrorV1(
				ctx, "Run candidate semantic projection", ErrLoopIntegrity,
			)
		}
		items = append(items, item)
	}
	items, truncated := trimOverviewPageV1(items, limit)
	return items, truncated, nil
}

func overviewClosureErrorV1(ctx context.Context, label string, source error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s: %v", controloverview.ErrIntegrity, label, source)
}

type overviewUnknownCandidateV1 struct {
	kind, resourceID string
	updated          uint64
}

func loadOverviewUnknownV1(
	ctx context.Context,
	tx *sql.Tx,
	closures *overviewRunClosureMemoV1,
	_ any,
	tenantID, workspaceID string,
	limit int,
) ([]controloverview.UnknownV1, bool, error) {
	all := make([]overviewUnknownCandidateV1, 0, 5*(limit+1))
	queries := []struct {
		kind, resourceKind, state string
	}{
		{controloverview.UnknownKindModelV1, overviewResourceModelV1, "MODEL_UNKNOWN"},
		{controloverview.UnknownKindActionV1, overviewResourceActionV1, "UNKNOWN"},
		{controloverview.UnknownKindChannelSendV1, overviewResourceChannelV1, "UNKNOWN"},
		{controloverview.UnknownKindLearningProposalV1, overviewResourceLearningProposalV1, "REVIEW_UNKNOWN"},
		{controloverview.UnknownKindLearningTaskV1, overviewResourceLearningTaskV1, "UNKNOWN"},
	}
	for _, source := range queries {
		query := `SELECT resource_id,updated_at FROM overview_resource_heads
			INDEXED BY overview_resource_heads_tenant_state_recent_idx
			WHERE resource_kind=? AND tenant_id=? AND state=?
			ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
		args := []any{source.resourceKind, tenantID, source.state, limit + 1}
		if workspaceID != "" {
			query = `SELECT resource_id,updated_at FROM overview_resource_heads
				INDEXED BY overview_resource_heads_workspace_state_recent_idx
				WHERE resource_kind=? AND tenant_id=? AND workspace_id=? AND state=?
				ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
			args = []any{source.resourceKind, tenantID, workspaceID, source.state, limit + 1}
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, false, fmt.Errorf("currentstore: query Overview %s UNKNOWN: %w", source.kind, err)
		}
		for rows.Next() {
			var candidate overviewUnknownCandidateV1
			var updated int64
			candidate.kind = source.kind
			if err := rows.Scan(&candidate.resourceID, &updated); err != nil {
				_ = rows.Close()
				return nil, false, fmt.Errorf("currentstore: scan Overview UNKNOWN: %w", err)
			}
			if !validLeaseOpaqueID(candidate.resourceID) || !validOverviewMicrosV1(updated) {
				_ = rows.Close()
				return nil, false, overviewClosureErrorV1(ctx, "UNKNOWN candidate", ErrLoopIntegrity)
			}
			candidate.updated = uint64(updated)
			all = append(all, candidate)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("currentstore: iterate Overview UNKNOWN: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, false, fmt.Errorf("currentstore: close Overview UNKNOWN: %w", err)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		left, right := all[i], all[j]
		if left.updated != right.updated {
			return left.updated > right.updated
		}
		if left.resourceID != right.resourceID {
			return left.resourceID > right.resourceID
		}
		return left.kind > right.kind
	})
	if len(all) > limit+1 {
		all = all[:limit+1]
	}
	truncated := len(all) > limit
	itemCapacity := len(all)
	if itemCapacity > limit {
		itemCapacity = limit
	}
	items := make([]controloverview.UnknownV1, 0, itemCapacity)
	for index, candidate := range all {
		resourceKind := ""
		expectedState := "UNKNOWN"
		switch candidate.kind {
		case controloverview.UnknownKindModelV1:
			resourceKind, expectedState = overviewResourceModelV1, "MODEL_UNKNOWN"
		case controloverview.UnknownKindActionV1:
			resourceKind = overviewResourceActionV1
		case controloverview.UnknownKindChannelSendV1:
			resourceKind = overviewResourceChannelV1
		case controloverview.UnknownKindLearningProposalV1:
			resourceKind, expectedState = overviewResourceLearningProposalV1, "REVIEW_UNKNOWN"
		case controloverview.UnknownKindLearningTaskV1:
			resourceKind = overviewResourceLearningTaskV1
		}
		record, err := loadOverviewResourceHeadOnlineV1(ctx, tx, resourceKind, candidate.resourceID)
		if err != nil || record.Snapshot.State != expectedState ||
			record.Snapshot.UpdatedAtUnixMicros != candidate.updated {
			return nil, false, overviewClosureErrorV1(ctx, "UNKNOWN resource head", err)
		}
		snapshot := record.Snapshot
		runRef := snapshot.SubjectRun
		if candidate.kind == controloverview.UnknownKindLearningProposalV1 {
			runRef = snapshot.RelatedRun
		}
		if runRef == nil {
			return nil, false, overviewClosureErrorV1(ctx, "UNKNOWN Run ref", ErrLoopIntegrity)
		}
		if _, err := loadOverviewExactRunScopeV1(ctx, closures, runRef.RunID,
			snapshot.TenantID, snapshot.WorkspaceID); err != nil {
			return nil, false, err
		}
		item := controloverview.UnknownV1{Kind: candidate.kind, ResourceID: candidate.resourceID,
			TenantID: snapshot.TenantID, WorkspaceID: snapshot.WorkspaceID,
			RunID: runRef.RunID, Revision: snapshot.ResourceRevision,
			UpdatedAtUnixMicros: snapshot.UpdatedAtUnixMicros}
		if !validOverviewResourceV1(item.TenantID, item.WorkspaceID, item.ResourceID,
			tenantID, workspaceID) {
			return nil, false, overviewClosureErrorV1(ctx, "UNKNOWN scope", ErrLoopIntegrity)
		}
		// The global N+1 fact is semantically closed before it is used to prove
		// truncation, but remains outside the bounded representation.
		if index < limit {
			items = append(items, item)
		}
	}
	return items, truncated, nil
}

func loadOverviewExactRunScopeV1(
	ctx context.Context,
	closures *overviewRunClosureMemoV1,
	runID, tenantID, workspaceID string,
) (runObservationRecordV1, error) {
	run, err := closures.load(ctx, runID)
	if err != nil {
		return runObservationRecordV1{}, err
	}
	if run.Snapshot.RunID != runID || run.Snapshot.TenantID != tenantID ||
		run.Snapshot.WorkspaceID != workspaceID {
		return runObservationRecordV1{}, overviewClosureErrorV1(
			ctx, "related Run exact scope", ErrAdmissionIntegrity,
		)
	}
	return run, nil
}
func loadOverviewLearningV1(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, workspaceID string,
	limit int,
) ([]controloverview.LearningV1, bool, error) {
	query := `SELECT resource_id FROM overview_resource_heads
		INDEXED BY overview_resource_heads_tenant_recent_idx
		WHERE resource_kind='LEARNING_PROPOSAL' AND tenant_id=?
		ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
	args := []any{tenantID, limit + 1}
	if workspaceID != "" {
		query = `SELECT resource_id FROM overview_resource_heads
			INDEXED BY overview_resource_heads_workspace_recent_idx
			WHERE resource_kind='LEARNING_PROPOSAL' AND tenant_id=? AND workspace_id=?
			ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
		args = []any{tenantID, workspaceID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("currentstore: query Overview Learning: %w", err)
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("currentstore: scan Overview Learning: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, fmt.Errorf("currentstore: iterate Overview Learning: %w", err)
	}
	_ = rows.Close()
	items := make([]controloverview.LearningV1, 0, len(ids))
	for _, id := range ids {
		record, err := loadOverviewResourceHeadOnlineV1(
			ctx, tx, overviewResourceLearningProposalV1, id,
		)
		s := record.Snapshot
		if err != nil || !validOverviewResourceV1(s.TenantID, s.WorkspaceID,
			s.ResourceID, tenantID, workspaceID) {
			return nil, false, overviewClosureErrorV1(ctx, "Learning resource head", err)
		}
		items = append(items, controloverview.LearningV1{
			ProposalID: s.ResourceID, TenantID: s.TenantID, WorkspaceID: s.WorkspaceID,
			Kind: s.ProposalKind, State: s.State, Revision: s.ResourceRevision,
			CreatedAtUnixMicros: s.CreatedAtUnixMicros,
			UpdatedAtUnixMicros: s.UpdatedAtUnixMicros,
		})
	}
	items, truncated := trimOverviewPageV1(items, limit)
	return items, truncated, nil
}

func loadOverviewModuleCandidatesV1(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, workspaceID string,
	limit int,
) ([]controloverview.ModuleCandidateV1, bool, error) {
	query := `SELECT resource_id FROM overview_resource_heads
		INDEXED BY overview_resource_heads_module_tenant_recent_idx
		WHERE resource_kind='MODULE_REVIEW' AND tenant_id=?
		ORDER BY created_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
	args := []any{tenantID, limit + 1}
	if workspaceID != "" {
		query = `SELECT resource_id FROM overview_resource_heads
			INDEXED BY overview_resource_heads_module_workspace_recent_idx
			WHERE resource_kind='MODULE_REVIEW' AND tenant_id=?
			AND binding_target_kind='WORKSPACE_CHANNEL_ENDPOINT' AND workspace_id=?
			ORDER BY created_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
		args = []any{tenantID, workspaceID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("currentstore: query Overview Module candidates: %w", err)
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("currentstore: scan Overview Module candidate: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, fmt.Errorf("currentstore: iterate Overview Module candidates: %w", err)
	}
	_ = rows.Close()
	items := make([]controloverview.ModuleCandidateV1, 0, len(ids))
	for _, id := range ids {
		record, err := loadOverviewResourceHeadOnlineV1(
			ctx, tx, overviewResourceModuleReviewV1, id,
		)
		s := record.Snapshot
		if err != nil || s.TenantID != tenantID ||
			(workspaceID != "" && (s.BindingTargetKind != "WORKSPACE_CHANNEL_ENDPOINT" ||
				s.WorkspaceID != workspaceID)) ||
			(workspaceID == "" && s.BindingTargetKind != "PROFILE" &&
				s.BindingTargetKind != "WORKSPACE_CHANNEL_ENDPOINT") {
			return nil, false, overviewClosureErrorV1(ctx, "Module resource head", err)
		}
		items = append(items, controloverview.ModuleCandidateV1{
			ReviewID: s.ResourceID, CandidateID: s.CandidateID, TenantID: s.TenantID,
			WorkspaceID: s.WorkspaceID, BindingTargetKind: s.BindingTargetKind,
			CurrentInstanceID: s.CurrentInstanceID, TargetInstanceID: s.TargetInstanceID,
			CurrentModuleID: s.CurrentModuleID, CurrentExactVersion: s.CurrentExactVersion,
			CurrentArtifactDigest: s.CurrentArtifactDigest, TargetModuleID: s.TargetModuleID,
			TargetExactVersion: s.TargetExactVersion, TargetArtifactDigest: s.TargetArtifactDigest,
			Conclusion: s.State, CreatedAtUnixMicros: s.CreatedAtUnixMicros,
		})
	}
	items, truncated := trimOverviewPageV1(items, limit)
	return items, truncated, nil
}

func loadOverviewUsageV1(
	ctx context.Context,
	tx *sql.Tx,
	closures *overviewRunClosureMemoV1,
	tenantID, workspaceID string,
	limit int,
) ([]controloverview.UsageV1, bool, error) {
	query := `SELECT resource_id FROM overview_resource_heads
		INDEXED BY overview_resource_heads_tenant_recent_idx
		WHERE resource_kind='MODEL' AND tenant_id=?
		ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
	args := []any{tenantID, limit + 1}
	if workspaceID != "" {
		query = `SELECT resource_id FROM overview_resource_heads
			INDEXED BY overview_resource_heads_workspace_recent_idx
			WHERE resource_kind='MODEL' AND tenant_id=? AND workspace_id=?
			ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`
		args = []any{tenantID, workspaceID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("currentstore: query Overview Usage: %w", err)
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, false, fmt.Errorf("currentstore: scan Overview Usage: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, fmt.Errorf("currentstore: iterate Overview Usage: %w", err)
	}
	_ = rows.Close()
	items := make([]controloverview.UsageV1, 0, len(ids))
	for _, id := range ids {
		record, err := loadOverviewResourceHeadOnlineV1(ctx, tx, overviewResourceModelV1, id)
		s := record.Snapshot
		if err != nil || s.SubjectRun == nil || s.UsageRevision == nil ||
			!validOverviewResourceV1(s.TenantID, s.WorkspaceID, s.ResourceID,
				tenantID, workspaceID) {
			return nil, false, overviewClosureErrorV1(ctx, "Usage resource head", err)
		}
		if _, err := loadOverviewExactRunScopeV1(ctx, closures, s.SubjectRun.RunID,
			s.TenantID, s.WorkspaceID); err != nil {
			return nil, false, err
		}
		items = append(items, controloverview.UsageV1{
			AttemptID: s.ResourceID, RunID: s.SubjectRun.RunID, TenantID: s.TenantID,
			WorkspaceID: s.WorkspaceID, Revision: *s.UsageRevision,
			InputTokens:          cloneOverviewTokenV1(s.InputTokens),
			CachedInputTokens:    cloneOverviewTokenV1(s.CachedInputTokens),
			UncachedInputTokens:  cloneOverviewTokenV1(s.UncachedInputTokens),
			OutputTokens:         cloneOverviewTokenV1(s.OutputTokens),
			ReasoningTokens:      cloneOverviewTokenV1(s.ReasoningTokens),
			ReconciliationStatus: s.UsageStatus,
			UpdatedAtUnixMicros:  s.UpdatedAtUnixMicros,
		})
	}
	items, truncated := trimOverviewPageV1(items, limit)
	return items, truncated, nil
}
func validOverviewUsageStatusStoreV1(value string) bool {
	switch value {
	case modelUsageStatusPending, modelUsageStatusReported,
		modelUsageStatusNoReport, modelUsageStatusReconciliation:
		return true
	default:
		return false
	}
}

func validOverviewUsageTokensStoreV1(tokens corecontract.UsageTokens) bool {
	if err := tokens.Validate(); err != nil {
		return false
	}
	for _, value := range []*uint64{tokens.Input, tokens.CachedInput,
		tokens.UncachedInput, tokens.Output, tokens.Reasoning} {
		if value != nil && *value > overviewMaximumJSONIntegerV1 {
			return false
		}
	}
	return true
}

func cloneOverviewTokenV1(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validOverviewResourceV1(
	actualTenant, actualWorkspace, resourceID, requestedTenant, requestedWorkspace string,
) bool {
	if actualTenant != requestedTenant || !validLeaseOpaqueID(resourceID) ||
		!validLeaseOpaqueID(actualWorkspace) {
		return false
	}
	if requestedWorkspace != "" && actualWorkspace != requestedWorkspace {
		return false
	}
	return true
}

func validOverviewTextV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != strings.TrimSpace(value) || value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validOverviewNumbersV1(revision, created, updated int64) bool {
	return revision >= 0 && uint64(revision) <= overviewMaximumJSONIntegerV1 &&
		validOverviewMicrosV1(created) && validOverviewMicrosV1(updated)
}

func validOverviewMicrosV1(value int64) bool {
	return value > 0 && uint64(value) <= overviewMaximumJSONIntegerV1
}

func trimOverviewPageV1[T any](items []T, limit int) ([]T, bool) {
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	if items == nil {
		items = []T{}
	}
	return items, truncated
}
