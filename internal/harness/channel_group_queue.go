package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func channelGroupCancelKey(connection, group string) string {
	return "channel/cancelled/" + connection + "/" + contentHash([]byte(group))
}

func lockChannelRun(ctx context.Context, tx *sql.Tx, runID string) error {
	if runID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "channel/run/"+runID)
	return err
}

// Queue the entire frozen reply and its enqueue receipts in one transaction.
// A crash leaves either no group or every member; an enqueue is not a platform
// dispatch and therefore cannot create an UNKNOWN send on its own.
func (s *channelSession) queueReplyGroup(ctx context.Context, run Run, origin *ChannelRunOrigin, group, revision, format string, parts []channelwire.TextPart, buttons []channelButton, maintenance bool) error {
	bridge := s.tasks.core.Store.channelBridge.Load()
	if bridge == nil {
		return ErrToolDisabled
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	var connection string
	for id, c := range bridge.connections {
		if c.config.Source == origin.Source && c.config.ApplicationID == origin.ApplicationID {
			connection = id
			break
		}
	}
	if connection == "" {
		return ErrPermissionDenied
	}
	tx, err := s.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockChannelRun(ctx, tx, run.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, connection+"/"+group); err != nil {
		return err
	}
	var cancelled bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key IN ($1,$2))`, "channel/run-cancelled/"+run.ID, channelGroupCancelKey(connection, group)).Scan(&cancelled); err != nil {
		return err
	}
	if cancelled {
		return nil
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key >= $1 AND key < $2 AND document->>'group'=$3`, channelOperationPrefix+connection+"/", channelOperationPrefix+connection+"/\uffff", group).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return tx.Commit()
	} // A committed group is immutable.
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key >= $1 AND key < $2 AND document->>'run_id'=$3 AND document->>'kind'='sticker' AND document->>'state'='held' ORDER BY document->>'created_at',key FOR UPDATE`, channelOperationPrefix+connection+"/", channelOperationPrefix+connection+"/\uffff", run.ID)
	if err != nil {
		return err
	}
	type held struct {
		key string
		op  channelwire.Operation
	}
	stickers := []held{}
	for rows.Next() {
		var item held
		var raw string
		if err := rows.Scan(&item.key, &raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(raw), &item.op) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		stickers = append(stickers, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if run.Status != "completed" {
		for _, item := range stickers {
			// A stopped/failed Run cannot later emit a selected sticker. Waiting
			// approval keeps it held for the explicit continuation.
			if run.Status == "waiting_confirmation" {
				break
			}
			item.op.State = "cancelled"
			if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(item.op)), item.key); err != nil {
				return err
			}
		}
		stickers = nil
	}
	total := len(parts) + len(stickers)
	if total > 256 {
		return ErrContextLimit
	}
	gap := 0
	if snapshot, err := decodeRunSnapshot(run.ConfigJSON); err == nil {
		gap = snapshot.Config.Delivery.IntervalMS
	}
	reply := s.replyMessage(ctx, run)
	person := ""
	if run.Permission != nil {
		person = run.Permission.PersonID
	}
	for i, part := range parts {
		id := contentHash(mustJSON([]any{connection, group, i + 1}))
		claim := "channel/send/" + run.ID + "/" + contentHash([]byte(fmt.Sprintf("%s/%d", revision, i)))
		op := channelwire.Operation{ID: id, Connection: connection, Kind: "text", Target: channelwire.Target(origin.ChannelTarget), Reply: reply, Text: part.Text, State: "pending", CreatedAt: nowText(), RunID: run.ID, ConversationID: run.ConversationID, Group: group, Sequence: i + 1, Total: total, ClaimKey: claim, Maintenance: maintenance, Entities: part.Entities, FormatVersion: format, LogicalPart: part.LogicalPart, PlatformPart: part.PlatformPart, MinGapMS: gap, Person: person, LocalOwner: person == "", AccessRevision: origin.AccessRevision}
		if i == len(parts)-1 {
			for _, button := range buttons {
				op.Buttons = append(op.Buttons, channelwire.Button{Text: button.Text, Token: button.Token})
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, channelOperationPrefix+connection+"/"+id, string(mustJSON(op))); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, claim, string(mustJSON(map[string]any{"state": "queued", "message_id": id}))); err != nil {
			return err
		}
	}
	for i, item := range stickers {
		item.op.Group, item.op.Sequence, item.op.Total, item.op.State = group, len(parts)+i+1, total, "pending"
		item.op.MinGapMS = gap
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(item.op)), item.key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Stop shares the enqueue advisory lock; row updates race safely with claims
// via their exact-value CAS. Dispatched/UNKNOWN/sent evidence stays intact.
func (tasks *TaskService) cancelChannelDeliveries(ctx context.Context, chat, reason string) (bool, error) {
	if tasks.core == nil || tasks.core.Store == nil || tasks.core.Store.channelBridge.Load() == nil {
		return false, nil
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT id FROM fa.runs WHERE conversation_id=$1 AND (id=(SELECT id FROM fa.runs WHERE conversation_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1) OR id IN (SELECT document->>'run_id' FROM fa.meta WHERE key >= 'channel/operation/' AND key < 'channel/operation0' AND document->>'conversation_id'=$1 AND document->>'state' IN ('pending','paused','scheduled','reviewing','held'))) ORDER BY id`, chat)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := lockChannelRun(ctx, tx, id); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'true') ON CONFLICT(key) DO NOTHING`, "channel/run-cancelled/"+id); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT 'channel/cancelled/'||(document->>'connection')||'/'||encode(sha256(convert_to(document->>'group','UTF8')),'hex'),'true' FROM fa.meta WHERE key >= 'channel/operation/' AND key < 'channel/operation0' AND document->>'conversation_id'=$1 AND document->>'run_id'=ANY($2::text[]) AND document->>'state' IN ('pending','paused','scheduled','reviewing','held') ON CONFLICT(key) DO NOTHING`, chat, ids); err != nil {
		return false, err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(jsonb_set(document,'{state}','"cancelled"'::jsonb,true),'{suppressed_reason}',to_jsonb($3::text),true)::text WHERE key >= 'channel/operation/' AND key < 'channel/operation0' AND document->>'conversation_id'=$1 AND document->>'run_id'=ANY($2::text[]) AND document->>'state' IN ('pending','paused','scheduled','reviewing','held')`, chat, ids, reason)
	if err != nil {
		return false, err
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return false, err
	}
	pending, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(jsonb_set(document,'{state}','"cancelled"'::jsonb,true),'{suppressed_reason}',to_jsonb($2::text),true)::text WHERE key >= 'channel/pending-reply/' AND key < 'channel/pending-reply0' AND document->>'run_id'=ANY($1::text[]) AND COALESCE(document->>'state','pending')='pending'`, ids, reason)
	if err != nil {
		return false, err
	}
	preparing, err := pending.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0 || preparing > 0, tx.Commit()
}

func (tasks *TaskService) stopChannelDeliveries(chat, reason string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed, err := tasks.cancelChannelDeliveries(ctx, chat, reason)
	if err != nil {
		tasks.core.event(Event{Kind: EventChannelDeliveryError, ConversationID: chat, Message: "stop unsent delivery: " + safeError(err)})
	}
	return changed
}
