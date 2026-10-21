package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func autoHarness(t *testing.T, model ModelClient) (*Service, *TaskService) {
	t.Helper()
	core, _, _ := testHarness(t, model)
	core.Config.MaxOutputTokens = 256
	core.Config.Models = map[string]ModelConfig{
		"default": {Provider: "echo", Capabilities: ModelCapabilities{Strength: "strong"}},
		"light":   {Provider: "echo", Capabilities: ModelCapabilities{Strength: "light"}},
	}
	core.Config.Auto.Enabled = true
	core.Config.DefaultSelection = "auto"
	saveAutoConfig(t, core)
	return core, NewTaskService(core)
}

func saveAutoConfig(t *testing.T, core *Service) {
	t.Helper()
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	core.Refresh = func() (Config, error) { return LoadConfig(core.Store.DataDir()) }
}

func TestAutoManualSelectionAndDisableHaveNoAssessment(t *testing.T) {
	var requests []ModelRequest
	core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		if request.Purpose != "" {
			t.Fatal("manual/known intent unexpectedly invoked assessment")
		}
		return ModelResponse{Content: "from " + request.ModelID}, nil
	}))
	core.Config.Auto.AssessmentModel = "default"
	saveAutoConfig(t, core)
	chat := conversation(t, core)
	if err := tasks.SelectModel(t.Context(), chat, "light"); err != nil {
		t.Fatal(err)
	}
	manual, err := tasks.Submit(t.Context(), chat, "manual-chat", "architecture root cause")
	if err != nil || manual.Run.ModelID != "light" || manual.Run.Routing[0].Mode != "manual" {
		t.Fatalf("Chat manual selection=%+v err=%v", manual, err)
	}
	override, err := tasks.SubmitWithOptions(t.Context(), chat, "manual-override", "hello", RunOptions{ModelID: "default"})
	if err != nil || override.Run.ModelID != "default" {
		t.Fatalf("task manual selection=%+v err=%v", override, err)
	}
	selected, _ := tasks.SelectedModel(t.Context(), chat)
	if selected != "light" {
		t.Fatal("task override changed the Chat selection")
	}
	automatic, err := tasks.SubmitWithOptions(t.Context(), chat, "auto", "hello", RunOptions{ModelID: "auto"})
	if err != nil || automatic.Run.ModelID != "light" || automatic.Run.Routing[0].Mode != "auto" {
		t.Fatalf("explicit Auto=%+v err=%v", automatic, err)
	}
	core.Config.Auto.Enabled = false
	core.Config.DefaultSelection = "default"
	saveAutoConfig(t, core)
	off, err := tasks.Submit(t.Context(), chat, "off", "unknown intent")
	if err != nil || off.Run.ModelID != "light" || len(requests) != 4 {
		t.Fatalf("disabled Auto changed manual work: %+v err=%v", off, err)
	}
	if _, err := tasks.SubmitWithOptions(t.Context(), chat, "off-auto", "hello", RunOptions{ModelID: "auto"}); !errors.Is(err, ErrAutoDisabled) {
		t.Fatalf("disabled Auto accepted: %v", err)
	}
	if len(requests) != 4 {
		t.Fatal("disabled Auto dispatched a routing request")
	}
}

