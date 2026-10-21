package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollaborationReplanFailureReplacementKeepsAllConsumption(t *testing.T) {
	var replans atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		if request.Purpose == "replanning" {
			replans.Add(1)
			edit := CollaborationEdit{ExpectedRevision: 1, Reason: "retain failed work and add a bounded replacement",
				Retire: []string{"one"}, Upsert: []ChildSpec{
					{Name: "retry", Role: "reviewer", Objective: "Read facts"},
					{Name: "two", Role: "reviewer", Objective: "Review facts", Context: "CHILD_TWO", DependsOn: []string{"retry"}},
				}}
			raw, _ := json.Marshal(edit)
			return ModelResponse{Content: string(raw)}, nil
		}
		if strings.Contains(request.Messages[len(request.Messages)-1].Content, "CHILD_ONE") {
			return ModelResponse{}, &CallError{Certain: true, Message: "definite first failure"}
		}
		return readingChild(ctx, request, emit)
	}))
	plan.MaxReplans = 1
	plan.Children[1].DependsOn = []string{"one"}
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || replans.Load() != 1 || report.Revision != 2 ||
		report.Usage.Children != 3 || report.Usage.ModelCalls != 6 || report.Usage.ToolCalls != 2 ||
		report.Usage.Replans != 1 || report.Usage.OutputBytes < 4096 ||
		!report.Children[0].Retired || report.Children[0].Run.Status != "failed" {
		t.Fatalf("replan: status=%s usage=%+v err=%v", report.Parent.Status, report.Usage, err)
	}
	attempts, _ := core.Store.ListAttempts(t.Context(), id)
	revisions, _ := collab.Revisions(t.Context(), id)
	if len(attempts) != 1 || attempts[0].Status != "finished" || len(revisions) != 2 || revisions[1].AttemptID != attempts[0].ID {
		t.Fatalf("replan/revision provenance lost: attempts=%+v revisions=%+v", attempts, revisions)
	}
	collab = reopenCollaboration(t, core)
	after, err := collab.Continue(t.Context(), id)
	if err != nil || after.Usage != report.Usage || replans.Load() != 1 {
		t.Fatalf("reopen repeated known replan: %+v %v", after.Usage, err)
	}
}

func TestCollaborationReplanNoChangeAcknowledgesFollowUpOnce(t *testing.T) {
	var replans atomic.Int32
	collab, _, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "replanning" {
			replans.Add(1)
			return ModelResponse{Content: `{"expected_revision":1,"reason":"no further work","upsert":[],"retire":[]}`}, nil
		}
		return ModelResponse{Content: `{"summary":"analysis","findings":[],"follow_up":"consider another review"}`}, nil
	}))
	role := plan.Roles["reviewer"]
	role.Tools, role.Paths, role.Acceptance = nil, nil, "analysis"
	plan.Roles["reviewer"] = role
	plan.MaxReplans = 4
	id := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), id)
	if err != nil || report.Parent.Status != "completed" || report.Usage.Replans != 1 || report.Usage.ModelCalls != 3 || replans.Load() != 1 {
		t.Fatalf("unchanged follow_up repeatedly assessed: usage=%+v err=%v", report.Usage, err)
	}
}

