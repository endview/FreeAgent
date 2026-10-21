package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestChatMemoryExplicitPrivateLifecycleAndSources(t *testing.T) {
	data, workspace := initCLI(t)
	script := strings.Join([]string{
		"/settings memory", "y", "8", "4096", "8192",
		"/memory", "2", "1", "2", "Working preference", "8", "PRIVATE_SELECTED_MARKER", ".", "y",
		"/memory select", "1", "", "n",
		"ordinary task", "/memory sources",
		"/memory manage", "1", "1", "1", "3", "y",
		"next task", "/memory sources",
		"/memory manage", "1", "1", "1", "4", "y",
		"/memory manage", "1", "1", "1", "5", "y",
		"/memory clear", "/exit", "",
	}, "\n")
	output, diagnostics, err := invoke(t, script, "chat", "--data-dir", data)
	if err != nil || strings.Contains(diagnostics, "error:") ||
		!strings.Contains(diagnostics, "Memory settings saved.") ||
		!strings.Contains(diagnostics, "Retain for future tasks") ||
		!strings.Contains(diagnostics, "current: active") ||
		!strings.Contains(diagnostics, "current: disabled") ||
		!strings.Contains(diagnostics, deletionBoundary) ||
		!strings.Contains(output, "Echo: next task") {
		t.Fatalf("memory Chat=%s diagnostics=%s err=%v", output, diagnostics, err)
	}
	tasks, closeAll, err := openMemoryTasks(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	config := tasks.Config()
	if !config.Memory.KnowledgeEnabled || config.Memory.MaxEntries != 8 ||
		config.Memory.MaxContextBytes != 4096 || config.Memory.MaxDocumentBytes != 8192 {
		t.Fatalf("interactive settings not saved: %+v", config.Memory)
	}
	sessions, err := tasks.Conversations(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	chat := sessions[0].ID
	last, err := tasks.Latest(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	last, attempts, _, err := tasks.Inspect(t.Context(), last.ID)
	if err != nil || last.Status != "completed" || len(attempts) != 1 {
		t.Fatalf("latest=%+v attempts=%+v err=%v", last, attempts, err)
	}
	var request harness.ModelRequest
	if err := json.Unmarshal([]byte(attempts[0].RequestJSON), &request); err != nil ||
		len(request.Memory) != 1 || request.Memory[0].State != "disabled" {
		t.Fatalf("disabled selection=%+v err=%v", request.Memory, err)
	}
	raw, _ := json.Marshal(request.Messages)
	if bytes.Contains(raw, []byte("PRIVATE_SELECTED_MARKER")) {
		t.Fatal("disabled private content entered a new model request")
	}
	scope := harness.MemoryScope{Kind: "private", Owner: "chat-" + chat, Project: workspace}
	if _, err := tasks.ReadMemory(t.Context(), chat, scope, request.Memory[0].EntryID); !errors.Is(err, harness.ErrMemoryDeleted) {
		t.Fatalf("deleted private memory=%v", err)
	}
	selection, err := tasks.SelectedMemory(t.Context(), chat)
	if err != nil || len(selection.Entries)+len(selection.Knowledge) != 0 || selection.TaskPrivate {
		t.Fatalf("clear=%+v err=%v", selection, err)
	}
	sources, err := tasks.MemorySources(t.Context(), last.ID)
	if err != nil || len(sources) != 1 || sources[0].Current != "deleted" {
		t.Fatalf("historical reference status=%+v err=%v", sources, err)
	}
	if strings.Contains(output+diagnostics, chat) || strings.Contains(output+diagnostics, last.ID) ||
		strings.Contains(output+diagnostics, request.Memory[0].EntryID) {
		t.Fatal("ordinary memory entry exposed internal identifiers")
	}
}

func TestChatMemoryKnowledgeImportExplicitToolAndClear(t *testing.T) {
	data, workspace := initCLI(t)
	const documentBody = "# Notes\nIMPORTED_DOCUMENT_MARKER is selected source data.\n"
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte(documentBody), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := harness.MemoryScope{Kind: "knowledge", Owner: "Project notes", Project: workspace}
	scopeRaw, _ := json.Marshal(scope)
	requests := make(chan []byte, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		writer.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			arguments := `{"scope":` + string(scopeRaw) + `,"query":"IMPORTED_DOCUMENT_MARKER","limit":1,"max_bytes":4096}`
			_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "selected-search", "type": "function",
					"function": map[string]any{"name": "knowledge_search", "arguments": arguments},
				}}}, "finish_reason": "tool_calls"}}})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Ordinary answer"}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		"/settings", "3", "5", "y", "8", "8192", "8192",
		"/memory import", "1", "Project notes", "2", "notes.md", "y",
		"/memory select", "", "1", "n",
		"retrieve document", "/memory sources", "/memory clear",
		"without knowledge",
		"/memory manage", "3", "1", "1", "5", "y",
		"/exit", "",
	}, "\n")
	output, diagnostics, err := invoke(t, script, "chat", "--data-dir", data)
	if err != nil || strings.Contains(diagnostics, "error:") || calls.Load() != 3 ||
		!strings.Contains(diagnostics, "notes.md") || !strings.Contains(diagnostics, "Bytes ") ||
		!strings.Contains(diagnostics, "excerpt SHA-256:") || !strings.Contains(output, "Ordinary answer") {
		t.Fatalf("knowledge Chat=%s diagnostics=%s err=%v calls=%d", output, diagnostics, err, calls.Load())
	}
	first, second, cleared := <-requests, <-requests, <-requests
	if bytes.Contains(first, []byte("IMPORTED_DOCUMENT_MARKER")) ||
		!bytes.Contains(first, []byte("knowledge_search")) ||
		!bytes.Contains(second, []byte("IMPORTED_DOCUMENT_MARKER")) {
		t.Fatal("knowledge was injected before explicit tool retrieval, or retrieval did not reach the model")
	}
	var clearedRequest struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []harness.ModelMessage `json:"messages"`
	}
	if err := json.Unmarshal(cleared, &clearedRequest); err != nil {
		t.Fatal(err)
	}
	for _, tool := range clearedRequest.Tools {
		if tool.Function.Name == "knowledge_search" {
			t.Fatal("cleared selection still advertised knowledge access")
		}
	}
	for _, message := range clearedRequest.Messages {
		if strings.HasPrefix(message.Content, "Runtime-selected memory scopes") {
			t.Fatal("cleared selection still supplied a retrieval grant")
		}
	}
	tasks, closeAll, err := openMemoryTasks(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	sessions, err := tasks.Conversations(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	hits, err := tasks.SearchKnowledge(t.Context(), sessions[0].ID, scope, "IMPORTED_DOCUMENT_MARKER", 1, 4096)
	if err != nil || hits == nil || len(hits) != 0 {
		t.Fatalf("deleted knowledge search=%+v err=%v", hits, err)
	}
}

func TestChatMemoryDeclineAndInvalidSettingsDoNotWrite(t *testing.T) {
	data, workspace := initCLI(t)
	before, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		"/memory", "9",
		"/memory save", "4",
		"/memory save", "1", "2", "Rejected", "3", "REJECTED_TEXT", ".", "n",
		"/settings memory", "y", "0", "4096", "8192",
		"/exit", "",
	}, "\n")
	output, diagnostics, err := invoke(t, script, "chat", "--data-dir", data)
	if err != nil || !strings.Contains(diagnostics, "Not saved.") ||
		!strings.Contains(diagnostics, "memory limits must be finite") ||
		strings.Contains(diagnostics, "Memory change saved.") {
		t.Fatalf("declined Chat=%s diagnostics=%s err=%v", output, diagnostics, err)
	}
	after, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid memory settings changed saved configuration")
	}
	tasks, closeAll, err := openMemoryTasks(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	sessions, err := tasks.Conversations(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	scope := harness.MemoryScope{Kind: "private", Owner: "chat-" + sessions[0].ID, Project: workspace}
	entries, err := tasks.ListMemory(t.Context(), sessions[0].ID, scope, 16)
	if err != nil || len(entries) != 0 {
		t.Fatalf("declined save persisted memory=%+v err=%v", entries, err)
	}
	if _, err := tasks.Latest(t.Context(), sessions[0].ID); err == nil {
		t.Fatal("memory management dispatched a task or model")
	}
}
