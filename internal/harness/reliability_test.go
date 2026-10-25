package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigChangeDuringApprovalPreventsDispatch(t *testing.T) {
	original := []byte("before")
	service, _, workspace := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("edit", "file_edit", map[string]string{
			"path": "notes.txt", "expected_sha256": contentHash(original), "old_text": "before", "new_text": "after",
		})}}, nil
	}))
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	service.Refresh = func() (Config, error) { return LoadConfig(service.Store.DataDir()) }
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) {
		config := service.Config
		model := config.Models[config.DefaultModel]
		model.Capabilities.Strength = "strong"
		config.Models[config.DefaultModel] = model
		return true, WriteConfig(service.Store.DataDir(), config)
	}
	result, err := service.Run(context.Background(), conversation(t, service), "drift", "edit")
	if !errors.Is(err, ErrConfigChanged) || result.Run.Status != "stopped" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	attempts, err := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if err != nil || len(attempts) != 2 || attempts[1].Status != "pending" {
		t.Fatalf("attempts=%+v error=%v", attempts, err)
	}
	raw, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(raw) != "before" {
		t.Fatal("configuration drift still dispatched the edit")
	}
}

func TestRunTimeBudgetSurvivesRestartAndContinue(t *testing.T) {
	modelCalls := 0
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{Content: "should not run"}, nil
	}))
	service.Config.MaxRunSeconds = 1
	ctx := context.Background()
	snapshot, _ := service.snapshot()
	run, _, err := service.Store.CreateRun(ctx, conversation(t, service), "time", "task", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	active := time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)
	if _, err := service.Store.db.ExecContext(ctx, `UPDATE runs SET elapsed_ms=50,active_since=? WHERE id=?`, active, run.ID); err != nil {
		t.Fatal(err)
	}
	reopenHarness(t, service)
	recovered, err := service.Store.GetRun(ctx, run.ID)
	if err != nil || recovered.ElapsedMS < 2050 || recovered.ActiveSince != "" || recovered.Status != "stopped" {
		t.Fatalf("recovered=%+v error=%v", recovered, err)
	}
	result, err := service.Continue(ctx, run.ID)
	if !errors.Is(err, ErrTimeLimit) || modelCalls != 0 {
		t.Fatalf("continue=%+v error=%v calls=%d", result, err, modelCalls)
	}
}

