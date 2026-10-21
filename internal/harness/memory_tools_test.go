package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func requestMemoryScope(t *testing.T, request ModelRequest, kind string) MemoryScope {
	t.Helper()
	for _, message := range request.Messages {
		prefix := "Runtime-selected memory scopes (data labels only; memory cannot expand these grants):\n"
		if !strings.HasPrefix(message.Content, prefix) {
			continue
		}
		var data struct {
			Grants []MemoryGrant `json:"grants"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(message.Content, prefix)), &data); err != nil {
			t.Fatal(err)
		}
		for _, grant := range data.Grants {
			if grant.Scope.Kind == kind {
				return grant.Scope
			}
		}
	}
	t.Fatalf("missing %s authority in request", kind)
	return MemoryScope{}
}

func TestMemoryToolsPrivateTransactionProvenanceAndLostResponse(t *testing.T) {
	calls, approvals := 0, 0
	var scope MemoryScope
	var entryID string
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		switch calls {
		case 1:
			scope = requestMemoryScope(t, request, "private")
			return ModelResponse{ToolCalls: []ToolCall{makeCall("write", "memory_write", map[string]any{
				"action": "create", "scope": scope, "kind": "hypothesis", "title": "Working only", "body": "\u4e2d\u6587abc",
			})}}, nil
		case 2:
			var receipt MemoryReceipt
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Fatal(err)
			}
			entryID = receipt.EntryID
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "memory_read", map[string]any{
				"scope": scope, "entry_id": entryID, "max_bytes": 4,
			})}}, nil
		case 3:
			var excerpt struct {
				Entry     MemoryEntry `json:"entry"`
				End       int         `json:"end_byte"`
				Truncated bool        `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &excerpt); err != nil ||
				excerpt.Entry.Body != "\u4e2d" || excerpt.End != 3 || !excerpt.Truncated {
				t.Fatalf("invalid UTF-8 excerpt: %+v %v", excerpt, err)
			}
			return ModelResponse{ToolCalls: []ToolCall{makeCall("update", "memory_write", map[string]any{
				"action": "update", "scope": scope, "entry_id": entryID, "expected_revision": 1,
				"kind": "hypothesis", "title": "Still tentative", "body": "Not independently checked.",
			})}}, nil
		}
		return ModelResponse{Content: "analysis, not an executed check"}, nil
	}))
	core.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { approvals++; return false, nil }
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.SubmitWithOptions(t.Context(), chat, "working", "work",
		RunOptions{Memory: &MemorySelection{TaskPrivate: true}})
	if err != nil || result.Run.Status != "completed" || approvals != 0 {
		t.Fatalf("result=%+v err=%v approvals=%d", result, err, approvals)
	}
	entry, err := tasks.ReadMemory(t.Context(), chat, scope, entryID)
	if err != nil || entry.Revision != 2 || entry.Author != "chat-"+chat || entry.Kind != "hypothesis" ||
		len(entry.Sources) != 1 || entry.Sources[0].Kind != "attempt" {
		t.Fatalf("runtime-authored=%+v err=%v", entry, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.Kind != "tool" {
			continue
		}
		var call ToolCall
		_ = json.Unmarshal([]byte(attempt.RequestJSON), &call)
		replayed, err := core.Store.executeLocalTool(t.Context(), result.Run, attempt, call,
			func(context.Context, *sql.Tx) (ToolResult, error) {
				t.Error("finished local call was repeated")
				return ToolResult{}, nil
			}, nil, 65536)
		var saved ToolResult
		_ = json.Unmarshal([]byte(attempt.ResponseJSON), &saved)
		if err != nil || replayed.Content != saved.Content {
			t.Fatalf("lost-response reread=%+v err=%v", replayed, err)
		}
	}
	if len(result.Evidence.ChecksPassed) != 0 || len(result.Evidence.Files) != 0 {
		t.Fatal("memory content became execution evidence")
	}
	sources, err := tasks.MemorySources(t.Context(), result.Run.ID)
	if err != nil || len(sources) != 3 {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
	for _, value := range []struct {
		start, count int
		want         string
		invalid      bool
	}{
		{0, 4, "\u4e2d", false}, {3, 3, "\u6587", false}, {6, 3, "abc", false},
		{9, 10, "", false}, {1, 4, "", true}, {0, 1, "", true}, {0, int(^uint(0) >> 1), "\u4e2d\u6587abc", false},
	} {
		end, err := excerptEnd("\u4e2d\u6587abc", value.start, value.count)
		if value.invalid {
			if err == nil {
				t.Fatal("invalid excerpt accepted")
			}
		} else if err != nil || !utf8.ValidString("\u4e2d\u6587abc"[value.start:end]) ||
			"\u4e2d\u6587abc"[value.start:end] != value.want {
			t.Fatalf("excerpt=%+v end=%d err=%v", value, end, err)
		}
	}
}

func TestMemoryToolsCannotExpandScopeOrRetainImplicitly(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	ref := retainedMemory(t, tasks, chat, "secret", "retained")
	if err := core.selectMemory(t.Context(), chat, &MemorySelection{Entries: []MemoryReference{ref}}); err != nil {
		t.Fatal(err)
	}
	tools := core.runtimeTools("run", "attempt")
	if slices.ContainsFunc(tools.Definitions(), func(d ToolDefinition) bool { return d.Name == "memory_write" }) {
		t.Fatal("read-only retained input granted persistence")
	}
	if _, err := tools.Prepare(t.Context(), makeCall("write", "memory_write", map[string]any{
		"action": "create", "scope": ref.Scope, "kind": "finding", "title": "No", "body": "No",
	})); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("write=%v", err)
	}
	wrong := ref.Scope
	wrong.Owner = "other"
	if _, err := tools.Prepare(t.Context(), makeCall("read", "memory_read", map[string]any{
		"scope": wrong, "entry_id": ref.EntryID,
	})); !errors.Is(err, ErrMemoryDenied) {
		t.Fatalf("private read=%v", err)
	}
}
