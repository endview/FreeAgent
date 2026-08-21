package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
)

var (
	// ErrInvalidFairScheduler identifies malformed tenant, owner, lease, or
	// concurrency-limit input. The scheduler grants no authority of its own.
	ErrInvalidFairScheduler = errors.New(
		"currentstore: invalid fair Scheduler request",
	)

	// ErrFairSchedulerIntegrity identifies an impossible scheduler counter,
	// Run/Frame head, or atomic claim result.
	ErrFairSchedulerIntegrity = errors.New(
		"currentstore: fair Scheduler integrity violation",
	)
)

// FairSchedulerLimits is the explicit three-level concurrency ceiling for one
// tenant-scoped production composition. An active unit is an unexpired Run
// lease, including leases acquired through the direct execution path.
type FairSchedulerLimits struct {
	GlobalWorkers         uint32
	MaxActivePerWorkspace uint32
	MaxActivePerFamily    uint32
}

// ClaimFairRunInput requests one atomic candidate selection and existing
// RunLease acquisition. TTL is measured by the Store wall clock exactly as it
// is for AcquireCurrentRunLease.
type ClaimFairRunInput struct {
	TenantID string
	OwnerID  string
	TTL      time.Duration
	Limits   FairSchedulerLimits
}

// FairSchedulerClaimStatus distinguishes semantic idleness from temporary
// capacity throttling. Neither non-CLAIMED status writes authoritative state.
type FairSchedulerClaimStatus string

const (
	FairSchedulerClaimed           FairSchedulerClaimStatus = "CLAIMED"
	FairSchedulerNoRunnable        FairSchedulerClaimStatus = "NO_RUNNABLE"
	FairSchedulerCapacityExhausted FairSchedulerClaimStatus = "CAPACITY_EXHAUSTED"
)

// FairRunClaimResult is the result of one atomic fair-claim attempt. For
// CLAIMED, the existing fenced RunLease is also the only execution permit;
// callers must not release and reacquire it before advancing the Universal
// Loop. All other fields are empty for non-CLAIMED statuses.
type FairRunClaimResult struct {
	Status               FairSchedulerClaimStatus
	Lease                RunLease
	TenantID             string
	WorkspaceID          string
	FamilyRootRunID      string
	WorkspaceServedUnits uint64
	WorkspaceRevision    uint64
}

// WorkspaceSchedulerState is the restart-safe fairness counter for one stable
// (tenant, workspace) key. It is accounting state, not a runnable queue.
type WorkspaceSchedulerState struct {
	TenantID    string
	WorkspaceID string
	ServedUnits uint64
	Revision    uint64
	UpdatedAt   time.Time
}

type fairRunCandidate struct {
	runID           string
	workspaceID     string
	familyRootRunID string
	runRevision     int64
	frameRevision   int64
	leaseEpoch      int64
	servedUnits     int64
	stateRevision   int64
}

type fairDecisionFamilyProjection struct {
	isDecision bool
	runnable   map[string]struct{}
}

