package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/webaccess"
)

func TestSearchThroughActualCoreAndExplicitConversationExclusion(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					w.Write([]byte(`{"results":[{"title":"Test source","url":"https://example.org/source","content":"Synthetic context"}]}`))
				}
			}))
			defer server.Close()
			modelCalls := 0
			service, files, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
				modelCalls++
				if modelCalls == 1 {
					found := false
					for _, tool := range request.Tools {
						found = found || tool.Name == "web_search"
					}
					if !found {
						t.Fatal("search not offered")
					}
					if request.WebSearch == nil || *request.WebSearch {
						t.Fatal("API route also enabled native search")
					}
					return ModelResponse{ToolCalls: []ToolCall{makeCall("search", "web_search", map[string]string{"query": "public reference"})}}, nil
				}
				if modelCalls == 2 {
					found := false
					for _, m := range request.Messages {
						found = found || m.Role == "tool" && (strings.Contains(m.Content, "example.org/source") || strings.Contains(m.Content, "HTTP 429"))
					}
					if !found {
						t.Fatal("missing persisted tool result")
					}
				} else {
					for _, tool := range request.Tools {
						if tool.Name == "web_search" || tool.Name == "web_fetch" {
							t.Fatal("explicit exclusion ignored")
						}
					}
				}
				return ModelResponse{Content: "Search completed or unavailable; no replay."}, nil
			}))
			keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
			key, e := keys.Add(KeyInput{Name: "fixture", Protocol: KeyProtocolSearch, BaseURL: server.URL, Source: KeySourceNone})
			if e != nil {
				t.Fatal(e)
			}
			policy := webaccess.DefaultConfig()
			policy.Mode = "auto"
			policy.DefaultConnection = "fixture"
			policy.Connections = map[string]webaccess.Connection{"fixture": {Provider: "searxng", Key: key.ID}}
			service.Config.WebSearch = &policy
			service.Config.Tools.Network = true
			service.Keys = keys
			tools := NewRuntimeTools(files)
			tools.SetKeyStore(keys)
			service.Tools = NewToolRuntimeManager(tools, service.Config)
			tasks := NewTaskService(service)
			result, e := tasks.Submit(t.Context(), "", "search-fixture", "Search public reference")
			if e != nil || result.Run.Status != "completed" || requests.Load() != 1 {
				t.Fatalf("Core result=%+v calls=%d err=%v", result.Run, requests.Load(), e)
			}
			if e := tasks.SelectCapabilities(t.Context(), result.Run.ConversationID, CapabilitySelection{Search: "off"}); e != nil {
				t.Fatal(e)
			}
			next, e := tasks.Submit(t.Context(), result.Run.ConversationID, "next", "Ordinary chat, no search")
			if e != nil || next.Run.Status != "completed" || requests.Load() != 1 {
				t.Fatalf("exclusion result=%+v err=%v", next.Run, e)
			}
		})
	}
}

func TestSearchSelectionCannotEnableGlobalOffAndFreezesPolicy(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	policy := webaccess.DefaultConfig()
	config.WebSearch = &policy
	files, e := NewFileTools(config.Workspace, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer files.Close()
	manager := NewToolRuntimeManager(NewRuntimeTools(files), config)
	view := manager.Acquire().ForCapabilities(CapabilitySelection{Search: "api"})
	if len(view.webTools().Definitions()) != 0 {
		t.Fatal("conversation enabled global off")
	}
	config.WebSearch.Mode = "auto"
	frozen := view.Policy()
	if frozen.WebSearch.Mode != "off" {
		t.Fatal("policy shared mutable config")
	}
}

func TestSearchRouteAndAuxiliaryNoNetworking(t *testing.T) {
	policy := webaccess.DefaultConfig()
	policy.Mode = "api"
	service := &Service{Config: DefaultConfig(t.TempDir())}
	service.Config.Tools.Network = true
	service.Config.WebSearch = &policy
	service.Config.Models[service.Config.DefaultModel] = ModelConfig{Provider: "openai-compatible", Model: "custom", Capabilities: ModelCapabilities{Protocol: "responses", WebSearch: "supported"}}
	request := ModelRequest{ModelID: service.Config.DefaultModel, Tools: (&WebTools{Config: &policy}).Definitions()}
	out, e := service.prepareSearch(request)
	if e != nil || out.WebSearch == nil || *out.WebSearch {
		t.Fatalf("API route %v %+v", e, out)
	}
	request.Purpose = "compression"
	out, e = service.prepareSearch(request)
	if e != nil || out.WebSearch == nil || *out.WebSearch || len(out.Tools) != 0 {
		t.Fatal("auxiliary networking enabled", e)
	}
	policy.Mode = "native"
	request.Purpose = ""
	out, e = service.prepareSearch(request)
	if e != nil || out.WebSearch == nil || !*out.WebSearch || len(out.Tools) != 0 {
		t.Fatal("explicit native declaration rejected", e)
	}
	service.capabilities = &CapabilitySelection{Search: "off"}
	out, e = service.prepareSearch(request)
	if e != nil || out.WebSearch == nil || *out.WebSearch {
		t.Fatal("explicit exclusion lost", e)
	}
}

func TestSearchKeyAndReadFailureContract(t *testing.T) {
	keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	key, e := keys.Add(KeyInput{Name: "search", BaseURL: "https://example.org", Protocol: KeyProtocolSearch, Source: "saved", Secret: []byte("fixture-secret")})
	if e != nil {
		t.Fatal(e)
	}
	policy := webaccess.DefaultConfig()
	policy.Mode = "api"
	policy.DefaultConnection = "one"
	policy.Connections = map[string]webaccess.Connection{"one": {Provider: "deepseek", Key: key.ID}}
	tools := &WebTools{Config: &policy, Keys: keys}
	prepared, e := tools.Prepare(t.Context(), makeCall("search", "web_search", map[string]any{"query": "hello"}))
	if e != nil || prepared.Effect != EffectRead {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"url":"http://127.0.0.1/"}`, `{"url":"file:///etc/passwd"}`, `{"url":"https://example.org","headers":{"Authorization":"secret"}}`} {
		if _, e := tools.Prepare(t.Context(), ToolCall{Name: "web_fetch", Arguments: json.RawMessage(raw)}); e == nil {
			t.Fatalf("unsafe fetch %s", raw)
		}
	}
}
