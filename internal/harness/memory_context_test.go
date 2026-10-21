package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func retainedMemory(t *testing.T, tasks *TaskService, chat, key, body string) MemoryReference {
	t.Helper()
	agent, err := tasks.AgentIdentity(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	scope := MemoryScope{Kind: "private", Owner: agent, Project: tasks.Config().Workspace}
	receipt, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, key, body))
	if err != nil {
		t.Fatal(err)
	}
	return MemoryReference{Scope: scope, EntryID: receipt.EntryID}
}

func TestMemoryExplicitSelectionManualModelsAndNoLearning(t *testing.T) {
	var requests []ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		return ModelResponse{Content: "ordinary answer"}, nil
	}))
	core.Config.Models["other"] = ModelConfig{Provider: "echo"}
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "user-save", "EXPLICIT_PRIVATE_BACKGROUND")
	if _, err := tasks.Submit(t.Context(), chat, "ordinary", "normal chat"); err != nil {
		t.Fatal(err)
	}
	if len(requests[0].Memory) != 0 {
		t.Fatal("ordinary chat retrieved persistent memory automatically")
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ordinary output was learned: entries=%d err=%v", count, err)
	}
	selection := MemorySelection{Entries: []MemoryReference{ref}}
	if err := tasks.SelectMemory(t.Context(), chat, selection); err != nil {
		t.Fatal(err)
	}
	for index, model := range []string{"default", "other"} {
		if err := tasks.SelectModel(t.Context(), chat, model); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.Submit(t.Context(), chat, []string{"first", "second"}[index], "use selected context"); err != nil {
			t.Fatal(err)
		}
		request := requests[index+1]
		raw, _ := json.Marshal(request.Messages)
		if request.ModelID != model || len(request.Memory) != 1 ||
			request.Memory[0].Scope.Owner != ref.Scope.Owner ||
			!strings.Contains(string(raw), "EXPLICIT_PRIVATE_BACKGROUND") ||
			strings.Contains(request.Messages[0].Content, "EXPLICIT_PRIVATE_BACKGROUND") {
			t.Fatalf("model or source/authority boundary changed: %+v", request)
		}
	}
	override, err := tasks.SubmitWithOptions(t.Context(), chat, "override", "not selected", RunOptions{Memory: &MemorySelection{}})
	if err != nil || override.Run.Status != "completed" || len(requests[3].Memory) != 0 {
		t.Fatalf("one-task override=%+v err=%v", override, err)
	}
	if selected, err := tasks.SelectedMemory(t.Context(), chat); err != nil || len(selected.Entries) != 1 {
		t.Fatalf("task override rewrote Chat selection: %+v %v", selected, err)
	}
	other := conversation(t, core)
	if err := tasks.SelectMemory(t.Context(), other, selection); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("other Agent inherited private data: %v", err)
	}
	if err := tasks.SelectAgent(t.Context(), other, ref.Scope.Owner); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectMemory(t.Context(), other, selection); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), other, "explicit-reuse", "use"); err != nil || len(requests[4].Memory) != 1 {
		t.Fatalf("explicit reuse failed: %v", err)
	}
	bad := ref
	bad.Scope.Project = filepath.Join(core.Config.Workspace, "different")
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{Entries: []MemoryReference{bad}}); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("cross-project selection=%v", err)
	}
}

func TestExcludeMemoryNarrowsOnly(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	core.Config.Memory.KnowledgeEnabled = true
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	first := retainedMemory(t, tasks, chat, "first", "one")
	second := retainedMemory(t, tasks, chat, "second", "two")
	knowledge := MemoryScope{Kind: "knowledge", Owner: "notes", Project: core.Config.Workspace}
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{
		Entries: []MemoryReference{first, second}, Knowledge: []MemoryScope{knowledge},
	}); err != nil {
		t.Fatal(err)
	}
	disabled := memoryCreate(second.Scope, "disable", "")
	disabled.Action, disabled.EntryID, disabled.ExpectedRevision = "disable", second.EntryID, 1
	disabled.Kind, disabled.Title, disabled.Body, disabled.Sources = "", "", "", nil
	if _, err := tasks.SaveMemory(t.Context(), chat, disabled); err != nil {
		t.Fatal(err)
	}
	removed, err := tasks.ExcludeMemory(t.Context(), chat, &first, nil)
	if err != nil || !removed {
		t.Fatalf("exclude active entry: removed=%v err=%v", removed, err)
	}
	selected, err := tasks.SelectedMemory(t.Context(), chat)
	if err != nil || len(selected.Entries) != 1 || selected.Entries[0].EntryID != second.EntryID ||
		len(selected.Knowledge) != 1 {
		t.Fatalf("selection after exclude=%+v err=%v", selected, err)
	}
	removed, err = tasks.ExcludeMemory(t.Context(), chat, &first, nil)
	if err != nil || removed {
		t.Fatalf("missing entry: removed=%v err=%v", removed, err)
	}
	removed, err = tasks.ExcludeMemory(t.Context(), chat, nil, &knowledge)
	if err != nil || !removed {
		t.Fatalf("exclude knowledge: removed=%v err=%v", removed, err)
	}
	if _, err := tasks.ExcludeMemory(t.Context(), chat, &second, &knowledge); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("accepted ambiguous exclusion: %v", err)
	}
}

