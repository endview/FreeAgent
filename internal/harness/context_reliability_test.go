package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func emptyBudgetResponse(tokens int64, reported bool) (ModelResponse, error) {
	response := ModelResponse{}
	if reported {
		response.Usage = &ModelUsage{OutputTokens: tokens, TotalTokens: tokens, ReasoningTokens: &tokens}
	}
	return response, &CallError{Certain: true, Category: "output_limit", Message: "model reached its output budget without an answer"}
}

func TestEmptyOutputLimitDoesNotRetry(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			calls := 0
			core := recoveryFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls++
				return emptyBudgetResponse(128, reported)
			}))
			result, err := core.Run(t.Context(), conversation(t, core), "empty-output", "answer briefly")
			attempts, inspectErr := core.Store.ListAttempts(t.Context(), result.Run.ID)
			if err == nil || result.Run.Status != "failed" || calls != 1 || inspectErr != nil || len(attempts) != 1 || !strings.Contains(attempts[0].ResponseJSON, "output_limit") {
				t.Fatalf("status=%s calls=%d attempts=%d err=%v inspect=%v", result.Run.Status, calls, len(attempts), err, inspectErr)
			}
		})
	}
}

func TestSummaryOutputBudgetIsIndependentOfStoredBytes(t *testing.T) {
	for _, tokens := range []int{4096, 8192} {
		request := summaryRequest(ModelBinding{}, ExecutionLimits{MaxOutputTokens: tokens}, 4096, "", nil)
		if request.MaxOutputTokens != tokens {
			t.Fatalf("configured=%d actual=%d; summary bytes became token budget", tokens, request.MaxOutputTokens)
		}
	}
}

