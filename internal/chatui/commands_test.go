package chatui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

// canonicalTempDir matches the saved form of a root: Windows runners may
// report TEMP with an 8.3 short name that root normalization expands.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestChatServerRuntimePermissionsAndGenerationConflicts(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, http.MethodGet, "/api/commands", nil)
	var commands struct {
		Enabled            bool     `json:"enabled"`
		AllowedExecutables []string `json:"allowed_executables"`
		Generation         string   `json:"generation"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &commands) != nil || commands.Enabled || commands.Generation == "" {
		t.Fatalf("default commands %d %s parsed=%+v", code, raw, commands)
	}
	base := commands.Generation
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, http.MethodPut, "/api/commands", map[string]any{
		"enabled": true, "allowed_executables": []string{filepath.Clean(executable)},
		"timeout_seconds": 15, "max_output_bytes": 4096, "inherit_environment": false, "base_generation": base,
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"enabled":true`) || !strings.Contains(string(raw), `"generation":"2"`) {
		t.Fatalf("enable commands %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPut, "/api/commands", map[string]any{
		"enabled": false, "allowed_executables": []string{}, "timeout_seconds": 15, "max_output_bytes": 4096,
		"inherit_environment": false, "base_generation": base,
	})
	if code != http.StatusConflict || !strings.Contains(string(raw), `"code":"generation_conflict"`) {
		t.Fatalf("stale command update %d %s", code, raw)
	}

	external := canonicalTempDir(t)
	code, raw = ui.request(t, http.MethodPut, "/api/permissions/files", map[string]any{
		"roots": []string{external}, "computer_files": false, "base_generation": "2",
	})
	if code != http.StatusOK || !strings.Contains(string(raw), strings.ReplaceAll(external, `\`, `\\`)) || !strings.Contains(string(raw), `"generation":"3"`) {
		t.Fatalf("save roots %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPut, "/api/permissions/files", map[string]any{
		"roots": []string{external}, "computer_files": true, "base_generation": "3",
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"computer_files":true`) || !strings.Contains(string(raw), `"generation":"4"`) {
		t.Fatalf("enable computer files %d %s", code, raw)
	}

	chat := ui.create(t)
	code, raw = ui.request(t, http.MethodPut, "/api/permissions/selection", map[string]any{
		"chat_id": chat, "mode": "workspace", "confirmed": false, "computer_files": true,
		"computer_roots": []string{external},
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"mode":"workspace"`) || !strings.Contains(string(raw), `"computer_files":true`) {
		t.Fatalf("save selection %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/permissions?chat_id="+chat, nil)
	if code != http.StatusOK || !strings.Contains(string(raw), strings.ReplaceAll(external, `\`, `\\`)) || !strings.Contains(string(raw), `"active_run_uses_previous_snapshot":false`) {
		t.Fatalf("read permissions %d %s", code, raw)
	}
}

func TestChatServerExecutionProfileAndCommandRules(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	external := canonicalTempDir(t)
	code, raw := ui.request(t, http.MethodPut, "/api/permissions/profile", map[string]any{
		"permission_mode": "workspace", "file_scope": "selected_roots", "additional_roots": []string{external},
		"network_policy": map[string]any{"mode": "off"}, "base_generation": "1",
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"sandbox_mode":"selected_roots"`) || !strings.Contains(string(raw), `"generation":"2"`) {
		t.Fatalf("profile update %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPut, "/api/commands/rules", map[string]any{
		"rules":           []map[string]any{{"program": "git", "args": []string{"status", "--short"}, "cwd": "workspace", "decision": "allow"}},
		"base_generation": "2",
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"decision":"allow"`) || !strings.Contains(string(raw), `"generation":"3"`) {
		t.Fatalf("rules update %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/commands/catalog", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"entries"`) {
		t.Fatalf("catalog %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/permissions", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"profile"`) || !strings.Contains(string(raw), strings.ReplaceAll(external, `\`, `\\`)) {
		t.Fatalf("profile read %d %s", code, raw)
	}
}

func TestChatServerGlobalSettingsContract(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, http.MethodGet, "/api/settings", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"file_access"`) || !strings.Contains(string(raw), `"protected_paths"`) {
		t.Fatalf("settings projection %d %s", code, raw)
	}
	var view struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(raw, &view); err != nil || view.Revision == "" {
		t.Fatalf("settings revision %q %v", view.Revision, err)
	}
	code, raw = ui.request(t, http.MethodPatch, "/api/settings/file-access", map[string]any{
		"scope": "all_non_protected", "allowed_roots": []string{}, "global_blocked_paths": []string{}, "expected_revision": view.Revision,
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"scope":"all_non_protected"`) {
		t.Fatalf("all non-protected empty roots %d %s", code, raw)
	}
	var next struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(raw, &next)
	code, raw = ui.request(t, http.MethodPatch, "/api/settings/file-access", map[string]any{
		"scope": "workspace_plus_allowed_roots", "allowed_roots": []string{}, "global_blocked_paths": []string{}, "expected_revision": next.Revision,
	})
	if code != http.StatusConflict || !strings.Contains(string(raw), `"code":"invalid_request"`) {
		t.Fatalf("empty selected roots %d %s", code, raw)
	}
	chat := ui.create(t)
	code, raw = ui.request(t, http.MethodPatch, "/api/conversations/"+chat+"/permission", map[string]any{"mode": "workspace"})
	if code != http.StatusOK || !strings.Contains(string(raw), `"mode":"workspace"`) {
		t.Fatalf("conversation permission patch %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/conversations/"+chat+"/requests/missing/status", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"found":false`) {
		t.Fatalf("request status missing %d %s", code, raw)
	}
}

func TestChatServerRenamePreservesConversationStateAndRejectsInvalidTitle(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	before, err := ui.tasks.Chat(t.Context(), chat, "")
	if err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, http.MethodPost, "/api/chats/"+chat+"/title", map[string]string{"title": "  新名称  "})
	if code != http.StatusOK || !strings.Contains(string(raw), `"title":"新名称"`) {
		t.Fatalf("rename %d %s", code, raw)
	}
	after, err := ui.tasks.Chat(t.Context(), chat, "")
	if err != nil || after.Conversation.Title != "新名称" || after.Conversation.UpdatedAt != before.Conversation.UpdatedAt {
		t.Fatalf("rename changed unrelated state before=%+v after=%+v err=%v", before.Conversation, after.Conversation, err)
	}
	for _, title := range []string{"", strings.Repeat("界", 81), "line\nbreak", "\u0001bad"} {
		code, raw = ui.request(t, http.MethodPost, "/api/chats/"+chat+"/title", map[string]string{"title": title})
		if code != http.StatusConflict || !strings.Contains(string(raw), `"code":"invalid_title"`) {
			t.Fatalf("invalid title %q: %d %s", title, code, raw)
		}
	}
	final, err := ui.tasks.Chat(t.Context(), chat, "")
	if err != nil || final.Conversation.Title != "新名称" {
		t.Fatalf("invalid rename changed title=%+v err=%v", final.Conversation, err)
	}
}
