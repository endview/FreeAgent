package coreloop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeRootWithPendingChildWaitsWithoutModelAttempt(t *testing.T) {
	fixture := newCompositeLoopFixture(t, "pending")
	invoker := newCompositeEchoInvoker(t, fixture)
	loop := newIntegrationLoop(t, fixture.base, invoker)

	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root): %v", err)
	}
	if result.Disposition != loopapi.DispositionWaitingExternal ||
		result.ReasonCode != reasonCompositeChildrenPending ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
}

func TestCompositeRootFailedChildTakesPriorityOverUnknown(t *testing.T) {
	fixture := newCompositeLoopFixture(t, "unknown-priority")
	invoker := &integrationInvoker{fixed: modulehost.InvocationResult{
		Provider: fixture.base.provider,
		Outcome:  modulehost.InvocationFailed,
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)

	failed, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.childRunID(0)),
	)
	if err != nil {
		t.Fatalf("Run(failed child): %v", err)
	}
	if failed.Disposition != loopapi.DispositionTerminated ||
		failed.ReasonCode != reasonModelFailed {
		t.Fatalf("failed child result=%+v", failed)
	}

	invoker.fixed = modulehost.InvocationResult{
		Provider: fixture.base.provider,
		Outcome:  modulehost.InvocationUnknown,
	}
	unknown, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.childRunID(1)),
	)
	if err != nil {
		t.Fatalf("Run(unknown child): %v", err)
	}
	if unknown.Disposition != loopapi.DispositionWaitingReconciliation ||
		unknown.ReasonCode != reasonModelUnknown {
		t.Fatalf("unknown child result=%+v", unknown)
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root): %v", err)
	}
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonCompositeChildFailed ||
		invoker.callCount() != 2 {
		t.Fatalf("root result=%+v calls=%d", root, invoker.callCount())
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
}

func TestCompositeRootFailedChildTerminatesAheadOfPendingWithoutModelAttempt(
	t *testing.T,
) {
	fixture := newCompositeLoopFixture(t, "failed")
	invoker := &integrationInvoker{fixed: modulehost.InvocationResult{
		Provider: fixture.base.provider,
		Outcome:  modulehost.InvocationFailed,
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)

	child, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.childRunID(0)),
	)
	if err != nil {
		t.Fatalf("Run(child): %v", err)
	}
	if child.Disposition != loopapi.DispositionTerminated ||
		child.ReasonCode != reasonModelFailed {
		t.Fatalf("child result=%+v", child)
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root): %v", err)
	}
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonCompositeChildFailed ||
		invoker.callCount() != 1 {
		t.Fatalf("root result=%+v calls=%d", root, invoker.callCount())
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	terminal, err := fixture.base.store.GetTerminalRunResult(
		context.Background(),
		fixture.rootRunID(),
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult(root): %v", err)
	}
	if terminal.ReasonCode != reasonCompositeChildFailed ||
		terminal.ErrorClassification != reasonCompositeChildFailed ||
		terminal.AttemptKind != "" || terminal.AttemptID != "" {
		t.Fatalf("terminal root=%+v", terminal)
	}

	if err := fixture.base.store.Close(); err != nil {
		t.Fatalf("Close Current Store for restart: %v", err)
	}
	reopened, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.base.dbPath,
	)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore after restart: %v", err)
	}
	fixture.base.store = reopened
	restartedLoop := newIntegrationLoop(t, fixture.base, invoker)
	restarted, err := restartedLoop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root after restart): %v", err)
	}
	if restarted.Disposition != loopapi.DispositionTerminated ||
		restarted.ReasonCode != reasonCompositeChildFailed ||
		invoker.callCount() != 1 {
		t.Fatalf("restarted=%+v calls=%d", restarted, invoker.callCount())
	}
}

