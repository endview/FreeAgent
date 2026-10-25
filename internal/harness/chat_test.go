package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestChatStateFixtures(t *testing.T) {
	for runtime, want := range map[string]string{
		"running": "generating", "waiting_confirmation": "awaiting_approval",
		"completed": "completed", "stopped": "stopped", "abandoned": "stopped",
		"failed": "failed", "limited": "needs_attention", "unknown": "unknown",
	} {
		if got := chatState(Run{Status: runtime}, nil); got != want {
			t.Fatalf("%s projected %s instead of %s", runtime, got, want)
		}
		if got := chatState(Run{Status: runtime}, []Attempt{{Status: "unknown"}}); got != "unknown" {
			t.Fatal("retained UNKNOWN was hidden")
		}
	}
	if chatState(Run{Status: "running"}, []Attempt{{Status: "dispatched"}}) != "generating" {
		t.Fatal("live dispatch was misrepresented as a lost outcome")
	}
}

func TestChatProjectionBoundedPresentationExactRawAndScope(t *testing.T) {
	answer := strings.Repeat("中文公式", 20000)
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: answer}, nil
	}))
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	if _, err := tasks.Submit(t.Context(), chat, "large", "task"); err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil {
		t.Fatal(err)
	}
	message := view.Turns[0].Messages[1]
	if !message.Truncated || len(message.Raw) > chatTextBytes || !utf8.ValidString(message.Raw) {
		t.Fatal("unbounded or invalid UTF-8 presentation")
	}
	raw, err := tasks.ChatRaw(t.Context(), chat, "message", message.Key)
	if err != nil || raw != answer {
		t.Fatal("stored raw was modified")
	}
	if _, err := tasks.ChatRaw(t.Context(), conversation(t, service), "message", message.Key); err == nil {
		t.Fatal("raw message crossed conversation scope")
	}
}

func TestChatProjectionRawHistorySourcesAndReconnect(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		emit("partial ")
		return ModelResponse{Content: "模型说明不是证据\n\n$$x^2$$\n\n`$5`"}, nil
	}))
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	var events []Event
	tasks.SetEvents(func(event Event) { events = append(events, event) })
	result, err := tasks.Submit(t.Context(), chat, "raw", "多行\n中文")
	if err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(view.Turns) != 1 || len(view.Turns[0].Messages) != 2 ||
		view.Turns[0].Messages[1].Raw != result.Answer || len(view.Turns[0].Evidence.Files) != 0 {
		t.Fatalf("projection=%+v err=%v", view, err)
	}
	for _, event := range events {
		if event.Kind == "model_text" || event.Kind == "model_started" || event.Kind == "model_finished" {
			if event.RunID != result.Run.ID || event.AttemptID == "" || event.ConversationID != chat {
				t.Fatalf("unbound stream event: %+v", event)
			}
		}
	}
	data := service.Store.DataDir()
	_ = service.Store.Close()
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service.Store = store
	recovered, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || !reflect.DeepEqual(view, recovered) {
		t.Fatalf("reconnect changed history: err=%v", err)
	}
}

func TestChatProjectionRetainsUnknownAfterEnd(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{}, &CallError{Message: "connection lost"}
	}))
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	result, err := tasks.Submit(t.Context(), chat, "lost", "inspect")
	if !errors.Is(err, ErrUnknownResult) {
		t.Fatal(err)
	}
	if err := tasks.Abandon(t.Context(), result.Run.ID); err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || view.Turns[0].State != "unknown" || !view.Turns[0].Ended ||
		view.Turns[0].CanContinue || len(view.Turns[0].Evidence.Uncertainty) == 0 {
		t.Fatalf("lost outcome became replayable: %+v %v", view, err)
	}
}

