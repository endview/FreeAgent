package harness

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestQueuedInputGuidesCurrentRunWithoutDuplicateMessage(t *testing.T) {
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
		} else {
			found := false
			for _, message := range request.Messages {
				found = found || message.Role == "user" && message.Content == "use the corrected requirement"
				if message.Content == "independent later task" {
					t.Error("unadopted queue input leaked into current Run")
				}
			}
			if !found {
				t.Error("queued guidance was not passed to the current Run")
			}
		}
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan RunResult, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := tasks.Submit(ctx, chat, "initial", "original request")
		done <- result
		errs <- err
	}()
	<-entered
	run, err := tasks.Latest(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []InputCommand{
		{Action: "queue", RequestKey: "guide", Prompt: "use the corrected requirement"},
		{Action: "queue", RequestKey: "later", Prompt: "independent later task"},
	} {
		if _, err := tasks.AcceptInput(t.Context(), chat, command); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		input, err := tasks.SteerInput(t.Context(), chat, "guide", run.ID)
		if err != nil || input.Action != "steer" || input.TargetRun != run.ID {
			t.Fatal(input, err)
		}
	}
	close(release)
	result := <-done
	if err := <-errs; err != nil || result.Run.ID != run.ID || result.Run.Status != "completed" || calls.Load() != 2 {
		t.Fatal(result.Run, calls.Load(), err)
	}
	input, err := tasks.SteerInput(t.Context(), chat, "guide", run.ID)
	if err != nil || input.Status != "adopted" || input.RunID != run.ID {
		t.Fatal("lost-response retry changed the saved adoption", input, err)
	}
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Content == "use the corrected requirement" {
			count++
			if message.RunID != run.ID || message.Deferred {
				t.Fatal("guidance did not retain one adopted source message")
			}
		}
	}
	if count != 1 {
		t.Fatal("guidance duplicated its queued message", count)
	}
	state, err := tasks.Inputs(t.Context(), chat)
	if err != nil || len(state.Entries) != 2 {
		t.Fatal(state, err)
	}
	for _, input := range state.Entries {
		if input.Key == "later" && (input.Action != "queue" || input.Status != "pending") {
			t.Fatal("guidance consumed a different queued request", input)
		}
	}
}

func TestQueuedInputGuidanceRetainsQueueWhenTargetEndedOrEntryCancelled(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	run, err := tasks.Submit(t.Context(), chat, "finished", "first task")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "later", Prompt: "later task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SteerInput(t.Context(), chat, "later", run.Run.ID); !errors.Is(err, ErrInputTargetEnded) {
		t.Fatal("ended target did not preserve the queue", err)
	}
	state, err := tasks.Inputs(t.Context(), chat)
	if err != nil || len(state.Entries) != 1 || state.Entries[0].Action != "queue" || state.Entries[0].Status != "pending" {
		t.Fatal(state, err)
	}
	if _, err := tasks.CancelInput(t.Context(), chat, "later"); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SteerInput(t.Context(), chat, "later", run.Run.ID); !errors.Is(err, ErrInputNotPending) {
		t.Fatal("cancelled queue entry was revived", err)
	}
}

func TestQueuedInputGuidanceCannotChangePublicRequester(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	core.Config.PublicWeb.Enabled = true
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	alice := WithPublicEntry(t.Context(), "alice", "127.0.0.1")
	bob := WithPublicEntry(t.Context(), "bob", "127.0.0.1")
	_, err := tasks.AcceptInput(alice, chat, InputCommand{Action: "queue", RequestKey: "alice-input", Prompt: "private requirement"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SteerInput(bob, chat, "alice-input", "target"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("another requester could guide the queued input", err)
	}
}
