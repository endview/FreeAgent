package harness

import (
	"context"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestChannelPreparationRestartsFreezesThenNeverReplaysUnknownSend(t *testing.T) {
	b, tasks, target := expressionBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: "**first**\n\nsecond"}, nil
	}))
	b.preparer = nil // Exercise the real durable preparation path.
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
	if err != nil || len(ops) != 1 || ops[0].Kind != "prepare_text" {
		t.Fatal(ops, err)
	}
	op := ops[0]
	if op.FormatVersion == "" || op.SourceHash != contentHash([]byte(op.Text)) {
		t.Fatal("preparation source not bound", op)
	}
	prepared := channelwire.Prepared{SourceHash: op.SourceHash, Format: op.FormatVersion, Parts: []channelwire.TextPart{{Text: "first", Entities: []channelwire.TextEntity{{Type: "bold", Length: 5}}}, {Text: "second"}}}
	bad := prepared
	bad.SourceHash = "changed"
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "downloaded", Data: mustJSON(bad)}); err == nil {
		t.Fatal("stale source accepted")
	}
	bad = prepared
	bad.Parts = []channelwire.TextPart{{Text: "x", Entities: []channelwire.TextEntity{{Offset: 2, Length: 1}}}}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "downloaded", Data: mustJSON(bad)}); err == nil {
		t.Fatal("invalid entity accepted")
	}
	// Losing pure preparation does not create an uncertain platform send.
	op.NextDispatchMS = time.Now().Add(-time.Second).UnixMilli()
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), channelOperationPrefix+"tg/"+op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Outbox(t.Context(), "tg"); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "prepare_text" || ops[0].ID != op.ID {
		t.Fatal("pure preparation did not retry same source", ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "downloaded", Data: mustJSON(prepared)}); err != nil {
		t.Fatal(err)
	}
	// Recreating the bridge simulates an Owner restart. Its old token is revoked;
	// persisted parts and source identity remain, and the plugin re-registers.
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = NewChannelBridge(tasks, "http://127.0.0.1:1", "next-generation", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if err := b.RegisterCapabilities("tg", channelwire.Capabilities{Version: channelwire.Version, Format: prepared.Format, Actions: []string{"prepare_text", "text"}}); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Kind != "text" || ops[0].Text != "first" || len(ops[0].Entities) != 1 {
		t.Fatal("not frozen after restart", ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "10"}); err != nil {
		t.Fatal(err)
	}
	ops, err = b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].Text != "second" {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Deliver(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	if next, err := b.Outbox(t.Context(), "tg"); err != nil || len(next) != 0 {
		t.Fatal("unknown send replayed", next, err)
	}
	groups, err := tasks.ChannelDeliveries(t.Context(), input.ConversationID)
	if err != nil || len(groups) != 1 || groups[0].Status != "partial" {
		t.Fatal("preparation confused with sent evidence", groups, err)
	}
}

func TestChannelPreparationCancelAndCapabilityRevision(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	b.preparer = nil
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
	if !tasks.CancelConversation(input.ConversationID) {
		t.Fatal("stop not recorded")
	}
	prepared := channelwire.Prepared{SourceHash: ops[0].SourceHash, Format: ops[0].FormatVersion, Parts: []channelwire.TextPart{{Text: "late content"}}}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "downloaded", Data: mustJSON(prepared)}); err != nil {
		t.Fatal(err)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("late prepare revived cancelled send", ops, err)
	}
	if err := b.RegisterCapabilities("tg", channelwire.Capabilities{Version: channelwire.Version, Format: "changed", Actions: []string{"prepare_text", "text"}}); err == nil {
		t.Fatal("live format changed within generation")
	}
	if err := b.RegisterCapabilities("tg", channelwire.Capabilities{Version: 2, Format: "old", Actions: []string{"prepare_text", "text"}}); err == nil {
		t.Fatal("old wire accepted")
	}
	var n int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'kind'='text' AND document->>'run_id'=$1`, result.Run.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("cancelled preparation froze sends", n, err)
	}
}
