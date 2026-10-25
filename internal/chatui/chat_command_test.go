package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func TestAttachMenuRequiresInteractiveCredential(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	client, err := handoff.NewClient(handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}, ui.server.attachToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ChatCommand(t.Context(), "config", "", "", nil, nil); err == nil {
		t.Fatal("observation credential reached interactive menus")
	}
	code, raw := ui.request(t, http.MethodPost, "/api/attach/chat-command", handoff.ChatCommand{Action: "config"})
	var projection harness.Config
	if code != 200 || json.Unmarshal(raw, &projection) != nil || projection.Workspace != ui.work || len(projection.Models) != 2 || projection.SystemPrompt != "" || projection.Models["default"].Key != "" {
		t.Fatalf("menu projection: %d %s", code, raw)
	}
}

func TestAttachCancelBindingCannotStopAnotherRequest(t *testing.T) {
	entered := make(chan struct{}, 1)
	ui := setupUI(t, fixtureModel(func(ctx context.Context, _ harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return harness.ModelResponse{}, ctx.Err()
	}))
	chat := ui.create(t)
	client, err := handoff.NewInteractiveClient(handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}, ui.server.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Submit(t.Context(), chat, "current-key", "wait"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	if err := client.CancelRequest(t.Context(), chat, "stale-key"); err != nil {
		t.Fatal(err)
	}
	state, err := client.State(t.Context())
	if err != nil || state.ActiveChat != chat || state.ActiveBinding != "current-key" {
		t.Fatalf("stale cancellation: %+v %v", state, err)
	}
	// A second viewer remains usable, while its competing execution is rejected.
	if _, err := client.Chat(t.Context(), chat); err != nil {
		t.Fatal(err)
	}
	if err := client.Submit(t.Context(), chat, "competing-key", "must not dispatch"); err == nil {
		t.Fatal("concurrent request accepted")
	}
	if err := client.CancelRequest(t.Context(), chat, "current-key"); err != nil {
		t.Fatal(err)
	}
	view := ui.settled(t, chat)
	if len(view.Turns) != 1 || view.Turns[0].State != "unknown" {
		t.Fatalf("cancellation evidence: %+v", view)
	}
}
