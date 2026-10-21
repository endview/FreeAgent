package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// contractClient serves handler behind a keyless loopback profile.
func contractClient(t *testing.T, caps ModelCapabilities, handler http.HandlerFunc) (*OpenAICompatibleClient, ModelConfig, *KeyStore) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	keys := testKeyStore(t)
	config := ModelConfig{Provider: "openai-compatible", Key: addTestProfile(t, keys, server.URL, KeySourceNone, "", ""), Model: "fixture", Capabilities: caps}
	return keyedClient(t, config, keys, nil), config, keys
}

func contractRequest() ModelRequest {
	return ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 32}
}

func TestProviderCapabilityMatrixAndRequestWire(t *testing.T) {
	for _, scenario := range []struct {
		name string
		caps ModelCapabilities
	}{
		{"defaults", ModelCapabilities{}},
		{"json", ModelCapabilities{Streaming: "none", ParallelTools: "none"}},
		{"completion-limit", ModelCapabilities{TokenLimit: "max_completion_tokens", Usage: "none"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			client, config, _ := contractClient(t, scenario.caps, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var wire openAIRequest
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				effective := (ModelConfig{Provider: "openai-compatible", Capabilities: scenario.caps}).EffectiveCapabilities()
				if wire.Stream != (effective.Streaming != "none") || len(wire.Tools) != 1 ||
					wire.ParallelToolCalls == nil || *wire.ParallelToolCalls != (effective.ParallelTools != "none") {
					t.Errorf("capabilities not honored: %+v", wire)
				}
				if effective.TokenLimit == "max_completion_tokens" {
					if wire.MaxTokens != 0 || wire.MaxCompletionTokens != 32 {
						t.Errorf("wrong completion limit: %+v", wire)
					}
				} else if wire.MaxTokens != 32 || wire.MaxCompletionTokens != 0 {
					t.Errorf("wrong max_tokens limit: %+v", wire)
				}
				if (wire.StreamOptions != nil) != (wire.Stream && effective.Usage != "none") {
					t.Error("usage request does not match declaration")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"answer-1","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
			})
			request := contractRequest()
			request.Tools = []ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			var emitted strings.Builder
			response, err := client.Generate(t.Context(), request, func(text string) { emitted.WriteString(text) })
			if err != nil || response.Content != "answer" || emitted.String() != "answer" || calls.Load() != 1 ||
				response.Provider == nil || response.Provider.ID != "answer-1" || response.Provider.UsageStatus != "missing" {
				t.Fatalf("response=%+v calls=%d err=%v", response, calls.Load(), err)
			}
			if config.EffectiveCapabilities().ErrorPolicy != "manual" {
				t.Fatal("implicit retry policy")
			}
		})
	}
}

func TestProviderMetadataMultilineSSEAndParallelToolItems(t *testing.T) {
	stream := ": heartbeat\r\nid: event-1\r\nevent: message\r\n" +
		"data: {\"id\":\"completion-1\",\"model\":\"fixture\",\"created\":123,\"system_fingerprint\":\"fp_1\",\r\n" +
		"data: \"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":1,\"id\":\"second\",\"type\":\"function\",\"function\":{\"name\":\"probe\",\"arguments\":\"{}\"}},{\"index\":0,\"id\":\"first\",\"type\":\"function\",\"function\":{\"name\":\"probe\",\"arguments\":\"{\"}}]},\"finish_reason\":null}]}\r\n\r\n" +
		"data: {\"id\":\"completion-1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\r\n\r\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\r\n\r\n" +
		"data: [DONE]\r\n\r\n"
	response, err := decodeModelStream(strings.NewReader(stream), "", nil)
	if err != nil || len(response.ToolCalls) != 2 || response.ToolCalls[0].ID != "first" ||
		response.ToolCalls[1].ID != "second" || response.Provider == nil ||
		response.Provider.ID != "completion-1" || response.Provider.Model != "fixture" ||
		response.Provider.Created != 123 || response.Provider.SystemFingerprint != "fp_1" ||
		!response.Provider.Streamed || response.Provider.FinishReason != "tool_calls" ||
		response.Provider.UsageStatus != "reported" || response.Usage.TotalTokens != 10 {
		t.Fatalf("multiline stream lost metadata: %+v err=%v", response, err)
	}
	for index, call := range response.ToolCalls {
		if call.Provider == nil || call.Provider.Index != index || call.Provider.Type != "function" || string(call.Arguments) != "{}" {
			t.Fatalf("lost tool item metadata: %+v", call)
		}
	}
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Messages[len(request.Messages)-1].Role == "user" {
			return response, nil
		}
		return ModelResponse{Content: "tool errors observed"}, nil
	}))
	result, err := core.Run(t.Context(), conversation(t, core), "metadata", "use tools")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("group did not finish: %+v err=%v", result, err)
	}
	before, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var saved ModelResponse
	if err := json.Unmarshal([]byte(before[0].ResponseJSON), &saved); err != nil || !reflect.DeepEqual(saved, response) {
		t.Fatalf("metadata not persisted: %+v err=%v", saved, err)
	}
	reopenHarness(t, core)
	backup := filepath.Join(t.TempDir(), "backup")
	if err := core.Store.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart or backup changed provider metadata")
	}
}

