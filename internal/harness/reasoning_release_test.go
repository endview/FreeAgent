package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReleaseReasoningPresets(t *testing.T) {
	models := []string{"gpt-6.1-sol", "claude-sonnet-4-6", "gemini-3.8-flash", "grok-4.7", "glm-5.3", "kimi-k3.1-pro", "MiniMax-M3.1-Flash", "muse-spark-1.3", "deepseek-flash"}
	presets := KeyPresets()
	if len(presets) != len(models) {
		t.Fatal("provider ordering changed")
	}
	for i, preset := range presets {
		t.Run(preset.ID, func(t *testing.T) {
			if preset.Model != models[i] {
				t.Fatalf("stale default %s", preset.Model)
			}
			cfg := ModelConfig{Provider: preset.Provider, Key: "k-12345678", Model: preset.Model, Capabilities: preset.Capabilities}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			info := OfficialReasoning(cfg)
			if info == nil || info.Source == "" || info.CheckedAt != "2026-11-09" {
				t.Fatal("missing dated source")
			}
			if on, effort, err := cfg.reasoningParameters(ReasoningSelection{Mode: "default"}); err != nil || on != nil || effort != "" {
				t.Fatal("provider default was rewritten", err)
			}
			for _, effort := range strings.Split(info.Efforts, ",") {
				if _, actual, err := cfg.reasoningParameters(ReasoningSelection{Effort: effort}); err != nil || actual != effort {
					t.Fatalf("effort %s: %s %v", effort, actual, err)
				}
			}
			key := KeyProfileView{Protocol: preset.Protocol, BaseURL: preset.BaseURL}
			if caps := SuggestedModelCapabilities(key, preset.Model); !reflect.DeepEqual(caps, preset.Capabilities) {
				t.Fatalf("GUI/CLI suggestions differ: %+v", caps)
			}
			key.BaseURL = "https://gateway.example/v1"
			if caps := SuggestedModelCapabilities(key, preset.Model); caps != (ModelCapabilities{}) {
				t.Fatal("official settings inferred for an unknown gateway")
			}
		})
	}
	if !slices.Contains(presets[1].Models, "claude-opus-5-5") {
		t.Fatal("Opus is not offered")
	}
	opus := ModelConfig{Provider: "anthropic", Key: "k-12345678", Model: "claude-opus-5-5", Capabilities: SuggestedModelCapabilities(KeyProfileView{Protocol: KeyProtocolAnthropic, BaseURL: presets[1].BaseURL}, "claude-opus-5-5")}
	if err := opus.Validate(); err != nil {
		t.Fatal(err)
	}
	if opus.Capabilities.Reasoning.Auxiliary.Effort != "low" {
		t.Fatal("mandatory reasoning cannot use an off recommendation")
	}
}

func TestReleaseReasoningExclusionsAndUserDeclarations(t *testing.T) {
	for _, tc := range []struct {
		model, provider string
		selection       ReasoningSelection
	}{
		{"gpt-6.1-sol", "openai-compatible", ReasoningSelection{Mode: "disabled"}},
		{"gpt-6.1-sol", "openai-compatible", ReasoningSelection{Effort: "minimal"}},
		{"claude-opus-5-5", "anthropic", ReasoningSelection{Mode: "disabled"}},
		{"muse-spark-1.3", "openai-compatible", ReasoningSelection{Mode: "disabled"}},
		{"claude-sonnet-4-6", "anthropic", ReasoningSelection{Effort: "xhigh"}},
	} {
		cfg := ModelConfig{Provider: tc.provider, Key: "k-12345678", Model: tc.model}
		cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
		// Neither declaring 'none' nor changing control can turn a known
		// mandatory-thinking model into an off-capable model.
		cfg.Capabilities.Reasoning.Efforts += ",none,xhigh"
		if _, _, err := cfg.reasoningParameters(tc.selection); err == nil {
			t.Fatalf("official exclusion bypassed: %+v", tc)
		}
	}
	cfg := ModelConfig{Provider: "openai-compatible", Key: "k-12345678", Model: "gpt-6.1-sol", Capabilities: ModelCapabilities{Protocol: "responses"}}
	cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
	cfg.Capabilities.Reasoning.Efforts += ",ultra"
	cfg.Capabilities.Reasoning.Default = ReasoningSelection{Effort: "ultra"}
	if err := cfg.Validate(); err != nil {
		t.Fatal("bounded user-declared future effort rejected", err)
	}
	if _, value, err := cfg.reasoningParameters(cfg.Capabilities.Reasoning.Default); err != nil || value != "ultra" {
		t.Fatal("custom declaration not encoded", value, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 33), " HIGH", "a,b", "../../x", "high\n", "{\"effort\":1}"} {
		if validReasoningEffort(bad) {
			t.Fatalf("unbounded/injectable effort accepted: %q", bad)
		}
	}
	if OfficialReasoning(ModelConfig{Model: "unknown-model"}) != nil {
		t.Fatal("unknown capabilities fabricated")
	}
}

