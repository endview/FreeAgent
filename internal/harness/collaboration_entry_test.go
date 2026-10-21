package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func entrySuggestion() CollaborationSuggestion {
	return CollaborationSuggestion{Reason: "Separate independent analysis.",
		Experts: []ExpertSuggestion{{Name: "expert", Objective: "Analyze the trade-off", ModelID: "default"}}}
}

func suggestedResponse(suggestion CollaborationSuggestion) ModelResponse {
	raw, _ := json.Marshal(suggestion)
	return ModelResponse{Content: string(raw)}
}

func entryOptions(mode string) RunOptions {
	settings := DefaultCollaborationSettings()
	settings.Mode = mode
	return RunOptions{Collaboration: &settings}
}

func TestCollaborationEntryPrecedenceAndTaskOnlySelection(t *testing.T) {
	var planning atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "planning" {
			planning.Add(1)
			return suggestedResponse(CollaborationSuggestion{Reason: "One Agent is sufficient."}), nil
		}
		return ModelResponse{Content: "single answer"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	settings := DefaultCollaborationSettings()
	settings.Mode = "auto"
	if err := tasks.SetDefaultCollaborationSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if got, err := tasks.CollaborationSettings(t.Context(), chat); err != nil || got.Mode != "auto" {
		t.Fatalf("defaults=%+v err=%v", got, err)
	}
	settings.Mode = "off"
	if err := tasks.SetCollaborationSettings(t.Context(), chat, settings); err != nil {
		t.Fatal(err)
	}
	for index, options := range []RunOptions{{}, entryOptions("auto"), {}} {
		result, err := tasks.SubmitWithOptions(t.Context(), chat, string(rune('a'+index)), "simple task", options)
		if err != nil || result.Run.Status != "completed" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if planning.Load() != 1 {
		t.Fatalf("off/once/off planning calls=%d", planning.Load())
	}
	if got, err := tasks.CollaborationSettings(t.Context(), chat); err != nil || got.Mode != "off" {
		t.Fatalf("task suggestion changed Chat preference: %+v %v", got, err)
	}
	config, err := LoadConfig(core.Store.DataDir())
	if err != nil || config.Collaboration.Mode != "auto" {
		t.Fatalf("Chat preference changed defaults: %+v %v", config.Collaboration, err)
	}
	raw := core.Store.DataDir()
	_ = core.Store.Close()
	reopened, err := OpenStore(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	core.Store = reopened
	if got, err := NewTaskService(core).CollaborationSettings(t.Context(), chat); err != nil || got.Mode != "off" {
		t.Fatalf("Chat preference did not survive reopen: %+v %v", got, err)
	}
}

func TestCollaborationEntryAskAcceptEditReject(t *testing.T) {
	for _, choice := range []string{"accept", "edit", "reject"} {
		t.Run(choice, func(t *testing.T) {
			var planning, reviews atomic.Int32
			core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose == "planning" {
					planning.Add(1)
					return suggestedResponse(entrySuggestion()), nil
				}
				return ModelResponse{Content: "analysis result"}, nil
			}))
			core.Config.Models["other"] = core.Config.Models["default"]
			tasks := NewTaskService(core)
			chat := conversation(t, core)
			before, _ := os.ReadFile(filepath.Join(core.Store.DataDir(), "config.json"))
			options := entryOptions("ask")
			options.Review = func(_ context.Context, suggestion CollaborationSuggestion) (CollaborationSuggestion, bool, error) {
				reviews.Add(1)
				if choice == "edit" {
					suggestion.Experts[0].Objective = "Edited objective"
					suggestion.Experts[0].ModelID = "other"
				}
				return suggestion, choice != "reject", nil
			}
			result, err := tasks.SubmitWithOptions(t.Context(), chat, "asked", "task goal", options)
			if err != nil || result.Run.Status != "completed" || reviews.Load() != 1 || planning.Load() != 1 {
				t.Fatalf("result=%+v reviews=%d err=%v", result, reviews.Load(), err)
			}
			report, err := tasks.CollaborationReport(t.Context(), result.Run.ID)
			if err != nil || report.Usage.ModelCalls != 2 {
				t.Fatalf("parent cumulative calls=%+v %v", report.Usage, err)
			}
			if choice == "reject" {
				if len(report.Children) != 0 || result.Answer != "analysis result" {
					t.Fatalf("reject started experts: %+v", report)
				}
			} else if len(report.Children) != 1 || report.Children[0].Run.Status != "completed" ||
				strings.Contains(result.Answer, `"usage"`) || !strings.Contains(result.Answer, "Analysis conclusion") {
				t.Fatalf("ordinary result exposed internal ledger: %+v", result)
			} else if choice == "edit" && (report.Children[0].Run.ModelID != "other" ||
				report.Children[0].Spec.Objective != "Edited objective") {
				t.Fatalf("edit/model identity lost: %+v", report.Children[0])
			}
			after, _ := os.ReadFile(filepath.Join(core.Store.DataDir(), "config.json"))
			if string(before) != string(after) {
				t.Fatal("task-only suggestion changed persisted defaults")
			}
			ordinary, err := tasks.Submit(t.Context(), chat, "ordinary", "normal chat")
			if err != nil || ordinary.Run.Status != "completed" || planning.Load() != 1 {
				t.Fatalf("Chat stayed controlled: %+v %v", ordinary, err)
			}
		})
	}
}

