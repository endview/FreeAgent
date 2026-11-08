package harness

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// These opt-in tests send real requests. No transcript, private continuation
// or credential is logged; mock wire tests assert the exact serialized fields.
func liveReasoningClient(t *testing.T, protocol, stream string) ModelClient {
	t.Helper()
	if os.Getenv("FREEAGENT_LIVE_REASONING_CONTROL") != "deepseek" {
		t.Skip("NOT_RUN: explicit DeepSeek reasoning acceptance configuration is absent")
	}
	config, keys, _ := liveProviderConfig(t)
	if protocol == "responses" {
		config.Model = os.Getenv("FREEAGENT_LIVE_RESPONSES_MODEL")
		if config.Model == "" {
			t.Skip("NOT_RUN: real Responses model is not configured")
		}
	}
	config.Capabilities = ModelCapabilities{Protocol: protocol, Streaming: stream, Usage: "required", Reasoning: ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max"}}
	key, err := keys.Resolve(config.Key)
	if err != nil {
		t.Fatal("live credential could not be resolved")
	}
	if protocol == "responses" {
		client, err := NewResponsesClient(config, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	return keyedClient(t, config, keys, nil)
}

func TestLiveProviderReasoningModes(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		for _, stream := range []string{"none", "supported"} {
			for _, selection := range []ReasoningSelection{{Mode: "default"}, {Mode: "disabled"}, {Mode: "enabled", Effort: "low"}, {Mode: "enabled", Effort: "high"}, {Mode: "enabled", Effort: "max"}} {
				t.Run(protocol+"/"+stream+"/"+selection.Mode+"/"+selection.Effort, func(t *testing.T) {
					client := liveReasoningClient(t, protocol, stream)
					ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
					defer cancel()
					var visible strings.Builder
					response, err := client.Generate(ctx, ModelRequest{Reasoning: &selection, MaxOutputTokens: 2048, Messages: []ModelMessage{{Role: "user", Content: "Calculate 17 * 23. Reply with only the decimal result."}}}, func(delta string) { visible.WriteString(delta) })
					if err != nil {
						t.Fatalf("real reasoning request rejected: %v", err)
					}
					if strings.TrimSpace(response.Content) != "391" || visible.String() != response.Content || len(response.ToolCalls) != 0 || response.Provider == nil || response.Provider.Streamed != (stream == "supported") || response.Provider.RequestedReasoning == nil || *response.Provider.RequestedReasoning != selection || response.Usage == nil || response.Usage.TotalTokens <= 0 {
						t.Fatal("real response did not satisfy answer, stream, selection and usage contract")
					}
					if selection.Mode == "disabled" && response.ChatContinuation != nil && response.ChatContinuation.Content != "" {
						t.Fatal("provider returned private thinking with explicit thinking disabled")
					}
					if protocol == "chat" && selection.Mode == "enabled" && (response.ChatContinuation == nil || response.ChatContinuation.Content == "") {
						t.Fatal("enabled Chat thinking returned no continuation evidence")
					}
					usage := "not_reported"
					if response.Usage.ReasoningTokens != nil {
						usage = "reported"
					}
					t.Logf("real HTTP accepted; input=%d output=%d reasoning_usage=%s; requested setting recorded (server effort not independently observable)", response.Usage.InputTokens, response.Usage.OutputTokens, usage)
				})
			}
		}
	}
}

func TestLiveProviderReasoningToolContinuation(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		for _, stream := range []string{"none", "supported"} {
			t.Run(protocol+"/"+stream, func(t *testing.T) {
				client := liveReasoningClient(t, protocol, stream)
				selection := ReasoningSelection{Mode: "enabled", Effort: "high"}
				request := ModelRequest{Reasoning: &selection, MaxOutputTokens: 2048, Tools: []ToolDefinition{{Name: "read_marker", Description: "Read a synthetic acceptance marker; this tool has no side effects.", Effect: EffectRead, InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","enum":["acceptance"]}},"required":["name"],"additionalProperties":false}`)}}, Messages: []ModelMessage{{Role: "user", Content: "Call read_marker with name acceptance exactly once to obtain the marker. Do not guess it. After its result, reply with only that exact marker and no further calls."}}}
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()
				first, err := client.Generate(ctx, request, nil)
				if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "read_marker" {
					t.Fatalf("real reasoning tool call not accepted: err=%v calls=%d", err, len(first.ToolCalls))
				}
				// A trivial tool choice can carry an empty continuation. Preserve
				// its presence exactly, and verify the real follow-up accepts it.
				const marker = "REASONING_TOOL_CONTINUATION_OK"
				request.Messages = append(request.Messages, ModelMessage{Role: "assistant", Content: first.Content, ToolCalls: first.ToolCalls, ChatContinuation: first.ChatContinuation, ProviderItems: first.ProviderItems}, ModelMessage{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: marker})
				var visible strings.Builder
				second, err := client.Generate(ctx, request, func(delta string) { visible.WriteString(delta) })
				if err != nil || strings.TrimSpace(second.Content) != marker || len(second.ToolCalls) != 0 || visible.String() != second.Content {
					t.Fatalf("real reasoning/tool continuation not accepted: %v", err)
				}
				t.Log("real tool call and follow-up accepted with protocol continuation; private content was not rendered")
			})
		}
	}
}
