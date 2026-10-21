package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPFixtureProcess(t *testing.T) {
	if os.Getenv("HARNESS_MCP_FIXTURE") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "harness-fixture", Version: "1"},
		&mcp.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, name := range []string{"echo.tool", "error.tool", "slow.tool", "unsafe.schema"} {
		schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
		if name == "unsafe.schema" {
			schema = json.RawMessage(`{"type":"object","unknownConstraint":true}`)
		}
		server.AddTool(&mcp.Tool{Name: name, Description: "Synthetic harness acceptance tool", InputSchema: schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if err := appendMarker(os.Getenv("MARKER"), req.Params.Name); err != nil {
					return nil, err
				}
				if req.Params.Name == "slow.tool" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Mapped secret: " + os.Getenv("TOKEN")}},
					StructuredContent: map[string]any{
						"parent_env_hidden": os.Getenv("UNRELATED_PARENT_SECRET") == "",
						"arguments":         string(req.Params.Arguments),
					},
					IsError: req.Params.Name == "error.tool",
				}, nil
			})
	}
	err := server.Run(context.Background(), &mcp.StdioTransport{})
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func mcpHarness(t *testing.T) (*Service, *RuntimeTools, string) {
	t.Helper()
	service, files, workspace := testHarness(t, EchoModel{})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "mcp-effects.txt")
	t.Setenv("MCP_EXPLICIT_SECRET", "fixture-mcp-secret-should-be-redacted")
	t.Setenv("UNRELATED_PARENT_SECRET", "fixture-parent-must-not-be-inherited")
	config := MCPServerConfig{
		Enabled: true, Command: executable, Args: []string{"-test.run=^TestMCPFixtureProcess$"}, Cwd: workspace,
		Env:     map[string]string{"HARNESS_MCP_FIXTURE": "1", "MARKER": marker},
		EnvFrom: map[string]string{"TOKEN": "MCP_EXPLICIT_SECRET"},
	}
	service.Config.MCPServers = map[string]MCPServerConfig{
		"trusted-fixture": config,
		"disabled":        {Command: filepath.Join(workspace, "does-not-exist.exe"), EnvFrom: map[string]string{"TOKEN": "ABSENT_DISABLED_KEY"}},
	}
	tools := NewRuntimeTools(files)
	t.Cleanup(func() { _ = tools.Close() })
	if err := tools.Connect(context.Background(), service.Config, ""); err != nil {
		t.Fatal(err)
	}
	service.Tools = tools
	return service, tools, marker
}

func remoteAlias(t *testing.T, tools *RuntimeTools, name string) string {
	t.Helper()
	for alias, tool := range tools.remote {
		if tool.name == name {
			return alias
		}
	}
	t.Fatalf("remote tool missing: %s", name)
	return ""
}

func mcpMultiHarness(t *testing.T) (*Service, *RuntimeTools, string) {
	t.Helper()
	service, files, workspace := testHarness(t, EchoModel{})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "mcp-multi-effects.txt")
	t.Setenv("MCP_EXPLICIT_SECRET", "fixture-mcp-secret-should-be-redacted")
	t.Setenv("UNRELATED_PARENT_SECRET", "fixture-parent-must-not-be-inherited")
	server := func(name string) MCPServerConfig {
		return MCPServerConfig{
			Enabled: true, Command: executable, Args: []string{"-test.run=^TestMCPFixtureProcess$"}, Cwd: workspace,
			Env:     map[string]string{"HARNESS_MCP_FIXTURE": "1", "MARKER": marker, "SERVER_NAME": name},
			EnvFrom: map[string]string{"TOKEN": "MCP_EXPLICIT_SECRET"},
		}
	}
	service.Config.MCPServers = map[string]MCPServerConfig{
		"first-fixture":  server("first-fixture"),
		"second-fixture": server("second-fixture"),
	}
	tools := NewRuntimeTools(files)
	t.Cleanup(func() { _ = tools.Close() })
	if err := tools.Connect(context.Background(), service.Config, ""); err != nil {
		t.Fatal(err)
	}
	service.Tools = tools
	return service, tools, marker
}