func TestCollaborationEntrySavedReviewRecovery(t *testing.T) {
	var planning atomic.Int32
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "planning" {
			planning.Add(1)
			return suggestedResponse(entrySuggestion()), nil
		}
		return ModelResponse{Content: "recovered analysis"}, nil
	})
	core, _, _ := testHarness(t, model)
	tasks := NewTaskService(core)
	result, err := tasks.SubmitWithOptions(t.Context(), "", "saved", "task", entryOptions("ask"))
	if err != nil || result.Run.Status != "waiting_confirmation" || result.Run.ActiveSince != "" {
		t.Fatalf("review waiting=%+v %v", result, err)
	}
	path := core.Store.DataDir()
	_ = core.Store.Close()
	store, err := OpenStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	core.Store = store
	tasks = NewTaskService(core)
	options := RunOptions{Review: func(_ context.Context, suggestion CollaborationSuggestion) (CollaborationSuggestion, bool, error) {
		return suggestion, true, nil
	}}
	result, err = tasks.ContinueWithOptions(t.Context(), result.Run.ID, options)
	if err != nil || result.Run.Status != "completed" || planning.Load() != 1 {
		t.Fatalf("saved plan not reused: %+v planning=%d err=%v", result, planning.Load(), err)
	}
	attempts, _ := store.ListAttempts(t.Context(), result.Run.ID)
	if len(attempts) != 1 {
		t.Fatalf("planning replayed=%+v", attempts)
	}
}

func TestCollaborationEntryPlanningAndSingleShareLimits(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			var calls atomic.Int32
			core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				if request.Purpose == "planning" {
					return suggestedResponse(CollaborationSuggestion{Reason: "Single analysis."}), nil
				}
				return ModelResponse{Content: "answer"}, nil
			}))
			options := entryOptions("auto")
			options.Collaboration.Budget.MaxModelCalls = limit
			result, err := NewTaskService(core).SubmitWithOptions(t.Context(), "", "limit", "goal", options)
			if limit == 1 && (!errors.Is(err, ErrParentBudget) || result.Run.Status != "limited") ||
				limit == 2 && (err != nil || result.Run.Status != "completed") || calls.Load() != int32(limit) {
				t.Fatalf("planning escaped parent limit: %+v calls=%d err=%v", result, calls.Load(), err)
			}
		})
	}
}

