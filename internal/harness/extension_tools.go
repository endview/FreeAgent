package harness

import (
	"context"
	"encoding/json"
	"errors"
)

// extensionToolRunner adapts extension tools to the existing ToolRunner
// boundary. It owns no execution state; dispatch and receipts remain in
// Service.executeTool.
type extensionToolRunner struct {
	base    ToolRunner
	service *Service
	host    *extensionHost
	runID   string
	tools   []extensionRuntimeTool
}

func (tools *extensionToolRunner) Definitions() []ToolDefinition {
	definitions := append([]ToolDefinition(nil), tools.base.Definitions()...)
	for _, tool := range tools.tools {
		definitions = append(definitions, tool.Definition)
	}
	return definitions
}

func (tools *extensionToolRunner) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	for _, tool := range tools.tools {
		if tool.Definition.Name != call.Name {
			continue
		}
		if len(call.Arguments) > 64<<10 || tools.host.redact(string(call.Arguments)) != string(call.Arguments) {
			return PreparedTool{}, errors.New("invalid or secret-bearing extension arguments")
		}
		var object map[string]any
		if json.Unmarshal(call.Arguments, &object) != nil || object == nil || tool.Input == nil ||
			tool.Input.Validate(object) != nil {
			return PreparedTool{}, errors.New("extension arguments do not satisfy the input schema")
		}
		return PreparedTool{
			Effect:  tool.Definition.Effect,
			Preview: tool.ExtensionID + "/" + tool.Name + "\n" + string(call.Arguments),
			Items: []ApprovalItem{{Kind: "extension", Target: tool.ExtensionID + "/" + tool.Name,
				Fields: map[string]string{"arguments": string(call.Arguments)}}},
			Execute: func(parent context.Context) (ToolResult, error) {
				if err := parent.Err(); err != nil {
					return ToolResult{}, err
				}
				run, err := tools.service.Store.GetRun(parent, tools.runID)
				if err != nil {
					return ToolResult{}, err
				}
				if tool.Workflow != "" {
					history, err := tools.service.Store.ListMessages(parent, run.ConversationID)
					if err != nil {
						return ToolResult{}, err
					}
					output, activity, err := tools.host.workflow(parent, tool.ExtensionID, tool.Workflow,
						run, object, modelHistory(history), tools.service)
					if len(activity) != 0 {
						summary, _ := activity["summary"].(string)
						if summary == "" {
							summary = "workflow activity"
						}
						tools.service.event(Event{Kind: "extension_activity", Message: summary,
							Activity: activity, RunID: run.ID, ConversationID: run.ConversationID})
					}
					if err != nil {
						var callErr *CallError
						if !errors.As(err, &callErr) {
							callErr = &CallError{Message: safeError(err)}
						}
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
							workflowOutcomeUnknown(err) {
							callErr.Certain = false
						}
						return ToolResult{}, callErr
					}
					return ToolResult{Content: output, Extensions: []ExtensionUse{{
						Source: "extension", Name: tool.ExtensionID, BlockID: tool.Workflow,
					}}}, nil
				}
				return tools.host.callTool(parent, tool, tools.service, run, call.Arguments)
			},
		}, nil
	}
	return tools.base.Prepare(ctx, call)
}

func (service *Service) extensionToolRunner(base ToolRunner, runID string) ToolRunner {
	if service == nil || service.extensions == nil || len(service.extensionToolDefs) == 0 {
		return base
	}
	return &extensionToolRunner{base: base, service: service, host: service.extensions,
		runID: runID, tools: append([]extensionRuntimeTool(nil), service.extensionToolDefs...)}
}
