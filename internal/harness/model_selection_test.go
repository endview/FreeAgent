package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func selectionHarness(t *testing.T, model ModelClient) (*Service, *TaskService, string) {
	t.Helper()
	core, _, workspace := testHarness(t, model)
	// Echo takes no model name; a capability gives "second" its own revision.
	core.Config.Models["second"] = ModelConfig{Provider: "echo", Capabilities: ModelCapabilities{Strength: "light"}}
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	core.Refresh = func() (Config, error) { return LoadConfig(core.Store.DataDir()) }
	return core, NewTaskService(core), workspace
}

func TestModelSelectionHistoryIdentityAndTaskPrecedence(t *testing.T) {
	var calls atomic.Int32
	core, tasks, workspace := selectionHarness(t, nil)
	core.ModelFor = func(config ModelConfig) (ModelClient, error) {
		return modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
			calls.Add(1)
			binding, _ := modelBinding(core.Config, req.ModelID)
			if req.ModelRevision != binding.Revision || config != binding.Config {
				t.Errorf("wrong actual binding: %+v config=%+v", req, config)
			}
			last := req.Messages[len(req.Messages)-1]
			if last.Content == "create once" {
				return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{
					"path": "one.txt", "content": "created once",
				})}}, nil
			}
			if req.ModelID == "second" {
				var savedTool bool
				for _, message := range req.Messages {
					savedTool = savedTool || message.Role == "tool" && message.ToolCallID == "create"
				}
				if !savedTool {
					t.Error("model switch lost the completed tool group")
				}
			}
			return ModelResponse{Content: "answer from " + req.ModelID}, nil
		}), nil
	}
	id := conversation(t, core)
	waiting, err := tasks.Submit(t.Context(), id, "create", "create once")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectModel(t.Context(), id, "second"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SwitchModel(t.Context(), waiting.Run.ID, "second"); !errors.Is(err, ErrModelBoundary) {
		t.Fatalf("switched an incomplete tool group: %v", err)
	}
	finished, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision, true)
	if err != nil || finished.Run.ModelID != "default" || finished.Run.InitialModel != "default" {
		t.Fatalf("saved operation was redirected: %+v err=%v", finished, err)
	}
	next, err := tasks.Submit(t.Context(), id, "next", "continue the discussion")
	if err != nil || next.Run.ModelID != "second" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	override, err := tasks.SubmitWithOptions(t.Context(), id, "override", "manual task", RunOptions{ModelID: "default"})
	if err != nil || override.Run.ModelID != "default" {
		t.Fatalf("override=%+v err=%v", override, err)
	}
	selected, _ := tasks.SelectedModel(t.Context(), id)
	if selected != "second" {
		t.Fatal("task override silently changed the Chat selection")
	}
	before := calls.Load()
	repeated, err := tasks.Submit(t.Context(), id, "create", "create once")
	if err != nil || !repeated.Duplicate || repeated.Run.ID != finished.Run.ID || calls.Load() != before {
		t.Fatalf("model selection replayed an edit: %+v calls=%d err=%v", repeated, calls.Load(), err)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "one.txt"))
	if err != nil || string(raw) != "created once" {
		t.Fatalf("file=%q err=%v", raw, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), finished.Run.ID)
	if err != nil || len(attempts) != 3 || attempts[0].ModelID != "default" ||
		attempts[2].ModelRevision != attempts[0].ModelRevision {
		t.Fatalf("actual identity not retained: %+v err=%v", attempts, err)
	}
	list, err := tasks.Conversations(t.Context())
	if err != nil || len(list) != 1 || list[0].ModelID != "second" {
		t.Fatalf("selection projection=%+v err=%v", list, err)
	}
	detached := tasks.Config()
	delete(detached.Models, "second")
	if _, ok := tasks.Config().Models["second"]; !ok {
		t.Fatal("a caller mutated the runtime through Config()")
	}
}

type boundaryTools struct {
	effect  ToolEffect
	execute func(context.Context) (ToolResult, error)
}

