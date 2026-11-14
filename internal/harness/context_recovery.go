package harness

import (
	"context"
	"encoding/json"
	"errors"
)

// Every automatic caller uses this boundary. Trigger identifies the decision,
// never selects another audience or grants permission to replay an operation.
func (service *Service) recoverContext(ctx context.Context, run Run, trigger string, validate ...func(*ConversationSummary) error) (ContextResult, error) {
	paused, err := service.compactionSuppressed(ctx, run.ConversationID)
	if err != nil {
		return ContextResult{}, err
	}
	var saved compactionSuppression
	if paused != "" {
		var raw string
		if err = service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, service.compactionSuppressionKey(run.ConversationID)).Scan(&raw); err != nil {
			return ContextResult{}, err
		}
		if json.Unmarshal([]byte(raw), &saved) != nil {
			return ContextResult{}, ErrStoreFormat
		}
		if saved.Failure != "source" {
			return ContextResult{}, &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: paused}
		}
	}
	plan, err := service.planCompactionFor(ctx, run, ContextCommand{Action: "compact"}, true)
	if err != nil {
		return ContextResult{}, err
	}
	semanticDigest, err := service.preflightContextPrefix(ctx, run, plan.base.Through, plan.source.Through)
	if err != nil {
		return ContextResult{}, err
	}
	plan.validatedSource = semanticDigest
	if paused != "" && saved.SourceDigest == semanticDigest {
		return ContextResult{}, &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: paused}
	}
	result, err := service.applyCompaction(ctx, run, plan, true, validate...)
	if err == nil && !result.Changed && result.ModelCalls > 0 {
		err = &CallError{Certain: true, Category: "context", Code: "summary_no_progress", Message: "compaction made no effective progress; prior summary retained"}
	}
	var failure *CallError
	if errors.As(err, &failure) && failure.Certain && failure.Code == "summary_no_progress" {
		fingerprint, e := service.compactionFingerprint()
		if e != nil {
			return result, errors.Join(err, e)
		}
		record := compactionSuppression{Fingerprint: fingerprint, Failure: "source", SourceDigest: semanticDigest, Reason: "压缩未产生有效收益；相同源和配置不再重复调用，新的可压缩内容或配置变化后重新判断。"}
		_, e = service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, service.compactionSuppressionKey(run.ConversationID), string(mustJSON(record)))
		if e != nil {
			return result, errors.Join(err, e)
		}
		service.event(Event{Kind: EventContextCompactionFailed, RunID: run.ID, ConversationID: run.ConversationID, Message: record.Reason, Activity: map[string]any{"trigger": trigger, "reason_code": "summary_no_progress"}})
	}
	if err != nil && compactionMayFallback(err) {
		notify, reason, e := service.rememberCompactionFailure(ctx, run, err)
		if e != nil {
			return result, errors.Join(err, e)
		}
		if notify {
			service.event(Event{Kind: EventContextCompactionFailed, RunID: run.ID, ConversationID: run.ConversationID, Message: reason, Activity: map[string]any{"trigger": trigger}})
		}
	}
	return result, err
}
