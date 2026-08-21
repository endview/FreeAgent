package currentstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCommitModelActionAndBeginDispatchIsAtomicAndOneShot(t *testing.T) {
	harness := newActionStoreHarness(t)
	input := harness.actionBeginInput(t, "action-attempt-1")

	start := make(chan struct{})
	results := make(chan CommitModelActionAndBeginDispatchResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			result, err := harness.store.CommitModelActionAndBeginDispatch(
				context.Background(),
				input,
			)
			results <- result
			errs <- err
		}()
	}
	close(start)
	var committed CommitModelActionAndBeginDispatchResult
	applied := 0
	for range 2 {
		result := <-results
		if err := <-errs; err != nil {
			t.Fatalf("concurrent CommitModelActionAndBeginDispatch: %v", err)
		}
		if result.Applied {
			applied++
			committed = result
		} else if result.GatewayAllowed || result.ConsumeActionGatewayPermit() {
			t.Fatalf("exact concurrent retry received Gateway permission: %+v", result)
		}
	}
	if applied != 1 {
		t.Fatalf("applied transactions=%d want 1", applied)
	}
	if committed.Model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		committed.Model.Usage.LedgerSequence == nil ||
		*committed.Model.Usage.LedgerSequence != 1 ||
		committed.Action.Attempt.State != ActionDispatchPending ||
		committed.Action.Attempt.SourceModelAttemptID !=
			harness.modelBegin.Attempt.AttemptID ||
		!bytes.Equal(committed.Action.Proposal.CanonicalBytes, input.ProposalCanonical) {
		t.Fatalf("atomic model-to-Action record = %+v", committed)
	}

	// Copies share one private capability; concurrency cannot turn a single
	// committed pre-effect boundary into more than one Gateway invocation.
	var consumed atomic.Int32
	var wait sync.WaitGroup
	for range 32 {
		copyOfResult := committed
		wait.Add(1)
		go func() {
			defer wait.Done()
			if copyOfResult.ConsumeActionGatewayPermit() {
				consumed.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := consumed.Load(); got != 1 {
		t.Fatalf("consumed Action permits=%d want 1", got)
	}

	retry, err := harness.store.CommitModelActionAndBeginDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.Applied || retry.GatewayAllowed || retry.ConsumeActionGatewayPermit() ||
		retry.Action.Attempt.AttemptID != committed.Action.Attempt.AttemptID {
		t.Fatalf("exact retry = %+v", retry)
	}

	run, err := harness.store.LoadRunForLoop(context.Background(), committed.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.Frame.Step != corecontract.ActionPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != committed.Action.Attempt.AttemptID ||
		len(run.ModelDispatches) != 1 || len(run.ActionDispatches) != 1 {
		t.Fatalf("post-commit Run = %+v", run)
	}
	assertActionAttemptCount(t, harness.store, 1)
}

func TestCommitModelActionAndBeginDispatchDenialsAreAtomic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *actionStoreHarness, *CommitModelActionAndBeginDispatchInput)
	}{
		{
			name: "deadline exceeds lease",
			mutate: func(_ *testing.T, harness *actionStoreHarness, input *CommitModelActionAndBeginDispatchInput) {
				input.Deadline = harness.modelBegin.Lease.ExpiresAt.Add(time.Microsecond)
			},
		},
		{
			name: "budget unknown",
			mutate: func(_ *testing.T, _ *actionStoreHarness, input *CommitModelActionAndBeginDispatchInput) {
				input.BudgetDecision = ActionBudgetUnknown
			},
		},
		{
			name: "current activation revoked",
			mutate: func(t *testing.T, harness *actionStoreHarness, _ *CommitModelActionAndBeginDispatchInput) {
				publishEmptyCurrentCatalog(
					t,
					harness.store,
					harness.tenantID,
					harness.basis.PointerRevision,
				)
			},
		},
		{
			name: "provider identity drift",
			mutate: func(_ *testing.T, _ *actionStoreHarness, input *CommitModelActionAndBeginDispatchInput) {
				input.Provider.InstanceID = "different-model-instance"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newActionStoreHarness(t)
			input := harness.actionBeginInput(t, "action-attempt-denied")
			test.mutate(t, harness, &input)
			result, err := harness.store.CommitModelActionAndBeginDispatch(
				context.Background(),
				input,
			)
			if err == nil {
				t.Fatal("denied Action begin succeeded")
			}
			if result.Applied || result.GatewayAllowed ||
				result.ConsumeActionGatewayPermit() {
				t.Fatalf("denied Action begin returned permission: %+v", result)
			}
			assertActionAttemptCount(t, harness.store, 0)
			model, getErr := harness.store.GetModelDispatchRecord(
				context.Background(),
				harness.modelBegin.Attempt.AttemptID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if model.Attempt.State != corecontract.ModelAttemptPending ||
				model.Attempt.Revision != 0 || model.Usage.Revision != 0 ||
				model.Usage.LedgerSequence != nil {
				t.Fatalf("denied begin partially wrote Model/Usage: %+v", model)
			}
		})
	}
}

func TestCommitModelActionAndBeginDispatchRollsBackEveryAuthoritativeWrite(
	t *testing.T,
) {
	tests := []struct {
		name    string
		trigger string
	}{
		{
			name: "Action Proposal content",
			trigger: `
				CREATE TRIGGER fail_action_proposal
				BEFORE INSERT ON content_records
				WHEN NEW.kind='ACTION_PROPOSAL'
				BEGIN SELECT RAISE(ABORT, 'forced Action Proposal failure'); END
			`,
		},
		{
			name: "Model Usage update",
			trigger: `
				CREATE TRIGGER fail_action_usage
				BEFORE UPDATE ON model_usage
				BEGIN SELECT RAISE(ABORT, 'forced Action Usage failure'); END
			`,
		},
		{
			name: "Action Attempt insert",
			trigger: `
				CREATE TRIGGER fail_action_attempt
				BEFORE INSERT ON dispatch_attempts
				BEGIN SELECT RAISE(ABORT, 'forced Action Attempt failure'); END
			`,
		},
		{
			name: "Frame update",
			trigger: `
				CREATE TRIGGER fail_action_frame
				BEFORE UPDATE ON loop_frames
				WHEN NEW.step='ACTION_PENDING'
				BEGIN SELECT RAISE(ABORT, 'forced Action Frame failure'); END
			`,
		},
		{
			name: "Run Event insert",
			trigger: `
				CREATE TRIGGER fail_action_event
				BEFORE INSERT ON run_events
				WHEN NEW.event_kind='ACTION_DISPATCH_PENDING'
				BEGIN SELECT RAISE(ABORT, 'forced Action Event failure'); END
			`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newActionStoreHarness(t)
			before := actionBeginRollbackSnapshotForRun(
				t,
				harness.store,
				harness.modelBegin.Attempt.RunID,
			)
			if _, err := harness.store.db.Exec(test.trigger); err != nil {
				t.Fatalf("create fault trigger: %v", err)
			}
			result, err := harness.store.CommitModelActionAndBeginDispatch(
				context.Background(),
				harness.actionBeginInput(t, "action-attempt-rollback"),
			)
			if err == nil {
				t.Fatal("fault-injected Action begin succeeded")
			}
			if result.Applied || result.GatewayAllowed ||
				result.ConsumeActionGatewayPermit() {
				t.Fatalf("failed transaction returned a permit: %+v", result)
			}
			after := actionBeginRollbackSnapshotForRun(
				t,
				harness.store,
				harness.modelBegin.Attempt.RunID,
			)
			if after != before {
				t.Fatalf("partial Action begin write: before=%+v after=%+v", before, after)
			}
			model, getErr := harness.store.GetModelDispatchRecord(
				context.Background(),
				harness.modelBegin.Attempt.AttemptID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if model.Attempt.State != corecontract.ModelAttemptPending ||
				model.Attempt.Revision != 0 || model.Attempt.ResultRef != "" ||
				model.Usage.Revision != 0 || model.Usage.LedgerSequence != nil ||
				model.Usage.RawReceiptRef != "" {
				t.Fatalf("model/Usage escaped rollback: %+v", model)
			}
			run, loadErr := harness.store.LoadRunForLoop(
				context.Background(),
				harness.modelBegin.Lease,
			)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if run.Frame.Step != corecontract.ModelPendingLoopStep ||
				run.Frame.PendingAttemptID !=
					harness.modelBegin.Attempt.AttemptID ||
				run.Frame.PendingDispatchAttemptID != "" ||
				len(run.ActionDispatches) != 0 {
				t.Fatalf("Frame escaped rollback: %+v", run.Frame)
			}
		})
	}
}

func TestCommitActionDispatchOutcomeProjectsAuthoritativeRunState(t *testing.T) {
	tests := []struct {
		name            string
		input           func(ActionDispatchRecord, RunLease) CommitActionDispatchOutcomeInput
		wantAction      ActionDispatchState
		wantStep        string
		wantRunState    string
		wantDisposition string
		wantResult      corecontract.ActionResultStatusV1
	}{
		{
			name: "success available continues to model two",
			input: func(action ActionDispatchRecord, lease RunLease) CommitActionDispatchOutcomeInput {
				return actionOutcomeInput(
					action,
					lease,
					moduleapi.ActionExecutionSucceeded,
				)
			},
			wantAction:   ActionDispatchSucceeded,
			wantStep:     corecontract.ModelReadyAfterActionLoopStep,
			wantRunState: corecontract.InitialRunState,
			wantResult:   corecontract.ActionResultAvailable,
		},
		{
			name: "failure terminates",
			input: func(action ActionDispatchRecord, lease RunLease) CommitActionDispatchOutcomeInput {
				return actionOutcomeInput(
					action,
					lease,
					moduleapi.ActionExecutionFailed,
				)
			},
			wantAction:      ActionDispatchFailed,
			wantStep:        corecontract.TerminatedLoopStep,
			wantRunState:    corecontract.TerminatedLoopStep,
			wantDisposition: corecontract.TerminatedLoopStep,
		},
		{
			name: "unknown waits for reconciliation",
			input: func(action ActionDispatchRecord, lease RunLease) CommitActionDispatchOutcomeInput {
				return actionOutcomeInput(
					action,
					lease,
					moduleapi.ActionExecutionUnknown,
				)
			},
			wantAction:      ActionDispatchUnknown,
			wantStep:        corecontract.WaitingReconciliationLoopStep,
			wantRunState:    corecontract.WaitingReconciliationLoopStep,
			wantDisposition: corecontract.WaitingReconciliationLoopStep,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newActionStoreHarness(t)
			begin := harness.beginAction(t, "action-attempt-outcome")
			result, err := harness.store.CommitActionDispatchOutcome(
				context.Background(),
				test.input(begin.Action, begin.Lease),
			)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Applied || result.Record.Attempt.State != test.wantAction {
				t.Fatalf("Action outcome = %+v", result)
			}
			run, err := harness.store.LoadRunForLoop(context.Background(), result.Lease)
			if err != nil {
				t.Fatal(err)
			}
			if run.Frame.Step != test.wantStep || run.State != test.wantRunState ||
				run.Disposition != test.wantDisposition ||
				run.Frame.PendingAttemptID != "" ||
				run.Frame.PendingDispatchAttemptID != "" {
				t.Fatalf("projected Run = %+v", run)
			}
			if test.wantResult != "" {
				if result.Record.Result == nil {
					t.Fatal("successful Action lacks persisted result")
				}
				restored, restoreErr := corecontract.RestoreActionResultV1(
					result.Record.Result.CanonicalBytes,
					result.Record.Result.Digest,
					harness.definition,
				)
				if restoreErr != nil || restored.Status != test.wantResult {
					t.Fatalf("restored Action result=%+v error=%v", restored, restoreErr)
				}
			} else if result.Record.Result != nil {
				t.Fatalf("non-success Action exposed result: %+v", result.Record.Result)
			}
		})
	}
}