func TestOfficialReasoningUpdatesDoNotLockOutSavedConfig(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	model := ModelConfig{Provider: "openai-compatible", Key: "k-12345678", Model: "gpt-6.1-sol", Capabilities: ModelCapabilities{Protocol: "responses", Reasoning: ReasoningCapabilities{Control: "openai", Efforts: "none,low", Default: ReasoningSelection{Mode: "disabled"}}}}
	core.Config.Models["default"] = model
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(core.Store.DataDir())
	if err != nil || !reflect.DeepEqual(loaded.Models["default"], model) {
		t.Fatal("new evidence prevented opening saved settings", err)
	}
	if err := ApplyModel(&loaded, "default", model, false); err == nil {
		t.Fatal("a new explicit unsupported edit was accepted")
	}
	var dispatched bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dispatched = true }))
	defer server.Close()
	client, err := NewResponsesClient(model, mustResolved(NewResolvedKey(server.URL, "")), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(t.Context(), ModelRequest{MaxOutputTokens: 100, Messages: []ModelMessage{{Role: "user", Content: "hello"}}}, nil)
	if err == nil || dispatched {
		t.Fatal("old unsupported choice reached provider")
	}
}

func TestAnthropicAdaptiveSignedContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				var wire anthropicRequest
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Error(err)
				}
				if wire.Thinking == nil || wire.Thinking.Type != "adaptive" || wire.OutputConfig == nil || wire.OutputConfig.Effort != "low" {
					t.Error("adaptive/effort not encoded", string(raw))
				}
				for _, field := range []string{"temperature", "top_p", "top_k", "budget_tokens"} {
					if strings.Contains(string(raw), `"`+field+`"`) {
						t.Error("incompatible parameter", field)
					}
				}
				if wire.ToolChoice != nil && wire.ToolChoice.Type != "auto" {
					t.Error("forced tool choice")
				}
				if calls == 2 {
					blocks := wire.Messages[0].Content
					if len(blocks) != 3 || blocks[0].Type != "thinking" || blocks[0].Thinking != "" || blocks[0].Signature != "opaque-signature" || blocks[1].Type != "redacted_thinking" || blocks[1].Data != "opaque-data" || blocks[2].ID != "read-1" {
						t.Error("native signed block order changed", string(raw))
					}
					if !strings.Contains(string(raw), `"thinking":""`) {
						t.Error("omitted thinking still needs its empty field")
					}
				}
				if calls >= 3 && strings.Contains(string(raw), "opaque-") {
					t.Error("private blocks crossed profile/model scope")
				}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					if calls == 1 {
						fmt.Fprint(w, `{"id":"a1","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"","signature":"opaque-signature"},{"type":"redacted_thinking","data":"opaque-data"},{"type":"tool_use","id":"read-1","name":"file_read","input":{"path":"a.txt"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20}}`)
					} else {
						fmt.Fprint(w, `{"id":"a2","content":[{"type":"text","text":"visible"}],"stop_reason":"end_turn"}`)
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				send := func(value string) { fmt.Fprintf(w, "data: %s\n\n", value) }
				send(`{"type":"message_start","message":{"id":"a1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":0}}}`)
				if calls == 1 {
					send(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
					send(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`)
					send(`{"type":"content_block_stop","index":0}`)
					send(`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-data"}}`)
					send(`{"type":"content_block_stop","index":1}`)
					send(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"read-1","name":"file_read","input":{}}}`)
					send(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}`)
					send(`{"type":"content_block_stop","index":2}`)
					send(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
				} else {
					send(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
					send(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`)
					send(`{"type":"content_block_stop","index":0}`)
					send(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
				}
				send(`{"type":"message_stop"}`)
			}))
			defer server.Close()
			cfg := ModelConfig{Provider: "anthropic", Model: "claude-opus-5-5"}
			cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
			if !stream {
				cfg.Capabilities.Streaming = "none"
			}
			key, err := NewResolvedKeyForProtocol(server.URL, "", KeyProtocolAnthropic)
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewAnthropicClient(cfg, key, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := ModelRequest{MaxOutputTokens: 100, Reasoning: &ReasoningSelection{Effort: "low"}, Messages: []ModelMessage{{Role: "user", Content: "read"}}, Tools: []ToolDefinition{{Name: "file_read", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
			var emitted strings.Builder
			first, err := client.Generate(t.Context(), req, func(v string) { emitted.WriteString(v) })
			if err != nil || first.ChatContinuation == nil || len(first.ToolCalls) != 1 || emitted.Len() != 0 {
				t.Fatalf("first result %+v: %v", first, err)
			}
			if first.Provider.RequestedReasoning == nil || first.Provider.RequestedReasoning.Effort != "low" {
				t.Fatal("requested effort missing")
			}
			req.Messages = []ModelMessage{{Role: "assistant", ToolCalls: first.ToolCalls, ChatContinuation: first.ChatContinuation}, {Role: "tool", ToolCallID: "read-1", Content: "read result"}}
			for n := 0; n < 3; n++ {
				if n == 1 {
					client.key.ProfileID = "other-profile"
				}
				if n == 2 {
					client.model = "other-model"
				}
				out, err := client.Generate(t.Context(), req, nil)
				if err != nil || out.Content != "visible" {
					t.Fatal(out, err)
				}
			}
		})
	}
}

func TestAnthropicCompactionDefaultsAndSamplingAbsence(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-5-5"} {
		cfg := ModelConfig{Provider: "anthropic", Model: model}
		cfg.Capabilities.Reasoning = suggestedReasoning(cfg)
		for _, choice := range []ReasoningSelection{{Mode: "default"}, cfg.Capabilities.Reasoning.Auxiliary} {
			req := ModelRequest{MaxOutputTokens: 8192, Purpose: "compaction", Reasoning: &choice, Messages: []ModelMessage{{Role: "user", Content: "summarize"}}}
			wire, err := anthropicRequestFrom(req, model, cfg.Capabilities)
			if err != nil {
				t.Fatal(err)
			}
			if choice.Mode == "default" && (wire.Thinking != nil || wire.OutputConfig != nil) {
				t.Fatal("default was not omitted")
			}
			if len(wire.Tools) != 0 || wire.ToolChoice != nil || wire.MaxTokens != 8192 {
				t.Fatal("summary request semantics changed")
			}
			var body map[string]any
			_ = json.Unmarshal(mustJSON(wire), &body)
			for _, key := range []string{"temperature", "top_p", "top_k"} {
				if _, ok := body[key]; ok {
					t.Fatal("sampling on native thinking compaction")
				}
			}
		}
	}
}

func TestPrivateContinuationDoesNotEnterSummaries(t *testing.T) {
	private := &ChatContinuation{Source: strings.Repeat("0", 64), Blocks: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"PRIVATE-NATIVE","signature":"PRIVATE-SIGNATURE"}`)}, ToolExtras: map[string]json.RawMessage{"read": json.RawMessage(`{"google":{"thought_signature":"PRIVATE-GEMINI"}}`)}}
	binding := ModelBinding{ID: "fixture", Config: ModelConfig{Provider: "echo"}}
	req := summaryRequest(binding, ExecutionLimits{MaxOutputTokens: 8192}, 4096, "", []Message{{ModelMessage: ModelMessage{Role: "assistant", Content: "visible", ChatContinuation: private, ProviderItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"PRIVATE-RESPONSES"}`)}, ProviderSource: strings.Repeat("0", 64)}}})
	if strings.Contains(string(mustJSON(req)), "PRIVATE-") {
		t.Fatal("private protocol material entered summary")
	}
}
