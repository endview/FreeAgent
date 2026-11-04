package harness

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTelegramSteeringKeepsVerifiedRequesterWithoutQueueing(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	r, tasks, _, _, _ := telegramRuntimeFixture(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
		}
		return ModelResponse{Content: "confirmed fixture response"}, nil
	}))
	admitTelegramFixture(t, r, 90, 7, "private")
	done := make(chan RunResult, 3)
	errs := make(chan error, 3)
	var mu sync.Mutex
	busy := false
	r.SetDispatch(func(chat string, call func(context.Context) (RunResult, error)) error {
		mu.Lock()
		if busy {
			mu.Unlock()
			return ErrActiveRun
		}
		busy = true
		mu.Unlock()
		go func() {
			result, err := call(t.Context())
			mu.Lock()
			busy = false
			mu.Unlock()
			done <- result
			errs <- err
		}()
		return nil
	})
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(10, 10, 90, 7, "initial goal", "private")); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(11, 11, 90, 7, "new direction", "private")); err != nil {
		t.Fatal(err)
	}
	close(release)
	first := <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if first.Run.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("first=%s calls=%d", first.Run.Status, calls.Load())
	}
	state, err := tasks.Inputs(t.Context(), first.Run.ConversationID)
	if err != nil || len(state.Entries) != 1 || state.Entries[0].Status != "adopted" || state.Entries[0].Principal != "channel:telegram:user:7" {
		t.Fatal(state, err)
	}
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(12, 12, 90, 7, "/queue later task", "private")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("Telegram queue command dispatched a new task")
	}
	run, origin, err := tasks.ChannelRun(t.Context(), first.Run.ID)
	if err != nil || origin == nil || origin.ChatID != "90" || run.Permission.PersonID != "telegram:user:7" || origin.InputMessageID == 0 {
		t.Fatal("queued channel identity lost", err)
	}
	if err := r.Deliver(t.Context(), first); err != nil {
		t.Fatal("steered result could not be delivered", err)
	}
	before := calls.Load()
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(12, 12, 90, 7, "/queue later task", "private")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("duplicate transport input replayed queued task")
	}
	messages, _ := tasks.Messages(t.Context(), first.Run.ConversationID)
	count := 0
	for _, m := range messages {
		if m.RunID == first.Run.ID && m.Role == "user" {
			count++
			if strings.HasPrefix(m.Content, "/queue") {
				t.Fatal("Owner command reached model")
			}
		}
	}
	if count != 2 {
		t.Fatal("received steering message was lost or duplicated", count)
	}
}
