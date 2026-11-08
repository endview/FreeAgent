package harness

import (
	"context"
	"errors"
	"fmt"
)

// prepareContextRequest retains the material produced by this step's hooks and
// attachment reads. A budget rebuild may change history, never rerun a hook.
func (service *Service) prepareContextRequest(ctx context.Context, run Run, request ModelRequest, binding ModelBinding, rebuildOut *func(*ConversationSummary) (ModelRequest, error)) (ModelRequest, error) {
	baseCount := len(request.Messages)
	prepared, err := service.prepareExtensionStep(ctx, run, request)
	if err != nil {
		return request, err
	}
	additions := append([]ModelMessage(nil), prepared.Messages[baseCount:]...)
	uses := prepared.Extensions
	rawMessages := append([]ModelMessage(nil), prepared.Messages...)
	prepared.Messages = append([]ModelMessage(nil), prepared.Messages...)
	prepared, err = service.prepareAttachmentRequest(ctx, run, binding.Config, prepared)
	if err != nil {
		return prepared, err
	}
	cache := make(map[string]ModelMessage, len(rawMessages))
	for i, m := range rawMessages {
		cache[contentHash(mustJSON(m))] = prepared.Messages[i]
	}
	rebuild := func(summary *ConversationSummary) (ModelRequest, error) {
		next, err := service.buildContextWithSummary(ctx, run, summary)
		if err != nil {
			return next, err
		}
		next.Extensions = uses
		next.Reasoning = request.Reasoning
		next.Messages = append(next.Messages, additions...)
		for i, m := range next.Messages {
			if saved, ok := cache[contentHash(mustJSON(m))]; ok {
				next.Messages[i] = saved
			} else if len(m.Attachments) != 0 {
				return next, ErrConfigChanged
			}
		}
		return next, nil
	}
	*rebuildOut = rebuild
	usage, err := service.requestUsage(run, prepared)
	if err != nil {
		return prepared, err
	}
	if err := service.recordContextUsage(ctx, run, usage); err != nil {
		return prepared, err
	}
	if service.analysisOnly {
		*rebuildOut = nil
		return prepared, usage.budgetError()
	}
	if service.Config.TaskSummaryBytes <= 0 || service.compactionFailed || run.ID == "" || usage.Occupancy < autoCompactPercent(service.Config) {
		return prepared, usage.budgetError()
	}
	// A fixed component cannot be made smaller by summarizing other history.
	var budget *ContextBudgetError
	if errors.As(usage.budgetError(), &budget) && budget.Code != "history_context_full" {
		return prepared, budget
	}
	plan, err := service.planCompactionFor(ctx, run, ContextCommand{Action: "compact"}, true)
	if err != nil {
		return prepared, err
	}
	var compacted ModelRequest
	result, err := service.applyCompaction(ctx, run, plan, true, func(summary *ConversationSummary) error {
		var err error
		compacted, err = rebuild(summary)
		if err != nil {
			return err
		}
		after, err := service.requestUsage(run, compacted)
		if err != nil {
			return err
		}
		if err := after.budgetError(); err != nil {
			return err
		}
		if after.Bytes >= usage.Bytes && after.EstimatedTokens >= usage.EstimatedTokens {
			return fmt.Errorf("%w: compaction made no effective progress; previous summary retained", ErrContextLimit)
		}
		return nil
	})
	if err != nil {
		service.compactionFailed = true
		service.event(Event{Kind: EventContextCompactionFailed, ConversationID: run.ConversationID, RunID: run.ID, Message: safeError(err)})
		return prepared, err
	}
	if !result.Changed {
		return prepared, usage.budgetError()
	}
	return compacted, nil
}
