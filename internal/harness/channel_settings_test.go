package harness

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChannelConfirmersRevisionReplayAndNoAdmissionGrant(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	core.Store.telegramIdentity("1")
	ctx := t.Context()
	a, err := tasks.TelegramConfirmers(ctx, "1")
	if err != nil || a.Revision != 0 || len(a.Members) != 0 {
		t.Fatal(a, err)
	}
	b, err := tasks.ApplyTelegramConfirmers(ctx, "1", []string{"7"}, 0, "first")
	if err != nil || b.Revision != 1 {
		t.Fatal(b, err)
	}
	if _, err := tasks.ApplyTelegramConfirmers(ctx, "1", []string{"8"}, 0, "stale"); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatal(err)
	}
	c, err := tasks.ApplyTelegramConfirmers(ctx, "1", nil, 1, "remove")
	if err != nil || c.Revision != 2 {
		t.Fatal(c, err)
	}
	replayed, err := tasks.ApplyTelegramConfirmers(ctx, "1", []string{"7"}, 0, "first")
	if err != nil || replayed.Revision != 1 {
		t.Fatal(replayed, err)
	}
	current, _ := tasks.TelegramConfirmers(ctx, "1")
	if current.Revision != 2 || len(current.Members) != 0 {
		t.Fatal("replay mutated current roster", current)
	}
	r, err := tasks.TelegramConfirmersResult(ctx, "1", "first")
	if err != nil || !r.Found || r.Revision != 1 {
		t.Fatal(r, err)
	}
	if _, err := tasks.CheckTelegramAccess(ctx, ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}, "telegram:user:7"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("confirmers also granted chat", err)
	}
}

