package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func expressionBridge(t *testing.T, model ModelClient) (*ChannelBridge, *TaskService, ChannelTarget) {
	core, _, _ := testHarness(t, model)
	tasks := NewTaskService(core)
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: KeySourceSaved, Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	core.Config.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true, ReplyStickers: true, AutonomousReactions: true}}}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	return b, tasks, target
}

func confirmStickerSets(t *testing.T, tasks *TaskService, chat string, revision uint64, names []string) StickerSets {
	t.Helper()
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetStickerSets, Authorization: ControlUserDirect, ConversationID: chat, Arguments: mustJSON(stickerSetsArguments{chat, revision, names})})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), op.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: chat, Principal: "fixture", Code: confirmation.Code, Capability: capability})
	if err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	var sets StickerSets
	if err := json.Unmarshal(result.Data, &sets); err != nil {
		t.Fatal(err)
	}
	return sets
}

func fixtureStickerCatalog(t *testing.T, b *ChannelBridge, tasks *TaskService, chat string, saved bool) StickerCatalog {
	t.Helper()
	type result struct {
		value any
		err   error
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		value, err := tasks.ChannelStickerCommand(ctx, chat, StickerCommand{Action: "list", Set: "fixture", Saved: saved})
		done <- result{value, err}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatal(result.err)
			}
			return result.value.(StickerCatalog)
		case <-ctx.Done():
			t.Fatal("sticker lookup did not finish")
		case <-ticker.C:
			ops, err := b.Outbox(ctx, "tg")
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range ops {
				if op.Kind != "sticker_set" {
					t.Fatal("unexpected operation", op.Kind)
				}
				page := channelwire.StickerPage{Name: "fixture", Title: "Fixture", Offset: op.PageOffset, Total: 1, Stickers: []channelwire.Sticker{{FileID: "known-file", UniqueID: "unique", SetName: "fixture", Format: "webp", Width: 64, Height: 64}}}
				if err := b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "downloaded", Data: mustJSON(page)}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestChannelExpressionStickerCollectionConfirmationScopeAndRevocation(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	chat := space.ActiveConversationID
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	args := stickerSetsArguments{chat, 0, []string{"https://t.me/addstickers/Fixture"}}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetStickerSets, Authorization: ControlModelProposed, ConversationID: chat, Arguments: mustJSON(args)})
	if err != nil || !strings.Contains(op.Preview, space.ID) || !strings.Contains(op.Preview, "fixture") {
		t.Fatal(op, err)
	}
	if sets, err := tasks.stickerSets(t.Context(), target, chat); err != nil || len(sets.Sets) != 0 {
		t.Fatal("planning persisted the collection", sets, err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "fixture", "wrong")); !errors.Is(err, ErrControlWrongCode) {
		t.Fatal("accepted wrong confirmation", err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "fixture", confirmation.Code)); err != nil {
		t.Fatal(err)
	}
	sets, err := tasks.stickerSets(t.Context(), target, chat)
	if err != nil || sets.Revision != 1 || len(sets.Sets) != 1 || sets.Sets[0] != "fixture" {
		t.Fatal(sets, err)
	}
	other, err := tasks.NewConversationInSpace(t.Context(), space.ID, "Other conversation")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := tasks.ChannelStickerCommand(t.Context(), other.ID, StickerCommand{Action: "sets"}); err != nil || value.(StickerSets).Revision != 1 {
		t.Fatal("collection was not space scoped", value, err)
	}
	if _, err := tasks.stickerSets(t.Context(), ChannelTarget{Source: "telegram", ApplicationID: "2", ChatID: target.ChatID}, chat); err == nil {
		t.Fatal("collection crossed Bot identity")
	}
	received, err := tasks.rememberSticker(t.Context(), target, channelwire.Sticker{FileID: "known-file", UniqueID: "unique", SetName: "fixture", Format: "webp", Width: 64, Height: 64}, chat)
	if err != nil {
		t.Fatal(err)
	}
	lookup := fixtureStickerCatalog(t, b, tasks, chat, false).Items[0]
	collection := fixtureStickerCatalog(t, b, tasks, chat, true).Items[0]
	if received.ID == lookup.ID || lookup.ID == collection.ID || received.ID == collection.ID {
		t.Fatal("provenance capabilities were merged")
	}
	if _, err := tasks.stickerResource(t.Context(), target, other.ID, collection.ID); err == nil {
		t.Fatal("collection file capability crossed conversation")
	}
	sets = confirmStickerSets(t, tasks, chat, 1, []string{"fixture", "retained"})
	if _, err := tasks.stickerResource(t.Context(), target, chat, collection.ID); err != nil {
		t.Fatal("unrelated addition revoked retained set", err)
	}
	sets = confirmStickerSets(t, tasks, chat, sets.Revision, []string{"retained"})
	if _, err := tasks.stickerResource(t.Context(), target, chat, collection.ID); err == nil {
		t.Fatal("removed collection capability remained valid")
	}
	for _, ref := range []StickerResource{received, lookup} {
		record, err := tasks.stickerResource(t.Context(), target, chat, ref.ID)
		if err != nil {
			t.Fatal("independent received/lookup reference revoked", err)
		}
		expires, err := time.Parse(time.RFC3339Nano, record.Expires)
		if err != nil || time.Until(expires) > 24*time.Hour || time.Until(expires) < 23*time.Hour {
			t.Fatal("reference lost 24h TTL", record)
		}
	}
	cached, err := b.stickerCatalog(t.Context(), target, chat, "", 0, 24)
	if err != nil || cached.Total != 2 || len(cached.Items) != 2 {
		t.Fatal("cached listing retained revoked collection capability", cached, err)
	}
	sets = confirmStickerSets(t, tasks, chat, sets.Revision, []string{"fixture", "retained"})
	if sets.Generations["fixture"] == 1 {
		t.Fatal("re-add reused removed generation")
	}
	if _, err := tasks.stickerResource(t.Context(), target, chat, collection.ID); err == nil {
		t.Fatal("re-add revived revoked capability")
	}
	for _, ref := range []StickerResource{received, lookup} {
		record, _ := tasks.stickerResource(t.Context(), target, chat, ref.ID)
		record.Expires = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
		if _, err := tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(record)), stickerResourceKey(target, chat, ref.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.stickerResource(t.Context(), target, chat, ref.ID); err == nil {
			t.Fatal("expired capability remained usable")
		}
	}
	// Re-read from PostgreSQL through a fresh service: no in-memory policy cache.
	fresh := NewTaskService(&Service{Store: tasks.core.Store, Config: tasks.Config()})
	if persisted, err := fresh.stickerSets(t.Context(), target, other.ID); err != nil || persisted.Revision != sets.Revision {
		t.Fatal("collection was not durable", persisted, err)
	}
}

