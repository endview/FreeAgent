package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLISpacesCreateChooseMoveAndRestart(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "spaces", "new", "--data-dir", data, "--title", "CLI Project", "--yes")
	if err != nil {
		t.Fatalf("new %s %v", raw, err)
	}
	var change harness.ControlResult
	_ = json.Unmarshal([]byte(raw), &change)
	var space harness.Space
	if json.Unmarshal(change.Data, &space) != nil || space.ID == "" {
		t.Fatalf("change %s", raw)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--space", space.ID, "--json", "hello")
	if err != nil {
		t.Fatalf("run %s %v", raw, err)
	}
	var result harness.RunResult
	if json.Unmarshal([]byte(raw), &result) != nil || result.Run.Permission.Mode != harness.PermissionNone {
		t.Fatalf("result %s", raw)
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data, "--space", space.ID)
	if err != nil || !strings.Contains(raw, result.Run.ConversationID) {
		t.Fatalf("sessions %s %v", raw, err)
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data)
	if err != nil || strings.Contains(raw, result.Run.ConversationID) {
		t.Fatalf("space isolation %s %v", raw, err)
	}
	raw, _, err = invoke(t, "", "spaces", "move", "--data-dir", data, "--session", result.Run.ConversationID, "--space", "local", "--yes")
	if err != nil {
		t.Fatalf("move %s %v", raw, err)
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data)
	if err != nil || !strings.Contains(raw, result.Run.ConversationID) {
		t.Fatalf("moved session %s %v", raw, err)
	}
}
