package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCommitModelDispatchOutcomePersistsFrozenDeepSeekEstimatedCost(
	t *testing.T,
) {
	price := testModelPriceSnapshot()
	price.Pricing = json.RawMessage(
		`{"cached_input_per_million_microunits":25000,"output_per_million_microunits":6000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":3000000}`,
	)
	fixture, _, beginInput := newModelDispatchFixtureWithModelProfileAndPrice(
		t,
		0,
		price,
	)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	input := CommitModelDispatchOutcomeInput{
		Lease:                   begin.Lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Attempt.Revision,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         outputCanonical,
		UsageReceiptCanonical:   usageCanonical,
	}
	result, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "0.0000301"
	if result.Record.Usage.EstimatedCost == nil ||
		*result.Record.Usage.EstimatedCost != expected {
		t.Fatalf("estimated cost=%v, want %s", result.Record.Usage.EstimatedCost, expected)
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Usage.EstimatedCost == nil ||
		*stored.Usage.EstimatedCost != expected ||
		stored.Usage.ReconciledCost != nil ||
		stored.Usage.Tokens.Reasoning == nil ||
		*stored.Usage.Tokens.Reasoning != 1 {
		t.Fatalf("stored Usage=%+v", stored.Usage)
	}
	replayed, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Applied || replayed.Lease != result.Lease ||
		replayed.Record.Attempt.Revision != result.Record.Attempt.Revision ||
		replayed.Record.Usage.Revision != result.Record.Usage.Revision ||
		replayed.Record.Usage.EstimatedCost == nil ||
		*replayed.Record.Usage.EstimatedCost != expected {
		t.Fatalf("estimated-cost replay=%+v original=%+v", replayed, result)
	}
}

func TestCommitModelDispatchOutcomePreservesUnknownFrozenCostAsNull(
	t *testing.T,
) {
	price := testModelPriceSnapshot()
	price.PricingStatus = corecontract.PricingUnknown
	price.Pricing = json.RawMessage(`{"schema_version":"unknown/v1"}`)
	fixture, _, beginInput := newModelDispatchFixtureWithModelProfileAndPrice(
		t,
		0,
		price,
	)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	result, err := fixture.store.CommitModelDispatchOutcome(
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
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Usage.EstimatedCost != nil {
		t.Fatalf("unknown estimated cost=%v, want NULL", result.Record.Usage.EstimatedCost)
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Usage.EstimatedCost != nil ||
		stored.Usage.Tokens.CachedInput == nil ||
		stored.Usage.Tokens.UncachedInput == nil ||
		stored.Usage.Tokens.Output == nil {
		t.Fatalf("stored Usage=%+v", stored.Usage)
	}
}

func TestCommitModelDispatchOutcomeSuccessIsAtomicAndIdempotent(
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
	input := CommitModelDispatchOutcomeInput{
		Lease:                   begin.Lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Attempt.Revision,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         outputCanonical,
		UsageReceiptCanonical:   usageCanonical,
	}
	result, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied ||
		result.Record.Attempt.State != corecontract.ModelAttemptSucceeded ||
		result.Record.Usage.LedgerSequence == nil ||
		*result.Record.Usage.LedgerSequence != 1 ||
		result.Record.Usage.Tokens.Input == nil ||
		*result.Record.Usage.Tokens.Input != 10 {
		t.Fatalf("result=%+v", result)
	}

	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		result.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	head, err := corecontract.ParseBudgetStateRefV1(
		run.Frame.BudgetStateRef,
		run.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != corecontract.TerminatedLoopStep ||
		run.Disposition != corecontract.TerminatedLoopStep ||
		run.Frame.Step != corecontract.TerminatedLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		head != 1 ||
		len(run.History) != 1 ||
		run.History[0].Role != string(moduleapi.ModelRoleAssistant) ||
		run.History[0].SourceAttemptID != begin.Attempt.AttemptID {
		t.Fatalf("run=%+v", run)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(
		run.History[0].Content.CanonicalBytes,
	)
	if err != nil || output.AssistantText != "Hello from the model." {
		t.Fatalf("history output=%+v error=%v", output, err)
	}

	replayed, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Applied ||
		replayed.Record.Attempt.Revision !=
			result.Record.Attempt.Revision ||
		replayed.Lease != result.Lease {
		t.Fatalf("replayed=%+v result=%+v", replayed, result)
	}

	wrongProvider := input
	wrongProvider.Provider.InstanceID = "another-model-instance"
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		wrongProvider,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("cross-provider outcome error=%v", err)
	}

	_, changedOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "A conflicting result.",
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.OutputCanonical = changedOutput
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		input,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("conflicting replay error=%v", err)
	}
}

func TestCommitModelDispatchOutcomeUnknownBlocksReplayUntilReconciled(
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
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-request-1",
			UnknownReason:           "provider response was not observed",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Record.Attempt.State != corecontract.ModelAttemptUnknown ||
		unknown.Record.Usage.LedgerSequence != nil ||
		unknown.Record.Usage.ReconciliationStatus !=
			modelUsageStatusReconciliation {
		t.Fatalf("unknown=%+v", unknown)
	}

	beginInput.Lease = unknown.Lease
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created || retry.InvokeAllowed ||
		retry.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("retry=%+v", retry)
	}

	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	reconcile := CommitModelDispatchOutcomeInput{
		Lease:                   unknown.Lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: unknown.Record.Attempt.Revision,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         outputCanonical,
		UsageReceiptCanonical:   usageCanonical,
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		reconcile,
	); !errors.Is(err, ErrInvalidModelDispatch) {
		t.Fatalf("reconciliation without evidence error=%v", err)
	}

	reconcile.ReconciliationEvidenceCanonical = []byte(
		`{"kind":"provider_lookup","request_id":"provider-request-1"}`,
	)
	terminal, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		reconcile,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Record.Attempt.State !=
		corecontract.ModelAttemptSucceeded ||
		terminal.Record.Attempt.ReconciliationEvidenceRef == "" ||
		terminal.Record.Usage.LedgerSequence == nil ||
		*terminal.Record.Usage.LedgerSequence != 1 {
		t.Fatalf("terminal=%+v", terminal)
	}
	unsettled, err := fixture.store.ScanUnsettledModelDispatchRecords(
		context.Background(),
		begin.Attempt.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsettled) != 0 {
		t.Fatalf("unsettled=%+v", unsettled)
	}
}

func TestCommitModelDispatchOutcomeRollsBackEveryAuthoritativeWrite(
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
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER fail_model_history
		BEFORE INSERT ON history_entries
		BEGIN
			SELECT RAISE(ABORT, 'forced history failure');
		END
	`); err != nil {
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
	); err == nil {
		t.Fatal("forced persistence failure was accepted")
	}

	record, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.State != corecontract.ModelAttemptPending ||
		record.Attempt.Revision != 0 ||
		record.Usage.Revision != 0 ||
		record.Usage.LedgerSequence != nil ||
		record.Usage.ReconciliationStatus != modelUsageStatusPending {
		t.Fatalf("record after rollback=%+v", record)
	}
	var historyCount, eventCount int
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM history_entries),
			(SELECT COUNT(*) FROM run_events)
	`).Scan(&historyCount, &eventCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 0 || eventCount != 2 {
		t.Fatalf(
			"history count=%d event count=%d",
			historyCount,
			eventCount,
		)
	}
	outputDigest, err := ComputeContentDigest(
		ContentModelResult,
		admissionJSONMediaType,
		outputCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.GetContent(
		context.Background(),
		outputDigest,
	); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("rolled-back result content error=%v", err)
	}
}

func TestModelUnknownReconciliationCannotReplaceKnownRawReceipt(
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
	outputCanonical, firstReceipt := modelSuccessOutcomeCanonical(t)
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-request-1",
			UsageReceiptCanonical:   firstReceipt,
			UnknownReason:           "completion was ambiguous",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt := modelUsageOutcomeCanonical(
		t,
		`{"id":"provider-request-1","status":"queried_later"}`,
	)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknown.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknown.Record.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   secondReceipt,
			ReconciliationEvidenceCanonical: []byte(
				`{"kind":"provider_lookup","request_id":"provider-request-1"}`,
			),
		},
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("raw receipt replacement error=%v", err)
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Attempt.State != corecontract.ModelAttemptUnknown ||
		stored.Usage.RawReceiptRef != unknown.Record.Usage.RawReceiptRef {
		t.Fatalf("stored=%+v", stored)
	}
}

func modelSuccessOutcomeCanonical(t *testing.T) ([]byte, []byte) {
	t.Helper()
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "Hello from the model.",
			ProviderRequestID: "provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return outputCanonical, modelUsageOutcomeCanonical(
		t,
		`{"id":"provider-request-1","status":"completed"}`,
	)
}

func modelUsageOutcomeCanonical(t *testing.T, raw string) []byte {
	t.Helper()
	input := uint64(10)
	cached := uint64(4)
	uncached := uint64(6)
	output := uint64(2)
	reasoning := uint64(1)
	cost := "0.0001"
	_, usageCanonical, err := moduleapi.NewModelUsageReceiptV1(
		moduleapi.ModelUsageReceiptV1{
			SchemaVersion:        moduleapi.ModelUsageReceiptSchemaV1,
			InputTokens:          &input,
			CachedInputTokens:    &cached,
			UncachedInputTokens:  &uncached,
			OutputTokens:         &output,
			ReasoningTokens:      &reasoning,
			ProviderReportedCost: &cost,
			RawReceipt:           []byte(raw),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return usageCanonical
}
