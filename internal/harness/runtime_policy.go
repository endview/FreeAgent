package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
)

var ErrToolDisabled = errors.New("harness: tool is disabled")

type ToolPolicy struct {
	EnabledTools BuiltinToolSettings `json:"enabled_tools"`
	Commands     CommandConfig       `json:"command_policy"`
	HTTP         HTTPConfig          `json:"http_policy"`
	File         FileAccessPolicy    `json:"file_policy"`
	Generation   string              `json:"tool_generation"`
	Profile      ExecutionProfile    `json:"execution_profile,omitempty"`
}

type RuntimeToolView struct {
	base   *RuntimeTools
	policy ToolPolicy
	gate   *controlDecisionGate
}

func (view *RuntimeToolView) Definitions() []ToolDefinition {
	if view == nil || view.base == nil {
		return nil
	}
	definitions := append([]ToolDefinition(nil), view.base.Definitions()...)
	if !view.policy.EnabledTools.File {
		filtered := definitions[:0]
		for _, definition := range definitions {
			if !isFileToolName(definition.Name) {
				filtered = append(filtered, definition)
			}
		}
		definitions = filtered
	}
	// HTTP is policy-owned so a hot reload can add it even when the process
	// started with networking disabled.
	filtered := definitions[:0]
	for _, definition := range definitions {
		if definition.Name != "http_get" {
			filtered = append(filtered, definition)
		}
	}
	definitions = filtered
	if view.policy.EnabledTools.Network && view.policy.HTTP.Enabled {
		definitions = append(definitions, (&HTTPTools{Config: view.policy.HTTP}).Definitions()...)
	}
	for index := range definitions {
		if !isFileToolName(definitions[index].Name) {
			continue
		}
		if view.policy.File.ComputerFiles || view.policy.File.Scope != FileScopeWorkspace {
			definitions[index].Description = strings.Replace(definitions[index].Description,
				"workspace", "workspace or an explicitly authorized computer directory", 1)
		}
	}
	if view.policy.EnabledTools.CommandRun && view.policy.Commands.Enabled {
		definitions = append(definitions, (&CommandTools{Files: view.base.Files, Workspace: view.base.Files.workspace, DataDir: view.base.dataDir, Config: view.policy.Commands, Profile: view.policy.Profile, Gate: view.gate}).Definitions()...)
	}
	return definitions
}

func (view *RuntimeToolView) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if view == nil || view.base == nil {
		return PreparedTool{}, errors.New("harness: runtime tool view is unavailable")
	}
	if call.Name == "command_run" {
		if !view.policy.EnabledTools.CommandRun || !view.policy.Commands.Enabled {
			return PreparedTool{}, ErrToolDisabled
		}
		return (&CommandTools{Files: view.base.Files, Workspace: view.base.Files.workspace, DataDir: view.base.dataDir, Config: view.policy.Commands, Profile: view.policy.Profile, Gate: view.gate}).Prepare(ctx, call)
	}
	if isFileToolName(call.Name) {
		if !view.policy.EnabledTools.File {
			return PreparedTool{}, ErrToolDisabled
		}
		return NewFileBroker(view.base.Files, view.policy.File).Prepare(ctx, call)
	}
	if call.Name == "http_get" && !view.policy.EnabledTools.Network {
		return PreparedTool{}, ErrToolDisabled
	}
	if call.Name == "http_get" {
		if !view.policy.HTTP.Enabled {
			return PreparedTool{}, ErrToolDisabled
		}
		return (&HTTPTools{Config: view.policy.HTTP}).Prepare(ctx, call)
	}
	return view.base.Prepare(ctx, call)
}

