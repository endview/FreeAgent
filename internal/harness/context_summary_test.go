package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const lateContextConstraint = "KEEP_THIS_CONSTRAINT_AFTER_120_CHARACTERS"

func summaryFixture(t *testing.T) (*Service, *TaskService, string, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	service, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "compaction" {
			calls.Add(1)
			if len(request.Tools)+len(request.Memory)+len(request.Extensions) != 0 {
				t.Error("summary acquired tools, selected memory or extensions")
			}
			if !strings.Contains(request.Messages[1].Content, lateContextConstraint) {
				t.Error("summary lost source content after character 120")
			}
			return ModelResponse{Content: "Goal: preserve " + lateContextConstraint + ". Completed: bounded fixture work. Pending: next user action. Unknown effects remain unverified."}, nil
		}
		return ModelResponse{Content: strings.Repeat("completed fixture work; ", 24)}, nil
	}))
	service.Config.TaskSummaryBytes = 0
	chat := conversation(t, service)
	for i := 0; i < 3; i++ {
		prompt := strings.Repeat("earlier source data; ", 24) + lateContextConstraint
		if _, err := service.Run(t.Context(), chat, fmt.Sprintf("seed-%d", i), prompt); err != nil {
			t.Fatal(err)
		}
	}
	return service, NewTaskService(service), chat, calls
}

func TestManualCompactionDurableBoundedAndNoSyntheticMessages(t *testing.T) {
	service, tasks, chat, calls := summaryFixture(t)
	before, _ := tasks.Messages(t.Context(), chat)
	latest, _ := tasks.Latest(t.Context(), chat)
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
	if err != nil || !result.Changed || result.State.Status != "current" || result.ModelCalls != 1 || calls.Load() != 1 || result.State.Automatic {
		t.Fatalf("compact: %+v %v", result, err)
	}
	if result.State.Summary.SourceCount != 4 || !strings.Contains(result.State.Summary.Text, lateContextConstraint) {
		t.Fatalf("summary: %+v", result.State.Summary)
	}
	after, _ := tasks.Messages(t.Context(), chat)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("compaction changed original history")
	}
	current, _ := tasks.Latest(t.Context(), chat)
	view, viewErr := tasks.Chat(t.Context(), chat, "")
	if viewErr != nil || len(view.Turns) != 3 || current.ID != latest.ID {
		t.Fatalf("maintenance became a conversation turn: %+v %v", view, viewErr)
	}
	_, attempts, _, err := tasks.Inspect(t.Context(), result.RunID)
	var request ModelRequest
	if err != nil || len(attempts) != 1 || json.Unmarshal([]byte(attempts[0].RequestJSON), &request) != nil || request.Purpose != "compaction" || attempts[0].Status != "finished" {
		t.Fatalf("summary accounting: %+v %v", attempts, err)
	}
	again, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"})
	if err != nil || again.Changed || calls.Load() != 1 {
		t.Fatalf("repeated compact replayed work: %+v %v", again, err)
	}
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service.Store = store
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		found := false
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "Conversation summary") && strings.Contains(message.Content, lateContextConstraint) {
				found = true
			}
		}
		if !found || request.Purpose != "" {
			t.Error("restart lost summary or silently enabled automatic compaction")
		}
		return ModelResponse{Content: "continued with retained context"}, nil
	})
	if _, err := service.Run(t.Context(), chat, "after-restart", "continue"); err != nil {
		t.Fatal(err)
	}
}

