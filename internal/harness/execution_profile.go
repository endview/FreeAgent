package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// SandboxMode is the operating-system boundary selected for a Run. The
// values describe the contract; an execution backend decides whether a mode
// is available on the current host.
type SandboxMode string

const (
	SandboxReadOnly       SandboxMode = "read_only"
	SandboxWorkspaceWrite SandboxMode = "workspace_write"
	SandboxSelectedRoots  SandboxMode = "selected_roots"
	SandboxHostFull       SandboxMode = "host_full"
)

func (mode SandboxMode) Validate() error {
	switch mode {
	case SandboxReadOnly, SandboxWorkspaceWrite, SandboxSelectedRoots, SandboxHostFull:
		return nil
	default:
		return fmt.Errorf("harness: invalid sandbox mode %q", mode)
	}
}

type ApprovalPolicy string

const (
	ApprovalAsk           ApprovalPolicy = "ask"
	ApprovalAutoInSandbox ApprovalPolicy = "auto_in_sandbox"
	ApprovalNeverHost     ApprovalPolicy = "never_host"
)

func (policy ApprovalPolicy) Validate() error {
	switch policy {
	case ApprovalAsk, ApprovalAutoInSandbox, ApprovalNeverHost:
		return nil
	default:
		return fmt.Errorf("harness: invalid approval policy %q", policy)
	}
}

type FileScope string

const (
	FileScopeWorkspace                 FileScope = "workspace"
	FileScopeWorkspacePlusAllowedRoots FileScope = "workspace_plus_allowed_roots"
	FileScopeAllNonProtected           FileScope = "all_non_protected"

	// These names remain source-level aliases for callers that used the
	// earlier profile vocabulary. New persisted settings use the product
	// names above.
	FileScopeSelectedRoots FileScope = FileScopeWorkspacePlusAllowedRoots
	FileScopeBrokerAll     FileScope = FileScopeAllNonProtected
	FileScopeHostAll       FileScope = FileScopeAllNonProtected
)

func (scope FileScope) Validate() error {
	switch scope {
	case FileScopeWorkspace, FileScopeWorkspacePlusAllowedRoots, FileScopeAllNonProtected,
		FileScope("selected_roots"), FileScope("broker_all"), FileScope("host_all"):
		return nil
	default:
		return fmt.Errorf("harness: invalid file scope %q", scope)
	}
}

// BuiltinToolSettings controls which built-in tools enter new Runs. The
// command, network and memory policies remain separate contracts; this type
// only answers whether the corresponding capability is present at all.
type BuiltinToolSettings struct {
	File       bool `json:"file"`
	CommandRun bool `json:"command_run"`
	Network    bool `json:"network"`
	Memory     bool `json:"memory"`
}

func defaultBuiltinToolSettings() BuiltinToolSettings {
	return BuiltinToolSettings{File: true, Memory: true}
}

func effectiveBuiltinToolSettings(config Config) BuiltinToolSettings {
	tools := config.Tools
	// A zero value is the safe v5/v6 configuration: file and memory tools were
	// already part of the local Harness, while command and network tools were
	// opt-in. Keep that behavior for fixtures that construct Config directly.
	if !tools.File && !tools.CommandRun && !tools.Network && !tools.Memory && config.Tools == (BuiltinToolSettings{}) {
		defaults := defaultBuiltinToolSettings()
		defaults.CommandRun = config.Commands.Enabled
		defaults.Network = config.HTTP.Enabled
		return defaults
	}
	// Direct callers historically enabled CommandConfig/HTTPConfig without a
	// separate tool toggle. Treat those policy fields as the authoritative
	// opt-in; the settings API keeps them synchronized when a user toggles a
	// built-in tool off.
	tools.CommandRun = tools.CommandRun || config.Commands.Enabled
	tools.Network = tools.Network || config.HTTP.Enabled
	return tools
}

func EffectiveBuiltinTools(config Config) BuiltinToolSettings {
	return effectiveBuiltinToolSettings(config)
}