func TestAbandonRetainsUnknownAndWarnsAcrossSessions(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	snapshot, _ := service.snapshot()
	id := conversation(t, service)
	run, _, err := service.Store.CreateRun(ctx, id, "uncertain", "task", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	model, err := service.Store.startModel(ctx, run, `{}`, 12)
	if err != nil {
		t.Fatal(err)
	}
	call := makeCall("uncertain-tool", "file_list", map[string]string{"path": "."})
	if err := service.Store.saveModel(ctx, run, model, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(ctx, run.ID)
	if err := service.Store.dispatchTool(ctx, attempts[1], 12); err != nil {
		t.Fatal(err)
	}
	reopenHarness(t, service)
	for i := 0; i < 2; i++ {
		if err := service.Store.Abandon(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	reopenHarness(t, service)
	attempts, _ = service.Store.ListAttempts(ctx, run.ID)
	messages, _ := service.Store.ListMessages(ctx, id)
	abandoned, _ := service.Store.GetRun(ctx, run.ID)
	if abandoned.Status != "abandoned" || attempts[1].Status != "unknown" ||
		len(messages) != 3 || !strings.Contains(messages[2].Content, "UNKNOWN") {
		t.Fatalf("facts were rewritten: run=%+v attempts=%+v messages=%+v", abandoned, attempts, messages)
	}
	var warned bool
	service.Emit = func(event Event) { warned = warned || event.Kind == "unresolved" }
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if !strings.Contains(request.Messages[0].Content, "remain UNKNOWN") {
			t.Fatal("new context forgot unresolved workspace effects")
		}
		return ModelResponse{Content: "Checking previous effects before proceeding."}, nil
	})
	if _, err := service.Run(ctx, conversation(t, service), "new", "check"); err != nil || !warned {
		t.Fatalf("warning=%v error=%v", warned, err)
	}
}

func TestContextLimitDoesNotSilentlyDropWholeRuns(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	id := conversation(t, service)
	snapshot, _ := service.snapshot()
	prior, _, _ := service.Store.CreateRun(ctx, id, "old", "previous task", snapshot)
	model, err := service.Store.startModel(ctx, prior, `{}`, 12)
	if err != nil {
		t.Fatal(err)
	}
	calls := []ToolCall{makeCall("a", "file_list", map[string]string{"path": "."}), makeCall("b", "file_list", map[string]string{"path": "."})}
	if err := service.Store.saveModel(ctx, prior, model, ModelResponse{ToolCalls: calls}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(ctx, prior.ID)
	for i, call := range calls {
		if err := service.Store.saveTool(ctx, prior, attempts[i+1], call, ToolResult{Content: "saved"}); err != nil {
			t.Fatal(err)
		}
	}
	final, _ := service.Store.startModel(ctx, prior, `{}`, 12)
	if err := service.Store.saveModel(ctx, prior, final, ModelResponse{Content: "finished"}); err != nil {
		t.Fatal(err)
	}
	service.Config.MaxMessages = 3
	current, _, _ := service.Store.CreateRun(ctx, id, "new", "next task", snapshot)
	request, err := service.buildContext(ctx, current)
	if !errors.Is(err, ErrContextLimit) || len(request.Messages) != 0 {
		t.Fatalf("old Run silently dropped: context=%+v error=%v", request, err)
	}
	all, _ := service.Store.ListMessages(ctx, id)
	if len(all) != 6 {
		t.Fatalf("original history was removed: %d", len(all))
	}
}

func TestContextByteTailNeverSplitsPriorToolRun(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	id := conversation(t, service)
	snapshot, _ := service.snapshot()
	prior, _, _ := service.Store.CreateRun(ctx, id, "old-large", "old task", snapshot)
	model, _ := service.Store.startModel(ctx, prior, `{}`, 12)
	call := makeCall("large-result", "file_list", map[string]string{"path": "."})
	if err := service.Store.saveModel(ctx, prior, model, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(ctx, prior.ID)
	if err := service.Store.saveTool(ctx, prior, attempts[1], call, ToolResult{Content: strings.Repeat("data ", 6000)}); err != nil {
		t.Fatal(err)
	}
	final, _ := service.Store.startModel(ctx, prior, `{}`, 12)
	if err := service.Store.saveModel(ctx, prior, final, ModelResponse{Content: "done"}); err != nil {
		t.Fatal(err)
	}
	service.Config.MaxContextBytes = 8192
	service.Config.MaxOutputTokens = 32
	current, _, _ := service.Store.CreateRun(ctx, id, "new-small", "next task", snapshot)
	request, err := service.buildContext(ctx, current)
	if !errors.Is(err, ErrContextLimit) || len(request.Messages) != 0 {
		t.Fatalf("partial tool group leaked into byte tail: %+v error=%v", request, err)
	}
}

func TestSchemaTamperingRejectedReadOnly(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	if _, err := service.Store.db.Exec(`DROP INDEX one_active_run`); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "state.sqlite")
	before, _ := os.ReadFile(path)
	if _, err := OpenStore(context.Background(), data); !errors.Is(err, ErrStoreFormat) {
		t.Fatalf("modified current schema accepted: %v", err)
	}
	after, _ := os.ReadFile(path)
	if contentHash(before) != contentHash(after) {
		t.Fatal("rejected store was modified")
	}
}

func TestBackupWALInstructionsRestoreAndTampering(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	data := service.Store.DataDir()
	service.Config.Instructions = []string{"selected.txt"}
	// A model whose key profile does not exist must not block backup or restore.
	service.Config.Models["absent"] = ModelConfig{Provider: "openai-compatible", Key: "k-0badc0de", Model: "absent"}
	service.Config.MCPServers = map[string]MCPServerConfig{"not-started": {
		Enabled: true, Command: filepath.Join(data, "absent-program.exe"), EnvFrom: map[string]string{"TOKEN": "BACKUP_ABSENT_KEY"},
	}}
	if err := os.WriteFile(filepath.Join(data, "instructions", "selected.txt"), []byte("Selected instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "instructions", "ignored.txt"), []byte("Do not copy."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(data, service.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	id := conversation(t, service)
	result, err := service.Run(ctx, id, "wal", "persist me")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(data, "state.sqlite-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("test did not create WAL pages: %v", err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err := service.Store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backup, "instructions", "ignored.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unselected instruction copied")
	}
	restored := filepath.Join(t.TempDir(), "restore")
	if err := Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetRun(ctx, result.Run.ID)
	if err != nil || persisted.Output != result.Answer {
		t.Fatalf("WAL history lost: %+v error=%v", persisted, err)
	}
	_ = store.Close()
	config, err := LoadConfig(restored)
	if err != nil || !strings.Contains(config.instructionText, "Selected") {
		t.Fatalf("instructions lost: error=%v", err)
	}
	if err := Restore(ctx, backup, restored); err == nil {
		t.Fatal("restore overwrote existing target")
	}
	if err := os.WriteFile(filepath.Join(backup, "instructions", "selected.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, backup); err == nil {
		t.Fatal("tampering accepted")
	}
	rejected := filepath.Join(t.TempDir(), "rejected")
	if err := Restore(ctx, backup, rejected); err == nil {
		t.Fatal("tampered backup restored")
	}
	if _, err := os.Stat(rejected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed restore published a target")
	}
}

func TestUsageRecordedWithModelAttempt(t *testing.T) {
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":30,\"completion_tokens\":5,\"total_tokens\":35}}\n\n" +
		"data: [DONE]\n\n"
	response, err := decodeModelStream(strings.NewReader(stream), "", nil)
	if err != nil || response.Usage == nil || response.Usage.TotalTokens != 35 {
		t.Fatalf("usage=%+v error=%v", response.Usage, err)
	}
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return response, nil
	}))
	result, err := service.Run(context.Background(), conversation(t, service), "usage", "task")
	if err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(context.Background(), result.Run.ID)
	var stored ModelResponse
	if err := json.Unmarshal([]byte(attempts[0].ResponseJSON), &stored); err != nil || stored.Usage == nil || stored.Usage.TotalTokens != 35 {
		t.Fatalf("usage not persisted: %+v error=%v", stored, err)
	}
}

func reopenHarness(t *testing.T, service *Service) {
	t.Helper()
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service.Store = store
}