func TestCompositeRootMergesOnceWithoutCopyingChildResultsIntoHistory(t *testing.T) {
	fixture := newCompositeLoopFixture(t, "merge")
	invoker := newCompositeEchoInvoker(t, fixture)
	loop := newIntegrationLoop(t, fixture.base, invoker)

	for index := range fixture.compiled.Children {
		child, err := loop.Run(
			context.Background(),
			integrationRunInput(fixture.childRunID(index)),
		)
		if err != nil {
			t.Fatalf("Run(child %d): %v", index, err)
		}
		if child.Disposition != loopapi.DispositionTerminated ||
			child.ReasonCode != reasonModelSucceeded {
			t.Fatalf("child %d result=%+v", index, child)
		}
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root): %v", err)
	}
	wantCalls := int32(len(fixture.compiled.Children) + 1)
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != wantCalls {
		t.Fatalf("root result=%+v calls=%d want=%d", root, invoker.callCount(), wantCalls)
	}

	mergeAttemptID := compositeModelAttemptID(
		t,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	merge, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		mergeAttemptID,
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord(merge): %v", err)
	}
	if merge.Attempt.LogicalStepID != corecontract.CompositeMergeLogicalStepIDV1 ||
		merge.Attempt.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("merge Attempt=%+v", merge.Attempt)
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		pureChatLogicalStepID,
	)

	terminal, err := fixture.base.store.GetTerminalRunResult(
		context.Background(),
		fixture.rootRunID(),
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult(root): %v", err)
	}
	if terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.AttemptID != mergeAttemptID {
		t.Fatalf("terminal root=%+v", terminal)
	}

	reentry, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root reentry): %v", err)
	}
	if reentry.Disposition != loopapi.DispositionTerminated ||
		reentry.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != wantCalls {
		t.Fatalf("reentry=%+v calls=%d want=%d", reentry, invoker.callCount(), wantCalls)
	}

	storedRoot := loadCompositeRunForLoop(t, fixture, fixture.rootRunID())
	if len(storedRoot.History) != 1 {
		t.Fatalf("root History entries=%d want 1", len(storedRoot.History))
	}
	rootHistory := storedRoot.History[0]
	if rootHistory.SourceAttemptID != mergeAttemptID ||
		rootHistory.Content.Kind != currentstore.ContentModelResult ||
		rootHistory.Content.Digest != merge.Attempt.ResultRef {
		t.Fatalf("root History entry=%+v merge Attempt=%+v", rootHistory, merge.Attempt)
	}
	for index, child := range fixture.compiled.Children {
		childAttemptID := compositeModelAttemptID(
			t,
			fixture.childRunID(index),
			child.MemberSnapshot.MemberID,
			pureChatLogicalStepID,
		)
		childRecord, err := fixture.base.store.GetModelDispatchRecord(
			context.Background(),
			childAttemptID,
		)
		if err != nil {
			t.Fatalf("GetModelDispatchRecord(child %d): %v", index, err)
		}
		if rootHistory.SourceAttemptID == childAttemptID ||
			rootHistory.Content.Digest == childRecord.Attempt.ResultRef {
			t.Fatalf(
				"root History copied Child %d Attempt/result: history=%+v child=%+v",
				index,
				rootHistory,
				childRecord.Attempt,
			)
		}
	}
}

func TestCompositeRootChildResultOverBudgetTerminatesBeforeModelPermit(
	t *testing.T,
) {
	fixture := newCompositeLoopFixture(t, "result-over-budget")
	invoker := &integrationInvoker{delegate: &oversizedCompositeResultInvoker{
		provider: fixture.base.provider,
		text:     strings.Repeat("x", 200_000),
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)

	for index := range fixture.compiled.Children {
		child, err := loop.Run(
			context.Background(),
			integrationRunInput(fixture.childRunID(index)),
		)
		if err != nil {
			t.Fatalf("Run(child %d): %v", index, err)
		}
		if child.Disposition != loopapi.DispositionTerminated ||
			child.ReasonCode != reasonModelSucceeded {
			t.Fatalf("child %d result=%+v", index, child)
		}
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root): %v", err)
	}
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonCompositeChildResultOverBudget ||
		invoker.callCount() != int32(len(fixture.compiled.Children)) {
		t.Fatalf("root=%+v calls=%d", root, invoker.callCount())
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	terminal, err := fixture.base.store.GetTerminalRunResult(
		context.Background(),
		fixture.rootRunID(),
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult(root): %v", err)
	}
	if terminal.ReasonCode != reasonCompositeChildResultOverBudget ||
		terminal.AttemptID != "" || terminal.AttemptKind != "" ||
		terminal.State != "" || terminal.ModelState != "" {
		t.Fatalf("terminal root=%+v", terminal)
	}

	reentered, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatalf("Run(root re-entry): %v", err)
	}
	if reentered.Disposition != loopapi.DispositionTerminated ||
		reentered.ReasonCode != reasonCompositeChildResultOverBudget ||
		invoker.callCount() != int32(len(fixture.compiled.Children)) {
		t.Fatalf("reentered=%+v calls=%d", reentered, invoker.callCount())
	}
}

