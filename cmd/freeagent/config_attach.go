package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

// configInvocation is the parsed, user-facing config command. It is converted
// to one typed control operation only when another process owns the store.
// Keeping this adapter at the CLI boundary makes the owner API the only writer
// without duplicating the core config semantics.
type configInvocation struct {
	Command string
	Flags   *flag.FlagSet

	ModelID     string
	Model       harness.ModelConfig
	KeyRef      string
	MakeDefault bool
	RemoveModel bool
	NewDefault  string
	Workspace   string

	Commands           harness.CommandConfig
	AllowedExecutables []string
	HTTP               harness.HTTPConfig
	AllowedOrigins     []string
	SummaryBytes       int

	Auto       harness.AutoConfig
	AutoModels []string

	Collaboration       harness.CollaborationSettings
	CollaborationMode   string
	CollaborationModels []string
	CollaborationTools  []string
	CollaborationPaths  []string
	SharedKinds         []string
	SharedEntries       []string

	Memory harness.MemoryConfig
}

func tryAttachedConfigShow(ctx context.Context, dataDir string, stdout io.Writer) (bool, error) {
	store, err := harness.OpenStore(ctx, dataDir)
	if err == nil {
		_ = store.Close()
		return false, nil
	}
	if !errors.Is(err, harness.ErrOwnerActive) {
		return false, err
	}
	client, err := interactiveOwner(ctx, dataDir)
	if err != nil {
		// A read-only diagnostic can still use config.json when the owner is a
		// non-Web process (for example a short-lived test or CLI owner). Only
		// mutations require an attachable interactive control endpoint.
		return false, nil
	}
	projection, err := client.Config(ctx)
	if err != nil {
		return true, err
	}
	return true, writeJSON(stdout, projection)
}

// tryAttachedConfig first probes the store lock without opening a second
// writer. A live owner must handle the mutation through its control service;
// a free data directory returns false so the existing local path can run.
func tryAttachedConfig(ctx context.Context, dataDir string, invocation configInvocation,
	stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	store, err := harness.OpenStore(ctx, dataDir)
	if err == nil {
		_ = store.Close()
		return false, nil
	}
	if !errors.Is(err, harness.ErrOwnerActive) {
		return false, err
	}
	client, err := interactiveOwner(ctx, dataDir)
	if err != nil {
		return true, harness.ErrOwnerActive
	}
	kind, arguments, err := invocation.controlPlan(ctx, dataDir)
	if err != nil {
		return true, err
	}
	operation, err := client.ControlPlan(ctx, kind, arguments, "")
	if err != nil {
		return true, err
	}
	result, err := applyAttachedControl(ctx, client, operation, "", "", stdin, stderr)
	if err != nil {
		return true, err
	}
	state, err := client.ControlState(ctx)
	if err != nil {
		return true, err
	}
	return true, writeJSON(stdout, map[string]any{
		"ok": true, "changed": result.Applied, "operation": result.Operation,
		"workspace": state.Config.Workspace, "default_model": state.Config.DefaultModel,
		"auto": state.Config.Auto, "control_state": state,
	})
}