func (tools boundaryTools) Definitions() []ToolDefinition {
	return []ToolDefinition{{Name: "probe", Effect: tools.effect, InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (tools boundaryTools) Prepare(context.Context, ToolCall) (PreparedTool, error) {
	return PreparedTool{Effect: tools.effect, Preview: "exact probe", Execute: tools.execute}, nil
}

func TestModelSelectionDuringActiveBoundaries(t *testing.T) {
	for _, boundary := range []string{"stream", "tool", "approval"} {
		t.Run(boundary, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			core, tasks, _ := selectionHarness(t, modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
				number := calls.Add(1)
				if number == 1 && boundary == "stream" {
					emit("partial")
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return ModelResponse{}, ctx.Err()
					}
				}
				if number == 1 && boundary != "stream" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("probe", "probe", map[string]string{})}}, nil
				}
				if number == 1 && req.ModelID != "default" {
					t.Errorf("active work changed models: %s", req.ModelID)
				}
				return ModelResponse{Content: "done"}, nil
			}))
			if boundary != "stream" {
				effect := EffectRead
				if boundary == "approval" {
					effect = EffectModify
					tasks.SetApproval(func(ctx context.Context, _ ToolCall, _ PreparedTool) (bool, error) {
						close(started)
						select {
						case <-release:
							return true, nil
						case <-ctx.Done():
							return false, ctx.Err()
						}
					})
				}
				core.Tools = boundaryTools{effect: effect, execute: func(ctx context.Context) (ToolResult, error) {
					if boundary == "tool" {
						close(started)
						select {
						case <-release:
						case <-ctx.Done():
							return ToolResult{}, ctx.Err()
						}
					}
					return ToolResult{Content: "observed"}, nil
				}}
			}
			id := conversation(t, core)
			done := make(chan struct {
				result RunResult
				err    error
			}, 1)
			go func() {
				result, err := tasks.Submit(t.Context(), id, "active", "task")
				done <- struct {
					result RunResult
					err    error
				}{result, err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("call did not reach the boundary")
			}
			if err := tasks.SelectModel(t.Context(), id, "second"); err != nil {
				t.Fatal(err)
			}
			run, _ := tasks.Latest(t.Context(), id)
			if err := tasks.SwitchModel(t.Context(), run.ID, "second"); !errors.Is(err, ErrActiveRun) {
				t.Fatalf("active run was edited: %v", err)
			}
			close(release)
			out := <-done
			if out.err != nil || out.result.Run.ModelID != "default" || out.result.Run.Status != "completed" {
				t.Fatalf("active outcome=%+v err=%v", out.result, out.err)
			}
			// The next call is the first call of a new task, not active old work.
			core.Model = EchoModel{}
			next, err := tasks.Submit(t.Context(), id, "next", "next task")
			if err != nil || next.Run.ModelID != "second" {
				t.Fatalf("future selection=%+v err=%v", next, err)
			}
		})
	}
}

