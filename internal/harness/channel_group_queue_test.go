package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestChannelReplyGroupRollsBackAllMembersAndEnqueueClaims(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	_, origin, err := tasks.ChannelRun(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := b.connections["tg"].session
	group := "atomic-group"
	parts := []channelwire.TextPart{{Text: "first"}, {Text: "second"}}
	collision := contentHash(mustJSON([]any{"tg", group, 2}))
	key := channelOperationPrefix + "tg/" + collision
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(channelwire.Operation{ID: collision, Connection: "tg", Group: "other-group", State: "cancelled", CreatedAt: nowText()}))); err != nil {
		t.Fatal(err)
	}
	if err := s.queueReplyGroup(t.Context(), result.Run, origin, group, "revision", "fixture", parts, nil, false); err == nil {
		t.Fatal("second member collision should fail the atomic enqueue")
	}
	var count int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE document->>'group'=$1 OR key LIKE $2`, group, "channel/send/"+result.Run.ID+"/%").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed enqueue leaked members/claims", count, err)
	}
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `DELETE FROM fa.meta WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.queueReplyGroup(t.Context(), result.Run, origin, group, "revision", "fixture", parts, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE document->>'group'=$1`, group).Scan(&count); err != nil || count != 2 {
		t.Fatal("complete group not idempotent", count, err)
	}
	for _, text := range []string{"first", "second"} {
		ops, err := b.Outbox(t.Context(), "tg")
		if err != nil || len(ops) != 1 || ops[0].Text != text {
			t.Fatal(ops, err)
		}
		if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: text}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChannelStopCancelsUnsentTailsAndPreventsLateEnqueue(t *testing.T) {
	b, tasks, target := expressionBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: "第一段\n\n第二段\n\n第三段"}, nil
	}))
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	_, origin, err := tasks.ChannelRun(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.connections["tg"].session.queueReplyGroup(t.Context(), result.Run, origin, "fixture-parts", "fixture", "fixture", []channelwire.TextPart{{Text: "第一段"}, {Text: "第二段"}, {Text: "第三段"}}, nil, false); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "10"}); err != nil {
		t.Fatal(err)
	}
	if !tasks.CancelConversation(input.ConversationID) {
		t.Fatal("stop did not acknowledge queued delivery cancellation")
	}
	if err := b.Deliver(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: result.Run.ID, Chat: input.ConversationID, Group: "late-group", Sequence: 1, Total: 1})
	late, err := b.perform(ctx, "tg", channelwire.Operation{Kind: "text", Target: channelwire.Target(target), Text: "must not send"})
	if !isChannelQueued(err) || late.State != "cancelled" {
		t.Fatal(late, err)
	}
	if next, err := b.Outbox(t.Context(), "tg"); err != nil || len(next) != 0 {
		t.Fatal("stop leaked a tail", next, err)
	}
	groups, err := tasks.ChannelDeliveries(t.Context(), input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		for _, p := range g.Parts {
			if p.State != "sent" && p.State != "cancelled" {
				t.Fatal("stopped evidence incorrect", p)
			}
		}
	}
}

func TestChannelAbandonPreventsFutureGroupMembers(t *testing.T) {
	b := bridgeFixture(t)
	chat := conversation(t, b.tasks.core)
	ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: "fixture", Chat: chat, Group: "abandoned", Sequence: 1, Total: 2})
	first, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}, Text: "first"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	groups, err := b.tasks.ChannelDeliveries(t.Context(), chat)
	if err != nil || len(groups) != 1 {
		t.Fatal(groups, err)
	}
	if err := b.tasks.ControlDelivery(t.Context(), chat, DeliveryCommand{Group: groups[0].ID, Digest: groups[0].Digest, Action: "abandon"}); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: "fixture", Chat: chat, Group: "abandoned", Sequence: 2, Total: 2})
	last, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: first.Target, Text: "last"})
	if !isChannelQueued(err) || last.State != "cancelled" {
		t.Fatal(last, err)
	}
	if ops, err := b.Outbox(t.Context(), "one"); err != nil || len(ops) != 0 {
		t.Fatal("abandoned future member dispatched", ops, err)
	}
}

func TestChannelResumedReplyYieldsToAlreadyStartedGroup(t *testing.T) {
	b := bridgeFixture(t)
	target := ChannelTarget{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}
	if _, err := b.tasks.PairChannel(t.Context(), target, "private", "fixture"); err != nil {
		t.Fatal(err)
	}
	input, err := b.tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	chat := input.ConversationID
	queue := func(group string, n, total int) channelwire.Operation {
		ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: result.Run.ID, Chat: chat, Group: group, Sequence: n, Total: total})
		op, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}, Text: group})
		if !isChannelQueued(err) {
			t.Fatal(err)
		}
		return op
	}
	a1 := queue("A", 1, 3)
	a2 := queue("A", 2, 3)
	a3 := queue("A", 3, 3)
	send := func(want channelwire.Operation, state string) {
		t.Helper()
		ops, err := b.Outbox(t.Context(), "one")
		if err != nil || len(ops) != 1 || ops[0].ID != want.ID {
			t.Fatal("wrong active reply", ops, err)
		}
		if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: want.ID, Dispatch: ops[0].Dispatch, State: state, MessageID: map[string]string{"sent": "real", "unknown": ""}[state]}); err != nil {
			t.Fatal(err)
		}
	}
	send(a1, "sent")
	send(a2, "unknown")
	b1 := queue("B", 1, 2)
	b2 := queue("B", 2, 2)
	send(b1, "sent")
	groups, err := b.tasks.ChannelDeliveries(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.ID == "A" {
			if err := b.tasks.ControlDelivery(t.Context(), chat, DeliveryCommand{Group: g.ID, Digest: g.Digest, Action: "resume"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	send(b2, "sent")
	send(a3, "sent")
	var raw string
	if err := b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"one/"+a2.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var uncertain channelwire.Operation
	if json.Unmarshal([]byte(raw), &uncertain) != nil || uncertain.State != "unknown" || uncertain.Attempts != 1 {
		t.Fatal("resume replayed uncertainty")
	}
}

func TestChannelStoppedDispatchCannotRequeueAfterLateRateLimit(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Deliver(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	tasks.CancelConversation(input.ConversationID)
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "failed", RetryAfterMS: 1}); err != nil {
		t.Fatal(err)
	}
	var raw string
	var saved channelwire.Operation
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+ops[0].ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "cancelled" || saved.Attempts != 1 || len(saved.Dispatches) != 1 || saved.Dispatches[0].State != "failed" {
		t.Fatal("late 429 bypassed stop or lost attempt facts", raw)
	}
	if next, err := b.Outbox(t.Context(), "tg"); err != nil || len(next) != 0 {
		t.Fatal(next, err)
	}
}

func TestChannelAbandonedRateLimitRetryDoesNotBlockNewReply(t *testing.T) {
	b := bridgeFixture(t)
	target := ChannelTarget{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}
	if _, err := b.tasks.PairChannel(t.Context(), target, "private", "fixture"); err != nil {
		t.Fatal(err)
	}
	input, err := b.tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, MessageID: "1", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: result.Run.ID, Chat: input.ConversationID, Group: "rate-limited", Sequence: 1, Total: 2})
	_, err = b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target(target), Text: "first"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	ctx = context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Run: result.Run.ID, Chat: input.ConversationID, Group: "rate-limited", Sequence: 2, Total: 2})
	_, err = b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target(target), Text: "second"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "one"}); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "failed", RetryAfterMS: 60000}); err != nil {
		t.Fatal(err)
	}
	groups, err := b.tasks.ChannelDeliveries(t.Context(), input.ConversationID)
	if err != nil || len(groups) != 1 {
		t.Fatal(groups, err)
	}
	if err := b.tasks.ControlDelivery(t.Context(), input.ConversationID, DeliveryCommand{Group: groups[0].ID, Digest: groups[0].Digest, Action: "abandon"}); err != nil {
		t.Fatal(err)
	}
	next := queuePart(t, b, "new-request", 1, 1, "new reply")
	ops, err = b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != next.ID {
		t.Fatal("abandoned retry blocked new reply", ops, err)
	}
}