func TestCompositeCancellationLatchCreatesNoModelAttempt(t *testing.T) {
	fixture := newCompositeLoopFixture(t, "canceled")
	_, cancellationCanonical, err :=
		corecontract.NewRunCancellationRequestV1(
			corecontract.RunCancellationRequestV1{
				SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
				RootRunID:          fixture.rootRunID(),
				RootManifestDigest: fixture.compiled.Parent.RunManifest.ManifestDigest,
				Scope:              corecontract.CancellationScopeFamilyV1,
				ReasonCode:         corecontract.CancellationReasonUserRequestV1,
			},
		)
	if err != nil {
		t.Fatalf("NewRunCancellationRequestV1: %v", err)
	}
	if _, err := fixture.base.store.RequestRunCancellation(
		context.Background(),
		currentstore.RequestRunCancellationInput{
			Canonical: cancellationCanonical,
		},
	); err != nil {
		t.Fatalf("RequestRunCancellation: %v", err)
	}

	invoker := newCompositeEchoInvoker(t, fixture)
	loop := newIntegrationLoop(t, fixture.base, invoker)
	runIDs := []string{fixture.rootRunID()}
	for index := range fixture.compiled.Children {
		runIDs = append(runIDs, fixture.childRunID(index))
	}
	for _, runID := range runIDs {
		result, err := loop.Run(
			context.Background(),
			integrationRunInput(runID),
		)
		if err != nil {
			t.Fatalf("Run(%s): %v", runID, err)
		}
		if result.Disposition != loopapi.DispositionWaitingExternal ||
			result.ReasonCode != reasonCompositeFamilyCanceled {
			t.Fatalf("Run(%s) result=%+v", runID, result)
		}
	}
	if invoker.callCount() != 0 {
		t.Fatalf("adapter calls=%d want 0", invoker.callCount())
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	for index, child := range fixture.compiled.Children {
		assertNoCompositeModelAttempt(
			t,
			fixture,
			fixture.childRunID(index),
			child.MemberSnapshot.MemberID,
			pureChatLogicalStepID,
		)
	}
}

func TestCompositeChildUsesTheReadyPureChatLoop(t *testing.T) {
	fixture := newCompositeLoopFixture(t, "child-ready")
	invoker := newCompositeEchoInvoker(t, fixture)
	loop := newIntegrationLoop(t, fixture.base, invoker)
	child := fixture.compiled.Children[0]

	result, err := loop.Run(
		context.Background(),
		integrationRunInput(child.RunManifest.RunID),
	)
	if err != nil {
		t.Fatalf("Run(child): %v", err)
	}
	if result.Disposition != loopapi.DispositionTerminated ||
		result.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != 1 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}

	attemptID := compositeModelAttemptID(
		t,
		child.RunManifest.RunID,
		child.MemberSnapshot.MemberID,
		pureChatLogicalStepID,
	)
	record, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord(child): %v", err)
	}
	if record.Attempt.LogicalStepID != pureChatLogicalStepID ||
		record.Attempt.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("child Attempt=%+v", record.Attempt)
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		child.RunManifest.RunID,
		child.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
}

type compositeLoopFixture struct {
	base     *loopIntegrationFixture
	compiled assemblycompiler.CompositeCompileOutput
}

func newCompositeLoopFixture(
	t *testing.T,
	name string,
) *compositeLoopFixture {
	return newCompositeLoopFixtureWithReviewer(t, name, false)
}

func newCompositeReviewerLoopFixture(
	t *testing.T,
	name string,
) *compositeLoopFixture {
	return newCompositeLoopFixtureWithReviewer(t, name, true)
}

