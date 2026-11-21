package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/endview/freeagent/internal/channelwire"
)

func (b *ChannelBridge) migrateTelegramGroup(ctx context.Context, old ChannelTarget, newChat string, event int64) error {
	if !telegramNumeric(newChat, false) || !strings.HasPrefix(newChat, "-") || old.ChatID == newChat {
		return ErrInvalidSpace
	}
	next := old
	next.ChatID = newChat
	store := b.tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The old grant/space and its alias commit together. A duplicate reverse
	// migration update acknowledges the same fact without incrementing revision.
	state, err := telegramGroupRead(ctx, tx, old)
	if err != nil {
		return err
	}
	if state.MigratedTo != "" {
		if state.MigratedTo == newChat {
			return nil
		}
		return ErrConversationSpace
	}
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE source='telegram' AND external_chat=$1 FOR UPDATE`, old.key()))
	if errors.Is(err, ErrSpaceNotFound) || errors.Is(err, sql.ErrNoRows) {
		state.CanSend, state.MigratedTo, state.LastEvent = false, newChat, event
		_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, telegramGroupKey(old), string(mustJSON(state)))
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	var conflict bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.spaces WHERE source='telegram' AND external_chat=$1 AND id<>$2)`, next.key(), space.ID).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		state.CanSend, state.Status = false, "migration_conflict"
		state.Revision++
		_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, telegramGroupKey(old), string(mustJSON(state)))
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if space.ChatKind != "group" {
		return ErrInvalidSpace
	}
	grant, _, err := telegramAccessRaw(ctx, tx, old)
	if err != nil {
		return err
	}
	if grant.Revision > 0 {
		grant.Target = next
		grant.Revision++
		grant.UpdatedAt = nowText()
		if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, telegramAccessKey(next), string(mustJSON(grant))); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, telegramAccessKey(old)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.spaces SET external_chat=$1,updated_at=$2 WHERE id=$3`, next.key(), nowText(), space.ID); err != nil {
		return err
	}
	newState, err := telegramGroupRead(ctx, tx, next)
	if err != nil {
		return err
	}
	newState.ReplyAll, newState.SaveBackground = state.ReplyAll, state.SaveBackground
	newState.Revision = max(newState.Revision, state.Revision) + 1
	newState.MigratedFrom = old.ChatID
	if newState.Status == "" {
		newState.Status = state.Status
		newState.CanSend = state.CanSend
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, telegramGroupKey(next), string(mustJSON(newState))); err != nil {
		return err
	}
	state.MigratedTo, state.CanSend, state.LastEvent = newChat, false, event
	state.Revision++
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, telegramGroupKey(old), string(mustJSON(state))); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE document#>>'{target,source}'='telegram' AND document#>>'{target,application_id}'=$1 AND document#>>'{target,chat_id}'=$2 OR starts_with(key,'channel/conversation-target/') AND document->>'source'='telegram' AND document->>'application_id'=$1 AND document->>'chat_id'=$2`, old.ApplicationID, old.ChatID)
	if err != nil {
		return err
	}
	type item struct{ key, raw string }
	var items []item
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.key, &i.raw); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, i := range items {
		if strings.HasPrefix(i.key, channelOperationPrefix) {
			var op channelwire.Operation
			if json.Unmarshal([]byte(i.raw), &op) != nil {
				return ErrStoreFormat
			}
			// Already dispatched/unknown evidence remains associated with the old
			// operation. Only never-dispatched, reference-free output can move.
			if op.State != "pending" || op.Attempts != 0 || len(op.Dispatches) != 0 {
				continue
			}
			if op.Reply != "" || len(op.Buttons) > 0 {
				op.State, op.SuppressedReason = "paused", "migration_reference_unverified"
			} else {
				op.Target.ChatID = newChat
				op.AccessRevision = grant.Revision
			}
			if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), i.key); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(i.key, "channel/conversation-target/") {
			if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{chat_id}',to_jsonb($1::text))::text WHERE key=$2`, newChat, i.key); err != nil {
				return err
			}
			continue
		}
		prefix := "connector/telegram/" + old.key() + "/"
		if strings.HasPrefix(i.key, prefix) {
			key := "connector/telegram/" + next.key() + "/" + strings.TrimPrefix(i.key, prefix)
			if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,jsonb_set($2::jsonb,'{target,chat_id}',to_jsonb($3::text))::text)`, key, i.raw, newChat); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, i.key); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