func TestChannelExpressionStickerCollectionRejectsBoundsStaleAndRevokedGrant(t *testing.T) {
	_, tasks, target := expressionBridge(t, EchoModel{})
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	chat := space.ActiveConversationID
	control, _ := tasks.Control()
	plan := func(revision uint64, names []string) (ControlOperation, error) {
		return control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetStickerSets, Authorization: ControlUserDirect, ConversationID: chat, Arguments: mustJSON(stickerSetsArguments{chat, revision, names})})
	}
	names := []string{}
	for n := 0; n < 33; n++ {
		names = append(names, fmt.Sprintf("set_%d", n))
	}
	if _, err := plan(0, names); err == nil {
		t.Fatal("accepted 33 sets")
	}
	if _, err := plan(0, nil); err == nil {
		t.Fatal("accepted missing explicit set list")
	}
	if _, err := plan(0, []string{"https://evil.example/a"}); err == nil {
		t.Fatal("accepted arbitrary URL")
	}
	old, err := plan(0, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	confirmStickerSets(t, tasks, chat, 0, []string{"current"})
	capability, _ := control.IssueInteractiveCapability(t.Context(), chat, "fixture")
	confirmation, _ := control.RenderConfirmation(t.Context(), old.ID, chat, "fixture")
	if _, err := control.ConfirmControlChange(t.Context(), old.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: chat, Principal: "fixture", Code: confirmation.Code, Capability: capability}); !errors.Is(err, ErrControlStaleRevision) && !errors.Is(err, ErrControlAlreadyUsed) {
		t.Fatal("accepted stale preview", err)
	}
	pending, err := plan(1, []string{"pending"})
	if err != nil {
		t.Fatal(err)
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: false, Members: []string{"telegram:user:7"}})
	confirmation, _ = control.RenderConfirmation(t.Context(), pending.ID, chat, "fixture")
	if _, err := control.ConfirmControlChange(t.Context(), pending.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: chat, Principal: "fixture", Code: confirmation.Code, Capability: capability}); !errors.Is(err, ErrPermissionRevoked) {
		t.Fatal("applied with revoked grant", err)
	}
}

