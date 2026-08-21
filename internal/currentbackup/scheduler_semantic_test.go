package currentbackup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
)

type schedulerStateSnapshot struct {
	TenantID    string
	WorkspaceID string
	ServedUnits int64
	Revision    int64
	UpdatedAt   int64
}

func TestSchedulerStateBackupRoundTripPreservesExactCounters(t *testing.T) {
	fixture := newBackupFixture(t)
	database, err := sql.Open("sqlite", fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var tenantID string
	var memberCanonical []byte
	if err := database.QueryRow(`
		SELECT r.tenant_id, member.canonical_json
		FROM runs AS r
		JOIN member_execution_snapshots AS member ON member.run_id=r.run_id
		WHERE r.run_id=?
	`, fixture.pendingRunID).Scan(&tenantID, &memberCanonical); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewChatService(store, yieldLoop{})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	admitted, err := service.Chat(context.Background(), localchat.ChatInput{
		TenantID: tenantID, PrincipalID: "scheduler-backup-principal",
		WorkspaceID: member.Workspace.ID, AgentID: member.Agent.ID,
		ProfileID: member.Profile.ID, Message: "scheduler backup runnable",
		RequestID: "scheduler-backup-request",
		Deadline:  time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("admit Scheduler backup Run: %v", err)
	}
	claim, err := store.ClaimFairRun(
		context.Background(),
		currentstore.ClaimFairRunInput{
			TenantID: tenantID, OwnerID: "scheduler-backup-owner",
			TTL: time.Minute,
			Limits: currentstore.FairSchedulerLimits{
				GlobalWorkers: 4, MaxActivePerWorkspace: 2,
				MaxActivePerFamily: 2,
			},
		},
	)
	if err != nil || claim.Status != currentstore.FairSchedulerClaimed ||
		claim.Lease.RunID != admitted.RunID {
		_ = store.Close()
		t.Fatalf("ClaimFairRun=%+v error=%v", claim, err)
	}
	if err := store.ReleaseRunLease(context.Background(), claim.Lease); err != nil {
		_ = store.Close()
		t.Fatalf("release Scheduler backup claim: %v", err)
	}
	persisted, found, err := store.GetWorkspaceSchedulerState(
		context.Background(),
		tenantID,
		member.Workspace.ID,
	)
	if err != nil || !found {
		_ = store.Close()
		t.Fatalf("read Scheduler backup state found=%v error=%v", found, err)
	}
	state := schedulerStateSnapshot{
		TenantID: tenantID, WorkspaceID: member.Workspace.ID,
		ServedUnits: int64(persisted.ServedUnits),
		Revision:    int64(persisted.Revision),
		UpdatedAt:   persisted.UpdatedAt.UnixMicro(),
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.databasePath); err != nil {
		t.Fatalf("verify Scheduler source: %v", err)
	}
	bundle := filepath.Join(t.TempDir(), "scheduler.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"scheduler-backup-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle(Scheduler): %v", err)
	}
	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(Scheduler): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Scheduler state: %v", err)
	}
	if restored := loadSchedulerStateSnapshot(t, restoredDatabase); !reflect.DeepEqual(restored, []schedulerStateSnapshot{state}) {
		t.Fatalf("restored Scheduler state=%+v want %+v", restored, state)
	}
}

func TestSchedulerSemanticClosureRejectsWorkspaceWithoutRun(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(
		context.Background(),
		databasePath,
	); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec(`
		INSERT INTO workspace_scheduler_state(
			tenant_id, workspace_id, served_units, revision, updated_at
		) VALUES('orphan-tenant', 'orphan-workspace', 1, 1, ?)
	`, time.Now().UTC().UnixMicro())
	if err := errors.Join(insertErr, database.Close()); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(
		context.Background(),
		databasePath,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("orphan Scheduler state error=%v want ErrIntegrity", err)
	}
}

func TestSchedulerSemanticClosureRejectsCounterRevisionDrift(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE runs(tenant_id TEXT NOT NULL, workspace_id TEXT NOT NULL);
		CREATE TABLE workspace_scheduler_state(
			tenant_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			served_units INTEGER NOT NULL,
			revision INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO runs VALUES('tenant-a', 'workspace-a');
		INSERT INTO workspace_scheduler_state
		VALUES('tenant-a', 'workspace-a', 2, 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := inspectSchedulerSemanticClosure(
		context.Background(),
		database,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("counter drift error=%v want ErrIntegrity", err)
	}
}

func loadSchedulerStateSnapshot(
	t *testing.T,
	databasePath string,
) []schedulerStateSnapshot {
	t.Helper()
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT tenant_id, workspace_id, served_units, revision, updated_at
		FROM workspace_scheduler_state
		ORDER BY tenant_id COLLATE BINARY, workspace_id COLLATE BINARY
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	states := make([]schedulerStateSnapshot, 0)
	for rows.Next() {
		var state schedulerStateSnapshot
		if err := rows.Scan(
			&state.TenantID,
			&state.WorkspaceID,
			&state.ServedUnits,
			&state.Revision,
			&state.UpdatedAt,
		); err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return states
}