// ClaimFairRun derives one runnable Run from current runs, loop_frames, and
// unexpired leases. Candidate selection, the three concurrency checks, lease
// acquisition, and fairness-counter increment commit in one BEGIN IMMEDIATE
// transaction. NO_RUNNABLE and CAPACITY_EXHAUSTED perform no authoritative
// row write.
func (store *Store) ClaimFairRun(
	ctx context.Context,
	input ClaimFairRunInput,
) (claim FairRunClaimResult, err error) {
	if ctx == nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidFairScheduler,
		)
	}
	if !validLeaseOpaqueID(input.TenantID) {
		return FairRunClaimResult{}, fmt.Errorf(
			"%w: invalid tenant ID",
			ErrInvalidFairScheduler,
		)
	}
	if !validLeaseOpaqueID(input.OwnerID) {
		return FairRunClaimResult{}, fmt.Errorf(
			"%w: invalid owner ID",
			ErrInvalidFairScheduler,
		)
	}
	if err := validateFairSchedulerLimits(input.Limits); err != nil {
		return FairRunClaimResult{}, err
	}
	ttlMicros, err := validateLeaseTTL(input.TTL)
	if err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidFairScheduler,
			err,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return FairRunClaimResult{}, err
	}
	defer unlock()

	observedAt := nowUnixMicro()
	expiry, err := leaseExpiry(observedAt, ttlMicros)
	if err != nil {
		return FairRunClaimResult{}, err
	}
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"currentstore: fair Scheduler connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"currentstore: begin fair Scheduler claim: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	runnable, err := hasFairRunnableCandidate(
		ctx,
		connection,
		input.TenantID,
	)
	if err != nil {
		return FairRunClaimResult{}, err
	}
	if !runnable {
		if _, commitErr := connection.ExecContext(ctx, `COMMIT`); commitErr != nil {
			return FairRunClaimResult{}, fmt.Errorf(
				"currentstore: commit empty fair Scheduler claim: %w",
				commitErr,
			)
		}
		committed = true
		return FairRunClaimResult{Status: FairSchedulerNoRunnable}, nil
	}

	candidate, err := selectFairRunCandidate(
		ctx,
		connection,
		input.TenantID,
		observedAt,
		input.Limits,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if _, commitErr := connection.ExecContext(ctx, `COMMIT`); commitErr != nil {
			return FairRunClaimResult{}, fmt.Errorf(
				"currentstore: commit throttled fair Scheduler claim: %w",
				commitErr,
			)
		}
		committed = true
		return FairRunClaimResult{Status: FairSchedulerCapacityExhausted}, nil
	}
	if err != nil {
		return FairRunClaimResult{}, err
	}
	if candidate.leaseEpoch == math.MaxInt64 ||
		candidate.frameRevision == math.MaxInt64 ||
		candidate.servedUnits == math.MaxInt64 ||
		candidate.stateRevision == math.MaxInt64 {
		return FairRunClaimResult{}, fmt.Errorf(
			"%w: claim revision or counter cannot advance",
			ErrFairSchedulerIntegrity,
		)
	}

	leaseResult, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=frame_revision+1,
			lease_owner=?,
			lease_epoch=lease_epoch+1,
			lease_expiry=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND lease_epoch=?
		  AND (lease_owner IS NULL OR lease_expiry<=?)
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.tenant_id=?
		        AND runs.revision=?
		        AND runs.cancel_request_ref IS NULL
		  )
	`,
		input.OwnerID,
		expiry,
		candidate.runID,
		candidate.frameRevision,
		candidate.leaseEpoch,
		observedAt,
		input.TenantID,
		candidate.runRevision,
	)
	if err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"currentstore: acquire fair Scheduler Run lease: %w",
			err,
		)
	}
	if err := requireLeaseCASRow(leaseResult, "fair Scheduler acquire"); err != nil {
		return FairRunClaimResult{}, err
	}

	stateResult, err := connection.ExecContext(ctx, `
		INSERT INTO workspace_scheduler_state(
			tenant_id, workspace_id, served_units, revision, updated_at
		) VALUES(?, ?, 1, 1, ?)
		ON CONFLICT(tenant_id, workspace_id) DO UPDATE SET
			served_units=workspace_scheduler_state.served_units+1,
			revision=workspace_scheduler_state.revision+1,
			updated_at=excluded.updated_at
		WHERE workspace_scheduler_state.served_units=?
		  AND workspace_scheduler_state.revision=?
	`,
		input.TenantID,
		candidate.workspaceID,
		observedAt,
		candidate.servedUnits,
		candidate.stateRevision,
	)
	if err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"currentstore: advance Workspace Scheduler state: %w",
			err,
		)
	}
	if err := requireExactlyOneSchedulerRow(stateResult, "advance Workspace state"); err != nil {
		return FairRunClaimResult{}, err
	}

	lease, err := newRunLease(
		candidate.runID,
		input.OwnerID,
		candidate.leaseEpoch+1,
		candidate.runRevision,
		candidate.frameRevision+1,
		expiry,
	)
	if err != nil {
		return FairRunClaimResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return FairRunClaimResult{}, fmt.Errorf(
			"currentstore: commit fair Scheduler claim: %w",
			err,
		)
	}
	committed = true
	return FairRunClaimResult{
		Status:               FairSchedulerClaimed,
		Lease:                lease,
		TenantID:             input.TenantID,
		WorkspaceID:          candidate.workspaceID,
		FamilyRootRunID:      candidate.familyRootRunID,
		WorkspaceServedUnits: uint64(candidate.servedUnits + 1),
		WorkspaceRevision:    uint64(candidate.stateRevision + 1),
	}, nil
}

func hasFairRunnableCandidate(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
) (bool, error) {
	candidates, err := loadFairRunnableCandidateHeads(
		ctx,
		connection,
		tenantID,
	)
	if err != nil {
		return false, err
	}
	cache := make(map[string]fairDecisionFamilyProjection)
	for _, candidate := range candidates {
		runnable, inspectErr := fairRunCandidateIsRunnable(
			ctx,
			connection,
			tenantID,
			candidate,
			cache,
		)
		if inspectErr != nil {
			return false, inspectErr
		}
		if runnable {
			return true, nil
		}
	}
	return false, nil
}

func selectFairRunCandidate(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
	observedAt int64,
	limits FairSchedulerLimits,
) (fairRunCandidate, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT
			r.run_id,
			r.workspace_id,
			CASE
				WHEN r.parent_run_id IS NULL THEN r.run_id
				ELSE r.parent_run_id
			END AS family_root_run_id,
			r.revision,
			f.frame_revision,
			f.lease_epoch,
			COALESCE(s.served_units, 0),
			COALESCE(s.revision, 0)
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		LEFT JOIN workspace_scheduler_state AS s
		  ON s.tenant_id=r.tenant_id AND s.workspace_id=r.workspace_id
		WHERE r.tenant_id=?
		  AND r.cancel_request_ref IS NULL
		  AND (f.lease_owner IS NULL OR f.lease_expiry<=?)
		  AND f.pending_attempt_id IS NULL
		  AND f.pending_dispatch_attempt_id IS NULL
		  AND NOT EXISTS(
		      SELECT 1
		      FROM channel_ingress_receipts AS ingress
		      WHERE ingress.run_id=r.run_id
		  )
		  AND (
		      (
		          r.state=?
		          AND r.disposition IS NULL
		          AND f.waiting_reason IS NULL
		          AND f.step IN (?, ?)
		      )
		      OR (
		          r.state=?
		          AND r.disposition=?
		          AND f.waiting_reason=?
		          AND f.step=?
		          AND (
		              (
		                  r.parent_run_id IS NULL
		                  AND EXISTS(
		                      SELECT 1 FROM runs AS family_member
		                      WHERE family_member.parent_run_id=r.run_id
		                  )
		              )
		              OR (
		                  r.parent_run_id IS NOT NULL
		                  AND r.parent_manifest_digest IS NOT NULL
		                  AND r.parent_slot_id IS NOT NULL
		              )
		          )
		      )
		  )
		  AND (
		      SELECT COUNT(*)
		      FROM runs AS active_run
		      JOIN loop_frames AS active_frame
		        ON active_frame.run_id=active_run.run_id
		      WHERE active_frame.lease_owner IS NOT NULL
		        AND active_frame.lease_expiry>?
		  ) < ?
		  AND (
		      SELECT COUNT(*)
		      FROM runs AS active_run
		      JOIN loop_frames AS active_frame
		        ON active_frame.run_id=active_run.run_id
		      WHERE active_run.tenant_id=r.tenant_id
		        AND active_run.workspace_id=r.workspace_id
		        AND active_frame.lease_owner IS NOT NULL
		        AND active_frame.lease_expiry>?
		  ) < ?
		  AND (
		      SELECT COUNT(*)
		      FROM runs AS active_run
		      JOIN loop_frames AS active_frame
		        ON active_frame.run_id=active_run.run_id
		      WHERE active_run.tenant_id=r.tenant_id
		        AND CASE
		              WHEN active_run.parent_run_id IS NULL THEN active_run.run_id
		              ELSE active_run.parent_run_id
		            END = CASE
		              WHEN r.parent_run_id IS NULL THEN r.run_id
		              ELSE r.parent_run_id
		            END
		        AND active_frame.lease_owner IS NOT NULL
		        AND active_frame.lease_expiry>?
		  ) < ?
		ORDER BY
			COALESCE(s.served_units, 0),
			r.workspace_id COLLATE BINARY,
			r.created_at,
			r.run_id COLLATE BINARY
	`,
		tenantID,
		observedAt,
		corecontract.InitialRunState,
		corecontract.InitialLoopStep,
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.InitialRunState,
		"WAITING_EXTERNAL",
		compositeChildrenPendingWaitingReason,
		corecontract.WaitingChildrenLoopStep,
		observedAt,
		int64(limits.GlobalWorkers),
		observedAt,
		int64(limits.MaxActivePerWorkspace),
		observedAt,
		int64(limits.MaxActivePerFamily),
	)
	if err != nil {
		return fairRunCandidate{}, fmt.Errorf(
			"currentstore: query fair Scheduler candidates: %w",
			err,
		)
	}
	defer rows.Close()
	candidates := make([]fairRunCandidate, 0)
	for rows.Next() {
		var candidate fairRunCandidate
		if err := rows.Scan(
			&candidate.runID,
			&candidate.workspaceID,
			&candidate.familyRootRunID,
			&candidate.runRevision,
			&candidate.frameRevision,
			&candidate.leaseEpoch,
			&candidate.servedUnits,
			&candidate.stateRevision,
		); err != nil {
			return fairRunCandidate{}, fmt.Errorf(
				"currentstore: scan fair Scheduler candidate: %w",
				err,
			)
		}
		if err := validateFairRunCandidate(candidate); err != nil {
			return fairRunCandidate{}, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return fairRunCandidate{}, fmt.Errorf(
			"currentstore: iterate fair Scheduler candidates: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return fairRunCandidate{}, fmt.Errorf(
			"currentstore: close fair Scheduler candidates: %w",
			err,
		)
	}
	cache := make(map[string]fairDecisionFamilyProjection)
	for _, candidate := range candidates {
		runnable, inspectErr := fairRunCandidateIsRunnable(
			ctx,
			connection,
			tenantID,
			candidate,
			cache,
		)
		if inspectErr != nil {
			return fairRunCandidate{}, inspectErr
		}
		if runnable {
			return candidate, nil
		}
	}
	return fairRunCandidate{}, sql.ErrNoRows
}

