package harness

import (
	"context"
	"testing"
)

func TestPrivateSettingChangesOnlySelectedExtensionAndNextRunActuallyUsesIt(t *testing.T) {
	b, tasks, target, chat := replyExtensionFixture(t, true, true, true, "parts-valid")
	core := tasks.core
	core.controlToolsActive = true
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	if err := control.refreshBaseline(); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 0, "allow"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls > 1 {
			return ModelResponse{Content: replyFixtureText}, nil
		}
		return ModelResponse{ToolCalls: []ToolCall{makeCall("choice", controlPlanTool, map[string]any{"kind": ControlSetConversationCapabilities, "arguments": map[string]any{"capabilities": CapabilitySelection{Extensions: []string{deliveryTestExtension}, Settings: map[string]map[string]string{deliveryTestExtension: {"reply_mode": "single"}}}}})}}, nil
	})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "setting", Purpose: "request"}, "one message")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatal(result.Run.Status, err)
	}
	preview, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "verified-click")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.renderConfirmation(t.Context(), preview.ControlOperationID, chat, "verified-click", true)
	if err != nil {
		t.Fatal(err)
	}
	decided, err := tasks.ConfirmControl(t.Context(), result.Run.ID, preview, ControlAuthorization{Source: ControlUserDirect, Principal: "verified-click", ConversationID: chat, Code: code.Code, Capability: capability, finishWithoutModel: true, acceptSatisfied: true}, true)
	if err != nil || decided.Run.Status != "completed" || calls != 1 {
		t.Fatal(decided.Run.Status, calls, err)
	}
	if core.currentConfig().Extensions.Settings[deliveryTestExtension]["reply_mode"] != "paragraphs" {
		t.Fatal("conversation setting changed global extension defaults")
	}
	next, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "next", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := tasks.SubmitChannel(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := b.connections["tg"].session.extensionReplyParts(t.Context(), "reply/"+reply.Run.ID+"/proof", reply.Answer)
	if err != nil || len(parts) != 1 || parts[0] != replyFixtureText || calls != 2 {
		t.Fatal("next Run did not use selected extension option", parts, calls, err)
	}
}