func TestCommitActionDispatchOutcomeIsExactAndRejectsIdentityDrift(
	t *testing.T,
) {
	harness := newActionStoreHarness(t)
	begin := harness.beginAction(t, "action-attempt-idempotent")
	input := actionOutcomeInput(
		begin.Action,
		begin.Lease,
		moduleapi.ActionExecutionSucceeded,
	)
	committed, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Applied || committed.Record.ProviderReceipt == nil ||
		!bytes.Equal(
			committed.Record.ProviderReceipt.CanonicalBytes,
			input.ProviderReceiptCanonical,
		) {
		t.Fatalf("committed Action outcome = %+v", committed)
	}
	retry, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("exact terminal retry: %v", err)
	}
	if retry.Applied || retry.Record.Attempt.Revision !=
		committed.Record.Attempt.Revision || retry.Lease != committed.Lease ||
		retry.Record.ProviderReceipt == nil ||
		!bytes.Equal(
			retry.Record.ProviderReceipt.CanonicalBytes,
			input.ProviderReceiptCanonical,
		) {
		t.Fatalf("exact terminal retry = %+v", retry)
	}

	conflicts := []struct {
		name   string
		mutate func(*CommitActionDispatchOutcomeInput)
	}{
		{
			name: "different terminal state",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				*value = actionOutcomeInput(
					committed.Record,
					committed.Lease,
					moduleapi.ActionExecutionFailed,
				)
			},
		},
		{
			name: "different receipt",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				value.Lease = committed.Lease
				value.ExpectedAttemptRevision = committed.Record.Attempt.Revision
				value.ProviderReceiptCanonical = []byte(`{"receipt_id":"different"}`)
			},
		},
		{
			name: "different external operation identity",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				value.Lease = committed.Lease
				value.ExpectedAttemptRevision = committed.Record.Attempt.Revision
				value.ExternalOperationID = "different-operation"
			},
		},
		{
			name: "different provider",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				value.Lease = committed.Lease
				value.ExpectedAttemptRevision = committed.Record.Attempt.Revision
				value.Provider.InstanceID = "different-action-provider"
			},
		},
		{
			name: "different invocation",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				value.Lease = committed.Lease
				value.ExpectedAttemptRevision = committed.Record.Attempt.Revision
				value.InvocationID = "different-invocation"
			},
		},
		{
			name: "different Attempt identity",
			mutate: func(value *CommitActionDispatchOutcomeInput) {
				value.Lease = committed.Lease
				value.ExpectedAttemptRevision = committed.Record.Attempt.Revision
				value.AttemptID = "different-action-attempt"
				value.InvocationID = value.AttemptID
			},
		},
	}
	for _, test := range conflicts {
		t.Run(test.name, func(t *testing.T) {
			changed := input
			test.mutate(&changed)
			if _, err := harness.store.CommitActionDispatchOutcome(
				context.Background(),
				changed,
			); err == nil {
				t.Fatal("outcome identity drift was accepted")
			}
			stored, err := harness.store.GetActionDispatchRecord(
				context.Background(),
				committed.Record.Attempt.AttemptID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Attempt.State != ActionDispatchSucceeded ||
				stored.Attempt.Revision != committed.Record.Attempt.Revision ||
				stored.ProviderReceipt == nil ||
				!bytes.Equal(
					stored.ProviderReceipt.CanonicalBytes,
					input.ProviderReceiptCanonical,
				) {
				t.Fatalf("conflict changed persisted outcome: %+v", stored)
			}
		})
	}
	assertActionAttemptCount(t, harness.store, 1)
}

