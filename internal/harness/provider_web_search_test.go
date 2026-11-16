package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDeepSeekNativeEndpointPayloadAndRouting(t *testing.T) {
	on := true
	r := ModelRequest{WebSearch: &on, Messages: []ModelMessage{{Role: "user", Content: "search"}}, MaxOutputTokens: 1024, Tools: []ToolDefinition{{Name: "read_marker", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	caps := ModelCapabilities{Protocol: "responses", WebSearch: "supported"}
	wire, e := responsesRequestForEndpoint(r, "deepseek-flash", caps, true, "https://api.deepseek.com")
	if e != nil {
		t.Fatal(e)
	}
	types := []string{}
	for _, tool := range wire.Tools {
		types = append(types, tool.Type)
	}
	if strings.Join(types, ",") != "function,deepseek_web_search,deepseek_read_url" {
		t.Fatalf("wrong native tools: %v", types)
	}
	if _, e := responsesRequestForEndpoint(r, "deepseek-v4-pro", caps, true, "https://api.deepseek.com"); e == nil {
		t.Fatal("unsupported official model guessed")
	}
	model := ModelConfig{Provider: "openai-compatible", Model: "deepseek-flash", Capabilities: caps}
	if !nativeSearchAvailable(model, "https://api.deepseek.com", false) || nativeSearchAvailable(model, "https://relay.example", false) || !nativeSearchAvailable(model, "https://relay.example", true) {
		t.Fatal("automatic/explicit endpoint boundaries changed")
	}
	r.WebSearch = new(bool)
	wire, e = responsesRequestForEndpoint(r, "deepseek-flash", caps, true, "https://api.deepseek.com")
	if e != nil || len(wire.Tools) != 1 || wire.Tools[0].Type != "function" {
		t.Fatal("explicit off still offered native search")
	}
}

func TestAnthropicNativeResultsStayOpaqueAndNeverBecomeLocalCalls(t *testing.T) {
	raw := `{"id":"native","model":"claude-fixture","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5,"server_tool_use":{"web_search_requests":1}},"content":[{"type":"server_tool_use","id":"search-1","name":"web_search","input":{"query":"public docs"},"caller":{"type":"direct"}},{"type":"web_search_tool_result","tool_use_id":"search-1","content":[{"type":"web_search_result","url":"https://example.org/docs","title":"Source","encrypted_content":"opaque-page"}],"caller":{"type":"direct"}},{"type":"text","text":"根据来源","citations":[{"type":"web_search_result_location","url":"https://example.org/docs","title":"Source","encrypted_index":"opaque-index"}]},{"type":"tool_use","id":"file-1","name":"read_marker","input":{}}]}`
	result, e := decodeAnthropicJSON(strings.NewReader(raw), "")
	if e != nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "read_marker" {
		t.Fatalf("native tool dispatched locally: %+v %v", result.ToolCalls, e)
	}
	if result.Provider == nil || len(result.Provider.WebSearchCalls) != 1 || result.Provider.WebSearchCalls[0].Status != "completed" || !strings.Contains(result.Content, "[Source](https://example.org/docs)") {
		t.Fatal("missing native evidence/citation")
	}
	if result.Usage.ServerTools == nil || result.Usage.ServerTools.SearchRequests == nil || *result.Usage.ServerTools.SearchRequests != 1 {
		t.Fatal("server search usage lost")
	}
	if result.ChatContinuation == nil {
		t.Fatal("native context without thinking was lost")
	}
	on := true
	enabled, e := anthropicRequestFrom(ModelRequest{WebSearch: &on, Messages: []ModelMessage{{Role: "user", Content: "search"}}, MaxOutputTokens: 1024}, "claude-fixture", ModelCapabilities{WebSearch: "supported"})
	if e != nil || len(enabled.Tools) != 1 || enabled.Tools[0].Type != "web_search_20250305" || enabled.Tools[0].MaxUses != 3 {
		t.Fatal("native tool definition mismatch", e)
	}
	encodedRequest, _ := json.Marshal(enabled)
	if bytes.Contains(encodedRequest, []byte("input_schema")) {
		t.Fatal("server tool received a client function schema")
	}
	if bytes.Contains(encodedRequest, []byte("temperature")) || bytes.Contains(encodedRequest, []byte("top_k")) {
		t.Fatal("sampling override added to native thinking request")
	}
	off := false
	r := ModelRequest{WebSearch: &off, Messages: []ModelMessage{{Role: "user", Content: "search and read"}, {Role: "assistant", Content: result.Content, ToolCalls: result.ToolCalls, ChatContinuation: result.ChatContinuation}, {Role: "tool", ToolCallID: "file-1", Content: "marker"}}, MaxOutputTokens: 1024}
	wire, e := anthropicRequestFrom(r, "claude-fixture", ModelCapabilities{WebSearch: "supported"})
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(wire)
	if !bytes.Contains(encoded, []byte(`"encrypted_content":"opaque-page"`)) || !bytes.Contains(encoded, []byte(`"encrypted_index":"opaque-index"`)) || !bytes.Contains(encoded, []byte(`"caller":{"type":"direct"}`)) || len(wire.Tools) != 0 {
		t.Fatal("continuation lost opaque fields or search-off did not apply")
	}
	paused := strings.Replace(raw, `"stop_reason":"tool_use"`, `"stop_reason":"pause_turn"`, 1)
	response, e := decodeAnthropicJSON(strings.NewReader(paused), "")
	var failure *CallError
	if !errors.As(e, &failure) || !failure.Certain || failure.Category != "provider_pause" || response.ChatContinuation == nil || response.Provider.FinishReason != "paused" {
		t.Fatal("pause became completion or replay permission", e)
	}
}

func TestAnthropicNativeSSECitationsAndUsage(t *testing.T) {
	var stream strings.Builder
	event := func(name string, value any) {
		raw, _ := json.Marshal(value)
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", name, raw)
	}
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "sse-native", "model": "claude-fixture", "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
	blocks := []map[string]any{{"type": "server_tool_use", "id": "s1", "name": "web_search", "input": map[string]any{}, "vendor_context": "opaque-server-metadata"}, {"type": "web_search_tool_result", "tool_use_id": "s1", "content": []map[string]string{{"type": "web_search_result", "url": "https://example.org", "encrypted_content": "opaque"}}}, {"type": "text", "text": "", "vendor_context": "opaque-text-metadata"}}
	for i, block := range blocks {
		event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": block})
		if i == 0 {
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]string{"type": "input_json_delta", "partial_json": `{"query":"public docs"}`}})
		}
		if i == 2 {
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]string{"type": "text_delta", "text": "中文来源"}})
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "citations_delta", "citation": map[string]string{"type": "web_search_result_location", "url": "https://example.org", "title": "Example", "encrypted_index": "i1"}}})
		}
		event("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]string{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 5, "server_tool_use": map[string]int{"web_search_requests": 1}}})
	event("message_stop", map[string]string{"type": "message_stop"})
	response, e := decodeAnthropicStream(strings.NewReader(stream.String()), "", nil)
	if e != nil || len(response.ToolCalls) != 0 || !strings.Contains(response.Content, "[Example](https://example.org)") || response.Usage.ServerTools == nil || *response.Usage.ServerTools.SearchRequests != 1 {
		t.Fatalf("SSE native lost data: %+v %v", response, e)
	}
	if response.ChatContinuation == nil || !bytes.Contains(mustJSON(response.ChatContinuation), []byte("opaque")) {
		t.Fatal("SSE result signature lost")
	}
	continuation := mustJSON(response.ChatContinuation)
	for _, expected := range []string{"opaque-server-metadata", "opaque-text-metadata", `"query":"public docs"`, `"encrypted_index":"i1"`} {
		if !bytes.Contains(continuation, []byte(expected)) {
			t.Fatalf("SSE continuation lost %s", expected)
		}
	}
}

func TestResponsesReportedServerUsage(t *testing.T) {
	for _, test := range []struct {
		name, server string
		invalid      bool
	}{
		{name: "absent"},
		{name: "reported", server: `,"server_tool_use":{"tool_search":2,"tool_fetch":1}`},
		{name: "negative", server: `,"server_tool_use":{"tool_search":-1}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"id":"usage","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15` + test.server + `}}`
			response, err := decodeResponsesJSON(strings.NewReader(raw), "")
			if test.invalid {
				var failure *CallError
				if !errors.As(err, &failure) || failure.Category != "protocol" {
					t.Fatal("invalid usage accepted", err)
				}
				return
			}
			if err != nil || response.Usage == nil {
				t.Fatal("missing reported usage", err)
			}
			if test.server == "" {
				if response.Usage.ServerTools != nil {
					t.Fatal("unreported server usage invented")
				}
				return
			}
			usage := response.Usage.ServerTools
			if usage == nil || usage.SearchRequests == nil || usage.FetchRequests == nil || *usage.SearchRequests != 2 || *usage.FetchRequests != 1 {
				t.Fatal("reported server usage lost")
			}
		})
	}
}
