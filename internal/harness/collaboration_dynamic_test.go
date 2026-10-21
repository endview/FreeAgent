package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollaborationRevisionPersistenceFailureRollsBackWholeEdit(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	id := createCollaboration(t, collab, plan)
	before, _ := collab.Inspect(t.Context(), id)
	if _, err := core.Store.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER reject_revision
		BEFORE INSERT ON meta WHEN NEW.key LIKE 'collaboration-revision/%'
		BEGIN SELECT RAISE(ABORT,'revision persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	edit := CollaborationEdit{ExpectedRevision: 1, Reason: "atomic split", Retire: []string{"one"},
		Upsert: []ChildSpec{{Name: "new", Role: "reviewer", Objective: "New analysis"}}}
	if err := collab.Revise(t.Context(), id, edit); err == nil {
		t.Fatal("revision persistence fault ignored")
	}
	after, _ := collab.Inspect(t.Context(), id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed revision partially changed existing records")
	}
	var children int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM meta WHERE key LIKE 'collaboration-run/%' AND value=?`, id).Scan(&children); err != nil || children != 3 {
		t.Fatalf("new run escaped failed revision: %d %v", children, err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `DROP TRIGGER reject_revision`); err != nil {
		t.Fatal(err)
	}
	if err := collab.Revise(t.Context(), id, edit); err != nil {
		t.Fatal(err)
	}
}

func TestCollaborationSavedReplanUsesExplicitIncreaseWithoutRepeatingCall(t *testing.T) {
	var replans atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		if req.Purpose == "replanning" {
			replans.Add(1)
			return ModelResponse{Content: `{"expected_revision":1,"reason":"bounded review","upsert":[{"name":"review","role":"reviewer","objective":"New review"}]}`}, nil
		}
		return ModelResponse{Content: `{"summary":"analysis","findings":[],"follow_up":"review useful"}`}, nil
	}))
	role := plan.Roles["reviewer"]
	role.Tools, role.Paths, role.Acceptance = nil, nil, "analysis"
	plan.Roles["reviewer"] = role
	plan.MaxReplans, plan.Budget.MaxChildren = 1, 2
	id := createCollaboration(t, collab, plan)
	before, err := collab.Continue(t.Context(), id)
	if !errors.Is(err, ErrParentBudget) || before.Usage.Replans != 1 || before.Usage.Children != 2 {
		t.Fatalf("unbounded replan: %+v %v", before.Usage, err)
	}
	attempts, _ := core.Store.ListAttempts(t.Context(), id)
	budget := plan.Budget
	budget.MaxChildren = 3
	if err := collab.IncreaseLimits(t.Context(), id, budget); err != nil {
		t.Fatal(err)
	}
	collab = reopenCollaboration(t, core)
	after, err := collab.Continue(t.Context(), id)
	saved, _ := core.Store.ListAttempts(t.Context(), id)
	if err != nil || after.Parent.Status != "completed" || after.Revision != 3 || after.Usage.Children != 3 ||
		after.Usage.Replans != 1 || after.Usage.ModelCalls != before.Usage.ModelCalls+1 || replans.Load() != 1 || !reflect.DeepEqual(attempts, saved) {
		t.Fatalf("saved proposal was not safely reused: status=%s usage=%+v err=%v", after.Parent.Status, after.Usage, err)
	}
}

func TestCollaborationCancelDuringReplanningKeepsSavedOutcomeAndStopsNewWork(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "saved"}[known], func(t *testing.T) {
			entered := make(chan struct{})
			var calls atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				if req.Purpose != "replanning" {
					return ModelResponse{Content: `{"summary":"analysis","findings":[],"follow_up":"more"}`}, nil
				}
				close(entered)
				<-ctx.Done()
				if !known {
					return ModelResponse{}, ctx.Err()
				}
				edit := CollaborationEdit{ExpectedRevision: 1, Reason: "late result",
					Upsert: []ChildSpec{{Name: "new", Role: "reviewer", Objective: "Never dispatch"}}}
				raw, _ := json.Marshal(edit)
				return ModelResponse{Content: string(raw)}, nil
			}))
			role := plan.Roles["reviewer"]
			role.Tools, role.Paths, role.Acceptance = nil, nil, "analysis"
			plan.Roles["reviewer"] = role
			plan.MaxReplans = 1
			id := createCollaboration(t, collab, plan)
			done := make(chan error, 1)
			go func() { _, err := collab.Continue(t.Context(), id); done <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("replanning did not start")
			}
			if err := collab.Control(t.Context(), id, "", "cancel"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not reach replanning")
			}
			report, _ := collab.Inspect(t.Context(), id)
			attempts, _ := core.Store.ListAttempts(t.Context(), id)
			if report.Parent.Status != "abandoned" || report.Usage.Children != 2 || report.Usage.Replans != 1 ||
				calls.Load() != 3 || report.Revision != 1 || len(attempts) != 1 ||
				known && attempts[0].Status != "finished" || !known && attempts[0].Status != "unknown" {
				t.Fatalf("cancel lost outcome or added work: status=%s usage=%+v attempts=%+v", report.Parent.Status, report.Usage, attempts)
			}
		})
	}
}

func TestCollaborationCannotRewriteActiveChildOrIncreaseByModel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	collab, _, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		close(entered)
		select {
		case <-release:
			return ModelResponse{Content: "analysis"}, nil
		case <-ctx.Done():
			return ModelResponse{}, ctx.Err()
		}
	}))
	plan = analysisPlan(plan)
	plan.Children = plan.Children[:1]
	id := createCollaboration(t, collab, plan)
	done := make(chan error, 1)
	go func() { _, err := collab.Continue(t.Context(), id); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("child not active")
	}
	spec := plan.Children[0]
	spec.Objective = "rewritten"
	if err := collab.Revise(t.Context(), id, CollaborationEdit{ExpectedRevision: 1, Reason: "active edit", Upsert: []ChildSpec{spec}}); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("active revision=%v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not finish")
	}
	var edit CollaborationEdit
	if strictDecode([]byte(`{"expected_revision":1,"reason":"unlimited","budget":{"max_children":100}}`), &edit) == nil {
		t.Fatal("model could encode increased allowances in a work edit")
	}
}
