package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

var configCommands = []string{"show", "model", "workspace", "auto", "commands", "context", "http", "collaboration", "memory"}

func runConfig(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || !slices.Contains(configCommands, args[0]) {
		return usageErr("usage: freeagent config <" + strings.Join(configCommands, "|") + "> [flags]; use --help")
	}
	flags := flag.NewFlagSet("config "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	var model harness.ModelConfig
	modelID := "default"
	makeDefault := false
	var keyRef, newDefault, workspace string
	removeModel := false
	var commands harness.CommandConfig
	var httpConfig harness.HTTPConfig
	var allowedExecutables, allowedOrigins repeatedStrings
	summaryBytes := 0
	collaboration := harness.DefaultCollaborationSettings()
	var collaborationMode string
	var collaborationModels, collaborationTools, collaborationPaths repeatedStrings
	var auto harness.AutoConfig
	var autoModels repeatedStrings
	var memory harness.MemoryConfig
	var sharedKinds, sharedEntries repeatedStrings
	if args[0] == "memory" {
		flags.BoolVar(&memory.KnowledgeEnabled, "knowledge", false, "explicitly enable local knowledge import and selected search")
		flags.IntVar(&memory.MaxEntries, "max-entries", 16, "selected entry count, 1-64")
		flags.IntVar(&memory.MaxContextBytes, "context-bytes", 16384, "memory/search input bound, 1024-65536")
		flags.IntVar(&memory.MaxDocumentBytes, "document-bytes", 65536, "import/save document bound, 1024-65536")
	}
	if args[0] == "auto" {
		flags.BoolVar(&auto.Enabled, "enabled", false, "explicitly enable bounded Auto selection")
		flags.BoolVar(&makeDefault, "default", false, "use Auto by default for unselected Chats")
		flags.Var(&autoModels, "allow-model", "authorized profile; repeat, empty authorizes the configured pool")
		flags.StringVar(&auto.AssessmentModel, "assessment-model", "", "optional profile for one bounded assessment of uncertain intent")
		flags.IntVar(&auto.MaxCandidates, "max-candidates", 8, "finite eligible pool bound, 1-32")
		flags.IntVar(&auto.MaxAssessments, "max-assessments", 1, "assessment limit, 0-1")
		flags.IntVar(&auto.MaxUpgrades, "max-upgrades", 1, "safe-boundary upgrade limit, 0-2")
	}
	if args[0] == "collaboration" {
		flags.StringVar(&collaborationMode, "mode", "", "future default: off, ask or auto")
		flags.Var(&collaborationModels, "allow-model", "authorized expert model profile; repeat")
		flags.Var(&collaborationTools, "allow-tool", "authorized expert tool; repeat")
		flags.Var(&collaborationPaths, "allow-path", "authorized expert file subtree; repeat")
		flags.IntVar(&collaboration.Budget.MaxChildren, "max-children", 6, "cumulative experts, including replacements")
		flags.IntVar(&collaboration.Budget.MaxConcurrentChildren, "concurrency", 2, "concurrent experts")
		flags.IntVar(&collaboration.Budget.MaxModelCalls, "model-calls", 40, "parent cumulative model calls")
		flags.IntVar(&collaboration.Budget.MaxToolCalls, "tool-calls", 40, "parent cumulative tool calls")
		flags.IntVar(&collaboration.Budget.MaxOutputBytes, "output-bytes", 512<<10, "parent cumulative output bytes")
		flags.IntVar(&collaboration.Budget.MaxActiveSeconds, "active-seconds", 600, "parent cumulative active seconds; approval waits excluded")
		flags.IntVar(&collaboration.MaxReplans, "max-replans", 1, "finite automatic replans")
		flags.BoolVar(&collaboration.Synthesis, "synthesis", false, "optional bounded final model synthesis")
		flags.BoolVar(&collaboration.SharedMemory.Read, "shared-read", true, "allow task-local shared reads")
		flags.BoolVar(&collaboration.SharedMemory.Append, "shared-append", true, "allow task-local shared additions")
		flags.BoolVar(&collaboration.SharedMemory.Modify, "shared-modify", true, "allow task-local versioned edits; requires reads")
		flags.Var(&sharedKinds, "shared-kind", "authorized shared entry kind; repeat")
		flags.Var(&sharedEntries, "shared-entry", "restrict reads/edits to these shared entries; excludes append; repeat")
	}
	if args[0] == "model" {
		flags.StringVar(&modelID, "id", "default", "local model identifier")
		flags.BoolVar(&makeDefault, "default", false, "use this model by default for unselected Chats")
		flags.StringVar(&model.Provider, "provider", "", "echo (tests only) or openai-compatible; --key implies openai-compatible")
		flags.StringVar(&keyRef, "key", "", "key profile id or name from \"key list\"; names a profile, never the key itself")
		flags.StringVar(&model.Model, "model", "", "model name")
		flags.BoolVar(&removeModel, "remove", false, "remove this model and every reference to it")
		flags.StringVar(&newDefault, "new-default", "", "new default model when --remove takes the default away")
		flags.StringVar(&model.Capabilities.Protocol, "protocol", "", "chat, responses or local")
		flags.StringVar(&model.Capabilities.Streaming, "streaming", "", "supported or none; none requests JSON")
		flags.StringVar(&model.Capabilities.Tools, "tools", "", "supported or none; empty uses the Harness tool contract")
		flags.StringVar(&model.Capabilities.WebSearch, "web-search", "", "supported or none; Responses provider server-side search")
		flags.StringVar(&model.Capabilities.Embeddings, "embeddings", "", "supported or none; enables extension model.embed")
		flags.StringVar(&model.Capabilities.ParallelTools, "parallel-tools", "", "supported or none; none rejects parallel tool history")
		flags.IntVar(&model.Capabilities.ContextBytes, "context-bytes", 0, "model context byte bound; 0 inherits the Harness bound")
		flags.IntVar(&model.Capabilities.OutputTokens, "output-tokens", 0, "model output token bound; 0 inherits the Harness bound")
		flags.StringVar(&model.Capabilities.Strength, "strength", "", "light, standard or strong; local declaration")
		flags.StringVar(&model.Capabilities.FinishReason, "finish-reason", "", "standard; no provider-specific completion inference")
		flags.StringVar(&model.Capabilities.Usage, "usage", "", "optional, required or none; never estimates reported usage")
		flags.StringVar(&model.Capabilities.ErrorPolicy, "error-policy", "", "manual; no automatic retries")
		flags.StringVar(&model.Capabilities.TokenLimit, "token-limit", "", "max_tokens or max_completion_tokens")
	}
	if args[0] == "workspace" {
		flags.StringVar(&workspace, "path", "", "workspace directory")
	}
	if args[0] == "commands" {
		flags.BoolVar(&commands.Enabled, "enabled", false, "explicitly enable approved local commands")
		flags.BoolVar(&commands.InheritEnv, "inherit-environment", false, "inherit ALL parent environment variables")
		flags.BoolVar(&commands.GitWrite, "git-write", false, "let sandboxed commands write .git; hooks and config stay read-only")
		flags.IntVar(&commands.TimeoutSecs, "timeout", 120, "timeout in seconds")
		flags.IntVar(&commands.MaxOutput, "output-bytes", 32768, "captured output limit")
		flags.Var(&allowedExecutables, "allow", "allowed absolute executable path; repeat for multiple paths")
	}
	if args[0] == "http" {
		flags.BoolVar(&httpConfig.Enabled, "enabled", false, "explicitly enable approved HTTP GET")
		flags.IntVar(&httpConfig.TimeoutSecs, "timeout", 20, "timeout in seconds")
		flags.IntVar(&httpConfig.MaxOutput, "output-bytes", 32768, "response text limit")
		flags.Var(&allowedOrigins, "origin", "allowed scheme://host[:port]; repeat for multiple origins")
	}
	if args[0] == "context" {
		flags.IntVar(&summaryBytes, "summary-bytes", 0, "explicit task-local recap bound; 0 disables")
	}
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageErr("config: unexpected arguments")
	}
	resolved, err := dataDirOrError(*dataDir)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := filepath.Abs(resolved)
	if err != nil {
		return err
	}
	if args[0] == "show" {
		attached, err := tryAttachedConfigShow(ctx, path, stdout)
		if err != nil || attached {
			return err
		}
	}
	if args[0] != "show" {
		invocation := configInvocation{
			Command: args[0], Flags: flags,
			ModelID: modelID, Model: model, KeyRef: keyRef, MakeDefault: makeDefault,
			RemoveModel: removeModel, NewDefault: newDefault, Workspace: workspace,
			Commands: commands, AllowedExecutables: allowedExecutables,
			HTTP: httpConfig, AllowedOrigins: allowedOrigins, SummaryBytes: summaryBytes,
			Auto: auto, AutoModels: autoModels,
			Collaboration: collaboration, CollaborationMode: collaborationMode,
			CollaborationModels: collaborationModels, CollaborationTools: collaborationTools,
			CollaborationPaths: collaborationPaths, SharedKinds: sharedKinds, SharedEntries: sharedEntries,
			Memory: memory,
		}
		attached, err := tryAttachedConfig(ctx, path, invocation, stdin, stdout, stderr)
		if err != nil || attached {
			return configCommandError(args[0], err)
		}
		before, err := harness.LoadConfig(path)
		if err != nil {
			return err
		}
		config, result, err := applyLocalConfigControl(ctx, path, invocation)
		if err != nil {
			return configCommandError(args[0], err)
		}
		writeConfigChanges(stderr, before, config, invocation)
		return writeConfigResult(stdout, path, config, result)
	}
	config, err := harness.LoadConfig(path)
	if err != nil {
		return err
	}
	return writeConfigResult(stdout, path, config, nil)
}