func (inv configInvocation) controlPlan(ctx context.Context, dataDir string) (string, json.RawMessage, error) {
	current, err := harness.LoadConfig(dataDir)
	if err != nil {
		return "", nil, err
	}
	visited := func(name string) bool {
		return inv.Flags != nil && inv.Flags.Lookup(name) != nil && inv.Flags.Lookup(name).Value.String() != ""
	}
	switch inv.Command {
	case "workspace":
		if strings.TrimSpace(inv.Workspace) == "" {
			return "", nil, usageErr("config workspace: --path is required")
		}
		workspace, err := filepath.Abs(inv.Workspace)
		if err != nil {
			return "", nil, err
		}
		return marshalControlArgs(harness.ControlSetWorkspace, map[string]string{"workspace": workspace})
	case "model":
		return inv.modelPlan(ctx, dataDir, current)
	case "auto":
		auto := current.Auto
		if visited("enabled") {
			auto.Enabled = inv.Auto.Enabled
		}
		if visited("allow-model") {
			auto.Models = append([]string(nil), inv.AutoModels...)
		}
		if visited("assessment-model") {
			auto.AssessmentModel = inv.Auto.AssessmentModel
		}
		if visited("max-candidates") {
			auto.MaxCandidates = inv.Auto.MaxCandidates
		}
		if visited("max-assessments") {
			auto.MaxAssessments = inv.Auto.MaxAssessments
		}
		if visited("max-upgrades") {
			auto.MaxUpgrades = inv.Auto.MaxUpgrades
		}
		args := map[string]any{
			"enabled": auto.Enabled, "models": auto.Models, "assessment_model": auto.AssessmentModel,
			"max_candidates": auto.MaxCandidates, "max_assessments": auto.MaxAssessments, "max_upgrades": auto.MaxUpgrades,
		}
		if inv.MakeDefault {
			args["default_selection"] = "auto"
		} else if visited("enabled") && !auto.Enabled && current.DefaultSelection == "auto" {
			args["default_selection"] = current.DefaultModel
		}
		return marshalControlArgs(harness.ControlSetAutoPolicy, args)
	case "commands":
		commands := current.Commands
		if visited("enabled") {
			commands.Enabled = inv.Commands.Enabled
		}
		if visited("inherit-environment") {
			commands.InheritEnv = inv.Commands.InheritEnv
		}
		if visited("git-write") {
			commands.GitWrite = inv.Commands.GitWrite
		}
		if visited("timeout") {
			commands.TimeoutSecs = inv.Commands.TimeoutSecs
		}
		if visited("output-bytes") {
			commands.MaxOutput = inv.Commands.MaxOutput
		}
		if visited("allow") {
			commands.AllowedExecutables = append([]string(nil), inv.AllowedExecutables...)
		}
		for _, path := range commands.AllowedExecutables {
			if !filepath.IsAbs(path) {
				return "", nil, usageErr("config commands: --allow requires an absolute executable path")
			}
		}
		return marshalControlArgs(harness.ControlSetCommandPolicy, commands)
	case "http":
		httpConfig := current.HTTP
		if visited("enabled") {
			httpConfig.Enabled = inv.HTTP.Enabled
		}
		if visited("timeout") {
			httpConfig.TimeoutSecs = inv.HTTP.TimeoutSecs
		}
		if visited("output-bytes") {
			httpConfig.MaxOutput = inv.HTTP.MaxOutput
		}
		if visited("origin") {
			httpConfig.AllowedOrigins = append([]string(nil), inv.AllowedOrigins...)
		}
		return marshalControlArgs(harness.ControlSetHTTPPolicy, httpConfig)
	case "context":
		limits := map[string]any{}
		if visited("summary-bytes") {
			limits["task_summary_bytes"] = inv.SummaryBytes
		}
		return marshalControlArgs(harness.ControlSetRuntimeLimits, limits)
	case "memory":
		memory := current.Memory
		if visited("knowledge") {
			memory.KnowledgeEnabled = inv.Memory.KnowledgeEnabled
		}
		if visited("max-entries") {
			memory.MaxEntries = inv.Memory.MaxEntries
		}
		if visited("context-bytes") {
			memory.MaxContextBytes = inv.Memory.MaxContextBytes
		}
		if visited("document-bytes") {
			memory.MaxDocumentBytes = inv.Memory.MaxDocumentBytes
		}
		return marshalControlArgs(harness.ControlSetMemoryPolicy, memory)
	case "collaboration":
		settings := current.Collaboration
		if settings.Mode == "" {
			settings = harness.DefaultCollaborationSettings()
		}
		if visited("mode") {
			settings.Mode = inv.CollaborationMode
		}
		if visited("allow-model") {
			settings.Models = append([]string(nil), inv.CollaborationModels...)
		}
		if visited("allow-tool") {
			settings.Tools = append([]string(nil), inv.CollaborationTools...)
		}
		if visited("allow-path") {
			settings.Paths = append([]string(nil), inv.CollaborationPaths...)
		}
		if visited("max-children") {
			settings.Budget.MaxChildren = inv.Collaboration.Budget.MaxChildren
		}
		if visited("concurrency") {
			settings.Budget.MaxConcurrentChildren = inv.Collaboration.Budget.MaxConcurrentChildren
		}
		if visited("model-calls") {
			settings.Budget.MaxModelCalls = inv.Collaboration.Budget.MaxModelCalls
		}
		if visited("tool-calls") {
			settings.Budget.MaxToolCalls = inv.Collaboration.Budget.MaxToolCalls
		}
		if visited("output-bytes") {
			settings.Budget.MaxOutputBytes = inv.Collaboration.Budget.MaxOutputBytes
		}
		if visited("active-seconds") {
			settings.Budget.MaxActiveSeconds = inv.Collaboration.Budget.MaxActiveSeconds
		}
		if visited("max-replans") {
			settings.MaxReplans = inv.Collaboration.MaxReplans
		}
		if visited("synthesis") {
			settings.Synthesis = inv.Collaboration.Synthesis
		}
		if visited("shared-read") {
			settings.SharedMemory.Read = inv.Collaboration.SharedMemory.Read
		}
		if visited("shared-append") {
			settings.SharedMemory.Append = inv.Collaboration.SharedMemory.Append
		}
		if visited("shared-modify") {
			settings.SharedMemory.Modify = inv.Collaboration.SharedMemory.Modify
		}
		if visited("shared-kind") {
			settings.SharedMemory.Kinds = append([]string(nil), inv.SharedKinds...)
		}
		if visited("shared-entry") {
			settings.SharedMemory.Entries = append([]string(nil), inv.SharedEntries...)
		}
		if !settings.SharedMemory.Read && !settings.SharedMemory.Append && !settings.SharedMemory.Modify {
			settings.SharedMemory.Kinds, settings.SharedMemory.Entries = nil, nil
		}
		return marshalControlArgs(harness.ControlSetCollaboration, settings)
	default:
		return "", nil, usageErr("config: this command cannot be attached")
	}
}

