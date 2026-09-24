package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeFamilyUsageProjectionDecisionAdmissionKeepsPhysicalFamily(
	t *testing.T,
) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	root := fixture.compiled.Parent.RunManifest
	before := usageProjectionStorageFingerprint(t, fixture.store)
	projection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		root.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if after := usageProjectionStorageFingerprint(t, fixture.store); after != before {
		t.Fatalf("Decision projection changed Store: before=%v after=%v", before, after)
	}
	assertDecisionUsageProjectionShape(t, fixture, projection, 0, 0)
}

func TestCompositeFamilyUsageProjectionDecisionApproveKeepsSkippedRepairs(
	t *testing.T,
) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(t, fixture, child, "usage-approve-child-"+string(rune('0'+index)))
	}
	finishDecisionReviewer(
		t,
		fixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionApproveV1,
		[]string{},
		"usage-approve-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"usage-approve-root",
	)
	transition, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	)
	if err != nil || !transition.Applied || len(transition.ActivatedRunIDs) != 0 ||
		len(transition.SkippedRunIDs) != 3 {
		t.Fatalf("APPROVE transition=%+v error=%v", transition, err)
	}
	finishDecisionUsageRoot(t, fixture, rootLease, "usage-approve-root")

	projection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertDecisionUsageProjectionShape(t, fixture, projection, 0, 4)
}

func TestCompositeFamilyUsageProjectionDecisionSubsetAndAllRepairAttempts(
	t *testing.T,
) {
	for _, test := range []struct {
		name          string
		affectedCount int
		wantAttempts  uint32
	}{
		{name: "subset", affectedCount: 1, wantAttempts: 6},
		{name: "all", affectedCount: 2, wantAttempts: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
			for index, child := range fixture.compiled.Children {
				finishDecisionSpecialist(
					t,
					fixture,
					child,
					"usage-repair-initial-"+string(rune('0'+index)),
				)
			}
			affected := make([]string, test.affectedCount)
			for index := range affected {
				affected[index] = fixture.compiled.Children[index].Assignment.SlotID
			}
			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Reviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionRepairRequiredV1,
				affected,
				"usage-repair-reviewer-zero",
			)
			rootLease := acquireCurrentDecisionLease(
				t,
				fixture.store,
				fixture.compiled.Parent.RunManifest.RunID,
				"usage-repair-root",
			)
			first, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || !first.Applied ||
				len(first.ActivatedRunIDs) != test.affectedCount {
				t.Fatalf("first repair transition=%+v error=%v", first, err)
			}
			for index := 0; index < test.affectedCount; index++ {
				finishDecisionSpecialist(
					t,
					fixture,
					fixture.compiled.Decision.RepairChildren[index],
					"usage-repair-child-"+string(rune('0'+index)),
				)
			}
			second, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || !second.Applied ||
				len(second.ActivatedRunIDs) != test.affectedCount+1 ||
				second.ActivatedRunIDs[len(second.ActivatedRunIDs)-1] != fixture.compiled.Decision.
					RepairReviewer.RunManifest.RunID {
				t.Fatalf("second repair transition=%+v error=%v", second, err)
			}
			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionApproveV1,
				[]string{},
				"usage-repair-reviewer-one",
			)
			finishDecisionUsageRoot(t, fixture, rootLease, "usage-repair-root")

			before := usageProjectionStorageFingerprint(t, fixture.store)
			projection, err := fixture.store.GetCompositeFamilyUsageProjection(
				context.Background(),
				fixture.compiled.Parent.RunManifest.RunID,
			)
			if err != nil {
				t.Fatal(err)
			}
			assertDecisionUsageProjectionShape(
				t,
				fixture,
				projection,
				test.affectedCount,
				test.wantAttempts,
			)
			if uint64Value(projection.Aggregate.TokenTotals.Input) !=
				uint64(test.wantAttempts)*10 {
				t.Fatalf("repair token aggregate=%+v", projection.Aggregate.TokenTotals)
			}
			retry, err := fixture.store.GetCompositeFamilyUsageProjection(
				context.Background(),
				fixture.compiled.Parent.RunManifest.RunID,
			)
			if err != nil || !reflect.DeepEqual(projection, retry) {
				t.Fatalf("Decision retry drifted: retry=%+v error=%v", retry, err)
			}
			if after := usageProjectionStorageFingerprint(t, fixture.store); after != before {
				t.Fatalf("Decision projection changed Store: before=%v after=%v", before, after)
			}
		})
	}
}

