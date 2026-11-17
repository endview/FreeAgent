package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/webaccess"
)

func TestSearchPolicySurvivesSavedApprovalAndRestart(t *testing.T) {
	for _, mode := range []string{"legacy", "off", "auto", "api", "native"} {
		for _, selection := range []string{"", "off"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				calls := 0
				var searchBefore []byte
				model := modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
					calls++
					if calls == 1 {
						searchBefore = mustJSON(request.SearchDecision)
						return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{"path": "approval.txt", "content": "approved once"})}}, nil
					}
					if string(searchBefore) != string(mustJSON(request.SearchDecision)) {
						t.Fatalf("approval changed the frozen search route: before=%s after=%s", searchBefore, mustJSON(request.SearchDecision))
					}
					return ModelResponse{Content: "Done."}, nil
				})
				service, files, workspace := testHarness(t, model)
				service.Config.Tools.Network = true
				keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
				key, err := keys.Add(KeyInput{Name: "approval fixture", Protocol: KeyProtocolSearch, BaseURL: "http://127.0.0.1:19999", Source: KeySourceNone})
				if err != nil {
					t.Fatal(err)
				}
				if mode != "legacy" {
					policy := webaccess.DefaultConfig()
					policy.Mode = mode
					if mode == "api" {
						policy.DefaultConnection = "fixture"
						policy.Connections = map[string]webaccess.Connection{"fixture": {Provider: "searxng", Key: key.ID}}
					}
					service.Config.WebSearch = &policy
				}
				service.Keys = keys
				if mode == "native" {
					modelKey, err := keys.Add(KeyInput{Name: "model fixture", Protocol: KeyProtocolOpenAICompatible, BaseURL: "http://127.0.0.1:19998", Source: KeySourceNone})
					if err != nil {
						t.Fatal(err)
					}
					service.Config.Models[service.Config.DefaultModel] = ModelConfig{Provider: "openai-compatible", Model: "fixture", Key: modelKey.ID,
						Capabilities: ModelCapabilities{Protocol: "responses", WebSearch: "supported"}}
					enableTestProviders(&service.Config)
				}
				runtime := NewRuntimeTools(files)
				runtime.SetKeyStore(keys)
				service.toolManager = NewToolRuntimeManager(runtime, service.Config)
				service.Tools = service.toolManager
				tasks := NewTaskService(service)
				waiting, err := tasks.SubmitWithOptions(t.Context(), "", "search-approval", "Create one file", RunOptions{Capabilities: &CapabilitySelection{Search: selection}})
				if err != nil || waiting.Run.Status != "waiting_confirmation" || calls != 1 {
					t.Fatalf("waiting=%+v calls=%d error=%v", waiting.Run, calls, err)
				}
				decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
				if err != nil || decision.Tool != "file_create" {
					t.Fatalf("saved search policy cannot preview: %v", err)
				}
				if _, err := os.Stat(filepath.Join(workspace, "approval.txt")); !os.IsNotExist(err) {
					t.Fatal("preview dispatched the pending write")
				}
				if _, err := tasks.ConfigureRuntimePolicy(t.Context(), "", func(config *Config) error {
					config.WebSearch = cloneWebConfig(service.Config.WebSearch)
					if config.WebSearch != nil {
						config.WebSearch.Mode = "off"
					}
					return nil
				}); err != nil {
					t.Fatal("future-only search update", err)
				}
				// Rebuild the service as an Owner restart does, preserving the Run.
				freshService := NewService(service.Store, service.Config, Adapters{Model: model, Keys: keys})
				freshService.toolManager = NewToolRuntimeManager(runtime, service.Config)
				freshService.Tools = freshService.toolManager
				restarted := NewTaskService(freshService)
				fresh, err := restarted.Preview(t.Context(), waiting.Run.ID)
				if err != nil || fresh.Digest != decision.Digest || calls != 1 {
					t.Fatalf("restart changed approval: %v calls=%d", err, calls)
				}
				finished, err := restarted.Confirm(t.Context(), waiting.Run.ID, fresh, true)
				if err != nil || finished.Run.Status != "completed" || calls != 2 {
					t.Fatalf("confirmation=%+v calls=%d error=%v", finished.Run, calls, err)
				}
				raw, err := os.ReadFile(filepath.Join(workspace, "approval.txt"))
				if err != nil || string(raw) != "approved once" {
					t.Fatalf("approved write=%q error=%v", raw, err)
				}
			})
		}
	}
}

func TestSearchPageApprovalCanBeDeclinedWithoutRequest(t *testing.T) {
	calls := 0
	service, files, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("fetch", "web_fetch", map[string]string{"url": "https://example.org/source"})}}, nil
		}
		return ModelResponse{Content: "Declined; no page read."}, nil
	}))
	policy := webaccess.DefaultConfig()
	policy.Mode = "auto"
	service.Config.WebSearch, service.Config.Tools.Network = &policy, true
	service.toolManager = NewToolRuntimeManager(NewRuntimeTools(files), service.Config)
	service.Tools = service.toolManager
	tasks := NewTaskService(service)
	waiting, err := tasks.Submit(t.Context(), "", "page-approval", "Read a public page")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting status=%s error=%v", waiting.Run.Status, err)
	}
	preview, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil || preview.Tool != "web_fetch" || !strings.Contains(preview.Preview, "https://example.org/source") {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	finished, err := tasks.Confirm(t.Context(), waiting.Run.ID, preview, false)
	if err != nil || finished.Run.Status != "completed" || calls != 2 || len(finished.Evidence.HTTP) != 0 {
		t.Fatalf("decline status=%s calls=%d evidence=%+v error=%v", finished.Run.Status, calls, finished.Evidence.HTTP, err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), waiting.Run.ID)
	if err != nil || len(attempts) != 3 || attempts[1].Status != "finished" || !strings.Contains(attempts[1].ResponseJSON, "user declined this operation") {
		t.Fatalf("declined tool result missing: %v", err)
	}
}

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
