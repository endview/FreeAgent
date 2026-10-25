package chatui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) chatCommand(writer http.ResponseWriter, request *http.Request) {
	if !server.isInteractiveAttachRequest(request) {
		server.writeError(writer, http.StatusForbidden, "user_interaction_required", "Terminal menus require the interactive owner credential.")
		return
	}
	var command handoff.ChatCommand
	if !server.readBody(writer, request, &command) {
		return
	}
	ctx, tasks := request.Context(), server.tasks
	var result any = map[string]bool{"ok": true}
	var err error
	decode := func(target any) bool {
		if err = json.Unmarshal(command.Value, target); err != nil {
			server.writeError(writer, http.StatusBadRequest, "input", "Invalid menu arguments.")
			return false
		}
		return true
	}
	switch command.Action {
	case "config":
		config := tasks.Config()
		// Only menu metadata crosses the surface; never prompts, env or secrets.
		projection := harness.Config{Workspace: config.Workspace, MaxContextBytes: config.MaxContextBytes, Memory: config.Memory}
		projection.Auto.Enabled = config.Auto.Enabled
		projection.Models = map[string]harness.ModelConfig{}
		for id, model := range config.Models {
			projection.Models[id] = harness.ModelConfig{Provider: model.Provider, Model: model.Model}
		}
		result = projection
	case "messages":
		result, err = tasks.Messages(ctx, command.Chat)
	case "latest":
		result, err = tasks.Latest(ctx, command.Chat)
	case "selected-model":
		result, err = tasks.SelectedModel(ctx, command.Chat)
	case "select-model":
		var model string
		if !decode(&model) {
			return
		}
		_, err = server.applyDirectControl(ctx, "", command.Chat, harness.ControlSetConversationModel,
			map[string]any{"conversation_id": command.Chat, "model_id": model})
	case "limits":
		result, err = tasks.Limits(ctx, command.Run)
	case "increase-limits":
		var limits harness.ExecutionLimits
		if !decode(&limits) {
			return
		}
		err = tasks.IncreaseLimits(ctx, command.Run, limits)
	case "abandon":
		var controlled bool
		controlled, err = tasks.IsCollaboration(ctx, command.Run)
		if err == nil {
			if controlled {
				err = tasks.StopCollaboration(ctx, command.Run)
			} else {
				err = tasks.Abandon(ctx, command.Run)
			}
		}
	case "is-collaboration":
		result, err = tasks.IsCollaboration(ctx, command.Run)
	case "expand-references":
		var input struct {
			Prompt string
			Paths  []string
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.ExpandReferences(ctx, input.Prompt, input.Paths)
	case "collaboration-settings":
		result, err = tasks.CollaborationSettings(ctx, command.Chat)
	case "set-collaboration", "default-collaboration":
		var settings harness.CollaborationSettings
		if !decode(&settings) {
			return
		}
		if command.Action == "default-collaboration" {
			_, err = server.applyDirectControl(ctx, "", "", harness.ControlSetCollaboration, settings)
		} else {
			err = tasks.SetCollaborationSettings(ctx, command.Chat, settings)
		}
	case "agent":
		result, err = tasks.AgentIdentity(ctx, command.Chat)
	case "select-agent":
		var agent string
		if !decode(&agent) {
			return
		}
		err = tasks.SelectAgent(ctx, command.Chat, agent)
	case "memory-scopes":
		var kind string
		if !decode(&kind) {
			return
		}
		result, err = tasks.MemoryScopes(ctx, command.Chat, kind)
	case "memory-selected":
		result, err = tasks.SelectedMemory(ctx, command.Chat)
	case "memory-select":
		var selection harness.MemorySelection
		if !decode(&selection) {
			return
		}
		err = tasks.ApplyUserMemorySelection(ctx, command.Chat, selection)
	case "memory-config":
		var config harness.MemoryConfig
		if !decode(&config) {
			return
		}
		err = tasks.ApplyUserMemoryConfig(ctx, config)
	case "memory-list":
		var input struct {
			Scope harness.MemoryScope
			Limit int
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.ListMemory(ctx, command.Chat, input.Scope, input.Limit)
	case "memory-read":
		var input struct {
			Scope harness.MemoryScope
			ID    string
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.ReadMemory(ctx, command.Chat, input.Scope, input.ID)
	case "memory-save":
		var mutation harness.MemoryMutation
		if !decode(&mutation) {
			return
		}
		result, err = tasks.SaveMemory(ctx, command.Chat, mutation)
	case "memory-sources":
		result, err = tasks.MemorySources(ctx, command.Run)
	case "memory-preview-run":
		var input struct {
			Scope            harness.MemoryScope
			Kind, Title, Key string
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.PreviewRunMemory(ctx, command.Chat, command.Run, input.Scope, input.Kind, input.Title, input.Key)
	case "memory-preview-copy":
		var input struct {
			Source           harness.MemoryReference
			Scope            harness.MemoryScope
			Kind, Title, Key string
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.PreviewMemoryCopy(ctx, command.Chat, input.Source, input.Scope, input.Kind, input.Title, input.Key)
	case "memory-preview-import":
		var input struct {
			Scope            harness.MemoryScope
			Path, Title, Key string
		}
		if !decode(&input) {
			return
		}
		result, err = tasks.PreviewKnowledgeImport(ctx, command.Chat, input.Scope, input.Path, input.Title, input.Key)
	default:
		err = errors.New("unknown terminal menu action")
	}
	if err != nil {
		server.fail(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, result)
}
