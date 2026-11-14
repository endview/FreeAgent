package harness

import (
	"context"
	"encoding/json"
)

// Keep a bounded recent prefix of actual human inputs verbatim. Retaining a
// user message retains the complete suffix after it, never a protocol half.
func (service *Service) retainRecentUserText(ctx context.Context, run Run, base, through int64) (int64, error) {
	limits, binding, err := service.requestLimits()
	if err != nil {
		return through, err
	}
	window := contextWindow(service.Config, limits, binding, limits.MaxOutputTokens)
	byteBudget, tokenBudget := min(4096, limits.MaxContextBytes/20), 1024
	if window.InputBudget > 0 {
		tokenBudget = min(tokenBudget, window.InputBudget/20)
	}
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,coalesce(run_id,''),body_json FROM `+conversationRecords+` WHERE conversation_id=$1 AND id>$2 AND id<=$3 AND body_json->>'Role'='user' AND coalesce(body_json->>'Deferred','false')='false' ORDER BY id DESC LIMIT 128`, run.ConversationID, base, through)
	if err != nil {
		return through, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		var raw string
		if err = rows.Scan(&m.ID, &m.RunID, &raw); err != nil {
			return through, err
		}
		if json.Unmarshal([]byte(raw), &m.ModelMessage) != nil {
			return through, ErrStoreFormat
		}
		scope := ""
		if service.scopedHistory() {
			scope = service.publicSummaryScope()
		}
		visible, err := contextVisible(m, scope, "", false)
		if err != nil {
			return through, err
		}
		if len(visible) == 0 {
			continue
		}
		tokens := estimateContextTokens(m.Content)
		if len(m.Content) > byteBudget || tokens > tokenBudget {
			break
		}
		byteBudget -= len(m.Content)
		tokenBudget -= tokens
		through = m.ID - 1
	}
	return through, rows.Err()
}