func TestCompositeFamilyUsageProjectionCompleteKnownSumsAndReadOnlyRetry(
	t *testing.T,
) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	first := commitCompositeUsageSuccess(
		t,
		fixture,
		0,
		"attempt-usage-complete-first",
		corecontract.PureChatModelLogicalStepIDV1,
		usageProjectionReceipt{
			Input: 10, Cached: 4, Uncached: 6, Output: 2, Reasoning: uint64Pointer(1),
		},
	)
	second := commitCompositeUsageSuccess(
		t,
		fixture,
		1,
		"attempt-usage-complete-second",
		corecontract.PureChatModelLogicalStepIDV1,
		usageProjectionReceipt{
			Input: 20, Cached: 5, Uncached: 15, Output: 3, Reasoning: uint64Pointer(2),
		},
	)

	before := usageProjectionStorageFingerprint(t, fixture.store)
	firstProjection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondProjection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	after := usageProjectionStorageFingerprint(t, fixture.store)
	if before != after {
		t.Fatalf("read projection changed Store: before=%v after=%v", before, after)
	}
	if !reflect.DeepEqual(firstProjection, secondProjection) {
		t.Fatalf("exact retry drifted:\nfirst=%+v\nsecond=%+v", firstProjection, secondProjection)
	}

	root := fixture.compiled.Parent.RunManifest
	if firstProjection.RootRunID != root.RunID ||
		firstProjection.RootManifestDigest != root.ManifestDigest ||
		firstProjection.FamilyModelDispatchLimit != 3 ||
		len(firstProjection.Runs) != 3 {
		t.Fatalf("projection identity=%+v", firstProjection)
	}
	for index, child := range fixture.compiled.Children {
		got := firstProjection.Runs[index]
		if got.RunID != child.RunManifest.RunID ||
			got.SlotID != child.Assignment.SlotID ||
			got.Role != corecontract.CompositeRunRoleChildV1 ||
			got.Attempt == nil {
			t.Fatalf("Child %d projection=%+v", index, got)
		}
	}
	for index, attemptID := range []string{first, second} {
		record, err := fixture.store.GetModelDispatchRecord(
			context.Background(),
			attemptID,
		)
		if err != nil {
			t.Fatal(err)
		}
		fact := firstProjection.Runs[index].Attempt
		if fact.Provider != record.Attempt.Provider ||
			fact.Model != record.Attempt.Model ||
			fact.RequestDigest != record.Attempt.Request.Digest ||
			!fact.CreatedAt.Equal(record.Attempt.CreatedAt) ||
			!fact.UpdatedAt.Equal(record.Attempt.UpdatedAt) {
			t.Fatalf(
				"Attempt %q execution fact=%+v record=%+v",
				attemptID,
				fact,
				record.Attempt,
			)
		}
	}
	rootFact := firstProjection.Runs[len(firstProjection.Runs)-1]
	if rootFact.RunID != root.RunID ||
		rootFact.Role != corecontract.CompositeRunRoleRootV1 ||
		rootFact.SlotID != "" || rootFact.Attempt != nil {
		t.Fatalf("Root projection=%+v", rootFact)
	}
	aggregate := firstProjection.Aggregate
	if aggregate.AttemptSlotsUsed != 2 ||
		uint64Value(aggregate.TokenTotals.Input) != 30 ||
		uint64Value(aggregate.TokenTotals.CachedInput) != 9 ||
		uint64Value(aggregate.TokenTotals.UncachedInput) != 21 ||
		uint64Value(aggregate.TokenTotals.Output) != 5 ||
		uint64Value(aggregate.TokenTotals.Reasoning) != 3 {
		t.Fatalf("token aggregate=%+v", aggregate)
	}
}

