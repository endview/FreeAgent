package currentstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeAdmissionConcurrentIdentityAndFamilyIsolation(t *testing.T) {
	t.Run("same root has exactly one creator", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		before := admissionCommitCounts(t, fixture.store)
		results, failures := commitCompositeConcurrently(
			fixture.store,
			fixture.input,
			fixture.input,
		)
		if len(failures) != 0 {
			t.Fatalf("concurrent same-root Admission errors=%v", failures)
		}
		created := 0
		for _, result := range results {
			if result.Created {
				created++
			}
			if result.Parent.RunID != fixture.compiled.Parent.RunManifest.RunID ||
				len(result.Children) != len(fixture.compiled.Children) {
				t.Fatalf("same-root result=%+v", result)
			}
		}
		if created != 1 {
			t.Fatalf("same-root Created count=%d want 1", created)
		}
		familySize := 1 + len(fixture.input.Children)
		after := admissionCommitCounts(t, fixture.store)
		for index := 0; index < 5; index++ {
			if after[index] != before[index]+familySize {
				t.Fatalf(
					"same-root closure count[%d]=%d want %d (before=%v after=%v)",
					index,
					after[index],
					before[index]+familySize,
					before,
					after,
				)
			}
		}
	})

	t.Run("different roots commit independently", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		second := compileSiblingCompositeFamily(t, fixture, "second")
		before := admissionCommitCounts(t, fixture.store)
		results, failures := commitCompositeConcurrently(
			fixture.store,
			fixture.input,
			second.input,
		)
		if len(failures) != 0 {
			t.Fatalf("concurrent distinct-root Admission errors=%v", failures)
		}
		if len(results) != 2 || !results[0].Created || !results[1].Created {
			t.Fatalf("distinct-root results=%+v", results)
		}
		if results[0].Parent.RunID == results[1].Parent.RunID {
			t.Fatalf("distinct roots aliased RunID %q", results[0].Parent.RunID)
		}
		familySize := 1 + len(fixture.input.Children)
		after := admissionCommitCounts(t, fixture.store)
		for index := 0; index < 5; index++ {
			if after[index] != before[index]+2*familySize {
				t.Fatalf(
					"distinct-root closure count[%d]=%d want %d (before=%v after=%v)",
					index,
					after[index],
					before[index]+2*familySize,
					before,
					after,
				)
			}
		}
		for _, family := range []struct {
			intent corecontract.AdmissionIntentV1
			input  CommitCompositeRunFamilyInput
			rootID string
		}{
			{fixture.parentIntent, fixture.input, fixture.compiled.Parent.RunManifest.RunID},
			{second.intent, second.input, second.compiled.Parent.RunManifest.RunID},
		} {
			resolved, found, err := fixture.store.ResolveCompositeRunFamily(
				context.Background(),
				family.intent.TenantID,
				family.intent.AdmissionKey,
				family.input.Parent.IntentDigest,
			)
			if err != nil || !found || resolved.Created ||
				resolved.Parent.RunID != family.rootID ||
				len(resolved.Children) != len(family.input.Children) {
				t.Fatalf(
					"resolve independent family root=%q found=%v result=%+v error=%v",
					family.rootID,
					found,
					resolved,
					err,
				)
			}
		}
	})
}

