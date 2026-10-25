package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIContextAndLocalCompactShareGUIOwner(t *testing.T) {
	data, workspace := initCLI(t)
	var summaries, normal atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(400)
			return
		}
		text := strings.Repeat("completed source work; ", 30)
		if len(body.Messages) != 0 && strings.Contains(body.Messages[0].Content, "Summarize the supplied conversation") {
			summaries.Add(1)
			if len(body.Tools) != 0 {
				t.Error("summary request received tools")
			}
			text = "Goal: retain source constraints. Completed fixture work. Next: await user."
		} else {
			normal.Add(1)
			for _, message := range body.Messages {
				if message.Content == "/compact" {
					t.Error("local command reached Provider as user task")
				}
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	key := fixtureKey(t, provider.URL, harness.KeySourceNone, "", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	config.TaskSummaryBytes = 0
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	origin, token, _ := startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := client.NewConversation(t.Context(), "Shared context")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := client.Submit(t.Context(), chat.ID, string(rune('a'+i)), strings.Repeat("source constraints; ", 30)); err != nil {
			t.Fatal(err)
		}
		if err := waitAttachedIdle(t.Context(), client, chat.ID); err != nil {
			t.Fatal(err)
		}
	}
	output, diagnostic, err := invoke(t, "/compact\n/exit\n", "chat", "--data-dir", data, "--resume", chat.ID)
	if err != nil || !strings.Contains(output, "changed=true") || summaries.Load() != 1 || normal.Load() != 3 || strings.Contains(diagnostic, "error:") {
		t.Fatalf("local compact: %s %s %v calls=%d/%d", output, diagnostic, err, summaries.Load(), normal.Load())
	}
	output, _, err = invoke(t, "", "context", "inspect", "--data-dir", data, "--session", chat.ID, "--json")
	var result harness.ContextResult
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.State.Status != "current" || result.State.Automatic {
		t.Fatalf("inspect: %s %v", output, err)
	}
	code, raw := webRequest(t, origin, token, "/api/attach/context?conversation_id="+chat.ID, "")
	var gui harness.ContextState
	if code != 200 || json.Unmarshal(raw, &gui) != nil || gui.Revision != result.State.Revision {
		t.Fatalf("GUI did not see CLI summary: %d %s", code, raw)
	}
	view, err := client.Chat(t.Context(), chat.ID)
	if err != nil || len(view.Turns) != 3 {
		t.Fatalf("compaction fabricated a chat turn: %+v %v", view, err)
	}
	output, _, err = invoke(t, "", "context", "clear", "--data-dir", data, "--session", chat.ID, "--json")
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || !result.Changed || result.State.Status != "none" || summaries.Load() != 1 {
		t.Fatalf("clear used model or failed: %s %v", output, err)
	}
}
