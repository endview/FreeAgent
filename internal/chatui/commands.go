package chatui

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type commandPolicyRequest struct {
	Enabled            bool                         `json:"enabled"`
	AllowedExecutables []string                     `json:"allowed_executables"`
	TimeoutSeconds     int                          `json:"timeout_seconds"`
	MaxOutputBytes     int                          `json:"max_output_bytes"`
	InheritEnvironment bool                         `json:"inherit_environment"`
	Execution          harness.CommandExecutionMode `json:"execution"`
	Rules              []harness.CommandRule        `json:"rules"`
	GitWrite           bool                         `json:"git_write"`
	Services           *harness.ServicePolicy       `json:"services"`
	BaseGeneration     string                       `json:"base_generation"`
}

type permissionSelectionRequest struct {
	ChatID          string                 `json:"chat_id"`
	Mode            harness.PermissionMode `json:"mode"`
	Confirmed       bool                   `json:"confirmed"`
	ComputerFiles   bool                   `json:"computer_files"`
	ComputerRoots   []string               `json:"computer_roots"`
	FileScope       harness.FileScope      `json:"file_scope"`
	AdditionalRoots []string               `json:"additional_roots"`
	BlockedPaths    []string               `json:"blocked_paths"`
	CommandEnabled  *bool                  `json:"command_enabled,omitempty"`
}

type permissionFilesRequest struct {
	Roots          []string `json:"roots"`
	ComputerFiles  *bool    `json:"computer_files,omitempty"`
	BaseGeneration string   `json:"base_generation"`
}

func (server *Server) commandsAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	switch {
	case path == "/api/commands" && request.Method == http.MethodGet:
		server.writeCommandPolicy(writer)
		return true
	case path == "/api/commands" && request.Method == http.MethodPut:
		server.updateCommandPolicy(writer, request)
		return true
	case path == "/api/commands/catalog" && request.Method == http.MethodGet:
		server.writeCommandCatalog(writer, request)
		return true
	case path == "/api/commands/rules" && request.Method == http.MethodPut:
		server.updateCommandRules(writer, request)
		return true
	case path == "/api/permissions" && request.Method == http.MethodGet:
		server.writePermissions(writer, request)
		return true
	case path == "/api/permissions/selection" && request.Method == http.MethodPut:
		server.updatePermissionSelection(writer, request)
		return true
	case path == "/api/permissions/files" && request.Method == http.MethodPut:
		server.updateComputerRoots(writer, request)
		return true
	case path == "/api/permissions/profile" && request.Method == http.MethodPut:
		server.updateExecutionProfile(writer, request)
		return true
	}
	return false
}

func (server *Server) commandPolicyJSON() map[string]any {
	policy := server.tasks.RuntimePolicy()
	return map[string]any{
		"enabled": policy.Commands.Enabled, "allowed_executables": policy.Commands.AllowedExecutables,
		"timeout_seconds": policy.Commands.TimeoutSecs, "max_output_bytes": policy.Commands.MaxOutput, "services": policy.Commands.Services,
		"inherit_environment": policy.Commands.InheritEnv, "execution": policy.Commands.Execution,
		"rules": policy.Commands.Rules, "git_write": policy.Commands.GitWrite, "generation": policy.Generation,
	}
}

func (server *Server) writeCommandPolicy(writer http.ResponseWriter) {
	value := server.commandPolicyJSON()
	server.mu.Lock()
	value["active_run_uses_previous_snapshot"] = server.active != ""
	server.mu.Unlock()
	server.writeJSON(writer, http.StatusOK, value)
}

func (server *Server) updateCommandPolicy(writer http.ResponseWriter, request *http.Request) {
	var body commandPolicyRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	services := server.tasks.Config().Commands.Services
	if body.Services != nil {
		services = *body.Services
	}
	_, err := server.applyDirectControl(request.Context(), body.BaseGeneration, "", harness.ControlSetCommandPolicy, harness.CommandConfig{
		Enabled: body.Enabled, AllowedExecutables: body.AllowedExecutables,
		TimeoutSecs: body.TimeoutSeconds, MaxOutput: body.MaxOutputBytes, InheritEnv: body.InheritEnvironment,
		Execution: body.Execution, Rules: body.Rules, GitWrite: body.GitWrite,
		Services: services,
	})
	if err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	policy := server.tasks.RuntimePolicy()
	server.writeJSON(writer, http.StatusOK, map[string]any{
		"enabled": policy.Commands.Enabled, "allowed_executables": policy.Commands.AllowedExecutables,
		"timeout_seconds": policy.Commands.TimeoutSecs, "max_output_bytes": policy.Commands.MaxOutput,
		"inherit_environment": policy.Commands.InheritEnv, "execution": policy.Commands.Execution,
		"rules": policy.Commands.Rules, "git_write": policy.Commands.GitWrite, "generation": policy.Generation,
	})
}

