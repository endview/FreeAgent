package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	acceptanceBefore = "Harness acceptance marker: H1-actual-provider"
	acceptanceAfter  = "Harness acceptance marker: H1-confirmed-write"
	acceptanceRead   = "First use file_list to list the directory, then use file_search to find H1-actual-provider, then use file_read to read acceptance.txt. Report its exact acceptance marker."
	acceptanceEdit   = "Read acceptance.txt again for the current sha256, then use file_edit to replace H1-actual-provider with H1-confirmed-write. Keep the rest unchanged."
)

func TestLiveProviderTools(t *testing.T) {
	config, keys, keyEnv := liveProviderConfig(t)
	providerAcceptance(t, config, keys, keyEnv)
}

// liveProviderConfig keeps the four FREEAGENT_LIVE variables. The key stays in
// the variable named by FREEAGENT_LIVE_KEY_ENV; a temporary keys file only
// references it.
func liveProviderConfig(t *testing.T) (ModelConfig, *KeyStore, string) {
	t.Helper()
	endpoint, model, keyEnv := os.Getenv("FREEAGENT_LIVE_ENDPOINT"), os.Getenv("FREEAGENT_LIVE_MODEL"), os.Getenv("FREEAGENT_LIVE_KEY_ENV")
	if endpoint == "" && model == "" && keyEnv == "" && os.Getenv("FREEAGENT_REQUIRE_LIVE") != "1" {
		t.Skip("NOT VERIFIED: live endpoint, model and credential variable are not configured")
	}
	if endpoint == "" || model == "" || !validEnvName(keyEnv) || os.Getenv(keyEnv) == "" {
		t.Fatal("BLOCKED: configure FREEAGENT_LIVE_ENDPOINT, FREEAGENT_LIVE_MODEL, FREEAGENT_LIVE_KEY_ENV and its nonempty credential variable; no request dispatched")
	}
	base, err := NormalizeBaseURL(endpoint)
	if err != nil {
		t.Fatalf("BLOCKED: FREEAGENT_LIVE_ENDPOINT is not a usable base URL (%v); no request dispatched", err)
	}
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, base, KeySourceEnv, keyEnv, "")
	return ModelConfig{Provider: "openai-compatible", Key: id, Model: model}, keys, keyEnv
}

type acceptanceModel struct {
	ModelClient
	calls int
}

func (model *acceptanceModel) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	model.calls++
	return model.ModelClient.Generate(ctx, request, emit)
}

