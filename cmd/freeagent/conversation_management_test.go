package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIConversationManagementUsesExactPreviewAndJournal(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "synthetic cleanup test")
	if err != nil {
		t.Fatal(raw, err)
	}
	var run harness.RunResult
	if json.Unmarshal([]byte(raw), &run) != nil {
		t.Fatal(raw)
	}
	chat := run.Run.ConversationID
	if _, _, err = invoke(t, "", "conversations", "apply", "--data-dir", data, "--operation", "missing"); err == nil {
		t.Fatal("apply without explicit confirmation")
	}
	raw, _, err = invoke(t, "", "conversations", "preview", "--data-dir", data, "--action", "trash", "--session", chat)
	if err != nil {
		t.Fatal(raw, err)
	}
	var preview harness.ManagementPreview
	if json.Unmarshal([]byte(raw), &preview) != nil || len(preview.Items) != 1 {
		t.Fatal(raw)
	}
	raw, _, err = invoke(t, "", "conversations", "apply", "--data-dir", data, "--operation", preview.ID, "--yes")
	if err != nil {
		t.Fatal(raw, err)
	}
	var result harness.ManagementResult
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Items) != 1 || result.Items[0].Status != "completed" {
		t.Fatal(raw)
	}
	raw, _, err = invoke(t, "", "conversations", "result", "--data-dir", data, "--operation", preview.ID)
	if err != nil || !strings.Contains(raw, chat) {
		t.Fatal(raw, err)
	}
	raw, _, err = invoke(t, "", "conversations", "list", "--data-dir", data, "--view", "trash", "--limit", "1")
	if err != nil || !strings.Contains(raw, chat) {
		t.Fatal(raw, err)
	}
	raw, _, err = invoke(t, "", "control", "list", "--data-dir", data)
	if err != nil || !strings.Contains(raw, harness.ControlManageConversations) {
		t.Fatal("CLI bypassed control journal", raw, err)
	}
}
