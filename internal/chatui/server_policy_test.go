package chatui

import (
	"encoding/json"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestServerPolicyUsesControlAndDoesNotRestartLiveOwner(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	code, raw := ui.request(t, "PUT", "/api/server", map[string]any{"port": 19876})
	var result struct {
		Policy  harness.ServerConfig `json:"policy"`
		Origin  string               `json:"origin"`
		Restart bool                 `json:"restart_required"`
	}
	if code != 200 || json.Unmarshal(raw, &result) != nil || result.Policy.Port != 19876 || !result.Restart || result.Origin != ui.http.URL {
		t.Fatalf("server policy %d %s", code, raw)
	}
	saved, err := harness.LoadConfig(ui.data)
	if err != nil || saved.Server.Port != 19876 {
		t.Fatalf("policy not persisted: %v", err)
	}
	code, _ = ui.request(t, "PUT", "/api/server", map[string]any{"port": 65536})
	if code == 200 {
		t.Fatal("invalid port accepted")
	}
}
