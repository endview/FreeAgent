package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBudgetPayloadMatchesAdapterHTTPBodies(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			var actual []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actual, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				switch protocol {
				case "anthropic":
					io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"fixture","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`)
				case "responses":
					io.WriteString(w, `{"id":"resp_1","object":"response","model":"fixture","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`)
				default:
					io.WriteString(w, `{"id":"chat_1","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`)
				}
			}))
			defer server.Close()
			config := ModelConfig{Provider: "openai-compatible", Model: "fixture", Capabilities: ModelCapabilities{Streaming: "none"}}
			keyProtocol := KeyProtocolOpenAICompatible
			if protocol == "anthropic" {
				config.Provider = "anthropic"
				keyProtocol = KeyProtocolAnthropic
			}
			if protocol == "responses" {
				config.Capabilities.Protocol = "responses"
			}
			key, err := NewResolvedKeyForProtocol(server.URL, "fixture-only-key", keyProtocol)
			if err != nil {
				t.Fatal(err)
			}
			var client ModelClient
			if protocol == "anthropic" {
				client, err = NewAnthropicClient(config, key, server.Client())
			} else if protocol == "responses" {
				client, err = NewResponsesClient(config, key, server.Client())
			} else {
				client, err = NewOpenAICompatibleClient(config, key, server.Client())
			}
			if err != nil {
				t.Fatal(err)
			}
			request := ModelRequest{MaxOutputTokens: 128, Messages: []ModelMessage{{Role: "user", Content: "中文, code and JSON: {}", ContextSource: &ContextSource{Ref: "#m1", Part: "current_input"}}}}
			expected, err := providerRequestPayload(ModelBinding{Config: config, BaseURL: server.URL}, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.Generate(t.Context(), request, nil); err != nil {
				t.Fatal(err)
			}
			if string(expected) != string(actual) {
				t.Fatalf("budget and adapter body differ: expected %d actual %d", len(expected), len(actual))
			}
		})
	}
}

func TestResultPreviewKeepsPrivateContinuationsAndNativeToolPairs(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	s.Config.MaxContextBytes = 16000
	s.Config.MaxOutputTokens = 128
	for _, protocol := range []string{"chat", "anthropic", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			config := ModelConfig{Provider: "openai-compatible", Key: "profile", Model: "fixture", Capabilities: ModelCapabilities{Streaming: "none"}}
			base := "https://fixture.example"
			assistant := ModelMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "command_run", Arguments: []byte(`{}`)}}}
			opaque := "opaque-private-continuation"
			switch protocol {
			case "chat":
				assistant.ChatContinuation = &ChatContinuation{Source: chatContinuationSource(base, config.Key, config.Model), Content: opaque}
			case "anthropic":
				config.Provider = "anthropic"
				assistant.ChatContinuation = &ChatContinuation{Source: nativeContinuationSource("anthropic", base, config.Key, config.Model), Blocks: []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"private","signature":"opaque-private-continuation"}`), json.RawMessage(`{"type":"tool_use","id":"call_1","name":"command_run","input":{}}`)}}
			case "responses":
				config.Capabilities.Protocol = "responses"
				assistant.ProviderSource = nativeContinuationSource("responses", base, config.Key, config.Model)
				assistant.ProviderItems = []json.RawMessage{json.RawMessage(`{"id":"rs_1","type":"reasoning","encrypted_content":"opaque-private-continuation"}`), json.RawMessage(`{"id":"fc_1","type":"function_call","call_id":"call_1","name":"command_run","arguments":"{}"}`)}
			}
			request := ModelRequest{MaxOutputTokens: 128, Tools: []ToolDefinition{contextToolDefinition()}, Messages: []ModelMessage{{Role: "user", Content: "read"}, assistant, {Role: "tool", ToolCallID: "call_1", Content: strings.Repeat("original result ", 5000), ContextSource: &ContextSource{Ref: "#m3", Revision: strings.Repeat("a", 64), Bytes: 80000, Part: "tool_results"}}}}
			folded := s.foldToolContext(request)
			if !reflect.DeepEqual(folded.Messages[1], assistant) || completeMessageGroups(folded.Messages) != nil {
				t.Fatal("native assistant protocol was rewritten")
			}
			body, err := providerRequestPayload(ModelBinding{Config: config, BaseURL: base}, folded)
			if err != nil || !strings.Contains(string(body), opaque) || !strings.Contains(string(body), "call_1") || !strings.Contains(string(body), "Bounded tool result") {
				t.Fatal("continuation or result pairing missing", err)
			}
		})
	}
}

func TestExtensionInputCacheRefreshesOnlyAfterAdoption(t *testing.T) {
	s, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, s)
	snapshot, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.Store.CreateRun(t.Context(), chat, "cache-input", "original requirement", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	s.extensions = &extensionHost{}
	s.capabilities = &CapabilitySelection{Extensions: []string{"com.example.fixture"}}
	if err = s.refreshExtensionInputCache(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	before := s.extensionInputRevision
	s.extensionPrepared = true
	s.extensionBlocks = []ExtensionBlock{{Text: "cached block"}}
	if _, err = NewTaskService(s).AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: run.ID, RequestKey: "queued-guidance", Prompt: "new adopted requirement"}); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshExtensionInputCache(t.Context(), run); err != nil || s.extensionInputRevision != before || !s.extensionPrepared {
		t.Fatal("pending input changed frozen hook material", err)
	}
	if adopted, err := s.adoptSteering(t.Context(), run); err != nil || !adopted {
		t.Fatal(adopted, err)
	}
	if err = s.refreshExtensionInputCache(t.Context(), run); err != nil || s.extensionInputRevision == before || s.extensionPrepared || len(s.extensionBlocks) != 0 || !strings.Contains(s.extensionInputText, "new adopted requirement") {
		t.Fatal("adopted input reused stale material", err)
	}
	identity, err := s.extensionIdentity(t.Context(), run)
	if err != nil || len(identity.Inputs) != 2 {
		t.Fatal("extension did not receive stable adopted identities", identity, err)
	}
}