func TestAutoRulesPoolSingleModelAndPromptLength(t *testing.T) {
	for _, test := range []struct {
		name   string
		prompt string
		want   string
		pool   []string
		single bool
	}{
		{"small", "hello", "light", nil, false},
		{"small-cross-domain", "simple list: physics, music, law", "light", nil, false},
		{"long-small", "format this text " + strings.Repeat("source text ", 800), "light", nil, false},
		{"strong", "debug the architecture root cause", "default", nil, false},
		{"chinese", "复杂推理，寻找根因", "default", nil, false},
		{"uncertain", "consider this situation", "default", nil, false},
		{"authorized-only", "architecture", "light", []string{"light"}, false},
		{"single-model", "hello", "default", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				if request.ModelID != test.want {
					t.Errorf("selected %s, want %s", request.ModelID, test.want)
				}
				return ModelResponse{Content: "done"}, nil
			}))
			core.Config.Auto.Models = test.pool
			if test.single {
				delete(core.Config.Models, "light")
			}
			saveAutoConfig(t, core)
			result, err := tasks.Submit(t.Context(), "", "rule", test.prompt)
			if err != nil || result.Run.Status != "completed" || calls != 1 ||
				result.Run.Routing[0].Assessments != 0 || result.Run.Routing[0].Reason == "" {
				t.Fatalf("routing=%+v calls=%d err=%v", result, calls, err)
			}
			if test.pool != nil && !slices.Equal(result.Run.Routing[0].Candidates, test.pool) {
				t.Fatalf("escaped authorized pool: %+v", result.Run.Routing)
			}
		})
	}
}

func TestAutoFiltersActualSerializedRequestAndToolHistory(t *testing.T) {
	for _, scenario := range []string{"tool-required", "large-definitions", "tool-history", "all-incompatible"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				number := calls.Add(1)
				if scenario == "tool-history" && number == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_list", map[string]string{"path": "."})}}, nil
				}
				if request.ModelID != "default" {
					t.Error("Auto selected a model incompatible with the actual request")
				}
				return ModelResponse{Content: "done"}, nil
			}))
			profile := core.Config.Models["light"]
			profile.Capabilities.Tools = "none"
			if scenario == "large-definitions" {
				profile.Capabilities.Tools, profile.Capabilities.ContextBytes = "supported", 4096
				core.Tools = describedTools{description: strings.Repeat("tool description ", 500)}
			}
			core.Config.Models["light"] = profile
			chat := conversation(t, core)
			if scenario == "tool-history" {
				saveAutoConfig(t, core)
				prior, err := tasks.SubmitWithOptions(t.Context(), chat, "prior", "file_list", RunOptions{ModelID: "default"})
				if err != nil || prior.Run.Status != "completed" {
					t.Fatalf("prior=%+v err=%v", prior, err)
				}
			}
			prompt := "hello"
			if scenario == "tool-required" || scenario == "all-incompatible" {
				prompt = "simple read file"
			}
			if scenario == "all-incompatible" {
				core.Config.Auto.Models = []string{"light"}
			}
			saveAutoConfig(t, core)
			before := calls.Load()
			result, err := tasks.Submit(t.Context(), chat, "filter", prompt)
			if scenario == "all-incompatible" {
				if !errors.Is(err, ErrModelCapability) || result.Run.ID != "" || calls.Load() != before {
					t.Fatalf("incompatible pool dispatched: %+v err=%v", result, err)
				}
			} else if err != nil || result.Run.ModelID != "default" || calls.Load() != before+1 {
				t.Fatalf("capability filter=%+v err=%v", result, err)
			}
		})
	}
}

type describedTools struct{ description string }

