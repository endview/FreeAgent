package currentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
)

var fairSchedulerTestLimits = FairSchedulerLimits{
	GlobalWorkers:         3,
	MaxActivePerWorkspace: 1,
	MaxActivePerFamily:    1,
}

func TestClaimFairRunOrdersLeastServedWorkspaceAndPersistsState(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	for index, workspaceID := range workspaceIDs {
		commitFairSchedulerRun(
			t,
			fixture,
			workspaceID,
			fmt.Sprintf("scheduler-run-%d", index),
			fmt.Sprintf("scheduler-admission-%d", index),
		)
	}

	for index, wantWorkspace := range workspaceIDs {
		claim, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  fmt.Sprintf("scheduler-owner-%d", index),
				TTL:      time.Minute,
				Limits:   fairSchedulerTestLimits,
			},
		)
		if err != nil {
			t.Fatalf("ClaimFairRun(%d): %v", index, err)
		}
		if claim.Status != FairSchedulerClaimed ||
			claim.WorkspaceID != wantWorkspace ||
			claim.WorkspaceServedUnits != 1 ||
			claim.WorkspaceRevision != 1 ||
			claim.Lease.RunID != fmt.Sprintf("scheduler-run-%d", index) {
			t.Fatalf("claim %d=%+v want Workspace %q", index, claim, wantWorkspace)
		}
		if err := fixture.store.ReleaseRunLease(
			context.Background(),
			claim.Lease,
		); err != nil {
			t.Fatalf("ReleaseRunLease(%d): %v", index, err)
		}
	}

	for _, workspaceID := range workspaceIDs {
		state, found, err := fixture.store.GetWorkspaceSchedulerState(
			context.Background(),
			fixture.intent.TenantID,
			workspaceID,
		)
		if err != nil || !found || state.ServedUnits != 1 || state.Revision != 1 {
			t.Fatalf("Scheduler state %q=(%+v,%v,%v)", workspaceID, state, found, err)
		}
	}
}

func TestClaimFairRunPersistsFairnessAcrossStoreRestart(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	for index, workspaceID := range workspaceIDs {
		commitFairSchedulerRun(
			t,
			fixture,
			workspaceID,
			fmt.Sprintf("scheduler-restart-run-%d", index),
			fmt.Sprintf("scheduler-restart-admission-%d", index),
		)
	}
	first, err := fixture.store.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-before-restart",
			TTL:      time.Minute,
			Limits:   fairSchedulerTestLimits,
		},
	)
	if err != nil || first.Status != FairSchedulerClaimed ||
		first.WorkspaceID != workspaceIDs[0] {
		t.Fatalf("pre-restart claim=%+v error=%v", first, err)
	}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		first.Lease,
	); err != nil {
		t.Fatalf("release pre-restart claim: %v", err)
	}

	databasePath := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen Current Store: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened Store: %v", err)
		}
	})
	state, found, err := reopened.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.intent.TenantID,
		workspaceIDs[0],
	)
	if err != nil || !found || state.ServedUnits != 1 || state.Revision != 1 {
		t.Fatalf("restarted state=%+v found=%v error=%v", state, found, err)
	}
	second, err := reopened.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-after-restart",
			TTL:      time.Minute,
			Limits:   fairSchedulerTestLimits,
		},
	)
	if err != nil || second.Status != FairSchedulerClaimed ||
		second.WorkspaceID != workspaceIDs[1] {
		t.Fatalf("post-restart claim=%+v error=%v", second, err)
	}
	if err := reopened.ReleaseRunLease(
		context.Background(),
		second.Lease,
	); err != nil {
		t.Fatalf("release post-restart claim: %v", err)
	}
}

