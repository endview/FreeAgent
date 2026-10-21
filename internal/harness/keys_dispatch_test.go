package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	dispatchKeyEnv    = "FA_DISPATCH_KEY"
	dispatchKeySecret = "FA-dispatch-secret-0001"
	dispatchFinished  = `{"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
)

// recordingProvider answers every chat request with respond and remembers the
// Authorization header of each request.
type recordingProvider struct {
	*httptest.Server
	mu            sync.Mutex
	authorization []string
}

func newRecordingProvider(t *testing.T, respond func(call int, writer http.ResponseWriter)) *recordingProvider {
	t.Helper()
	provider := &recordingProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provider.mu.Lock()
		provider.authorization = append(provider.authorization, request.Header.Get("Authorization"))
		call := len(provider.authorization)
		provider.mu.Unlock()
		if request.URL.Path != "/chat/completions" {
			t.Errorf("dispatch used %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		if respond == nil {
			_, _ = writer.Write([]byte(dispatchFinished))
			return
		}
		respond(call, writer)
	}))
	t.Cleanup(provider.Close)
	return provider
}

func (provider *recordingProvider) requests() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.authorization...)
}

// keyedDispatchHarness makes the default model an openai-compatible model whose
// key is resolved by keyedModelClient on every dispatch.
func keyedDispatchHarness(t *testing.T, base, source, env, secret string) (*Service, *KeyStore, string, string) {
	t.Helper()
	service, _, workspace := testHarness(t, nil)
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, base, source, env, secret)
	service.Config.Models["default"] = ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}
	service.Model, service.ModelFor, service.Keys = nil, keyedModelClient(keys), keys
	return service, keys, id, workspace
}

func unsetDispatchKey(t *testing.T) {
	t.Helper()
	t.Setenv(dispatchKeyEnv, "")
	if err := os.Unsetenv(dispatchKeyEnv); err != nil {
		t.Fatal(err)
	}
}

// stopForMissingKey runs once without the environment key and checks that
// the run stopped before any attempt or request.
func stopForMissingKey(t *testing.T, service *Service, providers ...*recordingProvider) Run {
	t.Helper()
	result, err := service.Run(context.Background(), conversation(t, service), "needs-key", "hello")
	if !errors.Is(err, ErrKeyNeedsReentry) || result.Run.Status != "stopped" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	assertNoDispatch(t, service, result.Run.ID, providers...)
	return result.Run
}

func assertNoDispatch(t *testing.T, service *Service, runID string, providers ...*recordingProvider) {
	t.Helper()
	for _, provider := range providers {
		if requests := provider.requests(); len(requests) != 0 {
			t.Fatalf("provider received %d requests", len(requests))
		}
	}
	if attempts, err := service.Store.ListAttempts(context.Background(), runID); err != nil || len(attempts) != 0 {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
}

func TestNeedsReentryStopsWithoutDispatch(t *testing.T) {
	unsetDispatchKey(t)
	provider := newRecordingProvider(t, nil)
	service, _, _, _ := keyedDispatchHarness(t, provider.URL, KeySourceEnv, dispatchKeyEnv, "")
	run := stopForMissingKey(t, service, provider)
	t.Setenv(dispatchKeyEnv, dispatchKeySecret)
	result, err := service.Continue(context.Background(), run.ID)
	if err != nil || result.Run.Status != "completed" || result.Answer != "done" {
		t.Fatalf("continue=%+v err=%v", result, err)
	}
	if requests := provider.requests(); len(requests) != 1 || requests[0] != "Bearer "+dispatchKeySecret {
		t.Fatalf("requests=%q", requests)
	}
}

func TestDispatchRefusesChangedBaseURL(t *testing.T) {
	unsetDispatchKey(t)
	first, second := newRecordingProvider(t, nil), newRecordingProvider(t, nil)
	service, keys, id, _ := keyedDispatchHarness(t, first.URL, KeySourceEnv, dispatchKeyEnv, "")
	run := stopForMissingKey(t, service, first, second)
	view, err := keys.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := keys.Update(id, KeyInput{Name: view.Name, BaseURL: second.URL}); err != nil || moved.Source != KeySourceEnv {
		t.Fatalf("moved=%+v err=%v", moved, err)
	}
	t.Setenv(dispatchKeyEnv, dispatchKeySecret)
	result, err := service.Continue(context.Background(), run.ID)
	if !errors.Is(err, ErrConfigChanged) || result.Run.Status == "completed" {
		t.Fatalf("continue=%+v err=%v", result, err)
	}
	assertNoDispatch(t, service, run.ID, first, second)
}

func TestDispatchRefusesDeletedProfile(t *testing.T) {
	unsetDispatchKey(t)
	provider := newRecordingProvider(t, nil)
	service, keys, id, _ := keyedDispatchHarness(t, provider.URL, KeySourceEnv, dispatchKeyEnv, "")
	run := stopForMissingKey(t, service, provider)
	if err := keys.Remove(id); err != nil {
		t.Fatal(err)
	}
	t.Setenv(dispatchKeyEnv, dispatchKeySecret)
	result, err := service.Continue(context.Background(), run.ID)
	if !errors.Is(err, ErrConfigChanged) || result.Run.Status == "completed" {
		t.Fatalf("continue=%+v err=%v", result, err)
	}
	assertNoDispatch(t, service, run.ID, provider)
}

func TestKeyRotationContinuesRun(t *testing.T) {
	const oldSecret, newSecret = "FA-rotation-secret-0001", "FA-rotation-secret-0002"
	var setup sync.Mutex
	var keys *KeyStore
	var id string
	provider := newRecordingProvider(t, func(call int, writer http.ResponseWriter) {
		if call != 1 {
			_, _ = writer.Write([]byte(dispatchFinished))
			return
		}
		// The key changes while the first call is in flight; the address does not.
		setup.Lock()
		keys, id := keys, id
		setup.Unlock()
		view, err := keys.Find(id)
		if err == nil {
			_, err = keys.Update(id, KeyInput{Name: view.Name, BaseURL: view.BaseURL, Secret: []byte(newSecret)})
		}
		if err != nil {
			t.Error(err)
		}
		message := openAIMessage{Role: "assistant", ToolCalls: toOpenAIToolCalls([]ToolCall{makeCall("read", "file_read", map[string]string{"path": "notes.txt"})})}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "tool_calls"}}})
		_, _ = writer.Write(raw)
	})
	service, store, profile, workspace := keyedDispatchHarness(t, provider.URL, KeySourceSaved, "", oldSecret)
	setup.Lock()
	keys, id = store, profile
	setup.Unlock()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), conversation(t, service), "rotate", "read notes")
	if err != nil || result.Run.Status != "completed" || result.Answer != "done" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if requests := provider.requests(); len(requests) != 2 || requests[0] != "Bearer "+oldSecret || requests[1] != "Bearer "+newSecret {
		t.Fatalf("requests=%q", requests)
	}
	attempts, err := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	models := 0
	for _, attempt := range attempts {
		if attempt.Status != "finished" {
			t.Fatalf("attempt %d status=%s", attempt.Sequence, attempt.Status)
		}
		if attempt.Kind == "model" {
			models++
		}
	}
	if models != 2 {
		t.Fatalf("model attempts=%d of %d", models, len(attempts))
	}
}