func TestCollaborationFailedOrInvalidReplanIsNotRedispatched(t *testing.T) {
	for _, outcome := range []string{"certain", "invalid", "unknown", "authority"} {
		t.Run(outcome, func(t *testing.T) {
			var replans atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose != "replanning" {
					return ModelResponse{Content: `{"summary":"analysis","findings":[],"follow_up":"review"}`}, nil
				}
				replans.Add(1)
				switch outcome {
				case "certain":
					return ModelResponse{}, &CallError{Certain: true, Message: "replan rejected"}
				case "unknown":
					return ModelResponse{}, errors.New("replan response lost")
				case "authority":
					return ModelResponse{Content: `{"expected_revision":1,"reason":"expand authority","roles":{"new":{"instructions":"x","model_id":"missing","output_format":"text","budget":{}}},"upsert":[{"name":"new","role":"new","objective":"new"}]}`}, nil
				default:
					return ModelResponse{Content: "not a revision"}, nil
				}
			}))
			role := plan.Roles["reviewer"]
			role.Tools, role.Paths, role.Acceptance = nil, nil, "analysis"
			plan.Roles["reviewer"] = role
			plan.MaxReplans = 2
			id := createCollaboration(t, collab, plan)
			before, err := collab.Continue(t.Context(), id)
			if err == nil || replans.Load() != 1 || before.Parent.Status == "failed" || before.Parent.Status == "completed" ||
				before.Usage.Replans != 1 || before.Usage.ModelCalls != 3 {
				t.Fatalf("replan failure: status=%s usage=%+v err=%v", before.Parent.Status, before.Usage, err)
			}
			collab = reopenCollaboration(t, core)
			after, err := collab.Continue(t.Context(), id)
			if err == nil || replans.Load() != 1 || after.Usage.ModelCalls != before.Usage.ModelCalls ||
				after.Usage.OutputBytes != before.Usage.OutputBytes || after.Revision != 1 {
				t.Fatalf("failed replan replayed: status=%s usage=%+v err=%v", after.Parent.Status, after.Usage, err)
			}
			if outcome != "unknown" {
				if err := collab.Revise(t.Context(), id, CollaborationEdit{ExpectedRevision: 1, Reason: "user supersedes unusable proposal",
					Upsert: []ChildSpec{{Name: "manual", Role: "reviewer", Objective: "explicit review"}}}); err != nil {
					t.Fatalf("known failure permanently closed parent: %v", err)
				}
			}
		})
	}
}

func TestCollaborationSavedReplanAttemptAppliedAfterReopen(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	plan = analysisPlan(plan)
	plan.MaxReplans = 1
	id := createCollaboration(t, collab, plan)
	if err := core.Store.FinishRun(t.Context(), id, "running", ""); err != nil {
		t.Fatal(err)
	}
	record, _ := loadCollaboration(t.Context(), core.Store.db, id)
	report, _ := collab.Inspect(t.Context(), id)
	request, err := replanRequest(core, record, report)
	if err != nil {
		t.Fatal(err)
	}
	core.guard = collaborationGuard{parentID: id}
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: `{"expected_revision":1,"reason":"saved follow up","upsert":[{"name":"review","role":"reviewer","objective":"Additional review"}],"retire":[]}`}, nil
	})
	run, _ := core.Store.GetRun(t.Context(), id)
	attempt, _, err := core.generate(t.Context(), run, request)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := collab.Inspect(t.Context(), id)
	var calls atomic.Int32
	core.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "replanning" {
			t.Error("saved replan was dispatched again")
		}
		return ModelResponse{Content: "analysis"}, nil
	})
	collab = reopenCollaboration(t, core)
	after, err := collab.Continue(t.Context(), id)
	revisions, _ := collab.Revisions(t.Context(), id)
	if err != nil || after.Parent.Status != "completed" || after.Usage.ModelCalls != before.Usage.ModelCalls+3 ||
		after.Usage.Replans != 1 || after.Usage.Children != 3 || calls.Load() != 3 || revisions[1].AttemptID != attempt.ID {
		t.Fatalf("saved replan not reused: status=%s usage=%+v err=%v", after.Parent.Status, after.Usage, err)
	}
}

func TestCollaborationSavedReplanRetiresWorkBeforeAnyNewDispatch(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	plan = analysisPlan(plan)
	plan.MaxReplans = 1
	id := createCollaboration(t, collab, plan)
	if err := core.Store.FinishRun(t.Context(), id, "running", ""); err != nil {
		t.Fatal(err)
	}
	record, _ := loadCollaboration(t.Context(), core.Store.db, id)
	report, _ := collab.Inspect(t.Context(), id)
	request, err := replanRequest(core, record, report)
	if err != nil {
		t.Fatal(err)
	}
	core.guard = collaborationGuard{parentID: id}
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: `{"expected_revision":1,"reason":"replace undispatched work","retire":["one"],"upsert":[{"name":"review","role":"reviewer","objective":"Replacement review"}]}`}, nil
	})
	run, _ := core.Store.GetRun(t.Context(), id)
	if _, _, err := core.generate(t.Context(), run, request); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	core.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "replanning" || strings.Contains(request.Messages[len(request.Messages)-1].Content, "CHILD_ONE") {
			t.Error("saved proposal was replayed or retired work was dispatched")
		}
		return ModelResponse{Content: "analysis"}, nil
	})
	collab = reopenCollaboration(t, core)
	after, err := collab.Continue(t.Context(), id)
	if err != nil || after.Parent.Status != "completed" || after.Revision != 2 || calls.Load() != 2 ||
		!after.Children[0].Retired || after.Children[0].Run.Steps != 0 || after.Children[0].Run.Status != "abandoned" {
		t.Fatalf("saved proposal lost scheduling priority: status=%s revision=%d calls=%d err=%v",
			after.Parent.Status, after.Revision, calls.Load(), err)
	}
}

