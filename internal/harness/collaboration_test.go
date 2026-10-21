package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readingChild(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
	if request.Messages[len(request.Messages)-1].Role == "user" {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_read", map[string]string{"path": "facts.txt"})}}, nil
	}
	return ModelResponse{Content: `{"summary":"Read assigned facts.","findings":[]}`}, nil
}

func collaborationFixture(t *testing.T, model ModelClient) (*Collaboration, *Service, CollaborationPlan) {
	t.Helper()
	core, _, workspace := testHarness(t, model)
	core.Config.Collaboration.Mode = "ask"
	core.Config.SystemPrompt = "PARENT_PRIVATE_INSTRUCTIONS"
	core.Config.instructionText = "PARENT_PRIVATE_FILE"
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "facts.txt"), []byte("known facts\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := CollaborationPlan{
		Objective: "Inspect the selected facts",
		Budget:    ParentBudget{MaxChildren: 4, MaxModelCalls: 12, MaxToolCalls: 8, MaxOutputBytes: 65536, MaxConcurrentChildren: 2, MaxActiveSeconds: 60},
		Roles: map[string]RoleProfile{"reviewer": {
			Instructions: "Inspect only the assigned context.",
			Tools:        []string{"file_read"}, Paths: []string{"facts.txt"}, OutputFormat: "json",
			Budget: ChildBudget{MaxSteps: 6, MaxSeconds: 30, MaxOutputBytes: 16384, MaxCallOutputBytes: 4096},
		}},
		Children: []ChildSpec{{Name: "one", Role: "reviewer", Objective: "Read facts", Context: "CHILD_ONE"},
			{Name: "two", Role: "reviewer", Objective: "Review facts", Context: "CHILD_TWO"}},
	}
	return NewCollaboration(NewTaskService(core)), core, plan
}