func TestCompositeAdmissionRejectsOptionalMutableAndEffectCapabilitiesWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name     string
		wantText string
		mutate   func(*testing.T, *compositeAdmissionFixture) CommitCompositeRunFamilyInput
	}{
		{
			name:     "Memory",
			wantText: "binds mutable Memory",
			mutate: func(t *testing.T, fixture *compositeAdmissionFixture) CommitCompositeRunFamilyInput {
				config, authority := compositeMemoryContents(t, fixture)
				input := rewriteCompositeParentMemberForAcceptance(
					t,
					fixture.input,
					func(member *corecontract.MemberExecutionSnapshot) {
						provider := member.PortPlans[0].Bindings[0].Provider
						member.PortPlans = append(member.PortPlans, moduleapi.PortPlan{
							Port: moduleapi.PortRef{
								Name:         moduleapi.PortNameContextProvide,
								ExactVersion: moduleapi.PortVersionV1,
							},
							Bindings: []moduleapi.PortBinding{{
								Provider:            provider,
								ConfigRef:           config.Digest,
								AuthorityCeilingRef: authority.Digest,
								FailurePolicy:       moduleapi.FailureRequired,
							}},
						})
					},
				)
				input.Parent.Contents = append(
					input.Parent.Contents,
					config,
					authority,
				)
				return input
			},
		},
		{
			name:     "Action over MCP local process",
			wantText: "binds Action, MCP, or Channel",
			mutate: func(t *testing.T, fixture *compositeAdmissionFixture) CommitCompositeRunFamilyInput {
				return rewriteCompositeParentMemberForAcceptance(
					t,
					fixture.input,
					func(member *corecontract.MemberExecutionSnapshot) {
						base := member.PortPlans[0].Bindings[0]
						base.Provider = moduleapi.ActivatedModuleRef{
							ModuleID:           "test.mcp.action",
							Version:            "v1",
							ArtifactDigest:     strings.Repeat("a", 64),
							InstanceID:         "instance-mcp-action",
							ExecutionClass:     moduleapi.ExecutionLocalProcess,
							AdapterIdentity:    "mcp-stdio-2025-11-25",
							ActivationRevision: 1,
						}
						member.PortPlans = append(member.PortPlans, moduleapi.PortPlan{
							Port: moduleapi.PortRef{
								Name:         moduleapi.PortNameActionProvider,
								ExactVersion: moduleapi.PortVersionV1,
							},
							Bindings: []moduleapi.PortBinding{base},
						})
						definition, _, err := corecontract.NewFrozenActionDefinitionV1(
							corecontract.FrozenActionDefinitionV1{
								PublicActionID:   "test.lookup",
								ProviderActionID: "provider.lookup",
								Description:      "Read a deterministic local value.",
								InputSchema: json.RawMessage(
									`{"additionalProperties":false,"properties":{},"type":"object"}`,
								),
								EffectClass:    moduleapi.EffectNone,
								MaxResultBytes: 1024,
							},
						)
						if err != nil {
							t.Fatalf("freeze MCP Action definition: %v", err)
						}
						member.Actions = []corecontract.FrozenActionDefinitionV1{definition}
					},
				)
			},
		},
		{
			name:     "Channel",
			wantText: "binds Action, MCP, or Channel",
			mutate: func(t *testing.T, fixture *compositeAdmissionFixture) CommitCompositeRunFamilyInput {
				return rewriteCompositeParentMemberForAcceptance(
					t,
					fixture.input,
					func(member *corecontract.MemberExecutionSnapshot) {
						binding := member.PortPlans[0].Bindings[0]
						member.PortPlans = append(member.PortPlans, moduleapi.PortPlan{
							Port: moduleapi.PortRef{
								Name:         moduleapi.PortNameChannelTransport,
								ExactVersion: moduleapi.PortVersionV1,
							},
							Bindings: []moduleapi.PortBinding{binding},
						})
					},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeAdmissionFixture(t)
			input := test.mutate(t, fixture)
			assertCompositeAcceptanceRejectedWithoutWrites(
				t,
				fixture.store,
				input,
				ErrInvalidAdmission,
				test.wantText,
			)
		})
	}
}

func TestCompositeAdmissionRejectsBrokenFamilyShapeWithoutWrites(t *testing.T) {
	t.Run("missing Child", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := cloneCompositeFamilyInputForAcceptance(fixture.input)
		input.Children = input.Children[:1]
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})

	t.Run("Child uses another Task", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := rewriteCompositeChildTaskForAcceptance(t, fixture, 0)
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})

	t.Run("depth two Child lineage", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := cloneCompositeFamilyInputForAcceptance(fixture.input)
		child := restoreCompositeManifest(t, input.Children[0])
		child.ParentRunID = fixture.compiled.Children[1].RunManifest.RunID
		child.Composite.RootRunID = child.ParentRunID
		_, canonical, err := corecontract.NewRunManifest(child)
		if err != nil {
			t.Fatalf("freeze depth-two Child: %v", err)
		}
		input.Children[0].RunManifestCanonical = canonical
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})

	t.Run("cross Workspace Child", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := rewriteCompositeChildWorkspaceForAcceptance(t, fixture, 0)
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})
}