func TestClaimFairRunSustainedBacklogDoesNotStarveAcrossStoreRestart(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	if len(workspaceIDs) != 3 {
		t.Fatalf("workspace count=%d want 3", len(workspaceIDs))
	}

	runIDs := make(map[string]string, len(workspaceIDs))
	for index, workspaceID := range workspaceIDs {
		runID := fmt.Sprintf("scheduler-sustained-run-%d", index)
		runIDs[workspaceID] = runID
		commitFairSchedulerRun(
			t,
			fixture,
			workspaceID,
			runID,
			fmt.Sprintf("scheduler-sustained-admission-%d", index),
		)
	}

	activeStore := fixture.store
	serviceCounts := make(map[string]int, len(workspaceIDs))
	firstService := make(map[string]int, len(workspaceIDs))
	lastService := make(map[string]int, len(workspaceIDs))
	for serviceIndex := 1; serviceIndex <= 900; serviceIndex++ {
		claim, err := activeStore.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  fmt.Sprintf("scheduler-sustained-owner-%d", serviceIndex),
				TTL:      time.Minute,
				Limits:   fairSchedulerTestLimits,
			},
		)
		if err != nil {
			t.Fatalf("ClaimFairRun(%d): %v", serviceIndex, err)
		}
		if claim.Status != FairSchedulerClaimed {
			t.Fatalf("ClaimFairRun(%d) status=%q want %q", serviceIndex, claim.Status, FairSchedulerClaimed)
		}
		wantRunID, knownWorkspace := runIDs[claim.WorkspaceID]
		if !knownWorkspace {
			t.Fatalf("ClaimFairRun(%d) selected unknown Workspace %q", serviceIndex, claim.WorkspaceID)
		}
		if claim.Lease.RunID != wantRunID {
			t.Fatalf(
				"ClaimFairRun(%d) Run=%q want %q for Workspace %q",
				serviceIndex,
				claim.Lease.RunID,
				wantRunID,
				claim.WorkspaceID,
			)
		}

		previousService, seen := lastService[claim.WorkspaceID]
		if !seen {
			firstService[claim.WorkspaceID] = serviceIndex
		} else if gap := serviceIndex - previousService; gap > 3 {
			t.Fatalf(
				"Workspace %q service gap=%d between %d and %d; want <=3",
				claim.WorkspaceID,
				gap,
				previousService,
				serviceIndex,
			)
		}
		lastService[claim.WorkspaceID] = serviceIndex
		serviceCounts[claim.WorkspaceID]++
		wantCount := uint64(serviceCounts[claim.WorkspaceID])
		if claim.WorkspaceServedUnits != wantCount || claim.WorkspaceRevision != wantCount {
			t.Fatalf(
				"ClaimFairRun(%d) Workspace %q accounting=(served=%d revision=%d) want %d",
				serviceIndex,
				claim.WorkspaceID,
				claim.WorkspaceServedUnits,
				claim.WorkspaceRevision,
				wantCount,
			)
		}

		if err := activeStore.ReleaseRunLease(context.Background(), claim.Lease); err != nil {
			t.Fatalf("ReleaseRunLease(%d): %v", serviceIndex, err)
		}

		if serviceIndex == 450 {
			databasePath := activeStore.Path()
			if err := activeStore.Close(); err != nil {
				t.Fatalf("close after service %d: %v", serviceIndex, err)
			}
			reopened, err := OpenExistingCurrentStore(context.Background(), databasePath)
			if err != nil {
				t.Fatalf("reopen Current Store after service %d: %v", serviceIndex, err)
			}
			t.Cleanup(func() {
				if err := reopened.Close(); err != nil {
					t.Errorf("close sustained-backlog reopened Store: %v", err)
				}
			})
			activeStore = reopened
		}
	}

	for _, workspaceID := range workspaceIDs {
		first, served := firstService[workspaceID]
		if !served || first > 3 {
			t.Errorf("Workspace %q first service=%d found=%v; want <=3", workspaceID, first, served)
		}
		if got := serviceCounts[workspaceID]; got != 300 {
			t.Errorf("Workspace %q service count=%d want 300", workspaceID, got)
		}
		state, found, err := activeStore.GetWorkspaceSchedulerState(
			context.Background(),
			fixture.intent.TenantID,
			workspaceID,
		)
		if err != nil {
			t.Errorf("GetWorkspaceSchedulerState(%q): %v", workspaceID, err)
			continue
		}
		if !found {
			t.Errorf("GetWorkspaceSchedulerState(%q) not found", workspaceID)
			continue
		}
		if state.ServedUnits != 300 || state.Revision != 300 {
			t.Errorf(
				"Workspace %q persisted accounting=(served=%d revision=%d) want (300,300)",
				workspaceID,
				state.ServedUnits,
				state.Revision,
			)
		}
	}
}

