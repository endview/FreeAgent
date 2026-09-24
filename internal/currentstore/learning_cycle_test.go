package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningCycleScheduleDefaultsDisabledAndUsesStrictEnablementCAS(
	t *testing.T,
) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindKnowledgeV1,
		firstDue,
	)
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Record.Enabled || created.Record.Revision != 0 ||
		created.Record.Schedule.IntervalSeconds !=
			learningcontract.DefaultLearningCycleIntervalSecondsV1 ||
		!created.Record.NextDueAt.Equal(firstDue) ||
		!created.Record.LastScheduledFor.IsZero() {
		t.Fatalf("created Schedule = %+v", created)
	}
	retried, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil || retried.Created || retried.Record.ScheduleDigest != created.Record.ScheduleDigest {
		t.Fatalf("exact create retry = %+v, %v", retried, err)
	}

	enabled, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: 0, Enabled: true,
		},
	)
	if err != nil || !enabled.Applied || !enabled.Record.Enabled ||
		enabled.Record.Revision != 1 {
		t.Fatalf("enable = %+v, %v", enabled, err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: 0, Enabled: true,
		},
	); !errors.Is(err, ErrLearningCycleConflict) {
		t.Fatalf("stale same-state enable error = %v", err)
	}
	unchanged, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: 1, Enabled: true,
		},
	)
	if err != nil || unchanged.Applied || unchanged.Record.Revision != 1 ||
		!unchanged.Record.UpdatedAt.Equal(enabled.Record.UpdatedAt) {
		t.Fatalf("current same-state enable = %+v, %v", unchanged, err)
	}
	disabled, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: 1, Enabled: false,
		},
	)
	if err != nil || !disabled.Applied || disabled.Record.Enabled ||
		disabled.Record.Revision != 2 {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
}

func TestEnsureDueTaskBoundaryCoalescingRollbackAndDisableDoNotCancel(
	t *testing.T,
) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindSkillV1,
		firstDue,
	)
	schedule.IntervalSeconds = 3_600
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: created.Record.Revision, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	before, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		firstDue.Add(-time.Microsecond),
	)
	if err != nil || before.Due || before.Created || before.Task.TaskID != "" ||
		before.Schedule.Revision != 1 {
		t.Fatalf("before due = %+v, %v", before, err)
	}

	observed := firstDue.Add(5*time.Hour + 37*time.Minute)
	due, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		observed,
	)
	wantWindow := firstDue.Add(5 * time.Hour)
	if err != nil || !due.Due || !due.Created ||
		due.Task.State != learningcontract.LearningCycleTaskPendingV1 ||
		due.Task.Revision != 0 || !due.Task.ScheduledFor.Equal(wantWindow) ||
		due.Task.Request.WindowEndMicros != wantWindow.Add(time.Hour).UnixMicro() ||
		!due.Schedule.LastScheduledFor.Equal(wantWindow) ||
		!due.Schedule.NextDueAt.Equal(wantWindow.Add(time.Hour)) {
		t.Fatalf("coalesced due = %+v, %v", due, err)
	}
	expectedTaskID, err := learningcontract.DeriveLearningCycleTaskIDV1(
		due.Schedule.ScheduleDigest,
		wantWindow.UnixMicro(),
		due.Task.RequestDigest,
	)
	if err != nil || due.Task.TaskID != expectedTaskID ||
		due.Task.Request.RequestDigest != due.Task.RequestDigest {
		t.Fatalf("Task identity = %s/%s, want %s (%v)",
			due.Task.TaskID, due.Task.RequestDigest, expectedTaskID, err)
	}

	rollback, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		firstDue.Add(-time.Hour),
	)
	if err != nil || !rollback.Due || rollback.Created ||
		rollback.Task.TaskID != due.Task.TaskID ||
		!rollback.Schedule.LastScheduledFor.Equal(due.Schedule.LastScheduledFor) ||
		!rollback.Schedule.NextDueAt.Equal(due.Schedule.NextDueAt) {
		t.Fatalf("clock rollback = %+v, %v", rollback, err)
	}

	disabled, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: due.Schedule.Revision, Enabled: false,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stillOpen, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		observed.Add(24*time.Hour),
	)
	if err != nil || disabled.Record.Enabled || !stillOpen.Due ||
		stillOpen.Created || stillOpen.Task.TaskID != due.Task.TaskID ||
		stillOpen.Task.State != learningcontract.LearningCycleTaskPendingV1 {
		t.Fatalf("disabled open task = %+v/%+v, %v", disabled, stillOpen, err)
	}
}