func (tools describedTools) Definitions() []ToolDefinition {
	return []ToolDefinition{{Name: "read", Effect: EffectRead, Description: tools.description, InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (tools describedTools) Prepare(context.Context, ToolCall) (PreparedTool, error) {
	return PreparedTool{}, errors.New("not used")
}

func TestAutoAssessmentIsBoundedRecordedAndNotConversationAuthority(t *testing.T) {
	for _, assessment := range []string{
		`{"model_id":"light","reason":"bounded low intensity"}`,
		`{"model_id":"unconfigured","reason":"try another endpoint"}`,
		`{"model_id":"light","reason":"override","max_steps":100}`,
		`not JSON`,
	} {
		t.Run(assessment, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
				calls.Add(1)
				if request.Purpose == "assessment" {
					if len(request.Tools) != 0 || request.MaxOutputTokens > 512 {
						t.Error("assessment had authority or unbounded output")
					}
					emit(assessment)
					return ModelResponse{Content: assessment}, nil
				}
				return ModelResponse{Content: "final from " + request.ModelID}, nil
			}))
			core.Config.Auto.AssessmentModel = "default"
			saveAutoConfig(t, core)
			var streamed string
			tasks.SetEvents(func(event Event) {
				if event.Kind == "model_text" {
					streamed += event.Message
				}
			})
			result, err := tasks.Submit(t.Context(), "", "assessed", "consider this situation")
			want := "default"
			if strings.Contains(assessment, "bounded low intensity") {
				want = "light"
			}
			if err != nil || result.Run.Status != "completed" || result.Run.ModelID != want ||
				result.Run.Steps != 2 || calls.Load() != 2 || len(result.Run.Routing) != 2 ||
				result.Run.Routing[1].Assessments != 1 || streamed != "" {
				t.Fatalf("assessment=%+v streamed=%q calls=%d err=%v", result, streamed, calls.Load(), err)
			}
			messages, err := tasks.Messages(t.Context(), result.Run.ConversationID)
			if err != nil || len(messages) != 2 || messages[1].Content != result.Answer {
				t.Fatalf("assessment became Chat history: %+v err=%v", messages, err)
			}
			attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
			var request ModelRequest
			if err != nil || len(attempts) != 2 || json.Unmarshal([]byte(attempts[0].RequestJSON), &request) != nil ||
				request.Purpose != "assessment" || attempts[0].Status != "finished" {
				t.Fatalf("assessment not durable: %+v err=%v", attempts, err)
			}
			reopenHarness(t, core)
			decisions, err := tasks.Routing(t.Context(), result.Run.ID)
			if err != nil || len(decisions) != 2 || decisions[1].Assessments != 1 {
				t.Fatalf("decisions lost on restart: %+v err=%v", decisions, err)
			}
			core.Config.Auto.Enabled, core.Config.DefaultSelection = false, "default"
			saveAutoConfig(t, core)
			repeated, err := tasks.Submit(t.Context(), result.Run.ConversationID, "assessed", "consider this situation")
			if err != nil || !repeated.Duplicate || calls.Load() != 2 {
				t.Fatalf("duplicate rerouted: %+v calls=%d err=%v", repeated, calls.Load(), err)
			}
		})
	}
}

func TestAutoAssessmentLimitsAndUnknownDoNotRetry(t *testing.T) {
	for _, scenario := range []string{"step-limit", "lost", "definite-failure", "illegal-tools"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				if request.Purpose == "assessment" {
					switch scenario {
					case "lost":
						return ModelResponse{}, errors.New("lost response")
					case "definite-failure":
						return ModelResponse{}, &CallError{Certain: true, Message: "definite rejection"}
					case "illegal-tools":
						return ModelResponse{ToolCalls: []ToolCall{makeCall("escape", "file_create", map[string]string{"path": "escape", "content": "forbidden"})}}, nil
					}
					return ModelResponse{Content: `{"model_id":"light","reason":"simple task"}`}, nil
				}
				return ModelResponse{Content: "done"}, nil
			}))
			core.Config.Auto.AssessmentModel = "default"
			if scenario == "step-limit" {
				core.Config.MaxSteps = 1
			}
			saveAutoConfig(t, core)
			result, err := tasks.Submit(t.Context(), "", "bounded", "consider this situation")
			if err == nil || calls.Load() != 1 || result.Run.Steps != 1 {
				t.Fatalf("failure bypassed limits: %+v calls=%d err=%v", result, calls.Load(), err)
			}
			reopenHarness(t, core)
			if scenario == "step-limit" {
				if result.Run.Status != "limited" || !errors.Is(err, ErrStepLimit) {
					t.Fatalf("step limit=%+v err=%v", result, err)
				}
				limits, _ := tasks.Limits(t.Context(), result.Run.ID)
				limits.MaxSteps = 2
				if err := tasks.IncreaseLimits(t.Context(), result.Run.ID, limits); err != nil {
					t.Fatal(err)
				}
				final, err := tasks.Continue(t.Context(), result.Run.ID)
				if err != nil || final.Run.Status != "completed" || final.Run.ModelID != "light" || calls.Load() != 2 {
					t.Fatalf("limited assessment was repeated: %+v calls=%d err=%v", final, calls.Load(), err)
				}
			} else {
				if scenario == "definite-failure" && result.Run.Status != "failed" ||
					scenario != "definite-failure" && result.Run.Status != "unknown" {
					t.Fatalf("wrong failure certainty: %+v err=%v", result, err)
				}
				if _, err := tasks.Continue(t.Context(), result.Run.ID); err == nil || calls.Load() != 1 {
					t.Fatalf("failed assessment retried: calls=%d err=%v", calls.Load(), err)
				}
				duplicate, err := tasks.Submit(t.Context(), result.Run.ConversationID, "bounded", "consider this situation")
				if err != nil || !duplicate.Duplicate || calls.Load() != 1 {
					t.Fatalf("failed assessment replayed: %+v err=%v", duplicate, err)
				}
			}
			attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
			for _, attempt := range attempts {
				if attempt.Kind == "tool" {
					t.Fatalf("assessment granted tool authority: %+v err=%v", attempts, err)
				}
			}
		})
	}
}

