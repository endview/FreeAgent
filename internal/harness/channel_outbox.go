package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/fileops"
)

func (b *ChannelBridge) Outbox(ctx context.Context, id string) ([]channelwire.Operation, error) {
	if err := b.recoverPreparedReplies(ctx, id); err != nil {
		return nil, err
	}
	if err := b.prepareScheduled(ctx, id, time.Now()); err != nil {
		return nil, err
	}
	if err := b.recoverInboundNotices(ctx, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.connections[id]
	if c == nil {
		return nil, ErrPermissionDenied
	}
	if c.config.Source == SpaceSourceTelegram {
		// Match Configure's lock order. Revocation holds telegramMu through its
		// commit, so validation and the durable claim form one dispatch boundary.
		b.tasks.core.Store.telegramMu.Lock()
		defer b.tasks.core.Store.telegramMu.Unlock()
	}
	prefix := channelOperationPrefix + id + "/"
	// A lost receipt does not authorize another send. Expired dispatches are
	// projected as UNKNOWN, while a later matching success is still accepted.
	// Select one ready sequence member per target; hashes are never send order.
	const query = `WITH active AS MATERIALIZED (
	 SELECT DISTINCT COALESCE(document->>'group',document->>'id') AS id FROM fa.meta WHERE key >= $1 AND key < $2
	 AND (document->>'state' IN ('pending','dispatching') OR document->>'state'='sent' AND COALESCE((document->>'projected')::boolean,false)=false)
	 AND (document->>'state' IN ('sent','dispatching') OR NOT EXISTS(SELECT 1 FROM fa.meta x WHERE x.key='channel/run-cancelled/'||(fa.meta.document->>'run_id')) AND NOT EXISTS(SELECT 1 FROM fa.meta x WHERE x.key='channel/cancelled/'||$4||'/'||encode(sha256(convert_to(fa.meta.document->>'group','UTF8')),'hex')))),
	 scoped AS MATERIALIZED (SELECT o.key,o.value,o.document FROM fa.meta o JOIN active a ON a.id=COALESCE(o.document->>'group',o.document->>'id') WHERE o.key >= $1 AND o.key < $2),
	groups AS (SELECT COALESCE(document->>'group',document->>'id') AS id,document->'target' AS target,count(*) AS n,
	 bool_or(document->>'state' IN ('sent','dispatching')) AS started,
	 bool_or(document->>'state' IN ('failed','unknown','paused') AND COALESCE((document->>'reviewed_uncertainty')::boolean,false)=false) AS blocked,
	 count(*) FILTER (WHERE document->>'state'='pending') AS pending,
	 greatest(min(extract(epoch FROM COALESCE(NULLIF(document->>'created_at','')::timestamptz,'epoch'::timestamptz))*1000),max(COALESCE((document->>'resumed_ms')::bigint,0))) AS created FROM scoped GROUP BY 1,2)
	SELECT o.key,o.value FROM scoped o JOIN groups g ON g.id=COALESCE(o.document->>'group',o.document->>'id')
	WHERE (
	 (o.document->>'state'='sent' AND COALESCE((o.document->>'projected')::boolean,false)=false)
	 OR (o.document->>'state'='dispatching' AND COALESCE((o.document->>'next_dispatch_ms')::bigint,0)<=$3)
	 OR (o.document->>'state'='pending' AND COALESCE((o.document->>'attempts')::bigint,0)<3
	     AND NOT EXISTS(SELECT 1 FROM fa.meta WHERE key='channel/cancelled/'||$4||'/'||encode(sha256(convert_to(o.document->>'group','UTF8')),'hex'))
	     AND g.n>=COALESCE((o.document->>'total')::int,1)
	     AND COALESCE((o.document->>'next_dispatch_ms')::bigint,0)<=$3
	     AND NOT EXISTS (SELECT 1 FROM groups a WHERE a.target=g.target AND a.id<>g.id AND a.started AND NOT a.blocked AND a.pending>0 AND (NOT g.started OR (a.created,a.id)<(g.created,g.id)))
	     AND COALESCE((SELECT (p.document->>'confirmed_ms')::bigint FROM scoped p WHERE p.document->>'group'=o.document->>'group' AND (p.document->>'sequence')::int=(o.document->>'sequence')::int-1),0)+COALESCE((o.document->>'min_gap_ms')::int,0)<=$3
	     AND NOT EXISTS (SELECT 1 FROM fa.meta p WHERE p.key >= $1 AND p.key < $2
	       AND p.document->'target'=o.document->'target' AND p.document->>'state'='dispatching'
	       AND COALESCE((p.document->>'next_dispatch_ms')::bigint,0)>$3)
	     AND NOT EXISTS (SELECT 1 FROM fa.meta p WHERE p.key >= $1 AND p.key < $2
	       AND p.document->>'group'=o.document->>'group'
	       AND COALESCE((p.document->>'sequence')::int,0)<COALESCE((o.document->>'sequence')::int,0)
	       AND p.document->>'state'<>'sent' AND COALESCE((p.document->>'reviewed_uncertainty')::boolean,false)=false)))
	ORDER BY CASE WHEN g.started AND NOT g.blocked THEN 0 ELSE 1 END,g.created,
	 o.document->>'group',COALESCE((o.document->>'sequence')::int,0),o.key LIMIT 64`
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, query, prefix, prefix+"\uffff", time.Now().UnixMilli(), id)
	if err != nil {
		return nil, err
	}
	type item struct {
		key, raw string
		op       channelwire.Operation
	}
	var items []item
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.key, &i.raw); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal([]byte(i.raw), &i.op) != nil {
			rows.Close()
			return nil, ErrStoreFormat
		}
		items = append(items, i)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var result []channelwire.Operation
	targets := map[channelwire.Target]bool{}
	for _, i := range items {
		op := i.op
		if op.State == "sent" {
			if err := b.projectDelivery(ctx, op); err != nil {
				return nil, err
			}
			continue
		}
		if op.State == "dispatching" && channelwire.PurePreparation(op.Kind) {
			op.State = "pending"
			if op.Attempts >= 3 {
				op.State = "failed"
			}
			for n := range op.Dispatches {
				if op.Dispatches[n].ID == op.Dispatch {
					op.Dispatches[n].State = "preparation_interrupted"
				}
			}
			if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw); err != nil {
				return nil, err
			}
			continue
		}
		if op.State == "dispatching" {
			op.State = "unknown"
			for n := range op.Dispatches {
				if op.Dispatches[n].ID == op.Dispatch {
					op.Dispatches[n].State = "unknown"
				}
			}
			tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			changed, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw)
			if err != nil {
				tx.Rollback()
				return nil, err
			}
			n, err := changed.RowsAffected()
			if err != nil {
				tx.Rollback()
				return nil, err
			}
			if n == 0 {
				tx.Rollback()
				continue
			}
			if err := pauseChannelGroupTx(ctx, tx, op); err != nil {
				tx.Rollback()
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			if err := b.recordUnsent(ctx, op); err != nil {
				return nil, err
			}
			continue
		}
		if targets[op.Target] || len(result) >= 8 {
			continue
		}
		if op.Proactive {
			wait, reason, err := b.candidateEligibility(ctx, op, time.Now())
			if err != nil {
				return nil, err
			}
			if reason != "" {
				op.State, op.SuppressedReason = "cancelled", reason
			} else if wait > time.Now().UnixMilli() {
				op.State, op.NotBeforeMS = "scheduled", wait
			}
			if op.State != "pending" {
				if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw); err != nil {
					return nil, err
				}
				continue
			}
		}
		if op.Target.Source != c.config.Source || op.Target.ApplicationID != c.config.ApplicationID {
			return nil, ErrPermissionDenied
		}
		if op.Target.Source == SpaceSourceTelegram && !op.Admission {
			grant, err := b.tasks.telegramDeliveryGrant(ctx, ChannelTarget(op.Target), op.Person, op.LocalOwner)
			if err != nil || grant.Revision != op.AccessRevision {
				op.State, op.SuppressedReason = "cancelled", "authorization_revoked"
				if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw); err != nil {
					return nil, err
				}
				continue
			}
		}
		if op.Kind == "file" {
			_, data, err := b.tasks.Attachment(ctx, op.ConversationID, op.AttachmentID)
			if err != nil {
				return nil, err
			}
			op.Data = data
		}
		tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if err := lockChannelRun(ctx, tx, op.RunID); err != nil {
			tx.Rollback()
			return nil, err
		}
		var cancelled bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key IN ($1,$2))`, channelGroupCancelKey(id, op.Group), "channel/run-cancelled/"+op.RunID).Scan(&cancelled); err != nil {
			tx.Rollback()
			return nil, err
		}
		if cancelled {
			op.State, op.SuppressedReason = "cancelled", "user_stopped_or_abandoned"
		} else {
			op.Attempts++
			op.Dispatch = newID()
			op.LastDispatch = nowText()
			op.NextDispatchMS = time.Now().Add(2 * time.Minute).UnixMilli()
			op.Dispatches = append(op.Dispatches, channelwire.Dispatch{ID: op.Dispatch, At: op.LastDispatch, State: "dispatching"})
			op.State = "dispatching"
		}
		data := op.Data
		op.Data = nil
		changed, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if n, err := changed.RowsAffected(); err != nil {
			tx.Rollback()
			return nil, err
		} else if n != 1 {
			tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if cancelled {
			continue
		}
		op.Data = data
		result = append(result, op)
		targets[op.Target] = true
		if len(data) > 0 {
			break
		} // Keep the bounded wire response below 24 MiB.
	}
	return result, nil
}

func (b *ChannelBridge) Receipt(ctx context.Context, id string, r channelwire.Receipt) error {
	if !validChannelPart(r.ID) || !validChannelPart(r.Dispatch) || r.RetryAfterMS < 0 || r.RetryAfterMS > 86400000 {
		return ErrPermissionDenied
	}
	b.mu.Lock()
	if b.connections[id] == nil {
		b.mu.Unlock()
		return ErrPermissionDenied
	}
	defer b.mu.Unlock()
	key := channelOperationPrefix + id + "/" + r.ID
	var runID string
	if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT COALESCE(document->>'run_id','') FROM fa.meta WHERE key=$1`, key).Scan(&runID); err != nil {
		return err
	}
	tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockChannelRun(ctx, tx, runID); err != nil {
		return err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, key).Scan(&raw); err != nil {
		return err
	}
	var op channelwire.Operation
	if json.Unmarshal([]byte(raw), &op) != nil {
		return ErrStoreFormat
	}
	index := -1
	for n, d := range op.Dispatches {
		if d.ID == r.Dispatch {
			index = n
			break
		}
	}
	if op.Connection != id || index < 0 {
		return ErrPermissionDenied
	}
	if r.PlainTextFallback && op.Kind != "text" {
		return ErrRequestKey
	}
	if r.State != "sent" && r.State != "downloaded" && r.State != "failed" && r.State != "unknown" {
		return ErrRequestKey
	}
	if r.State == "sent" && (r.MessageID == "" || op.Kind == "download" || op.Kind == "sticker_set" || channelwire.PurePreparation(op.Kind)) {
		return ErrRequestKey
	}
	if r.State == "downloaded" && ((op.Kind != "download" && op.Kind != "sticker_set" && !channelwire.PurePreparation(op.Kind)) || int64(len(r.Data)) > op.Limit || op.Limit > 16<<20) {
		return ErrAttachment
	}
	d := &op.Dispatches[index]
	if d.State == "sent" || d.State == "downloaded" {
		if d.State != r.State || d.MessageID != r.MessageID {
			return ErrRequestKey
		}
	} else {
		d.State, d.MessageID, d.RetryAfterMS = r.State, r.MessageID, r.RetryAfterMS
	}
	if r.State == "downloaded" {
		if channelwire.PurePreparation(op.Kind) {
			if op.State == "cancelled" {
				return tx.Commit()
			}
			if _, err := validatePrepared(op, r.Data); err != nil {
				return err
			}
		}
		hash := contentHash(r.Data)
		if op.State == "downloaded" && (op.DataSHA256 != hash || op.DataBytes != int64(len(r.Data))) {
			return ErrAttachment
		}
		if err := b.saveDownload(op.ID, r.Data); err != nil {
			return err
		}
		op.DataSHA256, op.DataBytes = hash, int64(len(r.Data))
	}
	// Any real success wins over a newer uncertain dispatch; all attempt
	// receipts remain available. A late failure cannot overwrite success.
	if op.State != "sent" && op.State != "downloaded" {
		if r.State == "sent" || r.State == "downloaded" || op.Dispatch == r.Dispatch {
			op.State, op.MessageID = r.State, r.MessageID
			if r.State == "sent" && op.ConfirmedMS == 0 {
				op.ConfirmedMS = time.Now().UnixMilli()
			}
			if r.RetryAfterMS > 0 {
				op.NextDispatchMS = time.Now().Add(time.Duration(r.RetryAfterMS) * time.Millisecond).UnixMilli()
				if op.State == "failed" && op.Attempts < 3 {
					op.State = "pending"
				}
			}
		}
	}
	op.PlainTextFallback = op.PlainTextFallback || r.PlainTextFallback
	if op.State == "pending" {
		var cancelled bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key IN ($1,$2))`, channelGroupCancelKey(id, op.Group), "channel/run-cancelled/"+op.RunID).Scan(&cancelled); err != nil {
			return err
		}
		if cancelled {
			op.State, op.SuppressedReason = "cancelled", "user_stopped_or_abandoned"
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), key, raw); err != nil {
		return err
	}
	if (op.State == "unknown" || op.State == "failed") && !op.ReviewedUncertainty && !channelwire.PurePreparation(op.Kind) {
		if err := pauseChannelGroupTx(ctx, tx, op); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if op.State == "sent" {
		return b.projectDelivery(ctx, op)
	}
	if op.State == "failed" || op.State == "unknown" {
		return b.recordUnsent(ctx, op)
	}
	return nil
}

func (b *ChannelBridge) recordUnsent(ctx context.Context, op channelwire.Operation) error {
	if err := b.projectWakeReceipt(ctx, op); err != nil {
		return err
	}
	if op.ClaimKey != "" {
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{state}',to_jsonb($1::text),true))::text WHERE key=$2`, op.State, op.ClaimKey); err != nil {
			return err
		}
	}
	if op.Kind == "text" && op.RunID != "" {
		return b.tasks.RecordChannelDelivery(ctx, op.RunID, op.State+"-"+op.ID, "", op.State, op.Maintenance)
	}
	return nil
}

