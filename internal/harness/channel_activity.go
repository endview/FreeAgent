package harness

import (
	"context"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

// Activities exposes current, authorized processing as expiring hints. No
// activity is written to outbox/history, and old hints cannot survive an Owner.
func (b *ChannelBridge) Activities(ctx context.Context, id string) ([]channelwire.Activity, error) {
	b.mu.Lock()
	c := b.connections[id]
	if c == nil {
		b.mu.Unlock()
		return nil, ErrPermissionDenied
	}
	config := c.config
	b.mu.Unlock()
	if config.Source != SpaceSourceTelegram || config.DisableTyping {
		return []channelwire.Activity{}, nil
	}
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE status='running' ORDER BY created_at DESC,id DESC LIMIT 16`)
	if err != nil {
		return nil, err
	}
	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var result []channelwire.Activity
	seen := map[ChannelTarget]bool{}
	for _, run := range runs {
		origin := channelOriginFromJSON(run.ConfigJSON)
		if origin == nil || origin.Source != config.Source || origin.ApplicationID != config.ApplicationID || seen[origin.ChannelTarget] {
			continue
		}
		if err := b.tasks.checkTelegramRun(ctx, run); err != nil {
			continue
		}
		space, err := b.tasks.ChannelSpace(ctx, origin.ChannelTarget)
		if err != nil || space.ChatKind != "private" {
			continue
		}
		seen[origin.ChannelTarget] = true
		result = append(result, channelwire.Activity{Target: channelwire.Target(origin.ChannelTarget), Action: "typing", UntilMS: time.Now().Add(8 * time.Second).UnixMilli()})
	}
	return result, nil
}
