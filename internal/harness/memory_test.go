package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func memoryCreate(scope MemoryScope, key, body string) MemoryMutation {
	return MemoryMutation{RequestKey: key, Action: "create", Scope: scope, Kind: "finding",
		Title: "Selected background", Body: body, Sources: []MemorySource{{Kind: "user"}}}
}

func memoryApply(t *testing.T, core *Service, authority memoryAuthority, mutation MemoryMutation) (MemoryReceipt, error) {
	t.Helper()
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer tx.Rollback()
	receipt, err := mutateMemoryTx(t.Context(), tx, authority, mutation)
	if err != nil {
		return receipt, err
	}
	return receipt, tx.Commit()
}

func TestMemoryScopeAuthorizationAndIdentity(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	agent, err := tasks.AgentIdentity(t.Context(), chat)
	if err != nil || agent != "chat-"+chat {
		t.Fatalf("agent=%s err=%v", agent, err)
	}
	scope := MemoryScope{Kind: "private", Owner: agent, Project: project}
	receipt, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "save", "PRIVATE_DATA"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := tasks.Submit(t.Context(), chat, "request", "ordinary chat")
	if err != nil {
		t.Fatal(err)
	}
	reader := memoryAuthority{agent: agent, runID: run.Run.ID, project: project, grants: []MemoryGrant{{Scope: scope, Read: true}}}
	if entry, err := getMemory(t.Context(), core.Store.db, reader, scope, receipt.EntryID, "read"); err != nil || entry.Body != "PRIVATE_DATA" {
		t.Fatalf("authorized=%+v err=%v", entry, err)
	}
	for _, changed := range []memoryAuthority{
		{agent: agent, runID: run.Run.ID, project: project},
		{agent: "same-role-other-instance", runID: run.Run.ID, project: project, grants: reader.grants},
		{agent: agent, runID: run.Run.ID, project: filepath.Join(project, "other"), grants: reader.grants},
	} {
		if _, err := getMemory(t.Context(), core.Store.db, changed, scope, receipt.EntryID, "read"); !errors.Is(err, ErrMemoryDenied) {
			t.Fatalf("isolation error=%v", err)
		}
	}
	write := MemoryMutation{RequestKey: "no-write", Action: "delete", Scope: scope, EntryID: receipt.EntryID, ExpectedRevision: 1}
	reader.grants[0].Modify = true
	if _, err := memoryApply(t, core, reader, write); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("model changed retained private data: %v", err)
	}
	if err := tasks.SelectModel(t.Context(), chat, "default"); err != nil {
		t.Fatal(err)
	}
	if next, err := tasks.AgentIdentity(t.Context(), chat); err != nil || next != agent {
		t.Fatalf("model switched Agent identity: %s %v", next, err)
	}
	other := conversation(t, core)
	if next, _ := tasks.AgentIdentity(t.Context(), other); next == agent {
		t.Fatal("different Chat inherited private identity")
	}
	if err := tasks.SelectAgent(t.Context(), other, agent); err != nil {
		t.Fatal(err)
	}
	if next, _ := tasks.AgentIdentity(t.Context(), other); next != agent {
		t.Fatal("explicit Agent reuse was not retained")
	}
}

