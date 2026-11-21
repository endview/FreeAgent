package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramTopicIdentityRequiresForumAndTopicFlag(t *testing.T) {
	bot := TelegramUser{ID: 1, IsBot: true, Username: "fixture_bot"}
	for _, tt := range []struct {
		forum, topic bool
		id           int64
		want         string
	}{{false, false, 88, ""}, {false, true, 88, ""}, {true, false, 88, "general"}, {true, true, 1, "general"}, {true, true, 88, "88"}} {
		m := &TelegramMessage{ID: 9, Date: 1, Chat: TelegramChat{ID: -100, Type: "supergroup", Forum: tt.forum}, Topic: tt.topic, ThreadID: tt.id, From: &TelegramUser{ID: 7}, Text: "question"}
		e, ok := TelegramEventFromUpdate(TelegramUpdate{ID: 1, Message: m}, bot)
		if !ok || e.ThreadID != tt.want {
			t.Fatal(e, tt)
		}
		c, ok := TelegramEventFromUpdate(TelegramUpdate{ID: 2, Callback: &TelegramCallback{ID: "callback", From: TelegramUser{ID: 7}, Data: "yes", Message: m}}, bot)
		if !ok || c.ThreadID != tt.want {
			t.Fatal(c, tt)
		}
	}
}
func TestTelegramTopicSendsPreserveTargetAndGeneralOmitsThread(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+telegramFixtureToken+"/")
		methods = append(methods, method)
		var body map[string]any
		if strings.HasSuffix(method, "Document") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			body = map[string]any{}
			if value := r.FormValue("message_thread_id"); value != "" {
				body["message_thread_id"] = value
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		if len(methods) <= 3 {
			if fmt.Sprint(body["message_thread_id"]) != "88" {
				t.Errorf("topic lost: %v", body)
			}
		} else {
			if _, ok := body["message_thread_id"]; ok {
				t.Errorf("General must omit thread: %v", body)
			}
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":10,"chat":{"id":-100}}}`)
	}))
	defer server.Close()
	api, _ := NewTelegramAPI(server.URL, telegramFixtureToken, nil)
	for _, thread := range []string{"88", "general"} {
		if _, _, err := api.SendFormatted(t.Context(), -100, 0, "text", nil, nil, thread); err != nil {
			t.Fatal(err)
		}
		if _, err := api.SendSticker(t.Context(), -100, 0, "file", thread); err != nil {
			t.Fatal(err)
		}
		if _, err := api.SendDocument(t.Context(), -100, 0, "a.txt", []byte("content"), thread); err != nil {
			t.Fatal(err)
		}
	}
	before := len(methods)
	if _, _, err := api.SendFormatted(t.Context(), -100, 0, "text", nil, nil, "1"); err == nil || len(methods) != before {
		t.Fatal("invalid topic was dispatched")
	}
}