func TestPrivateSettingCardsBindClickerAndRosterRevision(t *testing.T) {
	for _, scenario := range []string{"valid_click", "revoked_then_readded", "new_session", "expired"} {
		revoked := scenario == "revoked_then_readded"
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			core, _, _ := testHarness(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				return ModelResponse{ToolCalls: []ToolCall{makeCall("setting", controlPlanTool, map[string]any{"kind": ControlSetConversationModel, "arguments": map[string]any{"model_id": "second"}})}}, nil
			}))
			core.controlToolsActive = true
			core.Config.Models["second"] = ModelConfig{Provider: "echo"}
			enableTestProviders(&core.Config)
			if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
				t.Fatal(err)
			}
			tasks := NewTaskService(core)
			target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
			grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
			_, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 0, "allow")
			if err != nil {
				t.Fatal(err)
			}
			input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "one", Purpose: "request"}, "switch model")
			if err != nil {
				t.Fatal(err)
			}
			result, err := tasks.SubmitChannel(t.Context(), input)
			if err != nil || result.Run.Status != "waiting_confirmation" {
				t.Fatal(result.Run.Status, err)
			}
			var cardMessage string
			session := newChannelSession(tasks, "telegram", func(_ context.Context, _ ChannelTarget, _ string, _ string, buttons []channelButton) (string, error) {
				cardMessage = "card"
				return cardMessage, nil
			})
			session.reset("1")
			if err := session.Deliver(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			token := ""
			for k, v := range session.callbacks {
				if v.Approved {
					token = k
				}
			}
			if token == "" {
				t.Fatal("no setting button")
			}
			event := channelEvent{Target: target, Person: "telegram:user:8", Callback: token, CallbackMessage: cardMessage}
			if err := session.callback(t.Context(), event); !errors.Is(err, ErrPermissionDenied) {
				t.Fatal("another requester consumed card", err)
			}
			if _, exists := session.callbacks[token]; !exists {
				t.Fatal("unauthorized click consumed card")
			}
			event.Person = "telegram:user:7"
			if scenario == "new_session" || scenario == "expired" {
				if scenario == "new_session" {
					session.reset("1")
				} else {
					key := "connector/telegram/1/setting-card/" + session.callbacks[token].Confirmation.ControlOperationID
					if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=jsonb_set(document,'{expires}',to_jsonb($1::text),true)::text WHERE key=$2`, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), key); err != nil {
						t.Fatal(err)
					}
				}
				if err := tasks.expireChannelSettingCards(t.Context(), target, session.sessionGeneration()); err != nil {
					t.Fatal(err)
				}
				run, _ := core.Store.GetRun(t.Context(), result.Run.ID)
				if run.Status == "waiting_confirmation" || calls != 1 {
					t.Fatal("invalid card retained conversation or replayed model", run.Status, calls)
				}
				return
			}
			if revoked {
				if _, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", nil, 1, "remove"); err != nil {
					t.Fatal(err)
				}
				if _, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 2, "readd"); err != nil {
					t.Fatal(err)
				}
				if err := session.callback(t.Context(), event); !errors.Is(err, ErrPermissionDenied) {
					t.Fatal("revoked card revived", err)
				}
				if err := tasks.expireChannelSettingCards(t.Context(), target); err != nil {
					t.Fatal(err)
				}
				run, _ := core.Store.GetRun(t.Context(), result.Run.ID)
				if run.Status == "waiting_confirmation" {
					t.Fatal("revoked proposal kept conversation locked")
				}
			} else {
				ctx := context.WithValue(t.Context(), channelEventKey{}, "fixture/click")
				if err := session.callback(ctx, event); err != nil {
					t.Fatal(err)
				}
				run, _ := core.Store.GetRun(t.Context(), result.Run.ID)
				if run.Status != "completed" {
					t.Fatal(run.Status)
				}
			}
			if calls != 1 {
				t.Fatal("decision ran model", calls)
			}
		})
	}
}

func TestSettingCardStoredBindingCannotBeReplaced(t *testing.T) {
	// Reissuing a card must not overwrite the first roster revision/Run binding.
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	core.Store.telegramIdentity("1")
	_, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 0, "allow")
	if err != nil {
		t.Fatal(err)
	}
	binding := channelSettingBinding{Run: "original", Target: ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}, Revision: 1, Expires: time.Now().Add(-time.Minute)}
	first, err := tasks.bindSettingCard(t.Context(), "proposal", binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.Revision = 3
	if _, err := tasks.bindSettingCard(t.Context(), "proposal", binding); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatal("old binding replaced", first, err)
	}
}

func TestPrivateChannelSettingApprovalAndRejectionDoNotCallModelAgain(t *testing.T) {
	for _, scenario := range []string{"approve", "reject", "already_satisfied", "different_target"} {
		approved := scenario != "reject"
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			core, _, _ := testHarness(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				return ModelResponse{ToolCalls: []ToolCall{makeCall("setting", controlPlanTool, map[string]any{"kind": ControlSetConversationModel, "arguments": map[string]any{"model_id": "second"}})}}, nil
			}))
			core.controlToolsActive = true
			core.Config.Models["second"] = ModelConfig{Provider: "echo"}
			core.Config.Models["third"] = ModelConfig{Provider: "echo"}
			enableTestProviders(&core.Config)
			if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
				t.Fatal(err)
			}
			tasks := NewTaskService(core)
			target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
			grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
			if _, err := tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 0, "allow"); err != nil {
				t.Fatal(err)
			}
			input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "one", Purpose: "request"}, "use second")
			if err != nil {
				t.Fatal(err)
			}
			result, err := tasks.SubmitChannel(t.Context(), input)
			if err != nil || result.Run.Status != "waiting_confirmation" {
				t.Fatal(result.Run.Status, result.Run.StopReason, err)
			}
			preview, err := tasks.Preview(t.Context(), result.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			control, _ := tasks.Control()
			var satisfiedRevision uint64
			if scenario == "already_satisfied" || scenario == "different_target" {
				goal := "second"
				if scenario == "different_target" {
					goal = "third"
				}
				op, e := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetConversationModel, Arguments: mustJSON(map[string]string{"model_id": goal}), Authorization: ControlUserDirect, ConversationID: result.Run.ConversationID})
				if e != nil {
					t.Fatal(e)
				}
				owner, e := control.IssueInteractiveCapability(t.Context(), result.Run.ConversationID, "owner")
				if e != nil {
					t.Fatal(e)
				}
				card, e := control.RenderConfirmation(t.Context(), op.ID, result.Run.ConversationID, "owner")
				if e != nil {
					t.Fatal(e)
				}
				if _, e := control.ConfirmControlChange(t.Context(), op.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: result.Run.ConversationID, Principal: "owner", Code: card.Code, Capability: owner}); e != nil {
					t.Fatal(e)
				}
				control.mu.Lock()
				satisfiedRevision = control.revisionLocked("conversation.model:" + result.Run.ConversationID)
				control.mu.Unlock()
			}
			capability, err := control.IssueInteractiveCapability(t.Context(), result.Run.ConversationID, "verified-click")
			if err != nil {
				t.Fatal(err)
			}
			confirmation, err := control.renderConfirmation(t.Context(), preview.ControlOperationID, result.Run.ConversationID, "verified-click", true)
			if err != nil {
				t.Fatal(err)
			}
			decided, err := tasks.ConfirmControl(t.Context(), result.Run.ID, preview, ControlAuthorization{Source: ControlUserDirect, Principal: "verified-click", ConversationID: result.Run.ConversationID, Code: confirmation.Code, Capability: capability, finishWithoutModel: true, acceptSatisfied: true}, approved)
			if scenario == "different_target" {
				if !errors.Is(err, ErrControlStaleRevision) || calls != 1 {
					t.Fatal("different target did not require new preview", err, calls)
				}
				chat, _ := core.Store.GetConversation(t.Context(), result.Run.ConversationID)
				if chat.ModelID != "third" {
					t.Fatal("old card overwrote new setting", chat.ModelID)
				}
				return
			}
			if err != nil || decided.Run.Status != "completed" || calls != 1 {
				t.Fatal(decided.Run.Status, calls, err)
			}
			chat, _ := core.Store.GetConversation(t.Context(), result.Run.ConversationID)
			if (chat.ModelID == "second") != approved {
				t.Fatal("wrong stored model", chat.ModelID)
			}
			if scenario == "already_satisfied" {
				if decided.Run.StopReason != "target_already_satisfied" {
					t.Fatal(decided.Run.StopReason)
				}
				control.mu.Lock()
				revision := control.revisionLocked("conversation.model:" + result.Run.ConversationID)
				control.mu.Unlock()
				if revision != satisfiedRevision {
					t.Fatal("satisfied proposal wrote a second time", revision)
				}
			}
		})
	}
}