func TestEnsureDueTaskConcurrentCallsCreateOneOpenTask(t *testing.T) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindSkillV1,
		firstDue,
	)
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: created.Record.Revision, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	const callers = 8
	results := make(chan EnsureLearningCycleTaskResult, callers)
	errorsChannel := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := store.EnsureDueTask(
				context.Background(),
				schedule.TenantID,
				schedule.ScheduleID,
				firstDue,
			)
			results <- result
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	createdCount := 0
	taskID := ""
	for result := range results {
		if !result.Due || result.Task.TaskID == "" {
			t.Fatalf("concurrent result = %+v", result)
		}
		if result.Created {
			createdCount++
		}
		if taskID == "" {
			taskID = result.Task.TaskID
		} else if result.Task.TaskID != taskID {
			t.Fatalf("concurrent TaskIDs = %s and %s", taskID, result.Task.TaskID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("Created count = %d, want 1", createdCount)
	}
	var openCount int
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM learning_cycle_tasks
		WHERE tenant_id=? AND schedule_id=? AND state IN ('PENDING','RUN_ADMITTED')
	`, schedule.TenantID, schedule.ScheduleID).Scan(&openCount); err != nil || openCount != 1 {
		t.Fatalf("open task count = %d, %v", openCount, err)
	}
}

func TestLearningCycleReadsDetachCanonicalAndVisibility(t *testing.T) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindKnowledgeV1,
		firstDue,
	)
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	schedule.KnowledgeVisibility[0].WorkspaceID = "mutated-input"
	created.Record.ScheduleCanonical[0] ^= 0xff
	created.Record.Schedule.KnowledgeVisibility[0].WorkspaceID = "mutated-return"
	stored, err := store.GetLearningCycleSchedule(
		context.Background(),
		created.Record.Schedule.TenantID,
		created.Record.Schedule.ScheduleID,
	)
	if err != nil || stored.ScheduleCanonical[0] == created.Record.ScheduleCanonical[0] ||
		stored.Schedule.KnowledgeVisibility[0].WorkspaceID != "*" {
		t.Fatalf("detached Schedule = %+v, %v", stored, err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: stored.Schedule.TenantID, ScheduleID: stored.Schedule.ScheduleID,
			ExpectedRevision: stored.Revision, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	due, err := store.EnsureDueTask(
		context.Background(),
		stored.Schedule.TenantID,
		stored.Schedule.ScheduleID,
		firstDue,
	)
	if err != nil {
		t.Fatal(err)
	}
	due.Task.RequestCanonical[0] ^= 0xff
	readTask, err := store.GetLearningCycleTask(
		context.Background(),
		due.Task.TenantID,
		due.Task.TaskID,
	)
	if err != nil || readTask.RequestCanonical[0] == due.Task.RequestCanonical[0] {
		t.Fatalf("detached Task = %+v, %v", readTask, err)
	}
	listed, err := store.ListLearningCycleTasks(
		context.Background(),
		stored.Schedule.TenantID,
		stored.Schedule.ScheduleID,
	)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListLearningCycleTasks = %+v, %v", listed, err)
	}
	listed[0].RequestCanonical[0] ^= 0xff
	again, err := store.GetLearningCycleTask(context.Background(), readTask.TenantID, readTask.TaskID)
	if err != nil || again.RequestCanonical[0] != readTask.RequestCanonical[0] {
		t.Fatalf("list alias changed stored Task = %+v, %v", again, err)
	}
}

func TestLearningCycleReturnedSkillSchedulePreservesCanonicalEmptyVisibility(
	t *testing.T,
) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindSkillV1,
		firstDue,
	)
	assertRoundTrip := func(label string, record LearningCycleScheduleRecord) {
		t.Helper()
		if record.Schedule.KnowledgeVisibility == nil {
			t.Fatalf("%s returned nil KnowledgeVisibility for canonical []", label)
		}
		if _, _, _, err := learningcontract.NewLearningCycleRequestV1(
			record.Schedule,
			record.ScheduleCanonical,
			record.ScheduleDigest,
			firstDue.UnixMicro(),
		); err != nil {
			t.Fatalf("%s returned Schedule does not round-trip canonically: %v", label, err)
		}
	}
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip("create", created.Record)
	retried, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil || retried.Created {
		t.Fatalf("exact create retry = %+v, %v", retried, err)
	}
	assertRoundTrip("exact create retry", retried.Record)
	enabled, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: created.Record.Revision, Enabled: true,
		},
	)
	if err != nil || !enabled.Applied {
		t.Fatalf("enable = %+v, %v", enabled, err)
	}
	assertRoundTrip("enable", enabled.Record)
	read, err := store.GetLearningCycleSchedule(
		context.Background(), schedule.TenantID, schedule.ScheduleID,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip("read", read)
}

func TestLearningCycleSchemaRejectsOpenDuplicateTimeOverflowAndBadOccupiedRefs(
	t *testing.T,
) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindSkillV1,
		firstDue,
	)
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: created.Record.Revision, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	due, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		firstDue,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO learning_cycle_tasks(
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			created_at, updated_at
		)
		SELECT ?, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for+1, request_digest, request_canonical,
			request_size_bytes, 'PENDING', 0, created_at, created_at
		FROM learning_cycle_tasks WHERE task_id=?
	`, strings.Repeat("f", 64), due.Task.TaskID); err == nil {
		t.Fatal("one-open-task partial unique index accepted a second PENDING task")
	}
	if _, err := store.db.Exec(`
		UPDATE learning_cycle_schedules SET next_due_at=9007199254740992
		WHERE tenant_id=? AND schedule_id=?
	`, schedule.TenantID, schedule.ScheduleID); err == nil {
		t.Fatal("Schedule CHECK accepted next_due_at above 2^53-1")
	}

	db := createSchemaTestDatabase(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	if _, err := db.Exec(`
		INSERT INTO learning_cycle_schedules(
			tenant_id, workspace_id, schedule_id, schedule_digest,
			schedule_canonical, schedule_size_bytes,
			enabled, revision, next_due_at, created_at, updated_at
		) VALUES('tenant-raw','workspace-raw','schedule-raw',?,X'01',1,0,0,1,1,1)
	`, digest); err != nil {
		t.Fatal(err)
	}
	insertTask := func(state string, resultRef any, proposalID any) error {
		_, err := db.Exec(`
			INSERT INTO learning_cycle_tasks(
				task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
				scheduled_for, request_digest, request_canonical,
				request_size_bytes, state, revision,
				run_id, run_manifest_digest, attempt_id,
				result_ref, proposal_id, created_at, updated_at
			) VALUES(?, 'tenant-raw','workspace-raw','schedule-raw',?,1,?,X'01',1,?,2,
				'run-raw',?,'attempt-raw',?,?,1,2)
		`, moduleapi.Digest("cycle-test-task", []byte(state)), digest,
			digest, state, digest, resultRef, proposalID)
		return err
	}
	for _, state := range []string{"SOURCE_OCCUPIED", "CONTENT_OCCUPIED", "TARGET_OCCUPIED"} {
		if err := insertTask(state, digest, nil); err == nil {
			t.Fatalf("%s accepted without ProposalID", state)
		}
	}
	if err := insertTask("SOURCE_OCCUPIED", digest, digest); err != nil {
		t.Fatalf("SOURCE_OCCUPIED with ProposalID was rejected: %v", err)
	}
	if err := insertTask("NO_CHANGE", digest, digest); err == nil {
		t.Fatal("NO_CHANGE accepted a ProposalID")
	}
}

func TestLearningCycleScheduleScannerRejectsOffGridWatermarkTamper(t *testing.T) {
	store := openContentTestStore(t)
	firstDue := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	schedule := learningCycleTestSchedule(
		learningcontract.ProposalKindSkillV1,
		firstDue,
	)
	schedule.IntervalSeconds = 60
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: schedule.TenantID, ScheduleID: schedule.ScheduleID,
			ExpectedRevision: created.Record.Revision, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureDueTask(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
		firstDue,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE learning_cycle_schedules
		SET last_scheduled_for=last_scheduled_for+1,
			next_due_at=next_due_at+1
		WHERE tenant_id=? AND schedule_id=?
	`, schedule.TenantID, schedule.ScheduleID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLearningCycleSchedule(
		context.Background(),
		schedule.TenantID,
		schedule.ScheduleID,
	); !errors.Is(err, ErrLearningCycleIntegrity) {
		t.Fatalf("off-grid watermark read error = %v", err)
	}
}

func learningCycleTestSchedule(
	kind learningcontract.ProposalKindV1,
	firstDue time.Time,
) learningcontract.LearningCycleScheduleV1 {
	visibility := []moduleapi.KnowledgeScopeRuleV1{}
	if kind == learningcontract.ProposalKindKnowledgeV1 {
		visibility = []moduleapi.KnowledgeScopeRuleV1{{
			TenantID: "tenant-learning-cycle", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
		}}
	}
	return learningcontract.LearningCycleScheduleV1{
		SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
		TenantID:             "tenant-learning-cycle",
		ScheduleID:           "schedule-learning-cycle",
		ServicePrincipalID:   "principal-learning-cycle",
		WorkspaceID:          "workspace-learning-cycle",
		AgentID:              "agent-learning-cycle",
		ProfileID:            "profile-learning-cycle",
		Kind:                 kind,
		TargetModuleID:       "freeagent.learning-cycle.target",
		Objective:            "Refresh the bounded local learning draft.",
		KnowledgeVisibility:  visibility,
		FirstDueAtUnixMicros: firstDue.UnixMicro(),
		MaxOutputTokens:      512,
	}
}

func TestCommitLearningCycleRunAdmissionIsExactModelOnlyAndAtomic(t *testing.T) {
	t.Run("exact identity deadline and retry", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":8192}`),
			8_192,
			nil,
		)
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(),
			fixture.admission,
		); !errors.Is(err, ErrInvalidAdmission) {
			t.Fatalf("generic reserved-namespace admission error = %v", err)
		}
		fixture.assertNoAdmittedRun(t)
		wrong, _, _ := fixture.compileAdmission(
			t,
			time.UnixMicro(fixture.task.Request.WindowEndMicros+1).UTC(),
		)
		if _, err := fixture.store.CommitLearningCycleRunAdmission(
			context.Background(),
			CommitLearningCycleRunAdmissionInput{
				TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
				ExpectedTaskRevision: 0, Run: wrong,
			},
		); !errors.Is(err, ErrInvalidLearningCycle) {
			t.Fatalf("wrong-deadline admission error = %v", err)
		}
		fixture.assertNoAdmittedRun(t)

		admitted := fixture.admit(t)
		if !admitted.Created || admitted.Task.State !=
			learningcontract.LearningCycleTaskRunAdmittedV1 ||
			admitted.Task.Revision != 1 ||
			admitted.Task.RunID != fixture.identity.RunID ||
			admitted.Run.RunID != fixture.identity.RunID {
			t.Fatalf("admitted = %+v", admitted)
		}
		retried, err := fixture.store.CommitLearningCycleRunAdmission(
			context.Background(),
			CommitLearningCycleRunAdmissionInput{
				TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
				ExpectedTaskRevision: 0, Run: fixture.admission,
			},
		)
		if err != nil || retried.Created || retried.Task.RunID != fixture.identity.RunID {
			t.Fatalf("admission retry = %+v, %v", retried, err)
		}
	})

	t.Run("missing max_tokens", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"temperature":0}`),
			512,
			nil,
		)
		if _, err := fixture.store.CommitLearningCycleRunAdmission(
			context.Background(),
			CommitLearningCycleRunAdmissionInput{
				TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
				ExpectedTaskRevision: 0, Run: fixture.admission,
			},
		); !errors.Is(err, ErrInvalidLearningCycle) {
			t.Fatalf("missing max_tokens error = %v", err)
		}
		fixture.assertNoAdmittedRun(t)
	})

	t.Run("task projection failure rolls back ordinary Run", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		if _, err := fixture.store.db.Exec(`
			CREATE TRIGGER learning_cycle_test_abort_task_admission
			BEFORE UPDATE OF state ON learning_cycle_tasks
			WHEN NEW.task_id='` + fixture.task.TaskID + `'
			BEGIN SELECT RAISE(ABORT, 'forced cycle task failure'); END
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.CommitLearningCycleRunAdmission(
			context.Background(),
			CommitLearningCycleRunAdmissionInput{
				TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
				ExpectedTaskRevision: 0, Run: fixture.admission,
			},
		); err == nil {
			t.Fatal("forced task projection failure unexpectedly committed")
		}
		fixture.assertNoAdmittedRun(t)
		stored, err := fixture.store.GetLearningCycleTask(
			context.Background(), fixture.task.TenantID, fixture.task.TaskID,
		)
		if err != nil || stored.State != learningcontract.LearningCycleTaskPendingV1 {
			t.Fatalf("rolled-back task = %+v, %v", stored, err)
		}
	})
}