func TestSafeRunSwitchAndFiniteIncreasePreserveConsumption(t *testing.T) {
	for _, selection := range []string{"second", "new-revision"} {
		t.Run(selection, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks, _ := selectionHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
				if calls.Add(1) == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_list", map[string]string{"path": "."})}}, nil
				}
				if err := completeMessageGroups(req.Messages); err != nil {
					t.Errorf("incomplete switched request: %v", err)
				}
				return ModelResponse{Content: "continued without replay"}, nil
			}))
			core.Config.MaxSteps = 2
			if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
				t.Fatal(err)
			}
			limited, err := tasks.Submit(t.Context(), "", "limited", "read then answer")
			if !errors.Is(err, ErrStepLimit) || limited.Run.Steps != 2 || limited.Run.Status != "limited" {
				t.Fatalf("limited=%+v err=%v", limited, err)
			}
			old, _ := core.Store.ListAttempts(t.Context(), limited.Run.ID)
			id := selection
			if selection == "new-revision" {
				config := core.Config
				config.Models = map[string]ModelConfig{"default": {Provider: "echo", Capabilities: ModelCapabilities{Strength: "standard"}}}
				if err := WriteConfig(core.Store.DataDir(), config); err != nil {
					t.Fatal(err)
				}
				core.Config = config
				id = "default"
			}
			if err := tasks.SwitchModel(t.Context(), limited.Run.ID, id); err != nil {
				t.Fatal(err)
			}
			current, _ := core.Store.GetRun(t.Context(), limited.Run.ID)
			if current.Steps != limited.Run.Steps || current.ElapsedMS != limited.Run.ElapsedMS ||
				current.Status != "limited" || current.InitialModel != "default" || current.ModelRevision == limited.Run.ModelRevision {
				t.Fatalf("switch reset facts: %+v", current)
			}
			limits, _ := tasks.Limits(t.Context(), limited.Run.ID)
			limits.MaxSteps = 4
			if err := tasks.IncreaseLimits(t.Context(), limited.Run.ID, limits); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("switch or limit edit dispatched a call")
			}
			reopenHarness(t, core)
			config, err := LoadConfig(core.Store.DataDir())
			if err != nil {
				t.Fatal(err)
			}
			core.Config = config
			after, err := tasks.Continue(t.Context(), limited.Run.ID)
			if err != nil || after.Run.Status != "completed" || after.Run.Steps != 3 || calls.Load() != 2 ||
				after.Run.ElapsedMS < limited.Run.ElapsedMS {
				t.Fatalf("continue=%+v calls=%d err=%v", after, calls.Load(), err)
			}
			attempts, _ := core.Store.ListAttempts(t.Context(), limited.Run.ID)
			if len(attempts) != 3 || attempts[0] != old[0] || attempts[1] != old[1] ||
				attempts[2].ModelID != id || attempts[2].ModelRevision != current.ModelRevision {
				t.Fatalf("historical attempts changed: %+v", attempts)
			}
		})
	}
}

func TestUnknownCannotBeReplayedBySelectionLimitsOrReopen(t *testing.T) {
	var calls atomic.Int32
	core, tasks, _ := selectionHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{}, errors.New("lost response")
	}))
	result, err := tasks.Submit(t.Context(), "", "lost", "task")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := tasks.SelectModel(t.Context(), result.Run.ConversationID, "second"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SwitchModel(t.Context(), result.Run.ID, "second"); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("UNKNOWN switched: %v", err)
	}
	limits, _ := tasks.Limits(t.Context(), result.Run.ID)
	limits.MaxSteps++
	limits.MaxRunSeconds++
	if err := tasks.IncreaseLimits(t.Context(), result.Run.ID, limits); err != nil {
		t.Fatal(err)
	}
	reopenHarness(t, core)
	current, _ := core.Store.GetRun(t.Context(), result.Run.ID)
	if current.Status != "unknown" || current.Steps != result.Run.Steps || current.ElapsedMS != result.Run.ElapsedMS ||
		current.ModelID != "default" {
		t.Fatalf("UNKNOWN facts changed: %+v", current)
	}
	if _, err := tasks.Continue(t.Context(), current.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("UNKNOWN continued: %v", err)
	}
	if _, err := tasks.Submit(t.Context(), current.ConversationID, "copy", "task"); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("new key bypassed unfinished work: %v", err)
	}
	duplicate, err := tasks.Submit(t.Context(), current.ConversationID, "lost", "task")
	if err != nil || !duplicate.Duplicate || duplicate.Run.Status != "unknown" || calls.Load() != 1 {
		t.Fatalf("UNKNOWN retry=%+v calls=%d err=%v", duplicate, calls.Load(), err)
	}
	lower := limits
	lower.MaxSteps--
	if err := tasks.IncreaseLimits(t.Context(), current.ID, lower); err == nil {
		t.Fatal("decreased a saved limit")
	}
	invalid := limits
	invalid.MaxRunSeconds = 0
	if err := tasks.IncreaseLimits(t.Context(), current.ID, invalid); err == nil {
		t.Fatal("unbounded limit accepted")
	}
}

