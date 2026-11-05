package chatui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelplugin"
	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/harness"
)

func TestChannelPluginTelegramRoundTripLostAckAndRestart(t *testing.T) {
	// A service account may deliberately have a non-writable home. Its cache
	// is an explicit writable service directory, separate from Core data.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var calls atomic.Int32
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		calls.Add(1)
		return harness.ModelResponse{Content: "plugin round trip"}, nil
	}))
	var loseAck atomic.Bool
	ui.http.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/channel-plugin/v1/inbound" && loseAck.CompareAndSwap(true, false) {
			recorded := httptest.NewRecorder()
			ui.server.ServeHTTP(recorded, r)
			if recorded.Code == 202 {
				w.WriteHeader(503)
				return
			}
			for k, v := range recorded.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorded.Code)
			_, _ = w.Write(recorded.Body.Bytes())
			return
		}
		ui.server.ServeHTTP(w, r)
	})
	var mu sync.Mutex
	var inbox []channels.TelegramUpdate
	var sent []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			result = map[string]any{"id": 1, "is_bot": true, "username": "fixture_bot"}
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			var input struct {
				Offset int64 `json:"offset"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			updates := []channels.TelegramUpdate{}
			for _, u := range inbox {
				if u.ID >= input.Offset {
					updates = append(updates, u)
				}
			}
			mu.Unlock()
			if len(updates) == 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(25 * time.Millisecond):
				}
			}
			result = updates
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var input struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			sent = append(sent, input.Text)
			id := len(sent) + 10
			mu.Unlock()
			result = map[string]any{"message_id": id}
		default:
			t.Errorf("unexpected platform request: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer fake.Close()
	key, err := ui.tasks.ChannelKeys().Add(harness.KeyInput{Name: "optional Bot", Protocol: "telegram", BaseURL: fake.URL, Source: "saved", Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	conn := channelwire.Connection{ID: "tg-one", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}
	if status, raw := ui.request(t, "PATCH", "/api/channels", harness.ChannelsConfig{Connections: []channelwire.Connection{conn}}); status != 200 {
		t.Fatal(status, string(raw))
	}
	bootstrap := filepath.Join(ui.data, "channel-runtime", conn.ID+".json")
	state := filepath.Join(t.TempDir(), "plugin-state")
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- channelplugin.RunAll(ctx, []string{bootstrap}, state) }()
		return cancel, done
	}
	cancel, done := start()
	defer func() { cancel(); <-done }()
	push := func(update, message int64, text string) {
		mu.Lock()
		defer mu.Unlock()
		inbox = append(inbox, channels.TelegramUpdate{ID: update, Message: &channels.TelegramMessage{ID: message, Date: time.Now().Unix(), Chat: channels.TelegramChat{ID: 90, Type: "private"}, From: &channels.TelegramUser{ID: 7, FirstName: "Applicant"}, Text: text}})
	}
	wait := func(what string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case err := <-done:
				done = func() <-chan error { ch := make(chan error, 1); ch <- err; return ch }()
				t.Fatalf("plugin stopped while waiting for %s: %v", what, err)
			default:
			}
			if condition() {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		chats, _ := ui.tasks.Conversations(t.Context())
		for _, chat := range chats {
			view, err := ui.tasks.Chat(t.Context(), chat.ID, "")
			t.Logf("chat state: %+v; %v", view, err)
		}
		mu.Lock()
		t.Logf("sent=%q model_calls=%d", sent, calls.Load())
		mu.Unlock()
		t.Fatal("timed out: " + what)
	}
	push(1, 1, "/start")
	wait("Core admission request", func() bool { return len(ui.tasks.TelegramAccessRequests()) == 1 })
	request := ui.tasks.TelegramAccessRequests()[0]
	if code, raw := ui.request(t, "POST", "/api/channels/telegram/access", map[string]any{"request_id": request.ID, "enabled": true}); code != 200 {
		t.Fatal(code, string(raw))
	}
	loseAck.Store(true)
	push(2, 2, "hello Core")
	wait("platform reply", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range sent {
			if s == "plugin round trip" {
				return true
			}
		}
		return false
	})
	wait("durable cursor after lost ACK", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":2`)
	})
	if calls.Load() != 1 {
		t.Fatal("lost ACK reexecuted model", calls.Load())
	}
	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	cancel, done = start()
	push(3, 2, "hello Core") // Same platform message, a different update envelope.
	wait("restarted cursor", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":3`)
	})
	if calls.Load() != 1 {
		t.Fatal("plugin restart replayed Core execution", calls.Load())
	}
	b, err := channelplugin.ReadBootstrap(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	client := channelplugin.NewClient(b)
	if err := client.Inbound(t.Context(), channelwire.Event{ID: "cross-bot", Target: channelwire.Target{Source: "telegram", ApplicationID: "2", ChatID: "90"}, Person: "telegram:user:7", Message: "5", Text: "must not execute", Trigger: true}); err == nil {
		t.Fatal("accepted cross-Bot identity")
	}
	if calls.Load() != 1 {
		t.Fatal("forged source reached model")
	}
	if list, err := ui.tasks.Keys().List(); err != nil || len(list) != 0 {
		t.Fatal("channel credential leaked into Provider namespace", err)
	}
}

func TestChannelPluginRejectsMixedCoreBeforeConnecting(t *testing.T) {
	var paths []string
	for n := 0; n < 2; n++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("core-%d.json", n))
		b := channelwire.Bootstrap{Version: 1, CoreID: fmt.Sprint(n), Generation: "g", Origin: "http://127.0.0.1:1", Token: strings.Repeat("a", 64), Connection: channelwire.Connection{ID: "bot", Source: "telegram", ApplicationID: "1"}}
		raw, _ := json.Marshal(b)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := channelplugin.RunAll(t.Context(), paths, t.TempDir()); err == nil || !strings.Contains(err.Error(), "only one Core") {
		t.Fatal(err)
	}
}