func EffectiveFileScope(config Config) FileScope {
	scope := canonicalFileScope(effectiveExecutionSettings(config).FileScope)
	if scope == "" {
		return FileScopeWorkspace
	}
	return scope
}

type NetworkMode string

const (
	NetworkOff           NetworkMode = "off"
	NetworkInternet      NetworkMode = "internet"
	NetworkAllowlist     NetworkMode = "allowlist"
	NetworkHostInherited NetworkMode = "host_inherited"
)

func (mode NetworkMode) Validate() error {
	switch mode {
	case NetworkOff, NetworkInternet, NetworkAllowlist, NetworkHostInherited:
		return nil
	default:
		return fmt.Errorf("harness: invalid network policy %q", mode)
	}
}

type NetworkPolicy struct {
	Mode           NetworkMode `json:"mode"`
	AllowedDomains []string    `json:"allowed_domains,omitempty"`
}

func (policy NetworkPolicy) Validate() error {
	if err := policy.Mode.Validate(); err != nil {
		return err
	}
	if policy.Mode != NetworkAllowlist && len(policy.AllowedDomains) != 0 {
		return errors.New("harness: allowed domains require allowlist network policy")
	}
	if len(policy.AllowedDomains) > 64 {
		return errors.New("harness: at most 64 allowed network domains")
	}
	seen := map[string]bool{}
	for _, raw := range policy.AllowedDomains {
		domain := strings.TrimSpace(strings.ToLower(raw))
		if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "/\\\x00\r\n :") || seen[domain] {
			return errors.New("harness: network allowlist contains an invalid or duplicate domain")
		}
		seen[domain] = true
	}
	return nil
}

type CommandExecutionMode string

const (
	CommandExecutionSandboxed    CommandExecutionMode = "sandboxed"
	CommandExecutionHostExplicit CommandExecutionMode = "host_explicit"
)

func (mode CommandExecutionMode) Validate() error {
	switch mode {
	case CommandExecutionSandboxed, CommandExecutionHostExplicit:
		return nil
	default:
		return fmt.Errorf("harness: invalid command execution mode %q", mode)
	}
}

type CommandRuleDecision string

const (
	CommandRuleAllow CommandRuleDecision = "allow"
	CommandRuleAsk   CommandRuleDecision = "ask"
	CommandRuleDeny  CommandRuleDecision = "deny"
)

func (decision CommandRuleDecision) Validate() error {
	switch decision {
	case CommandRuleAllow, CommandRuleAsk, CommandRuleDeny:
		return nil
	default:
		return fmt.Errorf("harness: invalid command rule decision %q", decision)
	}
}

type CommandRule struct {
	Program  string              `json:"program"`
	Args     []string            `json:"args,omitempty"`
	Cwd      string              `json:"cwd,omitempty"`
	Decision CommandRuleDecision `json:"decision"`
}

func (rule CommandRule) Validate() error {
	program := strings.TrimSpace(rule.Program)
	if program == "" || len(program) > 256 || strings.ContainsAny(program, "\x00\r\n") {
		return errors.New("harness: command rule program is invalid")
	}
	if len(rule.Args) > 128 {
		return errors.New("harness: command rule has too many arguments")
	}
	for _, arg := range rule.Args {
		if len(arg) > 32768 || strings.ContainsRune(arg, 0) {
			return errors.New("harness: command rule argument is invalid")
		}
	}
	if rule.Cwd != "" && rule.Cwd != "workspace" && rule.Cwd != "selected_roots" {
		return errors.New("harness: command rule cwd must be workspace or selected_roots")
	}
	return rule.Decision.Validate()
}

type CommandExecutionPolicy struct {
	Enabled   bool                 `json:"enabled"`
	Execution CommandExecutionMode `json:"execution"`
	Rules     []CommandRule        `json:"rules,omitempty"`
}