func TestModelCapabilitiesBoundDispatch(t *testing.T) {
	for _, scenario := range []string{"plain", "unexpected-tools", "context", "tool-history"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			core, tasks, _ := selectionHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
				number := calls.Add(1)
				if scenario == "tool-history" && number == 1 || scenario == "unexpected-tools" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_list", map[string]string{"path": "."})}}, nil
				}
				if scenario != "tool-history" && (len(req.Tools) != 0 || req.MaxOutputTokens != 16) {
					t.Errorf("illegal no-tool request: %+v", req)
				}
				return ModelResponse{Content: "analysis"}, nil
			}))
			caps := ModelCapabilities{Tools: "none", ContextBytes: 8192, OutputTokens: 16}
			if scenario == "context" {
				caps.ContextBytes = 4096
			}
			profile := core.Config.Models["second"]
			profile.Capabilities = caps
			core.Config.Models["second"] = profile
			if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
				t.Fatal(err)
			}
			id := conversation(t, core)
			if scenario == "tool-history" {
				prior, err := tasks.Submit(t.Context(), id, "prior", "read")
				if err != nil || prior.Run.Status != "completed" {
					t.Fatalf("prior=%+v err=%v", prior, err)
				}
			}
			if err := tasks.SelectModel(t.Context(), id, "second"); err != nil {
				t.Fatal(err)
			}
			prompt := "analyze"
			if scenario == "context" {
				prompt = strings.Repeat("x", 4000)
			}
			before := calls.Load()
			result, err := tasks.Submit(t.Context(), id, "selected", prompt)
			switch scenario {
			case "plain":
				if err != nil || result.Run.Status != "completed" || calls.Load() != before+1 {
					t.Fatalf("plain=%+v err=%v", result, err)
				}
			case "unexpected-tools":
				if !errors.Is(err, ErrModelCapability) || result.Run.Status != "unknown" {
					t.Fatalf("invalid tools=%+v err=%v", result, err)
				}
			case "context":
				if !errors.Is(err, ErrContextLimit) || result.Run.Steps != 0 || calls.Load() != before {
					t.Fatalf("oversized request dispatched: %+v err=%v", result, err)
				}
			case "tool-history":
				if !errors.Is(err, ErrModelCapability) || result.Run.Steps != 0 || calls.Load() != before {
					t.Fatalf("illegal tool history sent: %+v err=%v", result, err)
				}
			}
		})
	}
}

func TestApprovalWaitExcludedFromActiveExecution(t *testing.T) {
	var calls atomic.Int32
	core, _, workspace := selectionHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{
				makeCall("one", "file_create", map[string]string{"path": "one", "content": "one"}),
				makeCall("two", "file_create", map[string]string{"path": "two", "content": "two"}),
			}}, nil
		}
		return ModelResponse{Content: "both complete"}, nil
	}))
	core.Config.MaxRunSeconds = 1
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	id := conversation(t, core)
	approvals := 0
	core.Approve = func(ctx context.Context, _ ToolCall, _ PreparedTool) (bool, error) {
		approvals++
		run, _ := core.Store.GetLatestRun(ctx, id)
		if run.ActiveSince != "" {
			t.Error("approval idle time was not durably paused")
		}
		select {
		case <-time.After(650 * time.Millisecond):
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	result, err := core.Run(t.Context(), id, "slow-approval", "create both")
	if err != nil || result.Run.Status != "completed" || result.Run.ElapsedMS >= 1000 || approvals != 2 {
		t.Fatalf("wait charged as execution: %+v approvals=%d err=%v", result, approvals, err)
	}
	for _, name := range []string{"one", "two"} {
		if raw, err := os.ReadFile(filepath.Join(workspace, name)); err != nil || string(raw) != name {
			t.Fatalf("file=%q err=%v", raw, err)
		}
	}
}

func TestApprovalCancellationAndReopenPreserveIdleBoundary(t *testing.T) {
	var calls atomic.Int32
	core, tasks, _ := selectionHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{"path": "one", "content": "one"})}}, nil
		}
		return ModelResponse{Content: "created"}, nil
	}))
	core.Config.MaxRunSeconds = 1
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	tasks.SetApproval(func(ctx context.Context, _ ToolCall, _ PreparedTool) (bool, error) {
		close(started)
		<-ctx.Done()
		return false, ctx.Err()
	})
	id := conversation(t, core)
	done := make(chan error, 1)
	go func() { _, err := tasks.Submit(t.Context(), id, "cancel", "create"); done <- err }()
	<-started
	if !tasks.Cancel() {
		t.Fatal("approval wait was not cancellable")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	before, _ := tasks.Latest(t.Context(), id)
	if before.Status != "stopped" || before.ActiveSince != "" || before.Steps != 1 {
		t.Fatalf("cancelled boundary=%+v", before)
	}
	time.Sleep(1100 * time.Millisecond)
	reopenHarness(t, core)
	after, _ := tasks.Latest(t.Context(), id)
	if after.ElapsedMS != before.ElapsedMS || after.ActiveSince != "" {
		t.Fatalf("reopen billed idle time: before=%+v after=%+v", before, after)
	}
	tasks.SetApproval(func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil })
	finished, err := tasks.Continue(t.Context(), before.ID)
	if err != nil || finished.Run.Status != "completed" || finished.Run.Steps != 3 || calls.Load() != 2 {
		t.Fatalf("continued=%+v calls=%d err=%v", finished, calls.Load(), err)
	}
}

