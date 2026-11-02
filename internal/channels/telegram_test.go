package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

const telegramFixtureToken = "12345678:FIXTURE_ONLY_TOKEN"

func TestTelegramAPIUsesExplicitPollingContractAndPlaintextReplies(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+telegramFixtureToken+"/") {
			t.Errorf("wrong token binding")
			w.WriteHeader(401)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+telegramFixtureToken+"/")
		methods = append(methods, method)
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid request JSON")
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"fixture_bot","can_read_all_group_messages":true}}`)
		case "getUpdates":
			if string(body["offset"]) != "-1" || string(body["timeout"]) != "0" || string(body["limit"]) != "100" || string(body["allowed_updates"]) != `["message","callback_query"]` {
				t.Errorf("poll contract %s", body)
			}
			fmt.Fprint(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":6,"date":1,"chat":{"id":90,"type":"private"},"from":{"id":4},"text":"hello"}}]}`)
		case "sendMessage":
			if body["parse_mode"] != nil || body["allow_paid_broadcast"] != nil || !strings.Contains(string(body["link_preview_options"]), "true") {
				t.Error("reply enabled unwanted parsing, fetching or paid broadcasts")
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":100,"chat":{"id":90,"type":"private"}}}`)
		case "editMessageText", "answerCallbackQuery":
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected method")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	api, err := NewTelegramAPI(server.URL, telegramFixtureToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	me, err := api.Me(t.Context())
	if err != nil || me.ID != 1 || !me.CanReadGroupMessages {
		t.Fatalf("identity %+v %v", me, err)
	}
	updates, err := api.Poll(t.Context(), -1, 0)
	if err != nil || len(updates) != 1 || updates[0].ID != 7 {
		t.Fatalf("updates %+v %v", updates, err)
	}
	keyboard := &TelegramKeyboard{Rows: [][]TelegramButton{{{Text: "Approve", Data: "opaque-exact-reference"}}}}
	id, err := api.Send(t.Context(), 90, 6, "literal <b>text</b>", keyboard)
	if err != nil || id != 100 {
		t.Fatal(id, err)
	}
	if err := api.Edit(t.Context(), 90, id, "updated", keyboard); err != nil {
		t.Fatal(err)
	}
	if err := api.AnswerCallback(t.Context(), "callback", "recorded"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "getMe,getUpdates,sendMessage,editMessageText,answerCallbackQuery" {
		t.Fatal(methods)
	}
}

func TestTelegramRateLimitUncertaintyAndTokenRedaction(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		uncertain  bool
		retry      time.Duration
	}{
		{"rate", `{"ok":false,"error_code":429,"parameters":{"retry_after":9}}`, 429, false, 9 * time.Second},
		{"server", `{"ok":false,"error_code":500,"description":"` + telegramFixtureToken + `"}`, 500, true, 0},
		{"invalid", "`" + telegramFixtureToken + "`", 200, true, 0},
		{"wrong-target", `{"ok":true,"result":{"message_id":4,"chat":{"id":123}}}`, 200, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			api, err := NewTelegramAPI(server.URL, telegramFixtureToken, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = api.Send(t.Context(), 90, 0, "hello", nil)
			var failure *TelegramError
			if !errors.As(err, &failure) || failure.Uncertain != test.uncertain || failure.RetryAfter != test.retry {
				t.Fatalf("error %v", err)
			}
			if strings.Contains(err.Error(), telegramFixtureToken) || strings.Contains(err.Error(), "/bot") {
				t.Fatal("token-bearing URL leaked")
			}
			if requests.Load() != 1 {
				t.Fatal("delivery was implicitly retried")
			}
		})
	}
}

func TestTelegramDoesNotFollowRedirectOrDispatchCancelledSend(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	api, err := NewTelegramAPI(server.URL, telegramFixtureToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Send(t.Context(), 90, 0, "hello", nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if received.Load() != 0 {
		t.Fatal("token-bearing request followed redirect")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = api.Send(ctx, 90, 0, "hello", nil)
	var failure *TelegramError
	if !errors.As(err, &failure) || failure.Uncertain {
		t.Fatalf("known pre-dispatch cancellation=%v", err)
	}
	if _, err := NewTelegramAPI("https://untrusted.example", telegramFixtureToken, nil); err == nil {
		t.Fatal("arbitrary token destination accepted")
	}
}

func TestTelegramIdentityUsesEventSenderAndUTF16MentionOffsets(t *testing.T) {
	bot := TelegramUser{ID: 1, IsBot: true, Username: "fixture_bot"}
	message := TelegramMessage{ID: 5, Date: 1, Chat: TelegramChat{ID: 90, Type: "supergroup"}, From: &TelegramUser{ID: 7, FirstName: "Admin Full"}, Text: "😀 @fixture_bot show history", Entities: []TelegramEntity{{Type: "mention", Offset: 3, Length: 12}}}
	event, ok := TelegramEventFromUpdate(TelegramUpdate{ID: 1, Message: &message}, bot)
	if !ok || !event.Triggered || event.PersonID != "telegram:user:7" {
		t.Fatalf("sender %+v %t", event, ok)
	}
	message.SenderChat = &TelegramChat{ID: 90, Type: "supergroup"}
	event, ok = TelegramEventFromUpdate(TelegramUpdate{Message: &message}, bot)
	if !ok || event.PersonID != "" {
		t.Fatal("anonymous sender borrowed a user's identity")
	}
	message.SenderChat = nil
	message.Text = "/stop@other_bot"
	message.Entities = []TelegramEntity{{Type: "bot_command", Offset: 0, Length: 15}}
	event, ok = TelegramEventFromUpdate(TelegramUpdate{Message: &message}, bot)
	if !ok || event.Triggered {
		t.Fatal("command for another bot triggered")
	}
	message.Text = "ordinary background"
	message.Entities = nil
	event, ok = TelegramEventFromUpdate(TelegramUpdate{Message: &message}, bot)
	if !ok || event.Triggered {
		t.Fatal("background became a request")
	}
	callback := TelegramCallback{ID: "click", From: TelegramUser{ID: 8}, Message: &message, Data: "person=7;permission=full"}
	event, ok = TelegramEventFromUpdate(TelegramUpdate{Callback: &callback}, bot)
	if !ok || event.PersonID != "telegram:user:8" {
		t.Fatal("callback data overrode the clicker")
	}
	message.Date = 0
	if _, ok := TelegramEventFromUpdate(TelegramUpdate{Callback: &callback}, bot); ok {
		t.Fatal("inaccessible approval message accepted")
	}
	var edited TelegramUpdate
	_ = json.Unmarshal([]byte(`{"update_id":2,"edited_message":{"message_id":5,"text":"repeat operation"}}`), &edited)
	if _, ok := TelegramEventFromUpdate(edited, bot); ok {
		t.Fatal("edited old request was replayed")
	}
}

func TestTelegramChunkingAndCallbackBounds(t *testing.T) {
	text := strings.Repeat("😀中文", 1800)
	parts := SplitTelegramText(text)
	if strings.Join(parts, "") != text || len(parts) < 2 {
		t.Fatal("text loss")
	}
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) > 4000 {
			t.Fatal("UTF-16 limit exceeded")
		}
	}
	if validateKeyboard(&TelegramKeyboard{Rows: [][]TelegramButton{{{Text: "Approve", Data: strings.Repeat("x", 65)}}}}) == nil {
		t.Fatal("oversized callback data accepted")
	}
}
