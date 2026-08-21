package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestLoadRunForLoopReturnsFrozenRecoveryClosure(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-admitted",
			OwnerID:               "loop-worker-1",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != lease.RunID ||
		run.Manifest.RunID != lease.RunID ||
		run.Member.MemberID != run.Manifest.PrimaryMemberID ||
		run.Frame.Revision != lease.FrameRevision ||
		run.Frame.Step != "READY" ||
		len(run.History) != 0 {
		t.Fatalf("run=%+v", run)
	}
	if task, found := run.FindContent(
		run.Manifest.TaskInputRef,
	); !found || task.Kind != ContentTaskInput {
		t.Fatalf("task found=%v record=%+v", found, task)
	}
	foundStatic := false
	for _, plan := range run.Member.PortPlans {
		for _, binding := range plan.Bindings {
			for _, digest := range binding.StaticContextRefs {
				record, found := run.FindContent(digest)
				if !found || record.Kind != ContentStaticContext {
					t.Fatalf(
						"static context %s found=%v record=%+v",
						digest,
						found,
						record,
					)
				}
				foundStatic = true
			}
		}
	}
	if !foundStatic {
		t.Fatal("frozen static context recovery edge is absent")
	}

	run.ManifestCanonical[0] = 'x'
	run.MemberCanonical[0] = 'x'
	run.Contents[0].CanonicalBytes[0] = 'x'
	again, err := fixture.store.LoadRunForLoop(
		context.Background(),
		lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.ManifestCanonical[0] != '{' ||
		again.MemberCanonical[0] != '{' ||
		again.Contents[0].CanonicalBytes[0] == 'x' {
		t.Fatal("caller mutated Store recovery closure")
	}
}

func TestLoadRunForLoopReopensByteExactModelProfileClosure(t *testing.T) {
	fixture, lease, before := commitAndLoadProfiledRun(t, 1000)
	if before.Member.ModelProfile == nil {
		t.Fatal("profiled Run omitted ModelProfile ref")
	}
	beforeProfile, found := before.FindContent(
		before.Member.ModelProfile.Digest,
	)
	if !found || beforeProfile.Kind != ContentConfig {
		t.Fatalf("profile CONFIG found=%v record=%+v", found, beforeProfile)
	}

	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close profiled Store: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen profiled Store: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened profiled Store: %v", err)
		}
	})
	after, err := reopened.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop after reopen: %v", err)
	}
	afterProfile, found := after.FindContent(
		after.Member.ModelProfile.Digest,
	)
	if !found || afterProfile.Kind != ContentConfig {
		t.Fatalf("reopened profile CONFIG found=%v record=%+v", found, afterProfile)
	}
	if *after.Member.ModelProfile != *before.Member.ModelProfile ||
		!bytes.Equal(after.MemberCanonical, before.MemberCanonical) ||
		!bytes.Equal(
			afterProfile.CanonicalBytes,
			beforeProfile.CanonicalBytes,
		) {
		t.Fatal("reopened ModelProfile closure is not byte-exact")
	}
	if _, err := corecontract.RestoreModelProfileV1(
		afterProfile.CanonicalBytes,
		*after.Member.ModelProfile,
	); err != nil {
		t.Fatalf("restore reopened ModelProfile: %v", err)
	}
	if len(after.Contents) != len(before.Contents) {
		t.Fatalf(
			"reopened content count=%d want %d",
			len(after.Contents),
			len(before.Contents),
		)
	}
	for index := range before.Contents {
		left, right := before.Contents[index], after.Contents[index]
		if left.Digest != right.Digest ||
			left.Kind != right.Kind ||
			left.MediaType != right.MediaType ||
			!bytes.Equal(left.CanonicalBytes, right.CanonicalBytes) {
			t.Fatalf("reopened recovery content %d differs", index)
		}
	}
}

func TestLoadRunForLoopModelProfileCannotExpandContextPolicy(t *testing.T) {
	_, _, run := commitAndLoadProfiledRun(t, 2000)
	policyRecord, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found || policyRecord.Kind != ContentPolicy {
		t.Fatalf("ContextPolicy found=%v record=%+v", found, policyRecord)
	}
	document, err := corecontract.RestorePolicyDocument(
		policyRecord.CanonicalBytes,
		run.Member.ContextPolicy,
	)
	if err != nil {
		t.Fatalf("RestorePolicyDocument: %v", err)
	}
	base, err := corecontract.RestoreContextPolicyV1(document.Body)
	if err != nil {
		t.Fatalf("RestoreContextPolicyV1: %v", err)
	}
	effective, err := effectiveContextPolicyForMember(
		run.Member,
		base,
		runContentGetter(run),
	)
	if err != nil {
		t.Fatalf("effectiveContextPolicyForMember: %v", err)
	}
	if effective != base || effective.ContextWindowTokens != 1000 {
		t.Fatalf(
			"larger ModelProfile expanded ContextPolicy: base=%+v effective=%+v",
			base,
			effective,
		)
	}
}

