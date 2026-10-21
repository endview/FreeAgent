package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func enableKnowledge(t *testing.T, core *Service) {
	t.Helper()
	core.Config.Memory.KnowledgeEnabled = true
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeImportPreviewDuplicatesUpdateAndLifecycle(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	scope := MemoryScope{Kind: "knowledge", Owner: "project-notes", Project: project}
	file := filepath.Join(project, "notes.MD")
	original := "prefix \u4e2d\u6587\u5168\u6587\u68c0\u7d22 and source material"
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "off"); err == nil {
		t.Fatal("knowledge off accepted import")
	}
	enableKnowledge(t, core)
	preview, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "first")
	if err != nil || preview.Body != original || preview.Sources[0].Hash != contentHash([]byte(original)) {
		t.Fatalf("preview=%+v %v", preview, err)
	}
	changed := "replacement \u4e2d\u6587\u5168\u6587\u68c0\u7d22"
	if err := os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SaveMemory(t.Context(), chat, preview); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("stale preview saved: %v", err)
	}
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := tasks.SaveMemory(t.Context(), chat, preview)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "second")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := tasks.SaveMemory(t.Context(), chat, second)
	if err != nil || !duplicate.Duplicate || duplicate.EntryID != first.EntryID {
		t.Fatalf("duplicate=%+v %v", duplicate, err)
	}
	if err := os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	// A response lost after duplicate detection still has a durable receipt.
	again, err := tasks.SaveMemory(t.Context(), chat, second)
	if err != nil || !again.Duplicate || again.EntryID != first.EntryID {
		t.Fatalf("lost duplicate response=%+v %v", again, err)
	}
	if _, err := tasks.ImportKnowledgeFile(t.Context(), chat, scope, file, "", "changed-import"); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("changed import did not require explicit update: %v", err)
	}
	update, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "explicit-update")
	if err != nil {
		t.Fatal(err)
	}
	update.Action, update.EntryID, update.ExpectedRevision = "update", first.EntryID, 1
	if receipt, err := tasks.SaveMemory(t.Context(), chat, update); err != nil || receipt.Revision != 2 {
		t.Fatalf("update=%+v %v", receipt, err)
	}
	hits, err := tasks.SearchKnowledge(t.Context(), chat, scope, "\u5168\u6587\u68c0\u7d22", 4, 1024)
	if err != nil || len(hits) != 1 || hits[0].Source.Revision != 2 ||
		hits[0].Source.Hash != contentHash([]byte(changed)) || hits[0].Start != len("replacement \u4e2d\u6587") ||
		hits[0].Excerpt != changed[hits[0].Start:hits[0].End] {
		t.Fatalf("search=%+v %v", hits, err)
	}
	action := MemoryMutation{RequestKey: "disable", Action: "disable", Scope: scope, EntryID: first.EntryID, ExpectedRevision: 2}
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if hits, err := tasks.SearchKnowledge(t.Context(), chat, scope, "\u5168\u6587", 4, 1024); err != nil || len(hits) != 0 {
		t.Fatalf("disabled searchable: %+v %v", hits, err)
	}
	duplicate, err = tasks.ImportKnowledgeFile(t.Context(), chat, scope, file, "", "still-disabled")
	if err != nil || !duplicate.Duplicate || duplicate.State != "disabled" {
		t.Fatalf("duplicate implicitly enabled knowledge: %+v %v", duplicate, err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "enable", "enable", 3
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "delete", "delete", 4
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
	if hits, err := tasks.SearchKnowledge(t.Context(), chat, scope, "\u5168\u6587\u68c0\u7d22", 4, 1024); err != nil || len(hits) != 0 {
		t.Fatalf("deleted search returned after restart: %+v %v", hits, err)
	}
	if _, err := tasks.ReadMemory(t.Context(), chat, scope, first.EntryID); !errors.Is(err, ErrMemoryDeleted) {
		t.Fatalf("deleted reference=%v", err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	restoreStore, err := OpenStore(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreStore.Close()
	restoreTasks := NewTaskService(&Service{Store: restoreStore, Config: core.Config})
	if hits, err := restoreTasks.SearchKnowledge(t.Context(), chat, scope, "\u5168\u6587", 4, 1024); err != nil || len(hits) != 1 || hits[0].Source.Revision != 4 {
		t.Fatalf("backup actual content not restored: %+v %v", hits, err)
	}
}

func TestKnowledgeScopeFiltersLiteralQueryAndBoundedUTF8(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	enableKnowledge(t, core)
	scope := MemoryScope{Kind: "knowledge", Owner: "selected", Project: project}
	body := "\u023aXYZ \u4e2d\u6587\u5168\u6587\u68c0\u7d22 " + strings.Repeat("\u6587", 500)
	one, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "one", body))
	if err != nil {
		t.Fatal(err)
	}
	two, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "two", body))
	if err != nil {
		t.Fatal(err)
	}
	unselected := scope
	unselected.Owner = "not-selected"
	if _, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(unselected, "hidden", body)); err != nil {
		t.Fatal(err)
	}
	reader := memoryAuthority{agent: "reader", runID: "task", project: project,
		grants: []MemoryGrant{{Scope: scope, Read: true, Kinds: []string{"finding"}, Entries: []string{one.EntryID}}}}
	for _, query := range []string{"XYZ", "\u4e2d\u6587", "\u5168\u6587\u68c0\u7d22"} {
		hits, err := searchKnowledge(t.Context(), core.Store.db, reader, scope, query, 4, 256)
		if err != nil || len(hits) != 1 || hits[0].Source.EntryID != one.EntryID ||
			!utf8.ValidString(hits[0].Excerpt) || len(hits[0].Excerpt) > 256 ||
			hits[0].Excerpt != body[hits[0].Start:hits[0].End] || !strings.HasPrefix(hits[0].Excerpt, query) {
			t.Fatalf("query=%q hits=%+v %v", query, hits, err)
		}
	}
	reader.grants[0].Kinds = []string{"hypothesis"}
	if hits, err := searchKnowledge(t.Context(), core.Store.db, reader, scope, "XYZ", 4, 1024); err != nil || len(hits) != 0 {
		t.Fatalf("kind-filtered body retrieved: %+v %v", hits, err)
	}
	if _, err := searchKnowledge(t.Context(), core.Store.db, reader, unselected, "XYZ", 4, 1024); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("unselected collection retrieved: %v", err)
	}
	for _, query := range []string{`XYZ" OR "NOT_FOUND`, `NOT XYZ`, `"`, `OR`} {
		hits, err := tasks.SearchKnowledge(t.Context(), chat, scope, query, 4, 1024)
		if err != nil || len(hits) != 0 {
			t.Fatalf("query operators not literal: %q %+v %v", query, hits, err)
		}
	}
	if hits, err := tasks.SearchKnowledge(t.Context(), chat, scope, "XYZ", 4, 1024); err != nil || len(hits) == 0 || !slices.Contains([]string{one.EntryID, two.EntryID}, hits[0].Source.EntryID) {
		t.Fatalf("scoped query=%+v %v", hits, err)
	}
	otherProject := scope
	otherProject.Project = t.TempDir()
	if _, err := tasks.SearchKnowledge(t.Context(), chat, otherProject, "XYZ", 4, 1024); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("project isolation=%v", err)
	}
}

