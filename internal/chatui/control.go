package chatui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

const controlPrincipal = "chat-ui"

func (server *Server) controlCapability(conversationID string) (harness.InteractiveControlCapability, error) {
	control, err := server.tasks.Control()
	if err != nil {
		return harness.InteractiveControlCapability{}, err
	}
	return control.IssueInteractiveCapability(server.ctx, conversationID, controlPrincipal)
}

// confirmControlProposal decides a model-proposed control change through the
// Run that is waiting on it. The interactive caller submits the code it was
// shown by /api/control/confirmation; rejection needs no code.
func (server *Server) confirmControlProposal(ctx context.Context, chat, runID string, confirmation harness.Confirmation, code string, approved bool) (harness.RunResult, error) {
	capability, err := server.controlCapability(chat)
	if err != nil {
		return harness.RunResult{}, err
	}
	result, err := server.tasks.ConfirmControl(ctx, runID, confirmation, harness.ControlAuthorization{
		Source: harness.ControlModelProposed, ConversationID: chat, Principal: controlPrincipal,
		Code: code, Capability: capability,
	}, approved)
	if err != nil || !approved {
		return result, err
	}
	// The applied operation is persisted in the core; discovery only carries
	// the new workspace as a reconnect hint for an attached desktop.
	if control, controlErr := server.tasks.Control(); controlErr == nil {
		if operations, listErr := control.ListControlOperations(ctx); listErr == nil {
			for _, operation := range operations {
				if operation.ID == confirmation.ControlOperationID && operation.Status == harness.ControlApplied &&
					operation.Kind == harness.ControlSetWorkspace {
					var workspace string
					if json.Unmarshal([]byte(operation.ProposedValues["config.workspace"]), &workspace) == nil {
						_ = handoff.UpdateWorkspace(server.tasks.DataDir(), workspace)
					}
				}
			}
		}
	}
	return result, nil
}