func (inv configInvocation) modelPlan(ctx context.Context, dataDir string, current harness.Config) (string, json.RawMessage, error) {
	visited := map[string]bool{}
	if inv.Flags != nil {
		inv.Flags.Visit(func(item *flag.Flag) {
			if item.Name != "data-dir" && item.Name != "id" {
				visited[item.Name] = true
			}
		})
	}
	if inv.RemoveModel {
		delete(visited, "remove")
		delete(visited, "new-default")
		if len(visited) != 0 {
			return "", nil, usageErr("config model: --remove takes only --id and --new-default")
		}
		return marshalControlArgs(harness.ControlRemoveModel, map[string]string{"id": inv.ModelID, "new_default": inv.NewDefault})
	}
	if inv.NewDefault != "" {
		return "", nil, usageErr("config model: --new-default needs --remove")
	}
	if inv.MakeDefault && len(visited) == 1 {
		return marshalControlArgs(harness.ControlSetDefaultModel, map[string]string{"model_id": inv.ModelID})
	}
	model := inv.Model
	if model.Provider == "" && inv.KeyRef != "" {
		model.Provider = "openai-compatible"
	}
	if strings.TrimSpace(model.Provider) == "" {
		return "", nil, usageErr("config model: --key <profile> and --model <name> are required")
	}
	if model.Provider == "echo" && (inv.KeyRef != "" || model.Model != "") {
		return "", nil, usageErr("config model: echo is for tests and takes neither --key nor --model")
	}
	if inv.KeyRef != "" {
		store, err := keyStore()
		if err != nil {
			return "", nil, err
		}
		view, err := store.Find(inv.KeyRef)
		if err != nil {
			return "", nil, fmt.Errorf("config model: %w: %q; see freeagent key list", err, inv.KeyRef)
		}
		model.Key = view.ID
		suggested := harness.SuggestedModelCapabilities(view, model.Model)
		if !visited["protocol"] && suggested.Protocol != "" {
			model.Capabilities.Protocol = suggested.Protocol
		}
		if !visited["web-search"] && suggested.WebSearch != "" {
			model.Capabilities.WebSearch = suggested.WebSearch
		}
	}
	return marshalControlArgs(harness.ControlSetModelProfile, map[string]any{
		"id": inv.ModelID, "provider": model.Provider, "key": model.Key, "model": model.Model,
		"capabilities": model.Capabilities, "default": inv.MakeDefault,
	})
}

func marshalControlArgs(kind string, value any) (string, json.RawMessage, error) {
	raw, err := json.Marshal(value)
	return kind, raw, err
}
