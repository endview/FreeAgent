package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func permissionCreateModel() ModelClient {
	calls := 0
	return modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{
				makeCall("create", "file_create", map[string]string{"path": "permission.txt", "content": "permission"}),
			}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	})
}

func TestPermissionModesControlBuiltInEffects(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      PermissionMode
		confirmed bool
		waiting   bool
	}{
		{name: "ask", mode: PermissionAsk, waiting: true},
		{name: "workspace", mode: PermissionWorkspace},
		{name: "full", mode: PermissionFull, confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, workspace := testHarness(t, permissionCreateModel())
			tasks := NewTaskService(service)
			chat := conversation(t, service)
			if test.mode != PermissionAsk {
				if err := tasks.SelectPermission(t.Context(), chat, test.mode, test.confirmed); err != nil {
					t.Fatal(err)
				}
			}
			result, err := tasks.SubmitWithOptions(t.Context(), chat, test.name, "create a file", RunOptions{})
			if test.waiting {
				if err != nil || result.Run.Status != "waiting_confirmation" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Run.Status != "completed" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			raw, readErr := os.ReadFile(filepath.Join(workspace, "permission.txt"))
			if readErr != nil || string(raw) != "permission" {
				t.Fatalf("file=%q err=%v", raw, readErr)
			}
		})
	}
}

func TestFullAccessRequiresExplicitConfirmationAndAskRemainsDefault(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	selected, err := tasks.SelectedPermission(t.Context(), chat)
	if err != nil || selected.Mode != PermissionAsk || selected.Source != "default" {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	if err := tasks.SelectPermission(t.Context(), chat, PermissionFull, false); !errors.Is(err, ErrFullAccessConfirmation) {
		t.Fatalf("unconfirmed Full access error=%v", err)
	}
	selected, err = tasks.SelectedPermission(t.Context(), chat)
	if err != nil || selected.Mode != PermissionAsk {
		t.Fatalf("unconfirmed selection=%+v err=%v", selected, err)
	}
	if err := tasks.SelectPermission(t.Context(), chat, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	selected, err = tasks.SelectedPermission(t.Context(), chat)
	if err != nil || selected.Mode != PermissionFull || selected.Source != "conversation" {
		t.Fatalf("confirmed selection=%+v err=%v", selected, err)
	}
}

func TestPermissionIsFrozenInRunSnapshot(t *testing.T) {
	service, _, workspace := testHarness(t, permissionCreateModel())
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	if err := tasks.SelectPermission(t.Context(), chat, PermissionWorkspace, false); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "frozen", "create a file")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := tasks.SelectPermission(t.Context(), chat, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "permission.txt"))
	if err != nil || string(raw) != "permission" {
		t.Fatalf("file=%q err=%v", raw, err)
	}
	run, _, _, err := tasks.Inspect(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPermission(run.ConfigJSON, PermissionWorkspace) {
		t.Fatalf("run snapshot did not retain workspace permission: %s", run.ConfigJSON)
	}
}

func TestWorkspacePermissionOnlyAutoApprovesBuiltInWorkspaceFiles(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	workspace := PermissionSelection{Mode: PermissionWorkspace, Source: "turn"}
	service.permission = &workspace
	for _, test := range []struct {
		name          string
		workspaceFile bool
		needs         bool
	}{
		{name: "file_edit", workspaceFile: true, needs: false},
		{name: "file_create", workspaceFile: true, needs: false},
		{name: "file_delete", workspaceFile: true, needs: false},
		{name: "file_remote_like", workspaceFile: false, needs: true},
		{name: "command_run", workspaceFile: false, needs: true},
		{name: "http_get", workspaceFile: false, needs: true},
		{name: "mcp_external", workspaceFile: false, needs: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := service.needsApproval(PreparedTool{Effect: EffectModify, workspaceFile: test.workspaceFile}); got != test.needs {
				t.Fatalf("workspaceFile=%t needsApproval=%t want=%t", test.workspaceFile, got, test.needs)
			}
		})
	}
	full := PermissionSelection{Mode: PermissionFull, Source: "turn"}
	service.permission = &full
	if service.needsApproval(PreparedTool{Effect: EffectModify}) {
		t.Fatal("Full access still requested approval for an external operation")
	}
}

