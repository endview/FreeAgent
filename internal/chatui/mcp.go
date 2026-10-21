package chatui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type mcpCreateRequest struct {
	ID     string                  `json:"id"`
	Config harness.MCPServerConfig `json:"config"`
}

type mcpServerView struct {
	ID                string                  `json:"id"`
	Enabled           bool                    `json:"enabled"`
	State             string                  `json:"state"`
	Connected         bool                    `json:"connected"`
	Command           string                  `json:"command"`
	Args              []string                `json:"args,omitempty"`
	Cwd               string                  `json:"cwd,omitempty"`
	EnvNames          []string                `json:"env_names,omitempty"`
	EnvFrom           map[string]string       `json:"env_from,omitempty"`
	HiddenTools       []string                `json:"hidden_tools,omitempty"`
	TrustReadOnlyHint bool                    `json:"trust_read_only_hint"`
	ToolCount         int                     `json:"tool_count"`
	Tools             []harness.MCPToolStatus `json:"tools,omitempty"`
	Rejected          []string                `json:"rejected,omitempty"`
}

func (server *Server) mcpAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if path == "/api/mcp" {
		switch request.Method {
		case http.MethodGet:
			server.listMCP(writer)
		case http.MethodPost:
			server.addMCP(writer, request)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
	}
	if path == "/api/mcp/import" && request.Method == http.MethodPost {
		server.importMCP(writer, request)
		return true
	}
	if path == "/api/mcp/restart" && request.Method == http.MethodPost {
		server.restartMCP(writer, request)
		return true
	}
	if !strings.HasPrefix(path, "/api/mcp/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/mcp/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return true
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil || name == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "MCP 服务器名称无效。")
		return true
	}
	switch parts[1] {
	case "update":
		if request.Method == http.MethodPost {
			server.updateMCP(writer, request, name)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	case "delete":
		if request.Method == http.MethodPost {
			server.deleteMCP(writer, request, name)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	case "test":
		if request.Method == http.MethodPost {
			server.testMCP(writer, request, name)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	case "restart":
		if request.Method == http.MethodPost {
			server.restartMCP(writer, request)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	case "logs":
		if request.Method == http.MethodGet {
			server.mcpLogs(writer, name)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		http.NotFound(writer, request)
	}
	return true
}

func (server *Server) listMCP(writer http.ResponseWriter) {
	config := server.tasks.Config()
	statuses := server.tasks.MCPServers()
	views := make([]mcpServerView, 0, len(config.MCPServers))
	for _, status := range statuses {
		mcpConfig := config.MCPServers[status.ID]
		views = append(views, makeMCPView(status, mcpConfig))
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"servers": views})
}

func makeMCPView(status harness.MCPServerStatus, config harness.MCPServerConfig) mcpServerView {
	envNames := make([]string, 0, len(config.Env))
	for name := range config.Env {
		envNames = append(envNames, name)
	}
	sort.Strings(envNames)
	state := "stopped"
	if !config.Enabled {
		state = "disabled"
	} else if status.Connected {
		state = "running"
	}
	return mcpServerView{
		ID: status.ID, Enabled: config.Enabled, State: state, Connected: status.Connected,
		Command: config.Command, Args: append([]string(nil), config.Args...), Cwd: config.Cwd,
		EnvNames: envNames, EnvFrom: cloneStringMap(config.EnvFrom),
		HiddenTools:       append([]string(nil), config.HiddenTools...),
		TrustReadOnlyHint: config.TrustReadOnlyHint, ToolCount: status.ToolCount,
		Tools:    append([]harness.MCPToolStatus(nil), status.Tools...),
		Rejected: append([]string(nil), status.Rejected...),
	}
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func (server *Server) addMCP(writer http.ResponseWriter, request *http.Request) {
	var body mcpCreateRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "缺少 MCP 服务器名称。")
		return
	}
	if _, exists := server.tasks.Config().MCPServers[body.ID]; exists {
		server.writeError(writer, http.StatusConflict, "mcp_exists", "MCP 服务器已经存在。")
		return
	}
	if _, err := server.applyDirectMCPControl(request.Context(), harness.MCPControlRequest{ID: body.ID, Config: body.Config}); err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, server.mcpView(body.ID))
}

func (server *Server) updateMCP(writer http.ResponseWriter, request *http.Request, name string) {
	var config harness.MCPServerConfig
	if !server.readBody(writer, request, &config) {
		return
	}
	if _, exists := server.tasks.Config().MCPServers[name]; !exists {
		server.writeError(writer, http.StatusNotFound, "missing", "MCP 服务器不存在。")
		return
	}
	current := server.tasks.Config().MCPServers[name]
	if config.Env == nil {
		config.Env = current.Env
	}
	if config.EnvFrom == nil {
		config.EnvFrom = current.EnvFrom
	}
	if _, err := server.applyDirectMCPControl(request.Context(), harness.MCPControlRequest{ID: name, Config: config}); err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, server.mcpView(name))
}