func TestChatProjectionProviderFailuresSurviveReconnect(t *testing.T) {
	for _, certain := range []bool{true, false} {
		t.Run(map[bool]string{true: "failed", false: "unknown"}[certain], func(t *testing.T) {
			failure := &CallError{Certain: certain, Category: "rate_limit", Retryable: true, Message: "fixture"}
			service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				return ModelResponse{Content: "not a final answer"}, failure
			}))
			tasks := NewTaskService(service)
			chat := conversation(t, service)
			if _, err := tasks.Submit(t.Context(), chat, "failure", "task"); err == nil {
				t.Fatal("Provider failure ignored")
			}
			view, err := tasks.Chat(t.Context(), chat, "")
			if err != nil || len(view.Turns) != 1 {
				t.Fatal(err)
			}
			turn := view.Turns[0]
			if turn.State != map[bool]string{true: "failed", false: "unknown"}[certain] || turn.CanContinue ||
				len(turn.Messages) != 1 || len(turn.Activity) != 1 || !turn.Activity[0].IsError ||
				!reflect.DeepEqual(turn.Activity[0].Failure, failure) || turn.Activity[0].Text != "not a final answer" ||
				len(turn.Evidence.Files) != 0 {
				t.Fatalf("failure became a final answer or replayable result: %+v", turn)
			}
			raw, err := tasks.ChatRaw(t.Context(), chat, "activity", turn.Activity[0].Key)
			if err != nil || raw != "not a final answer" {
				t.Fatal("failure raw body was lost")
			}
			if _, err := tasks.ChatRaw(t.Context(), conversation(t, service), "activity", turn.Activity[0].Key); err == nil {
				t.Fatal("failure body crossed Chat boundary")
			}
			for range 2 {
				reopenHarness(t, service)
				after, err := tasks.Chat(t.Context(), chat, "")
				if err != nil || !reflect.DeepEqual(view, after) {
					t.Fatalf("reconnect changed failure projection: before=%+v after=%+v err=%v", view, after, err)
				}
			}
			backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
			if err := service.Store.Backup(t.Context(), backup); err != nil {
				t.Fatal(err)
			}
			if err := Restore(t.Context(), backup, restored); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(t.Context(), restored)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			restoredCore := *service
			restoredCore.Store = store
			after, err := NewTaskService(&restoredCore).Chat(t.Context(), chat, "")
			if err != nil || !reflect.DeepEqual(view, after) {
				t.Fatalf("backup restore changed failure projection: before=%+v after=%+v err=%v", view, after, err)
			}
		})
	}
}

func TestChatProjectionUnknownDiagnosticsAndDispatchRecovery(t *testing.T) {
	for _, phase := range []string{"call-error", "dispatched"} {
		t.Run(phase, func(t *testing.T) {
			calls := 0
			service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls++
				return ModelResponse{}, errors.New("fixture lost response")
			}))
			tasks := NewTaskService(service)
			chat := conversation(t, service)
			var run Run
			if phase == "call-error" {
				result, err := tasks.Submit(t.Context(), chat, "lost", "task")
				if !errors.Is(err, ErrUnknownResult) {
					t.Fatal(err)
				}
				run = result.Run
			} else {
				snapshot, err := service.snapshot()
				if err != nil {
					t.Fatal(err)
				}
				run, _, err = service.Store.CreateRun(t.Context(), chat, "lost", "task", snapshot)
				if err != nil {
					t.Fatal(err)
				}
				request, _ := json.Marshal(contractRequest())
				if _, err := service.Store.startModel(t.Context(), run, string(request), 10); err != nil {
					t.Fatal(err)
				}
			}
			reopenHarness(t, service)
			view, err := tasks.Chat(t.Context(), chat, "")
			if err != nil || len(view.Turns) != 1 {
				t.Fatalf("lost-call Chat inaccessible: %+v err=%v", view, err)
			}
			turn := view.Turns[0]
			if turn.State != "unknown" || turn.CanContinue || !turn.CanEnd ||
				len(turn.Messages) != 1 || len(turn.Activity) != 1 || turn.Activity[0].Text == "" ||
				turn.Activity[0].Failure != nil || len(turn.Evidence.Uncertainty) == 0 {
				t.Fatalf("lost diagnostic became a response or replayable outcome: %+v", turn)
			}
			raw, err := tasks.ChatRaw(t.Context(), chat, "activity", turn.Activity[0].Key)
			if err != nil || raw != turn.Activity[0].Text {
				t.Fatalf("lost diagnostic raw inaccessible: %q err=%v", raw, err)
			}
			if _, err := tasks.ChatRaw(t.Context(), conversation(t, service), "activity", turn.Activity[0].Key); err == nil {
				t.Fatal("lost diagnostic crossed Chat boundary")
			}
			beforeCalls := calls
			if _, err := tasks.Continue(t.Context(), run.ID); !errors.Is(err, ErrUnknownResult) || calls != beforeCalls {
				t.Fatal("unknown call was replayed")
			}
			reopenHarness(t, service)
			after, err := tasks.Chat(t.Context(), chat, "")
			if err != nil || !reflect.DeepEqual(view, after) {
				t.Fatalf("recovery diagnostic changed again: before=%+v after=%+v err=%v", view, after, err)
			}
		})
	}
}

