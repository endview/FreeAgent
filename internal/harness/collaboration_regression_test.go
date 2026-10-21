package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollaborationInvalidResponseRetainsUnknownReservation(t *testing.T) {
	collab, _, plan := collaborationFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: "incomplete", Usage: &ModelUsage{OutputTokens: -1}}, nil
	}))
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if err == nil || report.Parent.Status != "unknown" || report.Children[0].Run.Status != "unknown" ||
		report.Usage.ModelCalls != 1 || report.Usage.OutputBytes != plan.Roles["reviewer"].Budget.MaxCallOutputBytes {
		t.Fatalf("invalid response released UNKNOWN capacity: status=%s usage=%+v err=%v", report.Parent.Status, report.Usage, err)
	}
}

func TestCollaborationProviderFailureRetainsReservationAndRetiredWork(t *testing.T) {
	for _, streamed := range []int{0, 128, 5000} {
		t.Run(fmt.Sprintf("streamed_%d", streamed), func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, _ ModelRequest, emit func(string)) (ModelResponse, error) {
				if streamed != 0 {
					emit(strings.Repeat("x", streamed))
				}
				return ModelResponse{Content: "partial"},
					&CallError{Certain: true, Category: "provider", Code: "server_error", Message: "definite rejection"}
			}))
			plan = analysisPlan(plan)
			id := createCollaboration(t, collab, plan)
			if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
				t.Fatal(err)
			}
			before, err := collab.Continue(t.Context(), id)
			if err == nil || before.Children[0].Run.Status != "failed" || before.Usage.ModelCalls != 1 {
				t.Fatalf("failure was not retained: usage=%+v err=%v", before.Usage, err)
			}
			childRun := before.Children[0].Run.ID
			attempts, err := core.Store.ListAttempts(t.Context(), childRun)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "finished" {
				t.Fatalf("failure attempt: %+v %v", attempts, err)
			}
			var response ModelResponse
			if json.Unmarshal([]byte(attempts[0].ResponseJSON), &response) != nil || response.Failure == nil ||
				!response.Failure.Certain || response.Failure.Code != "server_error" || response.Content != "partial" {
				t.Fatal("structured failure or observed response was lost")
			}
			want := max(plan.Roles["reviewer"].Budget.MaxCallOutputBytes, streamed, len(attempts[0].ResponseJSON))
			record, err := loadCollaboration(t.Context(), core.Store.db, id)
			if err != nil || before.Usage.OutputBytes != want || recordChild(record, "one").OutputBytes != want {
				t.Fatalf("failure refunded output: usage=%+v want=%d err=%v", before.Usage, want, err)
			}
			collab = reopenCollaboration(t, core)
			if err := collab.Revise(t.Context(), id, CollaborationEdit{ExpectedRevision: 1, Reason: "retain failed work",
				Retire: []string{"one"}}); err != nil {
				t.Fatal(err)
			}
			after, err := collab.Inspect(t.Context(), id)
			saved, loadErr := core.Store.ListAttempts(t.Context(), childRun)
			if err != nil || loadErr != nil || after.Usage != before.Usage || !after.Children[0].Retired ||
				after.Children[0].Run.Status != "failed" || len(saved) != 1 || saved[0].ResponseJSON != attempts[0].ResponseJSON {
				t.Fatalf("reopen/retirement erased failure: usage=%+v err=%v/%v", after.Usage, err, loadErr)
			}
		})
	}
}

