package harness

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func limitedFixture(t *testing.T) (*Service, *TaskService, Run, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("listed", "file_list", map[string]string{"path": "."})}}, nil
		}
		return ModelResponse{Content: "remaining work complete; saved tool was not repeated"}, nil
	}))
	service.Config.MaxSteps = 2
	tasks := NewTaskService(service)
	result, err := tasks.Submit(t.Context(), "", "limited", "inspect then finish")
	if !errors.Is(err, ErrStepLimit) || result.Run.Status != "limited" || !result.Run.LimitSettled {
		t.Fatalf("not settled: status=%s settled=%t error=%v", result.Run.Status, result.Run.LimitSettled, err)
	}
	return service, tasks, result.Run, calls
}

func TestSettledLimitKeepsEvidenceAndStartsOneExplicitSuccessor(t *testing.T) {
	service, tasks, previous, calls := limitedFixture(t)
	attempts, _ := service.Store.ListAttempts(t.Context(), previous.ID)
	result, err := tasks.Submit(t.Context(), previous.ConversationID, "user-continue", "继续")
	if err != nil || result.Run.Status != "completed" || result.Run.ID == previous.ID || result.Run.ContinuationOf != previous.ID || calls.Load() != 2 {
		t.Fatalf("successor: status=%s parent=%s calls=%d error=%v", result.Run.Status, result.Run.ContinuationOf, calls.Load(), err)
	}
	old, _ := service.Store.GetRun(t.Context(), previous.ID)
	if old.Steps != previous.Steps || old.Status != "limited" || old.StopReason != previous.StopReason {
		t.Fatal("previous consumption/reason changed")
	}
	after, _ := service.Store.ListAttempts(t.Context(), previous.ID)
	if len(after) != len(attempts) {
		t.Fatal("previous tool was queued again")
	}
	duplicate, err := tasks.Submit(t.Context(), previous.ConversationID, "user-continue", "继续")
	if err != nil || !duplicate.Duplicate || duplicate.Run.ID != result.Run.ID || calls.Load() != 2 {
		t.Fatal("input retransmission dispatched", err)
	}
	second, err := tasks.Continue(t.Context(), previous.ID)
	if err != nil || !second.Duplicate || second.Run.ID != result.Run.ID || calls.Load() != 2 {
		t.Fatal("parent gained multiple successors", err)
	}
	view, err := tasks.Chat(t.Context(), previous.ConversationID, "")
	if err != nil || view.Turns[0].State != "limited" || view.Turns[0].CanEnd {
		t.Fatal("settled limit still requests end", err)
	}
}

func TestLimitWithUnknownAttemptCannotSettleOrEscape(t *testing.T) {
	service, tasks, previous, _ := limitedFixture(t)
	// Simulate a separately observed uncertain dispatch. It invalidates the
	// settled marker before this Run can be used as a successor source.
	if _, err := service.Store.db.ExecContext(t.Context(), `UPDATE attempts SET status='unknown' WHERE run_id=? AND kind='tool'`, previous.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Continue(t.Context(), previous.ID); err == nil {
		t.Fatal("UNKNOWN bypassed through successor")
	}
}

func TestLimitSuccessorRespectsExplicitModelAndNarrowerTurnPermission(t *testing.T) {
	service, tasks, previous, _ := limitedFixture(t)
	service.Config.Models["second"] = ModelConfig{Provider: "echo"}
	result, err := tasks.ContinueWithOptions(t.Context(), previous.ID, RunOptions{ModelID: "second", Permission: PermissionNone})
	if err != nil || result.Run.ModelID != "second" || result.Run.Permission == nil || result.Run.Permission.Mode != PermissionNone {
		t.Fatalf("continuation ignored explicit selection: model=%s err=%v", result.Run.ModelID, err)
	}
}