func remoteAliasForServer(t *testing.T, tools *RuntimeTools, server, name string) string {
	t.Helper()
	for alias, tool := range tools.remote {
		if tool.server == server && tool.name == name {
			return alias
		}
	}
	t.Fatalf("remote tool missing: %s/%s", server, name)
	return ""
}

func TestMCPCapabilitySelectionHidesAndRejectsUnselectedTools(t *testing.T) {
	_, tools, _ := mcpHarness(t)
	alias := remoteAlias(t, tools, "echo.tool")

	unselected := tools.ForCapabilities(CapabilitySelection{})
	for _, definition := range unselected.Definitions() {
		if definition.Name == alias {
			t.Fatalf("MCP tool exposed when no MCP server was selected: %q", alias)
		}
	}
	if _, err := unselected.Prepare(context.Background(), makeCall("forged", alias, map[string]string{"text": "blocked"})); err == nil {
		t.Fatal("forged MCP call accepted when no MCP server was selected")
	}
}

func TestMCPCapabilitySelectionLimitsToolsToSelectedServer(t *testing.T) {
	_, tools, _ := mcpMultiHarness(t)
	first := remoteAliasForServer(t, tools, "first-fixture", "echo.tool")
	second := remoteAliasForServer(t, tools, "second-fixture", "echo.tool")

	selected := tools.ForCapabilities(CapabilitySelection{MCPServers: []string{"first-fixture"}})
	seenFirst := false
	for _, definition := range selected.Definitions() {
		switch definition.Name {
		case first:
			seenFirst = true
		case second:
			t.Fatalf("unselected MCP server tool exposed: %q", second)
		}
	}
	if !seenFirst {
		t.Fatalf("selected MCP server tool was hidden: %q", first)
	}
	if _, err := selected.Prepare(context.Background(), makeCall("selected", first, map[string]string{"text": "allowed"})); err != nil {
		t.Fatalf("selected MCP server call rejected: %v", err)
	}
	if _, err := selected.Prepare(context.Background(), makeCall("forged", second, map[string]string{"text": "blocked"})); err == nil {
		t.Fatal("forged call to unselected MCP server accepted")
	}
}

func TestMCPToolUsesSharedApprovalStoreAndEnvironmentBoundary(t *testing.T) {
	service, tools, marker := mcpHarness(t)
	if len(tools.remote) != 3 || len(tools.Rejected) != 1 {
		t.Fatalf("per-tool schema rejection failed: remote=%d rejected=%v", len(tools.remote), tools.Rejected)
	}
	alias := remoteAlias(t, tools, "echo.tool")
	if strings.Contains(alias, ".") || len(alias) > 64 {
		t.Fatalf("invalid model-facing alias: %q", alias)
	}
	if _, err := tools.Prepare(context.Background(), makeCall("invalid", alias, map[string]int{"text": 5})); err == nil {
		t.Fatal("invalid schema arguments prepared")
	}
	modelCalls := 0
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("echo", alias, map[string]string{"text": "hello"})}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "echo" || !strings.Contains(last.Content, `"parent_env_hidden":true`) ||
			!strings.Contains(last.Content, "[REDACTED]") {
			t.Fatalf("MCP result missing boundary facts: %+v", last)
		}
		return ModelResponse{Content: "MCP completed."}, nil
	})
	ctx := context.Background()
	result, err := service.Run(ctx, conversation(t, service), "mcp", "call echo")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("MCP tool dispatched without confirmation")
	}
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	result, err = service.Continue(ctx, result.Run.ID)
	if err != nil || result.Run.Status != "completed" || modelCalls != 2 {
		t.Fatalf("result=%+v error=%v model calls=%d", result, err, modelCalls)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "echo.tool\n" {
		t.Fatalf("MCP execution count=%q", raw)
	}
	attempts, _ := service.Store.ListAttempts(ctx, result.Run.ID)
	serialized, _ := json.Marshal(attempts)
	if strings.Contains(string(serialized), os.Getenv("MCP_EXPLICIT_SECRET")) {
		t.Fatal("MCP secret leaked into attempts")
	}
}

