package currentstore

import (
	"context"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestCommitCoreDeterministicFailureIsAttemptFreeAtomicAndIdempotent(
	t *testing.T,
) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	putCompositeModelPrice(t, fixture.store)
	failedInput := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-core-failure-child",
		"child-step-core-failure",
	)
	failedBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		failedInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   failedBegin.Lease,
			AttemptID:               failedBegin.Attempt.AttemptID,
			InvocationID:            failedBegin.Attempt.AttemptID,
			Provider:                failedBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: failedBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptFailed,
			ErrorClassification:     "CHILD_PROVIDER_FAILED",
		},
	); err != nil {
		t.Fatal(err)
	}

	rootID := fixture.compiled.Parent.RunManifest.RunID
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		rootID,
		"core-failure-root",
	)
	committed, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  rootLease,
			Reason: corecontract.AllRequiredChildFailedReasonV1,
		},
	)
	if err != nil {
		t.Fatalf("CommitCoreDeterministicFailure: %v", err)
	}
	if !committed.Applied ||
		committed.Reason != corecontract.AllRequiredChildFailedReasonV1 {
		t.Fatalf("committed=%+v", committed)
	}

	retry, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  committed.Lease,
			Reason: corecontract.AllRequiredChildFailedReasonV1,
		},
	)
	if err != nil {
		t.Fatalf("exact re-entry: %v", err)
	}
	if retry.Applied || retry.Lease != committed.Lease ||
		retry.Reason != committed.Reason {
		t.Fatalf("retry=%+v committed=%+v", retry, committed)
	}
	if _, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  rootLease,
			Reason: corecontract.AllRequiredChildFailedReasonV1,
		},
	); !errors.Is(err, ErrCoreDeterministicFailureConflict) {
		t.Fatalf("stale CAS error=%v", err)
	}

	loaded, err := fixture.store.LoadRunForLoop(
		context.Background(),
		committed.Lease,
	)
	if err != nil {
		t.Fatalf("LoadRunForLoop(terminal): %v", err)
	}
	continued, err := corecontract.RestoreLoopContinuationV1(
		loaded.Frame.Continuation,
	)
	if err != nil || loaded.Frame.Step != corecontract.TerminatedLoopStep ||
		continued.CoreFailureReason !=
			corecontract.AllRequiredChildFailedReasonV1 {
		t.Fatalf("loaded=%+v continuation=%+v error=%v", loaded.Frame, continued, err)
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		rootID,
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult: %v", err)
	}
	if terminal.ReasonCode != corecontract.AllRequiredChildFailedReasonV1 ||
		terminal.ErrorClassification !=
			corecontract.AllRequiredChildFailedReasonV1 ||
		terminal.AttemptKind != "" || terminal.AttemptID != "" {
		t.Fatalf("terminal=%+v", terminal)
	}

	var modelAttempts, dispatchAttempts, historyRows, terminalEvents int
	if err := fixture.store.db.QueryRow(`
		SELECT
		  (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
		  (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
		  (SELECT COUNT(*) FROM history_entries WHERE run_id=?),
		  (SELECT COUNT(*) FROM run_events
		   WHERE run_id=? AND event_kind=?)
	`,
		rootID,
		rootID,
		rootID,
		rootID,
		corecontract.CoreDeterministicFailureEventKind,
	).Scan(
		&modelAttempts,
		&dispatchAttempts,
		&historyRows,
		&terminalEvents,
	); err != nil {
		t.Fatal(err)
	}
	if modelAttempts != 0 || dispatchAttempts != 0 || historyRows != 0 ||
		terminalEvents != 1 {
		t.Fatalf(
			"models=%d dispatches=%d history=%d terminal events=%d",
			modelAttempts,
			dispatchAttempts,
			historyRows,
			terminalEvents,
		)
	}
}