func (view *RuntimeToolView) Policy() ToolPolicy {
	if view == nil {
		return ToolPolicy{}
	}
	copy := view.policy
	copy.EnabledTools = view.policy.EnabledTools
	copy.Commands.AllowedExecutables = append([]string(nil), copy.Commands.AllowedExecutables...)
	copy.Commands.Rules = append([]CommandRule(nil), copy.Commands.Rules...)
	copy.HTTP.AllowedOrigins = append([]string(nil), copy.HTTP.AllowedOrigins...)
	copy.File.ComputerRoots = append([]string(nil), copy.File.ComputerRoots...)
	copy.File.BlockedPaths = append([]string(nil), copy.File.BlockedPaths...)
	copy.Profile.AdditionalRoots = append([]string(nil), copy.Profile.AdditionalRoots...)
	copy.Profile.BlockedPaths = append([]string(nil), copy.Profile.BlockedPaths...)
	copy.Profile.NetworkPolicy.AllowedDomains = append([]string(nil), copy.Profile.NetworkPolicy.AllowedDomains...)
	copy.Profile.CommandPolicy.Rules = append([]CommandRule(nil), copy.Profile.CommandPolicy.Rules...)
	return copy
}

func (view *RuntimeToolView) ForCapabilities(selection CapabilitySelection) *RuntimeToolView {
	if view == nil || view.base == nil {
		return view
	}
	return &RuntimeToolView{base: view.base.ForCapabilities(selection), policy: view.Policy(), gate: view.gate}
}

type ToolRuntimeManager struct {
	mu      sync.RWMutex
	base    *RuntimeTools
	current *RuntimeToolView
	gate    *controlDecisionGate
}

func NewToolRuntimeManager(base *RuntimeTools, config Config) *ToolRuntimeManager {
	manager := &ToolRuntimeManager{base: base}
	profile, err := executionProfileForConfig(config)
	if err != nil {
		profile = ExecutionProfile{PermissionMode: PermissionAsk, ApprovalPolicy: ApprovalAsk,
			SandboxMode: SandboxReadOnly, WorkspaceRoot: config.Workspace, FileScope: FileScopeWorkspace,
			NetworkPolicy: NetworkPolicy{Mode: NetworkOff}, CommandPolicy: commandExecutionPolicy(config),
			Generation: effectiveToolGeneration(config)}
	}
	manager.current = manager.build(toolPolicyForProfile(config, profile))
	return manager
}

func (manager *ToolRuntimeManager) build(policy ToolPolicy) *RuntimeToolView {
	if policy.Generation == "" {
		policy.Generation = "1"
	}
	return &RuntimeToolView{base: manager.base, policy: policy, gate: manager.gate}
}

func (manager *ToolRuntimeManager) SetControlGate(gate *controlDecisionGate) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.gate = gate
	if manager.current != nil {
		manager.current = manager.build(manager.current.Policy())
	}
}

func (manager *ToolRuntimeManager) Acquire() *RuntimeToolView {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current
}

func (manager *ToolRuntimeManager) Definitions() []ToolDefinition {
	return manager.Acquire().Definitions()
}

func (manager *ToolRuntimeManager) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	return manager.Acquire().Prepare(ctx, call)
}

func (manager *ToolRuntimeManager) Build(config Config) *RuntimeToolView {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	profile, err := executionProfileForConfig(config)
	if err != nil {
		profile = manager.Acquire().Policy().Profile
		if profile.Generation == "" {
			profile.Generation = effectiveToolGeneration(config)
		}
	}
	return manager.build(toolPolicyForProfile(config, profile))
}

func (manager *ToolRuntimeManager) Replace(view *RuntimeToolView) {
	manager.mu.Lock()
	manager.current = view
	manager.mu.Unlock()
}

func (manager *ToolRuntimeManager) MCPStatuses(config Config) []MCPServerStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.base.MCPStatuses(config)
}

func (manager *ToolRuntimeManager) RestartMCP(ctx context.Context, config Config) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.base.RestartMCP(ctx, config, "")
}

func effectiveToolGeneration(config Config) string {
	if config.ToolGeneration == "" {
		return "1"
	}
	return config.ToolGeneration
}

func nextToolGeneration(current string) string {
	number, err := strconv.ParseUint(current, 10, 64)
	if err != nil || number == 0 {
		return "1"
	}
	return strconv.FormatUint(number+1, 10)
}

func policyEqual(left, right ToolPolicy) bool {
	rawLeft, _ := json.Marshal(left)
	rawRight, _ := json.Marshal(right)
	return string(rawLeft) == string(rawRight)
}