func TestCompositeReopenMatrixPreservesLedgerAndNeverRegrantsPendingMerge(
	t *testing.T,
) {
	t.Run("just admitted", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		created, err := fixture.store.CommitCompositeRunFamily(
			context.Background(),
			fixture.input,
		)
		if err != nil {
			t.Fatal(err)
		}
		reopenCompositeAcceptanceStore(t, fixture)
		runs, err := fixture.store.ScanStartupRecovery(context.Background())
		if err != nil {
			t.Fatalf("ScanStartupRecovery after reopen: %v", err)
		}
		if got := startupRecoveryByRunID(runs, created.Parent.RunID); got.FrameStep != corecontract.WaitingChildrenLoopStep ||
			got.UnsettledAttemptID != "" {
			t.Fatalf("reopened just-admitted root=%+v", got)
		}
	})

	t.Run("one Child succeeded", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		putCompositeModelPrice(t, fixture.store)
		finishCompositeChildForAcceptance(t, fixture, 0)
		before := compositeModelAttemptCount(t, fixture.store)
		reopenCompositeAcceptanceStore(t, fixture)
		if _, err := fixture.store.ScanStartupRecovery(context.Background()); err != nil {
			t.Fatalf("ScanStartupRecovery after partial Child success: %v", err)
		}
		if after := compositeModelAttemptCount(t, fixture.store); after != before {
			t.Fatalf("partial-success reopen changed Attempts: %d -> %d", before, after)
		}
		rootLease := acquireCompositeTestLease(
			t,
			fixture.store,
			fixture.compiled.Parent.RunManifest.RunID,
			"partial-reopen-root-reader",
		)
		view := assertCompositeRootChildStates(
			t,
			fixture,
			rootLease,
			[]CompositeChildStateV1{
				CompositeChildSucceededV1,
				CompositeChildPendingV1,
			},
		)
		if view.Frame.Step != corecontract.WaitingChildrenLoopStep {
			t.Fatalf("partial-success root Frame=%+v", view.Frame)
		}
	})

	t.Run("merge PENDING becomes UNKNOWN without replay permit", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		putCompositeModelPrice(t, fixture.store)
		for index := range fixture.compiled.Children {
			finishCompositeChildForAcceptance(t, fixture, index)
		}
		rootID := fixture.compiled.Parent.RunManifest.RunID
		lease := acquireCompositeTestLease(
			t,
			fixture.store,
			rootID,
			"merge-pending-before-reopen",
		)
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
			t.Fatalf("compile root merge request: %v", err)
		}
		beginInput := BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-composite-merge-crash",
			LogicalStepID:               corecontract.CompositeMergeLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: fixture.compiled.Parent.RunManifest.Deadline.
				Add(-time.Hour).
				Truncate(time.Microsecond),
		}
		begun, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		)
		if err != nil {
			t.Fatalf("Begin root merge: %v", err)
		}
		if !begun.Created || !begun.InvokeAllowed ||
			begun.Attempt.State != corecontract.ModelAttemptPending {
			t.Fatalf("root merge Begin=%+v", begun)
		}
		if err := fixture.store.ReleaseRunLease(
			context.Background(),
			begun.Lease,
		); err != nil {
			t.Fatalf("release pre-reopen root lease: %v", err)
		}
		before := compositeModelAttemptCount(t, fixture.store)
		reopenCompositeAcceptanceStore(t, fixture)

		runs, err := fixture.store.ScanStartupRecovery(context.Background())
		if err != nil {
			t.Fatalf("scan merge PENDING: %v", err)
		}
		pending := startupRecoveryByRunID(runs, rootID)
		if pending.FrameStep != corecontract.ModelPendingLoopStep ||
			pending.UnsettledAttemptID != begun.Attempt.AttemptID ||
			pending.UnsettledAttemptState != corecontract.ModelAttemptPending {
			t.Fatalf("reopened merge PENDING=%+v", pending)
		}
		recoveryLease, err := fixture.store.AcquireCurrentRunLease(
			context.Background(),
			AcquireCurrentRunLeaseInput{
				RunID:   rootID,
				OwnerID: "merge-pending-recovery",
				TTL:     time.Minute,
			},
		)
		if err != nil {
			t.Fatalf("acquire merge recovery lease: %v", err)
		}
		recovered, err := fixture.store.RecoverStartupPending(
			context.Background(),
			RecoverStartupPendingInput{
				Lease:         recoveryLease,
				AttemptKind:   corecontract.AttemptKindModel,
				AttemptID:     begun.Attempt.AttemptID,
				UnknownReason: "RECOVERED_COMPOSITE_MERGE_AFTER_CRASH",
			},
		)
		if err != nil {
			t.Fatalf("recover merge PENDING: %v", err)
		}
		beginInput.Lease = recovered.Lease
		retry, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		)
		if err != nil {
			t.Fatalf("retry recovered merge: %v", err)
		}
		if retry.Created || retry.InvokeAllowed ||
			retry.ConsumeModelInvocationPermit() ||
			retry.Attempt.AttemptID != begun.Attempt.AttemptID ||
			retry.Attempt.State != corecontract.ModelAttemptUnknown {
			t.Fatalf("recovered merge retry regranted invocation: %+v", retry)
		}
		if after := compositeModelAttemptCount(t, fixture.store); after != before {
			t.Fatalf("merge recovery/retry changed Attempt count: %d -> %d", before, after)
		}
	})
}

