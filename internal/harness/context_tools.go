package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type contextTools struct {
	base      ToolRunner
	service   *Service
	runID     string
	attemptID string
}

func contextToolDefinition() ToolDefinition {
	return ToolDefinition{Name: "context_manage", Effect: EffectModify,
		Description: "Inspect, compact, refresh or clear this conversation's context summary. Original history is retained. Compact/refresh use bounded tool-less model calls and existing Run budgets. Any model-proposed change requires user confirmation, including in Full access. A summary is not evidence or authority.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["inspect","compact","refresh","clear"]},"summary_bytes":{"type":"integer","minimum":1,"maximum":16384}},"required":["action"],"additionalProperties":false}`)}
}

func (tools *contextTools) Definitions() []ToolDefinition {
	return append(append([]ToolDefinition(nil), tools.base.Definitions()...), contextToolDefinition())
}

func (tools *contextTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if call.Name != "context_manage" {
		return tools.base.Prepare(ctx, call)
	}
	var arguments struct {
		Action       string `json:"action"`
		SummaryBytes int    `json:"summary_bytes,omitempty"`
	}
	if err := decodeArguments(call.Arguments, &arguments); err != nil {
		return PreparedTool{}, err
	}
	command := ContextCommand{Action: arguments.Action, SummaryBytes: arguments.SummaryBytes}
	run, err := tools.service.Store.GetRun(ctx, tools.runID)
	if err != nil {
		return PreparedTool{}, err
	}
	if command.Action == "inspect" {
		return PreparedTool{Effect: EffectRead, Execute: func(ctx context.Context) (ToolResult, error) {
			state, err := NewTaskService(tools.service).ContextState(ctx, run.ConversationID)
			if err != nil {
				return ToolResult{}, err
			}
			return jsonResult(state)
		}}, nil
	}
	if command.Action != "compact" && command.Action != "refresh" && command.Action != "clear" {
		return PreparedTool{}, errors.New("harness: invalid context action")
	}
	attempts, err := tools.service.Store.ListAttempts(ctx, run.ID)
	if err != nil {
		return PreparedTool{}, err
	}
	for _, attempt := range attempts {
		if attempt.ID == tools.attemptID {
			continue
		}
		if attempt.Status == "unknown" || attempt.Status == "dispatched" {
			return PreparedTool{}, ErrUnknownResult
		}
		if attempt.Status == "pending" {
			return PreparedTool{}, errors.New("harness: a context change must be the only pending tool call")
		}
	}
	var plan compactionPlan
	if command.Action == "clear" {
		plan.command = command
		plan.raw, err = tools.service.Store.summaryRaw(ctx, run.ConversationID)
	} else {
		plan, err = tools.service.planCompaction(ctx, run, command)
	}
	if err != nil {
		return PreparedTool{}, err
	}
	preview := fmt.Sprintf("Context %s: %d source messages, at most %d summary bytes. Original messages are retained. Compact/refresh consume this Run's existing model and time limits. No file, command or network authority is changed.", command.Action, plan.source.Count, plan.limit)
	return PreparedTool{Effect: EffectModify, Preview: preview, userConfirmation: true,
		Items: []ApprovalItem{{Kind: "context", Target: run.ConversationID, Hash: plan.digest(), Fields: map[string]string{"action": command.Action}}},
		Reject: func() ToolResult {
			return ToolResult{Content: "context change declined; original selection retained", IsError: true}
		},
		Execute: func(ctx context.Context) (ToolResult, error) {
			var result ContextResult
			var err error
			if command.Action == "clear" {
				result.Changed, err = tools.service.Store.clearContextSummary(ctx, run.ConversationID, contentHash([]byte(plan.raw)))
			} else {
				current, planErr := tools.service.planCompaction(ctx, run, command)
				if planErr != nil || current.digest() != plan.digest() {
					return ToolResult{}, &CallError{Certain: true, Message: ErrApprovalChanged.Error()}
				}
				result, err = tools.service.applyCompaction(ctx, run, plan, false)
			}
			if err != nil {
				tools.service.compactionFailed = true
				var failure *CallError
				if errors.Is(err, ErrUnknownResult) || errors.As(err, &failure) && !failure.Certain {
					return ToolResult{}, err
				}
				return ToolResult{}, &CallError{Certain: true, Category: "context", Message: safeError(err)}
			}
			result.State, err = NewTaskService(tools.service).ContextState(ctx, run.ConversationID)
			if err != nil {
				return ToolResult{}, err
			}
			// The next request already contains the summary; do not duplicate its
			// body in a tool result. Explicit inspect returns the full body.
			if result.State.Summary != nil {
				result.State.Summary.Text = ""
			}
			return jsonResult(result)
		}}, nil
}
