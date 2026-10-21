package harness

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func analysisPlan(plan CollaborationPlan) CollaborationPlan {
	role := plan.Roles["reviewer"]
	role.Tools, role.Paths = nil, nil
	role.OutputFormat, role.Acceptance = "text", "analysis"
	plan.Roles["reviewer"] = role
	return plan
}

func reopenCollaboration(t *testing.T, core *Service) *Collaboration {
	t.Helper()
	data := core.Store.DataDir()
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	core.Store = store
	return NewCollaboration(NewTaskService(core))
}

func TestCollaborationDAGDeclarationOrderAndCycleRefusal(t *testing.T) {
	var order []string
	collab, _, plan := collaborationFixture(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		prompt := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(prompt, "CHILD_ONE") {
			order = append(order, "one")
		} else {
			order = append(order, "two")
		}
		return ModelResponse{Content: "analysis"}, nil
	}))
	plan = analysisPlan(plan)
	plan.Children[0].DependsOn = []string{"two"}
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || !reflect.DeepEqual(order, []string{"two", "one"}) {
		t.Fatalf("out of order DAG: order=%v status=%s err=%v", order, report.Parent.Status, err)
	}
	for _, dependencies := range [][]string{{"one"}, {"one", "one"}, {"missing"}} {
		plan.Children[1].DependsOn = dependencies
		if _, err := collab.Create(t.Context(), "", "", plan); err == nil {
			t.Fatalf("invalid dependencies accepted: %v", dependencies)
		}
	}
}

func TestCollaborationRevisionsSplitMergeReopenAndCumulativeCreation(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	plan = analysisPlan(plan)
	plan.Budget.MaxChildren = 5
	id := createCollaboration(t, collab, plan)
	edit := CollaborationEdit{ExpectedRevision: 1, Reason: "split undispatched work", Retire: []string{"one"},
		Upsert: []ChildSpec{{Name: "partA", Role: "reviewer", Objective: "First part", DependsOn: []string{"two"}},
			{Name: "partB", Role: "reviewer", Objective: "Second part", DependsOn: []string{"two"}}}}
	if err := collab.Revise(t.Context(), id, edit); err != nil {
		t.Fatal(err)
	}
	before, _ := collab.Inspect(t.Context(), id)
	if before.Revision != 2 || before.Usage.Children != 4 || !before.Children[0].Retired || before.Children[0].Run.Status != "abandoned" {
		t.Fatalf("split: %+v", before)
	}
	if err := collab.Revise(t.Context(), id, edit); !errors.Is(err, ErrPlanRevision) {
		t.Fatalf("stale revision=%v", err)
	}
	edit = CollaborationEdit{ExpectedRevision: 2, Reason: "merge two undispatched parts",
		Retire: []string{"partA", "partB"},
		Upsert: []ChildSpec{{Name: "merged", Role: "reviewer", Objective: "Merged analysis", DependsOn: []string{"two"}}}}
	if err := collab.Revise(t.Context(), id, edit); err != nil {
		t.Fatal(err)
	}
	edit = CollaborationEdit{ExpectedRevision: 3, Reason: "would exceed lifetime creation allowance",
		Upsert: []ChildSpec{{Name: "extra", Role: "reviewer", Objective: "More work"}}}
	if err := collab.Revise(t.Context(), id, edit); !errors.Is(err, ErrParentBudget) {
		t.Fatalf("creation cap bypass=%v", err)
	}
	collab = reopenCollaboration(t, core)
	revisions, err := collab.Revisions(t.Context(), id)
	if err != nil || len(revisions) != 3 || revisions[0].Plan.Children[0].Name != "one" ||
		revisions[2].Revision != 3 || len(revisions[2].Retired) != 3 {
		t.Fatalf("revision recovery: %+v %v", revisions, err)
	}
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || report.Usage.Children != 5 ||
		report.Usage.ModelCalls != 2 || report.Children[0].Run.Steps != 0 || report.Children[2].Run.Steps != 0 {
		t.Fatalf("retired work dispatched: usage=%+v status=%s err=%v", report.Usage, report.Parent.Status, err)
	}
	if duplicate, err := collab.Create(t.Context(), report.Parent.ConversationID, "plan", plan); err != nil || duplicate != id {
		t.Fatalf("original request identity lost after revisions: %s %v", duplicate, err)
	}
}

func TestCollaborationRevisionNeverRewritesStartedOrUnknownResults(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				if outcome == "failed" {
					return ModelResponse{}, &CallError{Certain: true, Message: "definite rejection"}
				}
				if outcome == "unknown" {
					return ModelResponse{}, errors.New("lost response")
				}
				return ModelResponse{Content: "saved result"}, nil
			}))
			plan = analysisPlan(plan)
			id := createCollaboration(t, collab, plan)
			if err := collab.Control(t.Context(), id, "two", "pause"); err != nil {
				t.Fatal(err)
			}
			before, _ := collab.Continue(t.Context(), id)
			attempts, _ := core.Store.ListAttempts(t.Context(), before.Children[0].Run.ID)
			spec := plan.Children[0]
			spec.Objective = "replace already attempted work"
			if err := collab.Revise(t.Context(), id, CollaborationEdit{ExpectedRevision: 1, Reason: "overwrite", Upsert: []ChildSpec{spec}}); err == nil {
				t.Fatal("started assignment was rewritten")
			}
			after, _ := collab.Inspect(t.Context(), id)
			saved, _ := core.Store.ListAttempts(t.Context(), before.Children[0].Run.ID)
			if after.Usage != before.Usage || after.Revision != before.Revision || !reflect.DeepEqual(saved, attempts) ||
				after.Children[0].Run.Prompt != before.Children[0].Run.Prompt {
				t.Fatal("rejected revision changed consumption or saved work")
			}
			if outcome == "failed" {
				edit := CollaborationEdit{ExpectedRevision: 1, Reason: "retain failure, create explicit replacement",
					Retire: []string{"one"}, Upsert: []ChildSpec{{Name: "replacement", Role: "reviewer", Objective: "New work"}}}
				if err := collab.Revise(t.Context(), id, edit); err != nil {
					t.Fatal(err)
				}
				after, _ = collab.Inspect(t.Context(), id)
				if !after.Children[0].Retired || after.Children[0].Run.Status != "failed" ||
					after.Children[0].Run.Steps != 1 || after.Usage.ModelCalls != 1 || after.Usage.OutputBytes < 4096 || after.Usage.Children != 3 {
					t.Fatalf("failure erased: %+v", after.Usage)
				}
			}
		})
	}
}

