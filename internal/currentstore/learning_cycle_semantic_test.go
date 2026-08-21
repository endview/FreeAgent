package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestVerifyLearningCycleSemanticClosureAcceptsDurableCrashWindows(t *testing.T) {
	t.Run("PENDING before Admission", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		if err := verifyLearningCycleSemanticTestStore(t, fixture.store); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("RUN_ADMITTED before Attempt", func(t *testing.T) {
		fixture := newLearningCycleExecutionFixture(
			t,
			learningcontract.ProposalKindSkillV1,
			json.RawMessage(`{"max_tokens":512}`),
			512,
			nil,
		)
		fixture.admit(t)
		if err := verifyLearningCycleSemanticTestStore(t, fixture.store); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("RUN_ADMITTED after terminal Attempt", func(t *testing.T) {
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
		if err := verifyLearningCycleSemanticTestStore(t, fixture.store); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("UNKNOWN after evidence reconciliation before task projection", func(t *testing.T) {
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
					learningcontract.LearningCycleResultProposeV1,
					nil,
					"Evidence-reconciled candidate awaiting Store projection.",
				)
			},
			false,
			[]byte(`{"kind":"provider_lookup","request_id":"review-request"}`),
		)
		before := learningCycleSemanticReadCounts(t, fixture.store)
		if err := verifyLearningCycleSemanticTestStore(t, fixture.store); err != nil {
			t.Fatal(err)
		}
		if after := learningCycleSemanticReadCounts(t, fixture.store); after != before {
			t.Fatalf("semantic verifier wrote crash-window facts: before=%v after=%v",
				before, after)
		}
		stored, err := fixture.store.GetLearningCycleTask(
			context.Background(), fixture.task.TenantID, fixture.task.TaskID,
		)
		if err != nil || stored.State != learningcontract.LearningCycleTaskUnknownV1 ||
			stored.Revision != 2 || stored.ProposalID != "" {
			t.Fatalf("semantic verifier projected UNKNOWN = %+v, %v", stored, err)
		}
	})

	t.Run("terminal Proposal", func(t *testing.T) {
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
					learningcontract.LearningCycleResultProposeV1,
					nil,
					"Persist the exact semantic-verifier candidate.",
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
		before := learningCycleSemanticReadCounts(t, fixture.store)
		if err := verifyLearningCycleSemanticTestStore(t, fixture.store); err != nil {
			t.Fatal(err)
		}
		if after := learningCycleSemanticReadCounts(t, fixture.store); after != before {
			t.Fatalf("semantic verifier changed terminal Proposal facts: before=%v after=%v",
				before, after)
		}
	})
}

func TestVerifyLearningCycleSemanticClosureRejectsCoordinationTamper(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, *learningCycleExecutionFixture, LearningCycleTaskRecord)
	}{
		{
			name: "Schedule watermark advances without Task",
			tamper: func(t *testing.T, fixture *learningCycleExecutionFixture, _ LearningCycleTaskRecord) {
				intervalMicros, err := learningCycleIntervalMicros(fixture.schedule.Schedule)
				if err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(t, fixture.store, nil, `
					UPDATE learning_cycle_schedules
					SET last_scheduled_for=next_due_at,
						next_due_at=next_due_at+?
					WHERE tenant_id=? AND schedule_id=?
				`, intervalMicros, fixture.task.TenantID, fixture.task.ScheduleID)
			},
		},
		{
			name: "Run AdmissionKey",
			tamper: func(t *testing.T, fixture *learningCycleExecutionFixture, _ LearningCycleTaskRecord) {
				execClosedFileTamperV1(t, fixture.store, nil, `
					UPDATE runs SET admission_key='tampered-cycle-admission'
					WHERE run_id=?
				`, fixture.identity.RunID)
			},
		},
		{
			name: "Task AttemptID",
			tamper: func(t *testing.T, fixture *learningCycleExecutionFixture, _ LearningCycleTaskRecord) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_cycle_tasks_observation_update_guard"}, `
					UPDATE learning_cycle_tasks SET attempt_id='attempt-cycle-tampered'
					WHERE task_id=?
				`, fixture.task.TaskID)
			},
		},
		{
			name: "Task ResultRef",
			tamper: func(t *testing.T, fixture *learningCycleExecutionFixture, _ LearningCycleTaskRecord) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_cycle_tasks_observation_update_guard"}, `
					UPDATE learning_cycle_tasks SET result_ref=? WHERE task_id=?
				`, moduleapi.Digest("cycle-semantic-tamper/v1", []byte("result")),
					fixture.task.TaskID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			finalized, err := fixture.store.FinalizeLearningCycleTask(
				context.Background(),
				FinalizeLearningCycleTaskInput{
					TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
					ExpectedTaskRevision: 1,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			test.tamper(t, fixture, finalized.Task)
			if err := verifyLearningCycleSemanticTestStore(
				t, fixture.store,
			); !errors.Is(err, ErrLearningCycleIntegrity) {
				t.Fatalf("semantic tamper error = %v", err)
			}
		})
	}
}

func verifyLearningCycleSemanticTestStore(t *testing.T, store *Store) error {
	t.Helper()
	connection, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	return VerifyLearningCycleSemanticClosureV1(context.Background(), connection)
}

func learningCycleSemanticReadCounts(t *testing.T, store *Store) [5]int {
	t.Helper()
	var counts [5]int
	for index, table := range []string{
		"learning_cycle_schedules",
		"learning_cycle_tasks",
		"runs",
		"model_dispatch_attempts",
		"learning_proposals",
	} {
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&counts[index]); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}