func TestProviderUsageContract(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		usage    string
		required bool
		ok       bool
		certain  bool
	}{
		{"optional-missing", "", false, true, false},
		{"required-missing", "", true, false, true},
		{"reported-zero", `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, true, false},
		{"empty", `,"usage":{}`, false, false, false},
		{"partial", `,"usage":{"prompt_tokens":2,"total_tokens":2}`, false, false, false},
		{"negative", `,"usage":{"prompt_tokens":2,"completion_tokens":-1,"total_tokens":1}`, false, false, false},
		{"inconsistent", `,"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":9}`, false, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			caps := ModelCapabilities{}
			if scenario.required {
				caps.Usage = "required"
			}
			client, _, _ := contractClient(t, caps, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]`+scenario.usage+`}`)
			})
			response, err := client.Generate(t.Context(), contractRequest(), nil)
			if (err == nil) != scenario.ok || len(response.ToolCalls) != 0 {
				t.Fatalf("usage outcome=%+v err=%v", response, err)
			}
			if err != nil {
				var failure *CallError
				if !errors.As(err, &failure) || failure.Certain != scenario.certain || failure.Retryable {
					t.Fatalf("wrong usage certainty: %v", err)
				}
			}
			if scenario.ok && (response.Provider.UsageStatus == "reported") != (response.Usage != nil) {
				t.Fatal("missing usage was turned into a zero report")
			}
		})
	}
}

func TestProviderDefinitiveFinishAndErrorCategoriesPersistWithoutReplay(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		category string
		retry    bool
	}{
		{"length", 200, `{"choices":[{"index":0,"message":{"role":"assistant","content":"partial","tool_calls":[{"id":"never","type":"function","function":{"name":"file_list","arguments":"{}"}}]},"finish_reason":"length"}]}`, "output_limit", false},
		{"filter", 200, `{"choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"content_filter"}]}`, "content_filter", false},
		{"refusal", 200, `{"choices":[{"index":0,"message":{"role":"assistant","refusal":"no"},"finish_reason":"stop"}]}`, "refusal", false},
		{"authentication", 401, `{"error":{"code":"invalid_api_key","type":"authentication_error"}}`, "authentication", false},
		{"permission", 403, `{}`, "permission", false},
		{"context", 400, `{"error":{"code":"context_length_exceeded","type":"invalid_request_error"}}`, "context_limit", false},
		{"rate", 429, `{"error":{"code":"slow_down","type":"rate_limit_error"}}`, "rate_limit", true},
		{"quota", 429, `{"error":{"code":"credit_balance_exhausted","type":"insufficient_quota"}}`, "quota", false},
		{"spend", 429, `{"error":{"code":"project_spend_limit_exceeded","type":"insufficient_quota"}}`, "quota", false},
		{"unavailable", 503, `{"error":{"code":"server_is_overloaded","type":"service_unavailable_error"}}`, "unavailable", true},
		{"json-error", 200, `{"error":{"type":"invalid_request_error","code":"invalid_parameter"}}`, "request", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			client, config, keys := contractClient(t, ModelCapabilities{}, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(scenario.status)
				fmt.Fprint(w, scenario.body)
			})
			core, _, _ := testHarness(t, client)
			core.Keys = keys
			core.Config.Models[core.Config.DefaultModel] = config
			id := conversation(t, core)
			result, err := core.Run(t.Context(), id, "failure", "task")
			var failure *CallError
			if !errors.As(err, &failure) || !failure.Certain || failure.Category != scenario.category ||
				failure.Retryable != scenario.retry || result.Run.Status != "failed" || result.Answer != "" || calls.Load() != 1 {
				t.Fatalf("failure=%+v result=%+v err=%v", failure, result, err)
			}
			attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "finished" {
				t.Fatalf("failed output dispatched a tool: %+v err=%v", attempts, err)
			}
			var saved ModelResponse
			if json.Unmarshal([]byte(attempts[0].ResponseJSON), &saved) != nil ||
				saved.Failure == nil || saved.Failure.Category != scenario.category ||
				saved.Failure.Retryable != scenario.retry || len(saved.ToolCalls) != 0 {
				t.Fatalf("failure category not retained: %+v", saved)
			}
			reopenHarness(t, core)
			repeated, err := core.Run(t.Context(), id, "failure", "task")
			if err != nil || !repeated.Duplicate || calls.Load() != 1 {
				t.Fatal("failure retry hint replayed the original request")
			}
		})
	}
}

