package currentstore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPureChatLegalActionRejectionIsRecoverableTerminalFailure(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	beginInput.LogicalStepID = corecontract.PureChatModelLogicalStepIDV1
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID:       "unknown.action",
				CanonicalInput: json.RawMessage(`{}`),
			},
			ProviderRequestID: "provider-request-action",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, usageCanonical := modelSuccessOutcomeCanonical(t)
	input := CommitLegalModelActionRejectionInput{
		Lease:                        begin.Lease,
		ModelAttemptID:               begin.Attempt.AttemptID,
		InvocationID:                 begin.Attempt.AttemptID,
		Provider:                     begin.Attempt.Binding.Provider,
		ExpectedModelAttemptRevision: begin.Attempt.Revision,
		OutputCanonical:              outputCanonical,
		UsageReceiptCanonical:        usageCanonical,
		ProviderRequestID:            "provider-request-action",
		ErrorClassification:          ModelActionRejectionUnknownAction,
	}
	committed, err := fixture.store.CommitLegalModelActionRejection(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Applied ||
		committed.Model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		committed.Model.Attempt.ErrorClassification != "" ||
		committed.ErrorClassification != ModelActionRejectionUnknownAction {
		t.Fatalf("committed=%+v", committed)
	}
	var actions int
	if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM dispatch_attempts`).Scan(
		&actions,
	); err != nil || actions != 0 {
		t.Fatalf("Action count=%d error=%v", actions, err)
	}
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		committed.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.Frame.Step != corecontract.TerminatedLoopStep ||
		len(run.History) != 0 || len(run.ActionDispatches) != 0 {
		t.Fatalf("run=%+v", run)
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		run.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.ModelState != corecontract.ModelAttemptSucceeded ||
		terminal.ErrorClassification != ModelActionRejectionUnknownAction ||
		len(terminal.OutputCanonical) != 0 {
		t.Fatalf("terminal=%+v", terminal)
	}
	replayed, err := fixture.store.CommitLegalModelActionRejection(
		context.Background(),
		input,
	)
	if err != nil || replayed.Applied || replayed.Lease != committed.Lease {
		t.Fatalf("replayed=%+v error=%v", replayed, err)
	}
}
