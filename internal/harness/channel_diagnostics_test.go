package harness

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestTelegramRouteRefusalRetainsExactRedactedReceiptAcrossRestart(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: "saved", Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	core.Config.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}}}
	start := func(generation string) *ChannelBridge {
		t.Helper()
		bridge, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", generation, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bridge.Close() })
		return bridge
	}
	ctx := t.Context()
	b := start("original")
	bad := channelwire.Event{ID: "7", Target: channelwire.Target{Source: "telegram", ApplicationID: "1"}, Person: "telegram:user:7", Text: "do not journal this payload", Kind: "group", Trigger: true}
	if err := b.Inbound(ctx, "tg", bad); err != nil {
		t.Fatal(err)
	}
	if disposition, err := b.InputReceipt(ctx, "tg", bad.ID); err != nil || disposition != "rejected" {
		t.Fatal(disposition, err)
	}
	var before string
	if err := core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelInboundKey("tg", bad.ID)).Scan(&before); err != nil || strings.Contains(before, bad.Text) || strings.Contains(before, bad.Person) {
		t.Fatal("invalid route receipt retained input", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := start("resumed")
	if err := resumed.Inbound(ctx, "tg", bad); err != nil {
		t.Fatal("same update should use the saved receipt", err)
	}
	conflict := bad
	conflict.Text = "a different event using the same ID"
	if err := resumed.Inbound(ctx, "tg", conflict); !errors.Is(err, ErrRequestKey) {
		t.Fatal("conflicting update was acknowledged", err)
	}
	var after string
	if err := core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelInboundKey("tg", bad.ID)).Scan(&after); err != nil || before != after {
		t.Fatal("original receipt was changed", err)
	}
}

func TestGroupBackgroundDiscardedBeforeInboundPersistence(t *testing.T) {
	b := bridgeFixture(t)
	target := channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "group"}
	if _, err := b.tasks.PairChannel(t.Context(), ChannelTarget(target), "group", "fixture"); err != nil {
		t.Fatal(err)
	}
	const secret = "BACKGROUND_MUST_NOT_BE_RETAINED"
	e := channelwire.Event{ID: "incoming", Target: target, Kind: "group", Person: "lark:user:member", Message: "message", Text: secret}
	if err := b.Inbound(t.Context(), "one", e); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE value LIKE $1`, "%"+secret+"%").Scan(&count); err != nil || count != 0 {
		t.Fatalf("background persisted in intent: %d %v", count, err)
	}
	if err := b.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE body_json::text LIKE $1`, "%"+secret+"%").Scan(&count); err != nil || count != 0 {
		t.Fatalf("background persisted in history: %d %v", count, err)
	}
	status := string(mustJSON(b.Status("lark")))
	if !strings.Contains(status, "background_ignored") || strings.Contains(status, secret) {
		t.Fatal(status)
	}
}

func TestTelegramApplicationRefusalIsHandledNotTransportFailure(t *testing.T) {
	r, _, store, _, sent := telegramRuntimeFixture(t, EchoModel{})
	store.telegramIdentity("1")
	for n := 0; n < telegramRequestCapacity; n++ {
		store.telegramRequests[string(rune('a'+n))] = TelegramAccessRequest{ExpiresAt: time.Now().Add(time.Hour)}
	}
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(1, 1, 90, 7, "/start", "private")); err != nil {
		t.Fatalf("classified refusal would stall pull cursor: %v", err)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Text, "待处理申请已满") {
		t.Fatal("refusal was not explained")
	}
}

func TestChannelObservationScopeBoundsAndGroupIsolation(t *testing.T) {
	b := bridgeFixture(t)
	for _, o := range []channelwire.Observation{
		{Stage: "poll_ok", Count: 101}, {Stage: "secret text", Count: 1},
		{Stage: channelwire.ObservationInboundRetry, Count: 1},
		{Stage: "received", Count: 1, Target: channelwire.Target{Source: "lark", ApplicationID: "other", ChatID: "group"}},
	} {
		if b.Observe("one", o) == nil {
			t.Fatal("invalid diagnostic accepted")
		}
	}
	if err := b.Observe("one", channelwire.Observation{Stage: "received", Count: 1, Target: channelwire.Target{Source: "lark", ApplicationID: "app:tenant", ChatID: "only-this-group"}}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	d := b.connections["one"].diagnostic
	if len(d.Groups) != 1 || d.Groups["only-this-group"]["received"] != 1 {
		t.Fatal(d)
	}
	b.mu.Unlock()
}
