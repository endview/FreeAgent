package chatui

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"testing"
	"time"
)

func TestOwnerTwoConversationsPreserveStreamsScopedStopAndHandoff(t *testing.T) {
	entered := make(chan string, 2)
	channels := map[string]chan struct{}{"A": make(chan struct{}), "B": make(chan struct{})}
	ui := setupUI(t, fixtureModel(func(ctx context.Context, r harness.ModelRequest, emit func(string)) (harness.ModelResponse, error) {
		text := r.Messages[len(r.Messages)-1].Content
		emit(text)
		entered <- text
		select {
		case <-channels[text]:
			return harness.ModelResponse{Content: text + " done"}, nil
		case <-ctx.Done():
			return harness.ModelResponse{}, ctx.Err()
		}
	}))
	a, b, c := ui.create(t), ui.create(t), ui.create(t)
	for _, item := range []struct{ chat, text string }{{a, "A"}, {b, "B"}} {
		code, raw := ui.request(t, "POST", "/api/chats/"+item.chat+"/submit", map[string]string{"prompt": item.text, "request_key": item.text})
		if code != 202 {
			t.Fatal(code, string(raw))
		}
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("no parallel model")
		}
	}
	for _, chat := range []string{a, b, c} {
		code, raw := ui.request(t, "GET", "/api/chats/"+chat, nil)
		var p struct {
			Active  []string `json:"active_chats"`
			Streams []Stream `json:"streams"`
			Busy    bool     `json:"busy"`
			Full    bool     `json:"running_elsewhere"`
		}
		if code != 200 || json.Unmarshal(raw, &p) != nil || len(p.Active) != 2 || chat == c && !p.Full || chat != c && (!p.Busy || len(p.Streams) != 1) {
			t.Fatalf("chat=%s state=%s", chat, raw)
		}
	}
	if code, _ := ui.request(t, "POST", "/api/chats/"+c+"/submit", map[string]string{"prompt": "third", "request_key": "third"}); code != 409 {
		t.Fatal("third foreground accepted", code)
	}
	code, raw := ui.request(t, "POST", "/api/attach/cancel", map[string]string{"conversation_id": a, "binding": "wrong"})
	if code != 200 || !containsJSONFalse(raw, "requested") {
		t.Fatal(code, string(raw))
	}
	code, _ = ui.request(t, "POST", "/api/chats/"+a+"/stop", struct{}{})
	if code != 200 {
		t.Fatal(code)
	}
	ui.settled(t, a)
	code, raw = ui.request(t, "GET", "/api/chats/"+b, nil)
	var active struct {
		Busy bool `json:"busy"`
	}
	json.Unmarshal(raw, &active)
	if code != 200 || !active.Busy {
		t.Fatal("stop A affected B", string(raw))
	}
	code, raw = ui.request(t, "GET", "/api/attach/state", nil)
	var state handoff.AttachState
	if code != 200 || json.Unmarshal(raw, &state) != nil || len(state.ActiveChats) != 1 || state.ActiveBindings[b] != "B" {
		t.Fatal(string(raw))
	}
	code, raw = ui.request(t, "POST", "/api/attach/handoff", map[string]bool{"cancel_active": false})
	if code != 409 {
		t.Fatal("handoff did not require cancellation", code)
	}
	code, raw = ui.request(t, "POST", "/api/attach/handoff", map[string]bool{"cancel_active": true})
	if code != 202 {
		t.Fatal(code, string(raw))
	}
	select {
	case <-ui.server.StopRequested():
	case <-time.After(3 * time.Second):
		t.Fatal("handoff did not drain all operations")
	}
}
func containsJSONFalse(raw []byte, key string) bool {
	var value map[string]bool
	return json.Unmarshal(raw, &value) == nil && !value[key]
}
func TestOwnerResultDeliveryRunsForLocalContinuation(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	chat := ui.create(t)
	delivered := make(chan harness.RunResult, 1)
	ui.server.AddChannelDelivery(func(_ context.Context, r harness.RunResult) error { delivered <- r; return nil })
	code, _ := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"prompt": "hello", "request_key": "delivery"})
	if code != http.StatusAccepted {
		t.Fatal(code)
	}
	select {
	case r := <-delivered:
		if r.Run.ConversationID != chat || r.Answer != "Echo: hello" {
			t.Fatal(r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local result did not route through channel delivery")
	}
}