func TestAutoResumeSavedAssessmentWithoutAnotherDispatch(t *testing.T) {
	var assessments, ordinary atomic.Int32
	core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "assessment" {
			assessments.Add(1)
			return ModelResponse{Content: `{"model_id":"light","reason":"low intensity"}`}, nil
		}
		ordinary.Add(1)
		return ModelResponse{Content: "done"}, nil
	}))
	core.Config.Auto.AssessmentModel = "default"
	saveAutoConfig(t, core)
	chat := conversation(t, core)
	bound, err := core.forTask(t.Context(), chat, "auto", "consider this situation")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := bound.snapshot()
	run, _, err := core.Store.CreateRun(t.Context(), chat, "saved-assessment", "consider this situation", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := modelBinding(core.Config, "default")
	bound.callOverride = &binding
	request, _ := bound.assessmentRequest(run)
	if _, _, err := bound.generate(t.Context(), run, request); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption after the durable response, before routing is saved.
	reopenHarness(t, core)
	result, err := tasks.Continue(t.Context(), run.ID)
	if err != nil || result.Run.Status != "completed" || result.Run.ModelID != "light" ||
		assessments.Load() != 1 || ordinary.Load() != 1 {
		t.Fatalf("saved assessment repeated/lost: %+v assessments=%d ordinary=%d err=%v",
			result, assessments.Load(), ordinary.Load(), err)
	}
}

func TestAutoUpgradeAtCompleteGroupRetainsConsumptionAndBounds(t *testing.T) {
	for _, upgrades := range []int{0, 1} {
		t.Run(string(rune('0'+upgrades)), func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if calls.Add(1) == 1 {
					if request.ModelID != "light" {
						t.Error("simple task did not start on light profile")
					}
					return ModelResponse{ToolCalls: []ToolCall{makeCall("probe", "probe", map[string]string{})}}, nil
				}
				if request.ModelID != "default" || completeMessageGroups(request.Messages) != nil {
					t.Error("upgrade lost a saved tool group")
				}
				return ModelResponse{Content: "done"}, nil
			}))
			core.Tools = boundaryTools{effect: EffectRead, execute: func(context.Context) (ToolResult, error) {
				return ToolResult{Content: strings.Repeat("observed data ", 310)}, nil
			}}
			profile := core.Config.Models["light"]
			profile.Capabilities.ContextBytes = 4096
			core.Config.Models["light"] = profile
			core.Config.Auto.MaxUpgrades = upgrades
			saveAutoConfig(t, core)
			result, err := tasks.Submit(t.Context(), "", "upgrade", "simple read file")
			if upgrades == 0 {
				if !errors.Is(err, ErrContextLimit) || result.Run.Status != "limited" ||
					result.Run.Steps != 2 || calls.Load() != 1 {
					t.Fatalf("unapproved upgrade: %+v calls=%d err=%v", result, calls.Load(), err)
				}
				if err := tasks.SwitchModel(t.Context(), result.Run.ID, "default"); err != nil {
					t.Fatal(err)
				}
				result, err = tasks.Continue(t.Context(), result.Run.ID)
			}
			if err != nil || result.Run.Status != "completed" || result.Run.Steps != 3 ||
				result.Run.InitialModel != "light" || result.Run.ModelID != "default" || calls.Load() != 2 {
				t.Fatalf("upgrade reset execution: %+v calls=%d err=%v", result, calls.Load(), err)
			}
			if upgrades == 1 && result.Run.Routing[len(result.Run.Routing)-1].Upgrades != 1 {
				t.Fatalf("upgrade was not durable: %+v", result.Run.Routing)
			}
			attempts, _ := core.Store.ListAttempts(t.Context(), result.Run.ID)
			if len(attempts) != 3 || attempts[0].ModelID != "light" || attempts[2].ModelID != "default" {
				t.Fatalf("actual models lost: %+v", attempts)
			}
		})
	}
}