func TestKnowledgeImportRejectsUnboundedUnsafeAndNonText(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	enableKnowledge(t, core)
	scope := MemoryScope{Kind: "knowledge", Owner: "notes", Project: project}
	for _, fixture := range []struct {
		name string
		body []byte
	}{
		{"huge.md", []byte(strings.Repeat("x", core.Config.Memory.MaxDocumentBytes+1))},
		{"binary.txt", []byte{0xff, 0xfe}},
		{"nul.md", []byte("abc\x00def")},
		{"wrong.pdf", []byte("text")},
		{"empty.txt", nil},
	} {
		file := filepath.Join(project, fixture.name)
		if err := os.WriteFile(file, fixture.body, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "bad"); err == nil {
			t.Fatalf("invalid document accepted: %s", fixture.name)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{outside, filepath.Join(core.Store.DataDir(), "config.md"), "relative.md", filepath.Join(project, ".git", "notes.md")} {
		if _, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, file, "", "unsafe"); err == nil {
			t.Fatalf("unsafe path accepted: %s", file)
		}
	}
	link := filepath.Join(project, "linked.md")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := tasks.PreviewKnowledgeImport(t.Context(), chat, scope, link, "", "link"); err == nil {
			t.Fatal("linked document accepted")
		}
	} else {
		t.Logf("symlink creation unavailable: %v", err)
	}
}

func TestKnowledgeSingleAgentToolSelectionSourcesAndOff(t *testing.T) {
	calls := 0
	var scope MemoryScope
	core, _, project := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			raw, _ := json.Marshal(request.Messages)
			if strings.Contains(string(raw), "DOCUMENT_BODY") {
				t.Fatal("knowledge was injected before explicit search")
			}
			scope = requestMemoryScope(t, request, "knowledge")
			return ModelResponse{ToolCalls: []ToolCall{makeCall("search", "knowledge_search", map[string]any{"scope": scope, "query": "DOCUMENT_BODY"})}}, nil
		}
		if calls == 2 {
			var hits []KnowledgeHit
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &hits); err != nil || len(hits) != 1 {
				t.Fatalf("tool search=%+v %v", hits, err)
			}
			return ModelResponse{Content: "An interpretation of the selected source, not a verified check"}, nil
		}
		if slices.ContainsFunc(request.Tools, func(d ToolDefinition) bool { return d.Name == "knowledge_search" }) {
			t.Fatal("disabled knowledge advertised a tool")
		}
		for _, message := range request.Messages {
			if strings.HasPrefix(message.Content, "Runtime-selected memory scopes") {
				t.Fatal("disabled knowledge left an input grant")
			}
		}
		return ModelResponse{Content: "ordinary single Agent"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	enableKnowledge(t, core)
	scope = MemoryScope{Kind: "knowledge", Owner: "notes", Project: project}
	if _, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "document", "DOCUMENT_BODY")); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectMemory(t.Context(), chat, MemorySelection{Knowledge: []MemoryScope{scope}}); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "selected", "search the selected collection")
	if err != nil || result.Run.Status != "completed" || result.Run.Steps != 3 {
		t.Fatalf("result=%+v %v", result, err)
	}
	sources, err := tasks.MemorySources(t.Context(), result.Run.ID)
	if err != nil || len(sources) != 1 || sources[0].Use.Scope != scope || sources[0].Current != "active" {
		t.Fatalf("sources=%+v %v", sources, err)
	}
	if len(result.Evidence.ChecksPassed) != 0 {
		t.Fatal("retrieved data became verification")
	}
	core.Config.Memory.KnowledgeEnabled = false
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SearchKnowledge(t.Context(), chat, scope, "DOCUMENT_BODY", 4, 1024); err == nil {
		t.Fatal("disabled knowledge search")
	}
	ordinary, err := tasks.Submit(t.Context(), chat, "off", "ordinary conversation")
	if err != nil || ordinary.Run.Status != "completed" || calls != 3 {
		t.Fatalf("ordinary=%+v %v calls=%d", ordinary, err, calls)
	}
	selected, err := tasks.SelectedMemory(t.Context(), chat)
	if err != nil || len(selected.Knowledge) != 1 {
		t.Fatal("disabling knowledge erased the explicit selection")
	}
}
