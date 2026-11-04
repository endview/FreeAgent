package chatui

import (
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/harness"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelegramAccessRequiresInteractiveSurfaceAndUsesOwnerLifetime(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			var message struct {
				ChatID int64 `json:"chat_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&message)
			fmt.Fprintf(w, `{"ok":true,"result":{"message_id":42,"chat":{"id":%d}}}`, message.ChatID)
			return
		}
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"fixture_bot"}}`)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer remote.Close()
	key, err := ui.tasks.Keys().Add(harness.KeyInput{Name: "fixture bot", BaseURL: remote.URL, Protocol: harness.KeyProtocolTelegram, Source: "saved", Secret: []byte("12345678:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	runtime := harness.NewTelegramRuntime(ui.tasks)
	ui.server.SetTelegramRuntime(runtime)
	runtime.SetDispatch(ui.server.DispatchChannel)
	ui.server.AddChannelDelivery(runtime.Deliver)
	t.Cleanup(func() { runtime.Stop() })
	code, raw := ui.request(t, "PATCH", "/api/channels/telegram", map[string]any{"enabled": true, "key": key.ID})
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	deadline := time.Now().Add(2 * time.Second)
	for !runtime.Status().Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !runtime.Status().Running {
		t.Fatalf("request lifetime stopped optional channel: %+v", runtime.Status())
	}
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
	if err := runtime.HandleUpdate(t.Context(), channels.TelegramUpdate{ID: 1, Message: &channels.TelegramMessage{ID: 1, Date: 1, Chat: channels.TelegramChat{ID: 90, Type: "private"}, From: &channels.TelegramUser{ID: 7, FirstName: "Applicant"}, Text: "/start payload-cannot-authorize"}}); err != nil {
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
