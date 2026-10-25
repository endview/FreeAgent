package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPatchExactCreateEditDeleteAndStaleApproval(t *testing.T) {
	_, files, work := testHarness(t, EchoModel{})
	for name, body := range map[string]string{"edit.txt": "one\ntwo\n", "delete.txt": "gone\n"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	patch := "--- a/edit.txt\n+++ b/edit.txt\n@@ -1,2 +1,2 @@\n one\n-two\n+three\n--- /dev/null\n+++ b/create.txt\n@@ -0,0 +1 @@\n+new\n--- a/delete.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-gone\n"
	call := makeCall("patch", "apply_patch", patchArguments{Patch: patch, Expected: map[string]string{"edit.txt": contentHash([]byte("one\ntwo\n")), "delete.txt": contentHash([]byte("gone\n")), "create.txt": "absent"}})
	prepared, err := files.Prepare(t.Context(), call)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Items) != 3 || prepared.Reject().Receipts[0].Status != "rejected" {
		t.Fatal("missing exact approval")
	}
	result, err := prepared.Execute(t.Context())
	if err != nil || result.IsError || len(result.Receipts) != 3 {
		t.Fatalf("apply %+v %v", result, err)
	}
	raw, _ := os.ReadFile(filepath.Join(work, "edit.txt"))
	if string(raw) != "one\nthree\n" {
		t.Fatalf("edit %q", raw)
	}
	if _, err := os.Stat(filepath.Join(work, "delete.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("delete failed")
	}
	if _, err := files.Prepare(t.Context(), call); err == nil {
		t.Fatal("stale source accepted")
	}
}

func TestPatchRejectsAmbiguityAndProtectedPaths(t *testing.T) {
	_, files, _ := testHarness(t, EchoModel{})
	for _, name := range []string{"../escape", "/absolute", ".git/config", ".freeagent/config", "nested/../alias", `C:\escape`, ".git"} {
		patch := "--- /dev/null\n+++ b/" + name + "\n@@ -0,0 +1 @@\n+new\n"
		if _, err := files.Prepare(t.Context(), makeCall("patch", "apply_patch", patchArguments{Patch: patch, Expected: map[string]string{name: "absent"}})); err == nil {
			t.Fatalf("unsafe path %s", name)
		}
	}
	for _, patch := range []string{"diff --git a/x b/x\n", "--- a/x\n+++ b/y\n@@ -1 +1 @@\n-a\n+b\n", "--- /dev/null\n+++ b/x\n@@ -0,0 +2 @@\n+x\n", "--- /dev/null\n+++ b/x\n@@ -0,0 +1,2 @@\n+x\n"} {
		if _, err := files.Prepare(t.Context(), makeCall("patch", "apply_patch", patchArguments{Patch: patch, Expected: map[string]string{"x": "absent"}})); err == nil {
			t.Fatalf("invalid patch %q", patch)
		}
	}
}

func TestPatchPartialResultsStopOnFirstFailure(t *testing.T) {
	_, files, work := testHarness(t, EchoModel{})
	patch := "--- /dev/null\n+++ b/first\n@@ -0,0 +1 @@\n+a\n--- /dev/null\n+++ b/second\n@@ -0,0 +1 @@\n+b\n--- /dev/null\n+++ b/third\n@@ -0,0 +1 @@\n+c\n"
	prepared, err := files.Prepare(t.Context(), makeCall("patch", "apply_patch", patchArguments{Patch: patch, Expected: map[string]string{"first": "absent", "second": "absent", "third": "absent"}}))
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	result, err := prepared.ExecuteRecorded(t.Context(), func(progress ToolResult) error {
		if !changed && progress.Receipts[0].Status == "applied" {
			changed = true
			return os.WriteFile(filepath.Join(work, "second"), []byte("external"), 0600)
		}
		return nil
	})
	if err != nil || !result.IsError || result.Receipts[0].Status != "applied" || result.Receipts[1].Status != "failed" || result.Receipts[2].Status != "skipped" {
		t.Fatalf("partial %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(work, "third")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("continued after failure")
	}
}

func TestPatchCRLFAndNoFinalNewline(t *testing.T) {
	output, err := applyHunks("one\r\ntwo\r\n", []patchHunk{{oldStart: 1, oldCount: 2, newStart: 1, newCount: 2, lines: []string{" one", "-two", "+three"}}})
	if err != nil || output != "one\r\nthree\r\n" {
		t.Fatalf("CRLF %q %v", output, err)
	}
	files, err := parsePatch("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n")
	if err != nil {
		t.Fatal(err)
	}
	output, err = applyHunks("old", files[0].hunks)
	if err != nil || output != "new" {
		t.Fatalf("EOF %q %v", output, err)
	}
}

func TestToolOutcomePreservesCompletionAndBoundsUTF8(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := normalizeToolOutcome(ctx, ToolResult{Content: "known completed"}, nil, time.Now(), 200)
	if !result.Outcome.CompletionKnown || !result.Outcome.Cancelled || result.Outcome.Status != "completed" {
		t.Fatalf("late cancel erased completion: %+v", result)
	}
	result = normalizeToolOutcome(ctx, ToolResult{Content: strings.Repeat("界", 100)}, &CallError{Certain: false}, time.Now(), 10)
	if result.Outcome.CompletionKnown || !result.Truncated || !utf8.ValidString(result.Content) || len(result.Content) > 10 {
		t.Fatalf("unknown/truncation %+v", result)
	}
}

func TestPatchUsesSharedAskAndWorkspaceApproval(t *testing.T) {
	for _, mode := range []PermissionMode{PermissionAsk, PermissionWorkspace} {
		t.Run(string(mode), func(t *testing.T) {
			count := 0
			service, _, work := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				count++
				if count == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("patch", "apply_patch", patchArguments{Patch: "--- /dev/null\n+++ b/exact.txt\n@@ -0,0 +1 @@\n+exact\n", Expected: map[string]string{"exact.txt": "absent"}})}}, nil
				}
				return ModelResponse{Content: "confirmed result"}, nil
			}))
			tasks := NewTaskService(service)
			chat := conversation(t, service)
			result, err := tasks.SubmitWithOptions(t.Context(), chat, "patch", "apply exact patch", RunOptions{Permission: mode})
			if err != nil {
				t.Fatal(err)
			}
			if mode == PermissionAsk {
				if result.Run.Status != "waiting_confirmation" {
					t.Fatal("Ask bypassed")
				}
				preview, err := tasks.Preview(t.Context(), result.Run.ID)
				if err != nil {
					t.Fatal(err)
				}
				result, err = tasks.Confirm(t.Context(), result.Run.ID, preview, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(filepath.Join(work, "exact.txt"))
			if err != nil || string(raw) != "exact\n" || result.Run.Status != "completed" {
				t.Fatalf("mode result %+v %q %v", result, raw, err)
			}
		})
	}
}