func TestAutoRevocationStopsAssessmentAndUpgradeWithoutReplacingSavedBinding(t *testing.T) {
	for _, scenario := range []string{"disable-before-assessment", "restrict-before-assessment", "disable-after-assessment", "restrict-before-upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				switch request.Purpose {
				case "assessment":
					return ModelResponse{Content: `{"model_id":"light","reason":"simple"}`}, nil
				default:
					if scenario == "restrict-before-upgrade" {
						return ModelResponse{ToolCalls: []ToolCall{makeCall("probe", "probe", map[string]string{})}}, nil
					}
					return ModelResponse{Content: "done"}, nil
				}
			}))
			core.Config.Auto.AssessmentModel = "default"
			prompt := "consider this situation"
			if scenario == "restrict-before-upgrade" {
				prompt = "simple read file"
				profile := core.Config.Models["light"]
				profile.Capabilities.ContextBytes = 4096
				core.Config.Models["light"] = profile
				core.Tools = boundaryTools{effect: EffectRead, execute: func(context.Context) (ToolResult, error) {
					current, _ := LoadConfig(core.Store.DataDir())
					current.Auto.Models, current.Auto.AssessmentModel = []string{"light"}, ""
					if err := WriteConfig(core.Store.DataDir(), current); err != nil {
						return ToolResult{}, err
					}
					return ToolResult{Content: strings.Repeat("observed data ", 310)}, nil
				}}
			}
			saveAutoConfig(t, core)
			chat := conversation(t, core)
			bound, err := core.forTask(t.Context(), chat, "auto", prompt)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, _ := bound.snapshot()
			run, _, err := core.Store.CreateRun(t.Context(), chat, "revoke", prompt, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "disable-after-assessment" {
				binding, _ := modelBinding(core.Config, "default")
				bound.callOverride = &binding
				request, _ := bound.assessmentRequest(run)
				if _, _, err := bound.generate(t.Context(), run, request); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "restrict-before-upgrade" {
				current, _ := LoadConfig(core.Store.DataDir())
				if scenario == "restrict-before-assessment" {
					current.Auto.Models, current.Auto.AssessmentModel = []string{"default"}, ""
				} else {
					current.Auto.Enabled, current.DefaultSelection = false, "default"
				}
				if err := WriteConfig(core.Store.DataDir(), current); err != nil {
					t.Fatal(err)
				}
			}
			result, err := tasks.Continue(t.Context(), run.ID)
			wantCalls := int32(1)
			if scenario == "disable-after-assessment" {
				wantCalls = 2
			}
			if scenario == "restrict-before-upgrade" {
				if !errors.Is(err, ErrContextLimit) || result.Run.Status != "limited" {
					t.Fatalf("revoked upgrade=%+v err=%v", result, err)
				}
			} else if err != nil || result.Run.Status != "completed" || result.Run.ModelID != "default" {
				t.Fatalf("revocation replaced the saved model: %+v err=%v", result, err)
			}
			if calls.Load() != wantCalls {
				t.Fatalf("revoked auxiliary work dispatched: calls=%d", calls.Load())
			}
			reopenHarness(t, core)
			saved, _ := core.Store.GetRun(t.Context(), run.ID)
			frozen, err := decodeRunSnapshot(saved.ConfigJSON)
			if err != nil || frozen.Auto != nil || frozen.Routing[len(frozen.Routing)-1].Mode != "manual" {
				t.Fatalf("revocation was not durable: %+v err=%v", frozen, err)
			}
			if scenario == "disable-after-assessment" && frozen.Routing[len(frozen.Routing)-1].Assessments != 1 {
				t.Fatal("revocation hid the saved assessment consumption")
			}
		})
	}
}

