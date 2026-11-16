package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/webaccess"
)

type boundedWebLiveModel struct {
	inner         ModelClient
	calls         int
	searchActions int
	usage         []ModelUsage
	trace         []string
	secret        string
}

type webLiveTraceTransport struct {
	next  http.RoundTripper
	tools *[]string
}

func (t webLiveTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		return nil, e
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct{ Tools []struct{ Type, Name string } }
	if json.Unmarshal(raw, &body) == nil {
		names := []string{}
		for _, tool := range body.Tools {
			names = append(names, tool.Type+":"+tool.Name)
		}
		*t.tools = append(*t.tools, strings.Join(names, ","))
	}
	return t.next.RoundTrip(r)
}

func (m *boundedWebLiveModel) Generate(ctx context.Context, r ModelRequest, emit func(string)) (ModelResponse, error) {
	if m.calls >= 5 {
		return ModelResponse{}, &CallError{Certain: true, Category: "quota", Message: "live acceptance request bound reached"}
	}
	m.calls++
	response, err := m.inner.Generate(ctx, r, emit)
	native := r.WebSearch != nil && *r.WebSearch
	finish := ""
	reported := ""
	if response.Provider != nil {
		finish = response.Provider.FinishReason
		reported = response.Provider.Model
	}
	tools := []string{}
	for _, call := range response.ToolCalls {
		tools = append(tools, call.Name)
	}
	m.trace = append(m.trace, fmt.Sprintf("native=%v purpose=%s reported_model=%s finish=%s local_tools=%v answer=%s", native, r.Purpose, reported, finish, tools, redact(truncateUTF8Bytes(response.Content, 700), m.secret)))
	if response.Provider != nil {
		for _, call := range response.Provider.WebSearchCalls {
			if call.Status == "completed" {
				m.searchActions++
			}
		}
	}
	if response.Usage != nil {
		m.usage = append(m.usage, *response.Usage)
	}
	return response, err
}

