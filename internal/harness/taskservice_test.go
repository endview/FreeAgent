package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfirmationItemsAreDigestBound(t *testing.T) {
	call := ToolCall{ID: "call-1", Name: "file_edit", Arguments: json.RawMessage(`{"path":"note.txt"}`)}
	first := confirmationFor("run-1", "attempt-1", call, PreparedTool{
		Preview: "note.txt\nSHA-256: before\n--- replace\nold\n+++ with\nnew",
		Items:   []ApprovalItem{{Kind: "file_edit", Target: "note.txt", Hash: "before", Old: "old", New: "new"}},
	})
	second := confirmationFor("run-1", "attempt-1", call, PreparedTool{
		Preview: first.Preview,
		Items:   []ApprovalItem{{Kind: "file_edit", Target: "other.txt", Hash: "before", Old: "old", New: "new"}},
	})
	if first.Digest == second.Digest {
		t.Fatal("changing approval items did not change digest")
	}
	if len(first.Items) != 1 || first.Items[0].Target != "note.txt" {
		t.Fatalf("confirmation items=%+v", first.Items)
	}
	raw, err := json.Marshal(first)
	if err != nil || !strings.Contains(string(raw), `"items"`) || !strings.Contains(string(raw), `"target":"note.txt"`) {
		t.Fatalf("confirmation JSON=%s err=%v", raw, err)
	}
}

func TestTaskServiceConfirmsOnlyExactSavedOperation(t *testing.T) {
	count := 0
	service, _, workspace := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		count++
		if count == 1 {
			return ModelResponse{ToolCalls: []ToolCall{
				makeCall("one", "file_create", map[string]string{"path": "one.txt", "content": "one"}),
				makeCall("two", "file_create", map[string]string{"path": "two.txt", "content": "two"}),
			}}, nil
		}
		return ModelResponse{Content: "Created."}, nil
	}))
	tasks := NewTaskService(service)
	waiting, err := tasks.Submit(t.Context(), "", "task", "create two files")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	decision, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil || decision.Tool != "file_create" || decision.CallID != "one" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	modified := decision
	modified.Digest = "different-change"
	if _, err := tasks.Confirm(t.Context(), waiting.Run.ID, modified, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatalf("modified confirmation accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "one.txt")); !os.IsNotExist(err) {
		t.Fatal("mismatching decision dispatched")
	}
	next, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision, true)
	if err != nil || next.Run.Status != "waiting_confirmation" || count != 1 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "two.txt")); !os.IsNotExist(err) {
		t.Fatal("first decision authorized second file")
	}
	decision2, err := tasks.Preview(t.Context(), waiting.Run.ID)
	if err != nil || decision2.CallID != "two" {
		t.Fatalf("second=%+v err=%v", decision2, err)
	}
	finished, err := tasks.Confirm(t.Context(), waiting.Run.ID, decision2, false)
	if err != nil || finished.Run.Status != "completed" || count != 2 ||
		len(finished.Evidence.Files) != 2 || finished.Evidence.Files[1].Status != "rejected" {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	duplicate, err := tasks.Submit(t.Context(), waiting.Run.ConversationID, "task", "create two files")
	if err != nil || !duplicate.Duplicate || count != 2 {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
}

func TestTaskServiceCancellationAndAdapterSnapshot(t *testing.T) {
	started := make(chan struct{})
	service, _, _ := testHarness(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}))
	tasks := NewTaskService(service)
	var events atomic.Int32
	tasks.SetEvents(func(Event) { events.Add(1) })
	done := make(chan error, 1)
	go func() {
		_, err := tasks.Submit(t.Context(), "", "", "cancellable")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model did not start")
	}
	tasks.SetEvents(func(Event) { t.Error("active call changed event adapter") })
	if _, err := tasks.Submit(t.Context(), "", "", "second"); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("concurrent call=%v", err)
	}
	if !tasks.Cancel() {
		t.Fatal("cancel had no target")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknownResult) {
			t.Fatalf("ambiguous cancellation became replayable: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not propagate")
	}
	if tasks.Cancel() || events.Load() == 0 {
		t.Fatal("cancellation or events not finalized")
	}
}

