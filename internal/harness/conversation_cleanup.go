package harness

import (
	"context"
	"database/sql"
)

// Explicit ownership, not a search for IDs inside arbitrary JSON. Keep this
// inventory aligned with docs/CONVERSATION_MANAGEMENT.md when adding a cache.
func discardConversationCaches(ctx context.Context, tx *sql.Tx, chat string) error {
	for _, family := range []string{summaryMetaPrefix, maintenanceMetaPrefix, "context/usage/", "context/compaction-suppression/", "input/", "chat/memory/", "chat/pin/", "chat/model/", "chat/reasoning/", "chat/agent/", "chat/collaboration/", capabilityMetaPrefix, "branch/request/"} {
		prefix := family + chat
		if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1 OR starts_with(key,$2)`, prefix, prefix+"/"); err != nil {
			return err
		}
	}
	// These caches are keyed by a Run, so remove them before deleting ordinary
	// Runs. Transport claims remain independently authoritative.
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta m USING fa.runs r WHERE r.conversation_id=$1 AND (
 m.key='channel/reply-parts/'||r.id OR m.key='context/omission/'||r.id OR starts_with(m.key,'context/repair/'||r.id||'/')
 OR m.key LIKE 'channel/pending-reply/%' AND m.document->>'run_id'=r.id
 OR m.key LIKE 'channel/render/%' AND split_part(m.key,'/',5)='reply' AND split_part(m.key,'/',6)=r.id
 OR m.key LIKE 'attachment-reply/%' AND m.document->>'Run'=r.id)`, chat); err != nil {
		return err
	}
	// Known terminal delivery bodies are redundant after messages are purged.
	// UNKNOWN retains its exact original body for late reconciliation.
	if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document-'text'-'entities'-'buttons'-'media'-'data')::text WHERE key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 AND document->>'state' IN ('sent','failed','cancelled')`, chat); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM fa.meta m WHERE m.key LIKE 'command/process/%' AND m.document->>'conversation_id'=$1 AND m.document->>'state' IN ('completed','failed','stopped','timed_out') AND NOT EXISTS(SELECT 1 FROM fa.meta u WHERE u.key='attempt/unresolved/'||(m.document->>'id')) AND NOT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.id=m.document->>'id' AND a.status IN ('unknown','dispatched'))`, chat)
	return err
}
