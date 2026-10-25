package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	ConfigVersion = 7
	StoreFormat   = "freeagent-harness-v7"
)

type Config struct {
	Version          int                        `json:"version"`
	Models           map[string]ModelConfig     `json:"models"`
	DefaultModel     string                     `json:"default_model"`
	DefaultSelection string                     `json:"default_selection,omitempty"`
	Auto             AutoConfig                 `json:"auto"`
	Workspace        string                     `json:"workspace"`
	MaxSteps         int                        `json:"max_steps"`
	MaxMessages      int                        `json:"max_messages"`
	MaxContextBytes  int                        `json:"max_context_bytes"`
	MaxOutputTokens  int                        `json:"max_output_tokens"`
	MaxRunSeconds    int                        `json:"max_run_seconds"`
	MaxToolResult    int                        `json:"max_tool_result_bytes"`
	TaskSummaryBytes int                        `json:"task_summary_bytes,omitempty"`
	SystemPrompt     string                     `json:"system_prompt,omitempty"`
	Instructions     []string                   `json:"instructions,omitempty"`
	MCPServers       map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	Skills           SkillsConfig               `json:"skills,omitempty"`
	Extensions       ExtensionsConfig           `json:"extensions,omitempty"`
	Tools            BuiltinToolSettings        `json:"tools,omitempty"`
	Commands         CommandConfig              `json:"commands"`
	Execution        ExecutionSettings          `json:"execution,omitempty"`
	ToolGeneration   string                     `json:"tool_generation,omitempty"`
	ComputerFiles    bool                       `json:"computer_files,omitempty"`
	ComputerRoots    []string                   `json:"computer_roots,omitempty"`
	HTTP             HTTPConfig                 `json:"http"`
	Collaboration    CollaborationSettings      `json:"collaboration"`
	Memory           MemoryConfig               `json:"memory"`
	instructionText  string
}

type AutoConfig struct {
	Enabled         bool     `json:"enabled"`
	Models          []string `json:"models,omitempty"`
	AssessmentModel string   `json:"assessment_model,omitempty"`
	MaxCandidates   int      `json:"max_candidates"`
	MaxAssessments  int      `json:"max_assessments"`
	MaxUpgrades     int      `json:"max_upgrades"`
}

type CommandConfig struct {
	Services           ServicePolicy        `json:"services"`
	Enabled            bool                 `json:"enabled"`
	TimeoutSecs        int                  `json:"timeout_seconds"`
	MaxOutput          int                  `json:"max_output_bytes"`
	InheritEnv         bool                 `json:"inherit_environment"`
	AllowedExecutables []string             `json:"allowed_executables,omitempty"`
	Execution          CommandExecutionMode `json:"execution,omitempty"`
	Rules              []CommandRule        `json:"rules,omitempty"`
	// GitWrite lets sandboxed commands write .git. Hooks and repository
	// config stay read-only either way.
	GitWrite bool `json:"git_write"`
}

type FileAccessPolicy struct {
	ComputerFiles bool      `json:"computer_files"`
	ComputerRoots []string  `json:"computer_roots,omitempty"`
	BlockedPaths  []string  `json:"blocked_paths,omitempty"`
	Scope         FileScope `json:"scope,omitempty"`
}

type HTTPConfig struct {
	Enabled        bool     `json:"enabled"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
	TimeoutSecs    int      `json:"timeout_seconds"`
	MaxOutput      int      `json:"max_output_bytes"`
}

type MCPServerConfig struct {
	Enabled           bool              `json:"enabled"`
	Command           string            `json:"command"`
	Args              []string          `json:"args,omitempty"`
	Cwd               string            `json:"cwd,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	EnvFrom           map[string]string `json:"envFrom,omitempty"`
	HiddenTools       []string          `json:"hiddenTools,omitempty"`
	TrustReadOnlyHint bool              `json:"trustReadOnlyHint"`
}

// SkillsConfig and ExtensionsConfig are intentionally only configuration
// state. Discovery and execution stay optional runtime capabilities.
type SkillsConfig struct {
	Enabled           map[string]bool `json:"enabled,omitempty"`
	TrustedWorkspaces map[string]bool `json:"trusted_workspaces,omitempty"`
}

