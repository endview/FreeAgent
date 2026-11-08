package harness

import (
	"encoding/json"
)

// Optional extensions can select one already-authorized reply resource. The
// existing channel tool validates scope, revocation and confirmation policy;
// an extension callback never acquires arbitrary tool authority.
func (host *extensionHost) handleReplyAction(inv *extensionInvocation, params map[string]json.RawMessage) (any, error) {
	if !inv.finished || inv.run.Status != "completed" || !inv.manifest.Delivery || inv.service == nil || !inv.service.replyExpressionsEnabled() {
		return nil, errExtensionUnavailable
	}
	var action struct {
		Action   string `json:"action"`
		Resource string `json:"resource,omitempty"`
		Emoji    string `json:"emoji,omitempty"`
	}
	if decodeControlJSON(params["action"], &action) != nil || action.Action != "sticker" && action.Action != "reaction" {
		return nil, errExtensionUnavailable
	}
	raw := mustJSON(action)
	key := "extension/reply-action/" + inv.run.ID + "/" + inv.extensionID + "/" + action.Action
	store := inv.service.Store
	prepared, err := (replyExpressionTools{service: inv.service, runID: inv.run.ID, attemptID: "ext/" + inv.extensionID + "/" + action.Action}).Prepare(inv.ctx, ToolCall{Name: "reply_expression", Arguments: raw})
	if err != nil || prepared.userConfirmation {
		return nil, errExtensionUnavailable
	}
	claim := string(mustJSON(map[string]any{"state": "entered", "digest": contentHash(raw)}))
	r, err := store.db.ExecContext(inv.ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, claim)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return map[string]any{"state": "previously_entered", "replayed": false}, nil
	}
	result, err := prepared.Execute(inv.ctx)
	if err != nil {
		return nil, err
	}
	// The durable channel operation remains authoritative even if this audit
	// update fails; the claim above prohibits executing the selection again.
	_, _ = store.db.ExecContext(inv.ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(map[string]any{"state": "settled", "digest": contentHash(raw), "result": result})), key)
	return result, nil
}
