package harness

import (
	"context"
	"database/sql"
	"github.com/endview/freeagent/internal/channelwire"
)

// The accepted generation and outbox operation become sendable in one
// transaction. A transport poll must never observe half of this association.
func associateWakeTx(ctx context.Context, tx *sql.Tx, op channelwire.Operation) error {
	if op.WakeID == "" {
		return nil
	}
	r, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('state','delivery_queued','delivery_id',$1::text))::text WHERE key=$2 AND document->>'run_id'=$3 AND document->>'state' IN ('generated','delivery_queued') AND COALESCE(document->>'delivery_id','') IN ('',$1)`, op.ID, wakePrefix+op.WakeID, op.RunID)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func (b *ChannelBridge) projectWakeReceipt(ctx context.Context, op channelwire.Operation) error {
	if op.WakeID == "" || op.State != "sent" && op.State != "unknown" && op.State != "failed" {
		return nil
	}
	_, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('delivery_state',$1::text,'delivery_message',$2::text,'state',CASE WHEN document->>'state'='delivery_queued' THEN $3::text ELSE document->>'state' END))::text WHERE key=$4 AND document->>'run_id'=$5`, op.State, op.MessageID, "delivery_"+op.State, wakePrefix+op.WakeID, op.RunID)
	return err
}
