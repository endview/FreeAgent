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
)

type modelFunc func(context.Context, ModelRequest, func(string)) (ModelResponse, error)

func (fn modelFunc) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	return fn(ctx, request, emit)
}

func testHarness(t *testing.T, model ModelClient) (*Service, *FileTools, string) {
	t.Helper()
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	data := filepath.Join(base, "state")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := InitStore(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	config := testEchoConfig(workspace)
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tools, err := NewFileTools(workspace, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close() })
	return &Service{Store: store, Model: model, Tools: tools, Config: config}, tools, workspace
}

func conversation(t *testing.T, service *Service) string {
	t.Helper()
	item, err := service.Store.CreateConversation(context.Background(), "test", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func TestConversationPinningUsesExistingMetadataAndKeepsSeparateLimits(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	first, err := service.Store.CreateConversation(ctx, "first", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Store.CreateConversation(ctx, "second", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.PinConversation(ctx, first.ID, true); err != nil {
		t.Fatal(err)
	}
	view, err := service.Store.GetConversation(ctx, first.ID)
	if err != nil || !view.Pinned || view.PinnedAt == "" {
		t.Fatalf("pinned view=%+v err=%v", view, err)
	}
	list, err := service.Store.ListConversations(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if list[0].ID != first.ID || !list[0].Pinned || list[1].ID != second.ID || list[1].Pinned {
		t.Fatalf("pin ordering=%+v", list)
	}
	if err := service.Store.PinConversation(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	view, err = service.Store.GetConversation(ctx, first.ID)
	if err != nil || view.Pinned || view.PinnedAt != "" {
		t.Fatalf("unpinned view=%+v err=%v", view, err)
	}
	for index := 0; index < 50; index++ {
		item, createErr := service.Store.CreateConversation(ctx, fmt.Sprintf("limit-%d", index), service.Config.Workspace)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if pinErr := service.Store.PinConversation(ctx, item.ID, true); pinErr != nil {
			t.Fatalf("pin %d: %v", index, pinErr)
		}
	}
	last, err := service.Store.CreateConversation(ctx, "over-limit", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.PinConversation(ctx, last.ID, true); !errors.Is(err, ErrPinnedLimit) {
		t.Fatalf("over-limit error=%v", err)
	}
}

func makeCall(id, name string, args any) ToolCall {
	raw, _ := json.Marshal(args)
	return ToolCall{ID: id, Name: name, Arguments: raw}
}

func TestOpenStoreRejectsV4(t *testing.T) {
	if !strings.HasSuffix(StoreFormat, "-v7") {
		t.Fatalf("store format %q", StoreFormat)
	}
	for name, statement := range map[string]string{
		"user_version": `PRAGMA user_version=4`,
		"format":       `UPDATE meta SET value='` + strings.Replace(StoreFormat, "v7", "v6", 1) + `' WHERE key='format'`,
	} {
		t.Run(name, func(t *testing.T) {
			service, _, _ := testHarness(t, EchoModel{})
			data := service.Store.DataDir()
			if _, err := service.Store.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := service.Store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(data, "state.sqlite")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if store, err := OpenStore(context.Background(), data); !errors.Is(err, ErrStoreFormat) {
				if store != nil {
					_ = store.Close()
				}
				t.Fatalf("v3 store error=%v", err)
			}
			if after, _ := os.ReadFile(path); contentHash(before) != contentHash(after) {
				t.Fatal("rejected v3 store was modified")
			}
		})
	}
}

// Decision 4 depends on this: an engine that already owns the data directory
// cannot open it a second time, so in-engine edits must reuse its store.
func TestSecondLockInSameProcessFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := acquireProcessFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireProcessFileLock(path); !errors.Is(err, ErrOwnerActive) {
		_ = second.release()
		_ = first.release()
		t.Fatalf("second lock error=%v", err)
	}
	if err := first.release(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireProcessFileLock(path)
	if err != nil {
		t.Fatalf("lock not reusable after release: %v", err)
	}
	if err := third.release(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshStoreIsolationPersistenceAndOwner(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	id := conversation(t, service)
	result, err := service.Run(ctx, id, "key-1", "hello")
	if err != nil || result.Run.Status != "completed" || result.Answer != "Echo: hello" {
		t.Fatalf("run=%+v error=%v", result, err)
	}
	if second, err := OpenStore(ctx, service.Store.DataDir()); !errors.Is(err, ErrOwnerActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second writer error=%v", err)
	}
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.ListMessages(ctx, id)
	if err != nil || len(messages) != 2 || messages[1].Content != "Echo: hello" {
		t.Fatalf("history=%+v error=%v", messages, err)
	}
	for _, name := range []string{"config.json", "state.sqlite", "instructions", "backups"} {
		if _, err := os.Stat(filepath.Join(data, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestInitRejectsNonemptyAndOpenRejectsUnknownWithoutChangingFile(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "state.sqlite")
	original := []byte("not our database")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InitStore(context.Background(), base); err == nil {
		t.Fatal("initialization accepted nonempty directory")
	}
	if _, err := OpenStore(context.Background(), base); !errors.Is(err, ErrStoreFormat) {
		t.Fatalf("unknown format error=%v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("unknown database was modified")
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 {
		t.Fatalf("opening unknown data created extra files: %v", entries)
	}
}

func TestFileToolsReadListSearchEditAndConflicts(t *testing.T) {
	service, tools, workspace := testHarness(t, EchoModel{})
	ctx := context.Background()
	file := filepath.Join(workspace, "notes.txt")
	original := "hello\nneedle here\n"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, call := range []ToolCall{
		makeCall("list", "file_list", map[string]string{"path": "."}),
		makeCall("read", "file_read", map[string]string{"path": "notes.txt"}),
		makeCall("search", "file_search", map[string]string{"path": ".", "query": "needle"}),
	} {
		prepared, err := tools.Prepare(ctx, call)
		if err != nil {
			t.Fatal(err)
		}
		result, err := prepared.Execute(ctx)
		if err != nil || !strings.Contains(result.Content, "notes.txt") {
			t.Fatalf("call=%s result=%+v error=%v", call.Name, result, err)
		}
	}
	edit := makeCall("edit", "file_edit", map[string]string{
		"path": "notes.txt", "expected_sha256": contentHash([]byte(original)), "old_text": "hello", "new_text": "goodbye",
	})
	prepared, err := tools.Prepare(ctx, edit)
	if err != nil || prepared.Effect != EffectModify || !strings.Contains(prepared.Preview, "goodbye") {
		t.Fatalf("prepare=%+v error=%v", prepared, err)
	}
	if err := os.WriteFile(file, []byte("external change"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Execute(ctx); err == nil {
		t.Fatal("edit accepted stale content")
	}
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err = tools.Prepare(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if string(raw) != "goodbye\nneedle here\n" {
		t.Fatalf("edited content=%q", raw)
	}
	_ = service
}

func TestFileToolsRejectEscapesLinksHardlinksAndState(t *testing.T) {
	service, tools, workspace := testHarness(t, EchoModel{})
	if err := os.WriteFile(filepath.Join(workspace, "plain.txt"), []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside.txt", service.Store.DataDir(), "C:/Windows/win.ini", ".git/config"} {
		if _, err := tools.Prepare(context.Background(), makeCall("bad", "file_read", map[string]string{"path": name})); err == nil {
			t.Errorf("accepted unsafe path %q", name)
		}
	}
	if err := os.Link(filepath.Join(workspace, "plain.txt"), filepath.Join(workspace, "alias.txt")); err == nil {
		prepared, err := tools.Prepare(context.Background(), makeCall("link", "file_read", map[string]string{"path": "alias.txt"}))
		if err == nil {
			if _, err := prepared.Execute(context.Background()); err == nil {
				t.Fatal("accepted hard-linked file")
			}
		}
	}
	if err := os.Symlink(service.Store.DataDir(), filepath.Join(workspace, "state-link")); err == nil {
		if _, err := tools.Prepare(context.Background(), makeCall("link", "file_read", map[string]string{"path": "state-link/config.json"})); err == nil {
			t.Fatal("accepted symlink")
		}
	}
}

func TestMultiRoundToolsRestartAndRequestDeduplication(t *testing.T) {
	calls := 0
	model := modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
		calls++
		last := req.Messages[len(req.Messages)-1]
		switch calls {
		case 1:
			return ModelResponse{ToolCalls: []ToolCall{
				makeCall("list-1", "file_list", map[string]string{"path": "."}),
				makeCall("search-1", "file_search", map[string]string{"path": ".", "query": "needle"}),
			}}, nil
		case 2:
			if last.Role != "tool" || last.ToolCallID != "search-1" {
				t.Fatalf("tools mismatched: %+v", last)
			}
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read-1", "file_read", map[string]string{"path": "notes.txt"})}}, nil
		case 3:
			if last.Role != "tool" || !strings.Contains(last.Content, "needle") {
				t.Fatalf("missing tool result: %+v", last)
			}
			return ModelResponse{Content: "Found the needle."}, nil
		case 4:
			if len(req.Messages) < 8 {
				t.Fatalf("history lost after restart: %+v", req)
			}
			return ModelResponse{Content: "Continuing our previous task."}, nil
		}
		t.Fatalf("unexpected model call %d", calls)
		return ModelResponse{}, nil
	})
	service, _, workspace := testHarness(t, model)
	_ = os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("a needle"), 0o600)
	id := conversation(t, service)
	result, err := service.Run(context.Background(), id, "request", "find the needle")
	if err != nil || result.Answer != "Found the needle." {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	repeated, err := service.Run(context.Background(), id, "request", "find the needle")
	if err != nil || !repeated.Duplicate || calls != 3 || repeated.Run.ID != result.Run.ID {
		t.Fatalf("duplicate=%+v error=%v calls=%d", repeated, err, calls)
	}
	if _, err := service.Run(context.Background(), id, "request", "different task"); !errors.Is(err, ErrRequestKey) {
		t.Fatalf("request mismatch error=%v", err)
	}
	data := service.Store.DataDir()
	_ = service.Store.Close()
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service.Store = store
	next, err := service.Run(context.Background(), id, "request-2", "continue")
	if err != nil || next.Answer != "Continuing our previous task." {
		t.Fatalf("continued=%+v error=%v", next, err)
	}
}

func TestWriteNeedsConfirmationAndCanContinueAfterRestart(t *testing.T) {
	modelCalls := 0
	original := []byte("before")
	model := modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("edit-1", "file_edit", map[string]string{
				"path": "notes.txt", "expected_sha256": contentHash(original), "old_text": "before", "new_text": "after",
			})}}, nil
		}
		return ModelResponse{Content: "Changed."}, nil
	})
	service, _, workspace := testHarness(t, model)
	_ = os.WriteFile(filepath.Join(workspace, "notes.txt"), original, 0o600)
	id := conversation(t, service)
	result, err := service.Run(context.Background(), id, "write", "change the file")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting=%+v error=%v", result, err)
	}
	raw, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(raw) != "before" {
		t.Fatal("wrote without confirmation")
	}
	data := service.Store.DataDir()
	_ = service.Store.Close()
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service.Store = store
	service.Approve = func(ctx context.Context, call ToolCall, prepared PreparedTool) (bool, error) { return true, nil }
	continued, err := service.Continue(context.Background(), result.Run.ID)
	if err != nil || continued.Run.Status != "completed" || modelCalls != 2 {
		t.Fatalf("continue=%+v error=%v model calls=%d", continued, err, modelCalls)
	}
	raw, _ = os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(raw) != "after" {
		t.Fatalf("file=%q", raw)
	}
}

func TestRecoveryUnknownNeverReplaysAndKnownResultContinues(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "saved"}[known], func(t *testing.T) {
			modelCalls := 0
			model := modelFunc(func(ctx context.Context, req ModelRequest, emit func(string)) (ModelResponse, error) {
				modelCalls++
				return ModelResponse{Content: "reused saved result"}, nil
			})
			service, _, _ := testHarness(t, model)
			ctx := context.Background()
			id := conversation(t, service)
			snapshot, err := service.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := service.Store.CreateRun(ctx, id, "crash", "do work", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := service.Store.startModel(ctx, run, `{}`, service.Config.MaxSteps)
			if err != nil {
				t.Fatal(err)
			}
			call := makeCall("tool-1", "file_list", map[string]string{"path": "."})
			if err := service.Store.saveModel(ctx, run, attempt, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
				t.Fatal(err)
			}
			attempts, _ := service.Store.ListAttempts(ctx, run.ID)
			toolAttempt := attempts[1]
			if err := service.Store.dispatchTool(ctx, toolAttempt, service.Config.MaxSteps); err != nil {
				t.Fatal(err)
			}
			if known {
				if err := service.Store.saveTool(ctx, run, toolAttempt, call, ToolResult{Content: "already saved"}); err != nil {
					t.Fatal(err)
				}
			}
			data := service.Store.DataDir()
			_ = service.Store.Close()
			store, err := OpenStore(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			service.Store = store
			result, err := service.Continue(ctx, run.ID)
			if known {
				if err != nil || result.Run.Status != "completed" || modelCalls != 1 {
					t.Fatalf("known=%+v error=%v model calls=%d", result, err, modelCalls)
				}
			} else if !errors.Is(err, ErrUnknownResult) || modelCalls != 0 {
				t.Fatalf("unknown was replayed: result=%+v error=%v calls=%d", result, err, modelCalls)
			}
		})
	}
}

func TestCancellationBeforeDispatchDoesNotModifyFile(t *testing.T) {
	original := []byte("before")
	model := modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("edit", "file_edit", map[string]string{
			"path": "notes.txt", "expected_sha256": contentHash(original), "old_text": "before", "new_text": "after",
		})}}, nil
	})
	service, _, workspace := testHarness(t, model)
	_ = os.WriteFile(filepath.Join(workspace, "notes.txt"), original, 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) {
		cancel()
		return true, nil
	}
	id := conversation(t, service)
	result, _ := service.Run(ctx, id, "cancel", "edit")
	attempts, err := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[1].Status != "pending" {
		t.Fatalf("unexpected dispatch after cancellation: %+v", attempts)
	}
	raw, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(raw) != "before" {
		t.Fatal("file changed after cancellation")
	}
}

func TestContextKeepsToolGroupsAndStepLimitSurvivesContinue(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("one", "file_list", map[string]string{"path": "."})}}, nil
	}))
	service.Config.MaxSteps = 2
	id := conversation(t, service)
	result, err := service.Run(context.Background(), id, "limit", "loop")
	if !errors.Is(err, ErrStepLimit) {
		t.Fatalf("limit error=%v", err)
	}
	if _, err := service.Continue(context.Background(), result.Run.ID); !errors.Is(err, ErrStepLimit) {
		t.Fatalf("continue reset limit: %v", err)
	}
}
