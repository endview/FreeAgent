package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func TestInteractiveChatAttachesAndPreservesBufferedMenus(t *testing.T) {
	data, _ := initCLI(t)
	origin, token, _ := startTestWeb(t, data)
	status, raw := webRequest(t, origin, token, "/api/chats", `{"title":"Shared session"}`)
	if status != 201 {
		t.Fatalf("create: %d %s", status, raw)
	}
	var chat harness.Conversation
	if err := json.Unmarshal(raw, &chat); err != nil {
		t.Fatal(err)
	}
	script := "first terminal turn\n/model\n/memory sources\n/collaboration off\n/begin\nsecond terminal turn\nwith another line\n/end\n/history\n/exit\n"
	output, diagnostic, err := invoke(t, script, "chat", "--data-dir", data, "--resume", chat.ID)
	if err != nil || strings.Contains(diagnostic, "error:") || !strings.Contains(output, "second terminal turn") ||
		!strings.Contains(output, "first terminal turn") || !strings.Contains(diagnostic, "No persistent memory") {
		t.Fatalf("attached chat: %s\n%s\n%v", output, diagnostic, err)
	}
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.State(t.Context())
	if err != nil || len(state.Conversations) != 1 {
		t.Fatalf("state: %+v %v", state, err)
	}
	view, err := client.Chat(t.Context(), chat.ID)
	if err != nil || len(view.Turns) != 2 || view.Turns[1].State != "completed" {
		t.Fatalf("view: %+v %v", view, err)
	}
	// An attached CLI must not open another writer or take down the GUI owner.
	if store, err := harness.OpenStore(t.Context(), data); !errors.Is(err, harness.ErrOwnerActive) {
		if store != nil {
			store.Close()
		}
		t.Fatalf("owner lock not retained: %v", err)
	}
}