func TestFinalizeLearningCycleTaskDerivesTerminalStatesAndDrafts(t *testing.T) {
	tests := []struct {
		name       string
		kind       learningcontract.ProposalKindV1
		state      corecontract.ModelAttemptState
		assistant  func(*testing.T, LearningCycleTaskRecord) string
		want       learningcontract.LearningCycleTaskStateV1
		wantDraft  bool
		checkDraft func(*testing.T, LearningProposalRecord, LearningCycleScheduleRecord)
	}{
		{
			name: "failed", kind: learningcontract.ProposalKindSkillV1,
			state: corecontract.ModelAttemptFailed,
			want:  learningcontract.LearningCycleTaskFailedV1,
		},
		{
			name: "invalid result", kind: learningcontract.ProposalKindSkillV1,
			state: corecontract.ModelAttemptSucceeded,
			assistant: func(_ *testing.T, _ LearningCycleTaskRecord) string {
				return `not-learning-cycle-json`
			},
			want: learningcontract.LearningCycleTaskInvalidResultV1,
		},
		{
			name: "no change", kind: learningcontract.ProposalKindSkillV1,
			state: corecontract.ModelAttemptSucceeded,
			assistant: func(t *testing.T, task LearningCycleTaskRecord) string {
				return learningCycleResultCanonical(
					t, task, learningcontract.LearningCycleResultNoChangeV1, nil, "",
				)
			},
			want: learningcontract.LearningCycleTaskNoChangeV1,
		},
		{
			name: "knowledge proposal", kind: learningcontract.ProposalKindKnowledgeV1,
			state: corecontract.ModelAttemptSucceeded,
			assistant: func(t *testing.T, task LearningCycleTaskRecord) string {
				return learningCycleResultCanonical(
					t, task, learningcontract.LearningCycleResultProposeV1,
					[]string{"First bounded cycle fact.", "Second bounded cycle fact."}, "",
				)
			},
			want:      learningcontract.LearningCycleTaskProposalSubmittedV1,
			wantDraft: true,
			checkDraft: func(
				t *testing.T,
				proposal LearningProposalRecord,
				schedule LearningCycleScheduleRecord,
			) {
				source, ref, err := moduleapi.RestoreKnowledgeSourceV1(proposal.DraftCanonical)
				if err != nil || ref.ID != proposal.Proposal.Target.ID ||
					ref.Version != proposal.Proposal.Target.Version || len(source.Chunks) != 2 {
					t.Fatalf("Knowledge draft = %+v/%+v, %v", source, ref, err)
				}
				for _, chunk := range source.Chunks {
					if !equalLearningCycleScopeRules(
						chunk.VisibleTo,
						schedule.Schedule.KnowledgeVisibility,
					) {
						t.Fatalf("Knowledge visibility = %+v, want %+v",
							chunk.VisibleTo, schedule.Schedule.KnowledgeVisibility)
					}
				}
			},
		},
		{
			name: "static Skill proposal", kind: learningcontract.ProposalKindSkillV1,
			state: corecontract.ModelAttemptSucceeded,
			assistant: func(t *testing.T, task LearningCycleTaskRecord) string {
				return learningCycleResultCanonical(
					t, task, learningcontract.LearningCycleResultProposeV1,
					nil, "Use the exact bounded static Skill text.",
				)
			},
			want:      learningcontract.LearningCycleTaskProposalSubmittedV1,
			wantDraft: true,
			checkDraft: func(
				t *testing.T,
				proposal LearningProposalRecord,
				_ LearningCycleScheduleRecord,
			) {
				skill, err := corecontract.RestoreStaticContextV1(proposal.DraftCanonical)
				if err != nil || skill.Text != "Use the exact bounded static Skill text." {
					t.Fatalf("Skill draft = %+v, %v", skill, err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLearningCycleExecutionFixture(
				t,
				test.kind,
				json.RawMessage(`{"max_tokens":512}`),
				512,
				nil,
			)
			fixture.admit(t)
			if _, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			); !errors.Is(err, ErrLearningCycleNotReady) {
				t.Fatalf("finalize before Attempt error = %v", err)
			}
			begin := fixture.review.beginAttempt(t)
			var assistant func(learningReviewStoreFixture) string
			if test.assistant != nil {
				assistant = func(_ learningReviewStoreFixture) string {
					return test.assistant(t, fixture.task)
				}
			}
			fixture.review.commitAttemptOutcome(
				t, begin, test.state, assistant, false, nil,
			)
			finalized, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			)
			if err != nil || !finalized.Applied || finalized.Task.State != test.want ||
				finalized.Task.Revision != 2 || finalized.Task.AttemptID != begin.Attempt.AttemptID {
				t.Fatalf("finalized = %+v, %v", finalized, err)
			}
			if test.state == corecontract.ModelAttemptSucceeded && finalized.Task.ResultRef == "" {
				t.Fatal("successful terminal task omitted ResultRef")
			}
			if test.wantDraft != (finalized.Task.ProposalID != "") {
				t.Fatalf("ProposalID = %q, wantDraft %v", finalized.Task.ProposalID, test.wantDraft)
			}
			if test.wantDraft {
				proposal, err := fixture.store.GetLearningProposal(
					context.Background(), fixture.task.TenantID, finalized.Task.ProposalID,
				)
				if err != nil || proposal.ProposerAttemptID != begin.Attempt.AttemptID ||
					proposal.Proposal.ProposerRunID != fixture.identity.RunID ||
					proposal.Proposal.ProposerResultRef != finalized.Task.ResultRef ||
					proposal.Proposal.Target != fixture.task.Request.Target {
					t.Fatalf("cycle Proposal = %+v, %v", proposal, err)
				}
				test.checkDraft(t, proposal, fixture.schedule)
			}
			retry, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			)
			if err != nil || retry.Applied || retry.Task.State != test.want {
				t.Fatalf("finalize retry = %+v, %v", retry, err)
			}
		})
	}
}

