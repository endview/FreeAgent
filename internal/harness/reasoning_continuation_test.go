package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestPrivateContinuationsPersistWithoutSchemaChanges(t *testing.T) {
	response := ModelResponse{Content: "visible", ProviderSource: strings.Repeat("a", 64), ProviderItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque-response"}`)}, ChatContinuation: &ChatContinuation{Source: strings.Repeat("b", 64), Blocks: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"","signature":"opaque-native"}`)}}}
	core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) { return response, nil }))
	chat := conversation(t, core)
	result, err := NewTaskService(core).Submit(t.Context(), chat, "private-persistence", "hello")
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	data := core.Store.DataDir()
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.ListMessages(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range messages {
		if m.Role == "assistant" {
			found = true
			var persisted, original any
			_ = json.Unmarshal(mustJSON([]any{m.ChatContinuation, m.ProviderItems, m.ProviderSource}), &persisted)
			_ = json.Unmarshal(mustJSON([]any{response.ChatContinuation, response.ProviderItems, response.ProviderSource}), &original)
			if !reflect.DeepEqual(persisted, original) {
				t.Fatal("private protocol data changed after reopen")
			}
		}
	}
	if !found {
		t.Fatal("assistant record missing")
	}
	var raw string
	if err := reopened.db.QueryRowContext(t.Context(), `SELECT response_json FROM fa.attempts WHERE run_id=$1 AND kind='model'`, result.Run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved ModelResponse
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.ProviderSource != response.ProviderSource {
		t.Fatal("existing Attempt JSON lost binding")
	}
}

func TestGeminiThoughtSignaturesAreScopedAndPrivate(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				var body openAIRequest
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
				}
				if calls == 2 {
					if len(body.Messages[0].ToolCalls) != 1 || !strings.Contains(string(body.Messages[0].ToolCalls[0].ExtraContent), "opaque-google") {
						t.Error("thought signature was lost", string(raw))
					}
					if body.Messages[0].ReasoningContent != nil {
						t.Error("signature-only continuation added reasoning_content")
					}
				}
				if calls == 3 && strings.Contains(string(raw), "opaque-google") {
					t.Error("signature crossed credential profile")
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					if calls == 1 {
						writeSSE(w, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read1","type":"function","function":{"name":"file_read","arguments":"{}"}}]},"finish_reason":null}]}`)
						writeSSE(w, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"extra_content":{"google":{"thought_signature":"opaque-google"}}}]},"finish_reason":"tool_calls"}]}`)
					} else {
						writeSSE(w, `{"choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":"stop"}]}`)
					}
					fmt.Fprint(w, "data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					if calls == 1 {
						fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"read1","type":"function","function":{"name":"file_read","arguments":"{}"},"extra_content":{"google":{"thought_signature":"opaque-google"}}}]},"finish_reason":"tool_calls"}]}`)
					} else {
						fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"visible"},"finish_reason":"stop"}]}`)
					}
				}
			}))
			defer server.Close()
			cfg := ModelConfig{Provider: "openai-compatible", Model: "gemini-3.8-flash"}
			cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
			if !stream {
				cfg.Capabilities.Streaming = "none"
			}
			client, err := NewOpenAICompatibleClient(cfg, mustResolved(NewResolvedKey(server.URL, "")), nil)
			if err != nil {
				t.Fatal(err)
			}
			req := ModelRequest{MaxOutputTokens: 100, Messages: []ModelMessage{{Role: "user", Content: "read"}}, Tools: []ToolDefinition{{Name: "file_read", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
			var shown strings.Builder
			first, err := client.Generate(t.Context(), req, func(value string) { shown.WriteString(value) })
			if err != nil || first.ChatContinuation == nil || shown.Len() != 0 {
				t.Fatal("signature was not privately recorded", err)
			}
			req.Messages = []ModelMessage{{Role: "assistant", ToolCalls: first.ToolCalls, ChatContinuation: first.ChatContinuation}, {Role: "tool", ToolCallID: "read1", Content: "read result"}}
			if _, err = client.Generate(t.Context(), req, nil); err != nil {
				t.Fatal(err)
			}
			client.key.ProfileID = "different-profile"
			if _, err = client.Generate(t.Context(), req, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatPreservedThinkingWithoutOfferedTools(t *testing.T) {
	for _, model := range []string{"kimi-k3.1-pro", "glm-5.3", "MiniMax-M3.1-Flash"} {
		t.Run(model, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body openAIRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Tools) != 0 {
					t.Error("ordinary conversation unexpectedly offered tools")
				}
				if calls == 2 && (body.Messages[1].ReasoningContent == nil || *body.Messages[1].ReasoningContent != "preserved-private") {
					t.Error("always-preserved thinking was lost in ordinary multi-turn chat")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"preserved-private","content":"visible"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			cfg := ModelConfig{Provider: "openai-compatible", Model: model, Capabilities: ModelCapabilities{Streaming: "none"}}
			cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
			client, err := NewOpenAICompatibleClient(cfg, mustResolved(NewResolvedKey(server.URL, "")), nil)
			if err != nil {
				t.Fatal(err)
			}
			req := ModelRequest{MaxOutputTokens: 100, Messages: []ModelMessage{{Role: "user", Content: "hello"}}}
			first, err := client.Generate(t.Context(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Messages = append(req.Messages, ModelMessage{Role: "assistant", Content: first.Content, ChatContinuation: first.ChatContinuation}, ModelMessage{Role: "user", Content: "next"})
			if _, err := client.Generate(t.Context(), req, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponsesPrivateItemsAreScopedAndEffortEchoIsIndependent(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body responsesRequest
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		if body.Reasoning == nil || body.Reasoning.Effort != "low" || len(body.Include) != 1 || body.Include[0] != "reasoning.encrypted_content" {
			t.Error("Sol native wire mismatch", string(raw))
		}
		if calls == 2 && !strings.Contains(string(raw), "opaque-reasoning") {
			t.Error("same-endpoint continuation lost")
		}
		if calls >= 3 && strings.Contains(string(raw), "opaque-reasoning") {
			t.Error("Responses reasoning crossed endpoint binding")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			fmt.Fprint(w, `{"id":"r1","status":"completed","reasoning":{"effort":"medium"},"output":[{"type":"reasoning","id":"rs1","encrypted_content":"opaque-reasoning"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}]}`)
		} else {
			fmt.Fprint(w, `{"id":"r2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}]}`)
		}
	}))
	defer server.Close()
	cfg := ModelConfig{Provider: "openai-compatible", Model: "gpt-6.1-sol", Capabilities: ModelCapabilities{Protocol: "responses", Streaming: "none"}}
	cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
	client, err := NewResponsesClient(cfg, mustResolved(NewResolvedKey(server.URL, "")), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := ModelRequest{MaxOutputTokens: 100, Reasoning: &ReasoningSelection{Effort: "low"}, Messages: []ModelMessage{{Role: "user", Content: "hello"}}}
	first, err := client.Generate(t.Context(), req, nil)
	if err != nil || first.ProviderSource == "" || first.Provider.RequestedReasoning.Effort != "low" || first.Provider.ReportedReasoningEffort != "medium" {
		t.Fatal("request and reported effort conflated", err)
	}
	req.Messages = []ModelMessage{{Role: "assistant", Content: first.Content, ProviderItems: first.ProviderItems, ProviderSource: first.ProviderSource}, {Role: "user", Content: "next"}}
	second, err := client.Generate(t.Context(), req, nil)
	if err != nil || second.Provider.ReportedReasoningEffort != "" {
		t.Fatal("missing echo fabricated", err)
	}
	client.key.ProfileID = "different-profile"
	if _, err = client.Generate(t.Context(), req, nil); err != nil {
		t.Fatal(err)
	}
	client.key.ProfileID = ""
	req.Messages[0].ProviderSource = "" // Old unbound data cannot assert an origin.
	if _, err = client.Generate(t.Context(), req, nil); err != nil {
		t.Fatal(err)
	}
}
