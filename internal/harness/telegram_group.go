package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/endview/freeagent/internal/channelwire"
)

// Platform/group policy belongs to the connector integration. It grants no
// tools, resources or identity. Only authenticated management can edit policy.
type TelegramGroup struct {
	Revision       int64  `json:"revision"`
	ReplyAll       bool   `json:"reply_all"`
	SaveBackground bool   `json:"save_background"`
	Status         string `json:"status,omitempty"`
	CanSend        bool   `json:"can_send"`
	Forum          bool   `json:"forum"`
	LastEvent      int64  `json:"last_event,omitempty"`
	MigratedTo     string `json:"migrated_to,omitempty"`
	MigratedFrom   string `json:"migrated_from,omitempty"`
}

func telegramGroupKey(t ChannelTarget) string {
	return "connector/telegram/" + t.ApplicationID + "/group/" + t.ChatID
}

func telegramGroupRead(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, target ChannelTarget) (TelegramGroup, error) {
	state := TelegramGroup{CanSend: true}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, telegramGroupKey(target)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err == nil && json.Unmarshal([]byte(raw), &state) != nil {
		err = ErrStoreFormat
	}
	return state, err
}
func (tasks *TaskService) TelegramGroup(ctx context.Context, target ChannelTarget) (TelegramGroup, error) {
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil {
		return TelegramGroup{}, err
	}
	if target.Source != SpaceSourceTelegram || space.ChatKind != "group" {
		return TelegramGroup{}, ErrInvalidSpace
	}
	return telegramGroupRead(ctx, tasks.core.Store.db, target)
}
func (tasks *TaskService) ApplyTelegramGroup(ctx context.Context, target ChannelTarget, replyAll, background bool, revision int64, operation string) (TelegramGroup, error) {
	if !validTelegramTarget(target) || target.ThreadID != "" || !validChannelPart(operation) {
		return TelegramGroup{}, ErrInvalidSpace
	}
	if _, err := tasks.TelegramGroup(ctx, target); err != nil {
		return TelegramGroup{}, err
	}
	store := tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return TelegramGroup{}, err
	}
	defer tx.Rollback()
	key := telegramGroupKey(target)
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, string(mustJSON(TelegramGroup{CanSend: true}))); err != nil {
		return TelegramGroup{}, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, key).Scan(&raw); err != nil {
		return TelegramGroup{}, err
	}
	var state TelegramGroup
	if json.Unmarshal([]byte(raw), &state) != nil {
		return state, ErrStoreFormat
	}
	digest := contentHash(mustJSON([]any{target, replyAll, background, revision}))
	ledger := key + "/result/" + operation
	var saved string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, ledger).Scan(&saved); err == nil {
		var result struct {
			Digest string
			State  TelegramGroup
		}
		if json.Unmarshal([]byte(saved), &result) != nil || result.Digest != digest {
			return state, ErrRequestKey
		}
		return result.State, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	if state.Revision != revision {
		return state, ErrControlStaleRevision
	}
	if state.ReplyAll != replyAll || state.SaveBackground != background {
		state.ReplyAll, state.SaveBackground = replyAll, background
		state.Revision++
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key); err != nil {
		return state, err
	}
	if !background {
		if err = clearTelegramBackgroundTx(ctx, tx, target); err != nil {
			return state, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, ledger, string(mustJSON(map[string]any{"Digest": digest, "State": state}))); err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func (b *ChannelBridge) telegramLifecycle(ctx context.Context, id string, event channelwire.Event) error {
	target := ChannelTarget(event.Target).group()
	if target.Source != SpaceSourceTelegram || !validTelegramTarget(target) || event.Lifecycle == nil {
		return ErrPermissionDenied
	}
	n, err := strconv.ParseInt(event.ID, 10, 64)
	if err != nil || n <= 0 {
		return ErrRequestKey
	}
	if event.Lifecycle.Kind == "migration" {
		return b.migrateTelegramGroup(ctx, target, event.Lifecycle.NewChatID, n)
	}
	if event.Lifecycle.Kind != "membership" {
		return ErrInvalidSpace
	}
	store := b.tasks.core.Store
	store.telegramMu.Lock()
	defer store.telegramMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := telegramGroupKey(target)
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, string(mustJSON(TelegramGroup{CanSend: true}))); err != nil {
		return err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, key).Scan(&raw); err != nil {
		return err
	}
	var state TelegramGroup
	if json.Unmarshal([]byte(raw), &state) != nil {
		return ErrStoreFormat
	}
	if n <= state.LastEvent {
		return nil
	}
	fact := event.Lifecycle
	changed := state.Status != fact.Status || state.CanSend != fact.CanSend
	state.LastEvent, state.Status, state.CanSend, state.Forum = n, fact.Status, fact.CanSend, fact.Forum
	if changed {
		state.Revision++
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key); err != nil {
		return err
	}
	grant, _, err := telegramAccessRaw(ctx, tx, target)
	if err != nil {
		return err
	}
	if changed && grant.Revision > 0 {
		grant.Revision++
		grant.UpdatedAt = nowText()
		if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(grant)), telegramAccessKey(target)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (tasks *TaskService) telegramTargetAvailable(ctx context.Context, target ChannelTarget) error {
	state, err := telegramGroupRead(ctx, tasks.core.Store.db, target)
	if err != nil {
		return err
	}
	if !state.CanSend || state.MigratedTo != "" {
		return ErrPermissionRevoked
	}
	return nil
}