func TestCompositeFamilyUsageProjectionKeepsEachMixedNullUnknown(t *testing.T) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	commitCompositeUsageSuccess(
		t,
		fixture,
		0,
		"attempt-usage-mixed-null-first",
		corecontract.PureChatModelLogicalStepIDV1,
		usageProjectionReceipt{
			Input: 10, Cached: 4, Uncached: 6, Output: 2, Reasoning: uint64Pointer(1),
		},
	)
	commitCompositeUsageSuccess(
		t,
		fixture,
		1,
		"attempt-usage-mixed-null-second",
		corecontract.PureChatModelLogicalStepIDV1,
		usageProjectionReceipt{
			Input: 20, Cached: 5, Uncached: 15, Output: 3,
			Reasoning: nil,
		},
	)

	projection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if uint64Value(projection.Aggregate.TokenTotals.Input) != 30 ||
		uint64Value(projection.Aggregate.TokenTotals.Output) != 5 {
		t.Fatalf("known token fields were not summed: %+v", projection.Aggregate.TokenTotals)
	}
	if projection.Aggregate.TokenTotals.Reasoning != nil {
		t.Fatalf("mixed NULL reasoning became known: %+v", projection.Aggregate.TokenTotals)
	}
}

func TestCompositeFamilyUsageProjectionUnknownAndPendingConsumeSlots(t *testing.T) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	unknownInput := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-usage-model-unknown",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	unknownBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		unknownInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknownBegin.Lease,
			AttemptID:               unknownBegin.Attempt.AttemptID,
			InvocationID:            unknownBegin.Attempt.AttemptID,
			Provider:                unknownBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknownBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-usage-unknown",
			UnknownReason:           "provider completion is ambiguous",
		},
	); err != nil {
		t.Fatal(err)
	}
	pendingInput := newCompositeChildBeginInput(
		t,
		fixture,
		1,
		"attempt-usage-pending",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		pendingInput,
	); err != nil {
		t.Fatal(err)
	}

	projection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Aggregate.AttemptSlotsUsed != 2 ||
		projection.FamilyModelDispatchLimit != 3 {
		t.Fatalf("UNKNOWN/PENDING slots=%+v", projection.Aggregate)
	}
	if projection.Runs[0].Attempt == nil ||
		projection.Runs[0].Attempt.State != corecontract.ModelAttemptUnknown ||
		projection.Runs[1].Attempt == nil ||
		projection.Runs[1].Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("attempt states were rewritten: %+v", projection.Runs)
	}
	if projection.Aggregate.TokenTotals.Input != nil ||
		projection.Aggregate.TokenTotals.Output != nil {
		t.Fatalf("UNKNOWN/PENDING tokens became zero: %+v", projection.Aggregate.TokenTotals)
	}
}

