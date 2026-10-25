package chatui

import (
	"net/http"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/winlaunch"
)

type settingsPatch struct {
	ExpectedRevision string `json:"expected_revision"`
}

type settingsFilePatch struct {
	settingsPatch
	Scope              harness.FileScope `json:"scope"`
	AllowedRoots       []string          `json:"allowed_roots"`
	GlobalBlockedPaths []string          `json:"global_blocked_paths"`
}

type settingsToolsPatch struct {
	settingsPatch
	File                  *bool     `json:"file"`
	CommandRun            *bool     `json:"command_run"`
	Network               *bool     `json:"network"`
	Memory                *bool     `json:"memory"`
	NetworkAllowedOrigins *[]string `json:"network_allowed_origins"`
}

type settingsCommandPatch struct {
	settingsPatch
	Enabled            *bool                        `json:"enabled"`
	Execution          harness.CommandExecutionMode `json:"execution"`
	Rules              []harness.CommandRule        `json:"rules"`
	AllowedExecutables []string                     `json:"allowed_executables"`
	TimeoutSeconds     int                          `json:"timeout_seconds"`
	MaxOutputBytes     int                          `json:"max_output_bytes"`
	InheritEnvironment *bool                        `json:"inherit_environment"`
	GitWrite           *bool                        `json:"git_write"`
	Services           *harness.ServicePolicy       `json:"services"`
}

func (server *Server) settingsAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	switch {
	case path == "/api/settings" && request.Method == http.MethodGet:
		server.writeSettings(writer, request)
	case path == "/api/settings/permissions" && request.Method == http.MethodPatch:
		server.patchSettingsPermissions(writer, request)
	case path == "/api/settings/file-access" && request.Method == http.MethodPatch:
		server.patchSettingsFileAccess(writer, request)
	case path == "/api/settings/tools" && request.Method == http.MethodPatch:
		server.patchSettingsTools(writer, request)
	case path == "/api/settings/command-run" && request.Method == http.MethodPatch:
		server.patchSettingsCommand(writer, request)
	case path == "/api/runtime-tools" && request.Method == http.MethodGet:
		server.writeRuntimeTools(writer, request)
	default:
		return false
	}
	return true
}

func (server *Server) settingsProjection() map[string]any {
	config := server.tasks.Config()
	policy := server.tasks.RuntimePolicy()
	tools := harness.EffectiveBuiltinTools(config)
	scope := harness.EffectiveFileScope(config)
	command := config.Commands
	commandEnabled := tools.CommandRun && command.Enabled
	return map[string]any{
		"schema_version": 1,
		"revision":       policy.Generation,
		"default_permission_mode": func() harness.PermissionMode {
			if config.Execution.PermissionMode == "" {
				return harness.PermissionAsk
			}
			return config.Execution.PermissionMode
		}(),
		"enabled_tools": map[string]bool{
			"file": tools.File, "command_run": commandEnabled, "network": tools.Network, "memory": tools.Memory,
		},
		"file_access": map[string]any{
			"scope": scope, "allowed_roots": config.ComputerRoots,
			"global_blocked_paths": config.Execution.BlockedPaths,
			"protected_paths":      server.tasks.ProtectedPaths(),
		},
		"command_run": map[string]any{
			"enabled": commandEnabled, "available": command.Execution == harness.CommandExecutionHostExplicit || winlaunch.SandboxSupported(),
			"status": commandStatus(command, tools.CommandRun, config.Execution.PermissionMode), "backend": command.Execution,
			"sandbox_supported": winlaunch.SandboxSupported(),
			"rules":             command.Rules, "allowed_executables": command.AllowedExecutables,
			"timeout_seconds": command.TimeoutSecs, "max_output_bytes": command.MaxOutput, "services": command.Services,
			"inherit_environment": command.InheritEnv, "git_write": command.GitWrite,
		},
		"network": map[string]any{"enabled": tools.Network && config.HTTP.Enabled, "allowed_origins": config.HTTP.AllowedOrigins,
			"mode": config.Execution.NetworkPolicy.Mode, "allowed_domains": config.Execution.NetworkPolicy.AllowedDomains},
	}
}

