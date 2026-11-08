package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/channelwire"
)

// freezeReplyText fixes all visible text and platform entity ranges before a
// single part is queued. Reopening or changing channel display settings cannot
// change the parts of an already-started logical reply.
func (s *channelSession) freezeReplyText(ctx context.Context, group, text string) ([]channelwire.TextPart, string, error) {
	var frozen struct {
		Parts  []channelwire.TextPart `json:"parts"`
		Format string                 `json:"format"`
	}
	key := "channel/render/" + s.source + "/" + s.application + "/" + group
	var raw string
	err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if err == nil {
		if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &frozen) != nil || len(frozen.Parts) == 0 || len(frozen.Parts) > 256 {
			return nil, "", ErrStoreFormat
		}
		return frozen.Parts, frozen.Format, nil
	}
	parts, err := s.extensionReplyParts(ctx, group, text)
	if err != nil {
		return nil, "", err
	}
	pending := false
	for logical, part := range parts {
		var rendered []channelwire.TextPart
		if s.renderText != nil {
			var err error
			rendered, err = s.renderText(part)
			if err != nil {
				return nil, "", err
			}
			frozen.Format = s.formatVersion
		} else if s.remote {
			bridge := s.tasks.core.Store.channelBridge.Load()
			if bridge == nil {
				return nil, "", ErrToolDisabled
			}
			info, _ := ctx.Value(channelDeliveryKey{}).(channelDeliveryInfo)
			info.Group, info.Sequence, info.Total = fmt.Sprintf("prepare/%s/%d", group, logical+1), 1, 1
			callCtx := context.WithValue(ctx, channelDeliveryKey{}, info)
			callCtx = context.WithValue(callCtx, channelEventKey{}, info.Group)
			target := ChannelTarget{Source: s.source, ApplicationID: s.application}
			if info.Run != "" {
				_, origin, err := s.tasks.ChannelRun(ctx, info.Run)
				if err != nil {
					return nil, "", err
				}
				if origin == nil {
					return nil, "", ErrPermissionDenied
				}
				target = origin.ChannelTarget
			}
			c, ok := bridge.connectionFor(target)
			if !ok {
				return nil, "", ErrPermissionDenied
			}
			var err error
			rendered, frozen.Format, err = bridge.prepareText(callCtx, c.ID, target, part)
			if errors.Is(err, errChannelPreparing) {
				pending = true
				continue
			}
			if err != nil {
				return nil, "", err
			}
		} else {
			rendered = channelwire.SplitTextParts(channelwire.TextPart{Text: part}, 3500)
		}
		for fragment := range rendered {
			rendered[fragment].LogicalPart = logical + 1
			rendered[fragment].PlatformPart = fragment + 1
		}
		frozen.Parts = append(frozen.Parts, rendered...)
	}
	if pending {
		return nil, "", errChannelPreparing
	}
	if _, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, string(mustJSON(frozen))); err != nil {
		return nil, "", err
	}
	if err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
		return nil, "", err
	}
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &frozen) != nil || len(frozen.Parts) == 0 || len(frozen.Parts) > 256 {
		return nil, "", ErrStoreFormat
	}
	return frozen.Parts, frozen.Format, nil
}