func (policy CommandExecutionPolicy) Validate() error {
	if err := policy.Execution.Validate(); err != nil {
		return err
	}
	if len(policy.Rules) > 128 {
		return errors.New("harness: at most 128 command rules are allowed")
	}
	for _, rule := range policy.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ExecutionSettings is the persisted global default. It is configuration
// state, not a separate task or permission object.
type ExecutionSettings struct {
	PermissionMode  PermissionMode `json:"permission_mode,omitempty"`
	SandboxMode     SandboxMode    `json:"sandbox_mode,omitempty"`
	FileScope       FileScope      `json:"file_scope,omitempty"`
	AdditionalRoots []string       `json:"additional_roots,omitempty"`
	BlockedPaths    []string       `json:"blocked_paths,omitempty"`
	NetworkPolicy   NetworkPolicy  `json:"network_policy,omitempty"`
}

func (settings ExecutionSettings) validate() error {
	if settings.PermissionMode != "" {
		if err := settings.PermissionMode.Validate(); err != nil {
			return err
		}
	}
	if settings.SandboxMode != "" {
		if err := settings.SandboxMode.Validate(); err != nil {
			return err
		}
	}
	if settings.FileScope != "" {
		if err := settings.FileScope.Validate(); err != nil {
			return err
		}
	}
	if settings.NetworkPolicy.Mode != "" {
		if err := settings.NetworkPolicy.Validate(); err != nil {
			return err
		}
	}
	if len(settings.AdditionalRoots) > 8 {
		return errors.New("harness: at most 8 execution settings roots are allowed")
	}
	if len(settings.BlockedPaths) > 64 {
		return errors.New("harness: at most 64 execution settings blocked paths are allowed")
	}
	for _, path := range settings.BlockedPaths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("harness: execution settings blocked paths must be absolute")
		}
	}
	seen := map[string]bool{}
	for _, root := range settings.AdditionalRoots {
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
			return errors.New("harness: execution settings roots must be absolute")
		}
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			return errors.New("harness: duplicate execution settings root")
		}
		seen[key] = true
	}
	return nil
}

// ExecutionProfile is the immutable effective contract held by a Run. It is
// serialized inside the existing Run snapshot and never re-read from global
// configuration while that Run is active.
type ExecutionProfile struct {
	PermissionMode  PermissionMode         `json:"permission_mode"`
	ApprovalPolicy  ApprovalPolicy         `json:"approval_policy"`
	SandboxMode     SandboxMode            `json:"sandbox_mode"`
	WorkspaceRoot   string                 `json:"workspace_root"`
	AdditionalRoots []string               `json:"additional_roots,omitempty"`
	BlockedPaths    []string               `json:"blocked_paths,omitempty"`
	FileScope       FileScope              `json:"file_scope"`
	NetworkPolicy   NetworkPolicy          `json:"network_policy"`
	CommandPolicy   CommandExecutionPolicy `json:"command_policy"`
	Generation      string                 `json:"generation"`
}

func (profile ExecutionProfile) Validate() error {
	if err := profile.PermissionMode.Validate(); err != nil {
		return err
	}
	if err := profile.ApprovalPolicy.Validate(); err != nil {
		return err
	}
	if err := profile.SandboxMode.Validate(); err != nil {
		return err
	}
	if err := profile.FileScope.Validate(); err != nil {
		return err
	}
	if err := profile.NetworkPolicy.Validate(); err != nil {
		return err
	}
	if err := profile.CommandPolicy.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(profile.WorkspaceRoot) || strings.ContainsAny(profile.WorkspaceRoot, "\x00\r\n") {
		return errors.New("harness: execution profile workspace root must be absolute")
	}
	if strings.TrimSpace(profile.Generation) == "" {
		return errors.New("harness: execution profile generation is required")
	}
	if len(profile.AdditionalRoots) > 8 {
		return errors.New("harness: at most 8 execution profile roots are allowed")
	}
	if len(profile.BlockedPaths) > 64 {
		return errors.New("harness: at most 64 execution profile blocked paths are allowed")
	}
	seen := map[string]bool{}
	for _, root := range profile.AdditionalRoots {
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
			return errors.New("harness: execution profile roots must be absolute")
		}
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			return errors.New("harness: duplicate execution profile root")
		}
		seen[key] = true
	}
	for _, path := range profile.BlockedPaths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("harness: execution profile blocked paths must be absolute")
		}
	}
	return nil
}