func (server *Server) writeCommandCatalog(writer http.ResponseWriter, request *http.Request) {
	catalog, err := server.tasks.CommandCatalog(request.Context())
	if err != nil {
		server.fail(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"entries": catalog})
}

func (server *Server) updateCommandRules(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Rules          []harness.CommandRule `json:"rules"`
		BaseGeneration string                `json:"base_generation"`
	}
	if !server.readBody(writer, request, &body) {
		return
	}
	commands := server.tasks.Config().Commands
	commands.Rules = append([]harness.CommandRule(nil), body.Rules...)
	_, err := server.applyDirectControl(request.Context(), body.BaseGeneration, "", harness.ControlSetCommandPolicy, commands)
	if err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	policy := server.tasks.RuntimePolicy()
	server.writeJSON(writer, http.StatusOK, map[string]any{"rules": policy.Commands.Rules, "generation": policy.Generation})
}

func (server *Server) writePermissions(writer http.ResponseWriter, request *http.Request) {
	chatID := strings.TrimSpace(request.URL.Query().Get("chat_id"))
	selection := harness.PermissionSelection{Mode: harness.PermissionAsk, Source: "default"}
	if chatID != "" {
		value, err := server.tasks.SelectedPermission(request.Context(), chatID)
		if err != nil {
			server.fail(writer, err)
			return
		}
		selection = value
	}
	config := server.tasks.Config()
	policy := server.commandPolicyJSON()
	catalog, catalogErr := server.tasks.CommandCatalog(request.Context())
	if catalogErr != nil {
		server.fail(writer, catalogErr)
		return
	}
	profile, selection, profileErr := server.tasks.EffectiveExecutionProfile(request.Context(), chatID)
	if profileErr != nil {
		server.fail(writer, profileErr)
		return
	}
	server.mu.Lock()
	active := server.active != ""
	server.mu.Unlock()
	server.writeJSON(writer, http.StatusOK, map[string]any{
		"selection": selection, "computer_files": selection.ComputerFiles, "default_computer_files": config.ComputerFiles,
		"computer_roots": config.ComputerRoots, "commands": policy,
		"command_catalog": catalog,
		"profile":         profile, "execution": config.Execution,
		"active_run_uses_previous_snapshot": active,
	})
}

func (server *Server) updatePermissionSelection(writer http.ResponseWriter, request *http.Request) {
	var body permissionSelectionRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	if body.ChatID == "" {
		server.writeError(writer, http.StatusBadRequest, "input", "缺少会话 ID。")
		return
	}
	err := server.applyConversationPermission(request.Context(), body.ChatID, harness.PermissionSelection{
		Mode: body.Mode, Source: "conversation", ComputerFiles: body.ComputerFiles, ComputerRoots: body.ComputerRoots,
		FileScope: body.FileScope, AdditionalRoots: body.AdditionalRoots, BlockedPaths: body.BlockedPaths,
		CommandEnabled: body.CommandEnabled}, body.Confirmed)
	if err != nil {
		server.permissionFailure(writer, err)
		return
	}
	selection, err := server.tasks.SelectedPermission(request.Context(), body.ChatID)
	if err != nil {
		server.fail(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, selection)
}

func (server *Server) updateExecutionProfile(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ChatID          string                 `json:"chat_id"`
		Mode            harness.PermissionMode `json:"permission_mode"`
		FileScope       harness.FileScope      `json:"file_scope"`
		AdditionalRoots []string               `json:"additional_roots"`
		BlockedPaths    []string               `json:"blocked_paths"`
		CommandEnabled  *bool                  `json:"command_enabled,omitempty"`
		NetworkPolicy   harness.NetworkPolicy  `json:"network_policy"`
		Confirmed       bool                   `json:"confirmed"`
		BaseGeneration  string                 `json:"base_generation"`
	}
	if !server.readBody(writer, request, &body) {
		return
	}
	if strings.TrimSpace(body.ChatID) != "" {
		if body.FileScope == "" && len(body.AdditionalRoots) == 0 && len(body.BlockedPaths) == 0 && body.CommandEnabled == nil {
			if _, err := server.applyDirectControl(request.Context(), "", body.ChatID, harness.ControlSetConversationPermission, map[string]any{
				"conversation_id": body.ChatID, "mode": body.Mode,
			}); err != nil {
				server.permissionFailure(writer, err)
				return
			}
			selection, err := server.tasks.SelectedPermission(request.Context(), body.ChatID)
			if err != nil {
				server.fail(writer, err)
				return
			}
			server.writeJSON(writer, http.StatusOK, selection)
			return
		}
		err := server.applyConversationPermission(request.Context(), body.ChatID, harness.PermissionSelection{
			Mode: body.Mode, Source: "conversation", FileScope: body.FileScope,
			AdditionalRoots: body.AdditionalRoots, BlockedPaths: body.BlockedPaths,
			CommandEnabled: body.CommandEnabled}, body.Confirmed)
		if err != nil {
			server.permissionFailure(writer, err)
			return
		}
		profile, selection, err := server.tasks.EffectiveExecutionProfile(request.Context(), body.ChatID)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"profile": profile, "selection": selection})
		return
	}
	if _, err := server.applyDirectControl(request.Context(), body.BaseGeneration, "", harness.ControlSetExecutionProfile, map[string]any{
		"mode": body.Mode, "scope": body.FileScope, "allowed_roots": body.AdditionalRoots,
		"blocked_paths": body.BlockedPaths, "network_policy": body.NetworkPolicy,
	}); err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	policy := server.tasks.RuntimePolicy()
	server.writeJSON(writer, http.StatusOK, map[string]any{"profile": policy.Profile, "generation": policy.Generation})
}