func TestAutomaticCompactionFailureIsSuppressedAcrossRoundsAndRestart(t *testing.T) {
	core, _, chat, _ := summaryFixture(t)
	core.Config.MaxOutputTokens = 128
	core.Config.TaskSummaryBytes = 512
	core.Config.AutoCompactAt = 50
	core.Config.ModelRetries = 2
	core.retryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	probe, err := core.buildContext(t.Context(), Run{ConversationID: chat, Prompt: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := core.requestUsage(Run{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	core.Config.MaxContextBytes = (usage.Bytes + usage.ReservedBytes) * 100 / 65
	summaries, ordinary, notices := 0, 0, 0
	core.Emit = func(event Event) {
		if event.Kind == EventContextCompactionFailed {
			notices++
		}
	}
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			summaries++
			return emptyBudgetResponse(128, true)
		}
		ordinary++
		return ModelResponse{Content: "continued; previous history and UNKNOWN remain intact"}, nil
	})
	for i := range 3 {
		if i == 1 {
			reopenHarness(t, core)
			core.compactionFailed = false
		}
		result, runErr := NewTaskService(core).Submit(t.Context(), chat, fmt.Sprintf("fallback-%d", i), "continue")
		if runErr != nil || result.Run.Status != "completed" {
			t.Fatalf("round=%d status=%s summaries=%d err=%v", i, result.Run.Status, summaries, runErr)
		}
	}
	if summaries != 1 || ordinary != 3 || notices != 1 {
		t.Fatalf("summaries=%d ordinary=%d notices=%d", summaries, ordinary, notices)
	}
	state, err := NewTaskService(core).ContextState(t.Context(), chat)
	if err != nil || state.AutomaticPaused == "" {
		t.Fatalf("suppression not visible: %+v %v", state, err)
	}
	other, err := NewTaskService(core).ContextState(t.Context(), conversation(t, core))
	if err != nil || other.AutomaticPaused != "" {
		t.Fatalf("suppression leaked to another conversation: %+v %v", other, err)
	}
	core.Config.MaxOutputTokens++
	result, err := NewTaskService(core).Submit(t.Context(), chat, "changed-output", "continue")
	if err != nil || result.Run.Status != "completed" || summaries != 2 || notices != 2 {
		t.Fatalf("changed configuration did not reset suppression: status=%s calls=%d notices=%d err=%v", result.Run.Status, summaries, notices, err)
	}
	core.Config.MaxContextBytes = 4096
	_, err = NewTaskService(core).Submit(t.Context(), chat, "hard-budget", "continue")
	if !errors.Is(err, ErrContextLimit) || summaries != 2 {
		t.Fatalf("hard budget bypassed or repeated failed summary: calls=%d err=%v", summaries, err)
	}
}

func enablePreemptiveCompaction(t *testing.T, core *Service, chat string) {
	t.Helper()
	core.Config.MaxOutputTokens = 128
	core.Config.TaskSummaryBytes = 512
	core.Config.AutoCompactAt = 50
	probe, err := core.buildContext(t.Context(), Run{ConversationID: chat, Prompt: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := core.requestUsage(Run{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	core.Config.MaxContextBytes = (u.Bytes + u.ReservedBytes) * 100 / 65
}

func TestAutomaticCompactionDoesNotIgnoreUnsafeFailures(t *testing.T) {
	for _, mode := range []string{"authentication", "unknown", "cancel", "storage"} {
		t.Run(mode, func(t *testing.T) {
			core, _, chat, _ := summaryFixture(t)
			enablePreemptiveCompaction(t, core, chat)
			ordinary := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				switch mode {
				case "authentication":
					return ModelResponse{}, &CallError{Certain: true, Category: "authentication", Message: "fixture authentication failure"}
				case "unknown":
					return ModelResponse{}, &CallError{Category: "transport", Message: "response unknown"}
				case "cancel":
					cancel()
					return emptyBudgetResponse(128, true)
				default:
					return ModelResponse{Content: "Retain original goals and UNKNOWN."}, nil
				}
			})
			underlying := core.Model
			core.Model = modelFunc(func(ctx context.Context, r ModelRequest, emit func(string)) (ModelResponse, error) {
				if r.Purpose == "" {
					ordinary++
				}
				return underlying.Generate(ctx, r, emit)
			})
			if mode == "storage" {
				if err := installPGFault(t, core.Store, "reject_auto_summary", "meta", "INSERT", "NEW.key LIKE 'context/summary/%'"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := core.Run(ctx, chat, "unsafe-failure", "continue")
			if err == nil || result.Run.Status == "completed" || ordinary != 0 {
				t.Fatalf("unsafe failure ignored: status=%s calls=%d err=%v", result.Run.Status, ordinary, err)
			}
			var suppressed bool
			if err := core.Store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, core.compactionSuppressionKey(chat)).Scan(&suppressed); err != nil || suppressed {
				t.Fatalf("unsafe failure became configuration suppression: %t %v", suppressed, err)
			}
		})
	}
}

func TestCompactionFallbackAdoptsSteering(t *testing.T) {
	core, _, chat, _ := summaryFixture(t)
	enablePreemptiveCompaction(t, core, chat)
	entered, release := make(chan struct{}), make(chan struct{})
	core.Model = modelFunc(func(ctx context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
			return emptyBudgetResponse(128, true)
		}
		found := false
		for _, message := range r.Messages {
			found = found || strings.Contains(message.Content, "steering during compaction")
		}
		if !found {
			t.Error("fallback used a request prepared before user steering")
		}
		return ModelResponse{Content: "new requirement adopted"}, nil
	})
	tasks := NewTaskService(core)
	done := make(chan error, 1)
	go func() { _, err := tasks.Submit(t.Context(), chat, "with-steering", "continue"); done <- err }()
	<-entered
	run, err := tasks.Latest(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: run.ID, RequestKey: "during-summary", Prompt: "steering during compaction"})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSummaryContentRepairIsSharedByAllSourceChunks(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	for i := range 12 {
		if _, err := core.Run(t.Context(), chat, fmt.Sprintf("repair-source-%d", i), strings.Repeat("source detail ", 50)); err != nil {
			t.Fatal(err)
		}
	}
	core.Config.MaxContextBytes, core.Config.MaxOutputTokens = 4096, 128
	calls := 0
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 2 {
			return ModelResponse{Content: "Valid bounded summary of prior facts."}, nil
		}
		return ModelResponse{Content: strings.Repeat("x", 1024)}, nil
	})
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 512})
	if err == nil || result.Changed || result.ModelCalls != 3 || calls != 3 || result.State.Summary != nil {
		t.Fatalf("second source chunk got a fresh content repair: %+v calls=%d err=%v", result, calls, err)
	}
}