func TestChannelExpressionAlternateReactionRequiresExactConfirmation(t *testing.T) {
	var selected string
	calls := 0
	b, tasks, target := expressionBridge(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("alternate", "reply_expression", map[string]string{"action": "reaction", "emoji": "👍", "message_ref": selected})}}, nil
		}
		return ModelResponse{}, nil
	}))
	previous, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "11", Purpose: "background"}, "visible earlier message")
	if err != nil {
		t.Fatal(err)
	}
	selected = fmt.Sprintf("incoming:%d", previous.MessageID)
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "12", Purpose: "request"}, "react to earlier message")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "waiting_confirmation" || calls != 1 {
		t.Fatal("alternate target bypassed confirmation with autonomy enabled", result.Run.Status, err, calls)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("reaction dispatched before approval", ops, err)
	}
	decision, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil || !strings.Contains(decision.Preview, selected) {
		t.Fatal(decision, err)
	}
	modified := decision
	modified.Digest = "other"
	if _, err := tasks.Confirm(t.Context(), result.Run.ID, modified, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("accepted modified target approval", err)
	}
	if _, err := tasks.Confirm(t.Context(), result.Run.ID, decision, true); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "reaction" || ops[0].Reply != "11" {
		t.Fatal(ops, err)
	}
}

func TestChannelExpressionKnownReactionTargetsRejectUnprovenIDsAndScopes(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "11", Purpose: "background"}, "visible")
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("incoming:%d", input.MessageID)
	for _, wrong := range []string{"11", "incoming:999999", "outbox:unproven"} {
		if _, err := tasks.ChannelStickerCommand(t.Context(), input.ConversationID, StickerCommand{Action: "reaction", MessageRef: wrong, Emoji: "👍", RequestKey: "bad-" + wrong}); err == nil {
			t.Fatal("accepted unproven reaction", wrong)
		}
	}
	if _, err := tasks.reactionTarget(t.Context(), "tg", ChannelTarget{Source: "telegram", ApplicationID: "2", ChatID: "90"}, input.ConversationID, ref); err == nil {
		t.Fatal("crossed Bot")
	}
	if _, err := tasks.reactionTarget(t.Context(), "tg", ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "91"}, input.ConversationID, ref); err == nil {
		t.Fatal("crossed chat")
	}
	other, err := tasks.NewConversationInSpace(t.Context(), (func() string { s, _ := tasks.ChannelSpace(t.Context(), target); return s.ID })(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.reactionTarget(t.Context(), "tg", target, other.ID, ref); err == nil {
		t.Fatal("crossed conversation")
	}
	// This user-direct call works with a known fact even when no Run exists.
	if _, err := tasks.ChannelStickerCommand(t.Context(), input.ConversationID, StickerCommand{Action: "reaction", MessageRef: ref, Emoji: "👍", RequestKey: "explicit"}); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Reply != "11" {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "11"}); err != nil {
		t.Fatal(err)
	}
	op := channelwire.Operation{ID: "confirmed", Connection: "tg", Kind: "text", Target: channelwire.Target(target), ConversationID: input.ConversationID, State: "sent", MessageID: "50"}
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, channelOperationPrefix+"tg/"+op.ID, string(mustJSON(op))); err != nil {
		t.Fatal(err)
	}
	if selected, err := tasks.reactionTarget(t.Context(), "tg", target, input.ConversationID, "outbox:confirmed"); err != nil || selected.MessageID != "50" {
		t.Fatal(selected, err)
	}
	value, err := tasks.ChannelStickerCommand(t.Context(), input.ConversationID, StickerCommand{Action: "targets"})
	if err != nil || len(value.([]ReactionTarget)) != 2 {
		t.Fatal("known target list lost facts or included reaction receipts", value, err)
	}
	op.State = "unknown"
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), channelOperationPrefix+"tg/"+op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.reactionTarget(t.Context(), "tg", target, input.ConversationID, "outbox:confirmed"); err == nil {
		t.Fatal("UNKNOWN delivery authorized reaction")
	}
}

