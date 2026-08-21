package localchat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/runscheduler"
	"github.com/endview/freeagent/sdk/loopapi"
)

func TestPureChatFairSchedulerRunsThreeWorkspacesAndReusesTerminalRuns(t *testing.T) {
	fixture := newChatServiceFixture(t)
	workspaceIDs := addScheduledChatWorkspaces(t, fixture)
	config := runscheduler.DefaultConfig(fixture.tenantID)
	config.Limits = currentstore.FairSchedulerLimits{
		GlobalWorkers:         2,
		MaxActivePerWorkspace: 1,
		MaxActivePerFamily:    1,
	}
	config.PollInterval = time.Millisecond
	scheduler, err := runscheduler.New(fixture.store, fixture.loop, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scheduler.Close(ctx); err != nil {
			t.Errorf("close Scheduler: %v", err)
		}
	})
	service, err := NewChatService(fixture.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	results := make([]ChatResult, len(workspaceIDs))
	errorsSeen := make([]error, len(workspaceIDs))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, workspaceID := range workspaceIDs {
		index := index
		workspaceID := workspaceID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			input := fixture.input(
				fmt.Sprintf("scheduler-workspace-request-%d", index),
				fmt.Sprintf("workspace answer %d", index),
				deadline,
			)
			input.WorkspaceID = workspaceID
			results[index], errorsSeen[index] = service.Chat(
				context.Background(),
				input,
			)
		}()
	}
	close(start)
	wait.Wait()
	for index, result := range results {
		if errorsSeen[index] != nil || !result.AdmissionCreated ||
			result.LoopResult.Disposition != loopapi.DispositionTerminated ||
			result.Reply != fmt.Sprintf("workspace answer %d", index) {
			t.Fatalf("Workspace %d result=%+v error=%v", index, result, errorsSeen[index])
		}
		state, found, stateErr := fixture.store.GetWorkspaceSchedulerState(
			context.Background(),
			fixture.tenantID,
			workspaceIDs[index],
		)
		if stateErr != nil || !found || state.ServedUnits != 1 || state.Revision != 1 {
			t.Fatalf("Workspace %d Scheduler state=(%+v,%v,%v)", index, state, found, stateErr)
		}
	}
	if got := fixture.invoker.callCount(); got != len(workspaceIDs) {
		t.Fatalf("multi-Workspace model calls=%d want %d", got, len(workspaceIDs))
	}

	retryInput := fixture.input(
		"scheduler-workspace-request-0",
		"workspace answer 0",
		deadline,
	)
	retryInput.WorkspaceID = workspaceIDs[0]
	retry, err := service.Chat(context.Background(), retryInput)
	if err != nil || retry.AdmissionCreated || retry.Reply != results[0].Reply ||
		fixture.invoker.callCount() != len(workspaceIDs) {
		t.Fatalf("multi-Workspace retry=%+v calls=%d error=%v", retry, fixture.invoker.callCount(), err)
	}
}

func TestCompositeChatServiceRunsThroughFairSchedulerFacade(t *testing.T) {
	fixture := newCompositeChatServiceFixture(t, false)
	scheduler := newCompositeTestScheduler(t, fixture)
	service, err := NewCompositeChatService(fixture.base.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.base.input(
		"composite-scheduler-request",
		"combine scheduled specialists",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("scheduled Composite Chat: %v", err)
	}
	if !first.AdmissionCreated || len(first.Children) != 2 ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		first.Reply != "result for "+first.RootRunID {
		t.Fatalf("scheduled Composite result=%+v", first)
	}
	if !fixture.invoker.parallelChildrenObserved() {
		t.Fatal("Scheduler did not retain bounded Specialist parallelism")
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("scheduled Composite model calls=%d want 3", got)
	}
	state, found, err := fixture.base.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.base.tenantID,
		fixture.base.workspaceID,
	)
	if err != nil || !found || state.ServedUnits != 3 || state.Revision != 3 {
		t.Fatalf("scheduled Composite state=(%+v,%v,%v)", state, found, err)
	}

	retry, err := service.Chat(context.Background(), input)
	if err != nil || retry.AdmissionCreated || retry.Reply != first.Reply ||
		retry.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("scheduled Composite retry=%+v error=%v", retry, err)
	}
	state, found, err = fixture.base.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.base.tenantID,
		fixture.base.workspaceID,
	)
	if err != nil || !found || state.ServedUnits != 3 || state.Revision != 3 ||
		fixture.base.invoker.callCount() != 3 {
		t.Fatalf("retry changed Scheduler/model state=(%+v,%v,%v) calls=%d", state, found, err, fixture.base.invoker.callCount())
	}
}