func newCompositeLoopFixtureWithReviewer(
	t *testing.T,
	name string,
	reviewerEnabled bool,
) *compositeLoopFixture {
	t.Helper()
	ctx := context.Background()
	base := newLoopIntegrationFixture(
		t,
		"run-composite-seed-"+name,
		"composite "+name,
	)
	basis, control, catalog, err := base.store.LoadPublishedBasis(
		ctx,
		"tenant-loop",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	if len(control.Agents) != 1 || len(control.Profiles) != 1 ||
		len(control.Workspaces) != 1 {
		t.Fatalf(
			"base Control agents=%d profiles=%d workspaces=%d",
			len(control.Agents),
			len(control.Profiles),
			len(control.Workspaces),
		)
	}

	analysisAgent := corecontract.AgentRef{
		ID: "agent-composite-analysis", Version: "v1",
		Digest: strings.Repeat("d", 64),
	}
	reviewAgent := corecontract.AgentRef{
		ID: "agent-composite-review", Version: "v1",
		Digest: strings.Repeat("e", 64),
	}
	analysisProfile := cloneCompositeLoopProfile(control.Profiles[0])
	analysisProfile.Profile = corecontract.ProfileRef{
		ID: "profile-composite-analysis", Version: "v1",
		Digest: strings.Repeat("f", 64),
	}
	reviewProfile := cloneCompositeLoopProfile(control.Profiles[0])
	reviewProfile.Profile = corecontract.ProfileRef{
		ID: "profile-composite-review", Version: "v1",
		Digest: strings.Repeat("0", 64),
	}
	reviewerAgent := corecontract.AgentRef{
		ID: "agent-composite-reviewer", Version: "v1",
		Digest: strings.Repeat("1", 64),
	}
	reviewerProfile := cloneCompositeLoopProfile(control.Profiles[0])
	reviewerProfile.Profile = corecontract.ProfileRef{
		ID: "profile-composite-reviewer", Version: "v1",
		Digest: strings.Repeat("2", 64),
	}
	control.SnapshotID = "control-loop-composite-" + name
	control.Revision++
	control.Agents = append(control.Agents, analysisAgent, reviewAgent)
	control.Profiles = append(
		control.Profiles,
		analysisProfile,
		reviewProfile,
	)
	control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{{
		SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
		AgentID:              control.Agents[0].ID,
		CoordinatorProfileID: control.Profiles[0].Profile.ID,
		Members: []controlcontract.CompositeAgentMemberV1{
			{
				SlotID:            "slot-analysis",
				AgentID:           analysisAgent.ID,
				ProfileID:         analysisProfile.Profile.ID,
				FocusID:           "analysis",
				WeightBasisPoints: 6000,
			},
			{
				SlotID:            "slot-review",
				AgentID:           reviewAgent.ID,
				ProfileID:         reviewProfile.Profile.ID,
				FocusID:           "review",
				WeightBasisPoints: 4000,
			},
		},
	}}
	if reviewerEnabled {
		control.Agents = append(control.Agents, reviewerAgent)
		control.Profiles = append(control.Profiles, reviewerProfile)
		control.CompositeAgents[0].Reviewer =
			&controlcontract.CompositeReviewerDefinitionV1{
				SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
				AgentID:         reviewerAgent.ID,
				ProfileID:       reviewerProfile.Profile.ID,
				MaxOutputTokens: 512,
				Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
			}
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot(composite): %v", err)
	}

	catalog.GenerationID = "catalog-loop-composite-" + name
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration(composite): %v", err)
	}
	basis, err = base.store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("PublishControlCatalog(composite): %v", err)
	}

	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          base.taskText,
		},
	)
	if err != nil {
		t.Fatalf("NewTaskInputV1: %v", err)
	}
	task := newIntegrationContent(
		t,
		currentstore.ContentTaskInput,
		taskCanonical,
	)
	modelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
			SchemaVersion:  corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:       "tenant-loop",
			AdmissionKey:   "admission-composite-" + name,
			PrincipalID:    "principal-loop",
			WorkspaceID:    control.Workspaces[0].Workspace.ID,
			AgentID:        control.Agents[0].ID,
			ProfileID:      control.Profiles[0].Profile.ID,
			TaskInputRef:   task.Digest,
			RequestedPorts: []moduleapi.PortRef{modelPort},
			Deadline: time.Now().UTC().Add(time.Hour).
				Truncate(time.Microsecond),
			CancellationScope: corecontract.CancellationScopeFamilyV1,
			ExplicitLimits:    json.RawMessage(`{}`),
		})
	if err != nil {
		t.Fatalf("NewAdmissionIntentV1(composite): %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		ctx,
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  intentCanonical,
				IntentDigest:     intentDigest,
				RunID:            "run-composite-root-" + name,
				MemberID:         "member-composite-root",
				RecoveryRootRef:  "recovery/composite/" + name,
				PublishedBasis:   basis,
				ControlCanonical: controlCanonical,
				CatalogCanonical: catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("CompileCompositeFamily: %v", err)
	}
	commit := currentstore.CommitCompositeRunFamilyInput{
		Parent: compositeLoopCommitInput(
			basis,
			compiled.Parent,
			intentCanonical,
			intentDigest,
			task,
		),
		Children: make(
			[]currentstore.CommitRunAdmissionInput,
			len(compiled.Children),
		),
	}
	for index, child := range compiled.Children {
		commit.Children[index] = compositeLoopCommitInput(
			basis,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
			task,
		)
	}
	if compiled.Reviewer != nil {
		reviewer := compositeLoopCommitInput(
			basis,
			compiled.Reviewer.CompileOutput,
			compiled.Reviewer.IntentCanonical,
			compiled.Reviewer.IntentDigest,
			task,
		)
		commit.Reviewer = &reviewer
	}
	if _, err := base.store.CommitCompositeRunFamily(ctx, commit); err != nil {
		t.Fatalf("CommitCompositeRunFamily: %v", err)
	}
	return &compositeLoopFixture{base: base, compiled: compiled}
}

