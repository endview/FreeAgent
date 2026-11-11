package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

type controlStateQuery struct {
	Domain      string `json:"domain,omitempty"`
	Operation   string `json:"operation,omitempty"`
	ModelID     string `json:"model_id,omitempty"`
	ExtensionID string `json:"extension_id,omitempty"`
}

// Read only the requested domain. A normal settings inquiry must never fetch
// optional extension state or pay for the entire operation schema catalog.
func (tools *controlTools) readState(ctx context.Context, query controlStateQuery) (ToolResult, error) {
	if query.Domain == "" {
		query.Domain = controlDomainForKind(query.Operation)
	}
	if !slices.Contains([]string{"overview", "runtime", "models", "tools", "extensions"}, query.Domain) {
		return ToolResult{}, errors.New("harness: unknown control state domain")
	}
	state, err := tools.control.ReadControlStateForModel(ctx)
	if err != nil {
		return ToolResult{}, err
	}
	output := map[string]any{}
	if query.Operation != "" {
		operation, ok := catalogEntry(query.Operation)
		if !ok {
			return ToolResult{}, errors.New("harness: unknown control operation")
		}
		output["operation"] = operation
	} else {
		kinds := []string{}
		for _, operation := range ControlOperationCatalog() {
			if operation.ModelProposable {
				kinds = append(kinds, operation.Kind)
			}
		}
		output["available_operations"] = kinds
	}
	var run Run
	if tools.runID != "" {
		run, err = tools.service.Store.GetRun(ctx, tools.runID)
		if err != nil {
			return ToolResult{}, err
		}
		facts, err := NewTaskService(tools.service).RuntimeFacts(ctx, "", tools.runID)
		if err != nil {
			return ToolResult{}, err
		}
		state.CurrentRun = &facts
		if facts.PersonID != "" {
			state.TelegramRequests, state.ResourceRevisions, state.ExternalChangeResources = nil, nil, nil
			state.Spaces = slices.DeleteFunc(state.Spaces, func(space ControlSpaceState) bool { return space.ID != facts.SpaceID })
			// A channel may inspect its actual model, never other people's
			// provider profiles, admission requests or channel connections.
			state.Config.Models = slices.DeleteFunc(state.Config.Models, func(model ControlModelState) bool { return model.ID != run.ModelID })
			state.Config = ControlConfigState{PermissionMode: state.Config.PermissionMode, Commands: state.Config.Commands,
				Limits: state.Config.Limits, Models: state.Config.Models, DefaultModel: run.ModelID,
				EnabledTools: tools.service.activeToolPolicy().EnabledTools}
		}
		state.Pending = slices.DeleteFunc(state.Pending, func(op ControlOperation) bool { return op.ConversationID != run.ConversationID })
	}
	if query.ModelID != "" {
		state.Config.Models = slices.DeleteFunc(state.Config.Models, func(model ControlModelState) bool { return model.ID != query.ModelID })
	}
	// Safe current values are included with the operation schema. None of these
	// projections contain credential values, env values or optional extension state.
	config := state.Config
	var values any = config
	switch query.Domain {
	case "runtime":
		values = map[string]any{"limits": config.Limits, "models": config.Models, "default_model": config.DefaultModel}
	case "models":
		values = map[string]any{"models": config.Models, "default_model": config.DefaultModel, "auto": config.Auto}
	case "tools":
		values = map[string]any{"enabled_tools": config.EnabledTools, "commands": config.Commands, "http": config.HTTP, "network_mode": config.NetworkMode, "file_scope": config.FileScope}
	case "extensions":
		values = map[string]any{"enabled_extensions": config.EnabledExts, "enabled_skills": config.EnabledSkills, "enabled_mcp_servers": config.EnabledMCP}
	}
	output["config"], output["current_run"], output["pending"] = values, state.CurrentRun, state.Pending
	if query.Domain == "overview" {
		output["spaces"] = state.Spaces
	}
	if query.Domain == "extensions" && (state.CurrentRun == nil || state.CurrentRun.PersonID == "") {
		output["capabilities"] = NewTaskService(tools.service).CapabilityCatalog()
	}
	output["resource_revisions"] = state.ResourceRevisions
	if strings.HasPrefix(query.Operation, "keys.") && tools.service.Keys != nil {
		profiles, err := tools.service.Keys.List()
		if err != nil {
			return ToolResult{}, err
		}
		if state.CurrentRun != nil && state.CurrentRun.PersonID != "" {
			allowed := map[string]bool{}
			for _, model := range state.Config.Models {
				allowed[model.Key] = true
			}
			profiles = slices.DeleteFunc(profiles, func(p KeyProfileView) bool { return !allowed[p.ID] })
		}
		metadata := []map[string]string{}
		for _, p := range profiles {
			metadata = append(metadata, map[string]string{"id": p.ID, "name": tools.service.redactSecrets(p.Name), "note": tools.service.redactSecrets(p.Note), "source": p.Source})
		}
		output["key_profiles"] = metadata
	}
	if query.ExtensionID != "" {
		if run.ID == "" || tools.service.capabilities == nil || !slices.Contains(tools.service.capabilities.Extensions, query.ExtensionID) {
			return ToolResult{}, ErrPermissionDenied
		}
		identity, err := tools.service.extensionIdentity(ctx, run)
		if err != nil || len(identity.Inputs) == 0 {
			return ToolResult{}, ErrPermissionDenied
		}
		definition, err := tools.service.extensions.definition(query.ExtensionID)
		if err != nil || !definition.Manifest.State {
			return ToolResult{}, ErrToolDisabled
		}
		actor := identity.Inputs[len(identity.Inputs)-1].ActorID
		raw, err := NewTaskService(tools.service).ExtensionState(ctx, query.ExtensionID, run.ConversationID, extensionVariant(run, query.ExtensionID, tools.service.Config), actor)
		if err != nil {
			return ToolResult{}, err
		}
		output["extension_state_operations"] = definition.Manifest.StateOperations
		if len(raw) > min(16<<10, tools.service.limits().MaxToolResult/2) {
			output["extension_state"] = map[string]any{"omitted": true, "bytes": len(raw), "next_step": "Use the extension's state page or a specific extension tool; this result is not an empty state."}
		} else {
			output["extension_state"] = json.RawMessage(raw)
		}
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return ToolResult{}, err
	}
	if len(raw) > tools.service.limits().MaxToolResult {
		return ToolResult{Content: `{"error":"control state exceeds the result budget; request domain runtime/models/tools/extensions, model_id, or one operation"}`, IsError: true}, nil
	}
	return ToolResult{Content: string(raw)}, nil
}