func TestTerminalOwnerPublishesGUIAndReleasesOnExit(t *testing.T) {
	data, _ := initCLI(t)
	input, send := io.Pipe()
	defer input.Close()
	defer send.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output, diagnostic := &lockedBuffer{}, &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"chat", "--data-dir", data}, input, output, diagnostic) }()
	var client *handoff.Client
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		client, err = interactiveOwner(t.Context(), data)
		if err == nil && strings.Contains(output.String(), "> ") {
			break
		}
		client = nil
		time.Sleep(10 * time.Millisecond)
	}
	if client == nil {
		t.Fatal("terminal owner did not publish an attachable API")
	}
	state, err := client.State(t.Context())
	if err != nil || len(state.Conversations) != 1 {
		t.Fatalf("state: %+v %v", state, err)
	}
	chat := state.Conversations[0].ID
	if err := client.Submit(t.Context(), chat, "gui-turn", "hello from GUI"); err != nil {
		t.Fatal(err)
	}
	if err := waitAttachedIdle(t.Context(), client, chat); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(send, "/history\nhello from CLI\n/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("terminal owner did not drain and exit")
	}
	if !strings.Contains(output.String(), "hello from GUI") || !strings.Contains(output.String(), "hello from CLI") || strings.Contains(diagnostic.String(), "error:") {
		t.Fatalf("shared history: %s %s", output.String(), diagnostic.String())
	}
	tasks, closeAll, err := harness.OpenTasks(t.Context(), data, harness.Adapters{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil || len(messages) != 4 {
		t.Fatalf("saved messages: %+v %v", messages, err)
	}
}

func TestAttachedChatReviewUsesSavedPlanningAttempt(t *testing.T) {
	data, workspace := initCLI(t)
	planning, ordinary := chatSuggestionFixture(t, data, workspace)
	_, _, _ = startTestWeb(t, data)
	output, diagnostic, err := invoke(t, "/collaboration once\ngoal\ne\n1\nReviewed goal\nreview\ny\n/exit\n", "chat", "--data-dir", data)
	if err != nil || planning.Load() != 1 || ordinary.Load() != 1 || !strings.Contains(output, "reviewed: expert answer") || strings.Contains(diagnostic, "error:") {
		t.Fatalf("review: %s %s %v calls=%d/%d", output, diagnostic, err, planning.Load(), ordinary.Load())
	}
}

func TestAttachedChatConfirmsGUIPendingToolWithoutReplay(t *testing.T) {
	data, workspace := initCLI(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		message := map[string]any{"role": "assistant", "content": "saved write confirmed"}
		finish := "stop"
		if calls.Add(1) == 1 {
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "pending-write", "type": "function", "function": map[string]string{
				"name": "file_create", "arguments": `{"path":"shared-write.txt","content":"EXACT_SHARED_CONTENT"}`,
			}}}
			finish = "tool_calls"
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
	}))
	defer provider.Close()
	key := fixtureKey(t, provider.URL, harness.KeySourceNone, "", "")
	if err := harness.WriteConfig(data, cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})); err != nil {
		t.Fatal(err)
	}
	_, _, _ = startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := client.NewConversation(t.Context(), "GUI approval")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Submit(t.Context(), chat.ID, "gui-write", "write exact file"); err != nil {
		t.Fatal(err)
	}
	if err := waitAttachedIdle(t.Context(), client, chat.ID); err != nil {
		t.Fatal(err)
	}
	output, diagnostic, err := invoke(t, "/continue\ny\n/history\n/exit\n", "chat", "--data-dir", data, "--resume", chat.ID)
	if err != nil || calls.Load() != 2 || !strings.Contains(diagnostic, "Approve file_create?") || strings.Contains(diagnostic, "error:") {
		t.Fatalf("approval: %s %s %v calls=%d", output, diagnostic, err, calls.Load())
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "shared-write.txt"))
	if err != nil || string(raw) != "EXACT_SHARED_CONTENT" {
		t.Fatalf("file: %q %v", raw, err)
	}
	view, err := client.Chat(t.Context(), chat.ID)
	if err != nil || len(view.Turns) != 1 || view.Turns[0].State != "completed" || len(view.Turns[0].Evidence.Files) != 1 {
		t.Fatalf("GUI outcome: %+v %v", view, err)
	}
}

func TestAttachedChatExpiredEventsResnapshotWithoutResubmit(t *testing.T) {
	var snapshots, submits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/attach/events":
			if request.URL.Query().Get("after") == "0" {
				writer.WriteHeader(409)
				_, _ = io.WriteString(writer, `{"code":"cursor_expired"}`)
				return
			}
			_ = json.NewEncoder(writer).Encode(handoff.AttachEvents{Generation: "generation", NextSeq: 300})
		case "/api/attach/state":
			snapshots.Add(1)
			_ = json.NewEncoder(writer).Encode(handoff.AttachState{Generation: "generation", LastSeq: 300})
		case "/api/attach/status":
			_ = json.NewEncoder(writer).Encode(handoff.AttachRunStatus{Found: true, Run: harness.Run{ID: "saved"}})
		case "/api/attach/inspect":
			_ = json.NewEncoder(writer).Encode(map[string]any{"run": harness.Run{ID: "saved", Status: "completed", Output: "SAVED_ANSWER"}})
		default:
			submits.Add(1)
			writer.WriteHeader(400)
		}
	}))
	defer server.Close()
	remote := &remoteChat{Client: &handoff.Client{Origin: server.URL, Generation: "generation", Secret: strings.Repeat("s", 64)},
		stderr: io.Discard, emit: func(harness.Event) { t.Error("expired events were replayed") }}
	result, err := remote.wait(t.Context(), "chat", "request", "")
	if err != nil || result.Answer != "SAVED_ANSWER" || snapshots.Load() != 1 || submits.Load() != 0 || remote.emit != nil {
		t.Fatalf("resnapshot: %+v %v snapshots=%d submits=%d", result, err, snapshots.Load(), submits.Load())
	}
}