func TestCompositeFamilyUsageProjectionRejectsNonRootTamperAndClosesAtCap(
	t *testing.T,
) {
	t.Run("ordinary", func(t *testing.T) {
		fixture := newAdmissionCommitFixture(t)
		created, err := fixture.store.CommitRunAdmission(
			context.Background(),
			fixture.input,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			created.RunID,
		); !errors.Is(err, ErrInvalidCompositeUsageProjection) {
			t.Fatalf("ordinary Run error=%v", err)
		}
	})

	t.Run("child", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		if _, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			fixture.compiled.Children[0].RunManifest.RunID,
		); !errors.Is(err, ErrInvalidCompositeUsageProjection) {
			t.Fatalf("Child Run error=%v", err)
		}
	})

	t.Run("family graph", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		execClosedFileTamperV1(t, fixture.store, nil, `
			UPDATE runs
			SET parent_slot_id='slot-tampered'
			WHERE run_id=?
		`, fixture.compiled.Children[0].RunManifest.RunID)
		if _, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			fixture.compiled.Parent.RunManifest.RunID,
		); !errors.Is(err, ErrCompositeUsageProjectionIntegrity) {
			t.Fatalf("tampered graph error=%v", err)
		}
	})

	t.Run("Decision repair physical slot", func(t *testing.T) {
		fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
		repairRunID := fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID
		execClosedFileTamperV1(t, fixture.store, nil, `
			UPDATE runs
			SET parent_slot_id='repair-slot-tampered'
			WHERE run_id=?
		`, repairRunID)
		if _, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			fixture.compiled.Parent.RunManifest.RunID,
		); !errors.Is(err, ErrCompositeUsageProjectionIntegrity) {
			t.Fatalf("tampered repair physical slot error=%v", err)
		}
	})

	t.Run("Decision repair Specialist logical step", func(t *testing.T) {
		fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
		for index, child := range fixture.compiled.Children {
			finishDecisionSpecialist(
				t,
				fixture,
				child,
				"usage-step-initial-"+string(rune('0'+index)),
			)
		}
		affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
		finishDecisionReviewer(
			t,
			fixture,
			fixture.compiled.Reviewer.RunManifest.RunID,
			corecontract.CollaborationReviewDecisionRepairRequiredV1,
			affected,
			"usage-step-reviewer-zero",
		)
		rootLease := acquireCurrentDecisionLease(
			t,
			fixture.store,
			fixture.compiled.Parent.RunManifest.RunID,
			"usage-step-root",
		)
		if _, err := fixture.store.ApplyCompositeDecision(
			context.Background(),
			ApplyCompositeDecisionInput{Lease: rootLease},
		); err != nil {
			t.Fatal(err)
		}
		repair := fixture.compiled.Decision.RepairChildren[0]
		finishDecisionSpecialist(t, fixture, repair, "usage-step-repair")
		execClosedFileTamperV1(
			t,
			fixture.store,
			[]string{"model_dispatch_attempts_observation_update_guard"},
			`
			UPDATE model_dispatch_attempts
			SET logical_step_id='tampered-repair-step'
			WHERE run_id=?
		`,
			repair.RunManifest.RunID,
		)
		if _, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			fixture.compiled.Parent.RunManifest.RunID,
		); !errors.Is(err, ErrCompositeUsageProjectionIntegrity) {
			t.Fatalf("tampered repair logical step error=%v", err)
		}
	})

	t.Run("production lifecycle reaches frozen cap", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		finishCompositeChildForAcceptance(t, fixture, 0)
		finishCompositeChildForAcceptance(t, fixture, 1)
		_, begin := beginCompositeRootForCapTest(
			t,
			fixture,
			"usage-cap-root",
			"attempt-usage-cap-root",
		)
		projection, err := fixture.store.GetCompositeFamilyUsageProjection(
			context.Background(),
			fixture.compiled.Parent.RunManifest.RunID,
		)
		if err != nil ||
			projection.Aggregate.AttemptSlotsUsed !=
				projection.FamilyModelDispatchLimit ||
			projection.Runs[len(projection.Runs)-1].Attempt == nil ||
			projection.Runs[len(projection.Runs)-1].Attempt.AttemptID !=
				begin.Attempt.AttemptID {
			t.Fatalf("at-cap production projection=%+v error=%v", projection, err)
		}
	})
}

type usageProjectionReceipt struct {
	Input     uint64
	Cached    uint64
	Uncached  uint64
	Output    uint64
	Reasoning *uint64
}

func commitCompositeUsageSuccess(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	childIndex int,
	attemptID string,
	logicalStepID string,
	usage usageProjectionReceipt,
) string {
	t.Helper()
	input := newCompositeChildBeginInput(
		t,
		fixture,
		childIndex,
		attemptID,
		logicalStepID,
	)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	providerRequestID := "provider-" + attemptID
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "usage projection result " + attemptID,
			ProviderRequestID: providerRequestID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, usageCanonical, err := moduleapi.NewModelUsageReceiptV2(
		moduleapi.ModelUsageReceiptV2{
			SchemaVersion:       moduleapi.ModelUsageReceiptSchemaV2,
			InputTokens:         &usage.Input,
			CachedInputTokens:   &usage.Cached,
			UncachedInputTokens: &usage.Uncached,
			OutputTokens:        &usage.Output,
			ReasoningTokens:     usage.Reasoning,
			RawReceipt: json.RawMessage(
				`{"attempt_id":"` + attemptID + `"}`,
			),
		},
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
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
			ProviderRequestID:       providerRequestID,
		},
	); err != nil {
		t.Fatal(err)
	}
	return attemptID
}

func finishDecisionUsageRoot(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	lease RunLease,
	suffix string,
) {
	t.Helper()
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile Decision root: %v", err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-" + suffix,
			LogicalStepID:               corecontract.CompositeMergeLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("begin Decision root=%+v error=%v", begin, err)
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
		t.Fatalf("commit Decision root: %v", err)
	}
}

