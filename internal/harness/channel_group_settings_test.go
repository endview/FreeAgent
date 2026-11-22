package harness

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestTelegramGroupSettingHandoffAllowsOtherMemberAndNoModelOnDecision(t *testing.T) {
	for _, decision := range []string{"approve", "reject", "withdraw", "web_race"} {
		t.Run(decision, func(t *testing.T) {
			b, tasks, target, _ := replyExtensionFixture(t, true, true, true, "parts-valid")
			target.ChatID = "-100"
			target.ThreadID = "11"
			grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target.group(), Kind: "group", Enabled: true, Members: []string{"telegram:user:7", "telegram:user:8", "telegram:user:9"}})
			chat, err := tasks.ChannelConversation(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			space, _ := tasks.ChannelSpace(t.Context(), target)
			if err = tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
				t.Fatal(err)
			}
			if err = tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{Extensions: []string{deliveryTestExtension}}); err != nil {
				t.Fatal(err)
			}
			if _, err = tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"8"}, 0, "confirmer"); err != nil {
				t.Fatal(err)
			}
			core := tasks.core
			core.controlToolsActive = true
			if err = WriteConfig(core.Store.DataDir(), core.Config); err != nil {
				t.Fatal(err)
			}
			control, err := tasks.Control()
			if err != nil {
				t.Fatal(err)
			}
			if err = control.refreshBaseline(); err != nil {
				t.Fatal(err)
			}
			calls := 0
			core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls++
				if calls == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("option", controlPlanTool, map[string]any{"kind": ControlSetConversationCapabilities, "arguments": map[string]any{"capabilities": CapabilitySelection{Extensions: []string{deliveryTestExtension}, Settings: map[string]map[string]string{deliveryTestExtension: {"reply_mode": "single"}}}}})}}, nil
				}
				return ModelResponse{Content: replyFixtureText}, nil
			})
			if err = b.Inbound(t.Context(), "tg", groupEvent(target, 10, "change one option")); err != nil {
				t.Fatal(err)
			}
			original, err := tasks.Latest(t.Context(), chat)
			if err != nil || original.Status != "completed" || original.StopReason != detachedProposalReason || calls != 1 {
				t.Fatal(original.Status, original.StopReason, calls, err)
			}
			var card channelwire.Operation
			ready, err := b.Outbox(t.Context(), "tg")
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range ready {
				if len(op.Buttons) == 3 {
					card = op
				}
			}
			if card.ID == "" {
				t.Fatal("missing native group card", ready)
			}
			if err = b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: card.ID, Dispatch: card.Dispatch, State: "sent", MessageID: "100"}); err != nil {
				t.Fatal(err)
			}
			index := 0
			if decision == "reject" {
				index = 1
			}
			if decision == "withdraw" {
				index = 2
			}
			e := groupEvent(target, 20, "")
			e.Callback, e.CallbackMessage = card.Buttons[index].Token, "100"
			e.Trigger = false
			e.Person = "telegram:user:9"
			if err = b.Inbound(t.Context(), "tg", e); err != nil {
				t.Fatal(err)
			}
			current, _ := tasks.SelectedCapabilities(t.Context(), chat)
			if current.Settings[deliveryTestExtension]["reply_mode"] != "" {
				t.Fatal("outsider decided setting")
			}
			other := groupEvent(target, 21, "ordinary message while setting pending")
			other.Person = "telegram:user:9"
			if err = b.Inbound(t.Context(), "tg", other); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal("pending setting blocked ordinary group message", calls)
			}
			latest, _ := tasks.Latest(t.Context(), chat)
			e.ID = "22"
			e.Person = "telegram:user:8"
			if decision == "withdraw" {
				e.Person = "telegram:user:7"
			}
			cross := e
			cross.ID, cross.Target.ThreadID = "220", "12"
			if err = b.Inbound(t.Context(), "tg", cross); err != nil {
				t.Fatal(err)
			}
			current, _ = tasks.SelectedCapabilities(t.Context(), chat)
			if current.Settings[deliveryTestExtension]["reply_mode"] != "" {
				t.Fatal("cross-topic callback wrote settings")
			}
			if decision == "web_race" {
				confirmation, ok, e2 := tasks.detachedSettingConfirmation(t.Context(), original)
				if e2 != nil || !ok {
					t.Fatal(confirmation, e2)
				}
				credential, e2 := control.IssueInteractiveCapability(t.Context(), chat, "web-owner")
				if e2 != nil {
					t.Fatal(e2)
				}
				code, e2 := control.RenderConfirmation(t.Context(), confirmation.ControlOperationID, chat, "web-owner")
				if e2 != nil {
					t.Fatal(e2)
				}
				resource := "conversation.capabilities:" + chat
				control.mu.Lock()
				before := control.revisionLocked(resource)
				control.mu.Unlock()
				webDone := make(chan error, 1)
				go func() {
					_, e2 := control.ConfirmControlChange(t.Context(), code.OperationID, ControlAuthorization{Source: ControlUserDirect, ConversationID: chat, Principal: "web-owner", Capability: credential, Code: code.Code})
					webDone <- e2
				}()
				tgErr := b.Inbound(t.Context(), "tg", e)
				if webErr := <-webDone; webErr != nil && !errors.Is(webErr, ErrControlAlreadyUsed) {
					t.Fatal(webErr)
				}
				if tgErr != nil {
					t.Fatal(tgErr)
				}
				control.mu.Lock()
				after := control.revisionLocked(resource)
				control.mu.Unlock()
				if after != before+1 {
					t.Fatalf("expected one settings write: revision %d -> %d", before, after)
				}
			} else if err = b.Inbound(t.Context(), "tg", e); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal("decision reran model", calls)
			}
			current, _ = tasks.SelectedCapabilities(t.Context(), chat)
			want := ""
			if decision == "approve" || decision == "web_race" {
				want = "single"
			}
			if current.Settings[deliveryTestExtension]["reply_mode"] != want {
				t.Fatal("unexpected setting", current)
			}
			if got, _ := tasks.Latest(t.Context(), chat); got.ID != latest.ID || got.Status != "completed" {
				t.Fatal("decision changed newer Run")
			}
			for n := 23; n < 25; n++ {
				e.ID = fmt.Sprint(n)
				if err = b.Inbound(t.Context(), "tg", e); err != nil {
					t.Fatal("duplicate decision pins polling cursor", err)
				}
			}
			if calls != 2 {
				t.Fatal("duplicate decision called model")
			}
			if decision == "approve" {
				if err = b.Inbound(t.Context(), "tg", groupEvent(target, 30, "use the saved option")); err != nil {
					t.Fatal(err)
				}
				next, _ := tasks.Latest(t.Context(), chat)
				parts, e := b.connections["tg"].session.extensionReplyParts(t.Context(), "reply/"+next.ID+"/next-run-proof", next.Output)
				if e != nil || len(parts) != 1 || parts[0] != replyFixtureText || calls != 3 {
					t.Fatal("next group Run did not use new extension value", parts, calls, e)
				}
			}
		})
	}
}
