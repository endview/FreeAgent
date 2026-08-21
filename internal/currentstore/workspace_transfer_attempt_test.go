package currentstore

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestWorkspaceTransferBeginPersistsOneAtomicRequestAndExactRetryReadsIt(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	input := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"attempt-transfer-request",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	before, err := fixture.store.LoadRunForLoop(context.Background(), input.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if before.WorkspaceTransfer == nil {
		t.Fatal("cross-Workspace Child lacks Host transfer material")
	}
	if _, exposed := before.FindContent(before.Manifest.TaskInputRef); exposed {
		t.Fatal("cross-Workspace Child exposed original TASK_INPUT in generic Contents")
	}
	preview, err := PrepareWorkspaceTransferRequestV1(before)
	if err != nil || preview == nil {
		t.Fatalf("PrepareWorkspaceTransferRequestV1=%+v error=%v", preview, err)
	}

	first, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.InvokeAllowed ||
		first.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("first Begin=%+v", first)
	}
	assertWorkspaceTransferContentKindCount(
		t,
		fixture.store,
		ContentWorkspaceTransferPayload,
		1,
	)
	assertWorkspaceTransferContentKindCount(
		t,
		fixture.store,
		ContentWorkspaceTransferEnvelope,
		1,
	)
	compilation, err := corecontract.RestoreContextCompilationV1(
		first.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || len(compilation.WorkspaceTransfers) != 1 {
		t.Fatalf("ContextCompilation transfer=%+v error=%v", compilation.WorkspaceTransfers, err)
	}
	evidence := compilation.WorkspaceTransfers[0]
	if evidence.Direction != corecontract.WorkspaceTransferDirectionRequestV1 ||
		evidence.PayloadKind != corecontract.WorkspaceTransferPayloadTaskSummaryV1 ||
		evidence.EnvelopeRef != preview.EnvelopeRef ||
		evidence.EnvelopeDigest != preview.EnvelopeDigest ||
		evidence.PayloadRef != preview.Payload.Digest {
		t.Fatalf("REQUEST evidence=%+v preview=%+v", evidence, preview)
	}

	retry, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created || retry.InvokeAllowed || retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID ||
		retry.Lease != first.Lease {
		t.Fatalf("exact retry=%+v first=%+v", retry, first)
	}
	assertWorkspaceTransferContentKindCount(
		t,
		fixture.store,
		ContentWorkspaceTransferPayload,
		1,
	)
	assertWorkspaceTransferContentKindCount(
		t,
		fixture.store,
		ContentWorkspaceTransferEnvelope,
		1,
	)
}

func TestWorkspaceTransferConcurrentBeginCreatesOneAttemptAndEnvelope(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	input := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"attempt-transfer-concurrent",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	type outcome struct {
		result BeginModelDispatchResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			)
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	created := 0
	invocationGrants := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Begin: %v", result.err)
		}
		if result.result.Created {
			created++
		}
		if result.result.InvokeAllowed {
			invocationGrants++
		}
	}
	if created != 1 || invocationGrants != 1 {
		t.Fatalf("created=%d invocation grants=%d", created, invocationGrants)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferPayload, 1)
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
}