func TestMCPKnownErrorIsRecordedAndNotReplayed(t *testing.T) {
	service, tools, marker := mcpHarness(t)
	alias := remoteAlias(t, tools, "error.tool")
	modelCalls := 0
	service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("error", alias, map[string]string{"text": "partial effect"})}}, nil
		}
		return ModelResponse{Content: "The tool reported an error; no automatic retry."}, nil
	})
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	ctx := context.Background()
	id := conversation(t, service)
	result, err := service.Run(ctx, id, "error", "call")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	attempts, _ := service.Store.ListAttempts(ctx, result.Run.ID)
	var toolResult ToolResult
	if err := json.Unmarshal([]byte(attempts[1].ResponseJSON), &toolResult); err != nil || !toolResult.IsError || attempts[1].Status != "finished" {
		t.Fatalf("known error lost: attempts=%+v error=%v", attempts, err)
	}
	repeated, err := service.Run(ctx, id, "error", "call")
	raw, _ := os.ReadFile(marker)
	if err != nil || !repeated.Duplicate || string(raw) != "error.tool\n" {
		t.Fatalf("known error replayed: result=%+v error=%v effects=%q", repeated, err, raw)
	}
}

func TestMCPCancelAfterDispatchRemainsUnknown(t *testing.T) {
	service, tools, marker := mcpHarness(t)
	alias := remoteAlias(t, tools, "slow.tool")
	service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{makeCall("slow", alias, map[string]string{"text": "wait"})}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-deadline.C:
				cancel()
				return
			case <-tick.C:
				if _, err := os.Stat(marker); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	start := time.Now()
	result, err := service.Run(ctx, conversation(t, service), "cancel", "wait")
	<-done
	if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" || time.Since(start) > 6*time.Second {
		t.Fatalf("cancellation=%+v error=%v duration=%s", result, err, time.Since(start))
	}
	if _, err := service.Continue(context.Background(), result.Run.ID); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown MCP call continued: %v", err)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "slow.tool\n" {
		t.Fatalf("cancelled tool replayed: %q", raw)
	}
}

func TestMCPExternalReferencesAndUnsupportedSchemaAreRejectedOffline(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","$ref":"https://127.0.0.1:1/schema"}`,
		`{"type":"object","properties":{"url":{"type":"string","format":"uri"}}}`,
		`{"type":"object","unknownConstraint":true}`,
	} {
		if _, err := resolveToolSchema([]byte(schema)); err == nil {
			t.Fatalf("unsupported schema accepted: %s", schema)
		}
	}
}

func TestMCPReadOnlyHintTrustControlsPermissionEffect(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"additionalProperties":false}`)
	readOnly := &mcp.Tool{
		Name:        "inspect",
		Description: "Read-only fixture",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
	mutating := &mcp.Tool{
		Name:        "inspect",
		Description: "Read-only fixture",
		InputSchema: schema,
	}
	for _, test := range []struct {
		name   string
		config MCPServerConfig
		tool   *mcp.Tool
		effect ToolEffect
	}{
		{name: "trusted", config: MCPServerConfig{TrustReadOnlyHint: true}, tool: readOnly, effect: EffectRead},
		{name: "untrusted", config: MCPServerConfig{TrustReadOnlyHint: false}, tool: readOnly, effect: EffectModify},
		{name: "missing annotation", config: MCPServerConfig{TrustReadOnlyHint: true}, tool: mutating, effect: EffectModify},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, _ := testHarness(t, EchoModel{})
			tools := NewRuntimeTools(service.Tools.(*FileTools))
			definition, _, err := tools.describe("fixture", test.config, test.tool)
			if err != nil {
				t.Fatal(err)
			}
			if definition.Effect != test.effect {
				t.Fatalf("effect=%s want=%s", definition.Effect, test.effect)
			}
			for _, mode := range []PermissionMode{PermissionAsk, PermissionWorkspace, PermissionFull} {
				service.permission = &PermissionSelection{Mode: mode, Source: "turn"}
				wantApproval := definition.Effect != EffectRead && mode != PermissionFull
				if got := service.needsApproval(PreparedTool{Effect: definition.Effect}); got != wantApproval {
					t.Fatalf("mode=%s needsApproval=%t want=%t", mode, got, wantApproval)
				}
			}
		})
	}
}

func appendMarker(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value + "\n")
	return errors.Join(writeErr, file.Sync(), file.Close())
}