func TestManualCompactionSuccessClearsAutomaticSuppression(t *testing.T) {
	core, _, chat, _ := summaryFixture(t)
	enablePreemptiveCompaction(t, core, chat)
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			return emptyBudgetResponse(128, true)
		}
		return ModelResponse{Content: "continued"}, nil
	})
	tasks := NewTaskService(core)
	if _, err := tasks.Submit(t.Context(), chat, "first-failure", "continue"); err != nil {
		t.Fatal(err)
	}
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: "Prior goals remain; UNKNOWN is not success."}, nil
	})
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"})
	if err != nil || !result.Changed || result.State.AutomaticPaused != "" {
		t.Fatalf("manual success did not clear suppression: %+v %v", result, err)
	}
}

func TestAutomaticCompactionWithoutSavingsRetainsHistoryAndContinues(t *testing.T) {
	core, _, chat, _ := summaryFixture(t)
	enablePreemptiveCompaction(t, core, chat)
	core.Config.TaskSummaryBytes = 4096
	summaries := 0
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			summaries++
			return ModelResponse{Content: strings.Repeat("x", 4000)}, nil
		}
		return ModelResponse{Content: "continued from intact original history"}, nil
	})
	result, err := NewTaskService(core).Submit(t.Context(), chat, "no-savings", "continue")
	if err != nil || result.Run.Status != "completed" || summaries != 1 {
		t.Fatalf("no-progress summary blocked valid original context: status=%s calls=%d err=%v", result.Run.Status, summaries, err)
	}
	state, err := NewTaskService(core).ContextState(t.Context(), chat)
	if err != nil || state.Summary != nil || state.AutomaticPaused != "" {
		t.Fatalf("invalid selection or persistent no-progress suppression: %+v %v", state, err)
	}
}

func TestContextStateUsesLockedConfigWhileSettingsChange(t *testing.T) {
	base, files, _ := testHarness(t, EchoModel{})
	core := NewService(base.Store, base.Config, Adapters{Model: EchoModel{}, Tools: files})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				core.configMu.Lock()
				core.Config.MaxContextBytes = 131072 + (core.Config.MaxContextBytes % 2)
				core.configMu.Unlock()
				runtime.Gosched()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for range 20 {
		if _, err := tasks.ContextState(t.Context(), chat); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSummaryContentRepairDoesNotConsumeRateLimitRecovery(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	core.Config.ModelRetries = 2
	core.retryWait = func(ctx context.Context, delay time.Duration) error {
		if delay < 7*time.Second {
			t.Errorf("Retry-After not observed: %s", delay)
		}
		return ctx.Err()
	}
	calls := 0
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		switch calls {
		case 1:
			return ModelResponse{Content: strings.Repeat("x", 2048)}, nil
		case 2:
			return ModelResponse{}, &CallError{Certain: true, Category: "rate_limit", Message: "429", RetryAfterMS: 7000}
		default:
			return ModelResponse{Content: "Retain " + lateContextConstraint + "; UNKNOWN remains unverified."}, nil
		}
	})
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 512})
	attempts, inspectErr := core.Store.ListAttempts(t.Context(), result.RunID)
	if err != nil || !result.Changed || result.ModelCalls != 3 || calls != 3 || inspectErr != nil || len(attempts) != 3 {
		t.Fatalf("result=%+v calls=%d attempts=%d err=%v inspect=%v", result, calls, len(attempts), err, inspectErr)
	}
	var failed ModelResponse
	if json.Unmarshal([]byte(attempts[1].ResponseJSON), &failed) != nil || failed.Failure == nil || failed.Failure.Category != "rate_limit" {
		t.Fatal("rate-limit evidence was lost")
	}
}