func TestFinalizeLearningCycleTaskDerivesOccupiedStates(t *testing.T) {
	visibility := []moduleapi.KnowledgeScopeRuleV1{{
		TenantID: "tenant-publish", WorkspaceID: "workspace-default",
		AgentID: "*", TaskInputRef: "*",
	}}
	tests := []struct {
		name          string
		want          learningcontract.LearningCycleTaskStateV1
		candidateText string
	}{
		{
			name: "source", want: learningcontract.LearningCycleTaskSourceOccupiedV1,
			candidateText: "Cycle source-axis candidate.",
		},
		{
			name:          "content from rejected Proposal",
			want:          learningcontract.LearningCycleTaskContentOccupiedV1,
			candidateText: "Review this bounded candidate.",
		},
		{
			name: "target", want: learningcontract.LearningCycleTaskTargetOccupiedV1,
			candidateText: "Cycle target-axis candidate.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLearningCycleExecutionFixture(
				t,
				learningcontract.ProposalKindKnowledgeV1,
				json.RawMessage(`{"max_tokens":512}`),
				512,
				visibility,
			)
			fixture.admit(t)
			begin := fixture.review.beginAttempt(t)
			cycleResult, cycleCanonical, _, err :=
				learningcontract.NewLearningCycleResultV1(
					learningcontract.LearningCycleResultV1{
						SchemaVersion: learningcontract.LearningCycleResultSchemaVersionV1,
						RequestDigest: fixture.task.RequestDigest,
						Decision:      learningcontract.LearningCycleResultProposeV1,
						KnowledgeChunks: []string{
							test.candidateText,
						},
					},
				)
			if err != nil {
				t.Fatal(err)
			}
			outcome := fixture.review.commitAttemptOutcome(
				t,
				begin,
				corecontract.ModelAttemptSucceeded,
				func(learningReviewStoreFixture) string { return string(cycleCanonical) },
				false,
				nil,
			)
			resultRef := outcome.Record.Attempt.ResultRef
			candidateDraft, err := buildLearningCycleDraft(
				fixture.schedule,
				fixture.task,
				resultRef,
				cycleResult,
			)
			if err != nil {
				t.Fatal(err)
			}
			candidate, _, candidateID, err := learningcontract.NewProposalV1(
				learningCycleProposalValue(fixture, resultRef, fixture.task.Request.Target),
				candidateDraft,
				learningcontract.SourceEvidenceV1{
					Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			var conflict LearningProposalRecord
			switch test.want {
			case learningcontract.LearningCycleTaskSourceOccupiedV1:
				conflict = submitLearningCycleKnowledgeConflict(
					t,
					fixture,
					resultRef,
					moduleapi.Ref{
						ID: "freeagent.learning-cycle.source-conflict", Version: "v1",
					},
					"Different source-axis content.",
					learningcontract.SourceEvidenceV1{
						Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
					},
				)
				if conflict.Proposal.SourceFingerprint != candidate.SourceFingerprint ||
					conflict.Proposal.ContentFingerprint == candidate.ContentFingerprint ||
					conflict.Proposal.Target == candidate.Target {
					t.Fatalf("source conflict does not isolate source axis: %+v / %+v",
						conflict.Proposal, candidate)
				}
			case learningcontract.LearningCycleTaskContentOccupiedV1:
				seed := fixture.seedReview
				seed.commitAdmission(t)
				seedBegin := seed.beginAttempt(t)
				seed.commitAttemptOutcome(
					t,
					seedBegin,
					corecontract.ModelAttemptSucceeded,
					func(f learningReviewStoreFixture) string {
						return f.verdict(
							t,
							learningcontract.ReviewDecisionRejectV1,
							[]learningcontract.ReviewIssueCodeV1{
								learningcontract.ReviewIssueMissingEvidenceV1,
							},
						)
					},
					false,
					nil,
				)
				rejected, err := fixture.store.FinalizeLearningReview(
					context.Background(),
					FinalizeLearningReviewInput{
						TenantID:                 seed.proposal.Proposal.TenantID,
						ProposalID:               seed.proposal.ProposalID,
						ExpectedProposalRevision: 1,
					},
				)
				if err != nil || !rejected.Applied ||
					rejected.Proposal.State != LearningProposalRejected {
					t.Fatalf("reject occupied Proposal = %+v, %v", rejected, err)
				}
				conflict = rejected.Proposal
				if conflict.Proposal.ContentFingerprint != candidate.ContentFingerprint ||
					conflict.Proposal.SourceFingerprint == candidate.SourceFingerprint ||
					conflict.Proposal.Target == candidate.Target {
					t.Fatalf("content conflict does not isolate content axis: %+v / %+v",
						conflict.Proposal, candidate)
				}
			case learningcontract.LearningCycleTaskTargetOccupiedV1:
				conflict = submitLearningCycleKnowledgeConflict(
					t,
					fixture,
					resultRef,
					fixture.task.Request.Target,
					"Different target-axis content.",
					localLearningEvidence("cycle-target-origin", "cycle-target-revision"),
				)
				if conflict.Proposal.Target != candidate.Target ||
					conflict.Proposal.SourceFingerprint == candidate.SourceFingerprint ||
					conflict.Proposal.ContentFingerprint == candidate.ContentFingerprint {
					t.Fatalf("target conflict does not isolate target axis: %+v / %+v",
						conflict.Proposal, candidate)
				}
			default:
				t.Fatalf("unsupported occupied state %q", test.want)
			}
			if conflict.ProposalID == candidateID {
				t.Fatal("axis conflict unexpectedly has the exact candidate ProposalID")
			}
			var countBefore int
			if err := fixture.store.db.QueryRow(
				`SELECT COUNT(*) FROM learning_proposals`,
			).Scan(&countBefore); err != nil {
				t.Fatal(err)
			}

			finalized, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			)
			if err != nil || !finalized.Applied || finalized.Task.State != test.want ||
				finalized.Task.Revision != 2 ||
				finalized.Task.AttemptID != begin.Attempt.AttemptID ||
				finalized.Task.ResultRef != resultRef ||
				finalized.Task.ProposalID != conflict.ProposalID {
				t.Fatalf("occupied finalization = %+v, %v", finalized, err)
			}
			var countAfter int
			if err := fixture.store.db.QueryRow(
				`SELECT COUNT(*) FROM learning_proposals`,
			).Scan(&countAfter); err != nil {
				t.Fatal(err)
			}
			if countAfter != countBefore {
				t.Fatalf("occupied finalization created Proposal: before=%d after=%d",
					countBefore, countAfter)
			}
			if _, err := fixture.store.GetLearningProposal(
				context.Background(), fixture.task.TenantID, candidateID,
			); !errors.Is(err, ErrLearningProposalNotFound) {
				t.Fatalf("exact candidate Proposal unexpectedly exists: %v", err)
			}
			storedConflict, err := fixture.store.GetLearningProposal(
				context.Background(), fixture.task.TenantID, conflict.ProposalID,
			)
			if err != nil || storedConflict.State != conflict.State {
				t.Fatalf("occupied Proposal changed = %+v, %v", storedConflict, err)
			}
			if test.want == learningcontract.LearningCycleTaskContentOccupiedV1 &&
				storedConflict.State != LearningProposalRejected {
				t.Fatalf("rejected Proposal no longer occupies content: %+v", storedConflict)
			}
			retry, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			)
			if err != nil || retry.Applied || retry.Task.State != test.want ||
				retry.Task.ProposalID != conflict.ProposalID {
				t.Fatalf("occupied retry = %+v, %v", retry, err)
			}
		})
	}
}

