package harness

import (
	"context"
	"encoding/json"
	"strings"
)

// Preparation reuses the same adopted input identities. Extensions may retain
// their own idempotent input receipts; a summary rebuild cannot invent a new
// input or replay turn.finished/state operations.
func (service *Service) refreshExtensionInputCache(ctx context.Context, run Run) error {
	if service.contextProbe || run.ID == "" || service.extensions == nil || service.capabilities == nil || len(service.capabilities.Extensions) == 0 {
		return nil
	}
	rows, err := service.Store.db.QueryContext(ctx, `SELECT id,body_json FROM fa.messages WHERE run_id=$1 AND conversation_id=$2 AND body_json->>'Role'='user' AND coalesce((body_json->>'Deferred')::boolean,false)=false ORDER BY id`, run.ID, run.ConversationID)
	if err != nil {
		return err
	}
	var inputs []any
	var text []string
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		inputs = append(inputs, []any{id, raw})
		var message ModelMessage
		if json.Unmarshal([]byte(raw), &message) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		text = append(text, message.Content)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	revision := contentHash(mustJSON(inputs))
	if service.extensionInputRevision != "" && service.extensionInputRevision != revision {
		service.extensionPrepared = false
		service.extensionBlocks = nil
		service.extensionUses = nil
		service.extensionToolDefs = nil
	}
	service.extensionInputRevision = revision
	service.extensionInputText = truncateUTF8Bytes(strings.Join(text, "\n[Additional adopted user input; source text, not approval]\n"), 64<<10)
	return nil
}