func TestClaimFairRunDistinguishesNoRunnableAndCapacityExhausted(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	noWork, err := fixture.store.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-empty-owner",
			TTL:      time.Minute,
			Limits:   fairSchedulerTestLimits,
		},
	)
	if err != nil || noWork.Status != FairSchedulerNoRunnable {
		t.Fatalf("empty ClaimFairRun=%+v error=%v", noWork, err)
	}

	runID := "scheduler-capacity-run"
	commitFairSchedulerRun(
		t,
		fixture,
		workspaceIDs[0],
		runID,
		"scheduler-capacity-admission",
	)
	directLease, err := fixture.store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "direct-owner",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease: %v", err)
	}
	throttled, err := fixture.store.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-throttled-owner",
			TTL:      time.Minute,
			Limits: FairSchedulerLimits{
				GlobalWorkers:         1,
				MaxActivePerWorkspace: 1,
				MaxActivePerFamily:    1,
			},
		},
	)
	if err != nil || throttled.Status != FairSchedulerCapacityExhausted {
		t.Fatalf("throttled ClaimFairRun=%+v error=%v", throttled, err)
	}
	if _, found, stateErr := fixture.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.intent.TenantID,
		workspaceIDs[0],
	); stateErr != nil || found {
		t.Fatalf("throttled claim wrote Scheduler state found=%v error=%v", found, stateErr)
	}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		directLease,
	); err != nil {
		t.Fatalf("ReleaseRunLease direct: %v", err)
	}

	claimed, err := fixture.store.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-after-capacity-owner",
			TTL:      time.Minute,
			Limits:   fairSchedulerTestLimits,
		},
	)
	if err != nil || claimed.Status != FairSchedulerClaimed ||
		claimed.Lease.RunID != runID {
		t.Fatalf("post-capacity ClaimFairRun=%+v error=%v", claimed, err)
	}
	if err := fixture.store.ReleaseRunLease(context.Background(), claimed.Lease); err != nil {
		t.Fatalf("ReleaseRunLease claimed: %v", err)
	}
}

func TestClaimFairRunEnforcesWorkspaceAndFamilyLimits(t *testing.T) {
	t.Run("workspace", func(t *testing.T) {
		fixture, workspaceIDs := newFairSchedulerFixture(t)
		for index := 0; index < 2; index++ {
			commitFairSchedulerRun(
				t,
				fixture,
				workspaceIDs[0],
				fmt.Sprintf("scheduler-workspace-cap-run-%d", index),
				fmt.Sprintf("scheduler-workspace-cap-admission-%d", index),
			)
		}
		limits := FairSchedulerLimits{
			GlobalWorkers: 3, MaxActivePerWorkspace: 1,
			MaxActivePerFamily: 3,
		}
		first, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  "scheduler-workspace-cap-owner-1",
				TTL:      time.Minute, Limits: limits,
			},
		)
		if err != nil || first.Status != FairSchedulerClaimed {
			t.Fatalf("first Workspace claim=%+v error=%v", first, err)
		}
		second, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  "scheduler-workspace-cap-owner-2",
				TTL:      time.Minute, Limits: limits,
			},
		)
		if err != nil || second.Status != FairSchedulerCapacityExhausted {
			t.Fatalf("Workspace-cap claim=%+v error=%v", second, err)
		}
		if err := fixture.store.ReleaseRunLease(
			context.Background(),
			first.Lease,
		); err != nil {
			t.Fatalf("release Workspace-cap claim: %v", err)
		}
	})

	t.Run("family", func(t *testing.T) {
		fixture := newCompositeRuntimeFixture(t)
		if _, err := fixture.store.CommitCompositeRunFamily(
			context.Background(),
			fixture.input,
		); err != nil {
			t.Fatalf("CommitCompositeRunFamily: %v", err)
		}
		limits := FairSchedulerLimits{
			GlobalWorkers: 3, MaxActivePerWorkspace: 3,
			MaxActivePerFamily: 1,
		}
		first, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.parentIntent.TenantID,
				OwnerID:  "scheduler-family-cap-owner-1",
				TTL:      time.Minute, Limits: limits,
			},
		)
		if err != nil || first.Status != FairSchedulerClaimed ||
			first.FamilyRootRunID != fixture.compiled.Parent.RunManifest.RunID ||
			first.Lease.RunID == fixture.compiled.Parent.RunManifest.RunID {
			t.Fatalf("first family claim=%+v error=%v", first, err)
		}
		second, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.parentIntent.TenantID,
				OwnerID:  "scheduler-family-cap-owner-2",
				TTL:      time.Minute, Limits: limits,
			},
		)
		if err != nil || second.Status != FairSchedulerCapacityExhausted {
			t.Fatalf("family-cap claim=%+v error=%v", second, err)
		}
		if err := fixture.store.ReleaseRunLease(
			context.Background(),
			first.Lease,
		); err != nil {
			t.Fatalf("release family-cap claim: %v", err)
		}
	})
}

