package chatui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChatUIMCPFixtureProcess(t *testing.T) {
	if os.Getenv("CHATUI_MCP_FIXTURE") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "chatui-fixture", Version: "1"},
		&mcp.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
	server.AddTool(&mcp.Tool{Name: "echo.tool", Description: "Chat UI MCP fixture", InputSchema: schema},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// fixtureEnv receives only these variables. A -race fixture otherwise sleeps
// one second at exit (GORACE atexit_sleep_ms), longer than a graceful close.
func fixtureEnv() map[string]string {
	return map[string]string{"CHATUI_MCP_FIXTURE": "1", "FIXTURE_SECRET": "must-not-be-returned", "GORACE": "atexit_sleep_ms=0"}
}

func mcpUI(t *testing.T) *testUI {
	t.Helper()
	base := t.TempDir()
	work, data := filepath.Join(base, "workspace"), filepath.Join(base, "data")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := harness.DefaultConfig(work)
	config.Models = map[string]harness.ModelConfig{"default": {Provider: "echo"}}
	config.DefaultModel = "default"
	config.MCPServers = map[string]harness.MCPServerConfig{
		"fixture": {
			Enabled: true, Command: executable, Args: []string{"-test.run=^TestChatUIMCPFixtureProcess$"}, Cwd: work,
			Env: fixtureEnv(),
		},
	}
	return setupUIWithConfig(t, harness.EchoModel{}, data, work, config)
}

func TestMCPAPIManagesConfigurationWithoutReturningEnvironmentValues(t *testing.T) {
	ui := mcpUI(t)

	code, raw := ui.request(t, http.MethodGet, "/api/mcp", nil)
	if code != http.StatusOK {
		t.Fatalf("list MCP %d %s", code, raw)
	}
	if !strings.Contains(string(raw), `"id":"fixture"`) || !strings.Contains(string(raw), `"state":"running"`) ||
		!strings.Contains(string(raw), `"tool_count":1`) {
		t.Fatalf("unexpected MCP status: %s", raw)
	}
	if strings.Contains(string(raw), "must-not-be-returned") {
		t.Fatal("MCP environment value leaked from list response")
	}

	code, raw = ui.request(t, http.MethodPost, "/api/mcp/fixture/test", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"connected":true`) ||
		!strings.Contains(string(raw), `"tool_count":1`) {
		t.Fatalf("test MCP %d %s", code, raw)
	}

	code, raw = ui.request(t, http.MethodPost, "/api/mcp/fixture/update", harness.MCPServerConfig{
		Enabled: true, Command: os.Args[0], Args: []string{"-test.run=^TestChatUIMCPFixtureProcess$"}, Cwd: ui.work,
		Env:         fixtureEnv(),
		HiddenTools: []string{"echo.tool"},
	})
	if code != http.StatusOK {
		t.Fatalf("update MCP %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/mcp/restart", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("restart MCP %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/mcp", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"tool_count":0`) {
		t.Fatalf("hidden MCP tool was not applied: %d %s", code, raw)
	}

	code, raw = ui.request(t, http.MethodGet, "/api/mcp/fixture/logs", nil)
	if code != http.StatusOK || strings.Contains(string(raw), "must-not-be-returned") {
		t.Fatalf("MCP logs %d %s", code, raw)
	}
}

func TestMCPAPIImportRejectsRemoteEntriesAndDisablesFailedServers(t *testing.T) {
	ui := mcpUI(t)
	code, raw := ui.request(t, http.MethodPost, "/api/mcp/import", map[string]any{
		"mcpServers": map[string]any{
			"remote": map[string]any{"type": "sse", "url": "https://example.invalid/mcp"},
			"broken": map[string]any{"command": filepath.Join(ui.work, "missing-mcp.exe")},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("import MCP %d %s", code, raw)
	}
	if !strings.Contains(string(raw), `"id":"remote"`) || !strings.Contains(string(raw), `"id":"broken"`) {
		t.Fatalf("import result missing entries: %s", raw)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/mcp", nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"id":"broken"`) {
		t.Fatalf("imported servers not listed: %d %s", code, raw)
	}
	if strings.Contains(string(raw), `"id":"broken","enabled":true`) {
		t.Fatal("failed imported MCP server was enabled")
	}
}

func TestMCPAPIImportDefaultsReadOnlyTrustOff(t *testing.T) {
	ui := mcpUI(t)
	missing := filepath.Join(ui.work, "missing-mcp.exe")
	code, raw := ui.request(t, http.MethodPost, "/api/mcp/import", map[string]any{
		"mcpServers": map[string]any{
			"plain":   map[string]any{"command": missing},
			"opt-in":  map[string]any{"command": missing, "trustReadOnlyHint": true},
			"opt-out": map[string]any{"command": missing, "trustReadOnlyHint": false},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("import MCP %d %s", code, raw)
	}
	want := map[string]bool{"plain": false, "opt-in": true, "opt-out": false}
	config := ui.tasks.Config()
	for id, trusted := range want {
		server, ok := config.MCPServers[id]
		if !ok || server.TrustReadOnlyHint != trusted {
			t.Fatalf("stored %s trustReadOnlyHint=%v present=%v, want %v", id, server.TrustReadOnlyHint, ok, trusted)
		}
	}
	code, raw = ui.request(t, http.MethodGet, "/api/mcp", nil)
	var listed struct {
		Servers []mcpServerView `json:"servers"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &listed) != nil {
		t.Fatalf("list MCP %d %s", code, raw)
	}
	for _, server := range listed.Servers {
		if trusted, ok := want[server.ID]; ok && server.TrustReadOnlyHint != trusted {
			t.Fatalf("listed %s trust_read_only_hint=%v, want %v", server.ID, server.TrustReadOnlyHint, trusted)
		}
	}
}