// applyDirectControl is the GUI's user-direct path. The GUI gesture is the
// user interaction; the code is rendered and consumed inside this trusted
// surface so settings PATCH handlers cannot bypass the owner journal.
func (server *Server) applyDirectControl(ctx context.Context, generation, conversationID, kind string, arguments any) (harness.ControlResult, error) {
	if generation != "" && generation != server.tasks.RuntimePolicy().Generation {
		return harness.ControlResult{}, harness.ErrGenerationConflict
	}
	control, err := server.tasks.Control()
	if err != nil {
		return harness.ControlResult{}, err
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return harness.ControlResult{}, err
	}
	operation, err := control.PlanControlChange(ctx, harness.ControlPlanRequest{
		Kind: kind, Arguments: raw, Authorization: harness.ControlUserDirect, ConversationID: conversationID,
	})
	if err != nil {
		return harness.ControlResult{}, err
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	confirmation, err := control.RenderConfirmation(ctx, operation.ID, conversationID, controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	result, err := control.ConfirmControlChange(ctx, operation.ID, harness.ControlAuthorization{
		Source: harness.ControlUserDirect, ConversationID: conversationID, Principal: controlPrincipal,
		Code: confirmation.Code, Capability: capability,
	})
	if err != nil {
		return result, err
	}
	if !result.Applied {
		return result, errors.New("chat: direct control change was not applied")
	}
	if kind == harness.ControlSetWorkspace {
		var workspace string
		if json.Unmarshal([]byte(operation.ProposedValues["config.workspace"]), &workspace) == nil {
			_ = handoff.UpdateWorkspace(server.tasks.DataDir(), workspace)
		}
	}
	return result, nil
}

func (server *Server) applyDirectKeyControl(ctx context.Context, request harness.KeyControlRequest) (harness.ControlResult, error) {
	control, err := server.tasks.Control()
	if err != nil {
		return harness.ControlResult{}, err
	}
	operation, err := control.PlanKeyProfileChange(ctx, request)
	if err != nil {
		return harness.ControlResult{}, err
	}
	capability, err := control.IssueInteractiveCapability(ctx, "", controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	confirmation, err := control.RenderConfirmation(ctx, operation.ID, "", controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	return control.ConfirmControlChange(ctx, operation.ID, harness.ControlAuthorization{
		Source: harness.ControlUserDirect, Principal: controlPrincipal,
		Code: confirmation.Code, Capability: capability,
	})
}

func (server *Server) applyDirectKeyProbe(ctx context.Context, ref string) (harness.ProbeResult, error) {
	control, err := server.tasks.Control()
	if err != nil {
		return harness.ProbeResult{}, err
	}
	return control.ProbeKey(ctx, ref, controlPrincipal)
}

func (server *Server) applyDirectMCPControl(ctx context.Context, request harness.MCPControlRequest) (harness.ControlResult, error) {
	control, err := server.tasks.Control()
	if err != nil {
		return harness.ControlResult{}, err
	}
	operation, err := control.PlanMCPProfileChange(ctx, request)
	if err != nil {
		return harness.ControlResult{}, err
	}
	capability, err := control.IssueInteractiveCapability(ctx, "", controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	confirmation, err := control.RenderConfirmation(ctx, operation.ID, "", controlPrincipal)
	if err != nil {
		return harness.ControlResult{}, err
	}
	return control.ConfirmControlChange(ctx, operation.ID, harness.ControlAuthorization{
		Source: harness.ControlUserDirect, Principal: controlPrincipal,
		Code: confirmation.Code, Capability: capability,
	})
}

func (server *Server) controlAPI(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if !strings.HasPrefix(path, "/api/control") {
		return false
	}
	control, err := server.tasks.Control()
	if err != nil {
		server.fail(writer, err)
		return true
	}
	switch {
	case path == "/api/control/config" && request.Method == http.MethodGet:
		server.writeJSON(writer, http.StatusOK, server.configProjection())
		return true
	case path == "/api/control/state" && request.Method == http.MethodGet:
		state, err := control.ReadControlState(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, state)
		return true
	case path == "/api/control/catalog" && request.Method == http.MethodGet:
		server.writeJSON(writer, http.StatusOK, harness.ControlOperationCatalog())
		return true
	case path == "/api/control/operations" && request.Method == http.MethodGet:
		operations, err := control.ListControlOperations(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, operations)
		return true
	case path == "/api/control/plan" && request.Method == http.MethodPost:
		var body struct {
			Kind              string            `json:"kind"`
			Arguments         json.RawMessage   `json:"arguments"`
			ConversationID    string            `json:"conversation_id"`
			ExpectedRevisions map[string]uint64 `json:"expected_revisions,omitempty"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		operation, err := control.PlanControlChange(request.Context(), harness.ControlPlanRequest{
			Kind: body.Kind, Arguments: body.Arguments, Authorization: harness.ControlUserDirect,
			ConversationID: body.ConversationID, ExpectedRevisions: body.ExpectedRevisions,
		})
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusAccepted, operation)
		return true
	case path == "/api/control/key-plan" && request.Method == http.MethodPost:
		var body struct {
			Ref           string `json:"ref"`
			Remove        bool   `json:"remove"`
			Force         bool   `json:"force"`
			DisableModels bool   `json:"disable_models"`
			NewDefault    string `json:"new_default"`
			Name          string `json:"name"`
			BaseURL       string `json:"base_url"`
			Protocol      string `json:"protocol"`
			Note          string `json:"note"`
			Source        string `json:"source"`
			EnvName       string `json:"env_name"`
			Secret        string `json:"secret"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		secret := []byte(body.Secret)
		body.Secret = ""
		defer clear(secret)
		result, err := server.applyDirectKeyControl(request.Context(), harness.KeyControlRequest{
			Ref: body.Ref, Remove: body.Remove, Force: body.Force, DisableModels: body.DisableModels,
			NewDefault: body.NewDefault, Input: harness.KeyInput{Name: body.Name, BaseURL: body.BaseURL,
				Protocol: body.Protocol, Note: body.Note, Source: body.Source, EnvName: body.EnvName, Secret: secret},
		})
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, result)
		return true
	case path == "/api/control/key-probe" && request.Method == http.MethodPost:
		var body struct {
			Ref string `json:"ref"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		result, err := server.applyDirectKeyProbe(request.Context(), body.Ref)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, result)
		return true
	case path == "/api/control/mcp-reentry" && request.Method == http.MethodPost:
		var body struct {
			OperationID string                  `json:"operation_id"`
			ProfileID   string                  `json:"profile_id"`
			Config      harness.MCPServerConfig `json:"config"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		operation, err := control.ReenterMCPProfile(request.Context(), body.OperationID, harness.MCPControlRequest{ID: body.ProfileID, Config: body.Config})
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, operation)
		return true
	case path == "/api/control/confirmation" && request.Method == http.MethodGet:
		operationID, conversationID := request.URL.Query().Get("operation_id"), request.URL.Query().Get("conversation_id")
		_, err = server.controlCapability(conversationID)
		if err == nil {
			confirmation, confirmationErr := control.RenderConfirmation(request.Context(), operationID, conversationID, controlPrincipal)
			if confirmationErr == nil {
				server.writeJSON(writer, http.StatusOK, confirmation)
				return true
			}
			err = confirmationErr
		}
		server.fail(writer, err)
		return true
	case path == "/api/control/confirm" && request.Method == http.MethodPost:
		var body struct {
			OperationID    string `json:"operation_id"`
			ConversationID string `json:"conversation_id"`
			Code           string `json:"code"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		capability, err := server.controlCapability(body.ConversationID)
		if err == nil {
			result, applyErr := control.ConfirmControlChange(request.Context(), body.OperationID, harness.ControlAuthorization{
				Source: harness.ControlUserDirect, ConversationID: body.ConversationID, Principal: controlPrincipal,
				Code: body.Code, Capability: capability,
			})
			if applyErr == nil {
				if result.Operation.Kind == harness.ControlSetWorkspace {
					var workspace string
					if json.Unmarshal([]byte(result.Operation.ProposedValues["config.workspace"]), &workspace) == nil {
						_ = handoff.UpdateWorkspace(server.tasks.DataDir(), workspace)
					}
				}
				server.writeJSON(writer, http.StatusOK, result)
				return true
			}
			err = applyErr
		}
		server.fail(writer, err)
		return true
	case path == "/api/control/reject" && request.Method == http.MethodPost:
		var body struct {
			OperationID    string `json:"operation_id"`
			ConversationID string `json:"conversation_id"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		capability, err := server.controlCapability(body.ConversationID)
		if err == nil {
			result, rejectErr := control.RejectControlChange(request.Context(), body.OperationID, harness.ControlAuthorization{
				Source: harness.ControlUserDirect, ConversationID: body.ConversationID, Principal: controlPrincipal,
				Capability: capability,
			})
			if rejectErr == nil {
				server.writeJSON(writer, http.StatusOK, result)
				return true
			}
			err = rejectErr
		}
		server.fail(writer, err)
		return true
	case path == "/api/control/revert-plan" && request.Method == http.MethodPost:
		var body struct {
			OperationID    string `json:"operation_id"`
			ConversationID string `json:"conversation_id"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		operation, err := control.PlanRevertControlChange(request.Context(), body.OperationID, harness.ControlUserDirect, body.ConversationID)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusAccepted, operation)
		return true
	case path == "/api/control/review" && request.Method == http.MethodPost:
		if err := control.ReviewExternalChange(request.Context()); err != nil {
			server.fail(writer, err)
			return true
		}
		state, err := control.ReadControlState(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, state)
		return true
	default:
		if request.Method == http.MethodGet || request.Method == http.MethodPost {
			http.NotFound(writer, request)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
		return true
	}
}

// configProjection is the diagnostic shape used by the CLI's config show.
// It is narrower than Config so an attached observer cannot read prompts,
// MCP arguments, environment values or provider secrets.
func (server *Server) configProjection() map[string]any {
	config := server.tasks.Config()
	enabled := make([]string, 0)
	for name, item := range config.MCPServers {
		if item.Enabled {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	capabilities := make(map[string]harness.ModelCapabilities, len(config.Models))
	for id, profile := range config.Models {
		capabilities[id] = profile.EffectiveCapabilities()
	}
	return map[string]any{
		"ok": true, "config_path": filepath.Join(server.tasks.DataDir(), "config.json"),
		"workspace": config.Workspace, "models": config.Models, "default_model": config.DefaultModel,
		"default_selection": config.DefaultSelection, "auto": config.Auto,
		"enabled_mcp_servers": enabled, "instructions": config.Instructions,
		"model_capabilities": capabilities, "commands": config.Commands, "http": config.HTTP,
		"collaboration": config.Collaboration, "memory": config.Memory,
		"limits": map[string]int{
			"max_steps": config.MaxSteps, "max_messages": config.MaxMessages,
			"max_context_bytes": config.MaxContextBytes, "max_output_tokens": config.MaxOutputTokens,
			"max_run_seconds": config.MaxRunSeconds, "max_tool_result_bytes": config.MaxToolResult,
			"task_summary_bytes": config.TaskSummaryBytes,
		},
		"changed": false, "network_tested": false, "credentials_resolved": false,
	}
}