func TestClaimFairRunExcludesCanceledAndModelPendingRuns(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		fixture, workspaceIDs := newFairSchedulerFixture(t)
		commitFairSchedulerRun(
			t,
			fixture,
			workspaceIDs[0],
			"scheduler-canceled-run",
			"scheduler-canceled-admission",
		)
		// commitFairSchedulerRun compiles a different Run ID from the shared
		// fixture input, so load the exact persisted manifest for the target.
		var canonical []byte
		if err := fixture.store.db.QueryRow(`
			SELECT canonical_json FROM run_manifests
			WHERE run_id='scheduler-canceled-run'
		`).Scan(&canonical); err != nil {
			t.Fatal(err)
		}
		manifest, err := corecontract.RestoreRunManifest(canonical)
		if err != nil {
			t.Fatal(err)
		}
		_, cancelCanonical := newOrdinaryCancellationRequest(
			t,
			manifest,
			corecontract.CancellationReasonUserRequestV1,
		)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: cancelCanonical},
		); err != nil {
			t.Fatalf("RequestRunCancellation: %v", err)
		}
		claim, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  "scheduler-canceled-owner",
				TTL:      time.Minute, Limits: fairSchedulerTestLimits,
			},
		)
		if err != nil || claim.Status != FairSchedulerNoRunnable {
			t.Fatalf("canceled claim=%+v error=%v", claim, err)
		}
	})

	t.Run("model pending", func(t *testing.T) {
		fixture, _, beginInput := newModelDispatchFixture(t)
		begin, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		)
		if err != nil {
			t.Fatalf("BeginModelDispatch: %v", err)
		}
		if err := fixture.store.ReleaseRunLease(
			context.Background(),
			begin.Lease,
		); err != nil {
			t.Fatalf("release MODEL_PENDING lease: %v", err)
		}
		claim, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.intent.TenantID,
				OwnerID:  "scheduler-model-pending-owner",
				TTL:      time.Minute, Limits: fairSchedulerTestLimits,
			},
		)
		if err != nil || claim.Status != FairSchedulerNoRunnable {
			t.Fatalf("MODEL_PENDING claim=%+v error=%v", claim, err)
		}
	})
}