func TestMemoryVersionLifecycleIdempotencyAndDeletion(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	agent, _ := tasks.AgentIdentity(t.Context(), chat)
	scope := MemoryScope{Kind: "private", Owner: agent, Project: project}
	create := memoryCreate(scope, "create", "SENSITIVE_ORIGINAL")
	first, err := tasks.SaveMemory(t.Context(), chat, create)
	if err != nil {
		t.Fatal(err)
	}
	again, err := tasks.SaveMemory(t.Context(), chat, create)
	if err != nil || !again.Duplicate || first.EntryID != again.EntryID || again.Revision != 1 {
		t.Fatalf("duplicate=%+v err=%v", again, err)
	}
	create.Body = "different"
	if _, err := tasks.SaveMemory(t.Context(), chat, create); !errors.Is(err, ErrRequestKey) {
		t.Fatalf("changed idempotency key=%v", err)
	}
	update := memoryCreate(scope, "update", "SENSITIVE_REPLACEMENT")
	update.Action, update.EntryID, update.ExpectedRevision = "update", first.EntryID, 1
	second, err := tasks.SaveMemory(t.Context(), chat, update)
	if err != nil || second.Revision != 2 {
		t.Fatalf("update=%+v err=%v", second, err)
	}
	update.RequestKey = "stale"
	if _, err := tasks.SaveMemory(t.Context(), chat, update); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("stale update=%v", err)
	}
	action := MemoryMutation{RequestKey: "disable", Action: "disable", Scope: scope, EntryID: first.EntryID, ExpectedRevision: 2}
	disabled, err := tasks.SaveMemory(t.Context(), chat, action)
	if err != nil || disabled.State != "disabled" || disabled.Revision != 3 {
		t.Fatalf("disable=%+v err=%v", disabled, err)
	}
	if entry, err := tasks.ReadMemory(t.Context(), chat, scope, first.EntryID); err != nil || entry.Body != "SENSITIVE_REPLACEMENT" {
		t.Fatalf("disabled management=%+v err=%v", entry, err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "enable", "enable", 3
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "delete", "delete", 4
	deleted, err := tasks.SaveMemory(t.Context(), chat, action)
	if err != nil || deleted.State != "deleted" || deleted.Revision != 5 {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	if _, err := tasks.ReadMemory(t.Context(), chat, scope, first.EntryID); !errors.Is(err, ErrMemoryDeleted) {
		t.Fatalf("deleted reference=%v", err)
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM memory_revisions WHERE entry_id=?`, first.EntryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted revisions=%d err=%v", count, err)
	}
	var title, body, sources string
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT title,body,sources_json FROM memories WHERE id=?`, first.EntryID).Scan(&title, &body, &sources); err != nil || title != "" || body != "" || sources != "[]" {
		t.Fatalf("deleted content retained: %q %q %q %v", title, body, sources, err)
	}
	var operations string
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT group_concat(receipt_json) FROM memory_operations`).Scan(&operations); err != nil || strings.Contains(operations, "SENSITIVE_") {
		t.Fatalf("operation log retained bodies: %s %v", operations, err)
	}
	action.RequestKey, action.Action, action.ExpectedRevision = "resurrect", "enable", 5
	if _, err := tasks.SaveMemory(t.Context(), chat, action); !errors.Is(err, ErrMemoryDeleted) {
		t.Fatalf("deleted entry resurrected: %v", err)
	}
	if duplicate, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "create", "SENSITIVE_ORIGINAL")); err != nil || !duplicate.Duplicate {
		t.Fatalf("lost response retry=%+v err=%v", duplicate, err)
	}
}

func TestMemorySharedRaceSourcesAndNoPrivateExport(t *testing.T) {
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	parent := createCollaboration(t, collab, plan)
	tasks := NewTaskService(core)
	report, err := collab.Inspect(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	scope := MemoryScope{Kind: "shared", Owner: parent, Project: core.Config.Workspace}
	first, err := tasks.SaveMemory(t.Context(), report.Parent.ConversationID, memoryCreate(scope, "shared", "Hypothesis, not evidence."))
	if err != nil {
		t.Fatal(err)
	}
	reader := memoryAuthority{agent: "expert-1", runID: report.Children[0].Run.ID, parent: parent, project: scope.Project,
		grants: []MemoryGrant{{Scope: scope, Read: true, Append: true, Modify: true, Kinds: []string{"finding", "hypothesis"}}}}
	if entry, err := getMemory(t.Context(), core.Store.db, reader, scope, first.EntryID, "read"); err != nil || entry.Revision != 1 {
		t.Fatalf("shared read=%+v err=%v", entry, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			mutation := MemoryMutation{RequestKey: []string{"race-a", "race-b"}[index], Action: "update", Scope: scope,
				EntryID: first.EntryID, ExpectedRevision: 1, Kind: "hypothesis", Title: "Competing view", Body: "Unverified interpretation",
				Sources: []MemorySource{{Kind: "request", RunID: reader.runID, Hash: contentHash([]byte(report.Children[0].Run.Prompt))}}}
			_, err := memoryApply(t, core, reader, mutation)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrMemoryConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("silent overwrite: success=%d conflict=%d", success, conflict)
	}
	private := MemoryScope{Kind: "private", Owner: reader.agent, Project: scope.Project}
	secret, err := tasks.SaveMemory(t.Context(), report.Parent.ConversationID, memoryCreate(private, "secret", "NOT_SHARED"))
	if err != nil {
		t.Fatal(err)
	}
	reader.grants = append(reader.grants, MemoryGrant{Scope: private, Read: true})
	export := MemoryMutation{RequestKey: "implicit-export", Action: "create", Scope: scope, Kind: "finding", Title: "Export", Body: "NOT_SHARED",
		Sources: []MemorySource{{Kind: "memory", EntryID: secret.EntryID, Revision: 1, Hash: contentHash([]byte("NOT_SHARED"))}}}
	if _, err := memoryApply(t, core, reader, export); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("model exported private source: %v", err)
	}
	badScope := scope
	badScope.Owner = "nonexistent-parent"
	if _, err := tasks.SaveMemory(t.Context(), report.Parent.ConversationID, memoryCreate(badScope, "invalid", "not allowed")); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("invalid shared owner=%v", err)
	}
}

func TestMemorySourceValidationKnowledgeAndBackup(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	scope := MemoryScope{Kind: "knowledge", Owner: "notes", Project: project}
	if _, err := tasks.SaveMemory(t.Context(), chat, memoryCreate(scope, "disabled", "not indexed")); err == nil {
		t.Fatal("disabled knowledge accepted import")
	}
	core.Config.Memory.KnowledgeEnabled = true
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "notes.md")
	text := "# Selected document\n中文全文检索 and source material\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	mutation := memoryCreate(scope, "file", text)
	mutation.Sources = []MemorySource{{Kind: "file", Path: file, Hash: contentHash([]byte(text))}}
	first, err := tasks.SaveMemory(t.Context(), chat, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyStore(t.Context(), filepath.Join(core.Store.DataDir(), "state.sqlite")); err != nil {
		t.Fatalf("FTS modified schema identity: %v", err)
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH ?`, `"全文检索"`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("FTS=%d err=%v", count, err)
	}
	for _, source := range []MemorySource{
		{Kind: "user", Hash: strings.Repeat("0", 64)},
		{Kind: "file", Path: file, Hash: strings.Repeat("0", 64)},
		{Kind: "file", Path: filepath.Join(project, "missing.md"), Hash: contentHash([]byte(text))},
		{Kind: "run", RunID: "missing", Hash: contentHash([]byte(text))},
		{Kind: "attempt", RunID: "missing", AttemptID: "missing", Hash: contentHash([]byte(text))},
		{Kind: "memory", EntryID: first.EntryID, Revision: 2, Hash: contentHash([]byte(text))},
	} {
		bad := memoryCreate(scope, "bad-"+source.Kind+source.Hash+source.Path, text)
		bad.RequestKey = contentHash([]byte(bad.RequestKey))
		bad.Sources = []MemorySource{source}
		if _, err := tasks.SaveMemory(t.Context(), chat, bad); err == nil {
			t.Fatalf("forged source accepted: %+v", source)
		}
	}
	backup := filepath.Join(t.TempDir(), "before-delete")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	action := MemoryMutation{RequestKey: "delete", Action: "delete", Scope: scope, EntryID: first.EntryID, ExpectedRevision: 1}
	if _, err := tasks.SaveMemory(t.Context(), chat, action); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM knowledge_fts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted FTS=%d err=%v", count, err)
	}
	data := core.Store.DataDir()
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	core.Store = reopened
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := tasks.ReadMemory(t.Context(), chat, scope, first.EntryID); !errors.Is(err, ErrMemoryDeleted) {
		t.Fatalf("restart resurrected deletion: %v", err)
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
	if entry, err := restoreTasks.ReadMemory(t.Context(), chat, scope, first.EntryID); err != nil || entry.Body != text {
		t.Fatalf("backup did not retain its actual old content: %+v %v", entry, err)
	}
	if receipt, err := restoreTasks.SaveMemory(t.Context(), chat, mutation); err != nil || !receipt.Duplicate || receipt.EntryID != first.EntryID {
		t.Fatalf("restored idempotency=%+v err=%v", receipt, err)
	}
}

func TestMemoryTaskOnlyScopeKindsAndFiniteConfig(t *testing.T) {
	core, _, project := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	run, err := tasks.Submit(t.Context(), chat, "start", "task")
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := tasks.AgentIdentity(t.Context(), chat)
	scope := MemoryScope{Kind: "private", Owner: agent, Project: project, TaskID: run.Run.ID}
	authority := memoryAuthority{agent: agent, runID: run.Run.ID, project: project,
		grants: []MemoryGrant{{Scope: scope, Read: true, Append: true, Modify: true, Kinds: []string{"hypothesis"}}}}
	mutation := memoryCreate(scope, "working", "Tentative working context")
	mutation.Kind = "hypothesis"
	mutation.Sources = []MemorySource{{Kind: "request", RunID: run.Run.ID, Hash: contentHash([]byte(run.Run.Prompt))}}
	receipt, err := memoryApply(t, core, authority, mutation)
	if err != nil {
		t.Fatal(err)
	}
	authority.runID = "different-task"
	if _, err := getMemory(t.Context(), core.Store.db, authority, scope, receipt.EntryID, "read"); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("task-local private scope leaked: %v", err)
	}
	authority.runID = run.Run.ID
	mutation.RequestKey, mutation.Kind = "wrong-kind", "fact"
	if _, err := memoryApply(t, core, authority, mutation); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("kind grant expanded: %v", err)
	}
	for _, invalid := range []MemoryConfig{{}, {MaxEntries: 65, MaxContextBytes: 16384, MaxDocumentBytes: 65536},
		{MaxEntries: 16, MaxContextBytes: 65537, MaxDocumentBytes: 65536}} {
		core.Config.Memory = invalid
		if err := core.Config.Validate(); err == nil {
			t.Fatalf("unbounded memory config accepted: %+v", invalid)
		}
	}
}