func commitAndLoadProfiledRun(
	t *testing.T,
	contextWindowTokens uint64,
) (*admissionCommitFixture, RunLease, RunForLoop) {
	t.Helper()
	fixture := newAdmissionCommitFixtureWithModelProfile(
		t,
		contextWindowTokens,
	)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("CommitRunAdmission with ModelProfile: %v", err)
	}
	lease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-admitted",
			OwnerID:               "loop-worker-profiled",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireRunLease with ModelProfile: %v", err)
	}
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop with ModelProfile: %v", err)
	}
	return fixture, lease, run
}

func TestLoadRunForLoopRejectsStaleLease(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-admitted",
			OwnerID:               "loop-worker-1",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{
			Lease: first,
			TTL:   time.Minute,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.LoadRunForLoop(
		context.Background(),
		first,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("stale lease error=%v", err)
	}
}

func TestLoadRunForLoopRejectsEventAndContinuationCorruption(
	t *testing.T,
) {
	tests := []struct {
		name    string
		corrupt func(*Store) error
	}{
		{
			name: "event head",
			corrupt: func(store *Store) error {
				_, err := store.db.Exec(`
					UPDATE loop_frames
					SET last_authoritative_event=1
					WHERE run_id='run-admitted'
				`)
				return err
			},
		},
		{
			name: "continuation",
			corrupt: func(store *Store) error {
				_, err := store.db.Exec(`
					UPDATE loop_frames
					SET continuation=x'7b7d'
					WHERE run_id='run-admitted'
				`)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionCommitFixture(t)
			if _, err := fixture.store.CommitRunAdmission(
				context.Background(),
				fixture.input,
			); err != nil {
				t.Fatal(err)
			}
			lease, err := fixture.store.AcquireRunLease(
				context.Background(),
				AcquireRunLeaseInput{
					RunID:                 "run-admitted",
					OwnerID:               "loop-worker-1",
					ExpectedRunRevision:   0,
					ExpectedFrameRevision: 0,
					TTL:                   time.Minute,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.corrupt(fixture.store); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.LoadRunForLoop(
				context.Background(),
				lease,
			); !errors.Is(err, ErrLoopIntegrity) &&
				!errors.Is(err, ErrRunLeaseConflict) {
				t.Fatalf("corruption error=%v", err)
			}
		})
	}
}

func TestLoadRunForLoopRejectsHiddenUnsettledModelAttempt(t *testing.T) {
	fixture, _, input := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := corecontract.NewLoopContinuationV1(
		corecontract.InitialLoopStep,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`
		UPDATE loop_frames
		SET
			step=?,
			continuation=?,
			pending_attempt_id=NULL,
			waiting_reason=NULL
		WHERE run_id=?
	`, corecontract.InitialLoopStep, ready, begin.Attempt.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.LoadRunForLoop(
		context.Background(),
		begin.Lease,
	); !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("hidden PENDING error=%v", err)
	}

	deadlineContext, cancelDeadline := context.WithDeadline(
		context.Background(),
		time.Now().Add(-time.Second),
	)
	defer cancelDeadline()
	for _, probe := range []struct {
		failAt  int
		subject string
	}{
		{failAt: 1, subject: "hidden Model Attempt probe"},
		{failAt: 2, subject: "hidden dispatch Attempt probe"},
	} {
		queryer := &runObservationDeadlineQueryerV1{
			database:        fixture.store.db,
			deadlineContext: deadlineContext,
			failAt:          probe.failAt,
		}
		err := verifyRunObservationAttemptCardinalityV1(
			context.Background(),
			queryer,
			runObservationCanonicalV1{RunID: begin.Attempt.RunID},
			corecontract.LoopContinuationV1{},
		)
		if !errors.Is(err, ErrLoopIntegrity) ||
			!errors.Is(err, context.DeadlineExceeded) ||
			!strings.Contains(err.Error(), probe.subject) {
			t.Fatalf("%s deadline classification=%v", probe.subject, err)
		}
	}
}

type runObservationDeadlineQueryerV1 struct {
	database        *sql.DB
	deadlineContext context.Context
	failAt          int
	calls           int
}

func (queryer *runObservationDeadlineQueryerV1) QueryRowContext(
	_ context.Context,
	_ string,
	_ ...any,
) *sql.Row {
	queryer.calls++
	ctx := context.Background()
	if queryer.calls == queryer.failAt {
		ctx = queryer.deadlineContext
	}
	return queryer.database.QueryRowContext(ctx, "SELECT 0")
}

func (queryer *runObservationDeadlineQueryerV1) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	return queryer.database.QueryContext(ctx, query, args...)
}

func TestLoadRunForLoopRequiresExactAttemptUsageAndBudgetClosure(
	t *testing.T,
) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, *admissionCommitFixture, BeginModelDispatchResult)
	}{
		{
			name: "missing Usage",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				begin BeginModelDispatchResult,
			) {
				t.Helper()
				if _, err := fixture.store.db.Exec(`
					DELETE FROM model_usage
					WHERE attempt_id=?
				`, begin.Attempt.AttemptID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "BudgetRef belongs to another Run",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				begin BeginModelDispatchResult,
			) {
				t.Helper()
				if _, err := fixture.store.db.Exec(`
					UPDATE loop_frames
					SET budget_state_ref='usage-ledger/v1/other-run/0'
					WHERE run_id=?
				`, begin.Attempt.RunID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "BudgetRef is ahead of ledger",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				begin BeginModelDispatchResult,
			) {
				t.Helper()
				reference, err := corecontract.NewBudgetStateRefV1(
					begin.Attempt.RunID,
					1,
				)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.db.Exec(`
					UPDATE loop_frames
					SET budget_state_ref=?
					WHERE run_id=?
				`, reference, begin.Attempt.RunID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Frame says UNKNOWN but Attempt is PENDING",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				begin BeginModelDispatchResult,
			) {
				t.Helper()
				continuation, err := corecontract.NewLoopContinuationV1(
					corecontract.WaitingReconciliationLoopStep,
					begin.Attempt.LogicalStepID,
					begin.Attempt.AttemptID,
				)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.db.Exec(`
					UPDATE loop_frames
					SET
						step=?,
						continuation=?,
						waiting_reason='MODEL_UNKNOWN'
					WHERE run_id=?
				`,
					corecontract.WaitingReconciliationLoopStep,
					continuation,
					begin.Attempt.RunID,
				); err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(
					t,
					fixture.store,
					nil,
					`
					UPDATE runs
					SET state=?, disposition=?
					WHERE run_id=?
				`,
					corecontract.WaitingReconciliationLoopStep,
					corecontract.WaitingReconciliationLoopStep,
					begin.Attempt.RunID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _, input := newModelDispatchFixture(t)
			begin, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			)
			if err != nil {
				t.Fatal(err)
			}
			test.corrupt(t, fixture, begin)
			if _, err := fixture.store.LoadRunForLoop(
				context.Background(),
				begin.Lease,
			); !errors.Is(err, ErrLoopIntegrity) {
				t.Fatalf("corrupt recovery error=%v", err)
			}
		})
	}
}

func TestLoadRunForLoopValidatesHistoryAttemptProvenance(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, *admissionCommitFixture, string)
	}{
		{
			name: "missing History",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				runID string,
			) {
				t.Helper()
				if _, err := fixture.store.db.Exec(`
					DELETE FROM history_entries WHERE run_id=?
				`, runID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non-assistant role",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				runID string,
			) {
				t.Helper()
				if _, err := fixture.store.db.Exec(`
					UPDATE history_entries
					SET role='USER'
					WHERE run_id=?
				`, runID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing source Attempt",
			corrupt: func(
				t *testing.T,
				fixture *admissionCommitFixture,
				runID string,
			) {
				t.Helper()
				if _, err := fixture.store.db.Exec(`
					UPDATE history_entries
					SET source_attempt_id=NULL
					WHERE run_id=?
				`, runID); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _, input := newModelDispatchFixture(t)
			begin, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			)
			if err != nil {
				t.Fatal(err)
			}
			output, usage := modelSuccessOutcomeCanonical(t)
			terminal, err := fixture.store.CommitModelDispatchOutcome(
				context.Background(),
				CommitModelDispatchOutcomeInput{
					Lease:                   begin.Lease,
					AttemptID:               begin.Attempt.AttemptID,
					InvocationID:            begin.Attempt.AttemptID,
					Provider:                begin.Attempt.Binding.Provider,
					ExpectedAttemptRevision: begin.Attempt.Revision,
					State: corecontract.
						ModelAttemptSucceeded,
					OutputCanonical:       output,
					UsageReceiptCanonical: usage,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			test.corrupt(t, fixture, begin.Attempt.RunID)
			if _, err := fixture.store.LoadRunForLoop(
				context.Background(),
				terminal.Lease,
			); !errors.Is(err, ErrLoopIntegrity) {
				t.Fatalf("corrupt History error=%v", err)
			}
		})
	}
}
