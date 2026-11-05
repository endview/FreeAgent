package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/fileops"
)

func (b *ChannelBridge) Outbox(ctx context.Context, id string) ([]channelwire.Operation, error) {
	if err := b.recoverInboundNotices(ctx, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.connections[id]
	if c == nil {
		return nil, ErrPermissionDenied
	}
	prefix := channelOperationPrefix + id + "/"
	// Filter eligibility before LIMIT: exhausted or delayed operations must not
	// starve new output. A dispatch lease is deliberately longer than HTTP I/O.
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key>=$1 AND key<$2 AND (((document #>> '{state}')='sent' AND COALESCE(((document #>> '{projected}')::boolean),false)=false) OR ((document #>> '{state}') IN ('pending','dispatching','unknown') AND COALESCE(((document #>> '{attempts}')::bigint),0)<3 AND COALESCE(((document #>> '{next_dispatch_ms}')::bigint),0)<=$3)) ORDER BY key LIMIT 8`, prefix, prefix+"\uffff", time.Now().UnixMilli())
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
	for _, i := range items {
		op := i.op
		if op.State == "sent" {
			if err := b.projectDelivery(ctx, op); err != nil {
				return nil, err
			}
			continue
		}
		if op.Target.Source != c.config.Source || op.Target.ApplicationID != c.config.ApplicationID {
			return nil, ErrPermissionDenied
		}
		if op.Target.Source == SpaceSourceTelegram && !op.Admission {
			grant, err := b.tasks.telegramDeliveryGrant(ctx, ChannelTarget(op.Target), op.Person, op.LocalOwner)
			if err != nil || grant.Revision != op.AccessRevision {
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
		op.Attempts++
		op.Dispatch = newID()
		op.LastDispatch = nowText()
		op.NextDispatchMS = time.Now().Add(2 * time.Minute).UnixMilli()
		op.Dispatches = append(op.Dispatches, channelwire.Dispatch{ID: op.Dispatch, At: op.LastDispatch, State: "dispatching"})
		op.State = "dispatching"
		data := op.Data
		op.Data = nil
		changed, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), i.key, i.raw)
		if err != nil {
			return nil, err
		}
		if n, err := changed.RowsAffected(); err != nil {
			return nil, err
		} else if n != 1 {
			continue
		}
		op.Data = data
		result = append(result, op)
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
	tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err != nil {
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
	if r.State != "sent" && r.State != "downloaded" && r.State != "failed" && r.State != "unknown" {
		return ErrRequestKey
	}
	if r.State == "sent" && (r.MessageID == "" || op.Kind == "download") {
		return ErrRequestKey
	}
	if r.State == "downloaded" && (op.Kind != "download" || int64(len(r.Data)) > op.Limit || op.Limit > 16<<20) {
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
			if r.RetryAfterMS > 0 {
				op.NextDispatchMS = time.Now().Add(time.Duration(r.RetryAfterMS) * time.Millisecond).UnixMilli()
				if op.State == "failed" {
					op.State = "pending"
				}
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(op)), key, raw); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if op.State == "sent" {
		return b.projectDelivery(ctx, op)
	}
	return nil
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
