package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIMemoryEmptyCollections(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json",
		"--request", "empty-memory", "ordinary chat without retained memory")
	if err != nil {
		t.Fatal(err)
	}
	var completed harness.RunResult
	if err := json.Unmarshal([]byte(raw), &completed); err != nil || completed.Run.Status != "completed" {
		t.Fatalf("run=%s err=%v", raw, err)
	}
	for _, args := range [][]string{
		{"memory", "list", "--data-dir", data, "--session", completed.Run.ConversationID},
		{"memory", "sources", "--data-dir", data, "--run", completed.Run.ID},
	} {
		raw, _, err := invoke(t, "", args...)
		if err != nil || strings.TrimSpace(raw) != "[]" {
			t.Fatalf("%s returned %q instead of an empty array: %v", args[1], raw, err)
		}
	}
}

func TestCLIMemoryConfigurationAndPrivateLifecycle(t *testing.T) {
	data, _ := initCLI(t)

	raw, _, err := invoke(t, "", "config", "memory", "--data-dir", data,
		"--max-entries", "8", "--context-bytes", "4096", "--document-bytes", "8192")
	if err != nil || !strings.Contains(raw, `"changed": true`) {
		t.Fatalf("memory config=%s err=%v", raw, err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || config.Memory.KnowledgeEnabled || config.Memory.MaxEntries != 8 ||
		config.Memory.MaxContextBytes != 4096 || config.Memory.MaxDocumentBytes != 8192 {
		t.Fatalf("memory config=%+v err=%v", config.Memory, err)
	}

	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--json", "--request", "private-run", "completed result")
	if err != nil {
		t.Fatal(err)
	}
	var completed harness.RunResult
	if err := json.Unmarshal([]byte(raw), &completed); err != nil || completed.Run.Status != "completed" {
		t.Fatalf("run=%+v err=%v", completed, err)
	}
	agent := "chat-" + completed.Run.ConversationID
	scopeArgs := []string{"--data-dir", data, "--session", completed.Run.ConversationID,
		"--scope", "private", "--owner", agent}

	raw, _, err = invoke(t, "", append([]string{"memory", "save"}, append(scopeArgs,
		"--kind", "preference", "--title", "Working preference", "--text", "Use concise verification reports.",
		"--request", "private-save", "--yes")...)...)
	if err != nil {
		t.Fatalf("save=%s err=%v", raw, err)
	}
	var saved harness.MemoryReceipt
	if err := json.Unmarshal([]byte(raw), &saved); err != nil || saved.EntryID == "" || saved.Revision != 1 {
		t.Fatalf("receipt=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", append([]string{"memory", "list"}, scopeArgs...)...)
	if err != nil {
		t.Fatal(err)
	}
	var listed []harness.MemoryEntry
	if err := json.Unmarshal([]byte(raw), &listed); err != nil || len(listed) != 1 ||
		listed[0].ID != saved.EntryID || listed[0].Body != "" {
		t.Fatalf("list=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", append([]string{"memory", "read"}, append(scopeArgs,
		"--entry", saved.EntryID)...)...)
	if err != nil {
		t.Fatal(err)
	}
	var entry harness.MemoryEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil || entry.Body != "Use concise verification reports." {
		t.Fatalf("read=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", "memory", "select", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--entry", saved.EntryID)
	if err != nil {
		t.Fatalf("select=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", completed.Run.ConversationID,
		"--json", "--request", "selected-memory", "use selected memory")
	if err != nil {
		t.Fatal(err)
	}
	var selected harness.RunResult
	if err := json.Unmarshal([]byte(raw), &selected); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "memory", "sources", "--data-dir", data, "--run", selected.Run.ID)
	if err != nil {
		t.Fatalf("memory sources=%s err=%v", raw, err)
	}
	var sources []harness.MemorySourceStatus
	if err := json.Unmarshal([]byte(raw), &sources); err != nil || len(sources) != 1 ||
		sources[0].Use.EntryID != saved.EntryID || sources[0].Use.Revision != 1 ||
		sources[0].Current != "active" {
		t.Fatalf("selected source not recorded: %s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", append([]string{"memory", "update"}, append(scopeArgs,
		"--entry", saved.EntryID, "--revision", "1", "--text", "Updated preference.",
		"--request", "private-update", "--yes")...)...)
	if err != nil {
		t.Fatalf("update=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", append([]string{"memory", "disable"}, append(scopeArgs,
		"--entry", saved.EntryID, "--revision", "2", "--request", "private-disable", "--yes")...)...)
	if err != nil {
		t.Fatalf("disable=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", append([]string{"memory", "read"}, append(scopeArgs,
		"--entry", saved.EntryID)...)...)
	if err != nil || !strings.Contains(raw, `"state": "disabled"`) ||
		!strings.Contains(raw, "Updated preference.") {
		t.Fatalf("disabled entry=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", append([]string{"memory", "enable"}, append(scopeArgs,
		"--entry", saved.EntryID, "--revision", "3", "--request", "private-enable", "--yes")...)...)
	if err != nil {
		t.Fatalf("enable=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", append([]string{"memory", "delete"}, append(scopeArgs,
		"--entry", saved.EntryID, "--revision", "4", "--request", "private-delete", "--yes")...)...)
	if err != nil {
		t.Fatalf("delete=%s err=%v", raw, err)
	}
	if _, _, err := invoke(t, "", append([]string{"memory", "read"}, append(scopeArgs,
		"--entry", saved.EntryID)...)...); err == nil {
		t.Fatal("deleted memory remained readable")
	}
}

func TestCLIMemoryKnowledgeImportSearchAndSaveResult(t *testing.T) {
	data, workspace := initCLI(t)
	document := filepath.Join(workspace, "selected.md")
	if err := os.WriteFile(document, []byte("# Notes\n中文全文检索 source marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "memory", "--data-dir", data, "--knowledge"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "--request", "knowledge-run", "make a result")
	if err != nil {
		t.Fatal(err)
	}
	var completed harness.RunResult
	if err := json.Unmarshal([]byte(raw), &completed); err != nil {
		t.Fatal(err)
	}
	agent := "chat-" + completed.Run.ConversationID
	raw, _, err = invoke(t, "", "memory", "save-result", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--run", completed.Run.ID,
		"--scope", "private", "--owner", agent, "--title", "Saved result",
		"--kind", "finding", "--request", "save-result", "--yes")
	if err != nil {
		t.Fatalf("save-result=%s err=%v", raw, err)
	}
	var resultReceipt harness.MemoryReceipt
	if err := json.Unmarshal([]byte(raw), &resultReceipt); err != nil || resultReceipt.EntryID == "" {
		t.Fatalf("result receipt=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", "memory", "import", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--scope", "knowledge", "--owner", "project-notes",
		"--file", document, "--request", "import-notes", "--yes")
	if err != nil {
		t.Fatalf("import=%s err=%v", raw, err)
	}
	var importReceipt harness.MemoryReceipt
	if err := json.Unmarshal([]byte(raw), &importReceipt); err != nil || importReceipt.EntryID == "" {
		t.Fatalf("import receipt=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "memory", "search", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--scope", "knowledge", "--owner", "project-notes",
		"--query", "全文检索", "--limit", "4", "--max-bytes", "1024")
	if err != nil || !strings.Contains(raw, "source marker") || !strings.Contains(raw, importReceipt.EntryID) {
		t.Fatalf("search=%s err=%v", raw, err)
	}

	raw, _, err = invoke(t, "", "memory", "select", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--collection", "project-notes")
	if err != nil {
		t.Fatalf("knowledge select=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "memory", "delete", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--scope", "knowledge", "--owner", "project-notes",
		"--entry", importReceipt.EntryID, "--revision", "1", "--request", "delete-notes", "--yes")
	if err != nil {
		t.Fatalf("knowledge delete=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "memory", "search", "--data-dir", data,
		"--session", completed.Run.ConversationID, "--scope", "knowledge", "--owner", "project-notes",
		"--query", "全文检索", "--limit", "4", "--max-bytes", "1024")
	if err != nil || strings.TrimSpace(raw) != "[]" {
		t.Fatalf("deleted knowledge search=%s err=%v", raw, err)
	}
}

func TestCLIMemoryAgentSwitchClearsPrivateSelection(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "--request", "first", "first")
	if err != nil {
		t.Fatal(err)
	}
	var first harness.RunResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--json", "--request", "second", "second")
	if err != nil {
		t.Fatal(err)
	}
	var second harness.RunResult
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatal(err)
	}
	agent := "chat-" + first.Run.ConversationID
	raw, _, err = invoke(t, "", "memory", "save", "--data-dir", data,
		"--session", first.Run.ConversationID, "--scope", "private", "--owner", agent,
		"--title", "Private", "--text", "PRIVATE_ONLY", "--request", "agent-private", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var receipt harness.MemoryReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "memory", "select", "--data-dir", data,
		"--session", first.Run.ConversationID, "--entry", receipt.EntryID); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "memory", "agent", "--data-dir", data,
		"--session", first.Run.ConversationID, "--reuse-session", second.Run.ConversationID)
	if err != nil {
		t.Fatalf("agent switch=%s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "memory", "select", "--data-dir", data,
		"--session", first.Run.ConversationID)
	if err != nil || strings.Contains(raw, receipt.EntryID) {
		t.Fatalf("private selection survived Agent switch: %s err=%v", raw, err)
	}
}
