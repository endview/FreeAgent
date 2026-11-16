package chatui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func TestManagementPublicLoginAndCSRFUseTheSameLifecycle(t *testing.T) {
	f := newPublicFixture(t, harness.EchoModel{})
	chat := f.ui.create(t)
	path := "/api/conversation-management"
	if w := f.request(t, "GET", path+"?view=trash", nil, nil); w.Code != 401 {
		t.Fatal("anonymous management", w.Code, w.Body.String())
	}
	f.login(t)
	body, _ := json.Marshal(map[string]any{"operation": "preview", "command": harness.ManagementCommand{Action: "trash", IDs: []string{chat}}})
	w := f.request(t, "POST", path, bytes.NewReader(body), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p harness.ManagementPreview
	if json.Unmarshal(w.Body.Bytes(), &p) != nil {
		t.Fatal(w.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"operation": "execute", "id": p.ID})
	if w = f.request(t, "POST", path, bytes.NewReader(body), map[string]string{"X-FreeAgent-CSRF": ""}); w.Code != 403 {
		t.Fatal("missing management CSRF", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", path, bytes.NewReader(body), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "completed") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request(t, "GET", path+"?view=trash", nil, nil)
	var page harness.ManagementPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestManagementAPIUsesOwnerControlAndPreservesPartialResults(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	code, raw := ui.request(t, "POST", "/api/conversation-management", map[string]any{"operation": "preview", "command": harness.ManagementCommand{Action: "trash", IDs: []string{chat}}})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	var p harness.ManagementPreview
	if json.Unmarshal(raw, &p) != nil {
		t.Fatal(string(raw))
	}
	code, raw = ui.request(t, "POST", "/api/conversation-management", map[string]any{"operation": "execute", "id": p.ID})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	var result harness.ManagementResult
	if json.Unmarshal(raw, &result) != nil || len(result.Items) != 1 || result.Items[0].Status != "completed" {
		t.Fatal(string(raw))
	}
	code, raw = ui.request(t, "GET", "/api/chats/"+chat, nil)
	if code == 200 || !bytes.Contains(raw, []byte("conversation_retired")) {
		t.Fatal(code, string(raw))
	}
	request, err := http.NewRequestWithContext(t.Context(), "GET", ui.http.URL+"/api/conversation-management?view=trash", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.attachToken)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("observer token reached management", response.StatusCode)
	}
	operations, err := ui.tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	list, err := operations.ListControlOperations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range list {
		found = found || op.Kind == harness.ControlManageConversations && op.Status == harness.ControlApplied
	}
	if !found {
		t.Fatal("management omitted control journal")
	}
}

func TestManagementAIConfirmsCurrentConversationThenEndsBeforeTrash(t *testing.T) {
	for _, action := range []string{"trash", "move", "delete_space"} {
		t.Run(action, func(t *testing.T) { testManagementAIConfirmsCurrentConversation(t, action) })
	}
}

func testManagementAIConfirmsCurrentConversation(t *testing.T, action string) {
	var chat string
	var command harness.ManagementCommand
	var calls atomic.Int32
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		calls.Add(1)
		args, _ := json.Marshal(map[string]any{"kind": harness.ControlManageConversations, "arguments": map[string]any{"command": command}})
		return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "manage-self", Name: "control_plan_change", Arguments: args}}}, nil
	}))
	chat = ui.create(t)
	command = harness.ManagementCommand{Action: action, IDs: []string{chat}}
	if action != "trash" {
		source, kind, external := harness.SpaceSourceLocal, "", ""
		if action == "delete_space" {
			source, kind, external = harness.SpaceSourceTelegram, "private", "isolated-owner-private"
		}
		space, e := ui.tasks.NewSpace(t.Context(), "模型整理空间", source, kind, external)
		if e != nil {
			t.Fatal(e)
		}
		command.SpaceID = space.ID
		if action == "delete_space" {
			code, raw := ui.request(t, "POST", "/api/spaces", map[string]any{"kind": "space.set_permission", "arguments": map[string]any{"space_id": space.ID, "selection": harness.PermissionSelection{SpaceID: space.ID, Source: "space", Mode: harness.PermissionAsk, OwnerPrivate: true}}, "confirmed": true})
			if code != 200 {
				t.Fatal(code, string(raw))
			}
			inside, e := ui.tasks.NewConversationInSpace(t.Context(), space.ID, "当前空间内")
			if e != nil {
				t.Fatal(e)
			}
			chat = inside.ID
			yes := true
			command.IDs = nil
			command.TrashConversations = &yes
		}
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "把当前会话移到回收站", "request_key": "manage-self"})
	if code != 202 {
		t.Fatal(code, string(raw))
	}
	view := ui.settled(t, chat)
	if len(view.Turns) != 1 || view.Turns[0].State != "awaiting_approval" {
		t.Fatal(view)
	}
	code, raw = ui.request(t, "POST", "/api/chats/"+chat+"/preview", map[string]any{})
	var confirmation harness.Confirmation
	if code != 200 || json.Unmarshal(raw, &confirmation) != nil {
		t.Fatal(code, string(raw))
	}
	controlCode := renderedCode(t, ui, chat, confirmation)
	code, raw = ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{"confirmation": confirmation, "approved": true, "code": controlCode})
	if code != 202 {
		t.Fatal(code, string(raw))
	}
	deadline := time.Now().Add(8 * time.Second)
	retired := false
	for time.Now().Before(deadline) {
		code, raw = ui.request(t, "GET", "/api/chats/"+chat, nil)
		if action == "move" {
			current, e := ui.tasks.Chat(t.Context(), chat, "")
			if e == nil && current.Conversation.SpaceID == command.SpaceID {
				retired = true
				break
			}
		}
		if code != 200 && bytes.Contains(raw, []byte("conversation_retired")) {
			retired = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !retired {
		operations, _ := ui.tasks.Control()
		list, _ := operations.ListControlOperations(t.Context())
		t.Fatal("self management did not complete", string(raw), list)
	}
	if calls.Load() != 1 {
		t.Fatal("model called after management", calls.Load())
	}
	page, err := ui.tasks.ManageConversations(t.Context(), harness.ManagementQuery{View: "trash"})
	want := 1
	if action == "move" {
		want = 0
	}
	if err != nil || page.Total != want {
		t.Fatal(page, err)
	}
}
