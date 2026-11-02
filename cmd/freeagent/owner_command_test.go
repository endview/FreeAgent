package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnerCommandsRedactStatusAndStopExistingOwner(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "owner", "status", "--data-dir", data)
	if err != nil || !strings.Contains(raw, `"running": false`) {
		t.Fatalf("offline: %s %v", raw, err)
	}
	_, token, _ := startTestWeb(t, data)
	raw, _, err = invoke(t, "", "owner", "status", "--data-dir", data)
	if err != nil || !strings.Contains(raw, `"running": true`) || strings.Contains(raw, token) {
		t.Fatalf("status leaked or failed: %v", err)
	}
	raw, _, err = invoke(t, "", "owner", "url", "--data-dir", data)
	if err != nil || !strings.HasSuffix(strings.TrimSpace(raw), "/#key="+token) {
		t.Fatalf("explicit URL failed: %v", err)
	}
	raw, _, err = invoke(t, "", "owner", "stop", "--data-dir", data)
	var result map[string]any
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || result["stop_requested"] != true || result["effects_undone"] != false {
		t.Fatalf("stop: %s %v", raw, err)
	}
}
