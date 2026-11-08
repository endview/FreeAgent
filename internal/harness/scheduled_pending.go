package harness

import (
	"context"
	"encoding/json"
)

// Pending admission/revocation must not use the newest history page: many
// newer terminal records must never hide an older active opportunity.
func (tasks *TaskService) pendingWakes(ctx context.Context) ([]WakeOpportunity, error) {
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT value FROM fa.meta WHERE starts_with(key,$1) AND document->>'state' IN ('queued','generating','generated','delivery_queued') ORDER BY document->>'created_at',key LIMIT 65`, wakePrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WakeOpportunity{}
	for rows.Next() {
		var raw []byte
		var o WakeOpportunity
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &o) != nil {
			return nil, ErrStoreFormat
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
