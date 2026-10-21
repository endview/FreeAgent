package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestMemoryManagementCopyPreviewRequiresCurrentSource(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "source", "PRIVATE_COPY_V1")
	enableKnowledge(t, core)
	destination := MemoryScope{Kind: "knowledge", Owner: "explicit-export", Project: project}
	preview, err := tasks.PreviewMemoryCopy(t.Context(), chat, ref, destination, "", "", "copy")
	if err != nil || preview.Body != "PRIVATE_COPY_V1" || preview.Scope != destination ||
		len(preview.Sources) != 1 || preview.Sources[0].Revision != 1 {
		t.Fatalf("copy preview=%+v err=%v", preview, err)
	}
	update := memoryCreate(ref.Scope, "source-update", "PRIVATE_COPY_V2")
	update.Action, update.EntryID, update.ExpectedRevision = "update", ref.EntryID, 1
	if _, err := tasks.SaveMemory(t.Context(), chat, update); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SaveMemory(t.Context(), chat, preview); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("outdated export accepted: %v", err)
	}
	entries, err := tasks.ListMemory(t.Context(), chat, destination, 16)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed copy wrote a destination=%+v err=%v", entries, err)
	}
	preview, err = tasks.PreviewMemoryCopy(t.Context(), chat, ref, destination, "finding", "Selected export", "copy-current")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := tasks.SaveMemory(t.Context(), chat, preview)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := tasks.ReadMemory(t.Context(), chat, destination, receipt.EntryID)
	if err != nil || entry.Body != "PRIVATE_COPY_V2" || entry.Sources[0].Revision != 2 ||
		entry.Sources[0].EntryID != ref.EntryID || entry.Author != "user" {
		t.Fatalf("explicit export=%+v err=%v", entry, err)
	}
	if again, err := tasks.SaveMemory(t.Context(), chat, preview); err != nil ||
		!again.Duplicate || again.EntryID != receipt.EntryID {
		t.Fatalf("copy duplicate=%+v err=%v", again, err)
	}
	action := MemoryMutation{RequestKey: "disable-source", Action: "disable", Scope: ref.Scope,
		EntryID: ref.EntryID, ExpectedRevision: 2}
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.PreviewMemoryCopy(t.Context(), chat, ref, destination, "", "", "disabled-copy"); !errors.Is(err, ErrMemoryDisabled) {
		t.Fatalf("disabled export=%v", err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "delete-source", "delete", 3
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.PreviewMemoryCopy(t.Context(), chat, ref, destination, "", "", "deleted-copy"); !errors.Is(err, ErrMemoryDeleted) {
		t.Fatalf("deleted export=%v", err)
	}
}

func TestMemoryManagementRunPreviewScopeAndMetadata(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	completed, err := tasks.Submit(t.Context(), chat, "completed", "Selected output")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := tasks.AgentIdentity(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	scope := MemoryScope{Kind: "private", Owner: agent, Project: project}
	preview, err := tasks.PreviewRunMemory(t.Context(), chat, completed.Run.ID, scope, "", "Saved output", "result")
	if err != nil || preview.Body != completed.Answer || preview.Kind != "finding" ||
		preview.Sources[0].RunID != completed.Run.ID || preview.Sources[0].Kind != "run" {
		t.Fatalf("run preview=%+v err=%v", preview, err)
	}
	if _, err := tasks.SaveMemory(t.Context(), chat, preview); err != nil {
		t.Fatal(err)
	}
	scopes, err := tasks.MemoryScopes(t.Context(), chat, "private")
	if err != nil || len(scopes) != 1 || scopes[0] != scope {
		t.Fatalf("private scope metadata=%+v err=%v", scopes, err)
	}
	for _, state := range []string{"stopped", "waiting_confirmation", "unknown", "failed", "abandoned"} {
		if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE runs SET status=? WHERE id=?`, state, completed.Run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.PreviewRunMemory(t.Context(), chat, completed.Run.ID, scope, "", "Not complete", state); err == nil {
			t.Fatalf("%s output accepted for a saved completed result", state)
		}
	}
	if _, err := tasks.MemoryScopes(t.Context(), chat, "all"); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("unsupported scope=%v", err)
	}
}

func TestMemoryManagementSettingsIdleAndFinite(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	completed, err := tasks.Submit(t.Context(), chat, "ordinary", "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	memory := core.Config.Memory
	memory.KnowledgeEnabled = true
	memory.MaxEntries = 8
	if err := tasks.SetMemoryConfig(t.Context(), memory); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(core.Store.DataDir())
	if err != nil || config.Memory != memory || tasks.Config().Memory != memory {
		t.Fatalf("saved config=%+v err=%v", config.Memory, err)
	}
	bad := memory
	bad.MaxEntries = 0
	if err := tasks.SetMemoryConfig(t.Context(), bad); err == nil {
		t.Fatal("unbounded/invalid settings saved")
	}
	for _, state := range []string{"running", "waiting_confirmation", "stopped", "unknown", "limited"} {
		if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE runs SET status=? WHERE id=?`, state, completed.Run.ID); err != nil {
			t.Fatal(err)
		}
		change := memory
		change.KnowledgeEnabled = false
		if err := tasks.SetMemoryConfig(t.Context(), change); !errors.Is(err, ErrActiveRun) {
			t.Fatalf("settings changed during %s: %v", state, err)
		}
		config, err := LoadConfig(core.Store.DataDir())
		if err != nil || config.Memory != memory {
			t.Fatalf("rejected settings modified config=%+v err=%v", config.Memory, err)
		}
	}
}

func TestCollaborationMemoryKnowledgeSelectionAndParentSources(t *testing.T) {
	selected := MemoryScope{Kind: "knowledge", Owner: "selected"}
	calls := 0
	collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		scope := requestMemoryScope(t, request, "knowledge")
		if scope != selected {
			t.Fatalf("unselected scope granted: %+v", scope)
		}
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("selected", "knowledge_search", map[string]any{
				"scope": scope, "query": "EXPLICIT_KNOWLEDGE_BODY", "max_bytes": 4096,
			})}}, nil
		}
		raw, _ := json.Marshal(request.Messages)
		if !strings.Contains(string(raw), "EXPLICIT_KNOWLEDGE_BODY") ||
			strings.Contains(string(raw), "UNSELECTED_PRIVATE_BODY") {
			t.Fatal("authorized knowledge missing or parent private context leaked")
		}
		return ModelResponse{Content: "An interpretation, not a check"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	enableKnowledge(t, core)
	selected.Project = core.Config.Workspace
	if _, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(selected, "knowledge", "EXPLICIT_KNOWLEDGE_BODY")); err != nil {
		t.Fatal(err)
	}
	ref := retainedMemory(t, tasks, chat, "secret", "UNSELECTED_PRIVATE_BODY")
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{
		Entries: []MemoryReference{ref}, Knowledge: []MemoryScope{selected},
	}); err != nil {
		t.Fatal(err)
	}
	plan.Children = plan.Children[:1]
	role := plan.Roles["reviewer"]
	role.Tools, role.Paths, role.RequiredEvidence = nil, nil, nil
	role.Acceptance, role.OutputFormat = "analysis", "text"
	role.ModelID = core.Config.DefaultModel
	role.Memory.Selection.Knowledge = []MemoryScope{selected}
	plan.Roles["reviewer"] = role
	parent, err := collab.Create(t.Context(), chat, "collaboration", plan)
	if err != nil {
		t.Fatal(err)
	}
	report, err := collab.Continue(t.Context(), parent)
	if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 2 || report.Usage.ToolCalls != 1 {
		t.Fatalf("knowledge collaboration=%+v err=%v", report, err)
	}
	sources, err := tasks.MemorySources(t.Context(), parent)
	if err != nil || len(sources) != 1 || sources[0].RunID != report.Children[0].Run.ID ||
		sources[0].Use.Scope != selected || sources[0].Use.Excerpt == nil ||
		sources[0].Current != "active" {
		t.Fatalf("parent sources=%+v err=%v", sources, err)
	}
	record, err := loadCollaboration(t.Context(), core.Store.db, parent)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultCollaborationSettings()
	settings.Mode = "auto"
	record.Settings = &settings
	parentCore, err := core.forRun(report.Parent)
	if err != nil {
		t.Fatal(err)
	}
	if parentCore.memory == nil || !slices.Contains(parentCore.memory.Selection.Knowledge, selected) {
		t.Fatal("parent's explicit knowledge selection was not retained")
	}
	edit := CollaborationEdit{Roles: map[string]RoleProfile{"new": role}}
	if err := authorizeReplan(parentCore, record, edit); err != nil {
		t.Fatalf("authorized collection rejected for a replan: %v", err)
	}
	role.Memory.Selection.Knowledge[0].Owner = "not-selected"
	edit.Roles["new"] = role
	if err := authorizeReplan(parentCore, record, edit); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("replan granted new collection: %v", err)
	}
	role.Memory.Selection.Knowledge = []MemoryScope{selected}
	role.Memory.Selection.Entries = []MemoryReference{ref}
	edit.Roles["new"] = role
	if err := authorizeReplan(parentCore, record, edit); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("replan exported parent private entry: %v", err)
	}
}