func TestCollaborationOutputSettlementRequiresDurableResult(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	id := createCollaboration(t, collab, plan)
	if err := collab.activate(t.Context(), id, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	record, err := loadCollaboration(t.Context(), core.Store.db, id)
	if err != nil {
		t.Fatal(err)
	}
	child := recordChild(record, "one")
	run, err := core.Store.GetRun(t.Context(), child.RunID)
	if err != nil {
		t.Fatal(err)
	}
	guard := collaborationGuard{parentID: id, childName: "one"}
	if err := core.Store.FinishRun(t.Context(), run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	attempt, err := core.Store.startGuardedModel(t.Context(), run, `{}`, 6, guard)
	if err != nil {
		t.Fatal(err)
	}
	response := ModelResponse{Content: "known result"}
	raw, _ := json.Marshal(response)
	service := *core
	service.guard = guard
	if err := service.recordOutput(t.Context(), attempt, len(raw), false); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER reject_result BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'result persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.saveGuardedModel(t.Context(), run, attempt, response, guard, len(raw)); err == nil {
		t.Fatal("result persistence fault was ignored")
	}
	before, err := collab.Inspect(t.Context(), id)
	if err != nil || before.Usage.OutputBytes != 4096 {
		t.Fatalf("failed save released capacity: usage=%+v err=%v", before.Usage, err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `DROP TRIGGER reject_result`); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.saveGuardedModel(t.Context(), run, attempt, response, guard, len(raw)); err != nil {
		t.Fatal(err)
	}
	after, err := collab.Inspect(t.Context(), id)
	if err != nil || after.Usage.OutputBytes != len(raw) || after.Children[0].Run.Status != "completed" {
		t.Fatalf("durable result was not settled: usage=%+v err=%v", after.Usage, err)
	}
}

func TestCollaborationDefinitiveOverrunIsAtomicallyLimited(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	id := createCollaboration(t, collab, plan)
	if err := collab.activate(t.Context(), id, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	record, err := loadCollaboration(t.Context(), core.Store.db, id)
	if err != nil {
		t.Fatal(err)
	}
	run, err := core.Store.GetRun(t.Context(), recordChild(record, "one").RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Store.FinishRun(t.Context(), run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	guard := collaborationGuard{parentID: id, childName: "one"}
	attempt, err := core.Store.startGuardedModel(t.Context(), run, `{}`, 6, guard)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Store.saveGuardedModel(t.Context(), run, attempt, ModelResponse{Content: "known final"}, guard, 5000); !errors.Is(err, ErrChildBudget) {
		t.Fatal(err)
	}
	report, err := collab.Inspect(t.Context(), id)
	if err != nil || report.Children[0].Run.Status != "limited" || report.Usage.OutputBytes != 5000 {
		t.Fatalf("overrun completion escaped: usage=%+v err=%v", report.Usage, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "finished" {
		t.Fatalf("definite result lost: attempts=%+v err=%v", attempts, err)
	}
}

func TestCollaborationScopesDoNotFoldDistinctPathNames(t *testing.T) {
	tools := assignedTools{paths: []string{"Selected"}}
	for _, denied := range []string{"selected", "SELECTED/secret.txt", "SelectedElsewhere/a.txt", "Selected/../other.txt"} {
		if tools.allows(denied) {
			t.Fatalf("unauthorized path %q matched exact subtree", denied)
		}
	}
	for _, allowed := range []string{"Selected", "Selected/a.txt", `Selected\child\a.txt`} {
		if !tools.allows(allowed) {
			t.Fatalf("authorized path %q was rejected", allowed)
		}
	}
}

func TestCollaborationBackupRestoreRetainsPlanAndCounters(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	id := createCollaboration(t, collab, plan)
	if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
		t.Fatal(err)
	}
	before, err := collab.Continue(t.Context(), id)
	if err != nil || before.Usage.ModelCalls != 2 {
		t.Fatalf("before=%+v %v", before, err)
	}
	base := t.TempDir()
	backup := filepath.Join(base, "backup")
	restored := filepath.Join(base, "restored")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err := OpenTasks(t.Context(), restored, Adapters{Model: modelFunc(readingChild)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	other := NewCollaboration(tasks)
	report, err := other.Inspect(t.Context(), id)
	if err != nil || report.Usage != before.Usage || report.Children[1].Control != "paused" {
		t.Fatalf("restored=%+v %v", report, err)
	}
	if err := other.Control(t.Context(), id, "two", "resume"); err != nil {
		t.Fatal(err)
	}
	report, err = other.Continue(t.Context(), id)
	// The original fixture's in-memory private instruction is not configured in
	// the restored runtime; restoring must not silently accept config drift.
	if !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("snapshot drift was ignored=%+v %v", report, err)
	}
}

func TestCollaborationRestoredKnownWorkContinuesWithoutReplay(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	core.Config.instructionText = ""
	id := createCollaboration(t, collab, plan)
	if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
		t.Fatal(err)
	}
	before, err := collab.Continue(t.Context(), id)
	if err != nil || before.Usage.ModelCalls != 2 || before.Usage.ToolCalls != 1 {
		t.Fatalf("before=%+v err=%v", before.Usage, err)
	}
	base := t.TempDir()
	backup, restored := filepath.Join(base, "backup"), filepath.Join(base, "restored")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err := OpenTasks(t.Context(), restored, Adapters{Model: modelFunc(readingChild)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	other := NewCollaboration(tasks)
	if err := other.Control(t.Context(), id, "two", "resume"); err != nil {
		t.Fatal(err)
	}
	after, err := other.Continue(t.Context(), id)
	if err != nil || after.Parent.Status != "completed" || after.Usage.ModelCalls != 4 || after.Usage.ToolCalls != 2 ||
		after.Children[0].Run.Steps != before.Children[0].Run.Steps {
		t.Fatalf("restored known work replayed: usage=%+v err=%v", after.Usage, err)
	}
}

type uncertainAssignedRead struct{ ToolRunner }

func (tools uncertainAssignedRead) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := tools.ToolRunner.Prepare(ctx, call)
	if err != nil {
		return prepared, err
	}
	prepared.Execute = func(context.Context) (ToolResult, error) {
		return ToolResult{Content: "partial observation"}, errors.New("result lost after dispatch")
	}
	return prepared, nil
}

func TestCollaborationUnknownToolKeepsBudgetAndStopsDependencies(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	core.Tools = uncertainAssignedRead{core.Tools}
	plan.Children[1].DependsOn = []string{"one"}
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrUnknownResult) || report.Parent.Status != "unknown" || report.Usage.ToolCalls != 1 ||
		report.Usage.ModelCalls != 1 || report.Usage.OutputBytes < 4096 || report.Children[1].Run.Steps != 0 {
		t.Fatalf("unknown tool budget/dependency escaped: usage=%+v err=%v", report.Usage, err)
	}
	before := report.Usage
	report, err = collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrUnknownResult) || report.Usage != before {
		t.Fatalf("unknown tool was replayed: usage=%+v err=%v", report.Usage, err)
	}
}

type oversizedAssignedProgress struct{ ToolRunner }

func (tools oversizedAssignedProgress) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := tools.ToolRunner.Prepare(ctx, call)
	if err != nil {
		return prepared, err
	}
	prepared.Execute = nil
	prepared.ExecuteRecorded = func(_ context.Context, record func(ToolResult) error) (ToolResult, error) {
		// Even an adapter ignoring the callback error cannot hide prior output.
		_ = record(ToolResult{Content: strings.Repeat("x", 5000)})
		return ToolResult{Content: "small final"}, nil
	}
	return prepared, nil
}

func TestCollaborationToolProgressOverrunCannotBeHiddenByFinal(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	core.Tools = oversizedAssignedProgress{core.Tools}
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrChildBudget) || report.Parent.Status == "completed" || report.Parent.Status == "unknown" ||
		report.Children[0].Run.Status != "limited" || report.Usage.ToolCalls != 1 || report.Usage.OutputBytes < 5000 {
		t.Fatalf("tool hid oversized progress: status=%s usage=%+v err=%v", report.Parent.Status, report.Usage, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), report.Children[0].Run.ID)
	if err != nil || len(attempts) != 2 || attempts[1].Status != "finished" {
		t.Fatalf("known tool result lost: attempts=%+v err=%v", attempts, err)
	}
}

func TestCollaborationParallelBudgetReservationsAreAtomic(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	collab, _, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return readingChild(ctx, req, emit)
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}))
	plan.Parallel = true
	plan.Budget.MaxOutputBytes = 4096
	id := createCollaboration(t, collab, plan)
	done := make(chan error, 1)
	go func() { _, err := collab.Continue(t.Context(), id); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first dispatch did not start")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		report, err := collab.Inspect(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if report.Children[0].Run.Status == "limited" || report.Children[1].Run.Status == "limited" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parallel reservation did not stop second dispatch")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-entered:
		t.Fatal("two model calls consumed one output reservation")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, ErrParentBudget) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("budgeted batch did not finish")
	}
}

func TestOutputBudgetMustNotMaskAccountingFailure(t *testing.T) {
	if isOutputLimit(errors.Join(ErrParentBudget, errors.New("database commit failed"))) {
		t.Fatal("persistence failure was treated as a known limit")
	}
	if !isOutputLimit(errors.Join(ErrChildBudget, ErrParentBudget)) {
		t.Fatal("known limits were not classified")
	}
}