func TestAutoRevocationBetweenSelectionAndDispatchStopsNewCall(t *testing.T) {
	for _, scenario := range []string{"adapter-assembly", "saved-upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks := autoHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				if calls.Add(1) == 1 && scenario == "saved-upgrade" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("probe", "probe", map[string]string{})}}, nil
				}
				return ModelResponse{Content: "done"}, nil
			}))
			revoke := func() {
				current, err := LoadConfig(core.Store.DataDir())
				if err != nil {
					t.Fatal(err)
				}
				current.Auto.Enabled, current.DefaultSelection = false, "default"
				if err := WriteConfig(core.Store.DataDir(), current); err != nil {
					t.Fatal(err)
				}
			}
			prompt := "hello"
			if scenario == "adapter-assembly" {
				once := false
				core.ModelFor = func(ModelConfig) (ModelClient, error) {
					if !once {
						once = true
						revoke()
					}
					return core.Model, nil
				}
			} else {
				prompt = "simple read file"
				profile := core.Config.Models["light"]
				profile.Capabilities.ContextBytes = 4096
				core.Config.Models["light"] = profile
				core.Tools = boundaryTools{effect: EffectRead, execute: func(context.Context) (ToolResult, error) {
					return ToolResult{Content: strings.Repeat("observed data ", 310)}, nil
				}}
				tasks.SetEvents(func(event Event) {
					if event.Kind == "model_selected" && strings.Contains(event.Message, "capability upgrade") {
						revoke()
					}
				})
			}
			saveAutoConfig(t, core)
			result, err := tasks.Submit(t.Context(), "", "dispatch-revoke", prompt)
			wantCalls, wantSteps := int32(0), 0
			if scenario == "saved-upgrade" {
				wantCalls, wantSteps = 1, 2
			}
			if !errors.Is(err, ErrAutoDisabled) || result.Run.Status != "stopped" ||
				calls.Load() != wantCalls || result.Run.Steps != wantSteps {
				t.Fatalf("revocation raced past dispatch: %+v calls=%d err=%v", result, calls.Load(), err)
			}
			reopenHarness(t, core)
			final, err := tasks.Continue(t.Context(), result.Run.ID)
			if err != nil || final.Run.Status != "completed" || calls.Load() != wantCalls+1 ||
				final.Run.Steps != wantSteps+1 || final.Run.Routing[len(final.Run.Routing)-1].Mode != "manual" {
				t.Fatalf("explicit continuation lost saved selection/facts: %+v calls=%d err=%v", final, calls.Load(), err)
			}
		})
	}
}