func (server *Server) applyConversationPermission(ctx context.Context, conversationID string,
	selection harness.PermissionSelection, confirmed bool) error {
	previous, err := server.tasks.SelectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	if selection.Mode == harness.PermissionFull && previous.Mode != harness.PermissionFull && !confirmed {
		return harness.ErrFullAccessConfirmation
	}
	selection.Source = "conversation"
	arguments := map[string]any{"conversation_id": conversationID, "mode": selection.Mode}
	if selection.ComputerFiles || selection.FileScope != "" || len(selection.ComputerRoots) != 0 ||
		len(selection.AdditionalRoots) != 0 || len(selection.BlockedPaths) != 0 || selection.CommandEnabled != nil {
		arguments = map[string]any{"conversation_id": conversationID, "selection": selection}
	}
	_, err = server.applyDirectControl(ctx, "", conversationID, harness.ControlSetConversationPermission, arguments)
	return err
}

func (server *Server) updateComputerRoots(writer http.ResponseWriter, request *http.Request) {
	var body permissionFilesRequest
	if !server.readBody(writer, request, &body) {
		return
	}
	config := server.tasks.Config()
	scope := harness.EffectiveFileScope(config)
	if body.ComputerFiles != nil {
		if *body.ComputerFiles {
			scope = harness.FileScopeWorkspacePlusAllowedRoots
		} else {
			scope = harness.FileScopeWorkspace
		}
	}
	_, err := server.applyDirectControl(request.Context(), body.BaseGeneration, "", harness.ControlSetFileScope, map[string]any{
		"scope": scope, "allowed_roots": body.Roots, "blocked_paths": config.Execution.BlockedPaths,
	})
	if err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	policy := server.tasks.RuntimePolicy()
	server.writeJSON(writer, http.StatusOK, map[string]any{"computer_roots": policy.File.ComputerRoots,
		"computer_files": policy.File.ComputerFiles, "generation": policy.Generation})
}

func (server *Server) runtimePolicyFailure(writer http.ResponseWriter, err error) {
	code, message := "runtime_reload_failed", "权限运行时配置未保存。"
	switch {
	case errors.Is(err, harness.ErrGenerationConflict):
		code, message = "generation_conflict", "权限配置已被其他窗口更新，请刷新后重试。"
	case errors.Is(err, harness.ErrProtectedPath):
		code, message = "protected_path", "不能授权 FreeAgent 数据目录或密钥路径。"
	case errors.Is(err, harness.ErrPathNotAllowed):
		code, message = "path_not_allowed", "路径不在已授权范围内。"
	case errors.Is(err, harness.ErrComputerFilesDisabled):
		code, message = "computer_files_disabled", "电脑文件访问尚未开启。"
	case errors.Is(err, harness.ErrComputerRootsRequired):
		code, message = "invalid_request", "至少需要一个电脑文件授权目录。"
	case errors.Is(err, harness.ErrInvalidExecutable):
		code, message = "invalid_executable", "白名单中的可执行文件不存在或不是安全的普通文件。"
	case errors.Is(err, harness.ErrPathBlocked):
		code, message = "path_blocked", "路径被当前文件访问黑名单阻止。"
	case errors.Is(err, harness.ErrSandboxUnsupported):
		code, message = "sandbox_unsupported", "当前主机没有可用的命令沙箱，未回退到宿主机执行。"
	case errors.Is(err, harness.ErrHostExecutionRequiresFull):
		code, message = "permission_denied", "宿主机命令后端只能在当前 Run 明确使用 Full access 时执行。"
	case errors.Is(err, harness.ErrHTTPOriginsRequired):
		code, message = "network_origins_required", "开启网络工具前至少添加一个允许的网络来源。"
	case errors.Is(err, harness.ErrInvalidHTTPOrigin):
		code, message = "invalid_network_origin", "网络来源必须是完整的 http(s)://host[:port] 地址，不能包含路径、查询参数或凭据。"
	}
	server.writeError(writer, http.StatusConflict, code, message)
}

func (server *Server) permissionFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, harness.ErrFullAccessConfirmation) {
		server.writeError(writer, http.StatusConflict, "permission_confirmation", "启用 Full access 需要明确确认。")
		return
	}
	server.runtimePolicyFailure(writer, err)
}