type ExtensionsConfig struct {
	Enabled              map[string]bool              `json:"enabled,omitempty"`
	Required             map[string]bool              `json:"required,omitempty"`
	TrustedWorkspaces    map[string]bool              `json:"trusted_workspaces,omitempty"`
	Settings             map[string]map[string]any    `json:"settings,omitempty"`
	EnvFrom              map[string]map[string]string `json:"envFrom,omitempty"`
	MaxContextChars      int                          `json:"max_context_chars,omitempty"`
	MaxModelCallsPerTurn int                          `json:"max_model_calls_per_turn,omitempty"`
}

// UnmarshalJSON applies the v5 default for omitted MCP read-only trust while
// preserving an explicit false value.
func (config *MCPServerConfig) UnmarshalJSON(raw []byte) error {
	type plain MCPServerConfig
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if _, present := fields["trustReadOnlyHint"]; !present {
		value.TrustReadOnlyHint = true
	}
	*config = MCPServerConfig(value)
	return nil
}

// A keyed model stores only the local Key profile id; the address and secret
// live in keys.json and are resolved immediately before each dispatch.
type ModelConfig struct {
	Provider     string            `json:"provider"`
	Key          string            `json:"key,omitempty"`
	Model        string            `json:"model,omitempty"`
	Capabilities ModelCapabilities `json:"capabilities"`
}

// These are local capability declarations, not claims of provider certification.
// Zero context/output bounds inherit the finite Harness limits.
type ModelCapabilities struct {
	Protocol      string `json:"protocol,omitempty"`
	Streaming     string `json:"streaming,omitempty"`
	Tools         string `json:"tools,omitempty"`
	WebSearch     string `json:"web_search,omitempty"`
	Embeddings    string `json:"embeddings,omitempty"`
	ParallelTools string `json:"parallel_tools,omitempty"`
	ContextBytes  int    `json:"context_bytes,omitempty"`
	OutputTokens  int    `json:"output_tokens,omitempty"`
	Strength      string `json:"strength,omitempty"`
	FinishReason  string `json:"finish_reason,omitempty"`
	Usage         string `json:"usage,omitempty"`
	ErrorPolicy   string `json:"error_policy,omitempty"`
	TokenLimit    string `json:"token_limit,omitempty"`
}

// EffectiveCapabilities makes omitted declarations visible without discovering
// capabilities from a network request or treating them as live certification.
func (model ModelConfig) EffectiveCapabilities() ModelCapabilities {
	caps := model.Capabilities
	if caps.Protocol == "" {
		caps.Protocol = "chat"
		if model.Provider == "echo" {
			caps.Protocol = "local"
		}
	}
	if caps.Streaming == "" {
		caps.Streaming = "supported"
	}
	if caps.Tools == "" {
		caps.Tools = "supported"
	}
	if caps.WebSearch == "" {
		caps.WebSearch = "none"
	}
	if caps.Embeddings == "" {
		caps.Embeddings = "none"
	}
	if caps.ParallelTools == "" {
		caps.ParallelTools = "supported"
	}
	if caps.FinishReason == "" {
		caps.FinishReason = "standard"
	}
	if caps.Usage == "" {
		caps.Usage = "optional"
	}
	if caps.ErrorPolicy == "" {
		caps.ErrorPolicy = "manual"
	}
	if caps.TokenLimit == "" {
		caps.TokenLimit = "max_tokens"
	}
	return caps
}

func DefaultConfig(workspace string) Config {
	return Config{
		Version:         ConfigVersion,
		Models:          map[string]ModelConfig{},
		Auto:            AutoConfig{MaxCandidates: 8, MaxAssessments: 1, MaxUpgrades: 1},
		Collaboration:   DefaultCollaborationSettings(),
		Memory:          defaultMemoryConfig(),
		Workspace:       workspace,
		MaxSteps:        12,
		MaxMessages:     40,
		MaxContextBytes: 128 << 10,
		MaxOutputTokens: 4096,
		MaxRunSeconds:   300,
		MaxToolResult:   64 << 10,
		Commands:        CommandConfig{TimeoutSecs: 120, MaxOutput: 32768, Rules: defaultCommandRules(), Services: DefaultServicePolicy()},
		Tools:           defaultBuiltinToolSettings(),
		Execution:       defaultExecutionSettings(workspace),
		ToolGeneration:  "1",
		HTTP:            HTTPConfig{TimeoutSecs: 20, MaxOutput: 32768},
		Extensions:      ExtensionsConfig{MaxContextChars: 16384, MaxModelCallsPerTurn: 10},
		SystemPrompt:    "You are a concise local file assistant. Use tools when needed. Never claim a file was changed unless the tool result confirms it.",
	}
}

