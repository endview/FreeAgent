package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestReconcileLearningCycleTasksClosesFactsWithoutNewExecution(t *testing.T) {
	t.Run("PENDING_UNADMITTED", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		before := learningCycleExecutionEffectCounts(t, fixture.store)
		result, err := fixture.store.ReconcileLearningCycleTasks(
			context.Background(), fixture.task.TenantID, fixture.task.ScheduleID,
		)
		if err != nil || len(result.Entries) != 1 ||
			result.Entries[0].Status != LearningCycleReconciliationPendingUnadmitted ||
			result.Entries[0].Task.TaskID != fixture.task.TaskID {
			t.Fatalf("pending reconciliation = %+v, %v", result, err)
		}
		if after := learningCycleExecutionEffectCounts(t, fixture.store); after != before {
			t.Fatalf("pending reconciliation created execution/effect rows: before=%v after=%v",
				before, after)
		}
	})

	t.Run("OPEN_NOT_READY", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		fixture.admit(t)
		before := learningCycleExecutionEffectCounts(t, fixture.store)
		result, err := fixture.store.ReconcileLearningCycleTasks(
			context.Background(), fixture.task.TenantID, fixture.task.ScheduleID,
		)
		if err != nil || len(result.Entries) != 1 ||
			result.Entries[0].Status != LearningCycleReconciliationOpenNotReady ||
			result.Entries[0].Task.State != learningcontract.LearningCycleTaskRunAdmittedV1 {
			t.Fatalf("open reconciliation = %+v, %v", result, err)
		}
		if after := learningCycleExecutionEffectCounts(t, fixture.store); after != before {
			t.Fatalf("open reconciliation created execution/effect rows: before=%v after=%v",
				before, after)
		}
	})

	t.Run("FINALIZED and exact terminal helper", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		fixture.admit(t)
		begin := fixture.review.beginAttempt(t)
		fixture.review.commitAttemptOutcome(
			t,
			begin,
			corecontract.ModelAttemptSucceeded,
			func(learningReviewStoreFixture) string {
				return learningCycleResultCanonical(
					t,
					fixture.task,
					learningcontract.LearningCycleResultNoChangeV1,
					nil,
					"",
				)
			},
			false,
			nil,
		)
		before := learningCycleExecutionEffectCounts(t, fixture.store)
		result, err := fixture.store.ReconcileLearningCycleTasks(
			context.Background(), fixture.task.TenantID, fixture.task.ScheduleID,
		)
		if err != nil || len(result.Entries) != 1 ||
			result.Entries[0].Status != LearningCycleReconciliationFinalized ||
			result.Entries[0].Task.State != learningcontract.LearningCycleTaskNoChangeV1 ||
			result.Entries[0].Task.Revision != 2 {
			t.Fatalf("terminal reconciliation = %+v, %v", result, err)
		}
		if after := learningCycleExecutionEffectCounts(t, fixture.store); after != before {
			t.Fatalf("terminal reconciliation created execution/effect rows: before=%v after=%v",
				before, after)
		}
		exact, err := fixture.store.reconcileLearningCycleTask(
			context.Background(), result.Entries[0].Task,
		)
		if err != nil || exact.Status != LearningCycleReconciliationExactTerminal ||
			exact.Task.TaskID != fixture.task.TaskID {
			t.Fatalf("exact terminal reconciliation = %+v, %v", exact, err)
		}
	})
}