func TestFinalizeLearningCycleTaskRejectedSourceRemainsOccupied(t *testing.T) {
	visibility := []moduleapi.KnowledgeScopeRuleV1{{
		TenantID: "tenant-publish", WorkspaceID: "workspace-default",
		AgentID: "*", TaskInputRef: "*",
	}}
	fixture := newLearningCycleExecutionFixture(
		t,
		learningcontract.ProposalKindKnowledgeV1,
		json.RawMessage(`{"max_tokens":512}`),
		512,
		visibility,
	)
	fixture.admit(t)
	begin := fixture.review.beginAttempt(t)
	cycleResult, cycleCanonical, _, err :=
		learningcontract.NewLearningCycleResultV1(
			learningcontract.LearningCycleResultV1{
				SchemaVersion: learningcontract.LearningCycleResultSchemaVersionV1,
				RequestDigest: fixture.task.RequestDigest,
				Decision:      learningcontract.LearningCycleResultProposeV1,
				KnowledgeChunks: []string{
					"Rejected source occupancy candidate.",
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	outcome := fixture.review.commitAttemptOutcome(
		t,
		begin,
		corecontract.ModelAttemptSucceeded,
		func(learningReviewStoreFixture) string { return string(cycleCanonical) },
		false,
		nil,
	)
	resultRef := outcome.Record.Attempt.ResultRef
	candidateDraft, err := buildLearningCycleDraft(
		fixture.schedule,
		fixture.task,
		resultRef,
		cycleResult,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate, _, candidateID, err := learningcontract.NewProposalV1(
		learningCycleProposalValue(fixture, resultRef, fixture.task.Request.Target),
		candidateDraft,
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	conflict := submitLearningCycleKnowledgeConflict(
		t,
		fixture,
		resultRef,
		moduleapi.Ref{
			ID: "freeagent.learning-cycle.rejected-source", Version: "v1",
		},
		"Different content for the source that will be rejected.",
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
	)
	if conflict.Proposal.SourceFingerprint != candidate.SourceFingerprint ||
		conflict.Proposal.ContentFingerprint == candidate.ContentFingerprint ||
		conflict.Proposal.Target == candidate.Target {
		t.Fatalf(
			"rejected-source fixture does not isolate the source axis: conflict=%+v candidate=%+v",
			conflict.Proposal,
			candidate,
		)
	}

	review := newLearningCycleConflictReviewFixture(t, fixture, conflict)
	review.commitAdmission(t)
	reviewBegin := review.beginAttempt(t)
	review.commitAttemptOutcome(
		t,
		reviewBegin,
		corecontract.ModelAttemptSucceeded,
		func(f learningReviewStoreFixture) string {
			return f.verdict(
				t,
				learningcontract.ReviewDecisionRejectV1,
				[]learningcontract.ReviewIssueCodeV1{
					learningcontract.ReviewIssueMissingEvidenceV1,
				},
			)
		},
		false,
		nil,
	)
	rejected, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 conflict.Proposal.TenantID,
			ProposalID:               conflict.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil || !rejected.Applied ||
		rejected.Proposal.State != LearningProposalRejected {
		t.Fatalf("reject source Proposal = %+v, %v", rejected, err)
	}
	rejectedBefore, err := fixture.store.GetLearningProposal(
		context.Background(),
		conflict.Proposal.TenantID,
		conflict.ProposalID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var proposalCountBefore int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM learning_proposals`,
	).Scan(&proposalCountBefore); err != nil {
		t.Fatal(err)
	}

	finalized, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 1,
		},
	)
	if err != nil || !finalized.Applied ||
		finalized.Task.State != learningcontract.LearningCycleTaskSourceOccupiedV1 ||
		finalized.Task.ProposalID != conflict.ProposalID {
		t.Fatalf("rejected source finalization = %+v, %v", finalized, err)
	}
	var proposalCountAfter int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM learning_proposals`,
	).Scan(&proposalCountAfter); err != nil {
		t.Fatal(err)
	}
	if proposalCountAfter != proposalCountBefore {
		t.Fatalf(
			"rejected source created a Proposal: before=%d after=%d",
			proposalCountBefore,
			proposalCountAfter,
		)
	}
	if _, err := fixture.store.GetLearningProposal(
		context.Background(), fixture.task.TenantID, candidateID,
	); !errors.Is(err, ErrLearningProposalNotFound) {
		t.Fatalf("rejected-source candidate unexpectedly exists: %v", err)
	}
	rejectedAfter, err := fixture.store.GetLearningProposal(
		context.Background(),
		conflict.Proposal.TenantID,
		conflict.ProposalID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rejectedAfter, rejectedBefore) {
		t.Fatalf(
			"SOURCE_OCCUPIED changed the rejected Proposal:\nbefore=%+v\nafter=%+v",
			rejectedBefore,
			rejectedAfter,
		)
	}
}

func learningCycleProposalValue(
	fixture *learningCycleExecutionFixture,
	resultRef string,
	target moduleapi.Ref,
) learningcontract.ProposalV1 {
	return learningcontract.ProposalV1{
		SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
		Kind:                   learningcontract.ProposalKindKnowledgeV1,
		TenantID:               fixture.task.TenantID,
		Workspace:              fixture.manifest.Workspace,
		ProposerAgent:          fixture.manifest.PrimaryAgent,
		ProposerProfile:        fixture.member.Profile,
		ProposerRunID:          fixture.identity.RunID,
		ProposerManifestDigest: fixture.manifest.ManifestDigest,
		ProposerMember:         fixture.manifest.Members[0],
		ProposerResultRef:      resultRef,
		Target:                 target,
	}
}

func submitLearningCycleKnowledgeConflict(
	t *testing.T,
	fixture *learningCycleExecutionFixture,
	resultRef string,
	target moduleapi.Ref,
	text string,
	evidence learningcontract.SourceEvidenceV1,
) LearningProposalRecord {
	t.Helper()
	_, draft, _, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            target.ID,
			Version:       target.Version,
			Chunks: []moduleapi.KnowledgeChunkV1{{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      target.ID,
					Version: target.Version,
					Digest:  learningTestDigest(target.ID + "\x00" + target.Version + "\x00" + text),
				},
				ChunkID: "learning-cycle-conflict",
				Text:    text,
				VisibleTo: append(
					[]moduleapi.KnowledgeScopeRuleV1(nil),
					fixture.schedule.Schedule.KnowledgeVisibility...,
				),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		SubmitKnowledgeProposalInput{
			Proposal:       learningCycleProposalValue(fixture, resultRef, target),
			DraftCanonical: draft,
			SourceEvidence: evidence,
		},
	)
	if err != nil || !submitted.Created {
		t.Fatalf("submit occupied-axis Proposal = %+v, %v", submitted, err)
	}
	return submitted.Record
}

func newLearningCycleConflictReviewFixture(
	t *testing.T,
	cycle *learningCycleExecutionFixture,
	proposal LearningProposalRecord,
) learningReviewStoreFixture {
	t.Helper()
	_, reviewCanonical, _, err := learningcontract.NewReviewRequestV1(
		learningcontract.ReviewRequestV1{
			SchemaVersion:       learningcontract.ReviewRequestSchemaVersionV1,
			ProposalID:          proposal.ProposalID,
			SourceFingerprint:   proposal.Proposal.SourceFingerprint,
			ContentFingerprint:  proposal.Proposal.ContentFingerprint,
			DraftDigest:         proposal.Proposal.DraftDigest,
			OutputSchemaVersion: learningcontract.ReviewVerdictSchemaVersionV1,
			MaxOutputTokens:     512,
			ReviewPolicy:        learningcontract.ReviewPolicyProposalGateV1,
			Instructions:        learningcontract.ReviewInstructionsV1,
			ProposalCanonical:   append([]byte(nil), proposal.ProposalCanonical...),
			DraftCanonical:      append([]byte(nil), proposal.DraftCanonical...),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          string(reviewCanonical),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := newAdmissionContent(t, ContentTaskInput, taskCanonical)
	reviewerAgent := cycle.review.base.member.Agent
	reviewerProfile := cycle.review.base.member.Profile
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      proposal.Proposal.TenantID,
			AdmissionKey:  "learning-cycle-rejected-source-review-admission",
			PrincipalID:   "learning-cycle-rejected-source-review-principal",
			WorkspaceID:   proposal.Proposal.Workspace.ID,
			AgentID:       reviewerAgent.ID,
			ProfileID:     reviewerProfile.ID,
			TaskInputRef:  task.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2,
			}},
			Deadline:          time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			CancellationScope: "run",
			ExplicitLimits:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	basis := cycle.review.admission.PublishedBasis
	var controlCanonical, catalogCanonical []byte
	if err := cycle.store.db.QueryRow(
		`SELECT canonical_json FROM control_snapshots WHERE snapshot_id=?`,
		basis.Control.SnapshotID,
	).Scan(&controlCanonical); err != nil {
		t.Fatal(err)
	}
	if err := cycle.store.db.QueryRow(
		`SELECT canonical_json FROM runtime_catalog_generations WHERE generation_id=?`,
		basis.Catalog.GenerationID,
	).Scan(&catalogCanonical); err != nil {
		t.Fatal(err)
	}
	const (
		runID    = "run-learning-cycle-rejected-source-review"
		memberID = "member-learning-cycle-rejected-source-review"
	)
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            runID,
			MemberID:         memberID,
			RecoveryRootRef:  "recovery/" + runID,
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(compiled.MemberSnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return learningReviewStoreFixture{
		store:    cycle.store,
		base:     cycle.review.base,
		proposal: proposal,
		admission: CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents:                []ContentInput{task},
		},
		manifest:   manifest,
		member:     member,
		parameters: append(json.RawMessage(nil), cycle.review.parameters...),
		attemptID:  "attempt-learning-cycle-rejected-source-review",
	}
}

func TestFinalizeLearningCycleUnknownReconcilesSameAttemptWithEvidence(
	t *testing.T,
) {
	fixture := newLearningCycleExecutionFixture(
		t,
		learningcontract.ProposalKindSkillV1,
		json.RawMessage(`{"max_tokens":512}`),
		512,
		nil,
	)
	fixture.admit(t)
	begin := fixture.review.beginAttempt(t)
	unknown := fixture.review.commitAttemptOutcome(
		t,
		begin,
		corecontract.ModelAttemptUnknown,
		nil,
		false,
		nil,
	)
	projected, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 1,
		},
	)
	if err != nil || !projected.Applied ||
		projected.Task.State != learningcontract.LearningCycleTaskUnknownV1 ||
		projected.Task.Revision != 2 ||
		projected.Task.AttemptID != begin.Attempt.AttemptID ||
		projected.Task.ResultRef != "" || projected.Task.ProposalID != "" {
		t.Fatalf("UNKNOWN projection = %+v, %v", projected, err)
	}
	unchanged, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 2,
		},
	)
	if err != nil || unchanged.Applied || unchanged.Task.Revision != 2 {
		t.Fatalf("unchanged UNKNOWN = %+v, %v", unchanged, err)
	}
	assistant := func(_ learningReviewStoreFixture) string {
		return learningCycleResultCanonical(
			t,
			fixture.task,
			learningcontract.LearningCycleResultNoChangeV1,
			nil,
			"",
		)
	}
	fixture.review.commitAttemptOutcome(
		t,
		BeginModelDispatchResult{Attempt: unknown.Record.Attempt, Lease: unknown.Lease},
		corecontract.ModelAttemptSucceeded,
		assistant,
		false,
		[]byte(`{"kind":"provider_lookup","request_id":"review-request"}`),
	)
	reconciled, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 2,
		},
	)
	if err != nil || !reconciled.Applied ||
		reconciled.Task.State != learningcontract.LearningCycleTaskNoChangeV1 ||
		reconciled.Task.Revision != 3 ||
		reconciled.Task.AttemptID != begin.Attempt.AttemptID ||
		reconciled.Task.ResultRef == "" {
		t.Fatalf("reconciled task = %+v, %v", reconciled, err)
	}
	var attemptCount int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?`,
		fixture.identity.RunID,
	).Scan(&attemptCount); err != nil || attemptCount != 1 {
		t.Fatalf("reconciled Attempt count = %d, %v", attemptCount, err)
	}
}

type learningCycleExecutionFixture struct {
	store      *Store
	review     *learningReviewStoreFixture
	seedReview learningReviewStoreFixture
	schedule   LearningCycleScheduleRecord
	task       LearningCycleTaskRecord
	identity   learningcontract.LearningCycleExecutionIdentityV1
	admission  CommitRunAdmissionInput
	manifest   corecontract.RunManifest
	member     corecontract.MemberExecutionSnapshot
}

func newLearningCycleExecutionFixture(
	t *testing.T,
	kind learningcontract.ProposalKindV1,
	parameters json.RawMessage,
	maxOutputTokens uint32,
	visibility []moduleapi.KnowledgeScopeRuleV1,
) *learningCycleExecutionFixture {
	t.Helper()
	review := newLearningReviewStoreFixture(t, parameters)
	seedReview := *review
	firstDue := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	if kind == learningcontract.ProposalKindKnowledgeV1 && visibility == nil {
		visibility = []moduleapi.KnowledgeScopeRuleV1{{
			TenantID:    review.manifest.TenantID,
			WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
		}}
	}
	scheduleInput := learningcontract.LearningCycleScheduleV1{
		SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
		TenantID:             review.manifest.TenantID,
		ScheduleID:           "schedule-cycle-execution",
		ServicePrincipalID:   "principal-cycle-execution",
		WorkspaceID:          review.member.Workspace.ID,
		AgentID:              review.member.Agent.ID,
		ProfileID:            review.member.Profile.ID,
		Kind:                 kind,
		TargetModuleID:       "freeagent.learning-cycle.generated",
		Objective:            "Produce one bounded Store-derived cycle result.",
		KnowledgeVisibility:  append([]moduleapi.KnowledgeScopeRuleV1(nil), visibility...),
		FirstDueAtUnixMicros: firstDue.UnixMicro(),
		MaxOutputTokens:      maxOutputTokens,
	}
	_, err := review.store.CreateLearningCycleSchedule(
		context.Background(), scheduleInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := review.store.SetLearningCycleScheduleEnabled(
		context.Background(),
		SetLearningCycleScheduleEnabledInput{
			TenantID: scheduleInput.TenantID, ScheduleID: scheduleInput.ScheduleID,
			ExpectedRevision: 0, Enabled: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	due, err := review.store.EnsureDueTask(
		context.Background(),
		scheduleInput.TenantID,
		scheduleInput.ScheduleID,
		firstDue,
	)
	if err != nil || !due.Created {
		t.Fatalf("EnsureDueTask = %+v, %v", due, err)
	}
	identity, err := learningcontract.DeriveLearningCycleExecutionIdentityV1(
		due.Task.TaskID,
		due.Task.RequestDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &learningCycleExecutionFixture{
		store: review.store, review: review,
		seedReview: seedReview,
		schedule:   due.Schedule, task: due.Task, identity: identity,
	}
	fixture.admission, fixture.manifest, fixture.member = fixture.compileAdmission(
		t,
		time.UnixMicro(due.Task.Request.WindowEndMicros).UTC(),
	)
	review.admission = fixture.admission
	review.manifest = fixture.manifest
	review.member = fixture.member
	review.attemptID = "attempt-learning-cycle"
	return fixture
}

func (fixture *learningCycleExecutionFixture) compileAdmission(
	t *testing.T,
	deadline time.Time,
) (CommitRunAdmissionInput, corecontract.RunManifest, corecontract.MemberExecutionSnapshot) {
	t.Helper()
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          string(fixture.task.RequestCanonical),
	})
	if err != nil {
		t.Fatal(err)
	}
	taskContent := newAdmissionContent(t, ContentTaskInput, taskCanonical)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      fixture.task.TenantID,
			AdmissionKey:  fixture.identity.AdmissionKey,
			PrincipalID:   fixture.schedule.Schedule.ServicePrincipalID,
			WorkspaceID:   fixture.schedule.Schedule.WorkspaceID,
			AgentID:       fixture.schedule.Schedule.AgentID,
			ProfileID:     fixture.schedule.Schedule.ProfileID,
			TaskInputRef:  taskContent.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2,
			}},
			Deadline:          deadline,
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var controlCanonical, catalogCanonical []byte
	if err := fixture.store.db.QueryRow(
		`SELECT canonical_json FROM control_snapshots WHERE snapshot_id=?`,
		fixture.review.admission.PublishedBasis.Control.SnapshotID,
	).Scan(&controlCanonical); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRow(
		`SELECT canonical_json FROM runtime_catalog_generations WHERE generation_id=?`,
		fixture.review.admission.PublishedBasis.Catalog.GenerationID,
	).Scan(&catalogCanonical); err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical: intentCanonical, IntentDigest: intentDigest,
			RunID: fixture.identity.RunID, MemberID: fixture.identity.MemberID,
			RecoveryRootRef:  fixture.identity.RecoveryRootRef,
			PublishedBasis:   fixture.review.admission.PublishedBasis,
			ControlCanonical: controlCanonical, CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(compiled.MemberSnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return CommitRunAdmissionInput{
		PublishedBasis:          fixture.review.admission.PublishedBasis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents:                []ContentInput{taskContent},
	}, manifest, member
}

func (fixture *learningCycleExecutionFixture) admit(
	t *testing.T,
) CommitLearningCycleRunAdmissionResult {
	t.Helper()
	result, err := fixture.store.CommitLearningCycleRunAdmission(
		context.Background(),
		CommitLearningCycleRunAdmissionInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 0, Run: fixture.admission,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.task = result.Task
	return result
}

func (fixture *learningCycleExecutionFixture) assertNoAdmittedRun(t *testing.T) {
	t.Helper()
	var count int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM runs WHERE run_id=?`, fixture.identity.RunID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("derived Run count = %d, %v", count, err)
	}
}

func learningCycleResultCanonical(
	t *testing.T,
	task LearningCycleTaskRecord,
	decision learningcontract.LearningCycleResultDecisionV1,
	chunks []string,
	skillText string,
) string {
	t.Helper()
	_, canonical, _, err := learningcontract.NewLearningCycleResultV1(
		learningcontract.LearningCycleResultV1{
			SchemaVersion:   learningcontract.LearningCycleResultSchemaVersionV1,
			RequestDigest:   task.RequestDigest,
			Decision:        decision,
			KnowledgeChunks: append([]string(nil), chunks...),
			SkillText:       skillText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func equalLearningCycleScopeRules(
	left []moduleapi.KnowledgeScopeRuleV1,
	right []moduleapi.KnowledgeScopeRuleV1,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
