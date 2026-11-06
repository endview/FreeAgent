package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestSessionsBoundedHistoryExplicitExportAndAttachedRead(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "first original message")
	var first harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatal("first run failed", err)
	}
	_, _, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--json", "second original message")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data, "--show", first.Run.ConversationID, "--limit", "2")
	var page harness.HistoryPage
	if err != nil || json.Unmarshal([]byte(raw), &page) != nil || len(page.Items) != 2 || !page.HasMore || !strings.Contains(page.Items[0].Content, "second") {
		t.Fatalf("bounded history: %s %v", raw, err)
	}
	out := filepath.Join(t.TempDir(), "full history.jsonl")
	_, _, err = invoke(t, "", "sessions", "--data-dir", data, "--show", first.Run.ConversationID, "--export-history", out)
	content, readErr := os.ReadFile(out)
	if err != nil || readErr != nil || len(strings.Split(strings.TrimSpace(string(content)), "\n")) != 4 || !strings.Contains(string(content), "first original") {
		t.Fatal("export lost original messages", err, readErr)
	}
	if _, _, err = invoke(t, "", "sessions", "--data-dir", data, "--show", first.Run.ConversationID, "--export-history", out); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	attached, err := (&remoteChat{Client: client}).HistoryPage(t.Context(), first.Run.ConversationID, harness.HistoryQuery{Limit: 2, Before: page.Before})
	if err != nil || len(attached.Items) != 2 || !strings.Contains(attached.Items[0].Content, "first") {
		t.Fatalf("attached page: %+v %v", attached, err)
	}
}