func configCommandError(command string, err error) error {
	if err == nil || command != "model" || errors.Is(err, harness.ErrActiveRun) ||
		errors.Is(err, harness.ErrOwnerActive) ||
		errors.Is(err, harness.ErrExternalControlChange) || errors.Is(err, harness.ErrControlStaleRevision) ||
		errors.Is(err, harness.ErrControlBusy) || errors.Is(err, harness.ErrGenerationConflict) {
		return err
	}
	return &exitError{code: exitUsage, err: fmt.Errorf("config model: %w", err)}
}

func writeConfigChanges(stderr io.Writer, before, after harness.Config, invocation configInvocation) {
	if !invocation.RemoveModel {
		return
	}
	_, _ = fmt.Fprintf(stderr, "models: removed %s\n", invocation.ModelID)
	if before.DefaultModel != after.DefaultModel {
		_, _ = fmt.Fprintf(stderr, "default_model: %s\n", after.DefaultModel)
	}
	if before.DefaultSelection != after.DefaultSelection {
		_, _ = fmt.Fprintf(stderr, "default_selection: %s\n", after.DefaultSelection)
	}
}

// applyLocalConfigControl uses the same typed control service as an attached
// owner. Initialization remains the only direct config write because there is
// no current Harness runtime or control journal before init completes.
func applyLocalConfigControl(ctx context.Context, dataDir string, invocation configInvocation) (harness.Config, *harness.ControlResult, error) {
	kind, arguments, err := invocation.controlPlan(ctx, dataDir)
	if err != nil {
		return harness.Config{}, nil, err
	}
	config, err := harness.LoadConfig(dataDir)
	if err != nil {
		return harness.Config{}, nil, err
	}
	store, err := harness.OpenStore(ctx, dataDir)
	if err != nil {
		return harness.Config{}, nil, err
	}
	keys, err := keyStore()
	if err != nil {
		_ = store.Close()
		return harness.Config{}, nil, err
	}
	service := harness.NewService(store, config, harness.Adapters{Keys: keys})
	tasks := harness.NewTaskService(service)
	defer store.Close()
	control, err := tasks.Control()
	if err != nil {
		return harness.Config{}, nil, err
	}
	result, err := control.ApplyDirectControlChange(ctx, kind, arguments, "", "cli")
	if err != nil {
		return harness.Config{}, nil, err
	}
	return tasks.Config(), &result, nil
}

