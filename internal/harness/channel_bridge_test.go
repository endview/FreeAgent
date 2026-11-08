package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func bridgeFixture(t *testing.T) *ChannelBridge {
	t.Helper()
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "optional transport", BaseURL: "http://127.0.0.1:1", Protocol: "lark", Source: "saved", Secret: []byte("FIXTURE_ONLY_SECRET")})
	if err != nil {
		t.Fatal(err)
	}
	core.Config.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "one", Source: "lark", ApplicationID: "app:tenant", Credential: key.ID, Enabled: true}}}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestChannelBridgeOutboxEligibilityLateSuccessAndReceiptIdempotency(t *testing.T) {
	b := bridgeFixture(t)
	ctx := t.Context()
	target := channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "chat"}
	for n := 0; n < 12; n++ {
		op := channelwire.Operation{ID: fmt.Sprintf("old-%02d", n), Connection: "one", Target: target, Kind: "text", Text: "old", State: "unknown", Attempts: 3}
		_, err := b.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, channelOperationPrefix+"one/"+op.ID, string(mustJSON(op)))
		if err != nil {
			t.Fatal(err)
		}
	}
	op, err := b.perform(ctx, "one", channelwire.Operation{Target: target, Kind: "text", Text: "new reply"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	first, err := b.Outbox(ctx, "one")
	if err != nil || len(first) != 1 || first[0].ID != op.ID {
		t.Fatal("old exhausted deliveries starved new output", first, err)
	}
	if err := b.Receipt(ctx, "one", channelwire.Receipt{ID: op.ID, Dispatch: first[0].Dispatch, State: "unknown", RetryAfterMS: 1}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	second, err := b.Outbox(ctx, "one")
	if err != nil || len(second) != 0 {
		t.Fatal("UNKNOWN delivery was sent again", second, err)
	}
	r := channelwire.Receipt{ID: op.ID, Dispatch: first[0].Dispatch, State: "sent", MessageID: "late-real-message"}
	if err := b.Receipt(ctx, "one", r); err != nil {
		t.Fatal("lost late success", err)
	}
	if err := b.Receipt(ctx, "one", r); err != nil {
		t.Fatal("non-idempotent receipt", err)
	}
	if err := b.Receipt(ctx, "one", channelwire.Receipt{ID: op.ID, Dispatch: first[0].Dispatch, State: "failed"}); err == nil {
		t.Fatal("contradictory failure replaced an observed success")
	}
	var raw string
	if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"one/"+op.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved channelwire.Operation
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "sent" || saved.MessageID != "late-real-message" || len(saved.Dispatches) != 1 || saved.Dispatches[0].State != "sent" {
		t.Fatal(raw)
	}
	if next, err := b.Outbox(ctx, "one"); err != nil || len(next) != 0 {
		t.Fatal("replayed sent delivery", next, err)
	}
}

func TestChannelCredentialNamespacesRejectCrossUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	model := NewKeyStore(path)
	channel := NewChannelKeyStore(filepath.Join(t.TempDir(), "channel-keys.json"))
	if _, err := model.Add(KeyInput{Name: "wrong", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: "saved", Secret: []byte("FIXTURE_TOKEN")}); err == nil {
		t.Fatal("model namespace accepted Bot")
	}
	if _, err := channel.Add(KeyInput{Name: "wrong", BaseURL: "http://127.0.0.1:1", Source: "none"}); err == nil {
		t.Fatal("channel namespace accepted model")
	}
	_, err := channel.Add(KeyInput{Name: "Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: "saved", Secret: []byte("FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	wrong := NewKeyStore(channel.Path())
	if _, err := wrong.List(); !errors.Is(err, ErrKeysFormat) {
		t.Fatal("read leaked a different namespace", err)
	}
}

func TestChannelBridgeLocalOwnerReplyUsesSpaceWithoutImpersonation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	ctx := t.Context()
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: "saved", Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	core.Config.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}}}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "generation", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	input, err := tasks.ReceiveChannelText(ctx, ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "seed", Purpose: "background"}, "history")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(ctx, input.ConversationID, "local-owner", "continue in GUI")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.Permission.PersonID != "" {
		t.Fatal("invented a Telegram requester")
	}
	if err := b.Deliver(ctx, result); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(ctx, "tg")
	if err != nil || len(ops) != 1 || !ops[0].LocalOwner || ops[0].Person != "" {
		t.Fatal(ops, err)
	}
	if err := b.Receipt(ctx, "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "42"}); err != nil {
		t.Fatal(err)
	}
	messages, err := tasks.Messages(ctx, input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	public := false
	for _, m := range messages {
		if m.Public && m.Channel != nil && m.Channel.Purpose == "delivery" {
			public = true
		}
	}
	if !public {
		t.Fatal("confirmed GUI continuation was not published to its space")
	}
}