func TestAutoAssessmentDispatchCapIsTransactional(t *testing.T) {
	core, _ := autoHarness(t, EchoModel{})
	core.Config.Auto.AssessmentModel = "default"
	saveAutoConfig(t, core)
	chat := conversation(t, core)
	bound, err := core.forTask(t.Context(), chat, "auto", "consider this situation")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := bound.snapshot()
	run, _, err := core.Store.CreateRun(t.Context(), chat, "cap", "consider this situation", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := modelBinding(core.Config, "default")
	bound.callOverride = &binding
	request, _ := bound.assessmentRequest(run)
	raw, _ := json.Marshal(request)
	if _, err := core.Store.startModel(t.Context(), run, string(raw), 12); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Store.startModel(t.Context(), run, string(raw), 12); !errors.Is(err, ErrAutoDisabled) {
		t.Fatalf("second assessment was dispatched: %v", err)
	}
	current, _ := core.Store.GetRun(t.Context(), run.ID)
	attempts, _ := core.Store.ListAttempts(t.Context(), run.ID)
	if current.Steps != 1 || len(attempts) != 1 {
		t.Fatalf("failed assessment reservation consumed/reset counters: %+v attempts=%+v", current, attempts)
	}
}

func TestAutoSnapshotRejectsInconsistentPolicyAndDecision(t *testing.T) {
	core, _ := autoHarness(t, EchoModel{})
	bound, err := core.forTask(t.Context(), conversation(t, core), "auto", "hello")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := bound.snapshot()
	for _, scenario := range []string{"unauthorized-binding", "duplicate-candidate", "wrong-request", "wrong-revision", "over-limit"} {
		t.Run(scenario, func(t *testing.T) {
			snapshot, err := decodeRunSnapshot(raw)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unauthorized-binding":
				snapshot.Auto.Models = snapshot.Auto.Models[:1]
			case "duplicate-candidate":
				snapshot.Routing[0].Candidates = []string{"light", "light"}
			case "wrong-request":
				snapshot.Routing[0].RequestedModel = "light"
			case "wrong-revision":
				snapshot.Routing[0].SelectedRevision = strings.Repeat("0", 64)
			case "over-limit":
				snapshot.Routing[0].Upgrades = 2
			}
			corrupt, _ := json.Marshal(snapshot)
			if _, err := decodeRunSnapshot(string(corrupt)); !errors.Is(err, ErrStoreFormat) {
				t.Fatalf("inconsistent %s was accepted: %v", scenario, err)
			}
		})
	}
}

func TestAutoAssessmentAndMainShareParentCumulativeGuard(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			var calls atomic.Int32
			collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				if request.Purpose == "assessment" {
					return ModelResponse{Content: `{"model_id":"default","reason":"only authorized model"}`}, nil
				}
				return ModelResponse{Content: "analysis only"}, nil
			}))
			core.Config.Auto.Enabled, core.Config.Auto.AssessmentModel = true, "default"
			core.Config.MaxOutputTokens = 256
			core.Config.instructionText = ""
			saveAutoConfig(t, core)
			plan.Budget.MaxModelCalls = limit
			id := createCollaboration(t, collab, plan)
			record, _ := loadCollaboration(t.Context(), core.Store.db, id)
			child := record.Children[0]
			service, err := collab.childService(core, record, child)
			if err != nil {
				t.Fatal(err)
			}
			service, err = service.forTask(t.Context(), "", "auto", "consider this situation")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, _ := service.snapshot()
			if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE runs SET config_json=? WHERE id=?`, snapshot, child.RunID); err != nil {
				t.Fatal(err)
			}
			if err := collab.activate(t.Context(), id, []string{child.Spec.Name}); err != nil {
				t.Fatal(err)
			}
			result, err := service.Continue(t.Context(), child.RunID)
			if limit == 1 {
				if !errors.Is(err, ErrParentBudget) || result.Run.Status != "limited" {
					t.Fatalf("assessment escaped parent cap: %+v err=%v", result, err)
				}
			} else if err != nil || result.Run.Status != "completed" {
				t.Fatalf("bounded guarded result=%+v err=%v", result, err)
			}
			saved, _ := loadCollaboration(t.Context(), core.Store.db, id)
			if saved.Usage.ModelCalls != limit || calls.Load() != int32(limit) || result.Run.Steps != limit ||
				saved.Usage.OutputBytes < 1 || len(saved.Reservations) != limit {
				t.Fatalf("shared cumulative facts=%+v calls=%d result=%+v", saved.Usage, calls.Load(), result.Run)
			}
		})
	}
}