func TestFairSchedulerDoesNotClaimCompositeRootAfterChildUnknown(t *testing.T) {
	fixture := newCompositeChatServiceFixture(t, true)
	scheduler := newCompositeTestScheduler(t, fixture)
	service, err := NewCompositeChatService(fixture.base.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Chat(
		context.Background(),
		fixture.base.input(
			"composite-scheduler-unknown",
			"keep scheduled uncertainty explicit",
			time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		),
	)
	if err != nil {
		t.Fatalf("scheduled unknown Composite: %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		result.LoopResult.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
		result.TerminalResult != nil || result.Reply != "" {
		t.Fatalf("scheduled unknown result=%+v", result)
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("UNKNOWN path model calls=%d want two Specialists only", got)
	}
	state, found, err := fixture.base.store.GetWorkspaceSchedulerState(
		context.Background(),
		fixture.base.tenantID,
		fixture.base.workspaceID,
	)
	if err != nil || !found || state.ServedUnits != 2 || state.Revision != 2 {
		t.Fatalf("UNKNOWN Scheduler state=(%+v,%v,%v)", state, found, err)
	}
	root := loadCompositeChatRun(t, fixture.base.store, result.RootRunID)
	if len(root.ModelDispatches) != 0 {
		t.Fatalf("UNKNOWN Scheduler created %d root Attempts", len(root.ModelDispatches))
	}
}

func TestFairSchedulerRunsDecisionApproveWithoutClaimingSkippedRepair(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeApproveRoundZero,
	)
	scheduler := newDecisionTestScheduler(t, fixture)
	service, err := NewCompositeChatService(fixture.base.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.base.input(
		"decision-scheduler-approve",
		"approve the scheduled initial contributions",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	result, err := service.Chat(context.Background(), input)
	if err != nil || result.LoopResult.Disposition !=
		loopapi.DispositionTerminated || result.TerminalResult == nil ||
		result.FailureCode != "" || result.RepairReviewer == nil {
		t.Fatalf("scheduled Decision approve=%+v error=%v", result, err)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("scheduled Decision approve model calls=%d want 4", got)
	}
	invoked := fixture.invoker.runIDsSnapshot()
	skippedRunIDs := make([]string, 0, len(result.RepairChildren)+1)
	for _, repair := range result.RepairChildren {
		if containsDecisionRunID(invoked, repair.RunID) {
			t.Fatalf("skipped repair Child %q was invoked: %v", repair.RunID, invoked)
		}
		skippedRunIDs = append(skippedRunIDs, repair.RunID)
	}
	if containsDecisionRunID(invoked, result.RepairReviewer.RunID) {
		t.Fatalf("skipped repair Reviewer was invoked: %v", invoked)
	}
	skippedRunIDs = append(skippedRunIDs, result.RepairReviewer.RunID)
	assertDecisionRunsHaveNoModelAttempts(
		t,
		fixture.base.store,
		result.RootRunID,
		skippedRunIDs,
	)
	assertSchedulerServedUnits(
		t,
		fixture.base.store,
		fixture.base.tenantID,
		fixture.base.workspaceID,
		5,
	)
}

func TestFairSchedulerRunsOnlyActivatedDecisionRepairFrontier(t *testing.T) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneApprove,
	)
	scheduler := newDecisionTestScheduler(t, fixture)
	service, err := NewCompositeChatService(fixture.base.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.base.input(
		"decision-scheduler-repair-one",
		"schedule only the affected repair contribution",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	result, err := service.Chat(context.Background(), input)
	if err != nil || result.LoopResult.Disposition !=
		loopapi.DispositionTerminated || result.TerminalResult == nil ||
		result.FailureCode != "" || result.RepairReviewer == nil {
		t.Fatalf("scheduled Decision repair=%+v error=%v", result, err)
	}
	if got := fixture.base.invoker.callCount(); got != 6 {
		t.Fatalf("scheduled Decision repair model calls=%d want 6", got)
	}
	invoked := fixture.invoker.runIDsSnapshot()
	activated := 0
	skippedRunIDs := make([]string, 0, len(result.RepairChildren)-1)
	for _, repair := range result.RepairChildren {
		if containsDecisionRunID(invoked, repair.RunID) {
			activated++
			continue
		}
		skippedRunIDs = append(skippedRunIDs, repair.RunID)
	}
	if activated != 1 || !containsDecisionRunID(
		invoked,
		result.RepairReviewer.RunID,
	) {
		t.Fatalf("scheduled repair frontier differs: runs=%v result=%+v", invoked, result)
	}
	assertDecisionRunsHaveNoModelAttempts(
		t,
		fixture.base.store,
		result.RootRunID,
		skippedRunIDs,
	)
	assertSchedulerServedUnits(
		t,
		fixture.base.store,
		fixture.base.tenantID,
		fixture.base.workspaceID,
		8,
	)
}

func TestFairSchedulerRunsDecisionWorkspaceTransferWithoutClaimingDormantTargetRepair(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeApproveRoundZero,
	)
	targetWorkspaceID := publishDecisionWorkspaceTransferV1(
		t,
		fixture,
		"slot-analysis",
	)
	scheduler := newDecisionTestScheduler(t, fixture)
	service, err := NewCompositeChatService(fixture.base.store, scheduler)
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.base.input(
		"decision-scheduler-workspace-transfer",
		"schedule one bounded cross-workspace contribution",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	result, err := service.Chat(context.Background(), input)
	if err != nil || result.LoopResult.Disposition !=
		loopapi.DispositionTerminated || result.TerminalResult == nil ||
		result.FailureCode != "" || result.RepairReviewer == nil {
		t.Fatalf("scheduled Workspace transfer=%+v error=%v", result, err)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("scheduled Workspace transfer model calls=%d want 4", got)
	}
	transferredInitial := 0
	transferredIndex := -1
	for index, child := range result.Children {
		run := loadCompositeChatRun(t, fixture.base.store, child.RunID)
		if run.Member.Workspace.ID == targetWorkspaceID {
			if run.WorkspaceTransfer == nil || len(run.ModelDispatches) != 1 {
				t.Fatalf("target Workspace initial closure=%+v", run)
			}
			transferredInitial++
			transferredIndex = index
		}
	}
	if transferredInitial != 1 {
		t.Fatalf("transferred initial Specialists=%d want 1", transferredInitial)
	}
	if transferredIndex < 0 || transferredIndex >= len(result.RepairChildren) {
		t.Fatalf("transferred repair index=%d result=%+v", transferredIndex, result)
	}
	targetRepairRunID := result.RepairChildren[transferredIndex].RunID
	invoked := fixture.invoker.runIDsSnapshot()
	if containsDecisionRunID(invoked, targetRepairRunID) {
		t.Fatalf("dormant target repair %q was invoked: %v", targetRepairRunID, invoked)
	}
	assertDecisionRunsHaveNoModelAttempts(
		t,
		fixture.base.store,
		result.RootRunID,
		[]string{targetRepairRunID},
	)
	assertSchedulerServedUnits(
		t,
		fixture.base.store,
		fixture.base.tenantID,
		targetWorkspaceID,
		1,
	)
	assertSchedulerServedUnits(
		t,
		fixture.base.store,
		fixture.base.tenantID,
		fixture.base.workspaceID,
		4,
	)
}

func newCompositeTestScheduler(
	t *testing.T,
	fixture *compositeChatServiceFixture,
) *runscheduler.Scheduler {
	t.Helper()
	config := runscheduler.DefaultConfig(fixture.base.tenantID)
	config.Limits = currentstore.FairSchedulerLimits{
		GlobalWorkers:         2,
		MaxActivePerWorkspace: 2,
		MaxActivePerFamily:    2,
	}
	config.PollInterval = time.Millisecond
	scheduler, err := runscheduler.New(
		fixture.base.store,
		fixture.base.loop,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scheduler.Close(ctx); err != nil {
			t.Errorf("close Scheduler: %v", err)
		}
	})
	return scheduler
}

func newDecisionTestScheduler(
	t *testing.T,
	fixture *decisionCompositeFixture,
) *runscheduler.Scheduler {
	t.Helper()
	config := runscheduler.DefaultConfig(fixture.base.tenantID)
	config.Limits = currentstore.FairSchedulerLimits{
		GlobalWorkers:         2,
		MaxActivePerWorkspace: 2,
		MaxActivePerFamily:    2,
	}
	config.PollInterval = time.Millisecond
	scheduler, err := runscheduler.New(
		fixture.base.store,
		fixture.base.loop,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scheduler.Close(ctx); err != nil {
			t.Errorf("close Decision Scheduler: %v", err)
		}
	})
	return scheduler
}

func assertDecisionRunsHaveNoModelAttempts(
	t *testing.T,
	store *currentstore.Store,
	rootRunID string,
	runIDs []string,
) {
	t.Helper()
	projection, err := store.GetCompositeFamilyUsageProjection(
		context.Background(),
		rootRunID,
	)
	if err != nil {
		t.Fatalf("load family Usage projection: %v", err)
	}
	wanted := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		wanted[runID] = struct{}{}
	}
	for _, run := range projection.Runs {
		if _, ok := wanted[run.RunID]; !ok {
			continue
		}
		if run.Attempt != nil {
			t.Fatalf("Decision Run %q has model Attempt %+v, want nil", run.RunID, run.Attempt)
		}
		delete(wanted, run.RunID)
	}
	if len(wanted) != 0 {
		t.Fatalf("Decision Usage projection omitted Runs: %v", wanted)
	}
}

func assertSchedulerServedUnits(
	t *testing.T,
	store *currentstore.Store,
	tenantID string,
	workspaceID string,
	want uint64,
) {
	t.Helper()
	state, found, err := store.GetWorkspaceSchedulerState(
		context.Background(),
		tenantID,
		workspaceID,
	)
	if err != nil || !found || state.ServedUnits != want ||
		state.Revision != want {
		t.Fatalf(
			"Workspace %q Scheduler state=(%+v,%v,%v), want %d",
			workspaceID,
			state,
			found,
			err,
			want,
		)
	}
}

func addScheduledChatWorkspaces(
	t *testing.T,
	fixture *chatServiceFixture,
) []string {
	t.Helper()
	basis, control, catalog, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	workspaceIDs := []string{
		fixture.workspaceID,
		"workspace-chat-2",
		"workspace-chat-3",
	}
	for index, workspaceID := range workspaceIDs[1:] {
		control.Workspaces = append(
			control.Workspaces,
			controlcontract.WorkspaceDefinition{
				Workspace: corecontract.WorkspaceRef{
					ID:      workspaceID,
					Version: "v1",
					Digest:  strings.Repeat(fmt.Sprintf("%x", index+5), 64),
				},
				BudgetPolicy: control.Workspaces[0].BudgetPolicy,
			},
		)
	}
	control.SnapshotID = "control-chat-scheduler"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-chat-scheduler"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	return workspaceIDs
}