func TestMemoryRequestFrozenDuringStreamingLifecycleAndRestart(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var lock sync.Mutex
	var requests []ModelRequest
	core, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		lock.Lock()
		requests = append(requests, request)
		first := len(requests) == 1
		lock.Unlock()
		if first {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
		}
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "initial", "FROZEN_V1")
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{Entries: []MemoryReference{ref}}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		run RunResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := tasks.Submit(t.Context(), chat, "stream", "use context")
		done <- outcome{result, err}
	}()
	<-started
	update := memoryCreate(ref.Scope, "user-edit", "NEXT_V2")
	update.Action, update.EntryID, update.ExpectedRevision = "update", ref.EntryID, 1
	if _, err := tasks.SaveMemory(t.Context(), chat, update); err != nil {
		t.Fatal(err)
	}
	close(release)
	finished := <-done
	if finished.err != nil {
		t.Fatal(finished.err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "edited", "use context"); err != nil {
		t.Fatal(err)
	}
	if requests[0].Memory[0].Revision != 1 || requests[1].Memory[0].Revision != 2 ||
		requests[0].Memory[0].Hash != contentHash([]byte("FROZEN_V1")) {
		t.Fatal("in-flight memory revision was rewritten")
	}
	sources, err := tasks.MemorySources(t.Context(), finished.run.Run.ID)
	if err != nil || len(sources) != 1 || sources[0].Use.Revision != 1 || sources[0].Current != "updated" {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
	action := MemoryMutation{RequestKey: "disable", Action: "disable", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 2}
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "disabled", "use context"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(requests[2].Messages)
	if strings.Contains(string(body), "NEXT_V2") || requests[2].Memory[0].State != "disabled" {
		t.Fatal("disabled body entered a new request")
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "enable", "enable", 3
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	data := core.Store.DataDir()
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	core.Store = store
	t.Cleanup(func() { _ = store.Close() })
	if _, err := tasks.Submit(t.Context(), chat, "reopen", "use context"); err != nil || requests[3].Memory[0].Revision != 4 {
		t.Fatalf("restored selection=%v", err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "delete", "delete", 4
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "deleted", "use context"); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(requests[4].Messages)
	if strings.Contains(string(body), "NEXT_V2") || requests[4].Memory[0].State != "deleted" {
		t.Fatal("deleted body was resurrected from its selection")
	}
}

func TestMemoryRevisionRevalidationPreventsDispatchAndBoundsContext(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "initial", "before")
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{Entries: []MemoryReference{ref}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	core.ModelFor = func(ModelConfig) (ModelClient, error) {
		update := memoryCreate(ref.Scope, "interleaved-update", "after")
		update.Action, update.EntryID, update.ExpectedRevision = "update", ref.EntryID, 1
		if _, err := tasks.SaveMemory(t.Context(), chat, update); err != nil {
			return nil, err
		}
		return modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
			calls++
			return ModelResponse{Content: "must not be dispatched"}, nil
		}), nil
	}
	result, err := tasks.Submit(t.Context(), chat, "race", "task")
	if !errors.Is(err, ErrMemoryConflict) || calls != 0 || result.Run.Steps != 0 {
		t.Fatalf("stale request dispatched: result=%+v calls=%d err=%v", result, calls, err)
	}
	if attempts, _ := core.Store.ListAttempts(t.Context(), result.Run.ID); len(attempts) != 0 {
		t.Fatal("failed memory validation left a dispatch marker")
	}
	if err := tasks.Abandon(t.Context(), result.Run.ID); err != nil {
		t.Fatal(err)
	}
	core.ModelFor = nil
	core.Config.Memory.MaxContextBytes = 1024
	update := memoryCreate(ref.Scope, "oversized-input", strings.Repeat("x", 2048))
	update.Action, update.EntryID, update.ExpectedRevision = "update", ref.EntryID, 2
	if _, err := tasks.SaveMemory(t.Context(), chat, update); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "bounded", "task"); !errors.Is(err, ErrContextLimit) {
		t.Fatalf("oversized memory input=%v", err)
	}
	core.Config.Memory.MaxDocumentBytes = 1024
	update.RequestKey, update.ExpectedRevision = "oversized-document", 3
	if _, err := tasks.SaveMemory(t.Context(), chat, update); err == nil {
		t.Fatal("configured document limit was ignored")
	}
}
