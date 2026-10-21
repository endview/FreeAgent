package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

// proposalUI starts a Chat UI whose model proposes one permission change and
// answers plainly on any later call.
func proposalUI(t *testing.T) (*testUI, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		if calls.Add(1) == 1 {
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "control-1", Name: "control_plan_change",
				Arguments: json.RawMessage(`{"kind":"execution.set_permission","arguments":{"mode":"workspace"}}`)}}}, nil
		}
		return harness.ModelResponse{Content: "kept the current permission"}, nil
	}))
	return ui, calls
}

func pendingProposal(t *testing.T, ui *testUI) (string, harness.Confirmation) {
	t.Helper()
	chat := ui.create(t)
	if code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "use workspace access", "request_key": "control"}); code != http.StatusAccepted {
		t.Fatalf("submit %d %s", code, raw)
	}
	if state := ui.settled(t, chat).Turns[0].State; state != "awaiting_approval" {
		t.Fatalf("proposal state=%s", state)
	}
	code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/preview", map[string]any{})
	var confirmation harness.Confirmation
	if code != http.StatusOK || json.Unmarshal(raw, &confirmation) != nil || confirmation.ControlOperationID == "" {
		t.Fatalf("preview %d %s", code, raw)
	}
	return chat, confirmation
}

func renderedCode(t *testing.T, ui *testUI, chat string, confirmation harness.Confirmation) string {
	t.Helper()
	code, raw := ui.request(t, "GET", "/api/control/confirmation?operation_id="+url.QueryEscape(confirmation.ControlOperationID)+
		"&conversation_id="+url.QueryEscape(chat), nil)
	var rendered harness.ControlConfirmation
	if code != http.StatusOK || json.Unmarshal(raw, &rendered) != nil || len(rendered.Code) != 10 {
		t.Fatalf("confirmation %d %s", code, raw)
	}
	return rendered.Code
}

func TestChatConfirmsModelControlProposalWithRenderedCode(t *testing.T) {
	ui, calls := proposalUI(t)
	before := ui.tasks.Config().Execution.PermissionMode
	chat, confirmation := pendingProposal(t, ui)

	// Without the rendered code the decision is refused and nothing changes.
	ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{"confirmation": confirmation, "approved": true})
	if state := ui.settled(t, chat).Turns[0].State; state != "awaiting_approval" || ui.tasks.Config().Execution.PermissionMode != before {
		t.Fatalf("codeless confirm changed state=%s permission=%s", state, ui.tasks.Config().Execution.PermissionMode)
	}
	controlCode := renderedCode(t, ui, chat, confirmation)
	if again := renderedCode(t, ui, chat, confirmation); again != controlCode {
		t.Fatal("rendering the same pending proposal issued a different code")
	}
	// The direct control route cannot apply a proposal that its Run is waiting on.
	code, raw := ui.request(t, "POST", "/api/control/confirm", map[string]string{
		"operation_id": confirmation.ControlOperationID, "conversation_id": chat, "code": controlCode})
	if code != http.StatusConflict || !strings.Contains(string(raw), "control_run") {
		t.Fatalf("direct apply of a Run proposal %d %s", code, raw)
	}
	formatted := controlCode[:5] + "-" + strings.ToLower(controlCode[5:])
	if code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{
		"confirmation": confirmation, "approved": true, "code": formatted}); code != http.StatusAccepted {
		t.Fatalf("confirm %d %s", code, raw)
	}
	view := ui.settled(t, chat)
	if view.Turns[0].State != "completed" || ui.tasks.Config().Execution.PermissionMode != harness.PermissionWorkspace || calls.Load() != 1 {
		t.Fatalf("applied state=%s permission=%s calls=%d", view.Turns[0].State, ui.tasks.Config().Execution.PermissionMode, calls.Load())
	}
	_, raw = ui.request(t, "GET", "/api/chats/"+chat, nil)
	if strings.Contains(string(raw), controlCode) {
		t.Fatal("the confirmation code reached the chat transcript")
	}
}

