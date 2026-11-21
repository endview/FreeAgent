package harness

import (
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

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
