package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type channelRoute struct {
	Target  ChannelTarget `json:"target"`
	Default string        `json:"default"`
	Active  string        `json:"active"`
}

func cleanupChannelSpaceTx(ctx context.Context, tx *sql.Tx, space Space) error {
	target, ok := channelTarget(space)
	if !ok {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE starts_with(key,'channel/conversation-target/') AND substr(key,length('channel/conversation-target/')+1) IN (SELECT id FROM fa.conversations WHERE space_id=$1)`, space.ID); err != nil {
		return err
	}
	prefix := "connector/" + target.Source + "/" + target.key() + "/"
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key>=$1 AND key<$2`, prefix, prefix+"\uffff"); err != nil {
		return err
	}
	if target.Source == SpaceSourceTelegram {
		_, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('reply_all',false,'save_background',false,'revision',coalesce((document->>'revision')::bigint,0)+1))::text WHERE key=$1`, telegramGroupKey(target))
		return err
	}
	return nil
}

func channelRouteKey(t ChannelTarget) string {
	return "connector/" + t.Source + "/" + t.key() + "/topic/" + contentHash([]byte(t.ThreadID))
}

func channelConversationTarget(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, chat string, fallback ChannelTarget) (ChannelTarget, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "channel/conversation-target/"+chat).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback.group(), nil
	}
	var target ChannelTarget
	if err == nil && (json.Unmarshal([]byte(raw), &target) != nil || target.validate() != nil || target.group() != fallback.group()) {
		err = ErrConversationSpace
	}
	return target, err
}

func (tasks *TaskService) channelRouteTx(ctx context.Context, tx *sql.Tx, space Space, target ChannelTarget) (channelRoute, error) {
	route := channelRoute{Target: target, Default: space.DefaultConversationID, Active: space.ActiveConversationID}
	if target.ThreadID == "" {
		return route, nil
	}
	if space.ChatKind != "group" {
		return route, ErrConversationSpace
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelRouteKey(target)).Scan(&raw)
	if err == nil {
		if json.Unmarshal([]byte(raw), &route) != nil || route.Target != target {
			return route, ErrStoreFormat
		}
		return route, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return route, err
	}
	chat := space.DefaultConversationID
	if target.ThreadID != "general" || chat == "" {
		chat = newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5)`, chat, space.Title+" · "+target.ThreadID, tasks.Config().Workspace, space.ID, nowText())
		if err != nil {
			return route, err
		}
		if err = initializeSpaceCapabilitiesTx(ctx, tx, chat, space.ID); err != nil {
			return route, err
		}
	}
	route.Default, route.Active = chat, chat
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, channelRouteKey(target), string(mustJSON(route))); err != nil {
		return route, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "channel/conversation-target/"+chat, string(mustJSON(target)))
	return route, err
}

func (tasks *TaskService) ChannelConversation(ctx context.Context, target ChannelTarget) (string, error) {
	if target.validate() != nil {
		return "", ErrInvalidSpace
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE source=$1 AND external_chat=$2 FOR UPDATE`, target.Source, target.key()))
	if err != nil {
		return "", err
	}
	route, err := tasks.channelRouteTx(ctx, tx, space, target)
	if err != nil {
		return "", err
	}
	chat := route.Default
	if space.MultiConversation {
		chat = route.Active
	}
	return chat, tx.Commit()
}

func (tasks *TaskService) setChannelConversation(ctx context.Context, target ChannelTarget, chat string, asDefault bool) error {
	if target.ThreadID == "" {
		actual, err := channelConversationTarget(ctx, tasks.core.Store.db, chat, target)
		if err != nil || actual != target {
			return ErrConversationSpace
		}
		space, err := tasks.ChannelSpace(ctx, target)
		if err != nil {
			return err
		}
		if asDefault {
			return tasks.SetSpaceDefaultConversation(ctx, space.ID, chat)
		}
		return tasks.SetSpaceActiveConversation(ctx, space.ID, chat)
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE source=$1 AND external_chat=$2 FOR UPDATE`, target.Source, target.key()))
	if err != nil {
		return err
	}
	var belongs string
	if err = tx.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1`, chat).Scan(&belongs); err != nil {
		return err
	}
	actual, err := channelConversationTarget(ctx, tx, chat, target)
	if err != nil || belongs != space.ID || actual != target {
		return ErrConversationSpace
	}
	route, err := tasks.channelRouteTx(ctx, tx, space, target)
	if err != nil {
		return err
	}
	if asDefault {
		route.Default = chat
		if !space.MultiConversation {
			route.Active = chat
		}
	} else {
		route.Active = chat
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(route)), channelRouteKey(target))
	if err != nil {
		return err
	}
	return tx.Commit()
}