func TestCompactionFailuresRetainPreviousSummaryAndHistory(t *testing.T) {
	for _, mode := range []string{"certain", "unknown", "oversized", "output-limit", "transaction"} {
		t.Run(mode, func(t *testing.T) {
			service, tasks, chat, _ := summaryFixture(t)
			if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Run(t.Context(), chat, "more-source", strings.Repeat("more context; ", 50)); err != nil {
				t.Fatal(err)
			}
			before, _ := tasks.Messages(t.Context(), chat)
			raw, _ := service.Store.summaryRaw(t.Context(), chat)
			var calls atomic.Int32
			service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls.Add(1)
				switch mode {
				case "certain":
					return ModelResponse{}, &CallError{Certain: true, Category: "authentication", Message: "fixture authentication failure"}
				case "unknown":
					return ModelResponse{}, &CallError{Certain: false, Message: "fixture response lost"}
				case "oversized":
					return ModelResponse{Content: strings.Repeat("x", 2048)}, nil
				case "output-limit":
					return ModelResponse{Content: "A truncated summary", Provider: &ProviderResponse{Protocol: "chat", FinishReason: "output_limit", UsageStatus: "missing"}}, nil
				default:
					return ModelResponse{Content: "Updated retained goal and next action."}, nil
				}
			})
			if mode == "transaction" {
				if _, err := service.Store.db.ExecContext(t.Context(), `CREATE TRIGGER reject_summary BEFORE UPDATE ON meta WHEN NEW.key LIKE 'context/summary/%' BEGIN SELECT RAISE(ABORT,'summary fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
			retained, _ := service.Store.summaryRaw(t.Context(), chat)
			after, _ := tasks.Messages(t.Context(), chat)
			if err == nil || result.Changed || raw != retained || !reflect.DeepEqual(before, after) || calls.Load() != 1 {
				t.Fatalf("failure changed state or replayed: %+v %v calls=%d", result, err, calls.Load())
			}
			if mode == "unknown" {
				run, attempts, _, inspectErr := tasks.Inspect(t.Context(), result.RunID)
				if inspectErr != nil || run.Status != "unknown" || len(attempts) != 1 || attempts[0].Status != "unknown" {
					t.Fatalf("unknown lost: %+v %+v %v", run, attempts, inspectErr)
				}
				if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"}); !errors.Is(err, ErrActiveRun) || calls.Load() != 1 {
					t.Fatal("UNKNOWN was retried")
				}
			}
		})
	}
}

func TestCompactionIntegrityRefreshAndClear(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	first, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.db.ExecContext(t.Context(), `UPDATE messages SET body_json=json_set(body_json,'$.Content',?) WHERE id=(SELECT min(id) FROM messages WHERE conversation_id=?)`, strings.Repeat("changed source; ", 40)+lateContextConstraint, chat); err != nil {
		t.Fatal(err)
	}
	state, err := tasks.ContextState(t.Context(), chat)
	if err != nil || state.Status != "stale" {
		t.Fatalf("corruption not detected: %+v %v", state, err)
	}
	if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"}); !errors.Is(err, ErrSummaryStale) {
		t.Fatalf("stale summary silently replaced: %v", err)
	}
	refreshed, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "refresh"})
	if err != nil || !refreshed.Changed || refreshed.State.Status != "current" || refreshed.State.Summary.Revision <= first.State.Summary.Revision {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	before, _ := tasks.Messages(t.Context(), chat)
	cleared, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "clear"})
	after, _ := tasks.Messages(t.Context(), chat)
	if err != nil || !cleared.Changed || cleared.State.Status != "none" || !reflect.DeepEqual(before, after) {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
}

func TestManualCompactionRestoresLimitedRunAndRetainsBudget(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	service.Config.MaxMessages = 3
	result, err := service.Run(t.Context(), chat, "limited", "new task needing retained context")
	if !errors.Is(err, ErrContextLimit) || result.Run.Status != "limited" {
		t.Fatalf("limit: %+v %v", result, err)
	}
	before := result.Run
	compact, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
	current, _ := service.Store.GetRun(t.Context(), before.ID)
	if err != nil || !compact.Changed || compact.RunID != before.ID || current.Status != "limited" || current.Steps != before.Steps+compact.ModelCalls || current.ElapsedMS < before.ElapsedMS {
		t.Fatalf("limited compaction: %+v run=%+v %v", compact, current, err)
	}
	if resumed, err := tasks.Continue(t.Context(), before.ID); err != nil || resumed.Run.Status != "completed" {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
}

func TestCompactionChunksKeepSemanticContinuityAndBoundedMetadata(t *testing.T) {
	service, tasks, chat, calls := summaryFixture(t)
	service.Config.MaxMessages, service.Config.MaxSteps = 200, 100
	for i := 0; i < 16; i++ {
		if _, err := service.Run(t.Context(), chat, fmt.Sprintf("long-%d", i), strings.Repeat("source context; ", 30)+lateContextConstraint); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := tasks.Messages(t.Context(), chat)
	service.Config.MaxContextBytes, service.Config.MaxOutputTokens = 4096, 128
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 512})
	if err != nil || !result.Changed || result.ModelCalls < 2 || int(calls.Load()) != result.ModelCalls || result.State.Summary.SourceCount != int64(len(before)-2) {
		t.Fatalf("chunked compaction: %+v %v calls=%d", result, err, calls.Load())
	}
	if !strings.Contains(result.State.Summary.Text, lateContextConstraint) {
		t.Fatal("chunking lost the retained constraint")
	}
	raw, err := service.Store.summaryRaw(t.Context(), chat)
	if err != nil || len(raw) > 2048 || strings.Contains(raw, "message_ids") || strings.Contains(raw, "source_hashes") {
		t.Fatalf("unbounded summary metadata: %d %v", len(raw), err)
	}
	after, _ := tasks.Messages(t.Context(), chat)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("chunking modified source history")
	}
}

func TestAIContextChangesAlwaysRequireUserApproval(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(fmt.Sprint(approve), func(t *testing.T) {
			service, tasks, chat, _ := summaryFixture(t)
			service.controlToolsActive = true
			var normal, summaries int
			service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose == "compaction" {
					summaries++
					return ModelResponse{Content: "Goals and constraints retained. Earlier fixture work completed. Await next user action."}, nil
				}
				normal++
				if normal == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("compact-context", "context_manage", ContextCommand{Action: "compact", SummaryBytes: 512})}}, nil
				}
				return ModelResponse{Content: "user decision observed"}, nil
			})
			waiting, err := tasks.SubmitWithOptions(t.Context(), chat, "ai-compact", "compress this conversation", RunOptions{Permission: PermissionFull, ConfirmFullAccess: true})
			if err != nil || waiting.Run.Status != "waiting_confirmation" || summaries != 0 {
				t.Fatalf("Full bypassed context approval: %+v %v", waiting, err)
			}
			decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
			if err != nil || decision.Tool != "context_manage" {
				t.Fatalf("preview: %+v %v", decision, err)
			}
			result, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision, approve)
			if err != nil || result.Run.Status != "completed" {
				t.Fatalf("confirmation: %+v %v", result, err)
			}
			state, err := tasks.ContextState(t.Context(), chat)
			if err != nil || approve && (state.Status != "current" || summaries != 1 || state.Summary.RunID != waiting.Run.ID) || !approve && (state.Status != "none" || summaries != 0) {
				t.Fatalf("summary decision: %+v %v calls=%d", state, err, summaries)
			}
		})
	}
}

func TestCompactionRejectsChangedSourceBeforeCommit(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	first, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	service.Model = modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		_, err := service.Store.db.ExecContext(ctx, `UPDATE messages SET body_json=json_set(body_json,'$.Content','changed while summarizing') WHERE id=?`, first.State.Summary.CoveredThrough)
		if err != nil {
			t.Fatal(err)
		}
		return ModelResponse{Content: "A replacement which must never be committed."}, nil
	})
	raw, _ := service.Store.summaryRaw(t.Context(), chat)
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "refresh"})
	after, _ := service.Store.summaryRaw(t.Context(), chat)
	if !errors.Is(err, ErrSummaryStale) || result.Changed || raw != after || result.ModelCalls != 1 {
		t.Fatalf("changed source committed: %+v %v", result, err)
	}
}

func TestAutomaticCompactionRebuildsStalePrefixAndExtendsAtomically(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	first, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if _, err := service.Run(t.Context(), chat, fmt.Sprintf("tail-%d", i), strings.Repeat("tail source; ", 40)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = service.Store.db.ExecContext(t.Context(), `UPDATE messages SET body_json=json_set(body_json,'$.Content',?) WHERE id=(SELECT min(id) FROM messages WHERE conversation_id=?)`, strings.Repeat("updated prefix; ", 40)+lateContextConstraint, chat)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := tasks.ContextState(t.Context(), chat); err != nil || state.Status != "stale" {
		t.Fatalf("stale: %+v %v", state, err)
	}
	before, _ := tasks.Messages(t.Context(), chat)
	service.Config.TaskSummaryBytes, service.Config.MaxMessages = 1024, 3
	result, err := service.Run(t.Context(), chat, "automatic-recovery", "continue after changed source")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("automatic recovery: %+v %v", result, err)
	}
	state, err := tasks.ContextState(t.Context(), chat)
	if err != nil || state.Status != "current" || state.Summary.Revision != first.State.Summary.Revision+1 || state.Summary.SourceCount != int64(len(before)) {
		t.Fatalf("automatic rebuilt only old range or committed partial revisions: %+v %v", state, err)
	}
}

func TestCompactionOversizedNewestGroupRetainsSelection(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"}); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Store.summaryRaw(t.Context(), chat)
	service.Config.TaskSummaryBytes, service.Config.MaxContextBytes, service.Config.MaxOutputTokens = 512, 8192, 128
	result, err := service.Run(t.Context(), chat, "oversized-newest", strings.Repeat("indivisible request ", 700))
	after, _ := service.Store.summaryRaw(t.Context(), chat)
	if !errors.Is(err, ErrContextLimit) || before != after || result.Run.Status == "completed" {
		t.Fatalf("oversized newest group replaced selection: %+v %v", result, err)
	}
}

func TestCompactionBackupRestoreRetainsSummaryAndSourceHashes(t *testing.T) {
	service, tasks, chat, _ := summaryFixture(t)
	first, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(service.Store.DataDir(), service.Config); err != nil {
		t.Fatal(err)
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
	core := *service
	core.Store = store
	state, err := NewTaskService(&core).ContextState(t.Context(), chat)
	if err != nil || !reflect.DeepEqual(state, first.State) {
		t.Fatalf("restored summary differs: %+v %v", state, err)
	}
}