func TestReconcileLearningCycleUnknownReconciledCrashFinalizesSameAttempt(
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
		t, begin, corecontract.ModelAttemptUnknown, nil, false, nil,
	)
	projected, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 1,
		},
	)
	if err != nil || projected.Task.State != learningcontract.LearningCycleTaskUnknownV1 {
		t.Fatalf("project UNKNOWN = %+v, %v", projected, err)
	}
	fixture.review.commitAttemptOutcome(
		t,
		BeginModelDispatchResult{Attempt: unknown.Record.Attempt, Lease: unknown.Lease},
		corecontract.ModelAttemptSucceeded,
		func(learningReviewStoreFixture) string {
			return learningCycleResultCanonical(
				t,
				fixture.task,
				learningcontract.LearningCycleResultNoChangeV1,
				nil,
				"",
			)
		},
		false,
		[]byte(`{"kind":"provider_lookup","request_id":"review-request"}`),
	)
	before := learningCycleExecutionEffectCounts(t, fixture.store)
	reconciled, err := fixture.store.ReconcileLearningCycleTasks(
		context.Background(), fixture.task.TenantID, fixture.task.ScheduleID,
	)
	if err != nil || len(reconciled.Entries) != 1 ||
		reconciled.Entries[0].Status != LearningCycleReconciliationFinalized ||
		reconciled.Entries[0].Task.State != learningcontract.LearningCycleTaskNoChangeV1 ||
		reconciled.Entries[0].Task.Revision != 3 ||
		reconciled.Entries[0].Task.AttemptID != begin.Attempt.AttemptID {
		t.Fatalf("reconciled UNKNOWN crash = %+v, %v", reconciled, err)
	}
	if after := learningCycleExecutionEffectCounts(t, fixture.store); after != before {
		t.Fatalf("UNKNOWN compensation created execution/effect rows: before=%v after=%v",
			before, after)
	}
}

func TestBuildLearningCycleReportIsSortedHalfOpenAndReadOnly(t *testing.T) {
	fixture := newLearningCycleExecutionFixture(
		t,
		learningcontract.ProposalKindSkillV1,
		json.RawMessage(`{"max_tokens":512}`),
		512,
		nil,
	)
	fixture.admit(t)
	begin := fixture.review.beginAttempt(t)
	fixture.review.commitAttemptOutcome(
		t,
		begin,
		corecontract.ModelAttemptSucceeded,
		func(learningReviewStoreFixture) string {
			return learningCycleResultCanonical(
				t,
				fixture.task,
				learningcontract.LearningCycleResultNoChangeV1,
				nil,
				"",
			)
		},
		false,
		nil,
	)
	if _, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(),
		FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 1,
		},
	); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.store.EnsureDueTask(
		context.Background(),
		fixture.task.TenantID,
		fixture.task.ScheduleID,
		fixture.schedule.NextDueAt,
	)
	if err != nil || !second.Created {
		t.Fatalf("create second report task = %+v, %v", second, err)
	}
	start := fixture.task.ScheduledFor
	boundary := second.Task.ScheduledFor
	end := second.Schedule.NextDueAt
	before := learningCycleReportReadCounts(t, fixture.store)
	full, err := fixture.store.BuildLearningCycleReport(
		context.Background(), fixture.task.TenantID, fixture.task.ScheduleID, start, end,
	)
	if err != nil || len(full.Report.Entries) != 2 ||
		full.Report.Entries[0].TaskID != fixture.task.TaskID ||
		full.Report.Entries[1].TaskID != second.Task.TaskID ||
		full.Report.Entries[0].State != learningcontract.LearningCycleTaskNoChangeV1 ||
		full.Report.Entries[1].State != learningcontract.LearningCycleTaskPendingV1 {
		t.Fatalf("full report = %+v, %v", full, err)
	}
	restored, err := learningcontract.RestoreLearningCycleReportV1(
		full.ReportCanonical, full.ReportDigest,
	)
	if err != nil || len(restored.Entries) != 2 {
		t.Fatalf("restore report = %+v, %v", restored, err)
	}
	left, err := fixture.store.BuildLearningCycleReport(
		context.Background(), fixture.task.TenantID, fixture.task.ScheduleID, start, boundary,
	)
	if err != nil || len(left.Report.Entries) != 1 ||
		left.Report.Entries[0].TaskID != fixture.task.TaskID {
		t.Fatalf("left half-open report = %+v, %v", left, err)
	}
	right, err := fixture.store.BuildLearningCycleReport(
		context.Background(), fixture.task.TenantID, fixture.task.ScheduleID, boundary, end,
	)
	if err != nil || len(right.Report.Entries) != 1 ||
		right.Report.Entries[0].TaskID != second.Task.TaskID {
		t.Fatalf("right half-open report = %+v, %v", right, err)
	}
	if after := learningCycleReportReadCounts(t, fixture.store); after != before {
		t.Fatalf("report read wrote Store rows: before=%v after=%v", before, after)
	}
}

