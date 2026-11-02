package harness

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
	"testing"

	"github.com/endview/freeagent/internal/channels"
)

const telegramRuntimeFixtureToken = "12345678:RUNTIME_FIXTURE_TOKEN"

type telegramSentMessage struct {
	ID       int64
	ChatID   int64
	Text     string
	Callback []string
}

func telegramRuntimeFixture(t *testing.T, model ModelClient) (*TelegramRuntime, *TaskService, *Store, string, *[]telegramSentMessage) {
	t.Helper()
	service, _, workspace := testHarness(t, model)
	tasks := NewTaskService(service)
	var mu sync.Mutex
	sent := make([]telegramSentMessage, 0)
	var nextID int64 = 100
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method := filepath.Base(request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		if method == "sendMessage" {
			var body struct {
				ChatID      int64          `json:"chat_id"`
				Text        string         `json:"text"`
				Reply       int64          `json:"reply_parameters.message_id"`
				ReplyMarkup map[string]any `json:"reply_markup"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("send request: %v", err)
			}
			item := telegramSentMessage{ID: nextID, ChatID: body.ChatID, Text: body.Text}
			nextID++
			if rows, ok := body.ReplyMarkup["inline_keyboard"].([]any); ok {
				for _, rawRow := range rows {
					if row, ok := rawRow.([]any); ok {
						for _, rawButton := range row {
							if button, ok := rawButton.(map[string]any); ok {
								if data, ok := button["callback_data"].(string); ok {
									item.Callback = append(item.Callback, data)
								}
							}
						}
					}
				}
			}
			mu.Lock()
			sent = append(sent, item)
			mu.Unlock()
			fmt.Fprintf(writer, `{"ok":true,"result":{"message_id":%d,"chat":{"id":%d,"type":"private"}}}`, item.ID, item.ChatID)
			return
		}
		if method == "answerCallbackQuery" {
			fmt.Fprint(writer, `{"ok":true,"result":true}`)
			return
		}
		t.Errorf("unexpected Telegram fixture method %s", method)
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	api, err := channels.NewTelegramAPI(server.URL, telegramRuntimeFixtureToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewTelegramRuntime(tasks)
	runtime.api = api
	runtime.bot = channels.TelegramUser{ID: 1, IsBot: true, Username: "fixture_bot"}
	runtime.running = true
	t.Cleanup(func() { runtime.running = false })
	return runtime, tasks, service.Store, workspace, &sent
}

func telegramMessageUpdate(id, messageID, chatID, userID int64, text string, kind string) channels.TelegramUpdate {
	return channels.TelegramUpdate{ID: id, Message: &channels.TelegramMessage{ID: messageID, Date: 1,
		Chat: channels.TelegramChat{ID: chatID, Type: kind}, From: &channels.TelegramUser{ID: userID, FirstName: "Fixture"}, Text: text}}
}

func TestTelegramRuntimePairChatAndDuplicateDelivery(t *testing.T) {
	runtime, tasks, store, _, sent := telegramRuntimeFixture(t, EchoModel{})
	code, _, err := runtime.PairCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleUpdate(t.Context(), telegramMessageUpdate(1, 10, 90, 7, "/pair "+code, "private")); err != nil {
		t.Fatal(err)
	}
	target := ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "90"}
	space, err := tasks.ChannelSpace(t.Context(), target)
	if err != nil || space.DefaultConversationID == "" {
		t.Fatalf("space=%+v err=%v", space, err)
	}
	if permission, err := tasks.SpacePermission(t.Context(), space.ID); err != nil || permission.Mode != PermissionNone {
		t.Fatalf("pair permission=%+v err=%v", permission, err)
	}
	before := len(*sent)
	update := telegramMessageUpdate(2, 11, 90, 7, "hello from Telegram", "private")
	if err := runtime.HandleUpdate(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != before+1 {
		t.Fatalf("sent after chat=%d before=%d", len(*sent), before)
	}
	if err := runtime.HandleUpdate(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != before+1 {
		t.Fatalf("duplicate caused another delivery: %d", len(*sent))
	}
	run, err := store.GetLatestRun(t.Context(), space.DefaultConversationID)
	if err != nil || run.Status != "completed" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	messages, err := tasks.Messages(t.Context(), space.DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var received int
	for _, message := range messages {
		if message.Channel != nil && message.Channel.MessageID == "11" {
			received++
		}
	}
	if received != 1 {
		t.Fatalf("received message copies=%d messages=%+v", received, messages)
	}
}

func TestTelegramRuntimeApprovalKeepsExactRequesterAndRejectsOtherUser(t *testing.T) {
	var calls int
	model := modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{{ID: "file-1", Name: "file_create", Arguments: json.RawMessage(`{"path":"telegram.txt","content":"approved"}`)}}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	})
	runtime, tasks, _, workspace, sent := telegramRuntimeFixture(t, model)
	code, _, err := runtime.PairCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleUpdate(t.Context(), telegramMessageUpdate(1, 10, 90, 7, "/pair "+code, "private")); err != nil {
		t.Fatal(err)
	}
	space, err := tasks.ChannelSpace(t.Context(), ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "90"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetPersonPermission(t.Context(), "telegram:user:7", "Fixture", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleUpdate(t.Context(), telegramMessageUpdate(2, 11, 90, 7, "create a file", "private")); err != nil {
		t.Fatal(err)
	}
	var callbacks []string
	for _, message := range *sent {
		callbacks = append(callbacks, message.Callback...)
	}
	if len(callbacks) != 2 {
		t.Fatalf("approval callbacks=%v sent=%+v", callbacks, *sent)
	}
	callback := func(updateID, userID int64) channels.TelegramUpdate {
		return channels.TelegramUpdate{ID: updateID, Callback: &channels.TelegramCallback{ID: fmt.Sprintf("callback-%d", updateID),
			From: channels.TelegramUser{ID: userID}, Data: callbacks[0], Message: &channels.TelegramMessage{ID: (*sent)[len(*sent)-1].ID, Date: 1,
				Chat: channels.TelegramChat{ID: 90, Type: "private"}, From: &channels.TelegramUser{ID: 1, IsBot: true}}}}
	}
	if err := runtime.HandleUpdate(t.Context(), callback(3, 8)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleUpdate(t.Context(), callback(4, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "telegram.txt")); err != nil {
		t.Fatalf("approved file was not created: %v", err)
	}
	if calls != 2 {
		t.Fatalf("model calls=%d", calls)
	}
}

func TestTelegramRuntimeGroupBackgroundWaitsForExplicitWake(t *testing.T) {
	var requests []ModelRequest
	model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, request)
		return ModelResponse{Content: "group answer"}, nil
	})
	runtime, tasks, store, _, sent := telegramRuntimeFixture(t, model)
	code, _, err := runtime.PairCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleUpdate(t.Context(), telegramMessageUpdate(1, 10, -100, 7, "/pair "+code, "supergroup")); err != nil {
		t.Fatal(err)
	}
	space, err := tasks.ChannelSpace(t.Context(), ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "-100"})
	if err != nil {
		t.Fatal(err)
	}
	pairedReplies := len(*sent)
	background := telegramMessageUpdate(2, 11, -100, 7, "background fact", "supergroup")
	if err := runtime.HandleUpdate(t.Context(), background); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != pairedReplies || len(requests) != 0 {
		t.Fatalf("background triggered work: sent=%d requests=%d", len(*sent), len(requests))
	}
	wake := telegramMessageUpdate(3, 12, -100, 7, "@fixture_bot summarize", "supergroup")
	wake.Message.Entities = []channels.TelegramEntity{{Type: "mention", Offset: 0, Length: 12}}
	if err := runtime.HandleUpdate(t.Context(), wake); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || len(*sent) != pairedReplies+1 {
		t.Fatalf("wake did not run: requests=%d sent=%d", len(requests), len(*sent))
	}
	seenBackground := false
	for _, message := range requests[0].Messages {
		if strings.Contains(message.Content, "background fact") {
			seenBackground = true
		}
	}
	if !seenBackground {
		t.Fatalf("public group background missing from request: %+v", requests[0].Messages)
	}
	run, err := store.GetLatestRun(t.Context(), space.DefaultConversationID)
	if err != nil || run.Status != "completed" {
		t.Fatalf("group run=%+v err=%v", run, err)
	}
}