func assertDecisionUsageProjectionShape(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	projection CompositeFamilyUsageProjectionV1,
	activeRepairChildren int,
	wantAttempts uint32,
) {
	t.Helper()
	root := fixture.compiled.Parent.RunManifest
	if projection.RootRunID != root.RunID ||
		projection.RootManifestDigest != root.ManifestDigest ||
		projection.FamilyModelDispatchLimit != 7 || len(projection.Runs) != 7 ||
		projection.Aggregate.AttemptSlotsUsed != wantAttempts {
		t.Fatalf("Decision projection identity/aggregate=%+v", projection)
	}
	wantIDs := []string{
		fixture.compiled.Children[0].RunManifest.RunID,
		fixture.compiled.Children[1].RunManifest.RunID,
		fixture.compiled.Reviewer.RunManifest.RunID,
		fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID,
		fixture.compiled.Decision.RepairChildren[1].RunManifest.RunID,
		fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
		root.RunID,
	}
	wantRoles := []corecontract.CompositeRunRoleV1{
		corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1,
		corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1,
		corecontract.CompositeRunRoleRootV1,
	}
	wantSlots := []string{
		fixture.compiled.Children[0].Assignment.SlotID,
		fixture.compiled.Children[1].Assignment.SlotID,
		corecontract.CompositeReviewerParentSlotIDV1,
		fixture.compiled.Decision.RepairChildren[0].Assignment.SlotID,
		fixture.compiled.Decision.RepairChildren[1].Assignment.SlotID,
		corecontract.CompositeReviewerParentSlotIDV1,
		"",
	}
	for index := range projection.Runs {
		got := projection.Runs[index]
		if got.RunID != wantIDs[index] || got.Role != wantRoles[index] ||
			got.SlotID != wantSlots[index] {
			t.Fatalf("Decision Run %d=%+v", index, got)
		}
		wantAttempt := wantAttempts != 0 &&
			(index < 3 || (index >= 3 && index < 3+activeRepairChildren) ||
				(index == 5 && activeRepairChildren > 0) || index == 6)
		if (got.Attempt != nil) != wantAttempt {
			t.Fatalf("Decision Run %d Attempt presence=%t want=%t", index, got.Attempt != nil, wantAttempt)
		}
		if got.Attempt != nil {
			wantStep := corecontract.PureChatModelLogicalStepIDV1
			switch got.Role {
			case corecontract.CompositeRunRoleReviewerV1:
				wantStep = corecontract.CompositeReviewLogicalStepIDV1
			case corecontract.CompositeRunRoleRootV1:
				wantStep = corecontract.CompositeMergeLogicalStepIDV1
			}
			if got.Attempt.LogicalStepID != wantStep {
				t.Fatalf(
					"Decision Run %d logical step=%q want=%q",
					index,
					got.Attempt.LogicalStepID,
					wantStep,
				)
			}
		}
	}
}

type usageProjectionFingerprint struct {
	Runs            int
	RunRevision     int64
	Manifests       int
	Attempts        int
	AttemptRevision int64
	UsageRows       int
	UsageRevision   int64
	Contents        int
}

func usageProjectionStorageFingerprint(
	t *testing.T,
	store *Store,
) usageProjectionFingerprint {
	t.Helper()
	var result usageProjectionFingerprint
	if err := store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM runs),
			(SELECT COALESCE(SUM(revision), 0) FROM runs),
			(SELECT COUNT(*) FROM run_manifests),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COALESCE(SUM(revision), 0) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COALESCE(SUM(revision), 0) FROM model_usage),
			(SELECT COUNT(*) FROM content_records)
	`).Scan(
		&result.Runs,
		&result.RunRevision,
		&result.Manifests,
		&result.Attempts,
		&result.AttemptRevision,
		&result.UsageRows,
		&result.UsageRevision,
		&result.Contents,
	); err != nil {
		t.Fatal(err)
	}
	return result
}

func uint64Pointer(value uint64) *uint64 { return &value }

func uint64Value(value *uint64) uint64 {
	if value == nil {
		return ^uint64(0)
	}
	return *value
}