// Official DeepSeek plus a specifically authorized relay. No Anthropic
// official requests, production database, channel messages, or write tools.
func TestNativeWebCoreLive(t *testing.T) {
	if os.Getenv("FREEAGENT_NATIVE_ACCEPTANCE") != "1" {
		t.Skip("native live acceptance not selected")
	}
	type record struct {
		Name, Endpoint, Model, Status, Error string
		Calls, SearchActions                 int
		Usage                                []ModelUsage
		Trace                                []string
		WireTools                            []string
		Route                                string
		APISearches                          int
		DurationMS                           int64
	}
	var records []record
	cases := []struct{ name, endpoint, model, provider, protocol, keyEnv, control string }{
		{"deepseek", "https://api.deepseek.com", "deepseek-flash", "openai-compatible", "responses", "FA_DEEPSEEK_KEY", "deepseek"},
		{"relay-claude", os.Getenv("FA_RELAY_ENDPOINT") + "/v1", "claude-opus-5-5", "anthropic", "chat", "FA_RELAY_KEY", "anthropic"},
		{"relay-gpt", os.Getenv("FA_RELAY_ENDPOINT") + "/v1", "gpt-6.1-sol", "openai-compatible", "responses", "FA_RELAY_KEY", "openai"},
	}
	for _, tc := range cases {
		if selected := os.Getenv("FREEAGENT_NATIVE_PROVIDER"); selected != "" && selected != tc.name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			entry := record{Name: tc.name, Endpoint: tc.endpoint, Model: tc.model, Status: "FAIL"}
			entry.Route = "native"
			if os.Getenv("FREEAGENT_NATIVE_MODE") == "api" {
				entry.Route = "api"
			}
			defer func() { entry.DurationMS = time.Since(started).Milliseconds(); records = append(records, entry) }()
			secret := os.Getenv(tc.keyEnv)
			if secret == "" {
				entry.Error = "credential missing"
				t.Fatal(entry.Error)
			}
			keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
			keyProtocol := KeyProtocolOpenAICompatible
			if tc.provider == "anthropic" {
				keyProtocol = KeyProtocolAnthropic
			}
			key, e := keys.Add(KeyInput{Name: tc.name, Protocol: keyProtocol, BaseURL: tc.endpoint, Source: KeySourceEnv, EnvName: tc.keyEnv})
			if e != nil {
				entry.Error = "cannot create isolated Key reference"
				t.Fatal(entry.Error)
			}
			keyValue, e := keys.Resolve(key.ID)
			if e != nil {
				t.Fatal("cannot resolve isolated Key")
			}
			caps := ModelCapabilities{Protocol: tc.protocol, Streaming: "supported", Tools: "supported", WebSearch: "supported", OutputTokens: 4096, Usage: "required", Reasoning: ReasoningCapabilities{Control: tc.control, Efforts: "low,high,max", Default: ReasoningSelection{Mode: "enabled", Effort: "low"}, Auxiliary: ReasoningSelection{Mode: "disabled"}}}
			if os.Getenv("FREEAGENT_NATIVE_STREAMING") == "none" {
				caps.Streaming = "none"
			}
			if tc.control == "anthropic" {
				caps.Reasoning.Efforts = "low,medium,high,max"
				caps.Reasoning.Auxiliary = ReasoningSelection{Mode: "enabled", Effort: "low"}
			}
			if tc.control == "openai" {
				caps.Reasoning.Efforts = "low,medium,high,xhigh,max"
				caps.Reasoning.Auxiliary = ReasoningSelection{Mode: "enabled", Effort: "low"}
			}
			config := ModelConfig{Provider: tc.provider, Key: key.ID, Model: tc.model, Capabilities: caps}
			var client ModelClient
			httpClient := &http.Client{Transport: webLiveTraceTransport{next: http.DefaultTransport.(*http.Transport).Clone(), tools: &entry.WireTools}}
			if tc.provider == "anthropic" {
				client, e = NewAnthropicClient(config, keyValue, httpClient)
			} else {
				client, e = NewResponsesClient(config, keyValue, httpClient)
			}
			if e != nil {
				entry.Error = "provider configuration rejected"
				t.Fatal(e)
			}
			model := &boundedWebLiveModel{inner: client, secret: secret}
			defer func() { entry.Trace = model.trace }()
			service, files, workspace := testHarness(t, model)
			service.Keys = keys
			service.Config.Models[service.Config.DefaultModel] = config
			enableTestProviders(&service.Config)
			service.Config.ModelRetries = 0
			service.Config.MaxOutputTokens = 4096
			service.Config.Tools.Network = true
			policy := webaccess.DefaultConfig()
			policy.Mode = entry.Route
			if entry.Route == "api" {
				searchKey, e := keys.Add(KeyInput{Name: "acceptance-search", Protocol: KeyProtocolSearch, BaseURL: "https://api.tavily.com", Source: KeySourceEnv, EnvName: "FA_SEARCH_TAVILY_KEY"})
				if e != nil {
					t.Fatal("cannot create search Key reference")
				}
				policy.DefaultConnection = "acceptance"
				policy.Connections = map[string]webaccess.Connection{"acceptance": {Provider: "tavily", Key: searchKey.ID}}
			}
			service.Config.WebSearch = &policy
			base := NewRuntimeTools(files)
			base.SetKeyStore(keys)
			service.Tools = NewToolRuntimeManager(base, service.Config)
			if e := os.WriteFile(filepath.Join(workspace, "web-acceptance-marker.txt"), []byte("READ_ONLY_MARKER_20261117"), 0600); e != nil {
				t.Fatal(e)
			}
			tasks := NewTaskService(service)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			result, e := tasks.Submit(ctx, "", "native-web", "请先实际联网搜索 Go 官方文档网页 go.dev/doc，然后用 file_read 读取 web-acceptance-marker.txt。最终用一句中文给出 Go 官网来源链接和文件中的完整测试标记。两项都要实际调用，只做读取。")
			entry.Calls = model.calls
			entry.SearchActions = model.searchActions
			entry.Usage = model.usage
			if entry.Route == "api" {
				for _, attempt := range acceptanceAttempts(t, ctx, service, result.Run.ID) {
					if attempt.Kind != "tool" {
						continue
					}
					var call ToolCall
					var outcome ToolResult
					if json.Unmarshal([]byte(attempt.RequestJSON), &call) == nil && call.Name == "web_search" && json.Unmarshal([]byte(attempt.ResponseJSON), &outcome) == nil && !outcome.IsError {
						entry.APISearches++
					}
				}
			}
			hasSearch := model.searchActions > 0
			if entry.Route == "api" {
				hasSearch = entry.APISearches > 0 && model.searchActions == 0
			}
			if e != nil || result.Run.Status != "completed" || !hasSearch || !strings.Contains(result.Answer, "READ_ONLY_MARKER_20261117") || !strings.Contains(result.Answer, "go.dev") {
				entry.Error = "Core search/tool run did not complete with search evidence, source and marker; status=" + result.Run.Status + "; stop=" + result.Run.StopReason
				if e != nil {
					entry.Error += "; " + strings.ReplaceAll(e.Error(), secret, "[redacted]")
				}
				t.Fatal(entry.Error)
			}
			requireAcceptanceTools(t, ctx, service, result.Run.ID, "file_read")
			for _, attempt := range acceptanceAttempts(t, ctx, service, result.Run.ID) {
				if strings.Contains(attempt.RequestJSON, secret) || strings.Contains(attempt.ResponseJSON, secret) {
					entry.Error = "credential found in persisted Attempt"
					t.Fatal(entry.Error)
				}
			}
			if e := tasks.SelectCapabilities(ctx, result.Run.ConversationID, CapabilitySelection{Search: "off"}); e != nil {
				t.Fatal(e)
			}
			before := model.searchActions
			next, e := tasks.Submit(ctx, result.Run.ConversationID, "native-followup", "不要再次搜索或读文件。仅根据上一轮保留的资料，用一句中文复述完整测试标记和 Go 官网链接。")
			entry.Calls = model.calls
			entry.SearchActions = model.searchActions
			entry.Usage = model.usage
			if e != nil || next.Run.Status != "completed" || model.searchActions != before || !strings.Contains(next.Answer, "READ_ONLY_MARKER_20261117") || !strings.Contains(next.Answer, "go.dev") {
				entry.Error = "native history continuation or explicit search-off failed"
				if e != nil {
					entry.Error += "; " + strings.ReplaceAll(e.Error(), secret, "[redacted]")
				}
				t.Fatal(entry.Error)
			}
			entry.Status = "PASS"
			t.Logf("%s: %d model requests, %d server search activities, tool receipt and search-off follow-up verified", tc.name, entry.Calls, entry.SearchActions)
		})
	}
	if path := os.Getenv("FREEAGENT_NATIVE_ACCEPTANCE_REPORT"); path != "" {
		raw, _ := json.MarshalIndent(records, "", "  ")
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal("cannot save sanitized native report")
		}
	}
}
