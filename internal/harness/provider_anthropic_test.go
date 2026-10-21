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

const anthropicFixtureKey = "anthropic-fixture-key"

func newAnthropicFixture(t *testing.T, handler http.HandlerFunc) (*AnthropicClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	key, err := NewResolvedKeyForProtocol(server.URL, anthropicFixtureKey, KeyProtocolAnthropic)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	client, err := NewAnthropicClient(ModelConfig{Provider: "anthropic", Model: "claude-fixture"}, key, nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, server
}

func TestAnthropicJSONMessagesAndToolRoundTrip(t *testing.T) {
	client, server := newAnthropicFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("x-api-key") != anthropicFixtureKey ||
			request.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("request headers/path: %s %q", request.URL.Path, request.Header.Get("x-api-key"))
		}
		var body struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type      string `json:"type"`
					ToolUseID string `json:"tool_use_id"`
					Content   string `json:"content"`
				} `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.System != "system instruction" || len(body.Messages) != 3 || len(body.Tools) != 1 ||
			body.Messages[2].Role != "user" || body.Messages[2].Content[0].Type != "tool_result" ||
			body.Messages[2].Content[0].ToolUseID != "call-1" || body.Messages[2].Content[0].Content != "tool output" {
			t.Errorf("anthropic request conversion=%+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-fixture","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`)
	})
	defer server.Close()
	request := ModelRequest{
		Messages: []ModelMessage{
			{Role: "system", Content: "system instruction"},
			{Role: "user", Content: "find it"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"q":"it"}`)}}},
			{Role: "tool", ToolCallID: "call-1", Content: "tool output"},
		},
		Tools:           []ToolDefinition{{Name: "lookup", Description: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxOutputTokens: 100,
	}
	result, err := client.Generate(context.Background(), request, nil)
	if err != nil || result.Content != "done" || result.Provider == nil || result.Provider.FinishReason != "stop" ||
		result.Usage == nil || result.Usage.InputTokens != 7 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestAnthropicSSEUsageAndToolUse(t *testing.T) {
	client, server := newAnthropicFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_sse","model":"claude-fixture","usage":{"input_tokens":5}}}\n\n`,
			`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}\n\n`,
			`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}\n\n`,
			`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`,
			`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}\n\n`,
			`event: message_stop\ndata: {"type":"message_stop"}\n\n`,
		} {
			_, _ = fmt.Fprint(writer, strings.ReplaceAll(event, `\n`, "\n"))
		}
	})
	defer server.Close()
	client.caps.Streaming = "supported"
	var emitted strings.Builder
	result, err := client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 32}, func(text string) { emitted.WriteString(text) })
	if err != nil || result.Content != "hello" || emitted.String() != "hello" || result.Provider == nil || !result.Provider.Streamed ||
		result.Usage == nil || result.Usage.TotalTokens != 7 {
		t.Fatalf("result=%+v emitted=%q error=%v", result, emitted.String(), err)
	}
}

func TestAnthropicErrorsAndIncompleteStreamsAreSafe(t *testing.T) {
	partial := false
	client, server := newAnthropicFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if partial {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"x\"}}\n\n")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
	})
	defer server.Close()
	result, err := client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 32}, nil)
	var callErr *CallError
	if err == nil || !errors.As(err, &callErr) || callErr.Category != "authentication" || result.Content != "" || strings.Contains(callErr.Message, anthropicFixtureKey) {
		t.Fatalf("auth result=%+v error=%v", result, err)
	}
	partial = true
	_, err = client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 32}, nil)
	if err == nil || !errors.As(err, &callErr) || callErr.Category != "transport" || callErr.Certain {
		t.Fatalf("partial error=%v", err)
	}
}
