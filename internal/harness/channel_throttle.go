package harness

import (
	"context"
	"database/sql"
	"errors"

	"github.com/endview/freeagent/internal/channelwire"
)

func channelThrottleKey(target channelwire.Target) string {
	return "channel/throttle/" + target.Source + "/" + target.ApplicationID + "/" + target.ChatID
}

func saveChannelThrottle(ctx context.Context, tx *sql.Tx, target channelwire.Target, until int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,jsonb_build_object('until_ms',$2::bigint)::text)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
		WHERE (fa.meta.document->>'until_ms')::bigint < $2`, channelThrottleKey(target), until)
	return err
}

func (tasks *TaskService) channelCooldown(ctx context.Context, target ChannelTarget) (int64, error) {
	var until int64
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT (document->>'until_ms')::bigint FROM fa.meta WHERE key=$1`, channelThrottleKey(channelwire.Target(target))).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return until, err
}

func (b *ChannelBridge) failPendingDelivery(ctx context.Context, key, raw string, op channelwire.Operation, reason string) error {
	tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockChannelRun(ctx, tx, op.RunID); err != nil {
		return err
	}
	op.State, op.SuppressedReason = "failed", reason
	result, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), key, raw)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if channelwire.IsDelivery(op.Kind) {
		if err := pauseChannelGroupTx(ctx, tx, op); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return b.recordUnsent(ctx, op)
}