func TestCollaborationEntryRejectsAuthorityAndInvalidPlans(t *testing.T) {
	for _, change := range []string{"model", "tool", "path", "extra", "duplicate", "cycle"} {
		t.Run(change, func(t *testing.T) {
			suggestion := entrySuggestion()
			switch change {
			case "model":
				suggestion.Experts[0].ModelID = "unconfigured"
			case "tool":
				suggestion.Experts[0].Tools = []string{"file_read"}
			case "path":
				suggestion.Experts[0].Paths = []string{"secret"}
			case "duplicate":
				suggestion.Experts = append(suggestion.Experts, suggestion.Experts[0])
			case "cycle":
				suggestion.Experts[0].DependsOn = []string{"expert"}
			}
			response := suggestedResponse(suggestion)
			if change == "extra" {
				response.Content = `{"reason":"unsafe","experts":[],"limits":{"max_children":99}}`
			}
			var calls atomic.Int32
			core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls.Add(1)
				return response, nil
			}))
			tasks := NewTaskService(core)
			result, err := tasks.SubmitWithOptions(t.Context(), "", "invalid", "goal", entryOptions("auto"))
			if err == nil || result.Run.Status == "completed" || calls.Load() != 1 {
				t.Fatalf("invalid suggestion executed: %+v %v", result, err)
			}
			report, _ := tasks.CollaborationReport(t.Context(), result.Run.ID)
			if len(report.Children) != 0 || report.Usage.Children != 0 {
				t.Fatal("invalid suggestion partially created children")
			}
			_, _ = tasks.Continue(t.Context(), result.Run.ID)
			if calls.Load() != 1 {
				t.Fatal("invalid saved response triggered another planning dispatch")
			}
		})
	}
}

func TestCollaborationEntryChatOffCancelsPlanningAndPreservesUnknown(t *testing.T) {
	started := make(chan struct{})
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan RunResult, 1)
	go func() {
		result, _ := tasks.SubmitWithOptions(t.Context(), chat, "cancel", "goal", entryOptions("auto"))
		done <- result
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("planning did not start")
	}
	if err := tasks.SetCollaborationSettings(t.Context(), chat, DefaultCollaborationSettings()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		attempts, _ := core.Store.ListAttempts(t.Context(), result.Run.ID)
		if result.Run.Status != "abandoned" || len(attempts) != 1 || attempts[0].Status != "unknown" {
			t.Fatalf("Chat off erased ambiguous outcome: %+v %+v", result, attempts)
		}
		before, _ := tasks.CollaborationReport(t.Context(), result.Run.ID)
		_, err := tasks.Continue(t.Context(), result.Run.ID)
		after, _ := tasks.CollaborationReport(t.Context(), result.Run.ID)
		if err == nil || !reflect.DeepEqual(before.Usage, after.Usage) {
			t.Fatalf("cancelled planning continued: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Chat off did not cancel active planning")
	}
}

func TestCollaborationEntryFailedPlanningIsTerminal(t *testing.T) {
	var calls atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{}, &CallError{Certain: true, Message: "definite provider rejection"}
	}))
	tasks := NewTaskService(core)
	result, err := tasks.SubmitWithOptions(t.Context(), "", "failed", "goal", entryOptions("auto"))
	if err == nil || result.Run.Status != "failed" {
		t.Fatalf("failure=%+v %v", result, err)
	}
	if _, err := tasks.Continue(t.Context(), result.Run.ID); err == nil || calls.Load() != 1 {
		t.Fatalf("failed plan replayed: %v", err)
	}
}

func TestCollaborationEntryDeclineExactToolDoesNotConsumeDispatch(t *testing.T) {
	var calls atomic.Int32
	core, _, workspace := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "planning" {
			return suggestedResponse(CollaborationSuggestion{Reason: "Single execution."}), nil
		}
		if request.Messages[len(request.Messages)-1].Role == "user" {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create",
				map[string]string{"path": "declined.txt", "content": "no"})}}, nil
		}
		return ModelResponse{Content: "operation declined"}, nil
	}))
	tasks := NewTaskService(core)
	waiting, err := tasks.SubmitWithOptions(t.Context(), "", "tool", "create", entryOptions("auto"))
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting=%+v %v", waiting, err)
	}
	preview, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Confirm(t.Context(), waiting.Run.ID, preview, false)
	if err != nil || result.Run.Status != "completed" || calls.Load() != 3 {
		t.Fatalf("decline=%+v %v", result, err)
	}
	report, _ := tasks.CollaborationReport(t.Context(), result.Run.ID)
	if report.Usage.ToolCalls != 0 {
		t.Fatalf("decline charged dispatch=%+v", report.Usage)
	}
	if _, err := os.Stat(filepath.Join(workspace, "declined.txt")); !os.IsNotExist(err) {
		t.Fatal("declined tool executed")
	}
}