func pauseChannelGroupTx(ctx context.Context, tx *sql.Tx, op channelwire.Operation) error {
	if op.Group == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'true') ON CONFLICT(key) DO NOTHING`, channelGroupPauseKey(op.Connection, op.Group)); err != nil {
		return err
	}
	prefix := channelOperationPrefix + op.Connection + "/"
	_, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{state}','"paused"'::jsonb,true)::text WHERE key >= $1 AND key < $2 AND document->>'group'=$3 AND (document->>'sequence')::int>$4 AND document->>'state'='pending'`, prefix, prefix+"\uffff", op.Group, op.Sequence)
	return err
}

func channelGroupPauseKey(connection, group string) string {
	return "channel/paused/" + connection + "/" + contentHash([]byte(group))
}

func (b *ChannelBridge) saveDownload(id string, data []byte) error {
	dir := filepath.Join(b.tasks.DataDir(), "channel-downloads")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	target := filepath.Join(dir, id)
	if existing, err := os.ReadFile(target); err == nil {
		if contentHash(existing) != contentHash(data) {
			return ErrAttachment
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".download-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return fileops.Rename(name, target)
}

func (b *ChannelBridge) projectDelivery(ctx context.Context, op channelwire.Operation) error {
	if err := b.recordDelivered(ctx, op); err != nil {
		return err
	}
	_, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{projected}','true'::jsonb,true))::text WHERE key=$1`, channelOperationPrefix+op.Connection+"/"+op.ID)
	return err
}