func TestChannelExpressionAnonymousCountsReplaceWithoutIdentityOrRun(t *testing.T) {
	calls := 0
	b, tasks, target := expressionBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{}, nil
	}))
	if _, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "34", Purpose: "background"}, "known"); err != nil {
		t.Fatal(err)
	}
	event := channelwire.Event{ID: "100", Target: channelwire.Target(target), Message: "34", Kind: "private", Reaction: &channelwire.Reaction{Message: "34", Date: 10, Anonymous: true, CountSnapshot: true, Counts: []channelwire.ReactionCount{{Value: "👍", Count: 4}}}}
	for n := 0; n < 2; n++ {
		if err := b.Inbound(t.Context(), "tg", event); err != nil {
			t.Fatal(err)
		}
	}
	event.ID = "101" // Same-second updates still replace exactly.
	event.Reaction.Counts[0].Count = 2
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	event.ID = "100"
	event.Reaction.Counts[0].Count = 9
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	var raw string
	read := func() channelwire.Reaction {
		t.Helper()
		if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key LIKE 'channel/reaction-count/%'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var record struct {
			Reaction channelwire.Reaction `json:"reaction"`
		}
		if json.Unmarshal([]byte(raw), &record) != nil || strings.Contains(raw, `"person"`) {
			t.Fatal("anonymous fact gained identity", raw)
		}
		return record.Reaction
	}
	if saved := read(); len(saved.Counts) != 1 || saved.Counts[0].Count != 2 {
		t.Fatal("duplicate/stale counts accumulated", raw)
	}
	event.ID, event.Reaction.Date, event.Reaction.Counts = "102", 11, []channelwire.ReactionCount{}
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	if saved := read(); !saved.CountSnapshot || len(saved.Counts) != 0 {
		t.Fatal("revoke lost", raw)
	}
	event.ID, event.Message, event.Reaction.Message = "103", "unknown", "unknown"
	if err := b.Inbound(t.Context(), "tg", event); err == nil {
		t.Fatal("accepted malformed target")
	}
	event.Message, event.Reaction.Message = "999", "999"
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	var observations, runs int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/reaction-count/%'),(SELECT count(*) FROM fa.runs)`).Scan(&observations, &runs); err != nil || observations != 1 || runs != 0 || calls != 0 {
		t.Fatal("counts created history or a Run", observations, runs, calls, err)
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: false, Members: []string{"telegram:user:7"}})
	event.ID, event.Message, event.Reaction.Message, event.Reaction.Date = "104", "34", "34", 12
	event.Reaction.Counts = []channelwire.ReactionCount{{Value: "👍", Count: 100}}
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	if saved := read(); saved.Date != 11 || len(saved.Counts) != 0 {
		t.Fatal("disabled chat accepted anonymous observation", raw)
	}
}

func TestChannelExpressionNoneUsesOnlyScopedReplyTool(t *testing.T) {
	var sticker string
	calls := 0
	b, tasks, target := expressionBridge(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if !req.DenyOperations || !replyOnlyDefinitions(req.Tools) {
			t.Fatal("None exposed host or network tools")
		}
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("s1", "reply_expression", map[string]string{"action": "sticker", "resource": sticker})}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "s1" {
			t.Error("safe expression result lost")
		}
		return ModelResponse{Content: "done"}, nil
	}))
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := tasks.rememberSticker(t.Context(), target, channelwire.Sticker{FileID: "known-file", UniqueID: "unique", Format: "webp", Width: 64, Height: 64}, space.ActiveConversationID)
	if err != nil {
		t.Fatal(err)
	}
	sticker = ref.ID
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "use this sticker")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" || calls != 2 {
		t.Fatalf("status=%s calls=%d err=%v", result.Run.Status, calls, err)
	}
	if err := b.Deliver(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "text" {
		t.Fatal("text did not precede sticker", ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "10"}); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "sticker" || ops[0].Target != channelwire.Target(target) || ops[0].Sticker.FileID != "known-file" {
		t.Fatal(ops, err)
	}
	if _, err := tasks.stickerResource(t.Context(), ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "91"}, space.ActiveConversationID, ref.ID); err == nil {
		t.Fatal("resource escaped chat")
	}
	if _, err := tasks.stickerResource(t.Context(), target, "other-conversation", ref.ID); err == nil {
		t.Fatal("resource escaped conversation")
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("sticker UNKNOWN was resent", ops, err)
	}
}

func TestChannelExpressionReactionOnlyTargetsCurrentIncomingMessage(t *testing.T) {
	calls := 0
	b, tasks, target := expressionBridge(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("r1", "reply_expression", map[string]string{"action": "reaction", "emoji": "👍"})}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}))
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "34", Purpose: "request"}, "like this message")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "reaction" || ops[0].Reply != "34" || ops[0].Reaction != "👍" {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "34"}); err != nil {
		t.Fatal(err)
	}
	event := channelwire.Event{ID: "100", Target: channelwire.Target(target), Person: "telegram:user:7", Message: "34", Kind: "private", Reaction: &channelwire.Reaction{Message: "34", Date: 1, Old: []string{}, New: []string{"👍"}}}
	for n := 0; n < 2; n++ {
		if err := b.Inbound(t.Context(), "tg", event); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/reaction/%'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	event.ID = "101"
	event.Reaction.Date = 2
	event.Reaction.New = []string{}
	if err := b.Inbound(t.Context(), "tg", event); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key LIKE 'channel/reaction/%'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Reaction channelwire.Reaction `json:"reaction"`
	}
	if json.Unmarshal([]byte(raw), &saved) != nil || len(saved.Reaction.New) != 0 {
		t.Fatal(raw)
	}
	if calls != 2 {
		t.Fatal("reaction observation triggered a model run")
	}
}