func TestCollaborationEntryChatOffBeforeCreationCommits(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "suggested", true: "explicit"}[manual], func(t *testing.T) {
			var calls atomic.Int32
			var refreshes atomic.Int32
			core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls.Add(1)
				return suggestedResponse(entrySuggestion()), nil
			}))
			tasks := NewTaskService(core)
			chat := conversation(t, core)
			settings := DefaultCollaborationSettings()
			settings.Mode = "auto"
			if err := tasks.SetCollaborationSettings(t.Context(), chat, settings); err != nil {
				t.Fatal(err)
			}
			core.Refresh = func() (Config, error) {
				refreshes.Add(1)
				err := tasks.SetCollaborationSettings(t.Context(), chat, DefaultCollaborationSettings())
				return tasks.Config(), err
			}
			var err error
			if manual {
				plan, planErr := entrySuggestion().plan(core, "goal", settings)
				if planErr != nil {
					t.Fatal(planErr)
				}
				_, err = NewCollaboration(tasks).Create(t.Context(), chat, "create", plan)
			} else {
				_, err = tasks.Submit(t.Context(), chat, "create", "goal")
			}
			var runs int
			_ = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runs`).Scan(&runs)
			if !errors.Is(err, ErrConfigChanged) || runs != 0 || calls.Load() != 0 || refreshes.Load() == 0 {
				t.Fatalf("revoked selection created work: err=%v runs=%d calls=%d", err, runs, calls.Load())
			}
		})
	}
}

func TestCollaborationEntryChatOffDuringExpertAndSingle(t *testing.T) {
	for _, mode := range []string{"expert", "single"} {
		for _, outcome := range []string{"unknown", "saved"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				started := make(chan struct{})
				var calls atomic.Int32
				core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
					calls.Add(1)
					if request.Purpose == "planning" {
						suggestion := entrySuggestion()
						if mode == "single" {
							suggestion.Experts = nil
						}
						return suggestedResponse(suggestion), nil
					}
					close(started)
					<-ctx.Done()
					if outcome == "saved" {
						return ModelResponse{Content: "definite late result"}, nil
					}
					return ModelResponse{}, ctx.Err()
				}))
				tasks := NewTaskService(core)
				chat := conversation(t, core)
				done := make(chan RunResult, 1)
				go func() {
					result, _ := tasks.SubmitWithOptions(t.Context(), chat, "active", "goal", entryOptions("auto"))
					done <- result
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					tasks.Cancel()
					t.Fatal("main work did not start")
				}
				if err := tasks.SetCollaborationSettings(t.Context(), chat, DefaultCollaborationSettings()); err != nil {
					tasks.Cancel()
					t.Fatal(err)
				}
				select {
				case result := <-done:
					if result.Run.Status != "abandoned" || calls.Load() != 2 {
						t.Fatalf("active collaboration did not stop: %+v calls=%d", result, calls.Load())
					}
					report, err := tasks.CollaborationReport(t.Context(), result.Run.ID)
					if err != nil || report.Usage.ModelCalls != 2 || report.Usage.ActiveChildren != 0 {
						t.Fatalf("cancel lost accounting: %+v %v", report, err)
					}
					activeID := result.Run.ID
					if mode == "expert" {
						activeID = report.Children[0].Run.ID
					}
					active, _ := core.Store.GetRun(t.Context(), activeID)
					attempts, _ := core.Store.ListAttempts(t.Context(), activeID)
					last := attempts[len(attempts)-1]
					if outcome == "unknown" && last.Status != "unknown" ||
						outcome == "saved" && (last.Status != "finished" || active.Output != "definite late result") {
						t.Fatalf("actual outcome was erased: %+v %+v", active, last)
					}
					before := report.Usage
					_, err = tasks.Continue(t.Context(), result.Run.ID)
					after, _ := tasks.CollaborationReport(t.Context(), result.Run.ID)
					if err == nil || calls.Load() != 2 || !reflect.DeepEqual(before, after.Usage) {
						t.Fatal("cancelled work was replayed or refunded")
					}
				case <-time.After(5 * time.Second):
					tasks.Cancel()
					t.Fatal("Chat off did not cancel foreground work")
				}
			})
		}
	}
}

func TestCollaborationEntryChatOffDuringSingleConfirmation(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		if request.Purpose == "planning" {
			return suggestedResponse(CollaborationSuggestion{Reason: "Single execution is sufficient."}), nil
		}
		if request.Messages[len(request.Messages)-1].Role == "user" {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create",
				map[string]string{"path": "approved.txt", "content": "saved effect"})}}, nil
		}
		close(started)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	waiting, err := tasks.SubmitWithOptions(t.Context(), chat, "confirmation", "create file", entryOptions("auto"))
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting=%+v %v", waiting, err)
	}
	preview, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan RunResult, 1)
	go func() {
		result, _ := tasks.Confirm(t.Context(), waiting.Run.ID, preview, true)
		done <- result
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		tasks.Cancel()
		t.Fatal("confirmed operation did not reach model continuation")
	}
	if err := tasks.SetCollaborationSettings(t.Context(), chat, DefaultCollaborationSettings()); err != nil {
		tasks.Cancel()
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Run.Status != "abandoned" || calls.Load() != 3 ||
			len(result.Evidence.Files) != 1 || result.Evidence.Files[0].Status != "applied" {
			t.Fatalf("confirmation cancellation erased effect: %+v", result)
		}
	case <-time.After(5 * time.Second):
		tasks.Cancel()
		t.Fatal("Chat off did not reach active confirmation")
	}
}

func TestCollaborationEntrySavedAutoAssessmentAndPlanningRecovery(t *testing.T) {
	var assessments, planning, main atomic.Int32
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		switch request.Purpose {
		case "assessment":
			assessments.Add(1)
			return ModelResponse{Content: `{"model_id":"default","reason":"Suitable authorized profile"}`}, nil
		case "planning":
			planning.Add(1)
			return suggestedResponse(entrySuggestion()), nil
		default:
			main.Add(1)
			return ModelResponse{Content: "recovered expert analysis"}, nil
		}
	}))
	core.Config.Auto.Enabled = true
	core.Config.Auto.AssessmentModel = "default"
	tasks := NewTaskService(core)
	options := entryOptions("ask")
	options.ModelID = "auto"
	waiting, err := tasks.SubmitWithOptions(t.Context(), "", "recovery", "compare the alternatives", options)
	if err != nil || waiting.Run.Status != "waiting_confirmation" || assessments.Load() != 1 || planning.Load() != 1 {
		t.Fatalf("Auto planning wait=%+v assessments=%d planning=%d err=%v", waiting, assessments.Load(), planning.Load(), err)
	}
	data := core.Store.DataDir()
	_ = core.Store.Close()
	reopened, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	core.Store = reopened
	result, err := NewTaskService(core).ContinueWithOptions(t.Context(), waiting.Run.ID, RunOptions{
		Review: func(_ context.Context, suggestion CollaborationSuggestion) (CollaborationSuggestion, bool, error) {
			return suggestion, true, nil
		},
	})
	report, reportErr := NewTaskService(core).CollaborationReport(t.Context(), waiting.Run.ID)
	if err != nil || reportErr != nil || result.Run.Status != "completed" ||
		assessments.Load() != 1 || planning.Load() != 1 || main.Load() != 1 || report.Usage.ModelCalls != 3 {
		t.Fatalf("saved auxiliary calls replayed: %+v report=%+v err=%v %v", result, report, err, reportErr)
	}
}

func TestCollaborationEntryChatOffDuringAdapterAssemblyDoesNotDispatch(t *testing.T) {
	assembling, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	core, _, _ := testHarness(t, nil)
	core.ModelFor = func(ModelConfig) (ModelClient, error) {
		close(assembling)
		<-release
		return modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
			calls.Add(1)
			return suggestedResponse(entrySuggestion()), nil
		}), nil
	}
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	done := make(chan RunResult, 1)
	go func() {
		result, _ := tasks.SubmitWithOptions(t.Context(), chat, "assembly", "goal", entryOptions("auto"))
		done <- result
	}()
	select {
	case <-assembling:
	case <-time.After(5 * time.Second):
		tasks.Cancel()
		close(release)
		t.Fatal("adapter assembly did not start")
	}
	err := tasks.SetCollaborationSettings(t.Context(), chat, DefaultCollaborationSettings())
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		attempts, _ := core.Store.ListAttempts(t.Context(), result.Run.ID)
		report, reportErr := tasks.CollaborationReport(t.Context(), result.Run.ID)
		if result.Run.Status != "abandoned" || len(attempts) != 0 || calls.Load() != 0 ||
			reportErr != nil || report.Usage.ModelCalls != 0 || report.Usage.OutputBytes != 0 {
			t.Fatalf("revoked adapter dispatched: %+v %+v %v", result, report, reportErr)
		}
	case <-time.After(5 * time.Second):
		tasks.Cancel()
		t.Fatal("revoked assembly did not stop")
	}
}

func TestCollaborationEntryChildDeclineAndInvalidToolPreserveAccounting(t *testing.T) {
	for _, choice := range []string{"decline", "invalid"} {
		t.Run(choice, func(t *testing.T) {
			var main atomic.Int32
			core, _, workspace := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if request.Purpose == "planning" {
					suggestion := entrySuggestion()
					suggestion.Experts[0].Tools = []string{"file_create"}
					suggestion.Experts[0].Paths = []string{"selected.txt"}
					return suggestedResponse(suggestion), nil
				}
				if main.Add(1) == 1 {
					file := "selected.txt"
					if choice == "invalid" {
						file = "outside.txt"
					}
					return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create",
						map[string]string{"path": file, "content": "not authorized"})}}, nil
				}
				return ModelResponse{Content: "No modification was executed."}, nil
			}))
			options := entryOptions("auto")
			options.Collaboration.Tools = []string{"file_create"}
			options.Collaboration.Paths = []string{"selected.txt"}
			tasks := NewTaskService(core)
			result, err := tasks.SubmitWithOptions(t.Context(), "", "tool", "goal", options)
			collab := NewCollaboration(tasks)
			if choice == "decline" {
				report, reportErr := collab.Inspect(t.Context(), result.Run.ID)
				if err != nil || reportErr != nil || report.Children[0].Run.Status != "waiting_confirmation" {
					t.Fatalf("waiting=%+v %v %v", report, err, reportErr)
				}
				preview, previewErr := collab.ChildPreview(t.Context(), result.Run.ID, "expert")
				if previewErr != nil {
					t.Fatal(previewErr)
				}
				if _, err := collab.ChildConfirm(t.Context(), result.Run.ID, "expert", preview, false); err != nil {
					t.Fatal(err)
				}
				_, err = collab.Continue(t.Context(), result.Run.ID)
			}
			report, reportErr := collab.Inspect(t.Context(), result.Run.ID)
			if err != nil || reportErr != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 3 ||
				report.Usage.ToolCalls != 0 || report.Usage.ActiveChildren != 0 {
				t.Fatalf("undispatched proposal changed counters: %+v %v %v", report, err, reportErr)
			}
			for _, file := range []string{"selected.txt", "outside.txt"} {
				if _, err := os.Stat(filepath.Join(workspace, file)); !os.IsNotExist(err) {
					t.Fatal("undispatched proposal modified a file")
				}
			}
		})
	}
}