func controlOnlyDefinitions(definitions []ToolDefinition) bool {
	for _, definition := range definitions {
		if !isControlToolName(definition.Name) && definition.Name != "reply_expression" {
			return false
		}
	}
	return true
}

func (service *Service) passiveToolAllowed(name string) bool {
	return isControlToolName(name) && service.controlToolsEnabled() || name == "reply_expression" && service.replyExpressionsEnabled()
}

// Chat-only settings proposals remain interactive proposals, not permission
// to execute files, commands, or network tools.
func (service *Service) passiveDefinitions() []ToolDefinition {
	if binding, err := service.binding(); err == nil && binding.Config.EffectiveCapabilities().Tools == "none" {
		return nil
	}
	var definitions []ToolDefinition
	if service.controlToolsEnabled() {
		definitions = (&controlTools{service: service}).controlDefinitions()
	}
	if service.replyExpressionsEnabled() {
		definitions = append(definitions, (replyExpressionTools{base: deniedTools{}, service: service}).Definitions()...)
	}
	return definitions
}

func controlDomainForKind(kind string) string {
	if strings.HasPrefix(kind, "model.") {
		return "models"
	}
	if strings.HasPrefix(kind, "runtime.") {
		return "runtime"
	}
	if strings.HasPrefix(kind, "tools.") || strings.HasPrefix(kind, "execution.") || strings.HasPrefix(kind, "http.") || strings.HasPrefix(kind, "commands.") {
		return "tools"
	}
	if strings.HasPrefix(kind, "extensions.") || strings.HasPrefix(kind, "skills.") || strings.HasPrefix(kind, "mcp.") {
		return "extensions"
	}
	return "overview"
}
