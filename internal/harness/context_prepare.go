package harness

import (
	"context"
	"errors"
	"time"
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
	if reason, err := service.compactionSuppressed(ctx, run.ConversationID); err != nil || reason != "" {
		if err != nil {
			return prepared, err
		}
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
	started := time.Now()
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
			return &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: "generated summary does not fit the current context; previous summary retained"}
		}
		if after.Bytes >= usage.Bytes && after.EstimatedTokens >= usage.EstimatedTokens {
			return &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: "compaction made no effective progress; previous summary retained"}
		}
		return nil
	})
	if err == nil && !result.Changed && result.ModelCalls > 0 {
		err = &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: "summary did not reduce context occupancy; previous summary retained"}
	}
	if err != nil {
		service.compactionFailed = true
		if !compactionMayFallback(err) {
			service.event(Event{Kind: EventContextCompactionFailed, ConversationID: run.ConversationID, RunID: run.ID, Message: safeError(err)})
			return prepared, err
		}
		if ctx.Err() != nil {
			return prepared, ctx.Err()
		}
		if checkErr := service.checkCurrentPermission(ctx, run); checkErr != nil {
			return prepared, checkErr
		}
		if checkErr := service.checkDispatchConfig(run, true); checkErr != nil {
			return prepared, checkErr
		}
		current, loadErr := service.Store.GetRun(ctx, run.ID)
		if loadErr != nil {
			return prepared, loadErr
		}
		if current.Status != "running" {
			return prepared, errors.Join(err, ErrActiveRun)
		}
		if _, adoptErr := service.adoptSteering(ctx, run); adoptErr != nil {
			return prepared, adoptErr
		}
		summary, _, loadErr := service.Store.contextSummary(ctx, run.ConversationID, false)
		if loadErr != nil {
			return prepared, loadErr
		}
		if summary != nil && service.scopedHistory() && summary.Scope != service.publicSummaryScope() {
			summary = nil
		}
		fallback, rebuildErr := rebuild(summary)
		if rebuildErr != nil {
			return prepared, rebuildErr
		}
		after, usageErr := service.requestUsage(run, fallback)
		if usageErr != nil {
			return prepared, usageErr
		}
		if recordErr := service.recordContextUsage(ctx, run, after); recordErr != nil {
			return prepared, recordErr
		}
		notify, reason, recordErr := service.rememberCompactionFailure(ctx, run, err)
		if recordErr != nil {
			return prepared, recordErr
		}
		message := "压缩未完成，沿用原上下文继续；失败证据已保留。"
		if reason != "" {
			message += reason
		}
		budgetErr := after.budgetError()
		if budgetErr != nil {
			message = "压缩未完成，当前请求超过上下文硬预算。请打开上下文面板，核对摘要上限、模型窗口和占用来源；保存后手动压缩，再继续。" + reason
		}
		if notify {
			service.event(Event{Kind: EventContextCompactionFailed, ConversationID: run.ConversationID, RunID: run.ID, Message: message,
				Activity: map[string]any{"model_calls": result.ModelCalls, "elapsed_ms": time.Since(started).Milliseconds(), "continued": budgetErr == nil}})
		}
		if budgetErr != nil {
			return fallback, errors.Join(budgetErr, err)
		}
		return fallback, nil
	}
	if !result.Changed {
		return prepared, usage.budgetError()
	}
	return compacted, nil
}
