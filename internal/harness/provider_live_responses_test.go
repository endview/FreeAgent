package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const liveSearchPrompt = "Use web search now to open the official DeepSeek API documentation site api-docs.deepseek.com and name one model listed on its Models & Pricing page. Answer in one short sentence."

// liveResponsesConfig reuses the live key but selects the Responses model.
// Chat tests keep FREEAGENT_LIVE_MODEL; DeepSeek runs server-side search only
// with deepseek-v4-pro, so this model is configured separately.
func liveResponsesConfig(t *testing.T, caps ModelCapabilities) (ModelConfig, *KeyStore, string) {
	t.Helper()
	config, keys, keyEnv := liveProviderConfig(t)
	model := os.Getenv("FREEAGENT_LIVE_RESPONSES_MODEL")
	if model == "" {
		if os.Getenv("FREEAGENT_REQUIRE_LIVE") == "1" {
			t.Fatal("BLOCKED: configure FREEAGENT_LIVE_RESPONSES_MODEL; no Responses request dispatched")
		}
		t.Skip("NOT VERIFIED: FREEAGENT_LIVE_RESPONSES_MODEL is not configured")
	}
	caps.Protocol = "responses"
	config.Model = model
	config.Capabilities = caps
	return config, keys, keyEnv
}

func liveResponsesClient(t *testing.T, config ModelConfig, keys *KeyStore) *ResponsesClient {
	t.Helper()
	key, err := keys.Resolve(config.Key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewResponsesClient(config, key, &http.Client{Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func requireNoLiveCredential(t *testing.T, keyEnv string, values ...any) {
	t.Helper()
	key := os.Getenv(keyEnv)
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil || key == "" || strings.Contains(string(raw), key) {
			t.Fatal("live Responses record contained the active credential")
		}
	}
}

func completedSearches(calls []ProviderWebSearchCall) int {
	completed := 0
	for _, call := range calls {
		// A completed call must say what it searched or opened.
		if call.ID != "" && call.Status == "completed" && call.Action != "" && (call.Query != "" || call.URL != "") {
			completed++
		}
	}
	return completed
}

func hasProviderItem(items []json.RawMessage, kind string) bool {
	for _, raw := range items {
		var item struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &item) == nil && item.Type == kind {
			return true
		}
	}
	return false
}

func TestLiveProviderResponsesJSONAndSSE(t *testing.T) {
	for _, streaming := range []string{"none", "supported"} {
		t.Run(streaming, func(t *testing.T) {
			config, keys, keyEnv := liveResponsesConfig(t, ModelCapabilities{Streaming: streaming, Usage: "required", WebSearch: "none"})
			client := liveResponsesClient(t, config, keys)
			var text strings.Builder
			response, err := client.Generate(t.Context(), ModelRequest{
				Messages: []ModelMessage{
					{Role: "system", Content: "You are a protocol test endpoint."},
					{Role: "user", Content: "Reply exactly HARNESS_LIVE_RESPONSES_OK. No other text."},
				},
				MaxOutputTokens: 4096,
			}, func(delta string) { text.WriteString(delta) })
			meta := response.Provider
			if err != nil || meta == nil || !strings.Contains(response.Content, "HARNESS_LIVE_RESPONSES_OK") ||
				text.String() != response.Content || len(response.ToolCalls) != 0 || len(response.ProviderItems) == 0 ||
				meta.Protocol != "responses" || meta.ID == "" || meta.ResponseStatus != "completed" ||
				meta.FinishReason != "stop" || meta.Streamed != (streaming == "supported") ||
				response.Usage == nil || response.Usage.TotalTokens <= 0 {
				t.Fatalf("live Responses contract not accepted: stream=%s meta=%+v error=%v", streaming, meta, err)
			}
			requireNoLiveCredential(t, keyEnv, response)
			t.Logf("live Responses stream=%s status=%s items=%d input_tokens=%d output_tokens=%d", streaming,
				meta.ResponseStatus, len(response.ProviderItems), response.Usage.InputTokens, response.Usage.OutputTokens)
		})
	}
}

func TestLiveProviderResponsesWebSearch(t *testing.T) {
	for _, streaming := range []string{"none", "supported"} {
		t.Run(streaming, func(t *testing.T) {
			config, keys, keyEnv := liveResponsesConfig(t, ModelCapabilities{Streaming: streaming, Usage: "required", WebSearch: "supported"})
			client := liveResponsesClient(t, config, keys)
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()
			question := ModelMessage{Role: "user", Content: liveSearchPrompt}
			searched, err := client.Generate(ctx, ModelRequest{Messages: []ModelMessage{question}, MaxOutputTokens: 4096}, nil)
			meta := searched.Provider
			if err != nil || meta == nil || meta.ResponseStatus != "completed" || strings.TrimSpace(searched.Content) == "" ||
				len(searched.ToolCalls) != 0 || completedSearches(meta.WebSearchCalls) == 0 ||
				!hasProviderItem(searched.ProviderItems, "web_search_call") {
				t.Fatalf("live web search not accepted: stream=%s meta=%+v error=%v", streaming, meta, err)
			}
			// web_search_call items are passed back statelessly; the provider must
			// accept them as history without FreeAgent dispatching a local tool.
			continued, err := client.Generate(ctx, ModelRequest{Messages: []ModelMessage{
				question,
				{Role: "assistant", Content: searched.Content, ProviderItems: searched.ProviderItems},
				{Role: "user", Content: "Without searching again, reply exactly HARNESS_LIVE_SEARCH_CONTINUED. No other text."},
			}, MaxOutputTokens: 4096}, nil)
			if err != nil || continued.Provider == nil || continued.Provider.ResponseStatus != "completed" ||
				!strings.Contains(continued.Content, "HARNESS_LIVE_SEARCH_CONTINUED") || len(continued.ToolCalls) != 0 {
				t.Fatalf("live web search continuation not accepted: stream=%s error=%v", streaming, err)
			}
			requireNoLiveCredential(t, keyEnv, searched, continued)
			first := meta.WebSearchCalls[0]
			t.Logf("live web search stream=%s searches=%d completed=%d first=%s query=%q url=%q continued_searches=%d", streaming,
				len(meta.WebSearchCalls), completedSearches(meta.WebSearchCalls), first.Action, first.Query, first.URL,
				len(continued.Provider.WebSearchCalls))
		})
	}
}

// The Harness path saves Responses output items, replays them after a function
// call and a restart, and keeps server-side search out of local tool attempts.
func TestLiveProviderResponsesHarnessToolsSearchAndRestart(t *testing.T) {
	config, keys, keyEnv := liveResponsesConfig(t, ModelCapabilities{Streaming: "supported", Usage: "required", WebSearch: "supported"})
	core, _, workspace := testHarness(t, liveResponsesClient(t, config, keys))
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = config
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	if err := os.WriteFile(filepath.Join(workspace, "acceptance.txt"), []byte(acceptanceBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	id := conversation(t, core)

	read, err := core.Run(ctx, id, "responses-read",
		"Use file_read to read acceptance.txt and report its exact acceptance marker. Do not search the web.")
	if err != nil || read.Run.Status != "completed" || !strings.Contains(read.Answer, "H1-actual-provider") {
		t.Fatalf("Responses function-call round trip not accepted: status=%s error=%v", read.Run.Status, err)
	}
	requireAcceptanceTools(t, ctx, core, read.Run.ID, "file_read")

	search, err := core.Run(ctx, id, "responses-search", liveSearchPrompt)
	if err != nil || search.Run.Status != "completed" || strings.TrimSpace(search.Answer) == "" {
		t.Fatalf("Responses search run not accepted: status=%s error=%v", search.Run.Status, err)
	}
	searches := 0
	for _, attempt := range acceptanceAttempts(t, ctx, core, search.Run.ID) {
		switch attempt.Kind {
		case "model":
			var saved ModelResponse
			if attempt.Status != "finished" || json.Unmarshal([]byte(attempt.ResponseJSON), &saved) != nil {
				t.Fatal("search run kept an unfinished or invalid model attempt")
			}
			if saved.Provider != nil {
				searches += completedSearches(saved.Provider.WebSearchCalls)
			}
		case "tool":
			var call ToolCall
			if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil || strings.Contains(call.Name, "search") && call.Name != "file_search" {
				t.Fatal("server-side web search became a local tool attempt")
			}
		}
	}
	if searches == 0 {
		t.Fatal("Responses search run saved no completed web_search_call metadata")
	}

	reopenHarness(t, core)
	replayed, err := core.Run(ctx, id, "responses-replay",
		"Without using any tool or web search, reply exactly HARNESS_LIVE_RESPONSES_REPLAYED. No other text.")
	if err != nil || replayed.Run.Status != "completed" || !strings.Contains(replayed.Answer, "HARNESS_LIVE_RESPONSES_REPLAYED") {
		t.Fatalf("saved Responses history replay after restart not accepted: status=%s error=%v", replayed.Run.Status, err)
	}
	for _, runID := range []string{read.Run.ID, search.Run.ID, replayed.Run.ID} {
		requireNoLiveCredential(t, keyEnv, acceptanceAttempts(t, ctx, core, runID))
	}
	messages, err := core.Store.ListMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	requireNoLiveCredential(t, keyEnv, messages)
	t.Logf("Responses harness passed: file_read round trip, %d completed searches saved, restart replay; history_messages=%d",
		searches, len(messages))
}
