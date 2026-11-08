package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
)

func queuePart(t *testing.T, b *ChannelBridge, group string, sequence, total int, text string) channelwire.Operation {
	t.Helper()
	ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Group: group, Sequence: sequence, Total: total})
	op, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}, Text: text})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	return op
}

func TestChannelDeliverySequenceAndUnknownPauseOnlyOriginalReply(t *testing.T) {
	b := bridgeFixture(t)
	// Enqueue out of order to prove that timestamps and hashes cannot decide
	// the order of parts belonging to the same logical reply.
	second := queuePart(t, b, "original", 2, 2, "second")
	first := queuePart(t, b, "original", 1, 2, "first")
	other := queuePart(t, b, "next-request", 1, 1, "new reply")
	ops, err := b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != first.ID {
		t.Fatal("wrong first part", ops, err)
	}
	if next, err := b.Outbox(t.Context(), "one"); err != nil || len(next) != 0 {
		t.Fatal("interleaved before original receipt", next, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: first.ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	next, err := b.Outbox(t.Context(), "one")
	if err != nil || len(next) != 1 || next[0].ID != other.ID {
		t.Fatal("UNKNOWN blocked new conversation or replayed original", next, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: other.ID, Dispatch: next[0].Dispatch, State: "sent", MessageID: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: first.ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "late"}); err != nil {
		t.Fatal(err)
	}
	if next, err := b.Outbox(t.Context(), "one"); err != nil || len(next) != 0 {
		t.Fatal("old tail resumed without user action", next, err)
	}
	var raw string
	if err := b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"one/"+second.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved channelwire.Operation
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "paused" {
		t.Fatal(raw)
	}
}

func TestChannelDeliveryWaitsForFrozenWholeGroupAndExplicitlyResumesTail(t *testing.T) {
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
	ctx := context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Group: "frozen", Sequence: 1, Total: 2, Chat: chat, Run: result.Run.ID})
	first, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}, Text: "first"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	if ops, err := b.Outbox(t.Context(), "one"); err != nil || len(ops) != 0 {
		t.Fatal("incomplete group dispatched", ops, err)
	}
	ctx = context.WithValue(t.Context(), channelDeliveryKey{}, channelDeliveryInfo{Group: "frozen", Sequence: 2, Total: 2, Chat: chat, Run: result.Run.ID})
	second, err := b.perform(ctx, "one", channelwire.Operation{Kind: "text", Target: first.Target, Text: "second"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != first.ID {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: first.ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	groups, err := b.tasks.ChannelDeliveries(t.Context(), chat)
	if err != nil || len(groups) != 1 {
		t.Fatal(groups, err)
	}
	command := DeliveryCommand{Group: groups[0].ID, Digest: groups[0].Digest, Action: "resume"}
	if err := b.tasks.ControlDelivery(t.Context(), chat, command); err != nil {
		t.Fatal(err)
	}
	if err := b.tasks.ControlDelivery(t.Context(), chat, command); err != nil {
		t.Fatal("lost response not idempotent", err)
	}
	ops, err = b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != second.ID {
		t.Fatal("resume retried UNKNOWN or lost unsent tail", ops, err)
	}
	var raw string
	if err := b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"one/"+first.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved channelwire.Operation
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "unknown" || !saved.ReviewedUncertainty || saved.Attempts != 1 {
		t.Fatal("unknown evidence was overwritten", raw)
	}
}

func TestChannelDeliverySentPredecessorAndExpiredDispatch(t *testing.T) {
	b := bridgeFixture(t)
	first := queuePart(t, b, "reply", 1, 2, "one")
	second := queuePart(t, b, "reply", 2, 2, "two")
	ops, err := b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != first.ID {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "one", channelwire.Receipt{ID: first.ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "1"}); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "one")
	if err != nil || len(ops) != 1 || ops[0].ID != second.ID {
		t.Fatal("next part not released", ops, err)
	}
	op := ops[0]
	op.NextDispatchMS = time.Now().Add(-time.Second).UnixMilli()
	if _, err := b.tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), channelOperationPrefix+"one/"+op.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := b.Outbox(t.Context(), "one"); err != nil || len(next) != 0 {
		t.Fatal("expired dispatch was replayed", next, err)
	}
	var raw string
	_ = b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"one/"+op.ID).Scan(&raw)
	if !strings.Contains(raw, `"state":"unknown"`) {
		t.Fatal("expired dispatch not preserved as UNKNOWN", raw)
	}
}

func TestChannelRenderedReplyRemainsFrozenAfterSettingsChange(t *testing.T) {
	b := bridgeFixture(t)
	s := b.connections["one"].session
	s.renderText = func(text string) ([]channelwire.TextPart, error) { return channels.RenderTelegramText(text, false) }
	s.formatVersion = channels.TelegramFormatVersion
	first, format, err := s.freezeReplyText(t.Context(), "fixed", "**hello**")
	if err != nil || len(first) != 1 || first[0].Text != "hello" || len(first[0].Entities) != 1 {
		t.Fatal(first, err)
	}
	s.renderText = func(text string) ([]channelwire.TextPart, error) {
		t.Fatal("frozen reply was recomputed")
		return nil, nil
	}
	again, after, err := s.freezeReplyText(t.Context(), "fixed", "**hello**")
	if err != nil || string(mustJSON(first)) != string(mustJSON(again)) || format != after {
		t.Fatal(again, after, err)
	}
}

func TestChannelDeliveriesPaginateCompleteGroupsAndBindCursor(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	_, err := core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value)
 SELECT 'channel/operation/fixture/'||g::text||'/'||p::text,
 jsonb_build_object('id',g::text||'-'||p::text,'group','group-'||lpad(g::text,3,'0'),
 'conversation_id',$1::text,'run_id','fixture','kind','text','sequence',p,'total',48,
 'state',CASE WHEN g=0 AND p=1 THEN 'unknown' ELSE 'sent' END,
 'created_at',to_char('2026-11-07T00:00:00Z'::timestamptz+g*interval '1 second','YYYY-MM-DD"T"HH24:MI:SS"Z"'))::text
 FROM generate_series(0,54) g CROSS JOIN generate_series(1,48) p`, chat)
	if err != nil {
		t.Fatal(err)
	}
	first, err := tasks.ChannelDeliveries(t.Context(), chat)
	if err != nil || len(first) != 50 || first[0].ID != "group-054" || first[49].ID != "group-005" {
		t.Fatal(len(first), err)
	}
	for _, group := range first {
		if len(group.Parts) != 48 || group.Parts[0].Sequence != 1 || group.Parts[47].Sequence != 48 {
			t.Fatal("a group was clipped by a row limit", group.ID, len(group.Parts))
		}
	}
	second, err := tasks.ChannelDeliveries(t.Context(), chat, first[49].Cursor)
	if err != nil || len(second) != 5 || second[4].ID != "group-000" || second[4].Status != "partial" {
		t.Fatal(second, err)
	}
	empty, err := tasks.ChannelDeliveries(t.Context(), chat, second[4].Cursor)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	other := conversation(t, core)
	if _, err := tasks.ChannelDeliveries(t.Context(), other, first[49].Cursor); err == nil {
		t.Fatal("cursor escaped its conversation")
	}
}