func TestCollaborationNoOpReplanRequiresBoundedReasonWithoutReplay(t *testing.T) {
	for _, reason := range []string{"", " \n\t ", strings.Repeat("x", 4097)} {
		t.Run(fmt.Sprintf("length_%d", len(reason)), func(t *testing.T) {
			var replans atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose != "replanning" {
					return ModelResponse{Content: `{"summary":"analysis","findings":[],"follow_up":"review"}`}, nil
				}
				replans.Add(1)
				raw, _ := json.Marshal(CollaborationEdit{ExpectedRevision: 1, Reason: reason})
				return ModelResponse{Content: string(raw)}, nil
			}))
			plan = analysisPlan(plan)
			plan.MaxReplans = 1
			id := createCollaboration(t, collab, plan)
			before, err := collab.Continue(t.Context(), id)
			if err == nil || before.Parent.Status == "completed" || before.Revision != 1 || replans.Load() != 1 {
				t.Fatalf("invalid no-op accepted: revision=%d status=%s err=%v", before.Revision, before.Parent.Status, err)
			}
			collab = reopenCollaboration(t, core)
			after, err := collab.Continue(t.Context(), id)
			if err == nil || after.Usage.ModelCalls != before.Usage.ModelCalls || after.Revision != 1 || replans.Load() != 1 {
				t.Fatalf("invalid no-op replayed: revision=%d calls=%d err=%v", after.Revision, replans.Load(), err)
			}
		})
	}
}

func TestCollaborationParentActiveTimeExcludesApprovalWaitAndEnforcesDeadline(t *testing.T) {
	for _, mode := range []string{"approval", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
				if mode == "timeout" {
					<-ctx.Done()
					return ModelResponse{}, ctx.Err()
				}
				if req.Messages[len(req.Messages)-1].Role == "user" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("new", "file_create", map[string]string{"path": "new.txt", "content": "new"})}}, nil
				}
				return ModelResponse{Content: "created"}, nil
			}))
			role := plan.Roles["reviewer"]
			role.Tools, role.Paths, role.OutputFormat = []string{"file_create"}, []string{"new.txt"}, "text"
			plan.Roles["reviewer"] = role
			plan.Children = plan.Children[:1]
			plan.Budget.MaxActiveSeconds = 1
			id := createCollaboration(t, collab, plan)
			collab.tasks.SetApproval(func(ctx context.Context, _ ToolCall, _ PreparedTool) (bool, error) {
				run, _ := core.Store.GetRun(ctx, id)
				if run.ActiveSince != "" {
					t.Error("approval wait left parent clock running")
				}
				select {
				case <-time.After(1200 * time.Millisecond):
					return true, nil
				case <-ctx.Done():
					return false, ctx.Err()
				}
			})
			report, err := collab.Continue(t.Context(), id)
			if mode == "approval" {
				if err != nil || report.Parent.Status != "completed" || report.Usage.ActiveMS >= 1000 {
					t.Fatalf("idle approval billed: usage=%+v status=%s err=%v", report.Usage, report.Parent.Status, err)
				}
			} else {
				if !errors.Is(err, ErrTimeLimit) || report.Usage.ModelCalls != 1 || report.Usage.ActiveMS < 900 ||
					report.Parent.Status != "unknown" || report.Children[0].Run.Status != "unknown" {
					t.Fatalf("parent deadline failed: status=%s usage=%+v err=%v", report.Parent.Status, report.Usage, err)
				}
				before := report.Usage
				report, _ = collab.Continue(t.Context(), id)
				if !reflect.DeepEqual(before, report.Usage) {
					t.Fatal("time exhaustion replayed uncertain work")
				}
			}
		})
	}
}