func writeConfigResult(stdout io.Writer, path string, config harness.Config, result *harness.ControlResult) error {
	// Diagnostics deliberately omit prompts, MCP arguments and environment values.
	enabled := make([]string, 0)
	for name, server := range config.MCPServers {
		if server.Enabled {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	capabilities := make(map[string]harness.ModelCapabilities, len(config.Models))
	for id, profile := range config.Models {
		capabilities[id] = profile.EffectiveCapabilities()
	}
	output := map[string]any{
		"ok": true, "config_path": filepath.Join(path, "config.json"), "workspace": config.Workspace,
		"models": config.Models, "default_model": config.DefaultModel, "default_selection": config.DefaultSelection,
		"auto": config.Auto, "enabled_mcp_servers": enabled, "instructions": config.Instructions,
		"model_capabilities": capabilities,
		"commands":           config.Commands, "http": config.HTTP,
		"collaboration": config.Collaboration,
		"memory":        config.Memory,
		"limits": map[string]int{
			"max_steps": config.MaxSteps, "max_messages": config.MaxMessages,
			"max_context_bytes": config.MaxContextBytes, "max_output_tokens": config.MaxOutputTokens,
			"max_run_seconds": config.MaxRunSeconds, "max_tool_result_bytes": config.MaxToolResult,
			"task_summary_bytes": config.TaskSummaryBytes,
		},
		"changed": result != nil, "network_tested": false, "credentials_resolved": false,
	}
	if result != nil {
		output["operation"] = result.Operation
		output["control_result"] = result.Result
	}
	return writeJSON(stdout, output)
}

type repeatedStrings []string

func (values *repeatedStrings) String() string { return strings.Join(*values, ", ") }
func (values *repeatedStrings) Set(value string) error {
	*values = append(*values, value)
	return nil
}
