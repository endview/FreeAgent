package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPProviderMultiRoundToolsAndSecrets(t *testing.T) {
	const key = "fixture-not-a-real-model-secret"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/chat/completions" {
			t.Errorf("request path=%q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing configured model credential")
		}
		var body openAIRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !body.Stream || len(body.Tools) != 8 || body.MaxTokens == 0 {
			t.Errorf("request lost tools or limits: %+v", body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		switch calls {
		case 1:
			writeSSE(writer, `{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"search-1","type":"function","function":{"name":"file_search","arguments":"{\"path\":\".\","}}]},"finish_reason":null}]}`)
			writeSSE(writer, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"query\":\"needle\"}"}}]},"finish_reason":"tool_calls"}]}`)
		case 2:
			last := body.Messages[len(body.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "search-1" || !strings.Contains(last.Content, "notes.txt") {
				t.Errorf("tool result mismatch: %+v", last)
			}
			writeSSE(writer, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read-1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"notes.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		case 3:
			last := body.Messages[len(body.Messages)-1]
			if last.ToolCallID != "read-1" || !strings.Contains(last.Content, "needle") {
				t.Errorf("second result mismatch: %+v", last)
			}
			writeSSE(writer, `{"choices":[{"index":0,"delta":{"content":"Found "},"finish_reason":null}]}`)
			writeSSE(writer, `{"choices":[{"index":0,"delta":{"content":"the needle."},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected model request %d", calls)
		}
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Model: "fixture"}, mustResolved(NewResolvedKey(server.URL, key)), nil)
	if err != nil {
		t.Fatal(err)
	}
	service, _, workspace := testHarness(t, client)
	_ = os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("the needle is here"), 0o600)
	var emitted strings.Builder
	service.Emit = func(event Event) {
		if event.Kind == "model_text" {
			emitted.WriteString(event.Message)
		}
	}
	id := conversation(t, service)
	result, err := service.Run(context.Background(), id, "http", "find the needle")
	if err != nil || result.Answer != "Found the needle." || emitted.String() != result.Answer || calls != 3 {
		t.Fatalf("result=%+v emitted=%q calls=%d error=%v", result, emitted.String(), calls, err)
	}
	attempts, _ := service.Store.ListAttempts(context.Background(), result.Run.ID)
	raw, _ := json.Marshal(attempts)
	if bytes.Contains(raw, []byte(key)) {
		t.Fatal("credential leaked into persistent attempts")
	}
}

func TestHTTPProviderEmbeddings(t *testing.T) {
	const key = "embedding-fixture-secret"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/embeddings" {
			t.Errorf("request path=%q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing embedding credential")
		}
		var body openAIEmbeddingRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "embed-fixture" || len(body.Input) != 2 ||
			body.Input[0] != "one" || body.Input[1] != "two" {
			t.Fatalf("embedding request=%+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"model":"embed-fixture","data":[
			{"object":"embedding","embedding":[0.1,0.2,0.3],"index":1},
			{"object":"embedding","embedding":[0.4,0.5,0.6],"index":0}],
			"usage":{"prompt_tokens":4,"total_tokens":4}}`)
	}))
	defer server.Close()
	client, err := NewOpenAICompatibleClient(ModelConfig{
		Provider: "openai-compatible", Model: "embed-fixture",
		Capabilities: ModelCapabilities{Embeddings: "supported"},
	}, mustResolved(NewResolvedKey(server.URL, key)), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Embed(context.Background(), "embed-fixture", []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(result.Vectors) != 2 ||
		result.Vectors[0][0] != 0.4 || result.Vectors[1][2] != 0.3 ||
		result.Usage == nil || result.Usage.InputTokens != 4 {
		t.Fatalf("embedding result=%+v calls=%d", result, calls)
	}
}

func TestHTTPProviderEmbeddingsRejectsMalformedVectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"data":[
			{"embedding":[0.1,0.2],"index":0},
			{"embedding":[0.3],"index":1}]}`)
	}))
	defer server.Close()
	client, err := NewOpenAICompatibleClient(ModelConfig{
		Provider: "openai-compatible", Model: "embed-fixture",
		Capabilities: ModelCapabilities{Embeddings: "supported"},
	}, mustResolved(NewResolvedKey(server.URL, "")), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Embed(context.Background(), "embed-fixture", []string{"one", "two"})
	var callErr *CallError
	if err == nil || !errors.As(err, &callErr) || callErr.Category != "protocol" {
		t.Fatalf("malformed embedding error=%v", err)
	}
}

