package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const responsesFixtureKey = "responses-fixture-key"

func newResponsesFixture(t *testing.T, caps ModelCapabilities, handler http.HandlerFunc) (*ResponsesClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	key, err := NewResolvedKeyForProtocol(server.URL, responsesFixtureKey, KeyProtocolOpenAICompatible)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	config := ModelConfig{Provider: "openai-compatible", Model: "deepseek-v4-pro", Capabilities: caps}
	client, err := NewResponsesClient(config, key, nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, server
}

func writeResponsesEvent(writer io.Writer, eventType string, sequence int, data string) {
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &payload) != nil {
		return
	}
	payload["sequence_number"] = json.RawMessage(fmt.Sprintf("%d", sequence))
	encoded, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", eventType, encoded)
}

func TestResponsesJSONWebSearchAndMetadata(t *testing.T) {
	client, server := newResponsesFixture(t, ModelCapabilities{
		Protocol: "responses", Streaming: "none", Usage: "required", WebSearch: "supported",
	}, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" || request.Header.Get("Authorization") != "Bearer "+responsesFixtureKey {
			t.Errorf("request path/auth=%q/%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var wire responsesRequest
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Model != "deepseek-v4-pro" || wire.Stream || wire.Instructions != "system instruction" ||
			wire.MaxOutputTokens != 64 || len(wire.Input) != 1 || len(wire.Tools) != 2 ||
			wire.Tools[0].Type != "function" || wire.Tools[0].Name != "lookup" || wire.Tools[1].Type != "web_search" {
			t.Errorf("unexpected Responses request: %+v", wire)
		}
		var input struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(wire.Input[0], &input); err != nil || input.Type != "message" || input.Role != "user" || input.Content != "latest news" {
			t.Errorf("unexpected Responses input: %s", wire.Input[0])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"resp-1","status":"completed","model":"deepseek-v4-pro","output":[{"type":"web_search_call","id":"ws-1","status":"completed","action":{"type":"search","query":"latest DeepSeek API"}},{"type":"message","id":"msg-1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"回答"}]}],"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)
	})
	defer server.Close()

	request := ModelRequest{
		Messages: []ModelMessage{
			{Role: "system", Content: "system instruction"},
			{Role: "user", Content: "latest news"},
		},
		Tools:           []ToolDefinition{{Name: "lookup", Description: "lookup data", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxOutputTokens: 64,
	}
	var emitted strings.Builder
	response, err := client.Generate(context.Background(), request, func(text string) { emitted.WriteString(text) })
	if err != nil || response.Content != "回答" || emitted.String() != "回答" || response.Usage == nil || response.Usage.TotalTokens != 30 {
		t.Fatalf("Responses JSON result=%+v emitted=%q err=%v", response, emitted.String(), err)
	}
	if response.Provider == nil || response.Provider.Protocol != "responses" || response.Provider.ResponseStatus != "completed" ||
		response.Provider.FinishReason != "stop" || response.Provider.UsageStatus != "reported" || len(response.Provider.WebSearchCalls) != 1 ||
		response.Provider.WebSearchCalls[0].ID != "ws-1" || response.Provider.WebSearchCalls[0].Status != "completed" ||
		response.Provider.WebSearchCalls[0].Query != "latest DeepSeek API" {
		t.Fatalf("Responses metadata=%+v", response.Provider)
	}
	if len(response.ProviderItems) != 2 || !strings.Contains(string(response.ProviderItems[0]), `"action"`) {
		t.Fatalf("web_search_call output item was not preserved: %+v", response.ProviderItems)
	}
}

func TestResponsesSSEEventsAndWebSearch(t *testing.T) {
	client, server := newResponsesFixture(t, ModelCapabilities{
		Protocol: "responses", Streaming: "supported", Usage: "required", WebSearch: "supported",
	}, func(writer http.ResponseWriter, request *http.Request) {
		var wire responsesRequest
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if !wire.Stream || len(wire.Tools) != 1 || wire.Tools[0].Type != "web_search" {
			t.Errorf("stream/search declaration lost: %+v", wire)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writeResponsesEvent(writer, "response.created", 0, `{"type":"response.created"}`)
		writeResponsesEvent(writer, "response.output_item.added", 1, `{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws-1","status":"in_progress","action":{"type":"search","query":"current status"}}}`)
		writeResponsesEvent(writer, "response.web_search_call.in_progress", 2, `{"type":"response.web_search_call.in_progress","item_id":"ws-1"}`)
		writeResponsesEvent(writer, "response.web_search_call.searching", 3, `{"type":"response.web_search_call.searching","item_id":"ws-1"}`)
		writeResponsesEvent(writer, "response.output_text.delta", 4, `{"type":"response.output_text.delta","delta":"联网结果"}`)
		writeResponsesEvent(writer, "response.web_search_call.completed", 5, `{"type":"response.web_search_call.completed","item_id":"ws-1"}`)
		writeResponsesEvent(writer, "response.completed", 6, `{"type":"response.completed","response":{"id":"resp-sse","status":"completed","model":"deepseek-v4-pro","output":[{"type":"web_search_call","id":"ws-1","status":"completed"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"联网结果"}]}],"usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10}}}`)
	})
	defer server.Close()

	var emitted strings.Builder
	response, err := client.Generate(context.Background(), ModelRequest{
		Messages:        []ModelMessage{{Role: "user", Content: "search"}},
		MaxOutputTokens: 64,
	}, func(text string) { emitted.WriteString(text) })
	if err != nil || response.Content != "联网结果" || emitted.String() != "联网结果" || response.Provider == nil || !response.Provider.Streamed ||
		response.Provider.ResponseStatus != "completed" || len(response.Provider.WebSearchCalls) != 1 || response.Provider.WebSearchCalls[0].Status != "completed" || response.Provider.WebSearchCalls[0].Query != "current status" {
		t.Fatalf("Responses SSE result=%+v emitted=%q err=%v", response, emitted.String(), err)
	}
}

// The items below follow the shape recorded from DeepSeek on 2026-10-21: a
// "search" action with a queries list and an "open_page" action with a url,
// both carrying an internal ws_call_id marker.
const (
	deepSeekSearchItem = `{"type":"web_search_call","id":"call_00_a","status":"completed","action":{"type":"search","queries":["DeepSeek API pricing","ws_call_id=call_00_a"]}}`
	deepSeekOpenItem   = `{"type":"web_search_call","id":"call_01_b","status":"completed","action":{"type":"open_page","url":"https://api-docs.deepseek.com/quick_start/pricing#ws_call_id=call_01_b"}}`
)

func TestResponsesDeepSeekSearchActions(t *testing.T) {
	want := []ProviderWebSearchCall{
		{ID: "call_00_a", Status: "completed", Action: "search", Query: "DeepSeek API pricing"},
		{ID: "call_01_b", Status: "completed", Action: "open_page", URL: "https://api-docs.deepseek.com/quick_start/pricing"},
	}
	final := `{"id":"resp-ds","status":"completed","model":"deepseek-v4-pro","output":[` + deepSeekSearchItem + "," + deepSeekOpenItem +
		`,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	for _, streaming := range []string{"none", "supported"} {
		t.Run(streaming, func(t *testing.T) {
			client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: streaming, WebSearch: "supported"},
				func(writer http.ResponseWriter, request *http.Request) {
					if streaming == "none" {
						writer.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(writer, final)
						return
					}
					writer.Header().Set("Content-Type", "text/event-stream")
					writeResponsesEvent(writer, "response.output_item.added", 0, `{"type":"response.output_item.added","item":{"type":"web_search_call","id":"call_00_a","status":"in_progress"}}`)
					writeResponsesEvent(writer, "response.output_item.done", 1, `{"type":"response.output_item.done","item":`+deepSeekSearchItem+`}`)
					writeResponsesEvent(writer, "response.output_item.done", 2, `{"type":"response.output_item.done","item":`+deepSeekOpenItem+`}`)
					writeResponsesEvent(writer, "response.completed", 3, `{"type":"response.completed","response":`+final+`}`)
				})
			defer server.Close()
			response, err := client.Generate(context.Background(), contractRequest(), nil)
			if err != nil || response.Provider == nil || fmt.Sprint(response.Provider.WebSearchCalls) != fmt.Sprint(want) {
				t.Fatalf("search metadata=%+v err=%v", response.Provider, err)
			}
			// The marker stays in the raw item that is replayed to DeepSeek.
			if len(response.ProviderItems) != 3 || !strings.Contains(string(response.ProviderItems[1]), "#ws_call_id=call_01_b") {
				t.Fatalf("raw search items were altered: %s", response.ProviderItems)
			}
		})
	}
}

func TestResponsesFunctionCallContinuationReplaysProviderItems(t *testing.T) {
	callCount := 0
	client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "none"}, func(writer http.ResponseWriter, request *http.Request) {
		callCount++
		var wire responsesRequest
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if callCount == 2 {
			if len(wire.Input) != 3 {
				t.Fatalf("continuation input count=%d: %+v", len(wire.Input), wire.Input)
			}
			var functionCall, functionOutput struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Output string `json:"output"`
			}
			if err := json.Unmarshal(wire.Input[1], &functionCall); err != nil || functionCall.Type != "function_call" || functionCall.CallID != "call-1" {
				t.Errorf("saved function call was not replayed: %s", wire.Input[1])
			}
			if err := json.Unmarshal(wire.Input[2], &functionOutput); err != nil || functionOutput.Type != "function_call_output" || functionOutput.CallID != "call-1" || functionOutput.Output != "tool output" {
				t.Errorf("function output was not appended: %s", wire.Input[2])
			}
			if strings.Contains(string(wire.Input[1]), `"role":"assistant"`) {
				t.Error("continuation duplicated the assistant message instead of replaying provider items")
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			_, _ = io.WriteString(writer, `{"id":"resp-call","status":"completed","model":"deepseek-v4-pro","output":[{"type":"function_call","id":"fc-1","call_id":"call-1","name":"lookup","arguments":"{\"q\":\"status\"}"}]}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"resp-answer","status":"completed","model":"deepseek-v4-pro","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
	})
	defer server.Close()

	first, err := client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "lookup"}}, MaxOutputTokens: 64}, nil)
	if err != nil || len(first.ToolCalls) != 1 || len(first.ProviderItems) != 1 {
		t.Fatalf("first function response=%+v err=%v", first, err)
	}
	second, err := client.Generate(context.Background(), ModelRequest{
		Messages: []ModelMessage{
			{Role: "user", Content: "lookup"},
			{Role: "assistant", ToolCalls: first.ToolCalls, ProviderItems: first.ProviderItems},
			{Role: "tool", ToolCallID: "call-1", Content: "tool output"},
		},
		MaxOutputTokens: 64,
	}, nil)
	if err != nil || second.Content != "done" || callCount != 2 {
		t.Fatalf("continuation response=%+v calls=%d err=%v", second, callCount, err)
	}
}