func commandStatus(command harness.CommandConfig, enabled bool, permission ...harness.PermissionMode) string {
	if !enabled {
		return "disabled"
	}
	if command.Execution == "" || command.Execution == harness.CommandExecutionSandboxed {
		if !winlaunch.SandboxSupported() {
			return "unsupported"
		}
		return "ready"
	}
	if len(permission) != 0 && permission[0] != harness.PermissionFull {
		return "permission_denied"
	}
	return "ready"
}

func (server *Server) writeSettings(writer http.ResponseWriter, _ *http.Request) {
	server.writeJSON(writer, http.StatusOK, server.settingsProjection())
}

func (server *Server) writeRuntimeTools(writer http.ResponseWriter, request *http.Request) {
	config := server.tasks.Config()
	policy := server.tasks.RuntimePolicy()
	definitions := policyDefinitions(policy, server.tasks)
	server.writeJSON(writer, http.StatusOK, map[string]any{
		"revision": policy.Generation, "enabled_tools": harness.EffectiveBuiltinTools(config),
		"definitions": definitions, "active_run_uses_previous_snapshot": server.hasActiveRun(),
	})
}

func policyDefinitions(policy harness.ToolPolicy, tasks *harness.TaskService) []map[string]any {
	_ = tasks
	return []map[string]any{
		{"tool": "file", "enabled": policy.EnabledTools.File, "status": boolStatus(policy.EnabledTools.File)},
		{"tool": "command_run", "enabled": policy.EnabledTools.CommandRun && policy.Commands.Enabled, "status": commandStatus(policy.Commands, policy.EnabledTools.CommandRun, policy.Profile.PermissionMode)},
		{"tool": "network", "enabled": policy.EnabledTools.Network, "status": boolStatus(policy.EnabledTools.Network)},
		{"tool": "memory", "enabled": policy.EnabledTools.Memory, "status": boolStatus(policy.EnabledTools.Memory)},
	}
}

func boolStatus(enabled bool) string {
	if enabled {
		return "ready"
	}
	return "disabled"
}

func (server *Server) hasActiveRun() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.active != ""
}

func (server *Server) patchSettingsPermissions(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		settingsPatch
		DefaultPermissionMode harness.PermissionMode `json:"default_permission_mode"`
		NetworkPolicy         harness.NetworkPolicy  `json:"network_policy"`
	}
	if !server.readBody(writer, request, &body) {
		return
	}
	current := server.tasks.Config()
	mode := body.DefaultPermissionMode
	if mode == "" {
		mode = current.Execution.PermissionMode
	}
	if err := mode.Validate(); err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	if _, err := server.applyDirectControl(request.Context(), body.ExpectedRevision, "", harness.ControlSetPermission, map[string]any{"mode": mode}); err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	if body.NetworkPolicy.Mode != "" {
		if _, err := server.applyDirectControl(request.Context(), "", "", harness.ControlSetNetwork, body.NetworkPolicy); err != nil {
			server.runtimePolicyFailure(writer, err)
			return
		}
	}
	server.writeJSON(writer, http.StatusOK, server.settingsProjection())
}

func (server *Server) patchSettingsFileAccess(writer http.ResponseWriter, request *http.Request) {
	var body settingsFilePatch
	if !server.readBody(writer, request, &body) {
		return
	}
	scope := body.Scope
	if scope == "" {
		scope = harness.FileScopeWorkspace
	}
	if err := harness.ValidateFileAccessSettings(request.Context(), server.tasks.DataDir(), scope, body.AllowedRoots, body.GlobalBlockedPaths); err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	_, err := server.applyDirectControl(request.Context(), body.ExpectedRevision, "", harness.ControlSetFileScope, map[string]any{
		"scope": scope, "allowed_roots": body.AllowedRoots, "blocked_paths": body.GlobalBlockedPaths,
	})
	if err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, server.settingsProjection())
}