func (server *Server) deleteMCP(writer http.ResponseWriter, request *http.Request, name string) {
	if _, exists := server.tasks.Config().MCPServers[name]; !exists {
		server.writeError(writer, http.StatusNotFound, "missing", "MCP 服务器不存在。")
		return
	}
	if _, err := server.applyDirectMCPControl(request.Context(), harness.MCPControlRequest{ID: name, Remove: true}); err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

func (server *Server) testMCP(writer http.ResponseWriter, request *http.Request, name string) {
	var override *harness.MCPServerConfig
	if request.ContentLength != 0 {
		var config harness.MCPServerConfig
		if !server.readBody(writer, request, &config) {
			return
		}
		current, exists := server.tasks.Config().MCPServers[name]
		if exists {
			if config.Env == nil {
				config.Env = current.Env
			}
			if config.EnvFrom == nil {
				config.EnvFrom = current.EnvFrom
			}
		}
		override = &config
	}
	status, err := server.tasks.TestMCP(request.Context(), name, override)
	if err != nil {
		server.mcpFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, status)
}

func (server *Server) restartMCP(writer http.ResponseWriter, request *http.Request) {
	if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlRestartMCP, map[string]any{}); err != nil {
		server.configFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"servers": server.tasks.MCPServers()})
}

func (server *Server) mcpLogs(writer http.ResponseWriter, name string) {
	raw, err := server.tasks.MCPLog(name)
	if err != nil {
		server.mcpFailure(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(raw)
}

func (server *Server) mcpView(name string) mcpServerView {
	config := server.tasks.Config()
	for _, status := range server.tasks.MCPServers() {
		if status.ID == name {
			return makeMCPView(status, config.MCPServers[name])
		}
	}
	return mcpServerView{ID: name, Enabled: config.MCPServers[name].Enabled,
		Command: config.MCPServers[name].Command, Args: config.MCPServers[name].Args,
		Cwd: config.MCPServers[name].Cwd, EnvFrom: cloneStringMap(config.MCPServers[name].EnvFrom),
		HiddenTools:       config.MCPServers[name].HiddenTools,
		TrustReadOnlyHint: config.MCPServers[name].TrustReadOnlyHint}
}

func (server *Server) mcpFailure(writer http.ResponseWriter, err error) {
	code, text := userError(err)
	if errors.Is(err, harness.ErrActiveRun) {
		code, text = "busy", "存在未完成任务，请先继续、停止或明确结束。"
	}
	server.writeError(writer, http.StatusConflict, code, text)
}

func (server *Server) importMCP(writer http.ResponseWriter, request *http.Request) {
	var raw json.RawMessage
	if !server.readBody(writer, request, &raw) {
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 1 {
		server.writeError(writer, http.StatusBadRequest, "input", "导入格式必须是 mcpServers 对象。")
		return
	}
	serversRaw, ok := envelope["mcpServers"]
	if !ok {
		server.writeError(writer, http.StatusBadRequest, "input", "导入格式必须是 mcpServers 对象。")
		return
	}
	var entries map[string]json.RawMessage
	if json.Unmarshal(serversRaw, &entries) != nil || len(entries) == 0 || len(entries) > 16 {
		server.writeError(writer, http.StatusBadRequest, "input", "MCP 服务器列表无效。")
		return
	}
	type importResult struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
		Tested  bool   `json:"tested"`
		Error   string `json:"error,omitempty"`
	}
	results := make([]importResult, 0, len(entries))
	updated := server.tasks.Config()
	if updated.MCPServers == nil {
		updated.MCPServers = map[string]harness.MCPServerConfig{}
	}
	imported := make(map[string]harness.MCPServerConfig, len(entries))
	for name, item := range entries {
		var fields map[string]json.RawMessage
		var config harness.MCPServerConfig
		if json.Unmarshal(item, &fields) != nil || json.Unmarshal(item, &config) != nil ||
			fields["url"] != nil || fields["type"] != nil {
			results = append(results, importResult{ID: name, Error: "远程 MCP 暂不支持"})
			continue
		}
		// Pasted third-party configs rarely mention read-only trust; imported
		// servers start untrusted unless the entry opts in explicitly.
		if _, explicit := fields["trustReadOnlyHint"]; !explicit {
			config.TrustReadOnlyHint = false
		}
		config.Enabled = true
		status, err := server.tasks.TestMCP(request.Context(), name, &config)
		result := importResult{ID: name, Tested: true}
		if err == nil {
			config.Enabled = true
			result.Enabled = status.Connected
		} else {
			config.Enabled = false
			result.Error = userErrorText(err)
		}
		updated.MCPServers[name] = config
		imported[name] = config
		results = append(results, result)
	}
	if err := updated.Validate(); err != nil {
		server.configFailure(writer, err)
		return
	}
	for name, config := range imported {
		if _, err := server.applyDirectMCPControl(request.Context(), harness.MCPControlRequest{ID: name, Config: config}); err != nil {
			server.configFailure(writer, err)
			return
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	server.writeJSON(writer, http.StatusOK, map[string]any{"results": results, "servers": server.tasks.MCPServers()})
}

func userErrorText(err error) string {
	_, text := userError(err)
	return text
}