func TestActiveModelStillTimesOutWithoutReplay(t *testing.T) {
	var calls atomic.Int32
	core, tasks, _ := selectionHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}))
	core.Config.MaxRunSeconds = 1
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	result, err := tasks.Submit(t.Context(), "", "active-timeout", "wait")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" || result.Run.ElapsedMS < 950 ||
		time.Since(start) > 5*time.Second {
		t.Fatalf("active timeout=%+v err=%v", result, err)
	}
	if _, err := tasks.Continue(t.Context(), result.Run.ID); !errors.Is(err, ErrUnknownResult) || calls.Load() != 1 {
		t.Fatalf("timeout replay=%v calls=%d", err, calls.Load())
	}
}

func TestPredecessorFormatsRejectedBeforeRecovery(t *testing.T) {
	for _, old := range []string{"config", "store", "backup"} {
		t.Run(old, func(t *testing.T) {
			core, _, _ := testHarness(t, EchoModel{})
			data := core.Store.DataDir()
			snapshot, _ := core.snapshot()
			run, _, err := core.Store.CreateRun(t.Context(), conversation(t, core), "interrupted", "task", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.Store.startModel(t.Context(), run, `{}`, 12); err != nil {
				t.Fatal(err)
			}
			if old == "store" {
				if _, err := core.Store.db.ExecContext(t.Context(), `PRAGMA user_version=1`); err != nil {
					t.Fatal(err)
				}
			}
			if old == "backup" {
				backup := filepath.Join(t.TempDir(), "backup")
				if err := core.Store.Backup(t.Context(), backup); err != nil {
					t.Fatal(err)
				}
				body, _ := os.ReadFile(filepath.Join(backup, "backup.json"))
				var manifest backupManifest
				_ = json.Unmarshal(body, &manifest)
				manifest.Format = "freeagent-harness-backup-v1"
				body, _ = json.Marshal(manifest)
				if err := os.WriteFile(filepath.Join(backup, "backup.json"), body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := VerifyBackup(t.Context(), backup); err == nil {
					t.Fatal("old backup accepted")
				}
				target := filepath.Join(t.TempDir(), "must-not-exist")
				if err := Restore(t.Context(), backup, target); err == nil {
					t.Fatal("old backup restored")
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed restore published a directory")
				}
				return
			}
			if err := core.Store.Close(); err != nil {
				t.Fatal(err)
			}
			if old == "config" {
				config := core.Config
				config.Version = 1
				body, _ := json.Marshal(config)
				if err := os.WriteFile(filepath.Join(data, "config.json"), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(data, "state.sqlite")
			before, _ := os.ReadFile(path)
			if store, err := OpenStore(t.Context(), data); err == nil {
				_ = store.Close()
				t.Fatal("old format opened")
			}
			after, _ := os.ReadFile(path)
			if contentHash(after) != contentHash(before) {
				t.Fatal("rejected predecessor was changed by recovery")
			}
			if tasks, closeAll, err := OpenTasks(t.Context(), data, Adapters{}); err == nil {
				closeAll()
				t.Fatalf("old format started adapters: %v", tasks)
			}
		})
	}
}