func TestFairRunTargetViewRejectsPendingAttemptProjectionDrift(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch: %v", err)
	}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		begin.Lease,
	); err != nil {
		t.Fatalf("release MODEL_PENDING lease: %v", err)
	}
	execClosedFileTamperV1(t, fixture.store,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET state='MODEL_UNKNOWN', unknown_reason='tampered-projection'
		WHERE attempt_id=?
		`, begin.Attempt.AttemptID,
	)
	if _, err := fixture.store.GetFairRunTargetView(
		context.Background(),
		begin.Attempt.RunID,
	); !errors.Is(err, ErrFairSchedulerIntegrity) {
		t.Fatalf("tampered target view error=%v want Scheduler integrity", err)
	}
}

func TestClaimFairRunRollsBackLeaseWhenWorkspaceStateWriteFails(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	runID := "scheduler-rollback-run"
	commitFairSchedulerRun(
		t,
		fixture,
		workspaceIDs[0],
		runID,
		"scheduler-rollback-admission",
	)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER fail_scheduler_state_insert
		BEFORE INSERT ON workspace_scheduler_state
		BEGIN
			SELECT RAISE(ABORT, 'injected scheduler state failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	claim, err := fixture.store.ClaimFairRun(
		context.Background(),
		ClaimFairRunInput{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-rollback-owner",
			TTL:      time.Minute,
			Limits:   fairSchedulerTestLimits,
		},
	)
	if err == nil || claim != (FairRunClaimResult{}) {
		t.Fatalf("injected claim=%+v error=%v", claim, err)
	}
	if _, err := fixture.store.db.Exec(`DROP TRIGGER fail_scheduler_state_insert`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}

	lease, err := fixture.store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "rollback-verifier",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("rolled-back Scheduler lease remained held: %v", err)
	}
	if _, found, stateErr := fixture.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.intent.TenantID,
		workspaceIDs[0],
	); stateErr != nil || found {
		t.Fatalf("rolled-back Scheduler state found=%v error=%v", found, stateErr)
	}
	if err := fixture.store.ReleaseRunLease(context.Background(), lease); err != nil {
		t.Fatalf("release verifier lease: %v", err)
	}
}

func TestClaimFairRunConcurrentClaimsRespectStoreGlobalLimit(t *testing.T) {
	fixture, workspaceIDs := newFairSchedulerFixture(t)
	for index, workspaceID := range workspaceIDs {
		commitFairSchedulerRun(
			t,
			fixture,
			workspaceID,
			fmt.Sprintf("scheduler-concurrent-run-%d", index),
			fmt.Sprintf("scheduler-concurrent-admission-%d", index),
		)
	}
	limits := FairSchedulerLimits{
		GlobalWorkers:         2,
		MaxActivePerWorkspace: 1,
		MaxActivePerFamily:    1,
	}
	const callers = 12
	results := make(chan FairRunClaimResult, callers)
	errorsSeen := make(chan error, callers)
	var wait sync.WaitGroup
	for index := 0; index < callers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			claim, err := fixture.store.ClaimFairRun(
				context.Background(),
				ClaimFairRunInput{
					TenantID: fixture.intent.TenantID,
					OwnerID:  fmt.Sprintf("scheduler-concurrent-owner-%d", index),
					TTL:      time.Minute,
					Limits:   limits,
				},
			)
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- claim
		}(index)
	}
	wait.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("concurrent ClaimFairRun: %v", err)
	}

	var claimed []FairRunClaimResult
	capacity := 0
	for result := range results {
		switch result.Status {
		case FairSchedulerClaimed:
			claimed = append(claimed, result)
		case FairSchedulerCapacityExhausted:
			capacity++
		default:
			t.Errorf("unexpected concurrent claim status %q", result.Status)
		}
	}
	if len(claimed) != 2 || capacity != callers-2 {
		t.Fatalf("claimed=%d capacity=%d want 2/%d", len(claimed), capacity, callers-2)
	}
	for _, result := range claimed {
		if err := fixture.store.ReleaseRunLease(context.Background(), result.Lease); err != nil {
			t.Errorf("release concurrent claim: %v", err)
		}
	}
}

func TestClaimFairRunRejectsInvalidLimitsWithoutWrites(t *testing.T) {
	fixture, _ := newFairSchedulerFixture(t)
	for _, input := range []ClaimFairRunInput{
		{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-invalid-zero",
			TTL:      time.Minute,
			Limits:   FairSchedulerLimits{},
		},
		{
			TenantID: fixture.intent.TenantID,
			OwnerID:  "scheduler-invalid-scope",
			TTL:      time.Minute,
			Limits: FairSchedulerLimits{
				GlobalWorkers:         1,
				MaxActivePerWorkspace: 2,
				MaxActivePerFamily:    1,
			},
		},
	} {
		if _, err := fixture.store.ClaimFairRun(
			context.Background(),
			input,
		); !errors.Is(err, ErrInvalidFairScheduler) {
			t.Fatalf("ClaimFairRun(%+v) error=%v", input.Limits, err)
		}
	}
	var count int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM workspace_scheduler_state`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid claims Scheduler rows=%d error=%v", count, err)
	}
}

func newFairSchedulerFixture(
	t *testing.T,
) (*admissionCommitFixture, []string) {
	t.Helper()
	fixture := newAdmissionCommitFixture(t)
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	workspaceIDs := []string{
		control.Workspaces[0].Workspace.ID,
		"workspace-one",
		"workspace-two",
	}
	for index, workspaceID := range workspaceIDs[1:] {
		control.Workspaces = append(
			control.Workspaces,
			controlcontract.WorkspaceDefinition{
				Workspace: corecontract.WorkspaceRef{
					ID:      workspaceID,
					Version: "v1",
					Digest:  strings.Repeat(fmt.Sprintf("%x", index+4), 64),
				},
				BudgetPolicy: control.Workspaces[0].BudgetPolicy,
			},
		)
	}
	control.SnapshotID = "control-fair-scheduler"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-fair-scheduler"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
	return fixture, workspaceIDs
}

func commitFairSchedulerRun(
	t *testing.T,
	fixture *admissionCommitFixture,
	workspaceID string,
	runID string,
	admissionKey string,
) RunAdmissionResult {
	t.Helper()
	intent := fixture.intent
	intent.AdmissionKey = admissionKey
	intent.WorkspaceID = workspaceID
	intent.Deadline = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.compileInput(t, intentCanonical, intentDigest, runID)
	result, err := fixture.store.CommitRunAdmission(context.Background(), input)
	if err != nil {
		t.Fatalf("CommitRunAdmission(%s): %v", runID, err)
	}
	return result
}
