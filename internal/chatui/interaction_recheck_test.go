package chatui

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestRecheckRunActionsBindToDisplayedTurn(t *testing.T) {
	calls := 0
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		calls++
		if calls == 2 {
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "newer-write", Name: "file_create", Arguments: json.RawMessage(`{"path":"pending.txt","content":"pending"}`)}}}, nil
		}
		return harness.ModelResponse{Content: "done"}, nil
	}))
	chat := ui.create(t)
	old, err := ui.tasks.Submit(t.Context(), chat, "older", "first")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := ui.tasks.Submit(t.Context(), chat, "newer", "second")
	if err != nil || latest.Run.Status != "waiting_confirmation" {
		t.Fatal(err)
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/end?run_id="+old.Run.ID, map[string]bool{"acknowledge": true})
	if code != 409 {
		t.Fatalf("end exact task: %d %s", code, raw)
	}
	older, _, _, err := ui.tasks.Inspect(t.Context(), old.Run.ID)
	if err != nil || older.Status != "completed" {
		t.Fatal("wrong task ended", err)
	}
	newer, _, _, err := ui.tasks.Inspect(t.Context(), latest.Run.ID)
	if err != nil || newer.Status != "waiting_confirmation" {
		t.Fatal("newest task changed", err)
	}
	other := ui.create(t)
	for _, action := range []string{"end", "continue", "preview", "confirm"} {
		code, _ := ui.request(t, "POST", "/api/chats/"+other+"/"+action+"?run_id="+latest.Run.ID, map[string]bool{"acknowledge": true})
		if code < 400 {
			t.Fatalf("cross-conversation %s accepted: %d", action, code)
		}
	}
	code, raw = ui.request(t, "POST", "/api/chats/"+chat+"/end?run_id="+latest.Run.ID, map[string]bool{"acknowledge": true})
	if code != 200 {
		t.Fatalf("end pending task: %d %s", code, raw)
	}
	newer, _, _, err = ui.tasks.Inspect(t.Context(), latest.Run.ID)
	if err != nil || newer.Status != "abandoned" {
		t.Fatal("pending task not ended", err)
	}
}
