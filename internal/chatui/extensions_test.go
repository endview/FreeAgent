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

func writeUIExtension(t *testing.T, root, id, name string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"protocol": "freeagent-ext/1", "id": id, "version": "1.0.0",
		"name": name, "description": "UI fixture",
		"run":     map[string]any{"command": "fixture-extension"},
		"context": map[string]any{"phases": []string{"turn"}, "purposes": []string{"chat"}, "placement": []string{"tail"}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestExtensionsAPIImportTrustConfigureAndClearData(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	source := writeUIExtension(t, filepath.Join(t.TempDir(), "source", "com.example.ui"), "com.example.ui", "UI extension")
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, raw := ui.request(t, http.MethodPost, "/api/extensions", map[string]string{"path": source})
	var imported harness.ExtensionInfo
	if code != http.StatusCreated || json.Unmarshal(raw, &imported) != nil ||
		imported.ID != "com.example.ui" || imported.Enabled || !imported.Available ||
		strings.Contains(string(raw), source) {
		t.Fatalf("import %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(ui.data, "extensions", imported.ID, "README.txt")); err != nil {
		t.Fatal(err)
	}

	code, raw = ui.request(t, http.MethodPost, "/api/extensions/"+imported.ID+"/update", map[string]any{
		"enabled": true, "settings": map[string]any{"mode": "compact"},
		"env_from": map[string]string{"TOKEN": "ENV_TOKEN"},
	})
	if code != http.StatusOK || !strings.Contains(string(raw), `"enabled":true`) ||
		!strings.Contains(string(raw), `"mode":"compact"`) || !strings.Contains(string(raw), `"TOKEN":"ENV_TOKEN"`) {
		t.Fatalf("configure %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/"+imported.ID+"/update", map[string]any{"required": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"required":true`) {
		t.Fatalf("require %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/"+imported.ID+"/update", map[string]any{"enabled": false})
	if code != http.StatusConflict || !strings.Contains(string(raw), "required") {
		t.Fatalf("required disable %d %s", code, raw)
	}

	if err := os.MkdirAll(filepath.Join(ui.data, "extensions", imported.ID, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui.data, "extensions", imported.ID, "data", "state"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/"+imported.ID+"/clear-data", map[string]any{})
	if code != http.StatusOK || !strings.Contains(string(raw), `"cleared":true`) {
		t.Fatalf("clear data %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/extensions/"+imported.ID+"/logs", nil)
	if code != http.StatusOK || len(raw) != 0 {
		t.Fatalf("logs %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/"+imported.ID+"/restart", map[string]any{})
	if code != http.StatusOK || !strings.Contains(string(raw), `"restarted":true`) {
		t.Fatalf("restart %d %s", code, raw)
	}
}

func TestExtensionsAPIShowsWorkspaceExtensionBeforeTrust(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	root := writeUIExtension(t, filepath.Join(ui.work, ".freeagent", "extensions", "com.example.workspace"), "com.example.workspace", "Workspace extension")
	code, raw := ui.request(t, http.MethodGet, "/api/extensions", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"id":"com.example.workspace"`) ||
		!strings.Contains(string(raw), `"available":false`) {
		t.Fatalf("untrusted list %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/com.example.workspace/update", map[string]any{"enabled": true})
	if code != http.StatusConflict || !strings.Contains(string(raw), "信任") {
		t.Fatalf("untrusted update %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/extensions/com.example.workspace/view", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"workspace_trusted":false`) {
		t.Fatalf("untrusted view %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/trust", map[string]bool{"trusted": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"trusted":true`) {
		t.Fatalf("trust %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/extensions/com.example.workspace/update", map[string]any{"enabled": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"source":"workspace"`) {
		t.Fatalf("trusted update %d %s", code, raw)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionsAPIRejectsInvalidImport(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, http.MethodPost, "/api/extensions", map[string]string{"path": filepath.Join(t.TempDir(), "missing")})
	if code != http.StatusConflict || !strings.Contains(string(raw), "扩展") {
		t.Fatalf("invalid import %d %s", code, raw)
	}
}