func TestReconcileActionUnknownCASesOriginalAttemptWithoutReplay(t *testing.T) {
	harness := newActionStoreHarness(t)
	originalBeginInput := harness.actionBeginInput(t, "action-attempt-reconcile")
	begin, err := harness.store.CommitModelActionAndBeginDispatch(
		context.Background(),
		originalBeginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		actionOutcomeInput(
			begin.Action,
			begin.Lease,
			moduleapi.ActionExecutionUnknown,
		),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Re-entering the original model-to-Action transaction can only reopen
	// the same UNKNOWN row; it never regenerates a process-local permit.
	reopened, err := harness.store.CommitModelActionAndBeginDispatch(
		context.Background(),
		originalBeginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Applied || reopened.GatewayAllowed ||
		reopened.ConsumeActionGatewayPermit() ||
		reopened.Action.Attempt.AttemptID != begin.Action.Attempt.AttemptID ||
		reopened.Action.Attempt.State != ActionDispatchUnknown {
		t.Fatalf("UNKNOWN re-entry = %+v", reopened)
	}

	stale := actionOutcomeInput(
		unknown.Record,
		unknown.Lease,
		moduleapi.ActionExecutionSucceeded,
	)
	stale.ExpectedAttemptRevision = begin.Action.Attempt.Revision
	stale.ReconciliationEvidenceCanonical = []byte(
		`{"kind":"provider_lookup","status":"completed"}`,
	)
	if _, err := harness.store.ReconcileActionDispatchOutcome(
		context.Background(),
		ReconcileActionDispatchOutcomeInput(stale),
	); !errors.Is(err, ErrActionDispatchConflict) {
		t.Fatalf("stale reconciliation error=%v", err)
	}
	replacement := stale
	replacement.ExpectedAttemptRevision = unknown.Record.Attempt.Revision
	replacement.AttemptID = "replacement-action-attempt"
	replacement.InvocationID = replacement.AttemptID
	if _, err := harness.store.ReconcileActionDispatchOutcome(
		context.Background(),
		ReconcileActionDispatchOutcomeInput(replacement),
	); err == nil {
		t.Fatal("replacement reconciliation Attempt was accepted")
	}
	assertActionAttemptCount(t, harness.store, 1)

	reconcile := actionOutcomeInput(
		unknown.Record,
		unknown.Lease,
		moduleapi.ActionExecutionSucceeded,
	)
	reconcile.ReconciliationEvidenceCanonical = []byte(
		`{"kind":"provider_lookup","status":"completed"}`,
	)
	resolved, err := harness.store.ReconcileActionDispatchOutcome(
		context.Background(),
		ReconcileActionDispatchOutcomeInput(reconcile),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Applied ||
		resolved.Record.Attempt.AttemptID != begin.Action.Attempt.AttemptID ||
		resolved.Record.Attempt.State != ActionDispatchSucceeded ||
		resolved.Record.Attempt.ReconciliationEvidenceRef == "" {
		t.Fatalf("resolved original Action = %+v", resolved)
	}
	assertActionAttemptCount(t, harness.store, 1)

	different := harness.actionBeginInput(t, "replacement-action-attempt")
	different.Lease = resolved.Lease
	if _, err := harness.store.CommitModelActionAndBeginDispatch(
		context.Background(),
		different,
	); !errors.Is(err, ErrActionDispatchConflict) {
		t.Fatalf("replacement Action identity error=%v", err)
	}
	assertActionAttemptCount(t, harness.store, 1)
}

func TestReconcileActionUnknownCanCASOriginalAttemptToFailure(t *testing.T) {
	harness := newActionStoreHarness(t)
	begin := harness.beginAction(t, "action-attempt-reconcile-failed")
	unknown, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		actionOutcomeInput(
			begin.Action,
			begin.Lease,
			moduleapi.ActionExecutionUnknown,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	reconcile := actionOutcomeInput(
		unknown.Record,
		unknown.Lease,
		moduleapi.ActionExecutionFailed,
	)
	reconcile.ReconciliationEvidenceCanonical = []byte(
		`{"kind":"provider_lookup","status":"failed"}`,
	)
	failed, err := harness.store.ReconcileActionDispatchOutcome(
		context.Background(),
		ReconcileActionDispatchOutcomeInput(reconcile),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !failed.Applied ||
		failed.Record.Attempt.AttemptID != begin.Action.Attempt.AttemptID ||
		failed.Record.Attempt.State != ActionDispatchFailed ||
		failed.Record.Attempt.ErrorClassification != "EXECUTION_FAILED" ||
		failed.Record.Attempt.ReconciliationEvidenceRef == "" ||
		failed.Record.ReconciliationEvidence == nil ||
		!bytes.Equal(
			failed.Record.ReconciliationEvidence.CanonicalBytes,
			reconcile.ReconciliationEvidenceCanonical,
		) {
		t.Fatalf("failed reconciliation = %+v", failed)
	}
	retry, err := harness.store.ReconcileActionDispatchOutcome(
		context.Background(),
		ReconcileActionDispatchOutcomeInput(reconcile),
	)
	if err != nil {
		t.Fatalf("exact failed reconciliation retry: %v", err)
	}
	if retry.Applied || retry.Record.Attempt.AttemptID !=
		begin.Action.Attempt.AttemptID ||
		retry.Record.Attempt.State != ActionDispatchFailed {
		t.Fatalf("failed reconciliation retry = %+v", retry)
	}
	run, err := harness.store.LoadRunForLoop(context.Background(), failed.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != corecontract.TerminatedLoopStep ||
		run.Disposition != corecontract.TerminatedLoopStep ||
		run.Frame.Step != corecontract.TerminatedLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != "" {
		t.Fatalf("failed reconciliation Run = %+v", run)
	}
	terminal, err := harness.store.GetTerminalRunResult(
		context.Background(),
		run.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.AttemptKind != corecontract.AttemptKindAction ||
		terminal.AttemptID != begin.Action.Attempt.AttemptID ||
		terminal.ActionState != ActionDispatchFailed ||
		terminal.ErrorClassification != "EXECUTION_FAILED" {
		t.Fatalf("failed reconciliation terminal = %+v", terminal)
	}
	assertActionAttemptCount(t, harness.store, 1)
}

func TestSecondModelDispatchClosesSuccessfulActionRun(t *testing.T) {
	harness := newActionStoreHarness(t)
	actionBegin := harness.beginAction(t, "action-attempt-model-2")
	actionDone, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		actionOutcomeInput(
			actionBegin.Action,
			actionBegin.Lease,
			moduleapi.ActionExecutionSucceeded,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	modelTwoInput := harness.secondModelInput(t, actionDone)
	modelTwo, err := harness.store.BeginModelDispatch(
		context.Background(),
		modelTwoInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelTwo.Created || !modelTwo.InvokeAllowed ||
		modelTwo.Attempt.LogicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		modelTwo.Attempt.SourceDispatchAttemptID !=
			actionBegin.Action.Attempt.AttemptID ||
		modelTwo.Attempt.ContextCompilation != nil {
		t.Fatalf("model-2 begin = %+v", modelTwo)
	}
	if !modelTwo.ConsumeModelInvocationPermit() ||
		modelTwo.ConsumeModelInvocationPermit() {
		t.Fatal("model-2 permit is not exactly one-shot")
	}

	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "The text statistics are ready.",
			ProviderRequestID: "provider-request-model-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   modelTwo.Lease,
			AttemptID:               modelTwo.Attempt.AttemptID,
			InvocationID:            modelTwo.Attempt.AttemptID,
			Provider:                modelTwo.Attempt.Binding.Provider,
			ExpectedAttemptRevision: modelTwo.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t,
				`{"id":"provider-request-model-2","status":"completed"}`,
			),
			ProviderRequestID: "provider-request-model-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Record.Usage.LedgerSequence == nil ||
		*terminal.Record.Usage.LedgerSequence != 2 {
		t.Fatalf("model-2 Usage = %+v", terminal.Record.Usage)
	}
	run, err := harness.store.LoadRunForLoop(context.Background(), terminal.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != corecontract.TerminatedLoopStep ||
		run.Disposition != corecontract.TerminatedLoopStep ||
		run.Frame.Step != corecontract.TerminatedLoopStep ||
		len(run.ModelDispatches) != 2 || len(run.ActionDispatches) != 1 ||
		len(run.History) != 1 ||
		run.History[0].SourceAttemptID != modelTwo.Attempt.AttemptID {
		t.Fatalf("terminal Action Run = %+v", run)
	}
	result, err := harness.store.GetTerminalRunResult(
		context.Background(),
		run.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AttemptKind != corecontract.AttemptKindModel ||
		result.AttemptID != modelTwo.Attempt.AttemptID ||
		result.ModelState != corecontract.ModelAttemptSucceeded ||
		!bytes.Equal(result.OutputCanonical, outputCanonical) {
		t.Fatalf("terminal result = %+v", result)
	}
}

type actionStoreHarness struct {
	store          *Store
	tenantID       string
	basis          controlcontract.PublishedBasis
	definition     corecontract.FrozenActionDefinitionV1
	actionProvider moduleapi.ActivatedModuleRef
	modelBegin     BeginModelDispatchResult
	modelOutput    []byte
	usageReceipt   []byte
	proposal       []byte
}

type fixedActionMaterializer struct {
	definition corecontract.FrozenActionDefinitionV1
}

func (materializer fixedActionMaterializer) Materialize(
	_ context.Context,
	_ actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	return []corecontract.FrozenActionDefinitionV1{materializer.definition}, nil
}

func newActionStoreHarness(t *testing.T) *actionStoreHarness {
	t.Helper()
	ctx := context.Background()
	fixture := newAdmissionCommitFixture(t)
	store := fixture.store
	actionPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
	actionProvider := installActionStoreProvider(t, store, fixture.intent.TenantID)
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "text.stats",
				ProviderActionID: "builtin.text.stats",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   1024,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 fixture.intent.TenantID,
			AllowedWorkspaceIDs:      []string{fixture.intent.WorkspaceID},
			AllowedProviderActionIDs: []string{"builtin.text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef := putPublicationJSON(t, store, ContentConfig, configCanonical)
	authorityRef := putPublicationJSON(
		t,
		store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)

	basis, control, catalog, err := store.LoadPublishedBasis(
		ctx,
		fixture.intent.TenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	actionContextPolicy := putPublicationContextPolicyWithLimits(
		t,
		store,
		20_000,
		0,
	)
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != fixture.intent.ProfileID {
			continue
		}
		modelBindings := make([]controlcontract.BindingSpec, 0, 2)
		for _, binding := range control.Profiles[index].Bindings {
			if binding.Port.Name == moduleapi.PortNameModelGenerate {
				modelBindings = append(modelBindings, binding)
			}
		}
		modelBindings = append(modelBindings, controlcontract.BindingSpec{
			Port:                actionPort,
			InstanceID:          actionProvider.InstanceID,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			FailurePolicy:       moduleapi.FailureRequired,
		})
		control.Profiles[index].ContextPolicy = actionContextPolicy
		control.Profiles[index].Bindings = modelBindings
	}
	control.SnapshotID = "control-action-store"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	modelEntry, found := catalog.FindInstance(storeModelInstanceID(t, fixture))
	if !found {
		t.Fatal("model Catalog entry not found")
	}
	catalog.GenerationID = "catalog-action-store"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Entries = []controlcontract.CatalogEntry{
		modelEntry,
		{
			Activation: actionProvider,
			Provides:   []moduleapi.PortRef{actionPort},
		},
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err = store.PublishControlCatalog(
		ctx,
		PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	schema, err := moduleapi.CanonicalizeActionInputSchemaV1(
		json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text.stats",
			BindingIndex:     0,
			Description:      "Count deterministic text statistics.",
			InputSchema:      schema,
			EffectClass:      moduleapi.EffectNone,
			MaxResultBytes:   1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.intent.Deadline = time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(fixture.intent)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-action-store",
			MemberID:         "member-primary",
			RecoveryRootRef:  "recovery/run-action-store",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
			ActionMaterializer: fixedActionMaterializer{
				definition: definition,
			},
			ActionBindingMaterials: []actionmaterializer.BindingMaterialV1{{
				ConfigCanonical:    configCanonical,
				AuthorityCanonical: authorityCanonical,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := store.CommitRunAdmission(
		ctx,
		CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents:                []ContentInput{fixture.task},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutModelPriceSnapshot(ctx, testModelPriceSnapshot()); err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireRunLease(
		ctx,
		AcquireRunLeaseInput{
			RunID:                 admission.RunID,
			OwnerID:               "action-store-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   90 * time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	requestCanonical, compilationCanonical := compileActionModelOne(
		t,
		store,
		run,
	)
	modelBegin, err := store.BeginModelDispatch(
		ctx,
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "model-attempt-1",
			LogicalStepID:               corecontract.FirstModelLogicalStepIDV1,
			ContextCompilationCanonical: compilationCanonical,
			RequestCanonical:            requestCanonical,
			Deadline: time.Now().UTC().Add(time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelBegin.ConsumeModelInvocationPermit() {
		t.Fatal("model-1 fixture did not receive invocation permit")
	}
	_, modelOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID:       definition.PublicActionID,
				CanonicalInput: json.RawMessage(`{}`),
			},
			ProviderRequestID: "provider-request-model-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, proposal, _, err := corecontract.NewActionProposalV1(
		compiled.MemberSnapshot.MemberSnapshotDigest,
		definition,
		json.RawMessage(`{}`),
		json.RawMessage(`{"prepared":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &actionStoreHarness{
		store:          store,
		tenantID:       fixture.intent.TenantID,
		basis:          basis,
		definition:     definition,
		actionProvider: actionProvider,
		modelBegin:     modelBegin,
		modelOutput:    modelOutput,
		usageReceipt: modelUsageOutcomeCanonical(
			t,
			`{"id":"provider-request-model-1","status":"completed"}`,
		),
		proposal: proposal,
	}
}

func storeModelInstanceID(
	t *testing.T,
	fixture *admissionCommitFixture,
) string {
	t.Helper()
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, provider := exactTestModelProvider(t, member)
	return provider.InstanceID
}

func installActionStoreProvider(
	t *testing.T,
	store *Store,
	tenantID string,
) moduleapi.ActivatedModuleRef {
	t.Helper()
	manifest := canonicalModuleManifest(
		t,
		"test.action.store",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"provides": []any{map[string]any{
				"name":          moduleapi.PortNameActionProvider,
				"exact_version": moduleapi.PortVersionV1,
			}},
		},
	)
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-action-store",
			manifest,
			strings.Repeat("9", 64),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-action-store",
			TenantID:           tenantID,
			InstanceID:         "instance-action-store",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "builtin.action.store.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
}

func compileActionModelOne(
	t *testing.T,
	store *Store,
	run RunForLoop,
) ([]byte, []byte) {
	t.Helper()
	contextPolicy, err := store.GetContent(
		context.Background(),
		run.Member.ContextPolicy.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.GetContent(context.Background(), run.Manifest.TaskInputRef)
	if err != nil {
		t.Fatal(err)
	}
	modelBinding, err := exactModelBinding(run.Member)
	if err != nil {
		t.Fatal(err)
	}
	modelConfigRecord, err := store.GetContent(
		context.Background(),
		modelBinding.ConfigRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(
		modelConfigRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: contextPolicy.CanonicalBytes,
		ModelProfileRef:                run.Member.ModelProfile,
		ModelParameters:                modelConfig.Parameters,
		Actions:                        run.Member.Actions,
		TaskInputRef:                   run.Manifest.TaskInputRef,
		TaskInputCanonical:             task.CanonicalBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Compilation == nil ||
		compiled.Compilation.ActionResultReservation == nil ||
		len(compiled.Request.Actions) != 1 {
		t.Fatalf("Action model-1 compilation = %+v", compiled)
	}
	return compiled.RequestCanonical, compiled.CompilationCanonical
}

func (harness *actionStoreHarness) actionBeginInput(
	t *testing.T,
	attemptID string,
) CommitModelActionAndBeginDispatchInput {
	t.Helper()
	return CommitModelActionAndBeginDispatchInput{
		Lease:                        harness.modelBegin.Lease,
		ModelAttemptID:               harness.modelBegin.Attempt.AttemptID,
		InvocationID:                 harness.modelBegin.Attempt.AttemptID,
		Provider:                     harness.modelBegin.Attempt.Binding.Provider,
		ExpectedModelAttemptRevision: harness.modelBegin.Attempt.Revision,
		OutputCanonical:              bytes.Clone(harness.modelOutput),
		UsageReceiptCanonical:        bytes.Clone(harness.usageReceipt),
		ProviderRequestID:            "provider-request-model-1",
		DispatchAttemptID:            attemptID,
		ProposalCanonical:            bytes.Clone(harness.proposal),
		Deadline: time.Now().UTC().Add(30 * time.Minute).
			Truncate(time.Microsecond),
		BudgetDecision: ActionBudgetAllow,
	}
}

func (harness *actionStoreHarness) beginAction(
	t *testing.T,
	attemptID string,
) CommitModelActionAndBeginDispatchResult {
	t.Helper()
	result, err := harness.store.CommitModelActionAndBeginDispatch(
		context.Background(),
		harness.actionBeginInput(t, attemptID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.GatewayAllowed {
		t.Fatalf("Action fixture begin = %+v", result)
	}
	return result
}

func actionOutcomeInput(
	action ActionDispatchRecord,
	lease RunLease,
	outcome moduleapi.ActionExecutionOutcomeV1,
) CommitActionDispatchOutcomeInput {
	input := CommitActionDispatchOutcomeInput{
		Lease:                   lease,
		AttemptID:               action.Attempt.AttemptID,
		InvocationID:            action.Attempt.AttemptID,
		Provider:                action.Attempt.Binding.Provider,
		ExpectedAttemptRevision: action.Attempt.Revision,
		Outcome:                 outcome,
	}
	switch outcome {
	case moduleapi.ActionExecutionSucceeded:
		input.CanonicalResult = []byte(`{"bytes":5,"words":1}`)
		input.ProviderReceiptCanonical = []byte(`{"receipt_id":"receipt-1"}`)
		input.ExternalOperationID = "operation-1"
	case moduleapi.ActionExecutionFailed:
		input.ProviderReceiptCanonical = []byte(`{"receipt_id":"receipt-1"}`)
		input.ExternalOperationID = "operation-1"
		input.ErrorClassification = "EXECUTION_FAILED"
	case moduleapi.ActionExecutionUnknown:
		input.ProviderReceiptCanonical = []byte(`{"receipt_id":"receipt-1"}`)
		input.ExternalOperationID = "operation-1"
		input.UnknownReason = "TRANSPORT_TIMEOUT"
	}
	return input
}

func (harness *actionStoreHarness) secondModelInput(
	t *testing.T,
	actionDone CommitActionDispatchOutcomeResult,
) BeginModelDispatchInput {
	t.Helper()
	if actionDone.Record.Result == nil {
		t.Fatal("model-2 fixture lacks Action result")
	}
	result, err := corecontract.RestoreActionResultV1(
		actionDone.Record.Result.CanonicalBytes,
		actionDone.Record.Result.Digest,
		harness.definition,
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		result,
		harness.definition,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelOne, err := moduleapi.RestoreModelGenerateRequestV1(
		harness.modelBegin.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelOne.Messages = append(modelOne.Messages, envelope)
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(modelOne)
	if err != nil {
		t.Fatal(err)
	}
	return BeginModelDispatchInput{
		Lease:            actionDone.Lease,
		AttemptID:        "model-attempt-2",
		LogicalStepID:    corecontract.SecondModelLogicalStepIDV1,
		RequestCanonical: requestCanonical,
		Deadline: time.Now().UTC().Add(20 * time.Minute).
			Truncate(time.Microsecond),
	}
}

func assertActionAttemptCount(t *testing.T, store *Store, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM dispatch_attempts`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Action Attempt count=%d want %d", got, want)
	}
}

type actionBeginRollbackSnapshot struct {
	ActionAttempts   int
	UsageRows        int
	RunEvents        int
	ActionProposals  int
	ModelResults     int
	ProviderReceipts int
}

func actionBeginRollbackSnapshotForRun(
	t *testing.T,
	store *Store,
	runID string,
) actionBeginRollbackSnapshot {
	t.Helper()
	var snapshot actionBeginRollbackSnapshot
	if err := store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM model_usage WHERE run_id=?),
			(SELECT COUNT(*) FROM run_events WHERE run_id=?),
			(SELECT COUNT(*) FROM content_records WHERE kind='ACTION_PROPOSAL'),
			(SELECT COUNT(*) FROM content_records WHERE kind='MODEL_RESULT'),
			(SELECT COUNT(*) FROM content_records WHERE kind='PROVIDER_RECEIPT')
	`, runID, runID, runID).Scan(
		&snapshot.ActionAttempts,
		&snapshot.UsageRows,
		&snapshot.RunEvents,
		&snapshot.ActionProposals,
		&snapshot.ModelResults,
		&snapshot.ProviderReceipts,
	); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