func TestLearningCycleBoundedOperationsReject1025WithoutTruncation(t *testing.T) {
	t.Run("reconciliation", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		insertLearningCycleProbeOverflowTasks(
			t, fixture, "UNKNOWN", maxLearningCycleOperationTasks,
		)
		if _, err := fixture.store.ReconcileLearningCycleTasks(
			context.Background(), fixture.task.TenantID, fixture.task.ScheduleID,
		); !errors.Is(err, ErrLearningCycleLimit) {
			t.Fatalf("reconciliation overflow error = %v", err)
		}
	})

	t.Run("report", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		insertLearningCycleProbeOverflowTasks(
			t, fixture, "NO_CHANGE", maxLearningCycleOperationTasks,
		)
		start := fixture.task.ScheduledFor
		end := start.Add(time.Duration(maxLearningCycleOperationTasks+2) * time.Microsecond)
		if _, err := fixture.store.BuildLearningCycleReport(
			context.Background(), fixture.task.TenantID, fixture.task.ScheduleID, start, end,
		); !errors.Is(err, ErrLearningCycleLimit) {
			t.Fatalf("report overflow error = %v", err)
		}
	})
}

func learningCycleExecutionEffectCounts(t *testing.T, store *Store) [4]int {
	t.Helper()
	var counts [4]int
	for index, table := range []string{
		"runs", "model_dispatch_attempts", "dispatch_attempts", "learning_proposals",
	} {
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&counts[index]); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func learningCycleReportReadCounts(t *testing.T, store *Store) [3]int {
	t.Helper()
	var counts [3]int
	for index, table := range []string{
		"learning_cycle_schedules", "learning_cycle_tasks", "content_records",
	} {
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&counts[index]); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func insertLearningCycleProbeOverflowTasks(
	t *testing.T,
	fixture *learningCycleExecutionFixture,
	state string,
	count int,
) {
	t.Helper()
	if _, err := fixture.store.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	transaction, err := fixture.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	statement, err := transaction.Prepare(`
		INSERT INTO learning_cycle_tasks(
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			run_id, run_manifest_digest, attempt_id,
			result_ref, proposal_id, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 2, ?, ?, ?, ?, NULL, 1, 2)
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for index := 1; index <= count; index++ {
		taskID := moduleapi.Digest(
			"freeagent.learning-cycle-operation-overflow/v1",
			[]byte(fmt.Sprintf("%s-%d", state, index)),
		)
		resultRef := any(nil)
		if state == "NO_CHANGE" {
			resultRef = moduleapi.Digest(
				"freeagent.learning-cycle-operation-overflow-result/v1",
				[]byte(fmt.Sprintf("%d", index)),
			)
		}
		if _, err := statement.Exec(
			taskID,
			fixture.task.TenantID,
			fixture.task.WorkspaceID,
			fixture.task.ScheduleID,
			fixture.task.ScheduleDigest,
			fixture.task.ScheduledFor.UnixMicro()+int64(index),
			fixture.task.RequestDigest,
			fixture.task.RequestCanonical,
			len(fixture.task.RequestCanonical),
			state,
			fmt.Sprintf("learning-cycle-overflow-run-%s-%d", state, index),
			fixture.task.ScheduleDigest,
			fmt.Sprintf("learning-cycle-overflow-attempt-%s-%d", state, index),
			resultRef,
		); err != nil {
			t.Fatalf("insert overflow task %d: %v", index, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
}