func TestChatProjectionPagesWholeTurns(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	service.Config.MaxMessages = 80
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	for n := range 34 {
		if _, err := tasks.Submit(t.Context(), chat, string(rune('a'+n)), "hello"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(page.Turns) != 32 || !page.HasEarlier {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	older, err := tasks.Chat(t.Context(), chat, page.Before)
	if err != nil || len(older.Turns) != 2 || older.HasEarlier {
		t.Fatalf("older page=%+v err=%v", older, err)
	}
}

func TestChatTurnStepsElapsed(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		close(started)
		<-release
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	resultCh := make(chan RunResult, 1)
	go func() {
		result, _ := tasks.Submit(t.Context(), chat, "elapsed", "measure")
		resultCh <- result
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	time.Sleep(25 * time.Millisecond)
	live, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(live.Turns) != 1 {
		t.Fatalf("live Chat=%+v err=%v", live, err)
	}
	if live.Turns[0].Steps < 1 || live.Turns[0].ElapsedMS < 10 {
		t.Fatalf("live derived fields were not current: %+v", live.Turns[0])
	}
	close(release)
	result := <-resultCh
	if result.Run.Status != "completed" {
		t.Fatalf("run did not complete: %+v", result.Run)
	}
	settled, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(settled.Turns) != 1 {
		t.Fatalf("settled Chat=%+v err=%v", settled, err)
	}
	if settled.Turns[0].Steps != result.Run.Steps || settled.Turns[0].ElapsedMS != result.Run.ElapsedMS ||
		settled.Turns[0].ElapsedMS < live.Turns[0].ElapsedMS {
		t.Fatalf("settled derived fields diverged: turn=%+v run=%+v", settled.Turns[0], result.Run)
	}
}

func TestChatActivityTarget(t *testing.T) {
	longPath := strings.Repeat("文", 200)
	for _, test := range []struct {
		name, raw, want string
	}{
		{"path", `{"path":"notes.txt","old_text":"x"}`, "notes.txt"},
		{"command", `{"command":"go test ./..."}`, "go test ./..."},
		{"url", `{"url":"https://example.test/api"}`, "https://example.test/api"},
		{"query", `{"query":"needle"}`, "needle"},
		{"priority", `{"path":"first","command":"second"}`, "first"},
		{"truncate", `{"path":"` + longPath + `"}`, string([]rune(longPath)[:160])},
		{"invalid", `{"path":`, ""},
		{"non_string", `{"path":42}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := activityTarget(ToolCall{Name: "fixture", Arguments: json.RawMessage(test.raw)})
			if got != test.want {
				t.Fatalf("target=%q want %q", got, test.want)
			}
		})
	}
}
