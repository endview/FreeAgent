package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIAttachmentsOfflineThenAttachedGUIContinuation(t *testing.T) {
	data, _ := initCLI(t)
	source := filepath.Join(t.TempDir(), "输入 & !.md")
	if err := os.WriteFile(source, []byte("selected-file-fact"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, diagnostic, err := invoke(t, "", "run", "--data-dir", data, "--attachment", source, "--request", "input", "--json", "read attachment")
	var first harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &first) != nil || !strings.Contains(first.Answer, "selected-file-fact") {
		t.Fatalf("local %s %s %v", raw, diagnostic, err)
	}
	_, _, _ = startTestWeb(t, data)
	raw, diagnostic, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--attachment", source, "--request", "second", "--json", "read again")
	var second harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &second) != nil || !strings.Contains(second.Answer, "selected-file-fact") {
		t.Fatalf("attached %s %s %v", raw, diagnostic, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--attachment", source, "--request", "second", "--json", "read again")
	if err != nil || json.Unmarshal([]byte(raw), &second) != nil || !second.Duplicate {
		t.Fatalf("retry %s %v", raw, err)
	}
	raw, _, err = invoke(t, "", "attachments", "save-reply", "--data-dir", data, "--session", first.Run.ConversationID, "--run", second.Run.ID, "--request", "save")
	var ref harness.AttachmentRef
	if err != nil || json.Unmarshal([]byte(raw), &ref) != nil {
		t.Fatalf("reply %s %v", raw, err)
	}
	target := filepath.Join(t.TempDir(), "回复.md")
	if _, _, err := invoke(t, "", "attachments", "download", "--data-dir", data, "--session", first.Run.ConversationID, "--id", ref.ID, "--out", target); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != second.Answer {
		t.Fatalf("download %q %v", body, err)
	}
	if _, _, err := invoke(t, "", "attachments", "download", "--data-dir", data, "--session", first.Run.ConversationID, "--id", ref.ID, "--out", target); err == nil {
		t.Fatal("existing target overwritten")
	}
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	view, err := client.Chat(t.Context(), first.Run.ConversationID)
	if err != nil || len(view.Turns) != 2 || len(view.Turns[1].Messages[0].Attachments) != 1 {
		t.Fatalf("same GUI view %+v %v", view, err)
	}
}