func TestOpenTasksReplacesAdaptersAndDisabledToolsDoNotStart(t *testing.T) {
	service, files, _ := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	config := service.Config
	config.MCPServers = map[string]MCPServerConfig{"never": {Enabled: false, Command: "missing-command"}}
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err := OpenTasks(t.Context(), data, Adapters{Model: EchoModel{}, Tools: files})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	result, err := tasks.Submit(t.Context(), "", "", "offline caller")
	if err != nil || result.Answer != "Echo: offline caller" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestTaskServiceWorkflowPersistsAssistantOutputAndDeduplicates(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[{"name":"summarize","description":"Summarize input",
			"input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	tasks := NewTaskService(service)
	chat := conversation(t, service)

	first, err := tasks.RunWorkflow(t.Context(), chat, "workflow-request", extensionID, "summarize",
		map[string]any{"text": "hello"})
	if err != nil || first.Run.Status != "completed" || first.Answer != "workflow:hello" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil || len(messages) != 2 || messages[1].Role != "assistant" || messages[1].Content != "workflow:hello" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), first.Run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Kind != "workflow" || attempts[0].Status != "finished" {
		t.Fatalf("workflow attempts=%+v err=%v", attempts, err)
	}
	var workflowRequest workflowAttemptRequest
	if err := json.Unmarshal([]byte(attempts[0].RequestJSON), &workflowRequest); err != nil ||
		workflowRequest.ExtensionID != extensionID || workflowRequest.Workflow != "summarize" ||
		string(workflowRequest.Input) != `{"text":"hello"}` {
		t.Fatalf("workflow request=%+v err=%v", workflowRequest, err)
	}
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(view.Turns) != 1 || len(view.Turns[0].Activity) != 1 ||
		view.Turns[0].Activity[0].Kind != "workflow" ||
		view.Turns[0].Activity[0].Name != extensionID+"/summarize" ||
		view.Turns[0].Activity[0].Text != "workflow:hello" {
		t.Fatalf("workflow chat projection=%+v err=%v", view, err)
	}
	raw, err := tasks.ChatRaw(t.Context(), chat, "activity", attempts[0].ID)
	if err != nil || raw != "workflow:hello" {
		t.Fatalf("workflow raw activity=%q err=%v", raw, err)
	}
	duplicate, err := tasks.RunWorkflow(t.Context(), chat, "workflow-request", extensionID, "summarize",
		map[string]any{"text": "hello"})
	if err != nil || !duplicate.Duplicate || duplicate.Answer != "workflow:hello" {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	messages, err = tasks.Messages(t.Context(), chat)
	if err != nil || len(messages) != 2 {
		t.Fatalf("duplicate changed history=%+v err=%v", messages, err)
	}
	attempts, err = service.Store.ListAttempts(t.Context(), first.Run.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("duplicate changed attempts=%+v err=%v", attempts, err)
	}
}

func TestTaskServiceWorkflowCancellationRetainsUnknown(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	extensionID := "com.example.host"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.host",
		"version":"1.0.0",
		"name":"Workflow extension",
		"description":"Runs a bounded workflow",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-host-helper"]},
		"workflows":[{"name":"wait","description":"Wait",
			"input_schema":{"type":"object","additionalProperties":false}}]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	result, err := tasks.RunWorkflow(ctx, chat, "workflow-cancel", extensionID, "wait", map[string]any{})
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
		t.Fatalf("cancel result=%+v err=%v", result, err)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "unknown" {
		t.Fatalf("cancel attempts=%+v err=%v", attempts, err)
	}
	if _, err := tasks.Continue(t.Context(), result.Run.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown workflow became replayable: %v", err)
	}
}

func TestWorkflowUnknownSettlementCompletesAfterNestedRunUnknown(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), chat, "workflow-nested-unknown",
		"workflow prompt", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	attempt, err := service.Store.startWorkflow(t.Context(), run, "com.example.host", "nested",
		map[string]any{"value": "x"}, service.limits().MaxSteps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.db.ExecContext(t.Context(),
		`UPDATE runs SET status='unknown', stop_reason='nested host outcome unknown' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.finishWorkflowUnknown(t.Context(), run, attempt,
		"workflow outcome unknown; inspect saved attempt", ""); err != nil {
		t.Fatal(err)
	}
	savedAttempt, err := service.Store.GetAttempt(t.Context(), attempt.ID)
	if err != nil || savedAttempt.Status != "unknown" {
		t.Fatalf("attempt=%+v err=%v", savedAttempt, err)
	}
	savedRun, err := service.Store.GetRun(t.Context(), run.ID)
	if err != nil || savedRun.Status != "unknown" {
		t.Fatalf("run=%+v err=%v", savedRun, err)
	}
}

func TestTaskLocalCompactionKeepsGroupsSourcesAndOriginalRecords(t *testing.T) {
	modelCalls := 0
	summarized := false
	service, _, workspace := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		modelCalls++
		expectedResults := map[string]bool{}
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "Task-local recap") {
				summarized = true
				if !strings.HasPrefix(request.Messages[0].Content, executionInvariant) {
					t.Fatal("compaction dropped execution constraints")
				}
				if !strings.Contains(message.Content, "message_id") || !strings.Contains(message.Content, "sha256") {
					t.Fatal("recap lost provenance")
				}
			}
			for _, call := range message.ToolCalls {
				expectedResults[call.ID] = true
			}
			if message.Role == "tool" {
				if !expectedResults[message.ToolCallID] {
					t.Fatal("compaction split tool group")
				}
				delete(expectedResults, message.ToolCallID)
			}
		}
		if len(expectedResults) != 0 || request.Messages[1].Content != "long task input" {
			t.Fatal("compaction lost input or tool results")
		}
		if modelCalls <= 8 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall(strings.Repeat("x", modelCalls), "file_read", map[string]string{"path": "note"})}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}))
	service.Config.TaskSummaryBytes = 2048
	service.Config.MaxMessages = 7
	service.Config.MaxSteps = 30
	_ = os.WriteFile(filepath.Join(workspace, "note"), []byte("task data"), 0o600)
	result, err := service.Run(t.Context(), conversation(t, service), "long", "long task input")
	if err != nil || result.Run.Status != "completed" || !summarized {
		t.Fatalf("result=%+v summarized=%t err=%v", result, summarized, err)
	}
	history, err := service.Store.ListMessages(t.Context(), result.Run.ConversationID)
	if err != nil || len(history) != 18 {
		t.Fatalf("history deleted: count=%d err=%v", len(history), err)
	}
	for _, message := range history {
		if strings.Contains(message.Content, "Task-local recap") {
			t.Fatal("temporary recap was written as hidden memory")
		}
	}
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "Task-local recap") {
				t.Fatal("recap leaked across tasks")
			}
		}
		return ModelResponse{Content: "new task"}, nil
	})
	if _, err := service.Run(t.Context(), result.Run.ConversationID, "next", "new input"); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateIsExplicitBoundedOrdinaryFile(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	_ = os.WriteFile(filepath.Join(workspace, "template.md"), []byte("Inspect only selected files."), 0o600)
	prompt, err := tasks.Template(t.Context(), "template.md", "my task")
	if err != nil || !strings.Contains(prompt, "SHA-256:") || !strings.Contains(prompt, "my task") {
		t.Fatalf("prompt=%s err=%v", prompt, err)
	}
	_ = os.WriteFile(filepath.Join(workspace, "too-big.md"), []byte(strings.Repeat("x", 32769)), 0o600)
	if _, err := tasks.Template(t.Context(), "too-big.md", "task"); err == nil {
		t.Fatal("silently truncated template")
	}
}

func TestConfirmationCannotAuthorizeAnotherRunOrDriftedFile(t *testing.T) {
	modelCalls := 0
	service, _, workspace := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{ToolCalls: []ToolCall{
			makeCall("reused-provider-id", "file_create", map[string]string{"path": "new.txt", "content": "approved content"}),
		}}, nil
	}))
	tasks := NewTaskService(service)
	first, err := tasks.Submit(t.Context(), "", "first", "create")
	if err != nil {
		t.Fatal(err)
	}
	firstDecision, err := tasks.Preview(t.Context(), first.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tasks.Submit(t.Context(), "", "second", "create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Confirm(t.Context(), second.Run.ID, firstDecision, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatalf("another run borrowed confirmation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !os.IsNotExist(err) || modelCalls != 2 {
		t.Fatal("invalid decision dispatched work")
	}
	secondDecision, err := tasks.Preview(t.Context(), second.Run.ID)
	if err != nil || secondDecision.Digest == firstDecision.Digest {
		t.Fatalf("decision identity not run-bound: %+v err=%v", secondDecision, err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("external content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Confirm(t.Context(), second.Run.ID, secondDecision, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatalf("drifted preview continued the model: %v", err)
	}
	run, attempts, _, err := tasks.Inspect(t.Context(), second.Run.ID)
	if err != nil || run.Status != "waiting_confirmation" || attempts[1].Status != "pending" || modelCalls != 2 {
		t.Fatalf("stale decision consumed saved operation: run=%+v attempts=%+v err=%v", run, attempts, err)
	}
}

type consequentialReferenceTool struct {
	calls *int
}

func (tool consequentialReferenceTool) Definitions() []ToolDefinition { return nil }
func (tool consequentialReferenceTool) Prepare(context.Context, ToolCall) (PreparedTool, error) {
	return PreparedTool{Effect: EffectModify, Execute: func(context.Context) (ToolResult, error) {
		*tool.calls++
		return ToolResult{Content: `{"path":"note","content":"not read-only"}`}, nil
	}}, nil
}

func TestExplicitInputsDoNotBypassAdapterApproval(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	calls := 0
	service.Tools = consequentialReferenceTool{calls: &calls}
	tasks := NewTaskService(service)
	if _, err := tasks.Template(t.Context(), "note", "input"); err == nil {
		t.Fatal("template accepted a consequential adapter")
	}
	if _, err := tasks.ExpandReferences(t.Context(), "input", []string{"note"}); err == nil {
		t.Fatal("reference accepted a consequential adapter")
	}
	if calls != 0 {
		t.Fatal("explicit input executed a consequential adapter without approval")
	}
}

func TestTaskServiceReopenPreservesConfirmationAndUnknown(t *testing.T) {
	ctx := t.Context()
	workspace, data := t.TempDir(), filepath.Join(t.TempDir(), "data")
	if err := InitStore(ctx, data); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(data, testEchoConfig(workspace)); err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	model := modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("saved", "file_create",
				map[string]string{"path": "saved.txt", "content": "saved operation"})}}, nil
		}
		if modelCalls == 2 {
			return ModelResponse{Content: "saved operation complete"}, nil
		}
		return ModelResponse{}, &CallError{Message: "dispatched without response"}
	})
	tasks, closeAll, err := OpenTasks(ctx, data, Adapters{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := tasks.Submit(ctx, "", "saved", "create file")
	if err != nil || waiting.Run.Status != "waiting_confirmation" {
		closeAll()
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	decision, err := tasks.Preview(ctx, waiting.Run.ID)
	closeAll()
	if err != nil {
		t.Fatal(err)
	}
	tasks, closeAll, err = OpenTasks(ctx, data, Adapters{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := tasks.Confirm(ctx, waiting.Run.ID, decision, true)
	if err != nil || finished.Run.Status != "completed" || len(finished.Evidence.Files) != 1 {
		closeAll()
		t.Fatalf("reopened confirmation=%+v err=%v", finished, err)
	}
	unknown, err := tasks.Submit(ctx, waiting.Run.ConversationID, "unknown", "uncertain request")
	closeAll()
	if !errors.Is(err, ErrUnknownResult) || unknown.Run.Status != "unknown" {
		t.Fatalf("unknown=%+v err=%v", unknown, err)
	}
	tasks, closeAll, err = OpenTasks(ctx, data, Adapters{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	if _, err := tasks.Continue(ctx, unknown.Run.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("reopened unknown continued: %v", err)
	}
	if _, err := tasks.Preview(ctx, unknown.Run.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown offered confirmation: %v", err)
	}
	duplicate, err := tasks.Submit(ctx, unknown.Run.ConversationID, "unknown", "uncertain request")
	if err != nil || !duplicate.Duplicate || modelCalls != 3 ||
		len(duplicate.Evidence.Uncertainty) == 0 {
		t.Fatalf("replayed unknown: %+v calls=%d err=%v", duplicate, modelCalls, err)
	}
}

func TestDefaultRuntimeDoesNotAdvertiseOrStartDisabledCapabilities(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	config := service.Config
	config.MCPServers = map[string]MCPServerConfig{"disabled": {Command: "must-not-start", EnvFrom: map[string]string{"TOKEN": "MISSING_OPTIONAL_TOKEN"}}}
	config.HTTP.AllowedOrigins = []string{"http://127.0.0.1:1"}
	if err := WriteConfig(service.Store.DataDir(), config); err != nil {
		t.Fatal(err)
	}
	data := service.Store.DataDir()
	_ = service.Store.Close()
	tasks, closeAll, err := OpenTasks(t.Context(), data, Adapters{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	for _, tool := range tasks.core.Tools.Definitions() {
		if tool.Name == "http_get" || tool.Name == "command_run" || strings.HasPrefix(tool.Name, "mcp_") {
			t.Fatal("disabled optional tool advertised")
		}
	}
	result, err := tasks.Submit(t.Context(), "", "", "no optional process or network")
	if err != nil || result.Run.Status != "completed" || result.Answer != "Echo: no optional process or network" {
		t.Fatalf("default runtime=%+v err=%v", result, err)
	}
}