func createCollaboration(t *testing.T, collab *Collaboration, plan CollaborationPlan) string {
	t.Helper()
	id, err := collab.Create(t.Context(), "", "plan", plan)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCollaborationSerialEvidenceBudgetsAndDeduplication(t *testing.T) {
	var calls atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		calls.Add(1)
		raw, _ := json.Marshal(req)
		if strings.Contains(string(raw), "PARENT_PRIVATE") || strings.Contains(string(raw), planLeakMarker) {
			t.Error("private parent context leaked")
		}
		return readingChild(ctx, req, emit)
	}))
	id := createCollaboration(t, collab, plan)
	before, err := collab.Inspect(t.Context(), id)
	if err != nil || before.Usage.ModelCalls != 0 || len(before.Children) != 2 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	if _, err := core.Run(t.Context(), before.Parent.ConversationID, "early", "bypass"); !errors.Is(err, ErrControlledRun) {
		t.Fatalf("unfinished parent budget bypass=%v", err)
	}
	if _, err := core.Run(t.Context(), before.Children[0].Run.ConversationID, "early", "bypass"); !errors.Is(err, ErrControlledRun) {
		t.Fatalf("unfinished child budget bypass=%v", err)
	}
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 4 || report.Usage.ToolCalls != 2 ||
		report.Usage.ActiveChildren != 0 || report.Usage.OutputBytes <= 0 || len(report.Gaps) != 0 || len(report.Children[0].Observed) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := collab.Check(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	repeated, err := collab.Create(t.Context(), report.Parent.ConversationID, "plan", plan)
	if err != nil || repeated != id {
		t.Fatalf("duplicate=%s err=%v", repeated, err)
	}
	if _, err := collab.Continue(t.Context(), id); err != nil || calls.Load() != 4 {
		t.Fatalf("replayed calls=%d err=%v", calls.Load(), err)
	}
	plan.Objective = "different"
	if _, err := collab.Create(t.Context(), report.Parent.ConversationID, "plan", plan); !errors.Is(err, ErrRequestKey) {
		t.Fatalf("changed key=%v", err)
	}
	if _, err := NewTaskService(core).Continue(t.Context(), report.Children[0].Run.ID); !errors.Is(err, ErrControlledRun) {
		t.Fatalf("budget bypass=%v", err)
	}
	core.Model = EchoModel{}
	ordinary, err := core.Run(t.Context(), report.Parent.ConversationID, "fresh", "ordinary chat")
	if err != nil || ordinary.Run.Status != "completed" || ordinary.Answer != "Echo: ordinary chat" {
		t.Fatalf("completed collaboration kept the parent chat locked: result=%+v err=%v", ordinary, err)
	}
	var tables int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'knowledge_fts%'
		AND name NOT IN ('memories','memory_revisions','memory_operations')`).Scan(&tables); err != nil || tables != 5 {
		t.Fatalf("execution tables=%d err=%v", tables, err)
	}
}

const planLeakMarker = "DO_NOT_PASS_PREVIOUS_PARENT"

func TestCollaborationDisabledAndExplicitScope(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	core.Config.Collaboration.Mode = "off"
	if _, err := collab.Create(t.Context(), "", "", plan); !errors.Is(err, ErrCollaborationDisabled) {
		t.Fatalf("disabled=%v", err)
	}
	result, err := NewTaskService(core).Submit(t.Context(), "", "", "single")
	if err != nil || result.Run.ID == "" {
		t.Fatalf("core unavailable=%v", err)
	}
	core.Config.Collaboration.Mode = "ask"
	tools, err := newAssignedTools(core.Tools, plan.Roles["reviewer"])
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []ToolCall{
		makeCall("read", "file_read", map[string]string{"path": "outside.txt"}),
		makeCall("list", "file_list", map[string]string{"path": "."}),
		makeCall("write", "file_create", map[string]string{"path": "facts.txt", "content": "overwrite"}),
	} {
		if _, err := tools.Prepare(t.Context(), call); err == nil {
			t.Fatalf("scope bypass=%s", call.Name)
		}
	}
	plan.Children[0].DependsOn = []string{"two"}
	plan.Children[1].DependsOn = []string{"one"}
	if _, err := collab.Create(t.Context(), "", "", plan); err == nil {
		t.Fatal("cyclic dependency accepted")
	}
}

func TestCollaborationParentLimitAndUnknownSurviveReopen(t *testing.T) {
	var calls atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{}, errors.New("response lost after dispatch")
	}))
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrUnknownResult) || report.Parent.Status != "unknown" || report.Usage.ModelCalls != 1 ||
		report.Usage.OutputBytes != 4096 || report.Children[1].Run.Steps != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := collab.Continue(t.Context(), id); !errors.Is(err, ErrUnknownResult) || calls.Load() != 1 {
		t.Fatalf("unknown replay=%v calls=%d", err, calls.Load())
	}
	data := core.Store.DataDir()
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	core.Store = store
	collab = NewCollaboration(NewTaskService(core))
	report, err = collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrUnknownResult) || report.Usage.ModelCalls != 1 || report.Usage.OutputBytes != 4096 || calls.Load() != 1 {
		t.Fatalf("reopen=%+v err=%v", report, err)
	}
	if err := collab.Control(t.Context(), id, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	report, err = collab.Inspect(t.Context(), id)
	if err != nil || report.Parent.Status != "abandoned" || len(report.Children[0].Evidence.Uncertainty) == 0 {
		t.Fatalf("lost uncertainty=%+v %v", report, err)
	}
}

func TestCollaborationParentModelToolAndOutputLimits(t *testing.T) {
	for _, which := range []string{"model", "tool", "output", "child_steps"} {
		t.Run(which, func(t *testing.T) {
			collab, _, plan := collaborationFixture(t, modelFunc(readingChild))
			switch which {
			case "model":
				plan.Budget.MaxModelCalls = 1
			case "tool":
				plan.Budget.MaxToolCalls = 1
			case "output":
				plan.Budget.MaxOutputBytes = 4096
				role := plan.Roles["reviewer"]
				role.Budget.MaxCallOutputBytes = 1024
				plan.Roles["reviewer"] = role
			case "child_steps":
				role := plan.Roles["reviewer"]
				role.Budget.MaxSteps = 1
				plan.Roles["reviewer"] = role
			}
			id := createCollaboration(t, collab, plan)
			report, err := collab.Continue(t.Context(), id)
			if which == "output" {
				// Definitive small responses release unused reservation.
				if err != nil || report.Parent.Status != "completed" {
					t.Fatalf("settlement=%+v %v", report, err)
				}
				return
			}
			if err == nil || report.Parent.Status == "completed" {
				t.Fatalf("budget escaped=%+v %v", report, err)
			}
			if report.Usage.ModelCalls > plan.Budget.MaxModelCalls || report.Usage.ToolCalls > plan.Budget.MaxToolCalls {
				t.Fatal("dispatched beyond parent cap")
			}
		})
	}
}

func TestCollaborationParallelReadOnlyAndExclusiveWorkspace(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var inFlight, peak atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		current := inFlight.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		defer inFlight.Add(-1)
		if req.Messages[len(req.Messages)-1].Role == "user" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
		}
		return readingChild(ctx, req, emit)
	}))
	plan.Parallel = true
	id := createCollaboration(t, collab, plan)
	done := make(chan error, 1)
	go func() { _, err := collab.Continue(t.Context(), id); done <- err }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("read-only tasks were not parallel")
		}
	}
	report, err := collab.Inspect(t.Context(), id)
	if err != nil || report.Usage.ActiveChildren != 2 {
		t.Fatalf("capacity=%+v %v", report, err)
	}
	if _, err := NewTaskService(core).Submit(t.Context(), "", "", "unrelated write"); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("workspace lease bypass=%v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parallel tasks stuck")
	}
	if peak.Load() != 2 {
		t.Fatalf("peak=%d", peak.Load())
	}
}

func TestCollaborationPauseAdjustAndParentCancellation(t *testing.T) {
	collab, _, plan := collaborationFixture(t, modelFunc(readingChild))
	id := createCollaboration(t, collab, plan)
	if err := collab.Control(t.Context(), id, "one", "pause"); err != nil {
		t.Fatal(err)
	}
	spec := plan.Children[0]
	spec.Context = "UPDATED_EXPLICIT_CONTEXT"
	if err := collab.Adjust(t.Context(), id, "one", spec); err != nil {
		t.Fatal(err)
	}
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status == "completed" || report.Children[0].Run.Steps != 0 || report.Children[1].Run.Status != "completed" {
		t.Fatalf("paused=%+v %v", report, err)
	}
	if err := collab.Control(t.Context(), id, "one", "resume"); err != nil {
		t.Fatal(err)
	}
	report, err = collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || !strings.Contains(report.Children[0].Run.Prompt, "UPDATED_EXPLICIT_CONTEXT") {
		t.Fatalf("resumed=%+v %v", report, err)
	}
	if err := collab.Adjust(t.Context(), id, "one", spec); err == nil {
		t.Fatal("dispatched child was replaced")
	}

	started := make(chan struct{})
	other, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}))
	parent := createCollaboration(t, other, plan)
	done := make(chan error, 1)
	go func() { _, err := other.Continue(t.Context(), parent); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	if err := other.Control(t.Context(), parent, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknownResult) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not propagate")
	}
	report, err = other.Inspect(t.Context(), parent)
	if err != nil || report.Parent.Status != "abandoned" || report.Children[1].Run.Steps != 0 || report.Usage.ActiveChildren != 0 {
		t.Fatalf("cancel=%+v %v", report, err)
	}
	core.Model = EchoModel{}
	core.Config.Collaboration.Mode = "off"
	if _, err := NewTaskService(core).Submit(t.Context(), "", "", "normal again"); err != nil {
		t.Fatalf("lease leaked: %v", err)
	}
}

func TestCollaborationRequiresEvidenceAndRejectsOversizedOutput(t *testing.T) {
	collab, _, plan := collaborationFixture(t, EchoModel{})
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrMissingEvidence) || report.Parent.Status == "completed" || len(report.Gaps) == 0 {
		t.Fatalf("unverified completion=%+v %v", report, err)
	}
	other, _, plan := collaborationFixture(t, modelFunc(func(_ context.Context, _ ModelRequest, emit func(string)) (ModelResponse, error) {
		emit(strings.Repeat("x", 5000))
		return ModelResponse{Content: "small final"}, nil
	}))
	parent := createCollaboration(t, other, plan)
	report, err = other.Continue(t.Context(), parent)
	if !errors.Is(err, ErrChildBudget) || report.Usage.OutputBytes < 5000 || report.Children[0].Run.Status != "limited" ||
		report.Parent.Status == "unknown" {
		t.Fatalf("oversized=%+v %v", report, err)
	}
}

func TestCollaborationExactChildApprovalAndMergeConflict(t *testing.T) {
	var calls atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		calls.Add(1)
		if req.Messages[len(req.Messages)-1].Role == "user" {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{"path": "new.txt", "content": "created"})}}, nil
		}
		return ModelResponse{Content: `{"summary":"created","findings":[]}`}, nil
	}))
	role := plan.Roles["reviewer"]
	role.Tools = []string{"file_create"}
	role.Paths = []string{"new.txt"}
	plan.Roles["reviewer"] = role
	plan.Children = plan.Children[:1]
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Children[0].Run.Status != "waiting_confirmation" || report.Usage.ToolCalls != 0 {
		t.Fatalf("approval=%+v %v", report, err)
	}
	decision, err := collab.ChildPreview(t.Context(), id, "one")
	if err != nil {
		t.Fatal(err)
	}
	wrong := decision
	wrong.Digest = "wrong"
	if _, err := collab.ChildConfirm(t.Context(), id, "one", wrong, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal(err)
	}
	result, err := collab.ChildConfirm(t.Context(), id, "one", decision, true)
	if err != nil || result.Run.Status != "completed" || len(result.Evidence.Files) != 1 {
		t.Fatalf("confirmed=%+v %v", result, err)
	}
	report, err = collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || report.Usage.ToolCalls != 1 {
		t.Fatalf("merge=%+v %v", report, err)
	}
	if err := core.Store.Abandon(t.Context(), result.Run.ID); !errors.Is(err, ErrControlledRun) {
		t.Fatalf("controlled abandon=%v", err)
	}

	var reads atomic.Int32
	other, source, plan := collaborationFixture(t, modelFunc(readingChild))
	// The fixture writes between serial child reads, after the first receipt.
	other.tasks.core.Model = modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		if req.Messages[len(req.Messages)-1].Role == "user" && reads.Add(1) == 2 {
			if err := os.WriteFile(filepath.Join(source.Config.Workspace, "facts.txt"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return readingChild(ctx, req, emit)
	})
	parent := createCollaboration(t, other, plan)
	report, err = other.Continue(t.Context(), parent)
	if !errors.Is(err, ErrMergeDecision) || report.Parent.Status != "waiting_confirmation" || len(report.Conflicts) != 1 {
		t.Fatalf("conflict=%+v %v", report, err)
	}
	if err := other.Resolve(t.Context(), parent, "stale", "accept both snapshots"); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal(err)
	}
	if err := other.Resolve(t.Context(), parent, report.Digest, "Snapshots changed between reads; retain both."); err != nil {
		t.Fatal(err)
	}
	report, err = other.Continue(t.Context(), parent)
	if err != nil || report.Parent.Status != "completed" || report.Decision == "" || len(report.Conflicts) != 1 {
		t.Fatalf("resolved=%+v %v", report, err)
	}
}