// The live provider and deterministic wire fixtures must satisfy the same gate.
func providerAcceptance(t *testing.T, config ModelConfig, keys *KeyStore, keyEnv string) {
	t.Helper()
	model := &acceptanceModel{ModelClient: keyedClient(t, config, keys, nil)}
	service, _, workspace := testHarness(t, model)
	service.Keys = keys
	service.Config.Models[service.Config.DefaultModel] = config
	if err := WriteConfig(service.Store.DataDir(), service.Config); err != nil {
		t.Fatal(err)
	}
	service.Refresh = func() (Config, error) { return LoadConfig(service.Store.DataDir()) }
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	file := filepath.Join(workspace, "acceptance.txt")
	if err := os.WriteFile(file, []byte(acceptanceBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	id := conversation(t, service)
	read, err := service.Run(ctx, id, "acceptance-read", acceptanceRead)
	if err != nil || read.Run.Status != "completed" || !strings.Contains(read.Answer, "H1-actual-provider") {
		t.Fatalf("read gate: status=%s error=%v; not accepted", read.Run.Status, err)
	}
	requireAcceptanceTools(t, ctx, service, read.Run.ID, "file_list", "file_search", "file_read")
	calls := model.calls
	duplicate, err := service.Run(ctx, id, "acceptance-read", acceptanceRead)
	if err != nil || !duplicate.Duplicate || duplicate.Run.ID != read.Run.ID || model.calls != calls {
		t.Fatal("completed request was not deduplicated")
	}

	write, err := service.Run(ctx, id, "acceptance-edit", acceptanceEdit)
	if err != nil || write.Run.Status != "waiting_confirmation" {
		t.Fatalf("edit gate: status=%s error=%v; not accepted", write.Run.Status, err)
	}
	requireAcceptanceTools(t, ctx, service, write.Run.ID, "file_read")
	requireAcceptanceFile(t, file, acceptanceBefore)
	pending := acceptanceAttempts(t, ctx, service, write.Run.ID)
	var edit ToolCall
	for _, attempt := range pending {
		if attempt.Status != "pending" {
			continue
		}
		if attempt.Kind != "tool" || edit.ID != "" || json.Unmarshal([]byte(attempt.RequestJSON), &edit) != nil || edit.Name != "file_edit" {
			t.Fatal("edit gate requires exactly one pending file_edit")
		}
	}
	var args fileArguments
	if edit.ID == "" || json.Unmarshal(edit.Arguments, &args) != nil || args.Path != "acceptance.txt" ||
		args.Expected != contentHash([]byte(acceptanceBefore)) || args.Old == "" ||
		strings.Count(acceptanceBefore, args.Old) != 1 || strings.Replace(acceptanceBefore, args.Old, args.New, 1) != acceptanceAfter {
		t.Fatal("pending edit did not match the exact authorized fixture change")
	}
	calls = model.calls
	reopenHarness(t, service)
	duplicate, err = service.Run(ctx, id, "acceptance-edit", acceptanceEdit)
	if err != nil || !duplicate.Duplicate || duplicate.Run.ID != write.Run.ID ||
		model.calls != calls || !reflect.DeepEqual(pending, acceptanceAttempts(t, ctx, service, write.Run.ID)) {
		t.Fatal("pending request was replayed or altered after restart")
	}
	approvals := 0
	service.Approve = func(_ context.Context, call ToolCall, prepared PreparedTool) (bool, error) {
		approvals++
		if !reflect.DeepEqual(call, edit) || prepared.Effect != EffectModify ||
			!strings.HasPrefix(prepared.Preview, "acceptance.txt\nSHA-256: "+args.Expected+"\n") {
			return false, fmt.Errorf("confirmation does not match the saved fixture change")
		}
		return true, nil
	}
	continued, err := service.Continue(ctx, write.Run.ID)
	if err != nil || continued.Run.Status != "completed" || continued.Run.ID != write.Run.ID ||
		continued.Run.ConversationID != id || approvals != 1 {
		t.Fatalf("confirmation gate: status=%s approvals=%d error=%v; not accepted", continued.Run.Status, approvals, err)
	}
	requireAcceptanceFile(t, file, acceptanceAfter)
	requireAcceptanceTools(t, ctx, service, write.Run.ID, "file_edit")
	calls = model.calls
	duplicate, err = service.Continue(ctx, write.Run.ID)
	if err != nil || !duplicate.Duplicate || model.calls != calls || approvals != 1 {
		t.Fatal("completed continuation repeated a model call or confirmation")
	}

	reopenHarness(t, service)
	service.Approve = nil
	restarted, err := service.Run(ctx, id, "acceptance-restart",
		"Read acceptance.txt and report the exact marker after our approved edit.")
	if err != nil || restarted.Run.Status != "completed" || !strings.Contains(restarted.Answer, "H1-confirmed-write") {
		t.Fatalf("restart gate: status=%s error=%v; not accepted", restarted.Run.Status, err)
	}
	requireAcceptanceTools(t, ctx, service, restarted.Run.ID, "file_read")
	messages, err := service.Store.ListMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err := service.Store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.ListMessages(ctx, id)
	if err != nil || !reflect.DeepEqual(history, messages) {
		t.Fatal("backup/restore changed the provider conversation")
	}
	for _, runID := range []string{read.Run.ID, write.Run.ID, restarted.Run.ID} {
		attempts := acceptanceAttempts(t, ctx, service, runID)
		saved, err := store.ListAttempts(ctx, runID)
		if err != nil || !reflect.DeepEqual(saved, attempts) {
			t.Fatal("backup/restore changed the provider attempt facts")
		}
		key := os.Getenv(keyEnv)
		for _, attempt := range attempts {
			if attempt.Status != "finished" || strings.Contains(attempt.RequestJSON, key) || strings.Contains(attempt.ResponseJSON, key) {
				t.Fatal("provider gate retained an unresolved attempt or credential value")
			}
		}
	}
	t.Logf("acceptance passed: list/search/read, exact pending edit, restart/confirmation/continue, deduplication, backup/restore; model_calls=%d approvals=%d history_messages=%d", model.calls, approvals, len(messages))
}

func acceptanceAttempts(t *testing.T, ctx context.Context, service *Service, runID string) []Attempt {
	t.Helper()
	attempts, err := service.Store.ListAttempts(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

func requireAcceptanceTools(t *testing.T, ctx context.Context, service *Service, runID string, names ...string) {
	t.Helper()
	finished := make(map[string]bool)
	for _, attempt := range acceptanceAttempts(t, ctx, service, runID) {
		if attempt.Kind != "tool" || attempt.Status != "finished" {
			continue
		}
		var call ToolCall
		var result ToolResult
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil || json.Unmarshal([]byte(attempt.ResponseJSON), &result) != nil {
			t.Fatal("invalid provider tool fact")
		}
		if !result.IsError {
			finished[call.Name] = true
		}
	}
	for _, name := range names {
		if !finished[name] {
			t.Fatalf("missing successful %s tool fact", name)
		}
	}
}

func requireAcceptanceFile(t *testing.T, file, content string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != content {
		t.Fatal("fixture file does not match the expected confirmed content")
	}
}

func TestProviderAcceptanceHTTPFixtures(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			const credential = "acceptance-fixture-credential"
			t.Setenv("HARNESS_ACCEPTANCE_KEY", credential)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				var body openAIRequest
				if json.NewDecoder(request.Body).Decode(&body) != nil || !body.Stream || body.Model != "fixture" ||
					body.MaxTokens == 0 || len(body.Tools) != 7 || request.Header.Get("Authorization") != "Bearer "+credential {
					t.Error("provider request lost model, tools, output limit or credential")
				}
				var response ModelResponse
				switch calls {
				case 1:
					response.ToolCalls = []ToolCall{makeCall("list", "file_list", map[string]string{"path": "."})}
				case 2:
					response.ToolCalls = []ToolCall{makeCall("search", "file_search", map[string]string{"path": ".", "query": "H1-actual-provider"})}
				case 3, 5, 8:
					response.ToolCalls = []ToolCall{makeCall(fmt.Sprintf("read-%d", calls), "file_read", map[string]string{"path": "acceptance.txt"})}
				case 4:
					response.Content = acceptanceBefore
				case 6:
					response.ToolCalls = []ToolCall{makeCall("edit", "file_edit", map[string]string{
						"path": "acceptance.txt", "expected_sha256": contentHash([]byte(acceptanceBefore)),
						"old_text": "H1-actual-provider", "new_text": "H1-confirmed-write",
					})}
				case 7, 9:
					response.Content = acceptanceAfter
				default:
					t.Error("unexpected provider dispatch")
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				finish := "stop"
				if len(response.ToolCalls) > 0 {
					finish = "tool_calls"
				}
				message := openAIMessage{Role: "assistant", Content: response.Content, ToolCalls: toOpenAIToolCalls(response.ToolCalls)}
				field := "message"
				if streaming {
					writer.Header().Set("Content-Type", "text/event-stream")
					field = "delta"
				} else {
					writer.Header().Set("Content-Type", "application/json")
				}
				raw, err := json.Marshal(map[string]any{"choices": []any{
					map[string]any{"index": 0, field: message, "finish_reason": finish},
				}})
				if err != nil {
					t.Error(err)
					return
				}
				if streaming {
					writeSSE(writer, string(raw))
					_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
				} else {
					_, _ = writer.Write(raw)
				}
			}))
			defer server.Close()
			keys := testKeyStore(t)
			id := addTestProfile(t, keys, server.URL, KeySourceEnv, "HARNESS_ACCEPTANCE_KEY", "")
			providerAcceptance(t, ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}, keys, "HARNESS_ACCEPTANCE_KEY")
			if calls != 9 {
				t.Fatalf("model calls=%d, want 9; duplicate or continuation dispatch mismatch", calls)
			}
		})
	}
}