func TestResponsesFunctionNameContract(t *testing.T) {
	base := ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 16}
	for _, name := range []string{"bad name", "lookup/one", "查询"} {
		request := base
		request.Tools = []ToolDefinition{{Name: name}}
		if _, err := responsesRequestFrom(request, "deepseek-v4-pro", false, false); err == nil {
			t.Fatalf("accepted invalid Responses function name %q", name)
		}
	}
	request := base
	request.Tools = []ToolDefinition{{Name: "lookup"}, {Name: "lookup"}}
	if _, err := responsesRequestFrom(request, "deepseek-v4-pro", false, false); err == nil {
		t.Fatal("accepted duplicate Responses function names")
	}
}

func TestResponsesIncompleteFailedAndMalformedStreams(t *testing.T) {
	t.Run("incomplete output limit", func(t *testing.T) {
		client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "none"}, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"resp-limit","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]}`)
		})
		defer server.Close()
		response, err := client.Generate(context.Background(), contractRequest(), nil)
		var callErr *CallError
		if err == nil || !errors.As(err, &callErr) || !callErr.Certain || callErr.Category != "output_limit" || response.Content != "partial" || response.Provider.FinishReason != "output_limit" {
			t.Fatalf("incomplete result=%+v err=%v", response, err)
		}
	})

	t.Run("failed response", func(t *testing.T) {
		client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "none"}, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"resp-failed","status":"failed","error":{"type":"server_error","message":"provider failed"}}`)
		})
		defer server.Close()
		_, err := client.Generate(context.Background(), contractRequest(), nil)
		var callErr *CallError
		if err == nil || !errors.As(err, &callErr) || !callErr.Certain || callErr.Category != "unavailable" || !callErr.Retryable {
			t.Fatalf("failed response error=%v", err)
		}
	})

	t.Run("stream without terminal event", func(t *testing.T) {
		client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "supported"}, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writeResponsesEvent(writer, "response.output_text.delta", 0, `{"type":"response.output_text.delta","delta":"partial"}`)
		})
		defer server.Close()
		response, err := client.Generate(context.Background(), contractRequest(), nil)
		var callErr *CallError
		if err == nil || !errors.As(err, &callErr) || callErr.Category != "transport" || callErr.Certain || len(response.ToolCalls) != 0 {
			t.Fatalf("partial stream result=%+v err=%v", response, err)
		}
	})

	t.Run("sequence regression", func(t *testing.T) {
		client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "supported"}, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writeResponsesEvent(writer, "response.created", 1, `{"type":"response.created"}`)
			writeResponsesEvent(writer, "response.output_text.delta", 1, `{"type":"response.output_text.delta","delta":"bad"}`)
		})
		defer server.Close()
		_, err := client.Generate(context.Background(), contractRequest(), nil)
		var callErr *CallError
		if err == nil || !errors.As(err, &callErr) || callErr.Category != "protocol" {
			t.Fatalf("sequence regression error=%v", err)
		}
	})

	t.Run("invalid usage", func(t *testing.T) {
		client, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses", Streaming: "none"}, func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":99}}`)
		})
		defer server.Close()
		_, err := client.Generate(context.Background(), contractRequest(), nil)
		var callErr *CallError
		if err == nil || !errors.As(err, &callErr) || callErr.Category != "protocol" {
			t.Fatalf("invalid usage error=%v", err)
		}
	})
}
