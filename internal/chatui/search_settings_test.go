package chatui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/webaccess"
)

func TestSearchSettingsSaveReadbackConflictAndLegacy(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, "GET", "/api/settings/web-search", nil)
	var first struct {
		Config    webaccess.Config  `json:"config"`
		Legacy    bool              `json:"legacy"`
		Revisions map[string]uint64 `json:"expected_revisions"`
	}
	if code != 200 || json.Unmarshal(raw, &first) != nil || !first.Legacy || first.Config.Mode != "off" {
		t.Fatalf("initial %d %s", code, raw)
	}
	policy := webaccess.DefaultConfig()
	policy.Mode = "auto"
	code, raw = ui.request(t, "PATCH", "/api/settings/web-search", map[string]any{"config": policy, "expected_revisions": first.Revisions})
	if code != 200 || ui.tasks.Config().WebSearch == nil || ui.tasks.Config().WebSearch.Mode != "auto" {
		t.Fatalf("save %d %s", code, raw)
	}
	code, raw = ui.request(t, "PATCH", "/api/settings/web-search", map[string]any{"config": first.Config, "expected_revisions": first.Revisions})
	if code != 409 || ui.tasks.Config().WebSearch.Mode != "auto" {
		t.Fatalf("stale draft %d %s", code, raw)
	}
	loaded, e := harness.LoadConfig(ui.data)
	if e != nil || loaded.WebSearch == nil || loaded.WebSearch.Mode != "auto" {
		t.Fatalf("disk readback %v", e)
	}
	code, raw = ui.request(t, "PATCH", "/api/settings/web-search", map[string]any{"config": map[string]any{"mode": "api"}})
	if code == 200 || ui.tasks.Config().WebSearch.Mode != "auto" {
		t.Fatalf("invalid overwrote config %d %s", code, raw)
	}
}

func TestSearchKeyCannotBecomeModelKey(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	key, e := ui.tasks.Keys().Add(harness.KeyInput{Name: "search only", Protocol: harness.KeyProtocolSearch, BaseURL: "https://search.example.org", Source: "saved", Secret: []byte("fixture-search-secret")})
	if e != nil {
		t.Fatal(e)
	}
	code, raw := ui.request(t, "POST", "/api/models/enable", map[string]any{"key": key.ID, "model": "not-a-model"})
	if code == 200 || strings.Contains(string(raw), "fixture-search-secret") {
		t.Fatalf("search Key used as model: %d %s", code, raw)
	}
	code, raw = ui.request(t, "GET", "/api/keys", nil)
	if code != 200 || strings.Contains(string(raw), "fixture-search-secret") {
		t.Fatalf("secret leaked: %d %s", code, raw)
	}
}

func TestSearchNetworkSwitchDoesNotRequireHTTPOrigins(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, "PATCH", "/api/settings/tools", map[string]any{"network": true, "network_allowed_origins": []string{}})
	config := ui.tasks.Config()
	if code != 200 || !harness.EffectiveBuiltinTools(config).Network || config.HTTP.Enabled {
		t.Fatalf("network-only toggle: %d %s", code, raw)
	}
	code, raw = ui.request(t, "PATCH", "/api/settings/tools", map[string]any{"network": false})
	if code != 200 || harness.EffectiveBuiltinTools(ui.tasks.Config()).Network {
		t.Fatalf("disable: %d %s", code, raw)
	}
}

func TestSearchConnectionProtectsReferencedKey(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, "POST", "/api/keys", map[string]any{"name": "search-bound", "protocol": "search", "base_url": "http://127.0.0.1:19999", "source": "none"})
	var key struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(raw, &key) != nil {
		t.Fatalf("key %d %s", code, raw)
	}
	policy := webaccess.DefaultConfig()
	policy.Mode = "api"
	policy.DefaultConnection = "local"
	policy.Connections = map[string]webaccess.Connection{"local": {Provider: "searxng", Key: key.ID}}
	code, raw = ui.request(t, "PATCH", "/api/settings/web-search", map[string]any{"config": policy})
	if code != 200 {
		t.Fatalf("policy %d %s", code, raw)
	}
	code, raw = ui.request(t, "POST", "/api/keys/"+key.ID+"/delete", map[string]any{})
	if code != 409 {
		t.Fatalf("referenced key deleted: %d %s", code, raw)
	}
	if _, e := ui.tasks.Keys().Find(key.ID); e != nil {
		t.Fatal(e)
	}
	code, raw = ui.request(t, "POST", "/api/keys/"+key.ID+"/update", map[string]any{"name": "search-bound", "protocol": "search", "base_url": "http://127.0.0.1:20000", "source": "none"})
	if code == 200 {
		t.Fatalf("endpoint changed without unlinking: %d %s", code, raw)
	}
}