func defaultExecutionSettings(workspace string) ExecutionSettings {
	return ExecutionSettings{
		PermissionMode: PermissionAsk,
		SandboxMode:    SandboxReadOnly,
		FileScope:      FileScopeWorkspace,
		NetworkPolicy:  NetworkPolicy{Mode: NetworkOff},
	}
}

func effectiveExecutionSettings(config Config) ExecutionSettings {
	settings := config.Execution
	if settings.PermissionMode == "" {
		settings.PermissionMode = PermissionAsk
	}
	if settings.SandboxMode == "" {
		settings.SandboxMode = SandboxReadOnly
	}
	if settings.FileScope == "" {
		settings.FileScope = FileScopeWorkspace
	}
	if settings.NetworkPolicy.Mode == "" {
		settings.NetworkPolicy = NetworkPolicy{Mode: NetworkOff}
	}
	return settings
}

func canonicalFileScope(scope FileScope) FileScope {
	switch scope {
	case FileScope("selected_roots"):
		return FileScopeWorkspacePlusAllowedRoots
	case FileScope("broker_all"), FileScope("host_all"):
		return FileScopeAllNonProtected
	default:
		return scope
	}
}

func commandExecutionPolicy(config Config) CommandExecutionPolicy {
	mode := config.Commands.Execution
	if mode == "" {
		mode = CommandExecutionSandboxed
	}
	return CommandExecutionPolicy{Enabled: config.Commands.Enabled, Execution: mode,
		Rules: append([]CommandRule(nil), config.Commands.Rules...)}
}

func buildExecutionProfile(config Config, selection PermissionSelection) (ExecutionProfile, error) {
	settings := effectiveExecutionSettings(config)
	mode := selection.Mode
	if mode == "" {
		mode = settings.PermissionMode
	}
	if mode == "" {
		mode = PermissionAsk
	}
	if err := mode.Validate(); err != nil {
		return ExecutionProfile{}, err
	}

	workspace, err := filepath.Abs(config.Workspace)
	if err != nil {
		return ExecutionProfile{}, err
	}
	roots := append([]string(nil), config.ComputerRoots...)
	roots = append(roots, settings.AdditionalRoots...)
	if len(selection.ComputerRoots) != 0 {
		roots = append([]string(nil), selection.ComputerRoots...)
	}
	if len(selection.AdditionalRoots) != 0 {
		roots = append(roots, selection.AdditionalRoots...)
	}
	roots = uniqueAbsoluteRoots(roots)
	blocked := uniqueAbsoluteRoots(append(append([]string(nil), settings.BlockedPaths...), selection.BlockedPaths...))

	command := commandExecutionPolicy(config)
	if selection.CommandEnabled != nil {
		command.Enabled = *selection.CommandEnabled
	}
	profile := ExecutionProfile{
		PermissionMode:  mode,
		WorkspaceRoot:   filepath.Clean(workspace),
		AdditionalRoots: roots,
		BlockedPaths:    blocked,
		NetworkPolicy:   settings.NetworkPolicy,
		CommandPolicy:   command,
		Generation:      effectiveToolGeneration(config),
	}
	scope := canonicalFileScope(selection.FileScope)
	if scope == "" {
		scope = canonicalFileScope(settings.FileScope)
	}
	if selection.ComputerFiles && scope == FileScopeWorkspace {
		scope = FileScopeSelectedRoots
	}

	switch mode {
	case PermissionAsk:
		profile.ApprovalPolicy = ApprovalAsk
		profile.SandboxMode = SandboxReadOnly
		profile.FileScope = scope
	case PermissionWorkspace:
		profile.ApprovalPolicy = ApprovalAutoInSandbox
		profile.FileScope = scope
		if profile.FileScope == FileScopeWorkspacePlusAllowedRoots && len(profile.AdditionalRoots) != 0 {
			profile.SandboxMode = SandboxSelectedRoots
		} else if profile.FileScope == FileScopeAllNonProtected {
			profile.SandboxMode = SandboxWorkspaceWrite
		} else {
			if profile.FileScope != FileScopeWorkspace {
				return ExecutionProfile{}, ErrComputerRootsRequired
			}
			profile.SandboxMode = SandboxWorkspaceWrite
		}
		profile.NetworkPolicy = NetworkPolicy{Mode: NetworkOff}
	case PermissionFull:
		profile.ApprovalPolicy = ApprovalNeverHost
		profile.SandboxMode = SandboxHostFull
		profile.FileScope = FileScopeAllNonProtected
		profile.NetworkPolicy = NetworkPolicy{Mode: NetworkHostInherited}
		profile.CommandPolicy.Execution = CommandExecutionHostExplicit
	}
	if mode != PermissionFull {
		if profile.NetworkPolicy.Mode != NetworkOff && profile.NetworkPolicy.Mode != NetworkInternet {
			return ExecutionProfile{}, errors.New("harness: sandbox network supports only off or internet")
		}
		profile.NetworkPolicy.AllowedDomains = nil
	}
	if err := profile.Validate(); err != nil {
		return ExecutionProfile{}, err
	}
	return profile, nil
}

