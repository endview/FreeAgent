package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	controlReadTool = "control_read_state"
	controlPlanTool = "control_plan_change"
)

func isControlToolName(name string) bool {
	return name == controlReadTool || name == controlPlanTool
}

func controlResponseError(response ModelResponse, definitions []ToolDefinition) *CallError {
	available := map[string]bool{}
	for _, definition := range definitions {
		available[definition.Name] = true
	}
	proposalCount := 0
	for _, call := range response.ToolCalls {
		if !isControlToolName(call.Name) {
			continue
		}
		if !available[call.Name] {
			return &CallError{Certain: true, Category: "control", Code: "control_tool_unavailable",
				Message: "control tool is unavailable to this Run", Retryable: false}
		}
		if call.Name == controlPlanTool {
			proposalCount++
		}
	}
	if proposalCount > 0 && (len(response.ToolCalls) != 1 || proposalCount != 1) {
		return &CallError{Certain: true, Category: "control", Code: "control_call_not_exclusive",
			Message: "a control proposal must be the only ToolCall in a model response", Retryable: false}
	}
	return nil
}

type controlTools struct {
	base      ToolRunner
	service   *Service
	control   *ControlService
	runID     string
	attemptID string
}

func (tools *controlTools) Definitions() []ToolDefinition {
	if tools == nil || tools.service == nil || !tools.service.controlToolsEnabled() {
		return nil
	}
	definitions := tools.controlDefinitions()
	if tools.base == nil {
		return definitions
	}
	return append(append([]ToolDefinition(nil), tools.base.Definitions()...), definitions...)
}

func (tools *controlTools) controlDefinitions() []ToolDefinition {
	if tools == nil || tools.service == nil || !tools.service.controlToolsEnabled() {
		return nil
	}
	catalog, _ := json.Marshal(ControlOperationCatalog())
	return []ToolDefinition{
		{Name: controlReadTool, Effect: EffectRead,
			Description: "Read the bounded current FreeAgent control state. Paths, secrets, raw provider arguments and unrelated workspace contents are redacted.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: controlPlanTool, Effect: EffectModify,
			Description: "Prepare one typed persistent FreeAgent setting change. It creates a pending proposal only; it never confirms or applies the change. Available operations: " + string(catalog),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"arguments":{"type":"object"}},"required":["kind","arguments"],"additionalProperties":false}`)},
	}
}

func (tools *controlTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if tools == nil || tools.base == nil || tools.service == nil || tools.control == nil || !tools.service.controlToolsEnabled() {
		return PreparedTool{}, ErrToolDisabled
	}
	switch call.Name {
	case controlReadTool:
		if len(call.Arguments) != 0 {
			var ignored map[string]any
			if err := decodeControlJSON(call.Arguments, &ignored); err != nil || len(ignored) != 0 {
				return PreparedTool{}, errors.New("harness: control state takes no arguments")
			}
		}
		return PreparedTool{Effect: EffectRead, Execute: func(ctx context.Context) (ToolResult, error) {
			state, err := tools.control.ReadControlStateForModel(ctx)
			if err != nil {
				return ToolResult{}, err
			}
			raw, err := json.Marshal(state)
			if err != nil || len(raw) > tools.service.limits().MaxToolResult {
				return ToolResult{}, ErrContextLimit
			}
			return ToolResult{Content: string(raw)}, nil
		}}, nil
	case controlPlanTool:
		if tools.attemptID == "" || tools.runID == "" {
			return PreparedTool{}, errors.New("harness: control proposal requires a foreground Run attempt")
		}
		var args struct {
			Kind      string          `json:"kind"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := decodeControlJSON(call.Arguments, &args); err != nil || args.Kind == "" || len(args.Arguments) == 0 || !json.Valid(args.Arguments) {
			return PreparedTool{}, errors.New("harness: control proposal requires one typed kind and JSON arguments")
		}
		if existing, ok := tools.control.operationForAttempt(tools.attemptID); ok {
			return tools.preparedProposal(existing.Operation), nil
		}
		run, err := tools.service.Store.GetRun(ctx, tools.runID)
		if err != nil {
			return PreparedTool{}, err
		}
		operation, err := tools.control.PlanControlChange(ctx, ControlPlanRequest{Kind: args.Kind, Arguments: args.Arguments,
			Authorization: ControlModelProposed, ConversationID: run.ConversationID})
		if err != nil {
			return PreparedTool{}, err
		}
		if err := tools.control.bindAttempt(operation.ID, tools.attemptID); err != nil {
			return PreparedTool{}, err
		}
		return tools.preparedProposal(operation), nil
	default:
		return tools.base.Prepare(ctx, call)
	}
}

func (tools *controlTools) preparedProposal(operation ControlOperation) PreparedTool {
	content := fmt.Sprintf("control proposal %s is pending user confirmation; no setting has been applied. The change takes effect only in a new Run.", operation.ID)
	return PreparedTool{Effect: EffectModify, Preview: operation.Preview, controlProposal: true,
		controlOperationID: operation.ID, Items: []ApprovalItem{{Kind: "control_proposal", Target: operation.ID,
			Fields: map[string]string{"kind": operation.Kind, "risk": string(operation.Risk), "status": string(operation.Status)}}},
		Execute: func(context.Context) (ToolResult, error) { return ToolResult{Content: content}, nil },
		Reject: func() ToolResult {
			return ToolResult{Content: "control proposal was declined; no setting was changed", IsError: true}
		}}
}
