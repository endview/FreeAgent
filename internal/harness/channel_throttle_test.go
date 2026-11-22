package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestTelegramRateLimitPersistsAcrossTopicsAndBridgeRestart(t *testing.T) {
	b, tasks, private := expressionBridge(t, EchoModel{})
	ctx := t.Context()
	group := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "-10090"}
	grant := grantTelegramFixture(t, tasks, TelegramAccessChange{Target: group, Kind: "group", Enabled: true, Members: []string{"telegram:user:7"}})
	privateGrant, err := tasks.CheckTelegramAccess(ctx, private, "telegram:user:7")
	if err != nil {
		t.Fatal(err)
	}
	first, second := group, group
	first.ThreadID, second.ThreadID = "11", "12"
	queue := func(target ChannelTarget, revision uint64, group string, seq, total int) channelwire.Operation {
		t.Helper()
		bound := context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: target, Person: "telegram:user:7", Revision: revision})
		op, err := b.perform(bound, "tg", channelwire.Operation{Kind: "text", Text: fmt.Sprint(seq), Target: channelwire.Target(target), Group: group, Sequence: seq, Total: total})
		if !isChannelQueued(err) {
			t.Fatal(err)
		}
		return op
	}
	pull := func(id string) channelwire.Operation {
		t.Helper()
		ops, err := b.Outbox(ctx, "tg")
		if err != nil || len(ops) != 1 || ops[0].ID != id {
			t.Fatal("unexpected dispatch", ops, err)
		}
		return ops[0]
	}
	sent := func(op channelwire.Operation) {
		t.Helper()
		if err := b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "sent", MessageID: fmt.Sprint(op.Sequence + 10)}); err != nil {
			t.Fatal(err)
		}
	}
	part1 := queue(first, grant.Revision, "segmented", 1, 3)
	part2 := queue(first, grant.Revision, "segmented", 2, 3)
	part3 := queue(first, grant.Revision, "segmented", 3, 3)
	sent(pull(part1.ID))
	claim := pull(part2.ID)
	competitor := queue(second, grant.Revision, "other-topic", 1, 1)
	if ops, err := b.Outbox(ctx, "tg"); err != nil || len(ops) != 0 {
		t.Fatal("same chat dispatched before the prior platform result", ops, err)
	}
	const retryMS = 3601 * 1000
	before := time.Now().UnixMilli()
	retry := channelwire.Receipt{ID: claim.ID, Dispatch: claim.Dispatch, State: "failed", RetryAfterMS: retryMS}
	if err := b.Receipt(ctx, "tg", retry); err != nil {
		t.Fatal(err)
	}
	until, err := tasks.channelCooldown(ctx, second)
	if err != nil || until < before+retryMS || until > time.Now().UnixMilli()+retryMS {
		t.Fatal("cooldown rounded down or scoped to one topic", until, err)
	}
	if err := b.Receipt(ctx, "tg", retry); err != nil {
		t.Fatal(err)
	}
	if after, err := tasks.channelCooldown(ctx, first); err != nil || after != until {
		t.Fatal("duplicate receipt shifted cooldown", after, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = newTestChannelBridge(tasks, "http://127.0.0.1:1", "restart", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	independent := queue(private, privateGrant.Revision, "different-chat", 1, 1)
	sent(pull(independent.ID))
	if ops, err := b.Outbox(ctx, "tg"); err != nil || len(ops) != 0 {
		t.Fatal("restarted bridge skipped a saved cooldown", ops, err)
	}
	// Advance only the fixture's persisted deadlines, not a live clock.
	db := tasks.core.Store.db
	if _, err = db.ExecContext(ctx, `UPDATE fa.meta SET value='{"until_ms":0}' WHERE key=$1`, channelThrottleKey(channelwire.Target(group))); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{next_dispatch_ms}','0')::text WHERE key=$1`, channelOperationPrefix+"tg/"+part2.ID); err != nil {
		t.Fatal(err)
	}
	resumed := pull(part2.ID)
	if resumed.Dispatch == claim.Dispatch || resumed.Attempts != 2 {
		t.Fatal("certain rejection did not create a new claim", resumed)
	}
	sent(resumed)
	sent(pull(part3.ID))
	sent(pull(competitor.ID))
}

func TestTelegramRateLimitBeyondAuthorizationDoesNotResend(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	ctx := t.Context()
	grant, err := tasks.CheckTelegramAccess(ctx, target, "telegram:user:7")
	if err != nil {
		t.Fatal(err)
	}
	bound := context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: target, Person: "telegram:user:7", Revision: grant.Revision})
	_, err = b.perform(bound, "tg", channelwire.Operation{Kind: "text", Text: "expiring card", Target: channelwire.Target(target), ExpiresMS: time.Now().Add(time.Minute).UnixMilli()})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	ops, err := b.Outbox(ctx, "tg")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	op := ops[0]
	if err = b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "failed", RetryAfterMS: 3601000}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+op.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(raw), &op) != nil || op.State != "failed" || op.SuppressedReason != "rate_limit_exceeds_authorization" || op.Attempts != 1 {
		t.Fatal(raw)
	}
}