func TestWorkspacePermissionRequiresApprovalForWorkspaceConfig(t *testing.T) {
	service, tools, workspace := testHarness(t, EchoModel{})
	config := filepath.Join(workspace, ".freeagent", "extensions", "com.example.local")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("print('original')\n")
	for _, name := range []string{filepath.Join(config, "main.py"), filepath.Join(workspace, "plain.txt")} {
		if err := os.WriteFile(name, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hash := contentHash(original)
	workspaceMode := PermissionSelection{Mode: PermissionWorkspace, Source: "turn"}
	service.permission = &workspaceMode
	for _, test := range []struct {
		name        string
		call        ToolCall
		needs       bool
		windowsOnly bool
	}{
		{name: "edit config", needs: true, call: makeCall("edit", "file_edit", map[string]string{
			"path": ".freeagent/extensions/com.example.local/main.py", "expected_sha256": hash, "old_text": "original", "new_text": "changed"})},
		{name: "edit config other case", needs: true, windowsOnly: true, call: makeCall("edit", "file_edit", map[string]string{
			"path": `.FreeAgent\extensions\com.example.local\main.py`, "expected_sha256": hash, "old_text": "original", "new_text": "changed"})},
		{name: "create config", needs: true, call: makeCall("create", "file_create", map[string]string{
			"path": ".freeagent/extensions/com.example.local/extra.py", "content": "x"})},
		{name: "delete config", needs: true, call: makeCall("delete", "file_delete", map[string]string{
			"path": ".freeagent/extensions/com.example.local/main.py", "expected_sha256": hash})},
		{name: "batch touching config", needs: true, call: makeCall("batch", "file_batch_edit", map[string]any{"changes": []map[string]string{
			{"operation": "create", "path": "new.txt", "content": "x"},
			{"operation": "edit", "path": ".freeagent/extensions/com.example.local/main.py", "expected_sha256": hash, "old_text": "original", "new_text": "changed"},
		}})},
		{name: "plain edit", needs: false, call: makeCall("edit", "file_edit", map[string]string{
			"path": "plain.txt", "expected_sha256": hash, "old_text": "original", "new_text": "changed"})},
		{name: "plain batch", needs: false, call: makeCall("batch", "file_batch_edit", map[string]any{"changes": []map[string]string{
			{"operation": "create", "path": "new.txt", "content": "x"},
		}})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("case-insensitive backslash paths are Windows-specific")
			}
			prepared, err := tools.Prepare(t.Context(), test.call)
			if err != nil {
				t.Fatal(err)
			}
			if got := service.needsApproval(prepared); got != test.needs {
				t.Fatalf("needsApproval=%t want=%t", got, test.needs)
			}
		})
	}
}

type permissionExternalTool struct {
	marker string
}

func (tool permissionExternalTool) Definitions() []ToolDefinition {
	return []ToolDefinition{{
		Name:        "file_external",
		Description: "Synthetic external file-shaped operation.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Effect:      EffectModify,
	}}
}

func (tool permissionExternalTool) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if err := ctx.Err(); err != nil {
		return PreparedTool{}, err
	}
	if call.Name != "file_external" || string(call.Arguments) != `{}` {
		return PreparedTool{}, errors.New("unexpected external permission fixture call")
	}
	return PreparedTool{
		Effect:  EffectModify,
		Preview: "external file-shaped operation",
		Items:   []ApprovalItem{{Kind: "external", Target: "file_external"}},
		Execute: func(context.Context) (ToolResult, error) {
			if err := os.WriteFile(tool.marker, []byte("executed"), 0o600); err != nil {
				return ToolResult{}, err
			}
			return ToolResult{Content: `{"executed":true}`, Receipts: []ToolReceipt{{
				Kind: "external", Status: "completed", Detail: "fixture",
			}}}, nil
		},
		Reject: func() ToolResult {
			return ToolResult{Content: "declined", IsError: true, Receipts: []ToolReceipt{{
				Kind: "external", Status: "not_dispatched", Detail: "declined",
			}}}
		},
	}, nil
}

