package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func batchCall(changes ...map[string]string) ToolCall {
	return makeCall("batch", "file_batch_edit", map[string]any{"changes": changes})
}

func TestCreatePreviewRejectOverwriteAndEmptyContent(t *testing.T) {
	_, files, workspace := testHarness(t, EchoModel{})
	for _, content := range []string{"new\nfile", ""} {
		name := "new.txt"
		if content == "" {
			name = "empty.txt"
		}
		prepared, err := files.Prepare(t.Context(), makeCall("create", "file_create", map[string]string{"path": name, "content": content}))
		if err != nil || !strings.Contains(prepared.Preview, name) {
			t.Fatalf("prepare=%+v err=%v", prepared, err)
		}
		if _, err := os.Stat(filepath.Join(workspace, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("creation happened before confirmation")
		}
		if rejected := prepared.Reject(); !rejected.IsError || rejected.Receipts[0].Status != "rejected" {
			t.Fatalf("reject=%+v", rejected)
		}
		result, err := prepared.Execute(t.Context())
		if err != nil || result.Receipts[0].Status != "applied" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		raw, _ := os.ReadFile(filepath.Join(workspace, name))
		if string(raw) != content {
			t.Fatalf("content=%q", raw)
		}
		if result, err := prepared.Execute(t.Context()); err == nil && !result.IsError {
			t.Fatal("creation accepted existing target")
		}
	}
	for _, name := range []string{"../escape.txt", ".git", ".git/new.txt", "missing/child.txt"} {
		if _, err := files.Prepare(t.Context(), makeCall("bad", "file_create", map[string]string{"path": name, "content": "x"})); err == nil {
			t.Fatalf("unsafe creation=%s", name)
		}
	}
}

func TestBatchApprovalInvalidatedForAllFiles(t *testing.T) {
	_, files, workspace := testHarness(t, EchoModel{})
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("before"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := files.Prepare(t.Context(), batchCall(
		map[string]string{"operation": "edit", "path": "one.txt", "expected_sha256": contentHash([]byte("before")), "old_text": "before", "new_text": "after"},
		map[string]string{"operation": "edit", "path": "two.txt", "expected_sha256": contentHash([]byte("before")), "old_text": "before", "new_text": "after"},
	))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(workspace, "two.txt"), []byte("external"), 0o600)
	result, err := prepared.Execute(t.Context())
	if err != nil || !result.IsError || result.Receipts[0].Status != "skipped" || result.Receipts[1].Status != "rejected" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	raw, _ := os.ReadFile(filepath.Join(workspace, "one.txt"))
	if string(raw) != "before" {
		t.Fatal("stale batch wrote an unrelated first file")
	}
}

func TestBatchPartialProgressRecordedAndNotReplayed(t *testing.T) {
	calls := 0
	model := modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{batchCall(
				map[string]string{"operation": "create", "path": "one.txt", "content": "one"},
				map[string]string{"operation": "create", "path": "two.txt", "content": "two"},
			)}}, nil
		}
		return ModelResponse{Content: "Review the recorded results."}, nil
	})
	service, files, workspace := testHarness(t, model)
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	ctx, cancel := context.WithCancel(t.Context())
	runner := &recordingRunner{files: files, after: func(progress ToolResult) {
		if progress.Receipts[0].Status == "applied" {
			cancel()
		}
	}}
	service.Tools = runner
	result, err := service.Run(ctx, conversation(t, service), "batch", "create two files")
	if !errors.Is(err, context.Canceled) || result.Run.Status != "stopped" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	attempts, _ := service.Store.ListAttempts(t.Context(), result.Run.ID)
	var stored ToolResult
	if json.Unmarshal([]byte(attempts[1].ResponseJSON), &stored) != nil || stored.Receipts[0].Status != "applied" ||
		stored.Receipts[1].Status != "skipped" || attempts[1].Status != "finished" {
		t.Fatalf("progress not retained: %+v", attempts)
	}
	reopenHarness(t, service)
	continued, err := service.Continue(t.Context(), result.Run.ID)
	if err != nil || continued.Run.Status != "completed" || calls != 2 {
		t.Fatalf("continue=%+v err=%v", continued, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "two.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("continuation replayed skipped batch item")
	}
	if len(continued.Evidence.Files) != 2 || continued.Evidence.Files[0].Status != "applied" {
		t.Fatalf("evidence=%+v", continued.Evidence)
	}
}

type recordingRunner struct {
	files *FileTools
	after func(ToolResult)
}

func (runner *recordingRunner) Definitions() []ToolDefinition { return runner.files.Definitions() }
func (runner *recordingRunner) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := runner.files.Prepare(ctx, call)
	if err != nil || prepared.ExecuteRecorded == nil {
		return prepared, err
	}
	execute := prepared.ExecuteRecorded
	prepared.ExecuteRecorded = func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
		return execute(ctx, func(result ToolResult) error {
			if err := record(result); err != nil {
				return err
			}
			runner.after(result)
			return nil
		})
	}
	return prepared, nil
}

func TestBatchCrashPreservesKnownProgressAndUnknownNeverContinues(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := t.Context()
	snapshot, _ := service.snapshot()
	run, _, _ := service.Store.CreateRun(ctx, conversation(t, service), "crash-batch", "task", snapshot)
	model, _ := service.Store.startModel(ctx, run, `{}`, 12)
	call := batchCall(map[string]string{"operation": "create", "path": "x", "content": "x"})
	if err := service.Store.saveModel(ctx, run, model, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(ctx, run.ID)
	if err := service.Store.dispatchTool(ctx, attempts[1], 12); err != nil {
		t.Fatal(err)
	}
	progress := ToolResult{Receipts: []ToolReceipt{{Kind: "file", Path: "one", Status: "applied"}, {Kind: "file", Path: "two", Status: "unknown"}}}
	raw, _ := json.Marshal(progress)
	_, _ = service.Store.db.ExecContext(ctx, `UPDATE attempts SET response_json=? WHERE id=?`, string(raw), attempts[1].ID)
	reopenHarness(t, service)
	recovered, _ := service.Store.ListAttempts(ctx, run.ID)
	if recovered[1].Status != "unknown" || recovered[1].ResponseJSON != string(raw) {
		t.Fatalf("crash replaced progress: %+v", recovered)
	}
	if _, err := service.Continue(ctx, run.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown batch replayable: %v", err)
	}
}

func TestBatchRejectsAmbiguousArguments(t *testing.T) {
	_, files, _ := testHarness(t, EchoModel{})
	for _, raw := range []string{
		`{"changes":[{"operation":"create","path":"a","content":null}]}`,
		`{"changes":[{"operation":"create","path":"a"}]}`,
		`{"changes":[{"operation":"create","path":"a","content":"x","new_text":"x"}]}`,
		`{"changes":[{"operation":"create","path":"a","content":"x"},{"operation":"create","path":"A","content":"y"}]}`,
		`{"changes":[]}`,
	} {
		if _, err := files.Prepare(t.Context(), ToolCall{ID: "bad", Name: "file_batch_edit", Arguments: json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted=%s", raw)
		}
	}
}
