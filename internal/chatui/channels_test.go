package chatui

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTelegramAccessRequiresInteractiveSurfaceAndUsesOwnerLifetime(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	key, err := ui.tasks.ChannelKeys().Add(harness.KeyInput{Name: "fixture bot", BaseURL: "https://api.telegram.org", Protocol: harness.KeyProtocolTelegram, Source: "saved", Secret: []byte("12345678:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	connection := channelwire.Connection{ID: "telegram-test", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}
	code, raw := ui.request(t, "PATCH", "/api/channels", map[string]any{"connections": []channelwire.Connection{connection}})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	bridge := ui.server.channelBridge
	if bridge == nil {
		t.Fatal("configured connection did not create a Core bridge")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ops, err := bridge.Outbox(ctx, "telegram-test")
				if err != nil {
					return
				}
				for _, op := range ops {
					_ = bridge.Receipt(ctx, "telegram-test", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "sent", MessageID: "42"})
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	request, err := http.NewRequestWithContext(t.Context(), "GET", ui.http.URL+"/api/attach/channels", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-FreeAgent-Attach", ui.server.attachToken)
	response, err := ui.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || strings.Contains(string(body), "pair_code") {
		t.Fatalf("observer exposed pairing capability: %s", body)
	}
	code, raw = ui.request(t, "GET", "/api/channels", nil)
	if code != 200 || strings.Contains(string(raw), "pair_code") {
		t.Fatalf("Telegram status retained obsolete pairing: %s", raw)
	}
	code, raw = ui.request(t, "POST", "/api/channels/telegram", map[string]any{})
	if code != 410 {
		t.Fatal("obsolete TG pairing remained", code, string(raw))
	}
	if err := bridge.Inbound(t.Context(), "telegram-test", channelwire.Event{ID: "1", Target: channelwire.Target{Source: "telegram", ApplicationID: "1", ChatID: "90"}, Person: "telegram:user:7", Name: "Applicant", Message: "1", Kind: "private", Text: "/start payload-cannot-authorize", Trigger: true}); err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, "GET", "/api/channels/telegram/access", nil)
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	var state struct {
		Requests []harness.TelegramAccessRequest `json:"requests"`
	}
	if json.Unmarshal(raw, &state) != nil || len(state.Requests) != 1 {
		t.Fatal("request list absent", string(raw))
	}
	code, raw = ui.request(t, "POST", "/api/channels/telegram/access", map[string]any{"request_id": state.Requests[0].ID, "enabled": true})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	if _, err := ui.tasks.CheckTelegramAccess(t.Context(), state.Requests[0].Target, "telegram:user:7"); err != nil {
		t.Fatal("approved policy missing", err)
	}
	if len(ui.tasks.TelegramAccessRequests()) != 0 {
		t.Fatal("decided request remained pending")
	}

}