func defaultCommandRules() []CommandRule {
	return []CommandRule{
		{Program: "git", Decision: CommandRuleAllow},
		{Program: "go", Decision: CommandRuleAsk},
		{Program: "node", Decision: CommandRuleAsk},
		{Program: "npm", Decision: CommandRuleAsk},
		{Program: "python", Decision: CommandRuleAsk},
		{Program: "python3", Decision: CommandRuleAsk},
		{Program: "pytest", Decision: CommandRuleAsk},
		{Program: "pwsh", Decision: CommandRuleAsk},
		{Program: "powershell", Decision: CommandRuleAsk},
		{Program: "cmd", Decision: CommandRuleAsk},
	}
}

func LoadConfig(dataDir string) (Config, error) {
	var err error
	dataDir, err = canonicalDir(dataDir)
	if err != nil {
		return Config{}, err
	}
	path := filepath.Join(dataDir, "config.json")
	if err := verifyRegularFile(path); err != nil {
		return Config{}, fmt.Errorf("harness: config: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("harness: read config: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return Config{}, errors.New("harness: config exceeds size limit")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("harness: decode config: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("harness: config must contain one JSON object")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	if err := validateComputerRoots(context.Background(), dataDir, config.ComputerRoots); err != nil {
		return Config{}, err
	}
	if err := validateComputerRoots(context.Background(), dataDir, config.Execution.AdditionalRoots); err != nil {
		return Config{}, err
	}
	config.instructionText, err = readInstructions(dataDir, config.Instructions)
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

func WriteConfig(dataDir string, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if err := validateComputerRoots(context.Background(), dataDir, config.ComputerRoots); err != nil {
		return err
	}
	if err := validateComputerRoots(context.Background(), dataDir, config.Execution.AdditionalRoots); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("harness: create data directory: %w", err)
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: encode config: %w", err)
	}
	raw = append(raw, '\n')
	target := filepath.Join(dataDir, "config.json")
	temp, err := os.CreateTemp(dataDir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("harness: create config staging file: %w", err)
	}
	tempPath := temp.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: secure config staging file: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: write config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("harness: sync config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("harness: close config: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("harness: publish config: %w", err)
	}
	published = true
	return nil
}

// ConfigureModel registers an explicit profile without resolving credentials or
// dispatching requests. Existing run bindings are never rewritten. The first
// configured model also becomes the default.
func ConfigureModel(ctx context.Context, dataDir, id string, model ModelConfig, makeDefault bool) (Config, error) {
	return Configure(ctx, dataDir, func(config *Config) error {
		return applyModel(config, id, model, makeDefault)
	})
}

// applyModel adds or replaces one model. Validation requires a default once
// any model exists, so the first model becomes the default without counting
// as a switch.
func applyModel(config *Config, id string, model ModelConfig, makeDefault bool) error {
	if config == nil {
		return errors.New("harness: configuration is required")
	}
	if !validModelID(id) {
		return errors.New("harness: invalid model identifier")
	}
	if config.Models == nil {
		config.Models = map[string]ModelConfig{}
	}
	config.Models[id] = model
	if makeDefault || config.DefaultModel == "" {
		config.DefaultModel = id
		config.DefaultSelection = id
	}
	return nil
}

// ApplyModel adds or replaces a model in an already loaded configuration.
// Callers that persist through an open TaskService should use this from the
// ConfigureModels edit callback.
func ApplyModel(config *Config, id string, model ModelConfig, makeDefault bool) error {
	return applyModel(config, id, model, makeDefault)
}

func SetDefaultModel(config *Config, id string) error {
	if config == nil {
		return errors.New("harness: configuration is required")
	}
	if _, ok := config.Models[id]; !ok {
		return fmt.Errorf("harness: model %q is not configured", id)
	}
	config.DefaultModel, config.DefaultSelection = id, id
	return nil
}

// RemoveModels deletes models together with every reference to them and
// returns the changes as short English phrases. It edits only the given copy;
// on error the caller must not write it. newDefault, when given, must name a
// remaining model; it replaces a removed default model or default selection.
func RemoveModels(config *Config, ids []string, newDefault string) ([]string, error) {
	if config == nil {
		return nil, errors.New("harness: configuration is required")
	}
	remove := map[string]bool{}
	var order []string
	for _, id := range ids {
		if _, ok := config.Models[id]; !ok {
			return nil, fmt.Errorf("harness: model %q is not configured", id)
		}
		if !remove[id] {
			remove[id] = true
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		return nil, errors.New("harness: choose at least one model to remove")
	}
	remaining := len(config.Models) - len(order)
	if newDefault != "" {
		if _, ok := config.Models[newDefault]; !ok || remove[newDefault] {
			return nil, fmt.Errorf("harness: new default %q must be a remaining model", newDefault)
		}
	}
	removingDefault := remove[config.DefaultModel]
	if removingDefault && remaining > 0 && newDefault == "" {
		return nil, fmt.Errorf("harness: model %q is the default; choose a new default model", config.DefaultModel)
	}
	var changes []string
	for _, id := range order {
		delete(config.Models, id)
		changes = append(changes, "models: removed "+id)
	}
	if len(config.Auto.Models) != 0 {
		kept := make([]string, 0, len(config.Auto.Models))
		for _, id := range config.Auto.Models {
			if remove[id] {
				changes = append(changes, "auto.models: removed "+id)
			} else {
				kept = append(kept, id)
			}
		}
		config.Auto.Models = kept
		// An emptied explicit list must not widen to every model.
		if len(kept) == 0 {
			config.Auto.Models = nil
			if config.Auto.Enabled {
				config.Auto.Enabled = false
				changes = append(changes, "auto: disabled")
			}
		}
	}
	if remove[config.Auto.AssessmentModel] {
		config.Auto.AssessmentModel = ""
		changes = append(changes, "auto.assessment_model: cleared")
	}
	if remaining == 0 && config.Auto.Enabled {
		config.Auto.Enabled = false
		changes = append(changes, "auto: disabled")
	}
	if len(config.Collaboration.Models) != 0 {
		kept := make([]string, 0, len(config.Collaboration.Models))
		for _, id := range config.Collaboration.Models {
			if remove[id] {
				changes = append(changes, "collaboration.models: removed "+id)
			} else {
				kept = append(kept, id)
			}
		}
		config.Collaboration.Models = kept
		if len(kept) == 0 {
			config.Collaboration.Models = nil
			if config.Collaboration.Mode != "" && config.Collaboration.Mode != "off" {
				config.Collaboration.Mode = "off"
				changes = append(changes, "collaboration: off")
			}
		}
	}
	if remaining == 0 {
		config.DefaultModel, config.DefaultSelection = "", ""
		changes = append(changes, "default_model: none")
	} else {
		if removingDefault {
			config.DefaultModel = newDefault
			changes = append(changes, "default_model: "+newDefault)
		}
		if selection := config.DefaultSelection; remove[selection] || selection == "auto" && !config.Auto.Enabled {
			replacement := newDefault
			if replacement == "" {
				replacement = config.DefaultModel
			}
			config.DefaultSelection = replacement
			changes = append(changes, "default_selection: "+replacement)
		}
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return changes, nil
}

// Configure edits the selected environment under its existing sole-writer lock.
func Configure(ctx context.Context, dataDir string, edit func(*Config) error) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	config, err := LoadConfig(dataDir)
	if err != nil {
		return Config{}, err
	}
	store, err := OpenStore(ctx, dataDir)
	if err != nil {
		return Config{}, err
	}
	defer store.Close()
	if edit == nil {
		return Config{}, errors.New("harness: configuration edit is required")
	}
	before, err := safetyConfigJSON(config)
	if err != nil {
		return Config{}, err
	}
	if err := edit(&config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	var unfinished int
	err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM runs
		WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`).Scan(&unfinished)
	if err != nil {
		return Config{}, err
	}
	after, err := safetyConfigJSON(config)
	if err != nil {
		return Config{}, err
	}
	if unfinished != 0 && before != after {
		return Config{}, fmt.Errorf("%w; finish or abandon unfinished tasks before changing configuration", ErrActiveRun)
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	if err := WriteConfig(store.DataDir(), config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	return config.validate(true)
}

func (config Config) validate(requireWorkspace bool) error {
	if config.Version != ConfigVersion {
		return fmt.Errorf("harness: unsupported config version %d", config.Version)
	}
	if !filepath.IsAbs(config.Workspace) {
		return errors.New("harness: workspace must be an absolute directory")
	}
	workspace, err := filepath.Abs(config.Workspace)
	if err != nil {
		return fmt.Errorf("harness: workspace path: %w", err)
	}
	if requireWorkspace {
		if _, err := canonicalDir(workspace); err != nil {
			return err
		}
		info, err := os.Stat(workspace)
		if err != nil {
			return fmt.Errorf("harness: workspace: %w", err)
		}
		if !info.IsDir() {
			return errors.New("harness: workspace is not a directory")
		}
	}
	if config.MaxSteps <= 0 || config.MaxSteps > 100 {
		return errors.New("harness: max_steps must be between 1 and 100")
	}
	if config.MaxMessages <= 0 || config.MaxMessages > 1000 {
		return errors.New("harness: max_messages must be between 1 and 1000")
	}
	if config.MaxToolResult <= 0 || config.MaxToolResult > 16<<20 {
		return errors.New("harness: max_tool_result_bytes is out of range")
	}
	if config.TaskSummaryBytes < 0 || config.TaskSummaryBytes > 16384 {
		return errors.New("harness: task_summary_bytes must be between 0 and 16384")
	}
	if config.MaxContextBytes < 4096 || config.MaxContextBytes > 16<<20 ||
		config.MaxOutputTokens < 1 || config.MaxOutputTokens > 32768 ||
		config.MaxRunSeconds < 1 || config.MaxRunSeconds > 3600 {
		return errors.New("harness: context, output, or run time limit is out of range")
	}
	if len(config.MCPServers) > 16 || len(config.Instructions) > 16 {
		return errors.New("harness: too many instructions or MCP servers")
	}
	if err := validateCapabilityConfig(config.Skills.Enabled, config.Skills.TrustedWorkspaces, "skill"); err != nil {
		return err
	}
	if err := validateExtensionConfig(config.Extensions); err != nil {
		return err
	}
	if config.Commands.Enabled && (config.Commands.TimeoutSecs < 1 || config.Commands.TimeoutSecs > 600 ||
		config.Commands.MaxOutput < 1024 || config.Commands.MaxOutput > 1<<20) {
		return errors.New("harness: command timeout or output limit is out of range")
	}
	if err := config.Commands.Services.Validate(); err != nil {
		return err
	}
	if err := validateCommandAllowlist(config.Commands.AllowedExecutables); err != nil {
		return err
	}
	if config.Commands.Execution != "" {
		if err := config.Commands.Execution.Validate(); err != nil {
			return err
		}
	}
	if len(config.Commands.Rules) > 128 {
		return errors.New("harness: at most 128 command rules are allowed")
	}
	for _, rule := range config.Commands.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	if err := effectiveExecutionSettings(config).validate(); err != nil {
		return err
	}
	if len(config.ComputerRoots) > 8 {
		return errors.New("harness: at most 8 computer file roots are allowed")
	}
	seenComputerRoots := map[string]bool{}
	for _, root := range config.ComputerRoots {
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
			return errors.New("harness: computer file roots must be absolute directories")
		}
		resolved, err := canonicalDir(root)
		if err != nil || seenComputerRoots[strings.ToLower(filepath.Clean(resolved))] {
			return errors.New("harness: invalid or duplicate computer file root")
		}
		seenComputerRoots[strings.ToLower(filepath.Clean(resolved))] = true
	}
	if err := config.HTTP.Validate(); err != nil {
		return err
	}
	seenInstructions := map[string]bool{}
	for _, name := range config.Instructions {
		if !validInstructionName(name) || seenInstructions[name] {
			return errors.New("harness: invalid or duplicate instruction filename")
		}
		seenInstructions[name] = true
	}
	for name, server := range config.MCPServers {
		if !server.Enabled {
			continue
		}
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "\x00\r\n") ||
			!filepath.IsAbs(server.Command) || len(server.Args) > 128 ||
			(server.Cwd != "" && !filepath.IsAbs(server.Cwd)) {
			return errors.New("harness: enabled MCP servers require a name and absolute command/cwd")
		}
		if len(server.HiddenTools) > 128 {
			return errors.New("harness: too many hidden MCP tools")
		}
		seenHidden := map[string]bool{}
		for _, tool := range server.HiddenTools {
			if len(tool) == 0 || len(tool) > 256 || strings.ContainsAny(tool, "\x00\r\n") || seenHidden[tool] {
				return errors.New("harness: invalid or duplicate hidden MCP tool")
			}
			seenHidden[tool] = true
		}
		for _, arg := range server.Args {
			if len(arg) > 32768 || strings.ContainsRune(arg, 0) {
				return errors.New("harness: invalid MCP argument")
			}
		}
		for variable, value := range server.Env {
			if !validEnvName(variable) || strings.ContainsRune(value, 0) || len(value) > 32768 {
				return errors.New("harness: invalid MCP environment constant")
			}
		}
		for variable, source := range server.EnvFrom {
			if !validEnvName(variable) || !validEnvSource(source) {
				return errors.New("harness: invalid MCP environment mapping")
			}
			if _, conflict := server.Env[variable]; conflict {
				return errors.New("harness: MCP env and envFrom overlap")
			}
		}
	}
	if len(config.Models) > 32 {
		return errors.New("harness: configure at most 32 models")
	}
	if len(config.Models) == 0 && (config.DefaultModel != "" || config.DefaultSelection != "" || config.Auto.Enabled) {
		return errors.New("harness: without models, default_model and default_selection must be empty and Auto disabled")
	}
	if _, ok := config.Models[config.DefaultModel]; len(config.Models) != 0 && !ok {
		return errors.New("harness: default_model must name a configured model")
	}
	if config.DefaultSelection != "" && config.DefaultSelection != "auto" {
		if _, ok := config.Models[config.DefaultSelection]; !ok {
			return errors.New("harness: default_selection must name a configured model or auto")
		}
	}
	if config.DefaultSelection == "auto" && !config.Auto.Enabled {
		return errors.New("harness: auto default selection requires auto to be enabled")
	}
	if config.Auto.MaxCandidates < 0 || config.Auto.MaxCandidates > 32 ||
		config.Auto.MaxAssessments < 0 || config.Auto.MaxAssessments > 1 ||
		config.Auto.MaxUpgrades < 0 || config.Auto.MaxUpgrades > 2 {
		return errors.New("harness: auto routing bounds are out of range")
	}
	if config.Auto.Enabled && config.Auto.MaxCandidates < 1 {
		return errors.New("harness: enabled Auto requires a finite candidate bound")
	}
	if len(config.Auto.Models) > 32 {
		return errors.New("harness: too many authorized Auto models")
	}
	seenAuto := map[string]bool{}
	for _, id := range config.Auto.Models {
		if _, exists := config.Models[id]; !exists || seenAuto[id] {
			return errors.New("harness: Auto models must be unique configured profiles")
		}
		seenAuto[id] = true
	}
	if config.Auto.AssessmentModel != "" {
		if _, exists := config.Models[config.Auto.AssessmentModel]; !exists ||
			len(seenAuto) != 0 && !seenAuto[config.Auto.AssessmentModel] {
			return errors.New("harness: assessment model must be in the authorized Auto pool")
		}
	}
	for id, model := range config.Models {
		if !validModelID(id) {
			return errors.New("harness: invalid model identifier")
		}
		if err := model.Validate(); err != nil {
			return fmt.Errorf("harness: model %q: %w", id, err)
		}
	}
	if err := config.Collaboration.validate(config, nil); err != nil {
		return err
	}
	return config.Memory.validate()
}

func validModelID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return false
		}
	}
	return id != "auto"
}

func validCapabilityID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return false
		}
	}
	return true
}

func validateCapabilityConfig(enabled, trusted map[string]bool, label string) error {
	if len(enabled) > 256 || len(trusted) > 256 {
		return fmt.Errorf("harness: too many %s capability settings", label)
	}
	for id := range enabled {
		if !validCapabilityID(id) {
			return fmt.Errorf("harness: invalid %s identifier", label)
		}
	}
	for workspace := range trusted {
		if workspace == "" || !filepath.IsAbs(workspace) {
			return fmt.Errorf("harness: %s trust workspace must be absolute", label)
		}
	}
	return nil
}

func validateExtensionConfig(config ExtensionsConfig) error {
	if err := validateCapabilityConfig(config.Enabled, nil, "extension"); err != nil {
		return err
	}
	if len(config.Required) > 256 || len(config.TrustedWorkspaces) > 256 ||
		len(config.Settings) > 256 || len(config.EnvFrom) > 256 {
		return errors.New("harness: too many extension settings")
	}
	for id, required := range config.Required {
		if !validCapabilityID(id) {
			return errors.New("harness: invalid extension identifier")
		}
		if required && !config.Enabled[id] {
			return fmt.Errorf("harness: required extension %q must be enabled", id)
		}
	}
	for workspace, trusted := range config.TrustedWorkspaces {
		if !trusted || workspace == "" || !filepath.IsAbs(workspace) {
			return errors.New("harness: extension trust workspace must be absolute")
		}
	}
	for id := range config.Settings {
		if !validCapabilityID(id) {
			return errors.New("harness: invalid extension identifier")
		}
	}
	for id, values := range config.EnvFrom {
		if !validCapabilityID(id) {
			return errors.New("harness: invalid extension identifier")
		}
		for variable, source := range values {
			if !validEnvName(variable) || !validEnvSource(source) {
				return errors.New("harness: invalid extension environment mapping")
			}
		}
	}
	if config.MaxContextChars < 0 || config.MaxContextChars > 1<<20 ||
		config.MaxModelCallsPerTurn < 0 || config.MaxModelCallsPerTurn > 128 {
		return errors.New("harness: extension limits are out of range")
	}
	return nil
}

func validEnvSource(source string) bool {
	if strings.HasPrefix(source, "key:") {
		return validKeyID(strings.TrimPrefix(source, "key:"))
	}
	return validEnvName(source)
}

// SuggestModelID turns a provider model name into a stable local id and
// reserves a suffix when the generated id is already in use.
func SuggestModelID(name string, config Config) string {
	var builder strings.Builder
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	base := builder.String()
	if base == "" {
		base = "model"
	}
	if len(base) > 64 {
		base = base[:64]
	}
	if _, exists := config.Models[base]; !exists && base != "auto" {
		return base
	}
	for suffix := 2; ; suffix++ {
		tail := fmt.Sprintf("-%d", suffix)
		prefix := base
		if len(prefix)+len(tail) > 64 {
			prefix = prefix[:64-len(tail)]
		}
		candidate := prefix + tail
		if candidate != "auto" {
			if _, exists := config.Models[candidate]; !exists {
				return candidate
			}
		}
	}
}

func (config Config) DefaultModelConfig() ModelConfig { return config.Models[config.DefaultModel] }

func (caps ModelCapabilities) validate() error {
	if (caps.Protocol != "" && caps.Protocol != "chat" && caps.Protocol != "responses" && caps.Protocol != "local") ||
		(caps.Streaming != "" && caps.Streaming != "supported" && caps.Streaming != "none") ||
		(caps.Tools != "" && caps.Tools != "supported" && caps.Tools != "none") ||
		(caps.WebSearch != "" && caps.WebSearch != "supported" && caps.WebSearch != "none") ||
		(caps.Embeddings != "" && caps.Embeddings != "supported" && caps.Embeddings != "none") ||
		(caps.ParallelTools != "" && caps.ParallelTools != "supported" && caps.ParallelTools != "none") ||
		(caps.ContextBytes != 0 && (caps.ContextBytes < 4096 || caps.ContextBytes > 16<<20)) ||
		caps.OutputTokens < 0 || caps.OutputTokens > 32768 ||
		(caps.Strength != "" && caps.Strength != "light" && caps.Strength != "standard" && caps.Strength != "strong") ||
		(caps.FinishReason != "" && caps.FinishReason != "standard") ||
		(caps.Usage != "" && caps.Usage != "optional" && caps.Usage != "required" && caps.Usage != "none") ||
		(caps.ErrorPolicy != "" && caps.ErrorPolicy != "manual") ||
		(caps.TokenLimit != "" && caps.TokenLimit != "max_tokens" && caps.TokenLimit != "max_completion_tokens") {
		return errors.New("invalid model capabilities")
	}
	return nil
}

func validModelName(name string) bool {
	return len(name) <= 256 && !strings.ContainsAny(name, "\x00\r\n")
}

func (model ModelConfig) Validate() error {
	caps := model.Capabilities
	if err := caps.validate(); err != nil {
		return err
	}
	if !validModelName(model.Model) {
		return errors.New("invalid model name")
	}
	switch model.Provider {
	case "echo":
		if model.Key != "" || model.Model != "" {
			return errors.New("harness: echo does not use key or model settings")
		}
		if caps.Protocol != "" && caps.Protocol != "local" {
			return errors.New("harness: echo uses the local protocol")
		}
		return nil
	case "openai-compatible":
		if caps.Protocol == "local" {
			return errors.New("harness: openai-compatible requires chat or responses protocol")
		}
		if !validKeyID(model.Key) || strings.TrimSpace(model.Model) == "" {
			return errors.New("harness: openai-compatible requires a key id and model name")
		}
		return nil
	case "anthropic":
		if caps.Protocol == "local" || caps.Protocol == "responses" {
			return errors.New("harness: anthropic requires the chat protocol")
		}
		if caps.Embeddings == "supported" {
			return errors.New("harness: anthropic does not support embeddings in this adapter")
		}
		if !validKeyID(model.Key) || strings.TrimSpace(model.Model) == "" {
			return errors.New("harness: anthropic requires a key id and model name")
		}
		return nil
	default:
		return fmt.Errorf("harness: unsupported model provider %q", model.Provider)
	}
}

func safetyConfig(config Config) Config {
	config.Models, config.DefaultModel, config.DefaultSelection = nil, "", ""
	config.Auto = AutoConfig{}
	config.Collaboration = CollaborationSettings{}
	config.Commands = CommandConfig{}
	config.Tools = BuiltinToolSettings{}
	config.Execution = ExecutionSettings{}
	config.ToolGeneration = ""
	config.ComputerFiles = false
	config.ComputerRoots = nil
	return config
}

func safetyConfigJSON(config Config) (string, error) {
	raw, err := json.Marshal(safetyConfig(config))
	return string(raw), err
}

func validEnvName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for index, char := range name {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || index > 0 && char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func readInstructions(dataDir string, names []string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	directory, err := canonicalDir(filepath.Join(dataDir, "instructions"))
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	var text strings.Builder
	for _, name := range names {
		if !validInstructionName(name) {
			return "", errors.New("harness: instruction names must be simple filenames")
		}
		file, err := openWorkspaceFile(root, name)
		if err != nil {
			return "", errors.New("harness: cannot open selected instruction")
		}
		handleErr := checkRegularHandle(file)
		raw, readErr := io.ReadAll(io.LimitReader(file, (32<<10)+1))
		_ = file.Close()
		if handleErr != nil || readErr != nil || len(raw) > 32<<10 || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
			return "", errors.New("harness: selected instruction is not bounded UTF-8 text")
		}
		text.Write(raw)
		text.WriteByte('\n')
	}
	return text.String(), nil
}

func validInstructionName(name string) bool {
	return len(name) <= 128 && filepath.IsLocal(name) && name != "." && !strings.ContainsAny(name, ":/\\\x00")
}
