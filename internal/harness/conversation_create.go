package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// CreateActiveConversation commits routing and creation together. A retried
// platform request returns its original result without routing a later chat
// back to an older selection.
func (store *Store) CreateActiveConversation(ctx context.Context, spaceID, title, workspace, request string) (Conversation, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "新对话"
	}
	if len([]rune(title)) > 80 || strings.ContainsAny(title, "\r\n") || hasControl(title) {
		return Conversation{}, ErrInvalidConversationTitle
	}
	if workspace == "" || request == "" {
		return Conversation{}, ErrRequestKey
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1 FOR UPDATE`, spaceID))
	if err != nil {
		return Conversation{}, err
	}
	if space.Source != SpaceSourceLocal && !space.MultiConversation {
		return Conversation{}, ErrInvalidSpace
	}
	key := "space/new/" + spaceID + "/" + contentHash([]byte(request))
	fingerprint := contentHash(mustJSON([]string{title, workspace}))
	var saved string
	err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&saved)
	if err == nil {
		var receipt struct {
			Fingerprint  string       `json:"fingerprint"`
			Conversation Conversation `json:"conversation"`
		}
		if json.Unmarshal([]byte(saved), &receipt) != nil || receipt.Conversation.ID == "" {
			return Conversation{}, ErrStoreFormat
		}
		if receipt.Fingerprint != fingerprint {
			return Conversation{}, ErrRequestKey
		}
		return receipt.Conversation, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, err
	}
	c := Conversation{ID: newID(), Title: title, Workspace: workspace, SpaceID: spaceID, CreatedAt: nowText()}
	c.UpdatedAt = c.CreatedAt
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)`, c.ID, c.Title, c.Workspace, c.SpaceID, c.CreatedAt, c.UpdatedAt); err != nil {
		return Conversation{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.spaces SET active_conversation_id=$1,default_conversation_id=CASE WHEN default_conversation_id='' THEN $1 ELSE default_conversation_id END,updated_at=$2 WHERE id=$3`, c.ID, c.UpdatedAt, spaceID); err != nil {
		return Conversation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(map[string]any{"fingerprint": fingerprint, "conversation": c}))); err != nil {
		return Conversation{}, err
	}
	return c, tx.Commit()
}

func (tasks *TaskService) NewActiveConversationInSpace(ctx context.Context, spaceID, title, request string) (Conversation, error) {
	if tasks == nil || tasks.core == nil {
		return Conversation{}, errors.New("harness: task service is not configured")
	}
	return tasks.core.Store.CreateActiveConversation(ctx, spaceID, title, tasks.core.currentConfig().Workspace, request)
}
