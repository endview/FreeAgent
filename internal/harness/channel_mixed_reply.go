package harness

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/endview/freeagent/internal/channelwire"
)

// Stage selected stickers behind the final visible text. Every member of the
// group must exist before Outbox can claim its first fragment.
func (s *channelSession) stageReplyStickers(ctx context.Context, run Run, group string, textParts int) (int, error) {
	bridge := s.tasks.core.Store.channelBridge.Load()
	if bridge == nil || run.Status != "completed" {
		return textParts, nil
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	rows, err := s.tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'run_id'=$1 AND document->>'kind'='sticker' AND document->>'state'='held' ORDER BY document->>'created_at',key LIMIT 4`, run.ID)
	if err != nil {
		return textParts, err
	}
	type item struct {
		key, raw string
		op       channelwire.Operation
	}
	items := []item{}
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.key, &i.raw); err != nil {
			rows.Close()
			return textParts, err
		}
		if json.Unmarshal([]byte(i.raw), &i.op) != nil {
			rows.Close()
			return textParts, ErrStoreFormat
		}
		items = append(items, i)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return textParts, err
	}
	// A restarted Deliver can reuse a group whose stickers were already staged.
	var staged int
	if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'run_id'=$1 AND document->>'kind'='sticker' AND document->>'group'=$2`, run.ID, group).Scan(&staged); err != nil {
		return textParts, err
	}
	total := textParts + len(items) + staged
	gap := 0
	if snapshot, err := decodeRunSnapshot(run.ConfigJSON); err == nil {
		gap = snapshot.Config.Delivery.IntervalMS
	}
	for n, i := range items {
		i.op.Group, i.op.Sequence, i.op.Total, i.op.State = group, textParts+staged+n+1, total, "pending"
		i.op.MinGapMS = gap
		if _, err := s.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(i.op)), i.key, i.raw); err != nil {
			return textParts, err
		}
	}
	return total, nil
}