func compositeLoopCommitInput(
	basis controlcontract.PublishedBasis,
	compiled assemblycompiler.CompileOutput,
	intentCanonical []byte,
	intentDigest string,
	task currentstore.ContentInput,
) currentstore.CommitRunAdmissionInput {
	return currentstore.CommitRunAdmissionInput{
		PublishedBasis:          basis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents:                []currentstore.ContentInput{task},
	}
}

func cloneCompositeLoopProfile(
	input controlcontract.ProfileDefinition,
) controlcontract.ProfileDefinition {
	cloned := input
	if input.ModelProfile != nil {
		modelProfile := *input.ModelProfile
		cloned.ModelProfile = &modelProfile
	}
	cloned.Bindings = append([]controlcontract.BindingSpec(nil), input.Bindings...)
	for index := range cloned.Bindings {
		cloned.Bindings[index].StaticContextRefs = append(
			[]string(nil),
			input.Bindings[index].StaticContextRefs...,
		)
	}
	return cloned
}

func newCompositeEchoInvoker(
	t *testing.T,
	fixture *compositeLoopFixture,
) *integrationInvoker {
	t.Helper()
	return &integrationInvoker{delegate: &compositeResultInvoker{
		provider: fixture.base.provider,
	}}
}

type compositeResultInvoker struct {
	provider moduleapi.ActivatedModuleRef
}

type oversizedCompositeResultInvoker struct {
	provider moduleapi.ActivatedModuleRef
	text     string
}

func (invoker *oversizedCompositeResultInvoker) Invoke(
	_ context.Context,
	_ modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: invoker.text,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		Provider: invoker.provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   canonical,
	}, nil
}

func (invoker *compositeResultInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: "result for " + prepared.Invocation.RunID,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		Provider: invoker.provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   canonical,
	}, nil
}

func (fixture *compositeLoopFixture) rootRunID() string {
	return fixture.compiled.Parent.RunManifest.RunID
}

func (fixture *compositeLoopFixture) childRunID(index int) string {
	return fixture.compiled.Children[index].RunManifest.RunID
}

func (fixture *compositeLoopFixture) reviewerRunID() string {
	if fixture.compiled.Reviewer == nil {
		return ""
	}
	return fixture.compiled.Reviewer.RunManifest.RunID
}

func compositeModelAttemptID(
	t *testing.T,
	runID string,
	memberID string,
	logicalStepID string,
) string {
	t.Helper()
	operationKey, err := corecontract.ModelLogicalOperationKey(
		runID,
		memberID,
		logicalStepID,
	)
	if err != nil {
		t.Fatalf("ModelLogicalOperationKey: %v", err)
	}
	return modelAttemptIDPrefix + operationKey
}

func assertNoCompositeModelAttempt(
	t *testing.T,
	fixture *compositeLoopFixture,
	runID string,
	memberID string,
	logicalStepID string,
) {
	t.Helper()
	attemptID := compositeModelAttemptID(
		t,
		runID,
		memberID,
		logicalStepID,
	)
	if record, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	); err == nil {
		t.Fatalf("unexpected model Attempt=%+v", record.Attempt)
	}
}

func loadCompositeRunForLoop(
	t *testing.T,
	fixture *compositeLoopFixture,
	runID string,
) currentstore.RunForLoop {
	t.Helper()
	ctx := context.Background()
	lease, err := fixture.base.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "inspect-composite-loop",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease(%s): %v", runID, err)
	}
	run, loadErr := fixture.base.store.LoadRunForLoop(ctx, lease)
	releaseErr := fixture.base.store.ReleaseRunLease(ctx, lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf(
			"LoadRunForLoop(%s) error=%v ReleaseRunLease error=%v",
			runID,
			loadErr,
			releaseErr,
		)
	}
	return run
}
