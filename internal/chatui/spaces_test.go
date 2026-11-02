package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func TestSpaceGUIAndAttachedCLIShareRecordsAndConfirmation(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	if err := ui.tasks.SetSpacePermission(t.Context(), harness.LocalSpaceID, harness.PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	code, raw := ui.request(t, http.MethodPost, "/api/spaces", map[string]any{"kind": harness.ControlCreateSpace, "arguments": map[string]any{"title": "Project"}})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, raw)
	}
	var created harness.ControlResult
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	var space harness.Space
	if err := json.Unmarshal(created.Data, &space); err != nil || space.ID == "" {
		t.Fatalf("result %s %v", raw, err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/chats", map[string]string{"title": "Shared", "space_id": space.ID})
	if code != http.StatusCreated {
		t.Fatalf("conversation %d %s", code, raw)
	}
	var chat harness.Conversation
	_ = json.Unmarshal(raw, &chat)
	client, err := handoff.NewClient(handoff.EngineInfo{Origin: ui.http.URL, Generation: "fixture", OwnerKind: "interactive"}, ui.server.token)
	if err != nil {
		t.Fatal(err)
	}
	client.Header = "X-FreeAgent-Key"
	spaces, err := client.Spaces(t.Context())
	if err != nil || len(spaces) != 2 {
		t.Fatalf("attach spaces=%+v %v", spaces, err)
	}
	view, err := client.Chat(t.Context(), chat.ID)
	if err != nil || view.Conversation.SpaceID != space.ID || view.Permission.Mode != harness.PermissionNone {
		t.Fatalf("attach view=%+v %v", view, err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/spaces", map[string]any{"kind": harness.ControlSetSpacePermission, "arguments": map[string]string{"space_id": space.ID, "mode": "full"}})
	if code != http.StatusConflict {
		t.Fatalf("Full without confirmation %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/spaces", map[string]any{"kind": harness.ControlSetSpacePermission, "confirmed": true, "arguments": map[string]string{"space_id": space.ID, "mode": "workspace"}})
	if code != http.StatusOK {
		t.Fatalf("permission %d %s", code, raw)
	}
	view, err = client.Chat(t.Context(), chat.ID)
	if err != nil || view.Permission.Mode != harness.PermissionWorkspace {
		t.Fatalf("shared permission=%+v %v", view.Permission, err)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/spaces", map[string]any{"kind": harness.ControlMoveConversation, "confirmed": true, "arguments": map[string]string{"space_id": "local", "conversation_id": chat.ID}})
	if code != http.StatusOK {
		t.Fatalf("move %d %s", code, raw)
	}
	view, err = client.Chat(t.Context(), chat.ID)
	if err != nil || view.Conversation.ID != chat.ID || view.Permission.Mode != harness.PermissionNone {
		t.Fatalf("identity/grant changed=%+v %v", view, err)
	}
}

func TestSpaceAIProposalUsesExistingPendingRunAndExactConfirmation(t *testing.T) {
	calls := 0
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		calls++
		return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "new-space", Name: "control_plan_change", Arguments: json.RawMessage(`{"kind":"space.create","arguments":{"title":"AI Project"}}`)}}}, nil
	}))
	chat := ui.create(t)
	code, _ := ui.request(t, http.MethodPost, "/api/chats/"+chat+"/submit", map[string]string{"prompt": "create a space", "request_key": "new-space"})
	if code != http.StatusAccepted {
		t.Fatal(code)
	}
	view := ui.settled(t, chat)
	if len(view.Turns) != 1 || view.Turns[0].State != "awaiting_approval" {
		t.Fatalf("proposal %+v", view)
	}
	spaces, _ := ui.tasks.Spaces(t.Context())
	if len(spaces) != 1 {
		t.Fatal("proposal applied before confirmation")
	}
	confirmation, err := ui.tasks.Preview(t.Context(), view.Turns[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	control, err := ui.tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	card, err := control.RenderConfirmation(t.Context(), confirmation.ControlOperationID, chat, controlPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ui.server.confirmControlProposal(t.Context(), chat, confirmation.RunID, confirmation, card.Code, true)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("confirm %s %v", result.Run.Status, err)
	}
	spaces, _ = ui.tasks.Spaces(t.Context())
	if len(spaces) != 2 || calls != 1 {
		t.Fatalf("spaces=%+v modelcalls=%d", spaces, calls)
	}
}