func TestWorkspaceTransferBeginFailureRollsBackPayloadEnvelopeAndAttempt(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	input := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"attempt-transfer-begin-rollback",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER fail_workspace_transfer_attempt
		BEFORE INSERT ON model_dispatch_attempts
		WHEN NEW.attempt_id='attempt-transfer-begin-rollback'
		BEGIN SELECT RAISE(ABORT, 'forced transfer Attempt failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BeginModelDispatch(context.Background(), input); err == nil {
		t.Fatal("fault-injected transfer Begin succeeded")
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferPayload, 0)
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 0)
	var attemptCount int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM model_dispatch_attempts WHERE attempt_id=?`,
		input.AttemptID,
	).Scan(&attemptCount); err != nil || attemptCount != 0 {
		t.Fatalf("Attempt count=%d error=%v", attemptCount, err)
	}
	if _, err := fixture.store.db.Exec(`DROP TRIGGER fail_workspace_transfer_attempt`); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); err != nil || !result.Created || !result.InvokeAllowed {
		t.Fatalf("Begin after rollback=%+v error=%v", result, err)
	}
}

func TestWorkspaceTransferExpiredBeginKeepsRequestClosureWithoutInvocation(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	input := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"attempt-transfer-expired",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	input.Deadline = time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	result, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil || !result.Created || result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() ||
		result.Attempt.State != corecontract.ModelAttemptFailed ||
		result.Attempt.ErrorClassification !=
			modelDeadlineExpiredBeforeDispatchClassification {
		t.Fatalf("expired transfer Begin=%+v error=%v", result, err)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferPayload, 1)
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
	retry, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil || retry.Created || retry.InvokeAllowed {
		t.Fatalf("expired transfer retry=%+v error=%v", retry, err)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
}

func TestWorkspaceTransferSuccessfulOutcomePersistsOneResultEnvelopeAndReplaysExactly(
	t *testing.T,
) {
	fixture, begin, input := beginWorkspaceTransferSpecialist(
		t,
		"success",
	)
	output, usage := workspaceTransferSpecialistOutcomeCanonical(t, "success")
	commit := workspaceTransferSuccessCommitInput(begin, output, usage)
	result, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		commit,
	)
	if err != nil || !result.Applied {
		t.Fatalf("Commit result=%+v error=%v", result, err)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferPayload, 1)
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 2)

	run, err := fixture.store.LoadRunForLoop(context.Background(), result.Lease)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := fixture.store.GetContent(
		context.Background(),
		result.Record.Attempt.ResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := loadWorkspaceTransferResultV1(
		context.Background(),
		fixture.store.db,
		run,
		payload,
	)
	if err != nil || transfer == nil ||
		transfer.Envelope.Direction != corecontract.WorkspaceTransferDirectionResultV1 ||
		transfer.Envelope.PayloadRef != result.Record.Attempt.ResultRef {
		t.Fatalf("RESULT transfer=%+v error=%v", transfer, err)
	}

	replayed, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		commit,
	)
	if err != nil || replayed.Applied || replayed.Lease != result.Lease {
		t.Fatalf("outcome replay=%+v error=%v", replayed, err)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 2)

	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"transfer-result-root-reader",
	)
	root, err := fixture.store.LoadRunForLoop(context.Background(), rootLease)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.CompositeChildren) != 2 ||
		root.CompositeChildren[0].RunID != input.Lease.RunID ||
		root.CompositeChildren[0].WorkspaceTransfer == nil ||
		root.CompositeChildren[0].WorkspaceTransfer.EnvelopeRef != transfer.EnvelopeRef {
		t.Fatalf("Root transfer projection=%+v", root.CompositeChildren)
	}
}

func TestWorkspaceTransferOutcomeRollbackDoesNotLeakResultEnvelope(
	t *testing.T,
) {
	fixture, begin, _ := beginWorkspaceTransferSpecialist(t, "rollback")
	output, usage := workspaceTransferSpecialistOutcomeCanonical(t, "rollback")
	commit := workspaceTransferSuccessCommitInput(begin, output, usage)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER fail_workspace_transfer_history
		BEFORE INSERT ON history_entries
		WHEN NEW.source_attempt_id='attempt-transfer-rollback'
		BEGIN SELECT RAISE(ABORT, 'forced transfer History failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		commit,
	); err == nil {
		t.Fatal("fault-injected transfer outcome succeeded")
	}
	record, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil || record.Attempt.State != corecontract.ModelAttemptPending ||
		record.Attempt.ResultRef != "" {
		t.Fatalf("post-rollback record=%+v error=%v", record, err)
	}
	assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
	var resultCount int
	if err := fixture.store.db.QueryRow(
		`SELECT COUNT(*) FROM content_records WHERE kind=?`,
		string(ContentModelResult),
	).Scan(&resultCount); err != nil || resultCount != 0 {
		t.Fatalf("MODEL_RESULT count=%d error=%v", resultCount, err)
	}
	if _, err := fixture.store.db.Exec(`DROP TRIGGER fail_workspace_transfer_history`); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		commit,
	); err != nil || !result.Applied {
		t.Fatalf("Commit after rollback=%+v error=%v", result, err)
	}
}

func TestWorkspaceTransferUnknownAndInvalidNeverCreateResultEnvelopeOrReplay(
	t *testing.T,
) {
	t.Run("UNKNOWN", func(t *testing.T) {
		fixture, begin, beginInput := beginWorkspaceTransferSpecialist(t, "unknown")
		unknown, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   begin.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: begin.Attempt.Revision,
				State:                   corecontract.ModelAttemptUnknown,
				ProviderRequestID:       "provider-transfer-unknown",
				UnknownReason:           "ambiguous Workspace Specialist completion",
			},
		)
		if err != nil || !unknown.Applied {
			t.Fatalf("UNKNOWN=%+v error=%v", unknown, err)
		}
		assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
		beginInput.Lease = unknown.Lease
		retry, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		)
		if err != nil || retry.Created || retry.InvokeAllowed ||
			retry.ConsumeModelInvocationPermit() ||
			retry.Attempt.State != corecontract.ModelAttemptUnknown {
			t.Fatalf("UNKNOWN Begin retry=%+v error=%v", retry, err)
		}
		assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
	})

	t.Run("invalid successful wire", func(t *testing.T) {
		fixture, begin, _ := beginWorkspaceTransferSpecialist(t, "invalid")
		_, invalidOutput, err := moduleapi.NewModelGenerateOutputV1(
			moduleapi.ModelGenerateOutputV1{
				SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
				AssistantText:     "not a specialist-contribution/v1 object",
				ProviderRequestID: "provider-transfer-invalid",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		commit := workspaceTransferSuccessCommitInput(
			begin,
			invalidOutput,
			modelUsageOutcomeCanonical(t, `{"id":"provider-transfer-invalid","status":"completed"}`),
		)
		if _, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			commit,
		); err == nil {
			t.Fatal("invalid Specialist SUCCEEDED outcome was accepted")
		}
		record, err := fixture.store.GetModelDispatchRecord(
			context.Background(),
			begin.Attempt.AttemptID,
		)
		if err != nil || record.Attempt.State != corecontract.ModelAttemptPending {
			t.Fatalf("invalid outcome record=%+v error=%v", record, err)
		}
		assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
		failed, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   begin.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: begin.Attempt.Revision,
				State:                   corecontract.ModelAttemptFailed,
				UsageReceiptCanonical:   commit.UsageReceiptCanonical,
				ProviderRequestID:       "provider-transfer-invalid",
				ErrorClassification:     "COLLABORATION_SPECIALIST_OUTPUT_INVALID",
			},
		)
		if err != nil || !failed.Applied {
			t.Fatalf("normalized FAILED=%+v error=%v", failed, err)
		}
		assertWorkspaceTransferContentKindCount(t, fixture.store, ContentWorkspaceTransferEnvelope, 1)
	})
}

func TestWorkspaceTransferUnknownReconciliationClosesOriginalAttempt(
	t *testing.T,
) {
	tests := []struct {
		name          string
		suffix        string
		state         corecontract.ModelAttemptState
		wantEnvelopes int
		wantResult    bool
	}{
		{
			name:          "SUCCEEDED",
			suffix:        "reconcile-success",
			state:         corecontract.ModelAttemptSucceeded,
			wantEnvelopes: 2,
			wantResult:    true,
		},
		{
			name:          "FAILED",
			suffix:        "reconcile-failed",
			state:         corecontract.ModelAttemptFailed,
			wantEnvelopes: 1,
			wantResult:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, begin, beginInput := beginWorkspaceTransferSpecialist(
				t,
				test.suffix,
			)
			output, usage := workspaceTransferSpecialistOutcomeCanonical(
				t,
				test.suffix,
			)
			providerRequestID := "provider-transfer-" + test.suffix
			unknown, err := fixture.store.CommitModelDispatchOutcome(
				context.Background(),
				CommitModelDispatchOutcomeInput{
					Lease:                   begin.Lease,
					AttemptID:               begin.Attempt.AttemptID,
					InvocationID:            begin.Attempt.AttemptID,
					Provider:                begin.Attempt.Binding.Provider,
					ExpectedAttemptRevision: begin.Attempt.Revision,
					State:                   corecontract.ModelAttemptUnknown,
					ProviderRequestID:       providerRequestID,
					UnknownReason:           "provider result requires reliable reconciliation",
				},
			)
			if err != nil || !unknown.Applied ||
				unknown.Record.Attempt.AttemptID != begin.Attempt.AttemptID ||
				unknown.Record.Attempt.State != corecontract.ModelAttemptUnknown {
				t.Fatalf("commit transfer UNKNOWN=%+v error=%v", unknown, err)
			}
			assertWorkspaceTransferContentKindCount(
				t,
				fixture.store,
				ContentWorkspaceTransferEnvelope,
				1,
			)

			beginInput.Lease = unknown.Lease
			retry, err := fixture.store.BeginModelDispatch(
				context.Background(),
				beginInput,
			)
			if err != nil || retry.Created || retry.InvokeAllowed ||
				retry.ConsumeModelInvocationPermit() ||
				retry.Attempt.AttemptID != begin.Attempt.AttemptID ||
				retry.Attempt.State != corecontract.ModelAttemptUnknown {
				t.Fatalf("UNKNOWN exact Begin retry=%+v error=%v", retry, err)
			}

			reconcile := CommitModelDispatchOutcomeInput{
				Lease:                   unknown.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: unknown.Record.Attempt.Revision,
				State:                   test.state,
				UsageReceiptCanonical:   usage,
				ProviderRequestID:       providerRequestID,
				ReconciliationEvidenceCanonical: []byte(fmt.Sprintf(
					`{"attempt_id":%q,"kind":"provider_lookup","request_id":%q}`,
					begin.Attempt.AttemptID,
					providerRequestID,
				)),
			}
			if test.state == corecontract.ModelAttemptSucceeded {
				reconcile.OutputCanonical = output
			} else {
				reconcile.ErrorClassification = "PROVIDER_CONFIRMED_FAILED"
			}

			negative := []struct {
				name   string
				mutate func(*CommitModelDispatchOutcomeInput)
			}{
				{
					name: "without evidence",
					mutate: func(input *CommitModelDispatchOutcomeInput) {
						input.ReconciliationEvidenceCanonical = nil
					},
				},
				{
					name: "stale revision",
					mutate: func(input *CommitModelDispatchOutcomeInput) {
						input.ExpectedAttemptRevision--
					},
				},
				{
					name: "different Provider",
					mutate: func(input *CommitModelDispatchOutcomeInput) {
						input.Provider.InstanceID += "-replacement"
					},
				},
				{
					name: "different Attempt",
					mutate: func(input *CommitModelDispatchOutcomeInput) {
						input.AttemptID += "-replacement"
						input.InvocationID = input.AttemptID
					},
				},
			}
			for _, rejection := range negative {
				t.Run(rejection.name, func(t *testing.T) {
					candidate := reconcile
					rejection.mutate(&candidate)
					if result, err := fixture.store.CommitModelDispatchOutcome(
						context.Background(),
						candidate,
					); err == nil {
						t.Fatalf("invalid reconciliation succeeded: %+v", result)
					}
					stored, err := fixture.store.GetModelDispatchRecord(
						context.Background(),
						begin.Attempt.AttemptID,
					)
					if err != nil ||
						stored.Attempt.State != corecontract.ModelAttemptUnknown ||
						stored.Attempt.Revision != unknown.Record.Attempt.Revision ||
						stored.Attempt.ResultRef != "" ||
						stored.Attempt.ReconciliationEvidenceRef != "" {
						t.Fatalf(
							"rejected reconciliation changed original Attempt: %+v error=%v",
							stored,
							err,
						)
					}
					assertWorkspaceTransferContentKindCount(
						t,
						fixture.store,
						ContentWorkspaceTransferEnvelope,
						1,
					)
					assertWorkspaceTransferContentKindCount(
						t,
						fixture.store,
						ContentReconciliationEvidence,
						0,
					)
					assertWorkspaceTransferContentKindCount(
						t,
						fixture.store,
						ContentModelResult,
						0,
					)
					assertWorkspaceTransferContentKindCount(
						t,
						fixture.store,
						ContentProviderReceipt,
						0,
					)
				})
			}

			terminal, err := fixture.store.CommitModelDispatchOutcome(
				context.Background(),
				reconcile,
			)
			if err != nil || !terminal.Applied ||
				terminal.Record.Attempt.AttemptID != begin.Attempt.AttemptID ||
				terminal.Record.Attempt.State != test.state ||
				terminal.Record.Attempt.Revision !=
					unknown.Record.Attempt.Revision+1 ||
				terminal.Record.Attempt.ReconciliationEvidenceRef == "" {
				t.Fatalf("reconcile original transfer Attempt=%+v error=%v", terminal, err)
			}
			if (terminal.Record.Attempt.ResultRef != "") != test.wantResult {
				t.Fatalf(
					"terminal ResultRef=%q want result=%v",
					terminal.Record.Attempt.ResultRef,
					test.wantResult,
				)
			}
			assertWorkspaceTransferContentKindCount(
				t,
				fixture.store,
				ContentWorkspaceTransferEnvelope,
				test.wantEnvelopes,
			)
			assertWorkspaceTransferContentKindCount(
				t,
				fixture.store,
				ContentReconciliationEvidence,
				1,
			)
			wantResults := 0
			if test.wantResult {
				wantResults = 1
			}
			assertWorkspaceTransferContentKindCount(
				t,
				fixture.store,
				ContentModelResult,
				wantResults,
			)
			assertWorkspaceTransferContentKindCount(
				t,
				fixture.store,
				ContentProviderReceipt,
				1,
			)
		})
	}
}

func TestWorkspaceTransferRepairRequestRecompilesTaskAndClosesLineage(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture.compositeAdmissionFixture,
			child,
			fmt.Sprintf("transfer-initial-%d", index),
		)
	}
	affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
	finishDecisionReviewer(
		t,
		fixture.compositeAdmissionFixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"transfer-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"transfer-repair-root",
	)
	transition, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	)
	if err != nil || !transition.Applied || len(transition.ActivatedRunIDs) != 1 {
		t.Fatalf("repair transition=%+v error=%v", transition, err)
	}
	repair := fixture.compiled.Decision.RepairChildren[0]
	lease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		repair.RunManifest.RunID,
		"transfer-repair-specialist",
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	request, err := PrepareWorkspaceTransferRequestV1(run)
	if err != nil || request == nil {
		t.Fatalf("repair transfer request=%+v error=%v", request, err)
	}
	summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		request.Payload.CanonicalBytes,
	)
	if err != nil || summary.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		summary.PreviousSetDigest != run.WorkspaceTransfer.PreviousContributionSetDigest ||
		summary.VerdictRef != run.WorkspaceTransfer.RepairVerdictRef ||
		bytes.Equal(request.Payload.CanonicalBytes, run.WorkspaceTransfer.RepairBasisCanonical) {
		t.Fatalf("repair summary=%+v error=%v", summary, err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-transfer-repair",
			LogicalStepID:               corecontract.PureChatModelLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: repair.RunManifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("repair Begin=%+v error=%v", begin, err)
	}
	output, usage := workspaceTransferSpecialistOutcomeCanonical(t, "repair")
	if result, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		workspaceTransferSuccessCommitInput(begin, output, usage),
	); err != nil || !result.Applied {
		t.Fatalf("repair outcome=%+v error=%v", result, err)
	}
}

func beginWorkspaceTransferSpecialist(
	t *testing.T,
	suffix string,
) (*workspaceTransferStoreFixture, BeginModelDispatchResult, BeginModelDispatchInput) {
	t.Helper()
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	input := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"attempt-transfer-"+suffix,
		corecontract.PureChatModelLogicalStepIDV1,
	)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("Begin transfer Specialist=%+v error=%v", begin, err)
	}
	return fixture, begin, input
}

func workspaceTransferSpecialistOutcomeCanonical(
	t *testing.T,
	suffix string,
) ([]byte, []byte) {
	t.Helper()
	_, contributionCanonical, _, err :=
		corecontract.NewSpecialistContributionV1(
			corecontract.SpecialistContributionV1{
				SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
				Proposal:      "Workspace transfer proposal " + suffix,
				Evidence:      []corecontract.SpecialistEvidenceV1{},
				Assumptions:   []string{},
				Risks:         []string{},
				Conflicts:     []string{},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	providerRequestID := "provider-transfer-" + suffix
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(contributionCanonical),
			ProviderRequestID: providerRequestID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return outputCanonical, modelUsageOutcomeCanonical(
		t,
		fmt.Sprintf(`{"id":%q,"status":"completed"}`, providerRequestID),
	)
}

func workspaceTransferSuccessCommitInput(
	begin BeginModelDispatchResult,
	output []byte,
	usage []byte,
) CommitModelDispatchOutcomeInput {
	return CommitModelDispatchOutcomeInput{
		Lease:                   begin.Lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Attempt.Revision,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         output,
		UsageReceiptCanonical:   usage,
	}
}

func assertWorkspaceTransferContentKindCount(
	t *testing.T,
	store *Store,
	kind ContentKind,
	want int,
) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM content_records WHERE kind=?`,
		string(kind),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("content kind %s count=%d want=%d", kind, count, want)
	}
}
