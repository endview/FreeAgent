package chatui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelplugin"
	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/postgres"
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
	var holdUnconfirmed atomic.Bool
	var heldAttempts atomic.Int32
	ui.http.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/channel-plugin/v1/inbound" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var event channelwire.Event
			if json.Unmarshal(raw, &event) == nil {
				switch event.ID {
				case "6":
					// Simulate a bad projection like the former signed group-ID bug.
					event.Target.ChatID = ""
					raw, _ = json.Marshal(event)
				case "7":
					if holdUnconfirmed.Load() {
						heldAttempts.Add(1)
						w.WriteHeader(http.StatusAccepted)
						_ = json.NewEncoder(w).Encode(channelwire.Acceptance{Received: true, Disposition: "pending"})
						return
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
		}
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
		if strings.HasPrefix(r.URL.Path, "/api/channel-plugin/") {
			recorded := httptest.NewRecorder()
			ui.server.ServeHTTP(recorded, r)
			if recorded.Code >= 400 {
				t.Logf("channel endpoint %s: HTTP %d %s", r.URL.Path, recorded.Code, recorded.Body.String())
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
	type platformReply struct {
		Chat   int64  `json:"chat_id"`
		Thread int64  `json:"message_thread_id"`
		Text   string `json:"text"`
	}
	var sent []platformReply
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
			var input platformReply
			_ = json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			sent = append(sent, input)
			id := len(sent) + 10
			mu.Unlock()
			result = map[string]any{"message_id": id, "chat": channels.TelegramChat{ID: input.Chat}}
		case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
			result = true
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
		if binary := os.Getenv("FREEAGENT_TEST_CHANNEL_BINARY"); binary != "" {
			command := exec.CommandContext(ctx, binary, "--bootstrap-file", bootstrap, "--state-dir", state)
			go func() {
				err := command.Run()
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				done <- err
			}()
			return cancel, done
		}
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
		c, _ := postgres.Read(filepath.Join(ui.data, postgres.ConfigFile))
		db, e := postgres.SQL(c, "", false)
		if e == nil {
			rows, e := db.QueryContext(t.Context(), `SELECT key,value FROM fa.meta WHERE key LIKE 'channel/pending-reply/%' OR key LIKE 'channel/operation/%' ORDER BY key`)
			if e == nil {
				for rows.Next() {
					var key, value string
					_ = rows.Scan(&key, &value)
					t.Log(key, value)
				}
				rows.Close()
			}
			db.Close()
		}
		chats, _ := ui.tasks.Conversations(t.Context())
		for _, chat := range chats {
			view, err := ui.tasks.Chat(t.Context(), chat.ID, "")
			t.Logf("chat state: %+v; %v", view, err)
		}
		mu.Lock()
		t.Logf("sent=%+v model_calls=%d", sent, calls.Load())
		mu.Unlock()
		t.Fatal("timed out: " + what)
	}
	const groupID int64 = -1001960000001
	group := channels.TelegramChat{ID: groupID, Type: "supergroup", Forum: true}
	// A real Bot's signed group update can be ahead of private input in the one cursor.
	mu.Lock()
	inbox = append(inbox, channels.TelegramUpdate{ID: 1, MyMember: &channels.TelegramMemberUpdate{Chat: group, Date: time.Now().Unix(), New: channels.TelegramMember{Status: "administrator"}}})
	mu.Unlock()
	push(2, 1, "/start")
	wait("Core admission request", func() bool { return len(ui.tasks.TelegramAccessRequests()) == 1 })
	request := ui.tasks.TelegramAccessRequests()[0]
	if code, raw := ui.request(t, "POST", "/api/channels/telegram/access", map[string]any{"request_id": request.ID, "enabled": true}); code != 200 {
		t.Fatal(code, string(raw))
	}
	loseAck.Store(true)
	push(3, 2, "hello Core")
	wait("platform reply", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range sent {
			if s.Chat == 90 && s.Thread == 0 && s.Text == "plugin round trip" {
				return true
			}
		}
		return false
	})
	wait("durable cursor after lost ACK", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":3`)
	})
	if calls.Load() != 1 {
		t.Fatal("lost ACK reexecuted model", calls.Load())
	}
	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	cancel, done = start()
	push(4, 2, "hello Core") // Same platform message, a different update envelope.
	wait("restarted cursor", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":4`)
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
	groupTarget := harness.ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: fmt.Sprint(groupID)}
	grant := harness.TelegramAccessChange{Target: groupTarget, Kind: "group", Enabled: true, Members: []string{"telegram:user:7"}}
	if code, raw := ui.request(t, "POST", "/api/channels/telegram/access", grant); code != 200 {
		t.Fatal(code, string(raw))
	}
	if state, err := ui.tasks.TelegramGroup(t.Context(), groupTarget); err != nil || state.LastEvent != 1 || !state.Forum {
		t.Fatal("group lifecycle was not committed to the signed target", state, err)
	}
	const thread = 41
	mu.Lock()
	inbox = append(inbox, channels.TelegramUpdate{ID: 5, Message: &channels.TelegramMessage{ID: 6, Date: time.Now().Unix(), Chat: group, Topic: true, ThreadID: thread, From: &channels.TelegramUser{ID: 7}, Text: "@fixture_bot hello group", Entities: []channels.TelegramEntity{{Type: "mention", Length: len("@fixture_bot")}}}})
	mu.Unlock()
	wait("signed group topic delivery", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(sent, func(reply platformReply) bool {
			return reply.Chat == groupID && reply.Thread == thread && reply.Text == "plugin round trip"
		})
	})
	groupTarget.ThreadID = fmt.Sprint(thread)
	chat, err := ui.tasks.ChannelConversation(t.Context(), groupTarget)
	if err != nil {
		t.Fatal(err)
	}
	wait("confirmed group platform receipt", func() bool {
		groups, err := ui.tasks.ChannelDeliveries(t.Context(), chat)
		return err == nil && slices.ContainsFunc(groups, func(group harness.DeliveryGroup) bool {
			return group.RunID != "" && len(group.Parts) == 1 && group.Parts[0].State == "sent" && group.Parts[0].MessageID != ""
		})
	})
	if calls.Load() != 2 {
		t.Fatal("expected one private Run and one group Run", calls.Load())
	}
	wait("group cursor before ingress classification", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":5`)
	})

	holdUnconfirmed.Store(true)
	const privateMarker = "fixture-payload-not-to-be-retained"
	push(6, 7, privateMarker)
	push(7, 8, "hold this input")
	push(8, 9, "follow the confirmed input")
	awaitWait := func() bool {
		code, raw := ui.request(t, "GET", "/api/channels", nil)
		return code == 200 && bytes.Contains(raw, []byte(`"pending_since":`)) && bytes.Contains(raw, []byte(`"retries":`))
	}
	wait("unconfirmed input in diagnostics", awaitWait)
	rawCursor, err := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
	if err != nil || !strings.Contains(string(rawCursor), `"last":6`) || calls.Load() != 2 {
		t.Fatal("rejection did not advance or unconfirmed update advanced", string(rawCursor), calls.Load(), err)
	}
	pg, err := postgres.Read(filepath.Join(ui.data, postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.SQL(pg, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rejected, leaked int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FILTER (WHERE document->>'rejection'='invalid_telegram_route'),count(*) FILTER (WHERE value LIKE $1) FROM fa.meta WHERE key LIKE 'channel/inbound/%'`, "%"+privateMarker+"%").Scan(&rejected, &leaked); err != nil || rejected != 1 || leaked != 0 {
		t.Fatal("refusal receipt missing or retained input body", rejected, leaked, err)
	}
	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	beforeRestart := heldAttempts.Load()
	cancel, done = start()
	wait("unconfirmed ingress resumes without cursor advance", func() bool {
		return heldAttempts.Load() > beforeRestart && awaitWait()
	})
	rawCursor, err = os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
	if err != nil || !strings.Contains(string(rawCursor), `"last":6`) || calls.Load() != 2 {
		t.Fatal("restart skipped an unconfirmed input", string(rawCursor), calls.Load(), err)
	}
	holdUnconfirmed.Store(false)
	wait("confirmed input and its successor advance", func() bool {
		raw, _ := os.ReadFile(filepath.Join(state, conn.ID+".cursor.json"))
		return strings.Contains(string(raw), `"last":8`)
	})
	wait("ingress diagnostics clear waiting state", func() bool {
		code, raw := ui.request(t, "GET", "/api/channels", nil)
		return code == 200 && bytes.Contains(raw, []byte(`"advanced_at":`)) && !bytes.Contains(raw, []byte(`"pending_since":`))
	})
	if list, err := ui.tasks.Keys().List(); err != nil || len(list) != 0 {
		t.Fatal("channel credential leaked into Provider namespace", err)
	}
}

func TestChannelPluginRejectsMixedCoreBeforeConnecting(t *testing.T) {
	var paths []string
	for n := 0; n < 2; n++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("core-%d.json", n))
		b := channelwire.Bootstrap{Version: channelwire.Version, CoreID: fmt.Sprint(n), Generation: "g", Origin: "http://127.0.0.1:1", Token: strings.Repeat("a", 64), Connection: channelwire.Connection{ID: "bot", Source: "telegram", ApplicationID: "1"}}
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

func TestChannelPluginMinimalTextV4UsesSharedTaskPath(t *testing.T) {
	var calls atomic.Int32
	ui := setupUI(t, fixtureModel(func(_ context.Context, req harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		calls.Add(1)
		if !req.DenyOperations || !slices.ContainsFunc(req.Messages, func(msg harness.ModelMessage) bool {
			return msg.Role == "user" && strings.HasPrefix(msg.Content, "[Source lark request;") && strings.HasSuffix(msg.Content, "\nfixture input")
		}) {
			t.Error("connector altered the Core input or operation permissions")
		}
		for _, tool := range req.Tools {
			if tool.Name != "control_read_state" && tool.Name != "control_plan_change" {
				t.Errorf("connector granted an operational tool: %s", tool.Name)
			}
		}
		return harness.ModelResponse{Content: "fixture reply"}, nil
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	key, err := ui.tasks.ChannelKeys().Add(harness.KeyInput{Name: "text fixture", Protocol: "lark", BaseURL: "http://127.0.0.1:1", Source: "saved", Secret: []byte("FIXTURE_SECRET")})
	if err != nil {
		t.Fatal(err)
	}
	conn := channelwire.Connection{ID: "text-only", Source: "lark", ApplicationID: "app:tenant", Credential: key.ID, Enabled: true}
	if status, raw := ui.request(t, "PATCH", "/api/channels", harness.ChannelsConfig{Connections: []channelwire.Connection{conn}}); status != 200 {
		t.Fatal(status, string(raw))
	}
	boot, err := channelplugin.ReadBootstrap(filepath.Join(ui.data, "channel-runtime", conn.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	client := channelplugin.NewClient(boot)
	register := func(version int) (int, string) {
		t.Helper()
		raw, _ := json.Marshal(channelwire.Capabilities{Version: version, Format: "plain/1", Actions: []string{"prepare_text", "text"}})
		req, err := http.NewRequestWithContext(ctx, "POST", boot.Origin+"/api/channel-plugin/v1/capabilities", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+boot.Token)
		req.Header.Set("X-FreeAgent-Core", boot.CoreID)
		req.Header.Set("X-FreeAgent-Generation", boot.Generation)
		req.Header.Set("Origin", boot.Origin)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	if status, body := register(3); status != 409 || !strings.Contains(body, "version 3; require 4") {
		t.Fatal(status, body)
	}
	target := channelwire.Target{Source: "lark", ApplicationID: conn.ApplicationID, ChatID: "oc_fixture"}
	if _, err := ui.tasks.PairChannel(ctx, harness.ChannelTarget(target), "private", "Fixture"); err != nil {
		t.Fatal(err)
	}
	inbound := channelwire.Event{ID: "fixture-message", Target: target, Person: "lark:app:tenant:open:ou_fixture", Message: "fixture-message", Kind: "private", Text: "fixture input", Trigger: true}
	if err := client.Inbound(ctx, inbound); err == nil {
		t.Fatal("unregistered connector reached Core")
	}
	if status, body := register(4); status != 200 {
		t.Fatal(status, body)
	}
	undeclared := inbound
	undeclared.Target.ThreadID = "11"
	if err := client.Inbound(ctx, undeclared); err == nil {
		t.Fatal("accepted an undeclared topic")
	}
	if err := client.Inbound(ctx, inbound); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		ops, err := client.Outbox(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			if op.Target != target || op.Text != "fixture reply" || len(op.Buttons) != 0 {
				t.Fatal("invalid text operation", op)
			}
			r := channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch}
			switch op.Kind {
			case "prepare_text":
				r.State = "downloaded"
				r.Data, _ = json.Marshal(channelwire.Prepared{SourceHash: op.SourceHash, Format: "plain/1", Parts: []channelwire.TextPart{{Text: op.Text}}})
			case "text":
				r.State, r.MessageID = "sent", "fixture-outbound"
			default:
				t.Fatal("undeclared action queued", op.Kind)
			}
			if err := client.Receipt(ctx, r); err != nil {
				t.Fatal(err)
			}
			if r.State == "sent" {
				if calls.Load() != 1 {
					t.Fatal(calls.Load())
				}
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	t.Fatalf("no confirmed reply through the shared path; model calls=%d", calls.Load())
}
