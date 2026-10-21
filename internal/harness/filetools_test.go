package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileListPagesWithoutLosingEntries(t *testing.T) {
	service, tools, workspace := testHarness(t, EchoModel{})
	for index := 0; index < 237; index++ {
		name := fmt.Sprintf("entry-%03d.txt", index)
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	offset := 0
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("listing did not terminate")
		}
		prepared, err := tools.Prepare(context.Background(), makeCall("list", "file_list", map[string]any{
			"path": ".", "offset": offset, "max_entries": 40,
		}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := prepared.Execute(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var listing struct {
			Entries []struct {
				Name string `json:"name"`
			} `json:"entries"`
			Offset     int  `json:"offset"`
			NextOffset int  `json:"next_offset"`
			Truncated  bool `json:"truncated"`
		}
		if err := json.Unmarshal([]byte(result.Content), &listing); err != nil || len(listing.Entries) > 40 || listing.Offset != offset {
			t.Fatalf("listing=%+v error=%v", listing, err)
		}
		for _, item := range listing.Entries {
			if item.Name == ".git" || seen[item.Name] {
				t.Fatalf("protected or repeated entry: %s", item.Name)
			}
			seen[item.Name] = true
		}
		if !listing.Truncated {
			break
		}
		if listing.NextOffset <= offset {
			t.Fatalf("listing did not advance: %+v", listing)
		}
		offset = listing.NextOffset
	}
	if len(seen) != 237 {
		t.Fatalf("listing lost files: %d", len(seen))
	}
	if err := os.Mkdir(filepath.Join(workspace, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	nested, err := NewFileTools(workspace, filepath.Join(workspace, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer nested.Close()
	prepared, err := nested.Prepare(context.Background(), makeCall("list", "file_list", map[string]any{"path": "state", "offset": 0}))
	if err == nil || prepared.Execute != nil {
		t.Fatal("pagination made state directory accessible")
	}
	_ = service
}

func TestFileReadSmallUTF8PagesAlwaysAdvance(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	text := "\u4e2d\u6587\U0001f642abc\u6587"
	if err := os.WriteFile(filepath.Join(workspace, "unicode.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var assembled strings.Builder
			offset := 0
			for page := 0; ; page++ {
				if page > len(text) {
					t.Fatal("file read did not terminate")
				}
				prepared, err := tools.Prepare(context.Background(), makeCall("read", "file_read", map[string]any{
					"path": "unicode.txt", "offset": offset, "max_bytes": size,
				}))
				if err != nil {
					t.Fatal(err)
				}
				result, err := prepared.Execute(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var chunk struct {
					Content    string `json:"content"`
					Offset     int    `json:"offset"`
					NextOffset int    `json:"next_offset"`
					Truncated  bool   `json:"truncated"`
					SHA256     string `json:"sha256"`
				}
				if err := json.Unmarshal([]byte(result.Content), &chunk); err != nil {
					t.Fatal(err)
				}
				if !utf8.ValidString(chunk.Content) || len(chunk.Content) > max(size, utf8.UTFMax) ||
					chunk.Offset != offset || chunk.NextOffset <= offset || chunk.SHA256 != contentHash([]byte(text)) {
					t.Fatalf("invalid chunk: %+v", chunk)
				}
				assembled.WriteString(chunk.Content)
				if !chunk.Truncated {
					break
				}
				offset = chunk.NextOffset
			}
			if assembled.String() != text {
				t.Fatalf("text changed: %q", assembled.String())
			}
		})
	}
}

func TestFileToolsRejectNullArgumentsAndUnreadableEdits(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, call := range []ToolCall{
		{ID: "null-offset", Name: "file_read", Arguments: json.RawMessage(`{"path":"note.txt","offset":null}`)},
		{ID: "zero-size", Name: "file_read", Arguments: json.RawMessage(`{"path":"note.txt","max_bytes":0}`)},
		{ID: "null-size", Name: "file_list", Arguments: json.RawMessage(`{"path":".","max_entries":null}`)},
		{ID: "zero-entries", Name: "file_list", Arguments: json.RawMessage(`{"path":".","max_entries":0}`)},
		{ID: "oversized-page", Name: "file_list", Arguments: json.RawMessage(`{"path":".","max_entries":201}`)},
		{ID: "negative-offset", Name: "file_list", Arguments: json.RawMessage(`{"path":".","offset":-1}`)},
		makeCall("null-edit", "file_edit", map[string]any{"path": "note.txt", "expected_sha256": contentHash([]byte("original")), "old_text": "original", "new_text": nil}),
		makeCall("nul-edit", "file_edit", map[string]string{"path": "note.txt", "expected_sha256": contentHash([]byte("original")), "old_text": "original", "new_text": "bad\x00text"}),
	} {
		if _, err := tools.Prepare(context.Background(), call); err == nil {
			t.Errorf("accepted invalid call: %s", call.ID)
		}
	}
}

func TestFileDeleteRequiresExactHashAndConfirmsAbsence(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	name := filepath.Join(workspace, "delete-me.bin")
	content := []byte{0, 1, 2, 3, 4}
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentHash(content)
	prepared, err := tools.Prepare(context.Background(), makeCall("delete", "file_delete", map[string]string{
		"path": "delete-me.bin", "expected_sha256": expected,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Effect != EffectModify || !prepared.workspaceFile || len(prepared.Items) != 1 ||
		prepared.Items[0].Kind != "file_delete" || prepared.Items[0].Hash != expected ||
		prepared.ExecuteRecorded == nil {
		t.Fatalf("delete preview is not exact: %+v", prepared)
	}
	if rejected := prepared.Reject(); len(rejected.Receipts) != 1 || rejected.Receipts[0].Status != "rejected" {
		t.Fatalf("rejection receipt=%+v", rejected)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("declined delete changed target: %v", err)
	}
	result, err := prepared.Execute(context.Background())
	if err != nil || result.IsError || len(result.Receipts) != 1 || result.Receipts[0].Status != "applied" {
		t.Fatalf("delete result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("delete was not confirmed: err=%v", err)
	}
	if _, err := tools.Prepare(context.Background(), makeCall("stale", "file_delete", map[string]string{
		"path": "delete-me.bin", "expected_sha256": expected,
	})); err == nil {
		t.Fatal("deleted target remained preparable")
	}
}

func TestFileDeletePersistsDispatchBeforeRemoving(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	name := filepath.Join(workspace, "delete-me.txt")
	content := []byte("delete me")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := tools.Prepare(t.Context(), makeCall("delete", "file_delete", map[string]string{
		"path": "delete-me.txt", "expected_sha256": contentHash(content),
	}))
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	tools.removeFile = func(string) error {
		removed = true
		return errors.New("must not run")
	}
	result, err := prepared.ExecuteRecorded(t.Context(), func(ToolResult) error {
		return errors.New("record unavailable")
	})
	var certainty *CallError
	if !errors.As(err, &certainty) || !certainty.Certain || removed ||
		result.Receipts[0].Status != "not_launched" {
		t.Fatalf("delete crossed persistence boundary: result=%+v err=%v removed=%t", result, err, removed)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("unrecorded delete changed target: %v", err)
	}
}

func TestFileDeleteUnknownReceiptSurvivesRestartAndNeverReplays(t *testing.T) {
	modelCalls := 0
	content := []byte("delete me")
	model := modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{ToolCalls: []ToolCall{makeCall("delete", "file_delete", map[string]string{
			"path": "delete-me.txt", "expected_sha256": contentHash(content),
		})}}, nil
	})
	service, tools, workspace := testHarness(t, model)
	name := filepath.Join(workspace, "delete-me.txt")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	tools.postDeleteLstat = func(string) (os.FileInfo, error) {
		return nil, errors.New("fixture post-delete state unavailable")
	}
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	result, err := service.Run(t.Context(), conversation(t, service), "delete-unknown", "delete the file")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" || modelCalls != 1 {
		t.Fatalf("unknown delete became definitive: result=%+v err=%v calls=%d", result, err, modelCalls)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture delete did not occur: %v", err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored ToolResult
	if len(attempts) != 2 || attempts[1].Status != "unknown" ||
		json.Unmarshal([]byte(attempts[1].ResponseJSON), &stored) != nil ||
		len(stored.Receipts) != 1 || stored.Receipts[0].Path != "delete-me.txt" ||
		stored.Receipts[0].BeforeHash != contentHash(content) ||
		stored.Receipts[0].Status != "unknown" ||
		!strings.Contains(stored.Receipts[0].Detail, "could not be confirmed") {
		t.Fatalf("unknown delete receipt not retained: attempts=%+v stored=%+v", attempts, stored)
	}
	if len(result.Evidence.Files) != 1 ||
		!strings.Contains(strings.Join(result.Evidence.Uncertainty, "\n"), "delete-me.txt") {
		t.Fatalf("unknown delete evidence incomplete: %+v", result.Evidence)
	}
	reopenHarness(t, service)
	before := modelCalls
	if _, err := service.Continue(t.Context(), result.Run.ID); !errors.Is(err, ErrUnknownResult) ||
		modelCalls != before {
		t.Fatalf("unknown delete replayed after restart: err=%v calls=%d", err, modelCalls)
	}
	recovered, err := service.Store.Evidence(t.Context(), result.Run.ID)
	if err != nil || len(recovered.Files) != 1 ||
		!strings.Contains(strings.Join(recovered.Uncertainty, "\n"), "could not be confirmed") {
		t.Fatalf("restarted evidence lost delete facts: %+v err=%v", recovered, err)
	}
}

type finalDeleteProgressFailureRunner struct {
	files *FileTools
}

func (runner finalDeleteProgressFailureRunner) Definitions() []ToolDefinition {
	return runner.files.Definitions()
}

func (runner finalDeleteProgressFailureRunner) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	prepared, err := runner.files.Prepare(ctx, call)
	if err != nil || prepared.ExecuteRecorded == nil {
		return prepared, err
	}
	execute := prepared.ExecuteRecorded
	prepared.ExecuteRecorded = func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
		return execute(ctx, func(result ToolResult) error {
			if len(result.Receipts) == 1 &&
				result.Receipts[0].Status == "unknown" &&
				strings.Contains(result.Receipts[0].Detail, "could not be confirmed") {
				return errors.New("final progress persistence failed")
			}
			return record(result)
		})
	}
	return prepared, nil
}

func TestFileDeleteFinalUnknownReceiptSurvivesProgressPersistenceFailure(t *testing.T) {
	content := []byte("delete me")
	service, files, workspace := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("delete", "file_delete", map[string]string{
			"path": "delete-me.txt", "expected_sha256": contentHash(content),
		})}}, nil
	}))
	name := filepath.Join(workspace, "delete-me.txt")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	files.postDeleteLstat = func(string) (os.FileInfo, error) {
		return nil, errors.New("fixture post-delete state unavailable")
	}
	service.Tools = finalDeleteProgressFailureRunner{files: files}
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	result, err := service.Run(t.Context(), conversation(t, service), "delete-final-unknown", "delete the file")
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored ToolResult
	if len(attempts) != 2 || attempts[1].Status != "unknown" ||
		json.Unmarshal([]byte(attempts[1].ResponseJSON), &stored) != nil ||
		len(stored.Receipts) != 1 || stored.Receipts[0].Status != "unknown" ||
		stored.Receipts[0].Path != "delete-me.txt" ||
		!strings.Contains(stored.Receipts[0].Detail, "could not be confirmed") {
		t.Fatalf("service lost final UNKNOWN receipt: attempts=%+v stored=%+v", attempts, stored)
	}
}

func TestFileDeleteRejectsChangedTargetsAndDirectories(t *testing.T) {
	_, tools, workspace := testHarness(t, EchoModel{})
	if err := os.WriteFile(filepath.Join(workspace, "changed.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentHash([]byte("before"))
	if err := os.WriteFile(filepath.Join(workspace, "changed.txt"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Prepare(context.Background(), makeCall("changed", "file_delete", map[string]string{
		"path": "changed.txt", "expected_sha256": expected,
	})); err == nil {
		t.Fatal("changed target accepted with stale hash")
	}
	if err := os.Mkdir(filepath.Join(workspace, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Prepare(context.Background(), makeCall("directory", "file_delete", map[string]string{
		"path": "directory", "expected_sha256": strings.Repeat("0", 64),
	})); err == nil {
		t.Fatal("directory accepted for file deletion")
	}
}