func permissionToolModel(call ToolCall) ModelClient {
	count := 0
	return modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		count++
		if count == 1 {
			return ModelResponse{ToolCalls: []ToolCall{call}}, nil
		}
		return ModelResponse{Content: "operation finished"}, nil
	})
}

func TestWorkspaceAccessKeepsExternalOperationsAtConfirmation(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		service, commands, workspace := commandFixture(t)
		marker := filepath.Join(workspace, "workspace-command-marker")
		commands.Config.TimeoutSecs = 2
		service.Config.Commands = commands.Config
		service.Model = permissionToolModel(helperCall(t, "effect-sleep", marker))
		tasks := NewTaskService(service)
		result, err := tasks.SubmitWithOptions(t.Context(), "", "workspace-command", "run command", RunOptions{
			Permission: PermissionWorkspace,
		})
		if err != nil || result.Run.Status != "waiting_confirmation" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("Workspace access dispatched command before confirmation")
		}
	})

	t.Run("http", func(t *testing.T) {
		service, files, _ := testHarness(t, nil)
		tools := NewRuntimeTools(files)
		service.Tools = tools
		service.Model = permissionToolModel(makeCall("http", "http_get", map[string]string{"url": "https://docs.example/"}))
		tools.HTTP = fixtureHTTPTools("https://docs.example")
		service.Config.HTTP = tools.HTTP.Config
		tasks := NewTaskService(service)
		result, err := tasks.SubmitWithOptions(t.Context(), "", "workspace-http", "get docs", RunOptions{
			Permission: PermissionWorkspace,
		})
		if err != nil || result.Run.Status != "waiting_confirmation" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		service, tools, marker := mcpHarness(t)
		alias := remoteAlias(t, tools, "echo.tool")
		service.Model = permissionToolModel(makeCall("mcp", alias, map[string]string{"text": "hello"}))
		tasks := NewTaskService(service)
		result, err := tasks.SubmitWithOptions(t.Context(), "", "workspace-mcp", "call mcp", RunOptions{
			Permission: PermissionWorkspace,
		})
		if err != nil || result.Run.Status != "waiting_confirmation" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("Workspace access dispatched MCP before confirmation")
		}
	})

	t.Run("external-file-shaped-tool", func(t *testing.T) {
		service, _, workspace := testHarness(t, nil)
		marker := filepath.Join(workspace, "external-file-marker")
		service.Tools = permissionExternalTool{marker: marker}
		service.Model = permissionToolModel(makeCall("external", "file_external", map[string]string{}))
		tasks := NewTaskService(service)
		result, err := tasks.SubmitWithOptions(t.Context(), "", "workspace-external-file", "run external", RunOptions{
			Permission: PermissionWorkspace,
		})
		if err != nil || result.Run.Status != "waiting_confirmation" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("file-shaped external tool was auto-approved")
		}
	})
}

func TestFullAccessRunsExternalOperationsWithoutPerItemApproval(t *testing.T) {
	service, _, workspace := commandFixture(t)
	marker := filepath.Join(workspace, "full-command-marker")
	service.Model = permissionToolModel(helperCall(t, "ok"))
	tasks := NewTaskService(service)
	result, err := tasks.SubmitWithOptions(t.Context(), "", "full-command", "run command", RunOptions{
		Permission:        PermissionFull,
		ConfirmFullAccess: true,
	})
	if err != nil || result.Run.Status != "completed" || len(result.Evidence.Commands) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Evidence.Commands[0].Status != "completed" || result.Evidence.Commands[0].ExitCode == nil ||
		*result.Evidence.Commands[0].ExitCode != 0 {
		t.Fatalf("command was not executed under Full access: %+v", result.Evidence.Commands)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unexpected marker from wrong command fixture")
	}
}

func containsPermission(raw string, mode PermissionMode) bool {
	return len(raw) > 0 && string(mode) != "" && (raw == string(mode) || strings.Contains(raw, `"mode":"`+string(mode)+`"`))
}