func executionProfileForConfig(config Config) (ExecutionProfile, error) {
	settings := effectiveExecutionSettings(config)
	selection := PermissionSelection{Mode: settings.PermissionMode, Source: "default",
		ComputerFiles: config.ComputerFiles, ComputerRoots: append([]string(nil), config.ComputerRoots...),
		FileScope: settings.FileScope, AdditionalRoots: append([]string(nil), settings.AdditionalRoots...)}
	return buildExecutionProfile(config, selection)
}

func commandConfigForProfile(base CommandConfig, profile ExecutionProfile) CommandConfig {
	copy := base
	copy.Enabled = profile.CommandPolicy.Enabled
	copy.Execution = profile.CommandPolicy.Execution
	copy.Rules = append([]CommandRule(nil), profile.CommandPolicy.Rules...)
	copy.AllowedExecutables = append([]string(nil), base.AllowedExecutables...)
	return copy
}

func toolPolicyForProfile(config Config, profile ExecutionProfile) ToolPolicy {
	tools := effectiveBuiltinToolSettings(config)
	command := commandConfigForProfile(config.Commands, profile)
	command.Enabled = command.Enabled && tools.CommandRun
	return ToolPolicy{
		EnabledTools: tools,
		Commands:     command,
		HTTP:         HTTPConfig{Enabled: config.HTTP.Enabled, AllowedOrigins: append([]string(nil), config.HTTP.AllowedOrigins...), TimeoutSecs: config.HTTP.TimeoutSecs, MaxOutput: config.HTTP.MaxOutput},
		File: FileAccessPolicy{ComputerFiles: profile.FileScope != FileScopeWorkspace,
			ComputerRoots: append([]string(nil), profile.AdditionalRoots...), BlockedPaths: append([]string(nil), profile.BlockedPaths...), Scope: profile.FileScope},
		Generation: profile.Generation,
		Profile:    profile,
	}
}

func effectiveCommandExecution(config CommandConfig) CommandExecutionMode {
	if config.Execution == "" {
		return CommandExecutionSandboxed
	}
	return config.Execution
}

func sameCommandRules(left, right []CommandRule) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return string(leftRaw) == string(rightRaw)
}

func sameRootList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range left {
		seen[strings.ToLower(filepath.Clean(item))] = true
	}
	for _, item := range right {
		if !seen[strings.ToLower(filepath.Clean(item))] {
			return false
		}
	}
	return true
}

func uniqueAbsoluteRoots(roots []string) []string {
	result := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, raw := range roots {
		path, err := filepath.Abs(filepath.Clean(strings.TrimSpace(raw)))
		if err != nil || path == "." || strings.TrimSpace(raw) == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, filepath.Clean(path))
	}
	return result
}
