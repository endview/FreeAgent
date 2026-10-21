package harness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollaborationSynthesisFailureAndUnknown(t *testing.T) {
	for _, mode := range []string{"off", "success", "failure", "unknown", "invalid", "overrun"} {
		t.Run(mode, func(t *testing.T) {
			var synthesisCalls atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
				if request.Purpose != "synthesis" {
					return ModelResponse{Content: "saved expert conclusion"}, nil
				}
				synthesisCalls.Add(1)
				if len(request.Tools) != 0 || strings.Contains(request.Messages[1].Content, "CHILD_ONE") ||
					strings.Contains(request.Messages[1].Content, "PARENT_PRIVATE") {
					t.Fatal("synthesis received private context or tools")
				}
				switch mode {
				case "failure":
					return ModelResponse{}, &CallError{Certain: true, Message: "definitive fixture rejection"}
				case "unknown":
					return ModelResponse{}, errors.New("response lost")
				case "invalid":
					return ModelResponse{ToolCalls: []ToolCall{makeCall("forged", "file_read", map[string]string{"path": "facts.txt"})}}, nil
				case "overrun":
					emit(strings.Repeat("x", 17000))
				}
				return ModelResponse{Content: "Combined viewpoint. The model says a check passed."}, nil
			}))
			plan = analysisPlan(plan)
			plan.Synthesis = mode != "off"
			id := createCollaboration(t, collab, plan)
			report, err := collab.Continue(t.Context(), id)
			expectedCalls := int32(1)
			if mode == "off" {
				expectedCalls = 0
			}
			if synthesisCalls.Load() != expectedCalls || report.Usage.ModelCalls != 2+int(expectedCalls) ||
				len(report.CombinedEvidence().ChecksPassed) != 0 {
				t.Fatalf("accounting or model claim became evidence: %+v %v", report, err)
			}
			saved, _ := core.Store.GetRun(t.Context(), id)
			if !strings.Contains(saved.Output, "saved expert conclusion") {
				t.Fatal("optional synthesis lost deterministic outcomes")
			}
			switch mode {
			case "unknown", "invalid":
				if err == nil || report.Parent.Status != "unknown" || report.Synthesis.Status != "UNKNOWN" ||
					len(report.CombinedEvidence().Uncertainty) == 0 {
					t.Fatalf("uncertainty lost: %+v %v", report, err)
				}
				if _, err := collab.Continue(t.Context(), id); !errors.Is(err, ErrUnknownResult) {
					t.Fatal("UNKNOWN replay allowed")
				}
			case "overrun":
				if !errors.Is(err, ErrChildBudget) || report.Parent.Status != "limited" {
					t.Fatalf("synthesis cap escaped: %+v %v", report, err)
				}
			case "failure":
				if err == nil || report.Parent.Status != "completed" || report.Synthesis.Status != "failed" {
					t.Fatalf("fallback not completed: %+v %v", report, err)
				}
			default:
				if err != nil || report.Parent.Status != "completed" {
					t.Fatalf("result=%+v %v", report, err)
				}
			}
			if mode != "overrun" {
				_, _ = collab.Continue(t.Context(), id)
				if synthesisCalls.Load() != expectedCalls {
					t.Fatal("saved synthesis replayed")
				}
			}
		})
	}
}

func TestCollaborationSynthesisParentAllowanceAndDraft(t *testing.T) {
	var calls atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "synthesis" {
			return ModelResponse{Content: "combined"}, nil
		}
		return ModelResponse{Content: "saved child"}, nil
	}))
	plan = analysisPlan(plan)
	plan.Synthesis, plan.Budget.MaxModelCalls = true, 2
	id := createCollaboration(t, collab, plan)
	before, err := collab.Continue(t.Context(), id)
	saved, _ := core.Store.GetRun(t.Context(), id)
	if !errors.Is(err, ErrParentBudget) || before.Parent.Status != "limited" || calls.Load() != 2 ||
		!strings.Contains(saved.Output, "saved child") {
		t.Fatalf("synthesis allowance=%+v %v", before, err)
	}
	budget := plan.Budget
	budget.MaxModelCalls = 3
	if err := collab.IncreaseLimits(t.Context(), id, budget); err != nil {
		t.Fatal(err)
	}
	after, err := collab.Continue(t.Context(), id)
	if err != nil || after.Parent.Status != "completed" || calls.Load() != 3 || before.Digest != after.Digest {
		t.Fatalf("finite increase=%+v %v", after, err)
	}
}

func TestCollaborationSynthesisSavedAfterCompletionFaultNoReplay(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "known_failure"}[failed], func(t *testing.T) {
			var synthesisCalls atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose == "synthesis" {
					synthesisCalls.Add(1)
					if failed {
						return ModelResponse{}, &CallError{Certain: true, Message: "definitive synthesis rejection"}
					}
					return ModelResponse{Content: "saved synthesis"}, nil
				}
				return ModelResponse{Content: "expert"}, nil
			}))
			plan = analysisPlan(plan)
			plan.Synthesis = true
			id := createCollaboration(t, collab, plan)
			if _, err := core.Store.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER completion_fault BEFORE UPDATE OF status ON runs
		WHEN NEW.id='`+id+`' AND NEW.status='completed' BEGIN SELECT RAISE(ABORT,'completion failure'); END`); err != nil {
				t.Fatal(err)
			}
			report, err := collab.Continue(t.Context(), id)
			expectedStatus := map[bool]string{false: "finished", true: "failed"}[failed]
			if err == nil || report.Synthesis.Status != expectedStatus ||
				report.Parent.Status != map[bool]string{false: "stopped", true: "failed"}[failed] {
				t.Fatalf("save fault=%+v %v", report, err)
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
			if !failed && err != nil || report.Parent.Status != "completed" || synthesisCalls.Load() != 1 ||
				report.Synthesis.Status != expectedStatus {
				t.Fatalf("saved synthesis replayed=%+v %v calls=%d", report, err, synthesisCalls.Load())
			}
		})
	}
}

func TestCollaborationSynthesisCancellationAndActiveTime(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	collab, core, plan := collaborationFixture(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "synthesis" {
			close(started)
			<-ctx.Done()
			return ModelResponse{}, ctx.Err()
		}
		return ModelResponse{Content: "expert"}, nil
	}))
	plan = analysisPlan(plan)
	plan.Synthesis = true
	id := createCollaboration(t, collab, plan)
	done := make(chan error, 1)
	go func() { _, err := collab.Continue(t.Context(), id); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("synthesis not started")
	}
	time.Sleep(30 * time.Millisecond)
	if err := collab.Control(t.Context(), id, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknownResult) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop synthesis")
	}
	report, err := collab.Inspect(t.Context(), id)
	saved, _ := core.Store.GetRun(t.Context(), id)
	if err != nil || report.Parent.Status != "abandoned" || report.Synthesis.Status != "UNKNOWN" ||
		report.Usage.ActiveMS < 30 || calls.Load() != 3 || !strings.Contains(saved.Output, "expert") {
		t.Fatalf("cancel=%+v %v", report, err)
	}
}
