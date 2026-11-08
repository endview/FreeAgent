package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReasoningWireContracts(t *testing.T) {
	for _, tc := range []struct {
		name, control, protocol string
		selection               ReasoningSelection
		field                   string
		want                    any
	}{
		{"default", "deepseek", "chat", ReasoningSelection{Mode: "default"}, "thinking", nil},
		{"deepseek-off", "deepseek", "chat", ReasoningSelection{Mode: "disabled"}, "thinking", map[string]any{"type": "disabled"}},
		{"deepseek-on", "deepseek", "chat", ReasoningSelection{Mode: "enabled", Effort: "high"}, "reasoning_effort", "high"},
		{"openai-chat", "openai", "chat", ReasoningSelection{Effort: "low"}, "reasoning_effort", "low"},
		{"responses-off", "deepseek", "responses", ReasoningSelection{Mode: "disabled"}, "reasoning", map[string]any{"effort": "none"}},
		{"openai-responses", "openai", "responses", ReasoningSelection{Effort: "high"}, "reasoning", map[string]any{"effort": "high"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(mustJSON(body[tc.field])) != string(mustJSON(tc.want)) {
					t.Errorf("wire %s=%v want=%v", tc.field, body[tc.field], tc.want)
				}
				if tc.protocol == "responses" && (body["thinking"] != nil || body["reasoning_effort"] != nil) {
					t.Error("chat fields leaked into Responses")
				}
				if tc.control == "openai" && body["thinking"] != nil {
					t.Error("DeepSeek field leaked into OpenAI")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.protocol == "responses" {
					fmt.Fprint(w, `{"id":"r1","status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12,"output_tokens_details":{"reasoning_tokens":4}}}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"private thought"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12,"completion_tokens_details":{"reasoning_tokens":4}}}`)
				}
			}))
			defer server.Close()
			caps := ModelCapabilities{Protocol: tc.protocol, Streaming: "none", Reasoning: ReasoningCapabilities{Control: tc.control, Efforts: "low,high,max"}}
			config := ModelConfig{Provider: "openai-compatible", Model: "fixture", Capabilities: caps}
			key := mustResolved(NewResolvedKey(server.URL, "fixture-secret"))
			var client ModelClient
			var err error
			if tc.protocol == "responses" {
				client, err = NewResponsesClient(config, key, nil)
			} else {
				client, err = NewOpenAICompatibleClient(config, key, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Generate(t.Context(), ModelRequest{Reasoning: &tc.selection, MaxOutputTokens: 20, Messages: []ModelMessage{{Role: "user", Content: "hi"}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.Content != "answer" || response.Provider.RequestedReasoning == nil || *response.Provider.RequestedReasoning != tc.selection || response.Usage.ReasoningTokens == nil || *response.Usage.ReasoningTokens != 4 {
				t.Fatalf("response contract: %+v", response)
			}
		})
	}
}

func TestReasoningChatSSEAndScopedToolContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body openAIRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if calls == 2 && (len(body.Messages) < 2 || body.Messages[0].ReasoningContent == nil || *body.Messages[0].ReasoningContent != "private thought" || body.Messages[1].ToolCallID != "read1") {
					t.Error("tool follow-up lost exact private continuation")
				}
				if calls == 3 && body.Messages[0].ReasoningContent != nil {
					t.Error("foreign private continuation replayed")
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					writeSSE(w, `{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"private "},"finish_reason":null}]}`)
					writeSSE(w, `{"choices":[{"index":0,"delta":{"reasoning_content":"thought"},"finish_reason":null}]}`)
					if calls == 1 {
						writeSSE(w, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"a\"}"}}]},"finish_reason":"tool_calls"}]}`)
					} else {
						writeSSE(w, `{"choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":"stop"}]}`)
					}
					fmt.Fprint(w, "data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					if calls == 1 {
						fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"private thought","tool_calls":[{"id":"read1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"a\"}"}}]},"finish_reason":"tool_calls"}]}`)
					} else {
						fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"private thought","content":"visible"},"finish_reason":"stop"}]}`)
					}
				}
			}))
			defer server.Close()
			caps := ModelCapabilities{Reasoning: ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max"}}
			if !stream {
				caps.Streaming = "none"
			}
			client, err := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Model: "fixture", Capabilities: caps}, mustResolved(NewResolvedKey(server.URL, "")), nil)
			if err != nil {
				t.Fatal(err)
			}
			req := ModelRequest{MaxOutputTokens: 50, Tools: []ToolDefinition{{Name: "file_read", InputSchema: json.RawMessage(`{"type":"object"}`)}}, Messages: []ModelMessage{{Role: "user", Content: "read"}}}
			var visible strings.Builder
			first, err := client.Generate(t.Context(), req, func(s string) { visible.WriteString(s) })
			if err != nil || first.ChatContinuation == nil || visible.Len() != 0 {
				t.Fatalf("first=%+v emit=%q err=%v", first, visible.String(), err)
			}
			req.Messages = []ModelMessage{{Role: "assistant", ToolCalls: first.ToolCalls, ChatContinuation: first.ChatContinuation}, {Role: "tool", ToolCallID: "read1", Content: "result"}}
			if _, err := client.Generate(t.Context(), req, func(s string) { visible.WriteString(s) }); err != nil {
				t.Fatal(err)
			}
			foreign := *first.ChatContinuation
			foreign.Source = contentHash([]byte("other provider"))
			req.Messages[0].ChatContinuation = &foreign
			if _, err := client.Generate(t.Context(), req, nil); err != nil {
				t.Fatal(err)
			}
			if visible.String() != "visible" {
				t.Fatal("private continuation exposed as answer")
			}
		})
	}
}

func TestReasoningSelectionRejectsUnsupportedOptions(t *testing.T) {
	for _, tc := range []struct {
		caps      ReasoningCapabilities
		selection ReasoningSelection
		protocol  string
	}{
		{ReasoningCapabilities{}, ReasoningSelection{Effort: "high"}, "chat"},
		{ReasoningCapabilities{Control: "openai", Efforts: "low,high"}, ReasoningSelection{Mode: "disabled"}, "responses"},
		{ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max"}, ReasoningSelection{Effort: "medium"}, "chat"},
		{ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max"}, ReasoningSelection{Mode: "disabled", Effort: "high"}, "chat"},
		{ReasoningCapabilities{Control: "openai", Efforts: "low,high"}, ReasoningSelection{Mode: "enabled"}, "chat"},
	} {
		if _, _, err := tc.caps.parameters(tc.selection, tc.protocol); err == nil {
			t.Fatalf("accepted unsupported selection %+v", tc)
		}
	}
}

func TestReasoningEmptyChatContinuationRemainsPresent(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body openAIRequest
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid fixture request")
				}
				if calls == 2 && (body.Messages[0].ReasoningContent == nil || *body.Messages[0].ReasoningContent != "") {
					t.Error("an explicitly empty continuation was omitted")
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					writeSSE(w, `{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"","content":"answer"},"finish_reason":"stop"}]}`)
					fmt.Fprint(w, "data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"","content":"answer"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			config := ModelConfig{Provider: "openai-compatible", Model: "fixture"}
			if !stream {
				config.Capabilities.Streaming = "none"
			}
			client, err := NewOpenAICompatibleClient(config, mustResolved(NewResolvedKey(server.URL, "")), nil)
			if err != nil {
				t.Fatal(err)
			}
			req := ModelRequest{MaxOutputTokens: 50, Tools: []ToolDefinition{{Name: "file_read", InputSchema: json.RawMessage(`{"type":"object"}`)}}, Messages: []ModelMessage{{Role: "user", Content: "hi"}}}
			response, err := client.Generate(t.Context(), req, nil)
			if err != nil || response.ChatContinuation == nil || response.ChatContinuation.Content != "" {
				t.Fatal("empty continuation not retained", err)
			}
			req.Messages = []ModelMessage{{Role: "assistant", Content: response.Content, ChatContinuation: response.ChatContinuation}, {Role: "user", Content: "next"}}
			if _, err := client.Generate(t.Context(), req, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReasoningRunSnapshotAuxiliaryAndSummaryPrivacy(t *testing.T) {
	var main, aux int
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		if req.Purpose == "" {
			main++
			if req.Reasoning == nil || req.Reasoning.Effort != "high" {
				t.Error("main selection lost")
			}
		} else {
			aux++
			if req.Reasoning == nil || req.Reasoning.Mode != "disabled" {
				t.Error("auxiliary inherited main selection")
			}
		}
		return ModelResponse{Content: "ok"}, nil
	}))
	core.Config.Models["default"] = ModelConfig{Provider: "openai-compatible", Key: "fixture-key", Model: "fixture", Capabilities: ModelCapabilities{Reasoning: ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max", Auxiliary: ReasoningSelection{Mode: "disabled"}}}}
	// Injected clients need a valid local profile binding; no live credential is used.
	keys := testKeyStore(t)
	core.Keys = keys
	view, err := keys.Add(KeyInput{Name: "reasoning-fixture", BaseURL: "http://127.0.0.1:1", Protocol: KeyProtocolOpenAICompatible, Source: KeySourceNone})
	if err != nil {
		t.Fatal(err)
	}
	model := core.Config.Models["default"]
	model.Key = view.ID
	core.Config.Models["default"] = model
	enableTestProviders(&core.Config)
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, core)
	tasks := NewTaskService(core)
	selection := ReasoningSelection{Effort: "high"}
	result, err := tasks.SubmitWithOptions(t.Context(), chat, "reasoning", "hello", RunOptions{Reasoning: &selection})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := core.forRun(result.Run)
	if err != nil {
		t.Fatal(err)
	}
	if restored.reasoning == nil || *restored.reasoning != selection {
		t.Fatal("snapshot lost selection")
	}
	binding, err := restored.binding()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := core.Store.CreateRun(t.Context(), chat, "aux", "aux", result.Run.ConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = restored.generate(t.Context(), run, ModelRequest{ModelID: binding.ID, ModelRevision: binding.Revision, MaxOutputTokens: 10, Purpose: "compaction", Messages: []ModelMessage{{Role: "user", Content: "summarize"}}})
	if err != nil {
		t.Fatal(err)
	}
	private := &ChatContinuation{Source: contentHash([]byte("provider")), Content: "PRIVATE-CONTINUATION"}
	summary := summaryRequest(binding, restored.limits(), 100, "", []Message{{ModelMessage: ModelMessage{Role: "assistant", Content: "visible", ChatContinuation: private}}})
	if strings.Contains(string(mustJSON(summary)), private.Content) {
		t.Fatal("private continuation entered summary")
	}
	if main != 1 || aux != 1 {
		t.Fatalf("main=%d aux=%d", main, aux)
	}
}