func loadFairRunnableCandidateHeads(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
) ([]fairRunCandidate, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT
			r.run_id,
			r.workspace_id,
			CASE
				WHEN r.parent_run_id IS NULL THEN r.run_id
				ELSE r.parent_run_id
			END AS family_root_run_id,
			r.revision,
			f.frame_revision,
			f.lease_epoch,
			COALESCE(s.served_units, 0),
			COALESCE(s.revision, 0)
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		LEFT JOIN workspace_scheduler_state AS s
		  ON s.tenant_id=r.tenant_id AND s.workspace_id=r.workspace_id
		WHERE r.tenant_id=?
		  AND r.cancel_request_ref IS NULL
		  AND f.pending_attempt_id IS NULL
		  AND f.pending_dispatch_attempt_id IS NULL
		  AND NOT EXISTS(
		      SELECT 1
		      FROM channel_ingress_receipts AS ingress
		      WHERE ingress.run_id=r.run_id
		  )
		  AND (
		      (
		          r.state=?
		          AND r.disposition IS NULL
		          AND f.waiting_reason IS NULL
		          AND f.step IN (?, ?)
		      )
		      OR (
		          r.state=?
		          AND r.disposition=?
		          AND f.waiting_reason=?
		          AND f.step=?
		          AND (
		              (
		                  r.parent_run_id IS NULL
		                  AND EXISTS(
		                      SELECT 1 FROM runs AS family_member
		                      WHERE family_member.parent_run_id=r.run_id
		                  )
		              )
		              OR (
		                  r.parent_run_id IS NOT NULL
		                  AND r.parent_manifest_digest IS NOT NULL
		                  AND r.parent_slot_id IS NOT NULL
		              )
		          )
		      )
		  )
	`,
		tenantID,
		corecontract.InitialRunState,
		corecontract.InitialLoopStep,
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.InitialRunState,
		"WAITING_EXTERNAL",
		compositeChildrenPendingWaitingReason,
		corecontract.WaitingChildrenLoopStep,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: inspect fair Scheduler runnable heads: %w",
			err,
		)
	}
	defer rows.Close()
	candidates := make([]fairRunCandidate, 0)
	for rows.Next() {
		var candidate fairRunCandidate
		if err := rows.Scan(
			&candidate.runID,
			&candidate.workspaceID,
			&candidate.familyRootRunID,
			&candidate.runRevision,
			&candidate.frameRevision,
			&candidate.leaseEpoch,
			&candidate.servedUnits,
			&candidate.stateRevision,
		); err != nil {
			return nil, fmt.Errorf(
				"currentstore: scan fair Scheduler runnable head: %w",
				err,
			)
		}
		if err := validateFairRunCandidate(candidate); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: iterate fair Scheduler runnable heads: %w",
			err,
		)
	}
	return candidates, nil
}

func fairRunCandidateIsRunnable(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
	candidate fairRunCandidate,
	cache map[string]fairDecisionFamilyProjection,
) (bool, error) {
	projection, found := cache[candidate.familyRootRunID]
	if !found {
		root, err := loadFairSchedulerRootManifest(
			ctx,
			connection,
			candidate.familyRootRunID,
			tenantID,
		)
		if err != nil {
			return false, err
		}
		projection = fairDecisionFamilyProjection{}
		if root.Composite != nil {
			if root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
				root.Composite.Plan == nil {
				return false, fmt.Errorf(
					"%w: Scheduler family root Manifest is not intact",
					ErrFairSchedulerIntegrity,
				)
			}
			if root.Composite.Plan.Decision != nil {
				frontier, loadErr := loadCompositeDecisionFrontier(
					ctx,
					connection,
					root,
				)
				if loadErr != nil {
					return false, fmt.Errorf(
						"%w: Decision frontier: %v",
						ErrFairSchedulerIntegrity,
						loadErr,
					)
				}
				projection.isDecision = true
				projection.runnable = make(
					map[string]struct{},
					len(frontier.RunnableRunIDs),
				)
				for _, runID := range frontier.RunnableRunIDs {
					projection.runnable[runID] = struct{}{}
				}
			}
		}
		cache[candidate.familyRootRunID] = projection
	}
	if projection.isDecision {
		_, runnable := projection.runnable[candidate.runID]
		return runnable, nil
	}
	return legacyFairRunCandidateIsRunnable(
		ctx,
		connection,
		candidate.runID,
	)
}

func legacyFairRunCandidateIsRunnable(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
) (bool, error) {
	var exists int
	err := connection.QueryRowContext(ctx, `
		SELECT EXISTS(
		    SELECT 1
		    FROM runs AS r
		    JOIN loop_frames AS f ON f.run_id=r.run_id
		    WHERE r.run_id=?
		      AND r.cancel_request_ref IS NULL
		      AND f.pending_attempt_id IS NULL
		      AND f.pending_dispatch_attempt_id IS NULL
		      AND NOT EXISTS(
		          SELECT 1
		          FROM channel_ingress_receipts AS ingress
		          WHERE ingress.run_id=r.run_id
		      )
		      AND (
		          (
		              r.state=?
		              AND r.disposition IS NULL
		              AND f.waiting_reason IS NULL
		              AND f.step IN (?, ?)
		          )
		          OR (
		              r.state=?
		              AND r.disposition=?
		              AND f.waiting_reason=?
		              AND f.step=?
		              AND (
		                  (
		                      r.parent_run_id IS NULL
		                      AND EXISTS(
		                          SELECT 1 FROM runs AS family_member
		                          WHERE family_member.parent_run_id=r.run_id
		                      )
		                      AND NOT EXISTS(
		                          SELECT 1
		                          FROM runs AS family_member
		                          JOIN loop_frames AS member_frame
		                            ON member_frame.run_id=family_member.run_id
		                          WHERE family_member.parent_run_id=r.run_id
		                            AND member_frame.step<>?
		                      )
		                  )
		                  OR (
		                      r.parent_run_id IS NOT NULL
		                      AND r.parent_manifest_digest IS NOT NULL
		                      AND r.parent_slot_id=?
		                      AND EXISTS(
		                          SELECT 1 FROM runs AS specialist
		                          WHERE specialist.parent_run_id=r.parent_run_id
		                            AND specialist.parent_slot_id IS NOT NULL
		                            AND specialist.parent_slot_id<>?
		                      )
		                      AND NOT EXISTS(
		                          SELECT 1
		                          FROM runs AS specialist
		                          JOIN loop_frames AS specialist_frame
		                            ON specialist_frame.run_id=specialist.run_id
		                          WHERE specialist.parent_run_id=r.parent_run_id
		                            AND specialist.parent_slot_id IS NOT NULL
		                            AND specialist.parent_slot_id<>?
		                            AND specialist_frame.step<>?
		                      )
		                  )
		              )
		          )
		      )
		)
	`,
		runID,
		corecontract.InitialRunState,
		corecontract.InitialLoopStep,
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.InitialRunState,
		"WAITING_EXTERNAL",
		compositeChildrenPendingWaitingReason,
		corecontract.WaitingChildrenLoopStep,
		corecontract.TerminatedLoopStep,
		corecontract.CompositeReviewerParentSlotIDV1,
		corecontract.CompositeReviewerParentSlotIDV1,
		corecontract.CompositeReviewerParentSlotIDV1,
		corecontract.TerminatedLoopStep,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf(
			"currentstore: inspect legacy fair Scheduler candidate: %w",
			err,
		)
	}
	if exists != 0 && exists != 1 {
		return false, fmt.Errorf(
			"%w: invalid legacy runnable projection %d",
			ErrFairSchedulerIntegrity,
			exists,
		)
	}
	return exists == 1, nil
}

func loadFairSchedulerRootManifest(
	ctx context.Context,
	connection *sql.Conn,
	rootRunID string,
	tenantID string,
) (corecontract.RunManifest, error) {
	var canonical []byte
	var digest string
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, rootRunID).Scan(&canonical, &digest); err != nil {
		return corecontract.RunManifest{}, fmt.Errorf(
			"%w: Scheduler family root Manifest is absent",
			ErrFairSchedulerIntegrity,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(canonical)
	if err != nil || manifest.RunID != rootRunID ||
		manifest.ManifestDigest != digest || manifest.TenantID != tenantID {
		return corecontract.RunManifest{}, fmt.Errorf(
			"%w: Scheduler family root Manifest differs",
			ErrFairSchedulerIntegrity,
		)
	}
	return manifest, nil
}

func validateFairRunCandidate(candidate fairRunCandidate) error {
	if !validLeaseOpaqueID(candidate.runID) ||
		!validLeaseOpaqueID(candidate.workspaceID) ||
		!validLeaseOpaqueID(candidate.familyRootRunID) ||
		candidate.runRevision < 0 ||
		candidate.frameRevision < 0 ||
		candidate.leaseEpoch < 0 ||
		candidate.servedUnits < 0 ||
		candidate.stateRevision < 0 ||
		candidate.servedUnits != candidate.stateRevision {
		return fmt.Errorf(
			"%w: selected candidate has an invalid persisted field",
			ErrFairSchedulerIntegrity,
		)
	}
	return nil
}

// GetWorkspaceSchedulerState returns the exact persisted fairness counter. A
// missing row is a valid zero-service state and is reported with found=false.
func (store *Store) GetWorkspaceSchedulerState(
	ctx context.Context,
	tenantID string,
	workspaceID string,
) (WorkspaceSchedulerState, bool, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) ||
		!validLeaseOpaqueID(workspaceID) {
		return WorkspaceSchedulerState{}, false, fmt.Errorf(
			"%w: invalid Scheduler state read",
			ErrInvalidFairScheduler,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return WorkspaceSchedulerState{}, false, err
	}
	defer unlock()

	var servedUnits int64
	var revision int64
	var updatedAt int64
	err = store.db.QueryRowContext(ctx, `
		SELECT served_units, revision, updated_at
		FROM workspace_scheduler_state
		WHERE tenant_id=? AND workspace_id=?
	`, tenantID, workspaceID).Scan(&servedUnits, &revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceSchedulerState{}, false, nil
	}
	if err != nil {
		return WorkspaceSchedulerState{}, false, fmt.Errorf(
			"currentstore: read Workspace Scheduler state: %w",
			err,
		)
	}
	updated, err := timeFromUnixMicro(updatedAt)
	if err != nil || servedUnits <= 0 || revision <= 0 ||
		servedUnits != revision {
		return WorkspaceSchedulerState{}, false, fmt.Errorf(
			"%w: invalid persisted Workspace state",
			ErrFairSchedulerIntegrity,
		)
	}
	return WorkspaceSchedulerState{
		TenantID:    tenantID,
		WorkspaceID: workspaceID,
		ServedUnits: uint64(servedUnits),
		Revision:    uint64(revision),
		UpdatedAt:   updated,
	}, true, nil
}

func validateFairSchedulerLimits(limits FairSchedulerLimits) error {
	if limits.GlobalWorkers == 0 ||
		limits.MaxActivePerWorkspace == 0 ||
		limits.MaxActivePerFamily == 0 {
		return fmt.Errorf(
			"%w: all concurrency limits must be positive",
			ErrInvalidFairScheduler,
		)
	}
	if limits.MaxActivePerWorkspace > limits.GlobalWorkers ||
		limits.MaxActivePerFamily > limits.GlobalWorkers {
		return fmt.Errorf(
			"%w: scoped concurrency limit exceeds global workers",
			ErrInvalidFairScheduler,
		)
	}
	return nil
}

func requireExactlyOneSchedulerRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Scheduler %s: %w",
			operation,
			err,
		)
	}
	if affected != 1 {
		return fmt.Errorf(
			"%w: Scheduler %s affected %d rows",
			ErrFairSchedulerIntegrity,
			operation,
			affected,
		)
	}
	return nil
}
