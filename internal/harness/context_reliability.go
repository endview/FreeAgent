package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func isCompactionAttempt(attempt Attempt) bool {
	var request struct{ Purpose string }
	return attempt.Kind == "model" && json.Unmarshal([]byte(attempt.RequestJSON), &request) == nil && request.Purpose == "compaction"
}

// Only definite, persisted model/content failures can fall back to history.
// In particular an errors.Join containing a storage error cannot be ignored.
func compactionMayFallback(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !compactionMayFallback(child) {
				return false
			}
		}
		return true
	}
	failure, ok := err.(*CallError)
	if !ok || !failure.Certain {
		return false
	}
	switch failure.Category {
	case "output_limit", "rate_limit", "unavailable":
		return true
	case "context":
		return failure.Code == "summary_limit" || failure.Code == "summary_incomplete" || failure.Code == "summary_no_progress"
	}
	return false
}

type compactionSuppression struct {
	Fingerprint string `json:"fingerprint"`
	AttemptID   string `json:"attempt_id"`
	Reason      string `json:"reason"`
}

func (service *Service) compactionSuppressionKey(chat string) string {
	return "context/compaction-suppression/" + chat
}

func (service *Service) compactionFingerprint() (string, error) {
	limits, binding, err := service.requestLimits()
	if err != nil {
		return "", err
	}
	caps := binding.Config.EffectiveCapabilities()
	toggle, effort, err := binding.Config.reasoningParameters(caps.Reasoning.Auxiliary)
	if err != nil {
		return "", err
	}
	return contentHash(mustJSON([]any{"summary-policy/2", binding.ID, binding.BaseURL, binding.Config.Provider,
		binding.Config.Key, binding.Config.Model, caps.Protocol, caps.Reasoning.Control,
		toggle, effort, limits.MaxOutputTokens, service.Config.TaskSummaryBytes})), nil
}

func (service *Service) compactionSuppressed(ctx context.Context, chat string) (string, error) {
	var raw string
	err := service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, service.compactionSuppressionKey(chat)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var saved compactionSuppression
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.Fingerprint == "" {
		return "", ErrStoreFormat
	}
	fingerprint, err := service.compactionFingerprint()
	if err != nil || fingerprint != saved.Fingerprint {
		return "", err
	}
	return saved.Reason, nil
}

func (service *Service) rememberCompactionFailure(ctx context.Context, run Run) (bool, string, error) {
	var id, raw string
	err := service.Store.db.QueryRowContext(ctx, `SELECT id,response_json FROM fa.attempts WHERE run_id=$1 AND kind='model' AND request_doc->>'Purpose'='compaction' ORDER BY seq DESC LIMIT 1`, run.ID).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	var response ModelResponse
	if json.Unmarshal([]byte(raw), &response) != nil {
		return false, "", ErrStoreFormat
	}
	if !emptyOutputLimit(response, response.Failure) {
		return true, "", nil
	}
	reason := "输出额度耗尽，未产生完整摘要；思考用量未知。已暂停同配置的预防性压缩。"
	if response.Usage != nil && response.Usage.ReasoningTokens != nil && *response.Usage.ReasoningTokens > 0 {
		reason = "思考消耗了输出额度，未产生完整摘要。已暂停同配置的预防性压缩。"
	}
	binding, err := service.binding()
	if err != nil {
		return false, "", err
	}
	caps := binding.Config.Capabilities.Reasoning
	if _, _, err := caps.parameters(ReasoningSelection{Mode: "disabled"}, binding.Config.EffectiveCapabilities().Protocol); err == nil && caps.Control != "" && caps.Control != "none" {
		reason += "可回复“把压缩思考关掉”，再确认设置提案；也可从 GUI/CLI 调整辅助思考或输出额度。"
	} else {
		reason += "请从 GUI/CLI 调低辅助思考强度或调整输出额度。"
	}
	fingerprint, err := service.compactionFingerprint()
	if err != nil {
		return false, "", err
	}
	saved := compactionSuppression{fingerprint, id, reason}
	updated, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE fa.meta.document->>'fingerprint' IS DISTINCT FROM excluded.document->>'fingerprint'`, service.compactionSuppressionKey(run.ConversationID), string(mustJSON(saved)))
	if err != nil {
		return false, "", err
	}
	n, err := updated.RowsAffected()
	return n == 1, reason, err
}
