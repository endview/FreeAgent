package harness

import (
	"context"
	"testing"
)

func TestModelProposedBranchRequiresConfirmationAndDoesNotExecuteBranch(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	messages, _ := tasks.Messages(t.Context(), chat)
	core.controlToolsActive = true
	calls := 0
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{ToolCalls: []ToolCall{makeCall("branch-proposal", controlPlanTool, map[string]any{"kind": ControlBranchConversation, "arguments": map[string]any{"conversation_id": chat, "message_id": messages[0].ID, "mode": "rerun"}})}}, nil
	})
	before, _ := tasks.Conversations(t.Context())
	waiting, err := tasks.Submit(t.Context(), chat, "propose-branch", "make a branch")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("status=%s error=%v", waiting.Run.Status, err)
	}
	chats, _ := tasks.Conversations(t.Context())
	if len(chats) != len(before) {
		t.Fatal("proposal created branch before confirmation")
	}
	preview, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(t.Context(), preview.ControlOperationID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.ConfirmControl(t.Context(), waiting.Run.ID, preview, controlAuth(capability, chat, "user", code), true)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("confirmation status=%s error=%v", result.Run.Status, err)
	}
	chats, _ = tasks.Conversations(t.Context())
	if len(chats) != len(before)+1 || calls != 1 {
		t.Fatalf("chats=%d calls=%d", len(chats), calls)
	}
}
