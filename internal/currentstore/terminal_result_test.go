package currentstore

import (
	"context"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestGetTerminalRunResultReturnsPersistedSuccessfulOutput(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		begin.Attempt.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != begin.Attempt.RunID ||
		result.AttemptID != begin.Attempt.AttemptID ||
		result.MemberID != begin.Attempt.MemberID ||
		result.State != corecontract.ModelAttemptSucceeded ||
		result.Output.AssistantText != "Hello from the model." ||
		result.Output.ProviderRequestID != "provider-request-1" ||
		string(result.OutputCanonical) != string(outputCanonical) ||
		result.ErrorClassification != "" {
		t.Fatalf("result=%+v", result)
	}
	result.OutputCanonical[0] = '!'
	reloaded, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		begin.Attempt.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.OutputCanonical) != string(outputCanonical) {
		t.Fatal("terminal output did not use a defensive copy")
	}
}

func TestGetTerminalRunResultReturnsPersistedFailure(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptFailed,
			ErrorClassification:     "PROVIDER_REPORTED_FAILURE",
		},
	); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		begin.Attempt.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != corecontract.ModelAttemptFailed ||
		result.ErrorClassification != "PROVIDER_REPORTED_FAILURE" ||
		len(result.OutputCanonical) != 0 ||
		result.Output.AssistantText != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestGetTerminalRunResultRejectsNonTerminalAndCorruptHistory(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	if _, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		beginInput.Lease.RunID,
	); !errors.Is(err, ErrTerminalRunUnavailable) {
		t.Fatalf("non-terminal error=%v", err)
	}

	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`DELETE FROM history_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		begin.Attempt.RunID,
	); !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("missing History error=%v", err)
	}
}

func TestGetTerminalRunResultRejectsContinuationAttemptMismatch(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	badContinuation, err := corecontract.NewLoopContinuationV1(
		corecontract.TerminatedLoopStep,
		begin.Attempt.LogicalStepID,
		"different-attempt",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(
		`UPDATE loop_frames SET continuation=? WHERE run_id=?`,
		badContinuation,
		begin.Attempt.RunID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		begin.Attempt.RunID,
	); !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("mismatched continuation error=%v", err)
	}
}