func TestProviderRejectsAmbiguousOrInterruptedSSE(t *testing.T) {
	base := `{"id":"first","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`
	for _, scenario := range []struct {
		name   string
		events []string
		done   bool
	}{
		{"no-finish", []string{base}, true},
		{"no-done", []string{base, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`}, false},
		{"identity-change", []string{base, `{"id":"second","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`}, true},
		{"nonzero-choice", []string{`{"choices":[{"index":1,"delta":{"content":"x"},"finish_reason":"stop"}]}`}, true},
		{"noncontiguous-tools", []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"second","type":"function","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`}, true},
		{"duplicate-id", []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"same","type":"function","function":{"name":"probe","arguments":"{}"}},{"index":1,"id":"same","type":"function","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`}, true},
		{"invalid-usage", []string{`{"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`, `{"choices":[],"usage":{}}`}, true},
		{"bad-json", []string{base, `{"choices":[`}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var stream strings.Builder
			for _, event := range scenario.events {
				fmt.Fprintf(&stream, "data: %s\n\n", event)
			}
			if scenario.done {
				stream.WriteString("data: [DONE]\n\n")
			}
			response, err := decodeModelStream(strings.NewReader(stream.String()), "", nil)
			var failure *CallError
			if !errors.As(err, &failure) || failure.Certain || failure.Retryable || len(response.ToolCalls) != 0 {
				t.Fatalf("ambiguous event became executable: %+v err=%v", response, err)
			}
		})
	}
}

func TestProviderParallelCapabilityAndWholeGroupPreflight(t *testing.T) {
	var calls atomic.Int32
	client, _, _ := contractClient(t, ModelCapabilities{ParallelTools: "none"}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"probe","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	})
	response, err := client.Generate(t.Context(), contractRequest(), nil)
	var failure *CallError
	if !errors.As(err, &failure) || !failure.Certain || failure.Category != "capability" || len(response.ToolCalls) != 0 {
		t.Fatalf("parallel tool violation executed: %+v err=%v", response, err)
	}
	request := contractRequest()
	request.Messages = append(request.Messages, ModelMessage{Role: "assistant", ToolCalls: []ToolCall{
		makeCall("a", "probe", map[string]string{}), makeCall("b", "probe", map[string]string{}),
	}}, ModelMessage{Role: "tool", ToolCallID: "a", Content: "a"}, ModelMessage{Role: "tool", ToolCallID: "b", Content: "b"})
	if _, err := client.Generate(t.Context(), request, nil); err == nil || calls.Load() != 1 {
		t.Fatal("incompatible complete parallel history reached provider")
	}
	request.Messages = request.Messages[:3]
	normal, _, _ := contractClient(t, ModelCapabilities{}, func(http.ResponseWriter, *http.Request) {
		t.Error("incomplete tool group reached provider")
	})
	if _, err := normal.Generate(t.Context(), request, nil); err == nil {
		t.Fatal("incomplete tool group accepted")
	}
}

func TestProviderMetadataAndErrorSecretRedaction(t *testing.T) {
	const secret = "fixture_secret"
	for _, body := range []string{
		`{"id":"fixture_secret","model":"fixture_secret","system_fingerprint":"fixture_secret","choices":[{"index":0,"message":{"role":"assistant","content":"fixture_secret"},"finish_reason":"stop"}]}`,
		`{"error":{"code":"fixture_secret","type":"server_error","message":"fixture_secret"}}`,
		`{"choices":[{"index":0,"message":{"role":"assistant","content":"fixture_secret"},"finish_reason":"fixture_secret"}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}))
		client, err := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Model: "fixture"}, mustResolved(NewResolvedKey(server.URL, secret)), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Generate(t.Context(), contractRequest(), nil)
		server.Close()
		raw, _ := json.Marshal(response)
		errorRaw, _ := json.Marshal(err)
		if strings.Contains(string(raw)+string(errorRaw), secret) {
			t.Fatalf("credential leaked in metadata or error: %s %s", raw, errorRaw)
		}
	}
}

func TestProviderResponsesDeclarationAndDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" {
			t.Errorf("Responses path=%q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"id":"response-fixture","status":"completed","model":"fixture","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
	}))
	defer server.Close()
	keys := testKeyStore(t)
	config := ModelConfig{Provider: "openai-compatible", Model: "fixture",
		Key:          addTestProfile(t, keys, server.URL, KeySourceNone, "", ""),
		Capabilities: ModelCapabilities{Protocol: "responses", Streaming: "none"}}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	key, err := keys.Resolve(config.Key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewResponsesClient(config, key, nil)
	if err != nil {
		t.Fatalf("Responses client rejected supported protocol: %v", err)
	}
	response, err := client.Generate(t.Context(), contractRequest(), nil)
	if err != nil || response.Content != "answer" || response.Provider == nil || response.Provider.Protocol != "responses" {
		t.Fatalf("Responses dispatch=%+v err=%v", response, err)
	}
}