func TestCollaborationRevisionRoleChangeAndDependencyValidationAreAtomic(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	id := createCollaboration(t, collab, plan)
	role := plan.Roles["reviewer"]
	role.Instructions = "updated instructions"
	edit := CollaborationEdit{ExpectedRevision: 1, Reason: "explicit role revision",
		Roles: map[string]RoleProfile{"reviewer": role}, Upsert: plan.Children[:1]}
	if err := collab.Revise(t.Context(), id, edit); err == nil {
		t.Fatal("implicit change to another assignment was accepted")
	}
	edit.Upsert = plan.Children
	if err := collab.Revise(t.Context(), id, edit); err != nil {
		t.Fatal(err)
	}
	before, _ := collab.Inspect(t.Context(), id)
	for _, child := range before.Children {
		snapshot, err := decodeRunSnapshot(child.Run.ConfigJSON)
		if err != nil || snapshot.Config.SystemPrompt != role.Instructions {
			t.Fatalf("role snapshot not updated: %+v %v", snapshot, err)
		}
	}
	spec := plan.Children[1]
	spec.DependsOn = []string{"one"}
	if err := collab.Revise(t.Context(), id, CollaborationEdit{ExpectedRevision: 2, Reason: "retired dependency",
		Retire: []string{"one"}, Upsert: []ChildSpec{spec}}); err == nil {
		t.Fatal("live work depends on retired work")
	}
	after, _ := collab.Inspect(t.Context(), id)
	if before.Revision != after.Revision || before.Usage != after.Usage || after.Children[0].Retired {
		t.Fatal("invalid dependency revision partially committed")
	}
	attempts, _ := core.Store.ListAttempts(t.Context(), after.Parent.ID)
	if len(attempts) != 0 {
		t.Fatal("revision dispatched a model")
	}
}

func TestCollaborationParentIncreaseRetainsLimitedAndUnknownUsage(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "limited", true: "unknown"}[uncertain], func(t *testing.T) {
			var calls atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls.Add(1)
				if uncertain {
					return ModelResponse{}, errors.New("response lost")
				}
				return ModelResponse{Content: "analysis"}, nil
			}))
			plan = analysisPlan(plan)
			plan.Budget.MaxModelCalls = 1
			id := createCollaboration(t, collab, plan)
			before, _ := collab.Continue(t.Context(), id)
			limits := plan.Budget
			limits.MaxModelCalls = 3
			if err := collab.IncreaseLimits(t.Context(), id, limits); err != nil {
				t.Fatal(err)
			}
			after, _ := collab.Inspect(t.Context(), id)
			if before.Usage != after.Usage || after.Revision != 2 {
				t.Fatal("increase reset usage")
			}
			if err := collab.IncreaseLimits(t.Context(), id, plan.Budget); err == nil {
				t.Fatal("limit reduction was accepted")
			}
			collab = reopenCollaboration(t, core)
			after, err := collab.Continue(t.Context(), id)
			if uncertain {
				if !errors.Is(err, ErrUnknownResult) || calls.Load() != 1 || after.Usage != before.Usage {
					t.Fatalf("UNKNOWN replayed after increase: %+v %v", after.Usage, err)
				}
			} else if err != nil || after.Parent.Status != "completed" || after.Usage.ModelCalls != 2 {
				t.Fatalf("limited work not continued: status=%s usage=%+v err=%v", after.Parent.Status, after.Usage, err)
			}
		})
	}
}

func TestExecutionActivityParallelUnionAndIdleTime(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	runID := conversation(t, core)
	run, _, err := core.Store.CreateRun(t.Context(), runID, "clock", "work", "{}")
	if err != nil {
		t.Fatal(err)
	}
	activity := newExecutionActivity(t.Context(), core.Store, run.ID, 10)
	for range 2 {
		if err := activity.acquire(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if err := activity.release(); err != nil {
		t.Fatal(err)
	}
	current, _ := core.Store.GetRun(t.Context(), run.ID)
	if current.ActiveSince == "" {
		t.Fatal("parent paused while another child was active")
	}
	time.Sleep(100 * time.Millisecond)
	if err := activity.release(); err != nil {
		t.Fatal(err)
	}
	before, _ := core.Store.GetRun(t.Context(), run.ID)
	time.Sleep(150 * time.Millisecond)
	after, _ := core.Store.GetRun(t.Context(), run.ID)
	if before.ElapsedMS < 180 || before.ElapsedMS > 500 || after.ElapsedMS != before.ElapsedMS || after.ActiveSince != "" {
		t.Fatalf("parallel time doubled or idle time charged: before=%+v after=%+v", before, after)
	}
	if err := activity.close(); err != nil {
		t.Fatal(err)
	}
}
