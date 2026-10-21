package chatui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUISkill(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "---\nname: " + name + "\ndescription: " + name + " helper\ntrigger: auto\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSkillsAPIImportEnablePreviewAndDelete(t *testing.T) {
	ui := setupUI(t, nil)
	source := writeUISkill(t, filepath.Join(t.TempDir(), "source"), "report", "Write the report.\n")
	code, raw := ui.request(t, http.MethodPost, "/api/skills", map[string]string{"path": source})
	var imported skillView
	if code != http.StatusCreated || json.Unmarshal(raw, &imported) != nil ||
		imported.Name != "report" || imported.Enabled || imported.Body != "Write the report.\n" ||
		!imported.Available || !imported.Trusted {
		t.Fatalf("import %d %s", code, raw)
	}
	if strings.Contains(string(raw), source) {
		t.Fatal("skill import response leaked source path")
	}

	code, raw = ui.request(t, http.MethodPost, "/api/skills/report/update", map[string]bool{"enabled": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"enabled":true`) {
		t.Fatalf("enable %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/environment", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"id":"report"`) {
		t.Fatalf("enabled skill missing from environment: %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/skills/report/view", nil)
	var detail skillView
	if code != http.StatusOK || json.Unmarshal(raw, &detail) != nil ||
		detail.Name != "report" || detail.Body != "Write the report.\n" {
		t.Fatalf("skill detail %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/skills/report/delete", map[string]any{})
	if code != http.StatusOK || !strings.Contains(string(raw), `"deleted":true`) {
		t.Fatalf("delete %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(ui.data, "skills", "report")); !os.IsNotExist(err) {
		t.Fatal("deleted Skill directory remains")
	}
}

func TestSkillsAPIRequiresExplicitWorkspaceTrust(t *testing.T) {
	ui := setupUI(t, nil)
	writeUISkill(t, filepath.Join(ui.work, ".freeagent", "skills"), "workspace-only", "Workspace instructions.\n")
	code, raw := ui.request(t, http.MethodGet, "/api/skills", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"name":"workspace-only"`) ||
		!strings.Contains(string(raw), `"available":false`) {
		t.Fatalf("untrusted workspace skill %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/skills/workspace-only/update", map[string]bool{"enabled": true})
	if code != http.StatusConflict || !strings.Contains(string(raw), "信任") {
		t.Fatalf("untrusted workspace enable %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/skills/trust", map[string]bool{"trusted": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"trusted":true`) {
		t.Fatalf("trust %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/skills/workspace-only/update", map[string]bool{"enabled": true})
	if code != http.StatusOK || !strings.Contains(string(raw), `"source":"workspace"`) ||
		!strings.Contains(string(raw), `"enabled":true`) {
		t.Fatalf("trusted workspace enable %d %s", code, raw)
	}
}

func TestSkillsAPIRejectsMalformedImport(t *testing.T) {
	ui := setupUI(t, nil)
	code, raw := ui.request(t, http.MethodPost, "/api/skills", map[string]string{"path": filepath.Join(t.TempDir(), "missing")})
	if code != http.StatusConflict || !strings.Contains(string(raw), "Skill") {
		t.Fatalf("missing import %d %s", code, raw)
	}
}