func (server *Server) patchSettingsTools(writer http.ResponseWriter, request *http.Request) {
	var body settingsToolsPatch
	if !server.readBody(writer, request, &body) {
		return
	}
	tools := harness.EffectiveBuiltinTools(server.tasks.Config())
	changes := []struct {
		name    string
		enabled *bool
	}{
		{"file", body.File}, {"command_run", body.CommandRun}, {"network", body.Network}, {"memory", body.Memory},
	}
	first := body.ExpectedRevision
	// An enabled HTTP policy must already have its origins when the network
	// switch is committed. Stage that part first, then apply the tool switch.
	if body.Network != nil && *body.Network && body.NetworkAllowedOrigins != nil {
		current := server.tasks.Config().HTTP
		current.Enabled = true
		current.AllowedOrigins = append([]string(nil), (*body.NetworkAllowedOrigins)...)
		if _, err := server.applyDirectControl(request.Context(), first, "", harness.ControlSetHTTPPolicy, current); err != nil {
			server.runtimePolicyFailure(writer, err)
			return
		}
		first = ""
	}
	for _, change := range changes {
		if change.enabled == nil {
			continue
		}
		if change.name == "file" {
			tools.File = *change.enabled
		} else if change.name == "command_run" {
			tools.CommandRun = *change.enabled
		} else if change.name == "network" {
			tools.Network = *change.enabled
		} else {
			tools.Memory = *change.enabled
		}
		_, err := server.applyDirectControl(request.Context(), first, "", harness.ControlSetTool, map[string]any{"tool": change.name, "enabled": *change.enabled})
		if err != nil {
			server.runtimePolicyFailure(writer, err)
			return
		}
		first = ""
	}
	if body.NetworkAllowedOrigins != nil && !(body.Network != nil && *body.Network) {
		current := server.tasks.Config().HTTP
		current.AllowedOrigins = append([]string(nil), (*body.NetworkAllowedOrigins)...)
		if !tools.Network {
			current.AllowedOrigins = nil
		}
		if _, err := server.applyDirectControl(request.Context(), first, "", harness.ControlSetHTTPPolicy, current); err != nil {
			server.runtimePolicyFailure(writer, err)
			return
		}
	}
	server.writeJSON(writer, http.StatusOK, server.settingsProjection())
}

func (server *Server) patchSettingsCommand(writer http.ResponseWriter, request *http.Request) {
	var body settingsCommandPatch
	if !server.readBody(writer, request, &body) {
		return
	}
	commands := server.tasks.Config().Commands
	if body.Services != nil {
		commands.Services = *body.Services
	}
	if body.Enabled != nil {
		commands.Enabled = *body.Enabled
	}
	if body.Execution != "" {
		commands.Execution = body.Execution
	}
	if body.Rules != nil {
		commands.Rules = append([]harness.CommandRule(nil), body.Rules...)
	}
	if body.AllowedExecutables != nil {
		commands.AllowedExecutables = append([]string(nil), body.AllowedExecutables...)
	}
	if body.TimeoutSeconds != 0 {
		commands.TimeoutSecs = body.TimeoutSeconds
	}
	if body.MaxOutputBytes != 0 {
		commands.MaxOutput = body.MaxOutputBytes
	}
	if body.InheritEnvironment != nil {
		commands.InheritEnv = *body.InheritEnvironment
	}
	if body.GitWrite != nil {
		commands.GitWrite = *body.GitWrite
	}
	_, err := server.applyDirectControl(request.Context(), body.ExpectedRevision, "", harness.ControlSetCommandPolicy, commands)
	if err != nil {
		server.runtimePolicyFailure(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, server.settingsProjection())
}