func TestChatRejectsModelControlProposalWithoutCode(t *testing.T) {
	ui, calls := proposalUI(t)
	before := ui.tasks.Config().Execution.PermissionMode
	chat, confirmation := pendingProposal(t, ui)
	if code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/confirm", map[string]any{
		"confirmation": confirmation, "approved": false}); code != http.StatusAccepted {
		t.Fatalf("reject %d %s", code, raw)
	}
	view := ui.settled(t, chat)
	if view.Turns[0].State != "completed" || ui.tasks.Config().Execution.PermissionMode != before || calls.Load() != 2 {
		t.Fatalf("rejected state=%s permission=%s calls=%d", view.Turns[0].State, ui.tasks.Config().Execution.PermissionMode, calls.Load())
	}
}

// Ending the waiting Run, including the end-and-send path, withdraws its
// proposal so a card or CLI prompt left open cannot apply it later.
func TestEndingRunWithdrawsControlProposal(t *testing.T) {
	ui, _ := proposalUI(t)
	before := ui.tasks.Config().Execution.PermissionMode
	chat, confirmation := pendingProposal(t, ui)
	controlCode := renderedCode(t, ui, chat, confirmation)
	if code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/end", map[string]bool{"acknowledge": true}); code != http.StatusOK {
		t.Fatalf("end %d %s", code, raw)
	}
	control, err := ui.tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	operations, err := control.ListControlOperations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range operations {
		if operation.ID == confirmation.ControlOperationID {
			found = true
			if operation.Status != harness.ControlRejected || operation.ErrorCode != "run_abandoned" {
				t.Fatalf("ended Run left proposal status=%s code=%s", operation.Status, operation.ErrorCode)
			}
		}
	}
	if !found {
		t.Fatal("withdrawn proposal is missing from the operation list")
	}
	if code, _ := ui.request(t, "GET", "/api/control/confirmation?operation_id="+url.QueryEscape(confirmation.ControlOperationID)+
		"&conversation_id="+url.QueryEscape(chat), nil); code == http.StatusOK {
		t.Fatal("a withdrawn proposal still rendered a confirmation code")
	}
	ui.request(t, "POST", "/api/control/confirm", map[string]string{
		"operation_id": confirmation.ControlOperationID, "conversation_id": chat, "code": controlCode})
	if ui.tasks.Config().Execution.PermissionMode != before {
		t.Fatal("a withdrawn proposal was applied")
	}
}

func TestAttachConfirmsControlProposalOnlyForInteractiveClient(t *testing.T) {
	ui, _ := proposalUI(t)
	chat, confirmation := pendingProposal(t, ui)
	info := handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}
	observer, err := handoff.NewClient(info, ui.server.attachToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.ConfirmControl(t.Context(), confirmation, true, "0000000000"); err == nil || !strings.Contains(err.Error(), "user_interaction_required") {
		t.Fatalf("attach.secret confirmed a control proposal: %v", err)
	}
	if _, err := observer.ControlConfirmation(t.Context(), confirmation.ControlOperationID, chat); err == nil {
		t.Fatal("attach.secret rendered a control confirmation code")
	}
	interactive, err := handoff.NewInteractiveClient(info, ui.server.token)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := interactive.ControlConfirmation(t.Context(), confirmation.ControlOperationID, chat)
	if err != nil {
		t.Fatal(err)
	}
	if err := interactive.ConfirmControl(t.Context(), confirmation, true, rendered.Code); err != nil {
		t.Fatal(err)
	}
	if state := ui.settled(t, chat).Turns[0].State; state != "completed" || ui.tasks.Config().Execution.PermissionMode != harness.PermissionWorkspace {
		t.Fatalf("attached apply state=%s permission=%s", state, ui.tasks.Config().Execution.PermissionMode)
	}
}
