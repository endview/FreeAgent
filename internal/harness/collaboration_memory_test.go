package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCollaborationMemoryToolsAuthorBudgetAndRestart(t *testing.T) {
	var id string
	var scope MemoryScope
	writerCalls, readerCalls := 0, 0
	collab, core, plan := collaborationFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		raw, _ := json.Marshal(request.Messages)
		if strings.Contains(string(raw), "CHILD_ONE") {
			writerCalls++
			if writerCalls == 1 {
				scope = requestMemoryScope(t, request, "shared")
				return ModelResponse{ToolCalls: []ToolCall{makeCall("append", "memory_write", map[string]any{
					"action": "create", "scope": scope, "kind": "hypothesis", "title": "Tentative", "body": "unverified proposal",
				})}}, nil
			}
			var receipt MemoryReceipt
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Fatal(err)
			}
			id = receipt.EntryID
			return ModelResponse{Content: "Writer's tentative result"}, nil
		}
		readerCalls++
		switch readerCalls {
		case 1:
			got := requestMemoryScope(t, request, "shared")
			if got != scope {
				t.Fatal("different task shared scope")
			}
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "memory_read", map[string]any{"scope": scope, "entry_id": id})}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ToolCall{makeCall("edit", "memory_write", map[string]any{
				"action": "update", "scope": scope, "entry_id": id, "expected_revision": 1,
				"kind": "hypothesis", "title": "Disagreement retained", "body": "A different unverified view",
			})}}, nil
		}
		return ModelResponse{Content: "Reader's interpretation, not evidence"}, nil
	}))
	role := plan.Roles["reviewer"]
	role.Tools, role.Paths, role.RequiredEvidence = nil, nil, nil
	role.Acceptance, role.OutputFormat = "analysis", "text"
	role.Memory.Shared = SharedMemoryAccess{Read: true, Append: true, Modify: true}
	plan.Roles["reviewer"] = role
	plan.Children[1].DependsOn = []string{"one"}
	parent := createCollaboration(t, collab, plan)
	report, err := collab.Continue(t.Context(), parent)
	if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 5 || report.Usage.ToolCalls != 3 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	tasks := NewTaskService(core)
	entry, err := tasks.ReadMemory(t.Context(), report.Parent.ConversationID, scope, id)
	readerID := collaborationAgentID(parent, "two")
	if err != nil || entry.Revision != 2 || entry.Author != readerID || entry.Kind != "hypothesis" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	first, _ := tasks.AgentIdentity(t.Context(), report.Children[0].Run.ConversationID)
	second, _ := tasks.AgentIdentity(t.Context(), report.Children[1].Run.ConversationID)
	if first == second || second != readerID {
		t.Fatal("same-role temporary experts shared their private identity")
	}
	if len(report.Children[1].Evidence.ChecksPassed) != 0 {
		t.Fatal("shared hypothesis became a passed check")
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
	after, err := collab.Continue(t.Context(), parent)
	if err != nil || after.Usage != report.Usage || writerCalls != 2 || readerCalls != 3 {
		t.Fatalf("restarted execution repeated: %+v %v", after.Usage, err)
	}
}

func TestCollaborationMemoryLocalRollbackAndDefinitiveConflict(t *testing.T) {
	collab, core, plan := collaborationFixture(t, EchoModel{})
	role := plan.Roles["reviewer"]
	role.Memory.Shared = SharedMemoryAccess{Read: true, Append: true, Modify: true, Kinds: []string{"hypothesis"}}
	plan.Roles["reviewer"] = role
	parent := createCollaboration(t, collab, plan)
	report, err := collab.Inspect(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	run := report.Children[0].Run
	if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE runs SET status='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	scope := MemoryScope{Kind: "shared", Owner: parent, Project: core.Config.Workspace}
	call := makeCall("local", "memory_write", map[string]any{
		"action": "create", "scope": scope, "kind": "hypothesis", "title": "Atomic", "body": "rollback this",
	})
	raw, _ := json.Marshal(call)
	attempt := Attempt{ID: newID(), RunID: run.ID, Kind: "tool", CallID: call.ID, RequestJSON: string(raw), Status: "dispatched"}
	if _, err := core.Store.db.ExecContext(t.Context(), `INSERT INTO attempts VALUES(?,?,'tool',?,'dispatched',?,'')`,
		attempt.ID, run.ID, call.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	authority := memoryAuthority{agent: collaborationAgentID(parent, "one"), runID: run.ID, parent: parent, project: scope.Project,
		grants: []MemoryGrant{{Scope: scope, Read: true, Append: true, Modify: true}}}
	execute := func(ctx context.Context, tx *sql.Tx) (ToolResult, error) {
		mutation := memoryCreate(scope, attempt.ID, "rollback this")
		mutation.Sources = []MemorySource{{Kind: "request", RunID: run.ID, Hash: contentHash([]byte(run.Prompt))}}
		if _, err := mutateMemoryTx(ctx, tx, authority, mutation); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{}, ErrMemoryConflict
	}
	result, err := core.Store.executeLocalTool(t.Context(), run, attempt, call, execute, nil, 65536)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "revision changed") {
		t.Fatalf("definitive conflict=%+v %v", result, err)
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed local transaction leaked its mutation")
	}
	attempts, err := core.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || attempts[0].Status != "finished" {
		t.Fatalf("result status=%+v %v", attempts, err)
	}
	if _, err := core.Store.executeLocalTool(t.Context(), run, attempt, call, execute, nil, 65536); err != nil {
		t.Fatalf("finished failure not replayable as saved result: %v", err)
	}
	wrong := call
	wrong.Name = "other"
	if _, err := core.Store.executeLocalTool(t.Context(), run, attempt, wrong, execute, nil, 65536); !errors.Is(err, ErrRequestKey) {
		t.Fatalf("mismatched call=%v", err)
	}
}