func writeSSE(writer http.ResponseWriter, value string) {
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", value)
}

func TestProviderRedirectDoesNotForwardCredential(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetCalls++
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Model: "fixture"}, mustResolved(NewResolvedKey(redirect.URL, "fixture-key")), &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), ModelRequest{Messages: []ModelMessage{{Role: "user", Content: "hello"}}}, nil)
	var certainty *CallError
	if err == nil || !errors.As(err, &certainty) || !certainty.Certain || targetCalls != 0 {
		t.Fatalf("redirect result=%v target calls=%d", err, targetCalls)
	}
}

func TestPartialStreamNeverDispatchesToolsOrCompletes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "text/event-stream")
		writeSSE(writer, `{"choices":[{"index":0,"delta":{"content":"partial answer","tool_calls":[{"index":0,"id":"unfinished","type":"function","function":{"name":"file_edit","arguments":"{\"path\":"}}]},"finish_reason":null}]}`)
	}))
	defer server.Close()
	client, _ := NewOpenAICompatibleClient(ModelConfig{Provider: "openai-compatible", Model: "fixture"}, mustResolved(NewResolvedKey(server.URL, "")), nil)
	service, _, _ := testHarness(t, client)
	id := conversation(t, service)
	result, err := service.Run(context.Background(), id, "partial", "edit something")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
		t.Fatalf("partial=%+v error=%v", result, err)
	}
	attempts, _ := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if len(attempts) != 1 || attempts[0].Status != "unknown" {
		t.Fatalf("partial tool args dispatched: %+v", attempts)
	}
	repeated, err := service.Run(context.Background(), id, "partial", "edit something")
	if err != nil || !repeated.Duplicate || calls != 1 {
		t.Fatalf("replay=%+v calls=%d error=%v", repeated, calls, err)
	}
	if _, err := service.Continue(context.Background(), result.Run.ID); !errors.Is(err, ErrUnknownResult) || calls != 1 {
		t.Fatalf("continue replayed unknown: calls=%d error=%v", calls, err)
	}
}

func TestStreamRedactsSecretsAcrossDeltaBoundaries(t *testing.T) {
	const key = "credential-123456"
	var stream strings.Builder
	for _, delta := range []string{"hello credential-", "123456 goodbye"} {
		raw, _ := json.Marshal(delta)
		stream.WriteString(fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n", raw))
	}
	stream.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	var emitted strings.Builder
	result, err := decodeModelStream(strings.NewReader(stream.String()), key, func(text string) { emitted.WriteString(text) })
	if err != nil || strings.Contains(emitted.String(), key) || strings.Contains(result.Content, key) || emitted.String() != result.Content {
		t.Fatalf("redaction output=%q emitted=%q error=%v", result.Content, emitted.String(), err)
	}
}

func TestStreamRejectsChoiceDataAfterFinish(t *testing.T) {
	for _, delta := range []string{
		`{"content":"late content"}`,
		`{"tool_calls":[{"index":0,"function":{"arguments":"extra"}}]}`,
		`{}`,
	} {
		stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":" + delta + ",\"finish_reason\":null}]}\n\n" +
			"data: [DONE]\n\n"
		result, err := decodeModelStream(strings.NewReader(stream), "", nil)
		if err == nil || len(result.ToolCalls) != 0 {
			t.Fatalf("accepted data after finish: %+v error=%v", result, err)
		}
	}
}

// A missing environment key stops the run before any dispatch; the run can be
// continued once the key is available again.
func TestProviderMissingSecretDoesNotDispatch(t *testing.T) {
	t.Setenv("HARNESS_TEST_KEY", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	service, _, _ := testHarness(t, nil)
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, server.URL, KeySourceEnv, "HARNESS_TEST_KEY", "")
	service.Config.Models["default"] = ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}
	service.Model, service.ModelFor, service.Keys = nil, keyedModelClient(keys), keys
	result, err := service.Run(context.Background(), conversation(t, service), "missing-key", "hello")
	if !errors.Is(err, ErrKeyNeedsReentry) || calls != 0 || result.Run.Status != "stopped" {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, calls)
	}
	attempts, _ := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if len(attempts) != 0 {
		t.Fatalf("missing key recorded attempts: %+v", attempts)
	}
}
