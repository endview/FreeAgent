package harness

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/channelwire"
	"strings"
	"testing"
	"time"
)

func TestAffectWeaveGrantedExpressionAndFixedFollowupRecheck(t *testing.T) {
	b, tasks, target, rule := affectWakeHarness(t)
	source, _ := tasks.Latest(t.Context(), rule.ConversationID)
	identity, err := tasks.core.extensionIdentity(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	readRaw, err := tasks.ExtensionState(t.Context(), affectExtensionID, rule.ConversationID, "maya", "telegram:user:7")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		State struct {
			Revision int64 `json:"revision"`
		} `json:"state"`
	}
	_ = json.Unmarshal(readRaw, &view)
	chat, _ := tasks.core.Store.GetConversation(t.Context(), rule.ConversationID)
	control, _ := tasks.Control()
	args := map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": chat.SpaceID, "resource": "maya", "actor_id": "telegram:user:7"}, "action": "apply_proposal", "expected_revision": view.State.Revision, "payload": map[string]any{"source_id": identity.Inputs[0].ID, "proposal": map[string]any{"thread_proposals": []any{map[string]any{"action": "upsert", "thread_id": "deployment", "kind": "followup", "summary": "A specifically authorized deployment follow-up", "importance": .8, "unresolvedness_delta": .8, "confidence": .9}}}}}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, args, "", "fixture"); err != nil {
		t.Fatal(err)
	}
	sticker, err := tasks.rememberSticker(t.Context(), target, channelwire.Sticker{FileID: "test-file", UniqueID: "test-unique", Emoji: "👍", Width: 128, Height: 128, Format: "static"}, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := tasks.Config()
	rule.Generation = false
	cfg.Delivery.Rules = []FollowupRule{rule}
	cfg.Delivery.GenerationEnabled[affectExtensionID] = false
	settings := cfg.Extensions.Settings[affectExtensionID]
	settings["analysis_enabled"] = true
	settings["expression_enabled"] = true
	settings["fixed_followup_enabled"] = true
	settings["reply_mode"] = "character"
	if err = WriteConfig(tasks.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	tasks.core.setConfig(cfg)
	tasks.core.extensions.configure(cfg)
	if err = control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	analyses := 0
	tasks.core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.SupplementalKey != "" {
			analyses++
			return ModelResponse{Content: `{"appraisal":{"kind":"interest","confidence":0.8,"reason":"Confirmed deployment matter"},"emotion_candidates":[],"behavior_hint":"Be concise and concrete","persistent_candidates":{},"expression":{"sticker_ref":"` + sticker.ID + `","reaction":"👍","followup":{"text":"部署检查的结果怎么样了？","source_ref":"thread:deployment","after_seconds":600}}}`}, nil
		}
		return ModelResponse{Content: "第一段回应。\n\n再补充一句。"}, nil
	})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "2", Purpose: "request"}, "The deployment check is still pending.")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" || analyses != 1 {
		t.Fatal(result.Run.Status, analyses, err)
	}
	var held, reactions, fixed int
	if err = tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FILTER(WHERE document->>'kind'='sticker' AND document->>'state'='held'),count(*) FILTER(WHERE document->>'kind'='reaction'),count(*) FILTER(WHERE document->>'proactive'='true') FROM fa.meta WHERE starts_with(key,'channel/operation/') AND document->>'run_id'=$1`, result.Run.ID).Scan(&held, &reactions, &fixed); err != nil || held != 1 || reactions != 1 || fixed != 1 {
		t.Fatal("expression selection not queued", held, reactions, fixed, err)
	}
	// Resolving the actual matter must withdraw the fixed future candidate,
	// without another model evaluation at delivery.check.
	readRaw, _ = tasks.ExtensionState(t.Context(), affectExtensionID, chat.ID, "maya", "telegram:user:7")
	_ = json.Unmarshal(readRaw, &view)
	nextIdentity, _ := tasks.core.extensionIdentity(t.Context(), result.Run)
	args["expected_revision"] = view.State.Revision
	args["payload"] = map[string]any{"source_id": nextIdentity.Inputs[0].ID, "proposal": map[string]any{"thread_proposals": []any{map[string]any{"action": "resolve", "thread_id": "deployment", "confidence": .9}}}}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, args, "", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=jsonb_set(document,'{not_before_ms}',to_jsonb($1::bigint))::text WHERE starts_with(key,'channel/operation/') AND document->>'run_id'=$2 AND document->>'proactive'='true'`, time.Now().Add(-time.Second).UnixMilli(), result.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Outbox(t.Context(), "tg"); err != nil {
		t.Fatal(err)
	}
	var cancelled int
	if err = tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE starts_with(key,'channel/operation/') AND document->>'run_id'=$1 AND document->>'proactive'='true' AND document->>'state'='cancelled'`, result.Run.ID).Scan(&cancelled); err != nil || cancelled != 1 || analyses != 1 {
		t.Fatal("fixed review replayed or failed to withdraw", cancelled, analyses, err)
	}
}

func TestAffectWeaveLinkedFeedbackDraftConfirmationAndRestart(t *testing.T) {
	b, tasks, target, rule := affectWakeHarness(t)
	source, err := tasks.Latest(t.Context(), rule.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Deliver(t.Context(), RunResult{Run: source, Answer: source.Output}); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	if err = b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "100"}); err != nil {
		t.Fatal(err)
	}
	cfg := tasks.Config()
	cfg.Delivery.GenerationEnabled[affectExtensionID] = false
	cfg.Extensions.Settings[affectExtensionID]["analysis_enabled"] = true
	if err = WriteConfig(tasks.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	tasks.core.setConfig(cfg)
	tasks.core.extensions.configure(cfg)
	control, _ := tasks.Control()
	if err = control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	analyses := 0
	withConfirmedMemory := false
	tasks.core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.SupplementalKey != "" {
			analyses++
			if analyses == 1 && !strings.Contains(string(mustJSON(r.Messages)), "delivered_text") {
				t.Error("actual linked delivery missing")
			}
			return ModelResponse{Content: `{"appraisal":{"kind":"feedback","confidence":0.9,"reason":"Explicitly linked reaction"},"emotion_candidates":[],"behavior_hint":"Acknowledge the specific response","persistent_candidates":{},"feedback_candidate":{"feedback_input_id":"ignored-untrusted-id","evidence_level":"L2","summary":"User explicitly reports the explanation helped","proposal":{"relationship_delta":{"trust":0.04},"memory_candidates":[{"summary":"A reviewed preference for concrete explanations","salience":0.8,"confidence":0.9}]},"accept_memory_candidates":true}}`}, nil
		}
		withConfirmedMemory = withConfirmedMemory || strings.Contains(string(mustJSON(r.Messages)), "A reviewed preference for concrete explanations")
		return ModelResponse{Content: "ordinary reply"}, nil
	})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "2", ReplyTo: "100", Purpose: "request"}, "That specific explanation helped.")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	raw, err := tasks.ExtensionState(t.Context(), affectExtensionID, rule.ConversationID, "maya", "telegram:user:7")
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		State struct {
			Revision     int64 `json:"revision"`
			Relationship struct {
				Trust float64 `json:"trust"`
			} `json:"relationship"`
		} `json:"state"`
		Analyses []struct {
			Input struct {
				ID     string `json:"id"`
				Source string `json:"feedback_source"`
			} `json:"input"`
			Outcome json.RawMessage `json:"feedback_draft"`
		} `json:"analyses"`
	}
	if json.Unmarshal(raw, &read) != nil || len(read.Analyses) == 0 || len(read.Analyses[0].Outcome) == 0 || read.State.Relationship.Trust != .5 {
		t.Fatal("missing draft or implicit learning", string(raw))
	}
	draft := read.Analyses[0]
	args := map[string]any{"id": affectExtensionID, "scope": map[string]string{"resource": "maya", "actor_id": "telegram:user:7"}, "action": "record_outcome", "expected_revision": read.State.Revision, "payload": map[string]any{"source_id": draft.Input.Source, "outcome": draft.Outcome}}
	// A None channel cannot persist state by itself. The local Owner explicitly
	// reviews the model-generated draft through the existing settings protocol.
	if _, e := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlChangeExtensionState, Arguments: mustJSON(args), Authorization: ControlModelProposed, ConversationID: rule.ConversationID}); e == nil {
		t.Fatal("None channel proposed a persistent change")
	}
	chatScope, _ := tasks.core.Store.GetConversation(t.Context(), rule.ConversationID)
	args["scope"].(map[string]string)["space_id"] = chatScope.SpaceID
	proposal, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlChangeExtensionState, Arguments: mustJSON(args), Authorization: ControlUserDirect})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := control.IssueInteractiveCapability(t.Context(), "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := control.RenderConfirmation(t.Context(), proposal.ID, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := control.ConfirmControlChange(t.Context(), proposal.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: "", Principal: "fixture", Capability: cap, Code: confirm.Code})
	if err != nil || !applied.Applied {
		t.Fatal(applied, err)
	}
	if err = tasks.core.extensions.restart(affectExtensionID); err != nil {
		t.Fatal(err)
	}
	client, err := tasks.core.extensions.client(t.Context(), affectExtensionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.core.verifyExtensionBinding(t.Context(), affectExtensionID, client); err != nil {
		t.Fatal("restart source/feedback binding", err)
	}
	input, err = tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "3", Purpose: "request"}, "continue normally")
	if err != nil {
		t.Fatal(err)
	}
	if result, err = tasks.SubmitChannel(t.Context(), input); err != nil || result.Run.Status != "completed" {
		t.Fatal(err)
	}
	if analyses != 2 || !withConfirmedMemory {
		t.Fatal("confirmed memory not used on next input", analyses, withConfirmedMemory)
	}
}