func TestCompositeRootRejectsTerminalChildResultPointingAtNonResultContent(
	t *testing.T,
) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	putCompositeModelPrice(t, fixture.store)
	attemptID := finishCompositeChildForAcceptance(t, fixture, 0)
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"orphan-result-root-reader",
	)
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET result_ref=?
		WHERE attempt_id=?
	`,
		fixture.task.Digest,
		attemptID,
	)
	if _, err := fixture.store.LoadRunForLoop(
		context.Background(),
		rootLease,
	); err == nil ||
		(!errors.Is(err, ErrLoopIntegrity) &&
			!errors.Is(err, ErrAdmissionIntegrity)) {
		t.Fatalf("non-result Child result_ref error=%v", err)
	}
}

type compositeCompiledAcceptanceFamily struct {
	intent   corecontract.AdmissionIntentV1
	compiled assemblycompiler.CompositeCompileOutput
	input    CommitCompositeRunFamilyInput
}

func compileSiblingCompositeFamily(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	suffix string,
) compositeCompiledAcceptanceFamily {
	t.Helper()
	intentInput := fixture.parentIntent
	intentInput.AdmissionKey = "admission-composite-" + suffix
	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intentInput)
	if err != nil {
		t.Fatalf("freeze sibling composite intent: %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		context.Background(),
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  intentCanonical,
				IntentDigest:     intentDigest,
				RunID:            "run-composite-" + suffix,
				MemberID:         "member-composite-" + suffix,
				RecoveryRootRef:  "recovery/run-composite-" + suffix,
				PublishedBasis:   fixture.basis,
				ControlCanonical: fixture.controlCanonical,
				CatalogCanonical: fixture.catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("compile sibling composite family: %v", err)
	}
	input := CommitCompositeRunFamilyInput{
		Parent: compositeCommitRunInput(
			fixture,
			compiled.Parent,
			intentCanonical,
			intentDigest,
		),
		Children: make([]CommitRunAdmissionInput, len(compiled.Children)),
	}
	for index, child := range compiled.Children {
		input.Children[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	return compositeCompiledAcceptanceFamily{
		intent: intent, compiled: compiled, input: input,
	}
}

func commitCompositeConcurrently(
	store *Store,
	inputs ...CommitCompositeRunFamilyInput,
) ([]CompositeRunFamilyAdmissionResult, []error) {
	start := make(chan struct{})
	results := make(chan CompositeRunFamilyAdmissionResult, len(inputs))
	failures := make(chan error, len(inputs))
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := store.CommitCompositeRunFamily(
				context.Background(),
				input,
			)
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(failures)
	got := make([]CompositeRunFamilyAdmissionResult, 0, len(inputs))
	for result := range results {
		got = append(got, result)
	}
	errs := make([]error, 0, len(inputs))
	for err := range failures {
		errs = append(errs, err)
	}
	return got, errs
}

func rewriteCompositeParentMemberForAcceptance(
	t *testing.T,
	input CommitCompositeRunFamilyInput,
	mutate func(*corecontract.MemberExecutionSnapshot),
) CommitCompositeRunFamilyInput {
	t.Helper()
	input = cloneCompositeFamilyInputForAcceptance(input)
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		input.Parent.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatalf("restore composite Parent member: %v", err)
	}
	mutate(&member)
	frozenMember, memberCanonical, err :=
		corecontract.NewMemberExecutionSnapshot(member)
	if err != nil {
		t.Fatalf("freeze rewritten composite Parent member: %v", err)
	}
	input.Parent.MemberSnapshotCanonical = memberCanonical
	parent := restoreCompositeManifest(t, input.Parent)
	parent.Members[0].Digest = frozenMember.MemberSnapshotDigest
	frozenParent, parentCanonical, err := corecontract.NewRunManifest(parent)
	if err != nil {
		t.Fatalf("freeze rewritten composite Parent Manifest: %v", err)
	}
	input.Parent.RunManifestCanonical = parentCanonical
	for index := range input.Children {
		child := restoreCompositeManifest(t, input.Children[index])
		child.Composite.ParentManifestDigest = frozenParent.ManifestDigest
		_, childCanonical, freezeErr := corecontract.NewRunManifest(child)
		if freezeErr != nil {
			t.Fatalf("freeze rewritten composite Child %d: %v", index, freezeErr)
		}
		input.Children[index].RunManifestCanonical = childCanonical
	}
	return input
}

func rewriteCompositeChildTaskForAcceptance(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	childIndex int,
) CommitCompositeRunFamilyInput {
	t.Helper()
	input := cloneCompositeFamilyInputForAcceptance(fixture.input)
	_, canonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          "a different Child task",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := newAdmissionContent(t, ContentTaskInput, canonical)
	intent, err := corecontract.RestoreAdmissionIntentV1(
		input.Children[childIndex].IntentCanonical,
		input.Children[childIndex].IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.TaskInputRef = task.Digest
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input.Children[childIndex].IntentCanonical = intentCanonical
	input.Children[childIndex].IntentDigest = intentDigest
	child := restoreCompositeManifest(t, input.Children[childIndex])
	child.AdmissionIntentDigest = intentDigest
	child.TaskInputRef = task.Digest
	child.TaskInputDigest = task.Digest
	_, childCanonical, err := corecontract.NewRunManifest(child)
	if err != nil {
		t.Fatal(err)
	}
	input.Children[childIndex].RunManifestCanonical = childCanonical
	input.Children[childIndex].Contents = append(
		input.Children[childIndex].Contents,
		task,
	)
	return input
}

func rewriteCompositeChildWorkspaceForAcceptance(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	childIndex int,
) CommitCompositeRunFamilyInput {
	t.Helper()
	input := cloneCompositeFamilyInputForAcceptance(fixture.input)
	workspace := corecontract.WorkspaceRef{
		ID:      "workspace-other",
		Version: "v1",
		Digest:  strings.Repeat("9", 64),
	}
	intent, err := corecontract.RestoreAdmissionIntentV1(
		input.Children[childIndex].IntentCanonical,
		input.Children[childIndex].IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.WorkspaceID = workspace.ID
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input.Children[childIndex].IntentCanonical = intentCanonical
	input.Children[childIndex].IntentDigest = intentDigest
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		input.Children[childIndex].MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	member.Workspace = workspace
	frozenMember, memberCanonical, err := corecontract.NewMemberExecutionSnapshot(member)
	if err != nil {
		t.Fatal(err)
	}
	input.Children[childIndex].MemberSnapshotCanonical = memberCanonical
	child := restoreCompositeManifest(t, input.Children[childIndex])
	child.AdmissionIntentDigest = intentDigest
	child.Workspace = workspace
	child.Members[0].Digest = frozenMember.MemberSnapshotDigest
	_, childCanonical, err := corecontract.NewRunManifest(child)
	if err != nil {
		t.Fatal(err)
	}
	input.Children[childIndex].RunManifestCanonical = childCanonical
	return input
}

func cloneCompositeFamilyInputForAcceptance(
	input CommitCompositeRunFamilyInput,
) CommitCompositeRunFamilyInput {
	cloned := input
	cloned.Parent = cloneCompositeRunAdmissionInputForAcceptance(input.Parent)
	cloned.Children = make([]CommitRunAdmissionInput, len(input.Children))
	for index, child := range input.Children {
		cloned.Children[index] = cloneCompositeRunAdmissionInputForAcceptance(child)
	}
	return cloned
}

func cloneCompositeRunAdmissionInputForAcceptance(
	input CommitRunAdmissionInput,
) CommitRunAdmissionInput {
	cloned := input
	cloned.IntentCanonical = append([]byte(nil), input.IntentCanonical...)
	cloned.MemberSnapshotCanonical = append(
		[]byte(nil),
		input.MemberSnapshotCanonical...,
	)
	cloned.RunManifestCanonical = append(
		[]byte(nil),
		input.RunManifestCanonical...,
	)
	cloned.Contents = make([]ContentInput, len(input.Contents))
	for index, content := range input.Contents {
		cloned.Contents[index] = content
		cloned.Contents[index].CanonicalBytes = append(
			[]byte(nil),
			content.CanonicalBytes...,
		)
	}
	return cloned
}

func compositeMemoryContents(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) (ContentInput, ContentInput) {
	t.Helper()
	_, parameters, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion:     moduleapi.MemoryContextBindingSchemaV1,
			Kinds:             []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
			MaxItems:          2,
			MaxTotalTextBytes: 2048,
			CategoryRules:     []moduleapi.MemoryCategoryRuleV1{},
			StopTerms:         []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parentMember, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.Parent.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewMemoryAuthorityCeilingV1(
		moduleapi.MemoryAuthorityCeilingV1{
			SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
			TenantID:            fixture.parentIntent.TenantID,
			AgentID:             parentMember.Agent.ID,
			AllowedWorkspaceIDs: []string{parentMember.Workspace.ID},
			AllowedKinds:        []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
			MaxItems:            2,
			MaxTotalTextBytes:   2048,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return newAdmissionContent(t, ContentConfig, configCanonical),
		newAdmissionContent(t, ContentAuthorityCeiling, authorityCanonical)
}

func finishCompositeChildForAcceptance(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	childIndex int,
) string {
	t.Helper()
	attemptID := "attempt-acceptance-child-" +
		fixture.compiled.Children[childIndex].Assignment.SlotID
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		newCompositeChildBeginInput(
			t,
			fixture,
			childIndex,
			attemptID,
			"acceptance-child-step-"+
				fixture.compiled.Children[childIndex].Assignment.SlotID,
		),
	)
	if err != nil {
		t.Fatalf("Begin Child %d: %v", childIndex, err)
	}
	output, usage := minimalCompositeSuccessOutcomeCanonical(t)
	committed, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         output,
			UsageReceiptCanonical:   usage,
		},
	)
	if err != nil {
		t.Fatalf("complete Child %d: %v", childIndex, err)
	}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		committed.Lease,
	); err != nil {
		t.Fatalf("release Child %d lease: %v", childIndex, err)
	}
	return attemptID
}

func minimalCompositeSuccessOutcomeCanonical(t *testing.T) ([]byte, []byte) {
	t.Helper()
	_, output, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "x",
			ProviderRequestID: "p",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return output, modelUsageOutcomeCanonical(t, `{"id":"p"}`)
}

func reopenCompositeAcceptanceStore(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) {
	t.Helper()
	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close composite Store before reopen: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen composite Store: %v", err)
	}
	fixture.store = reopened
	t.Cleanup(func() { _ = reopened.Close() })
}

func startupRecoveryByRunID(
	runs []StartupRecoveryRun,
	runID string,
) StartupRecoveryRun {
	for _, run := range runs {
		if run.RunID == runID {
			return run
		}
	}
	return StartupRecoveryRun{}
}

func compositeModelAttemptCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM model_dispatch_attempts`).Scan(
		&count,
	); err != nil {
		t.Fatalf("count model Attempts: %v", err)
	}
	return count
}

func assertCompositeAcceptanceRejectedWithoutWrites(
	t *testing.T,
	store *Store,
	input CommitCompositeRunFamilyInput,
	want error,
	wantText string,
) {
	t.Helper()
	before := admissionCommitCounts(t, store)
	_, err := store.CommitCompositeRunFamily(context.Background(), input)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), wantText) {
		t.Fatalf(
			"CommitCompositeRunFamily error=%v want %v containing %q",
			err,
			want,
			wantText,
		)
	}
	if after := admissionCommitCounts(t, store); after != before {
		t.Fatalf(
			"rejected composite family changed rows: before=%v after=%v",
			before,
			after,
		)
	}
}
