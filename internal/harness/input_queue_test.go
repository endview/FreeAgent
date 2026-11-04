package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSteeringDuringToolKeepsDispatchedResultAndSkipsUnstartedSibling(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var modelCalls, toolCalls atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if modelCalls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("first", "probe", map[string]string{}), makeCall("second", "probe", map[string]string{})}}, nil
		}
		if completeMessageGroups(request.Messages) != nil {
			t.Error("steering split the saved tool-call/result group")
		}
		known, skipped := false, false
		for _, m := range request.Messages {
			known = known || m.Role == "tool" && m.Content == "confirmed dispatched result"
			skipped = skipped || m.Role == "tool" && strings.Contains(m.Content, "NOT_RUN")
		}
		if !known || !skipped {
			t.Error("dispatch evidence or unstarted sibling disposition lost")
		}
		return ModelResponse{Content: "updated goal complete"}, nil
	}))
	core.Tools = boundaryTools{effect: EffectRead, execute: func(ctx context.Context) (ToolResult, error) {
		toolCalls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		}
		return ToolResult{Content: "confirmed dispatched result"}, nil
	}}
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, e := tasks.Submit(t.Context(), chat, "tool-run", "original"); done <- e }()
	<-entered
	run, _ := tasks.Latest(t.Context(), chat)
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: run.ID, RequestKey: "during-tool", Prompt: "new requirement"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if toolCalls.Load() != 1 || modelCalls.Load() != 2 {
		t.Fatalf("tool dispatches=%d model calls=%d", toolCalls.Load(), modelCalls.Load())
	}
}

func TestSteeringDoesNotReusePendingApproval(t *testing.T) {
	var calls atomic.Int32
	core, _, work := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("pending-create", "file_create", map[string]string{"path": "never-created", "content": "stale approval"})}}, nil
		}
		return ModelResponse{Content: "new requirements observed"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	waiting, err := tasks.Submit(t.Context(), chat, "approval", "create file")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatal(err)
	}
	preview, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: waiting.Run.ID, RequestKey: "replace-plan", Prompt: "skip that file and explain instead"}); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Confirm(t.Context(), waiting.Run.ID, preview, true)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	if _, err := os.Stat(filepath.Join(work, "never-created")); !os.IsNotExist(err) {
		t.Fatal("old approval dispatched superseded plan")
	}
}

func TestQueueBindingChangesAndCancellationCannotDispatchInput(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "frozen", Prompt: "keep exact model binding"}); err != nil {
		t.Fatal(err)
	}
	model := core.Config.Models[core.Config.DefaultModel]
	model.Capabilities.ContextTokens = 8192
	core.Config.Models[core.Config.DefaultModel] = model
	if _, err := tasks.RunQueued(t.Context(), chat); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("queue ignored changed model binding", err)
	}
	item, err := tasks.CancelInput(t.Context(), chat, "frozen")
	if err != nil || item.Status != "cancelled" {
		t.Fatal(item.Status, err)
	}
	if err := tasks.PauseQueue(t.Context(), chat, false); err != nil {
		t.Fatal(err)
	}
	if result, err := tasks.RunQueued(t.Context(), chat); err != nil || result.Run.ID != "" {
		t.Fatal("cancelled input dispatched", err)
	}
}

func TestSteeringBeforeToolDispatchSettlesStalePlanNOTRUN(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
			return ModelResponse{ToolCalls: []ToolCall{makeCall("old-plan", "file_create", map[string]string{"path": "must-not-exist", "content": "stale"})}}, nil
		}
		found, notRun := false, false
		for _, m := range request.Messages {
			found = found || m.Role == "user" && m.Content == "change direction"
			notRun = notRun || m.Role == "tool" && strings.Contains(m.Content, "NOT_RUN")
		}
		if !found || !notRun {
			t.Error("steering or the protocol-complete NOT_RUN result was lost")
		}
		return ModelResponse{Content: "new direction"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, err := tasks.Submit(t.Context(), chat, "original", "original goal"); done <- err }()
	<-entered
	run, _ := tasks.Latest(t.Context(), chat)
	command := InputCommand{Action: "steer", RunID: run.ID, RequestKey: "steer-1", Prompt: "change direction"}
	first, err := tasks.AcceptInput(t.Context(), chat, command)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := tasks.AcceptInput(t.Context(), chat, command)
	if err != nil || duplicate.MessageID != first.MessageID {
		t.Fatal("input retry duplicated", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, attempts, _, err := tasks.Inspect(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 || !strings.Contains(attempts[1].ResponseJSON, "not_run") || calls.Load() != 2 {
		t.Fatalf("attempts=%d calls=%d", len(attempts), calls.Load())
	}
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: run.ID, RequestKey: "late", Prompt: "late input"}); err != nil {
		t.Fatal(err)
	}
	state, _ := tasks.Inputs(t.Context(), chat)
	if state.Entries[0].Status != "target_ended" {
		t.Fatal("late steer silently became another Run")
	}
}

func TestQueuePersistsFIFOWithoutLeakingDeferredInput(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var observed []string
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
			for _, m := range request.Messages {
				if strings.Contains(m.Content, "queued-") {
					t.Error("pending queue entered current context")
				}
			}
		}
		for i := len(request.Messages) - 1; i >= 0; i-- {
			if request.Messages[i].Role == "user" {
				observed = append(observed, request.Messages[i].Content)
				break
			}
		}
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, e := tasks.Submit(t.Context(), chat, "first", "first"); done <- e }()
	<-entered
	for _, key := range []string{"queued-a", "queued-b", "queued-c"} {
		if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: key, Prompt: key}); err != nil {
			t.Fatal(err)
		}
	}
	if item, err := tasks.CancelInput(t.Context(), chat, "queued-b"); err != nil || item.Status != "cancelled" {
		t.Fatal(item, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reopenHarness(t, core)
	tasks = NewTaskService(core)
	for range 2 {
		result, err := tasks.RunQueued(t.Context(), chat)
		if err != nil || result.Run.Status != "completed" {
			t.Fatalf("queued: %s %v", result.Run.Status, err)
		}
	}
	if strings.Join(observed, ",") != "first,queued-a,queued-c" {
		t.Fatal(observed)
	}
	if result, err := tasks.RunQueued(t.Context(), chat); err != nil || result.Run.ID != "" {
		t.Fatal("queue ran twice", err)
	}
	state, err := tasks.Inputs(t.Context(), chat)
	if err != nil || len(state.Entries) != 3 {
		t.Fatal(state, err)
	}
}

func TestQueuePausesOnUNKNOWNAndNeverReplays(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ModelResponse{}, errors.New("lost result")
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, e := tasks.Submit(t.Context(), chat, "unknown", "first"); done <- e }()
	<-entered
	_, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "after", Prompt: "after"})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if !errors.Is(<-done, ErrUnknownResult) {
		t.Fatal("UNKNOWN lost")
	}
	if _, err := tasks.RunQueued(t.Context(), chat); !errors.Is(err, ErrQueuePaused) {
		t.Fatal("queue did not pause", err)
	}
	if err := tasks.PauseQueue(t.Context(), chat, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.RunQueued(t.Context(), chat); !errors.Is(err, ErrActiveRun) {
		t.Fatal("resume bypassed UNKNOWN", err)
	}
}
