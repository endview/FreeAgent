package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrSpaceNotFound     = errors.New("harness: space not found")
	ErrLocalSpace        = errors.New("harness: the local space cannot be deleted")
	ErrSpaceHasActiveRun = errors.New("harness: space has unfinished runs")
	ErrConversationSpace = errors.New("harness: conversation does not belong to the space")
	ErrInvalidSpace      = errors.New("harness: invalid space")
)

func validateSpaceSource(source, chatKind, externalChat string) error {
	switch source {
	case SpaceSourceLocal:
		if chatKind != "" || externalChat != "" {
			return ErrInvalidSpace
		}
	case SpaceSourceTelegram, SpaceSourceLark, SpaceSourceQQ:
		if chatKind != "private" && chatKind != "group" {
			return ErrInvalidSpace
		}
		if strings.TrimSpace(externalChat) == "" || len(externalChat) > 512 ||
			strings.ContainsAny(externalChat, "\x00\r\n") {
			return ErrInvalidSpace
		}
	default:
		return ErrInvalidSpace
	}
	return nil
}

func validateSpaceTitle(title string) error {
	if strings.TrimSpace(title) == "" || len([]rune(title)) > 80 ||
		strings.ContainsAny(title, "\r\n") || hasControl(title) {
		return ErrInvalidSpace
	}
	return nil
}

func scanSpace(row interface{ Scan(...any) error }) (Space, error) {
	var space Space
	var multi bool
	err := row.Scan(&space.ID, &space.Title, &space.Source, &space.ChatKind, &space.ExternalChat,
		&space.DefaultConversationID, &space.ActiveConversationID, &multi, &space.CreatedAt, &space.UpdatedAt)
	space.MultiConversation = multi
	if errors.Is(err, sql.ErrNoRows) {
		return Space{}, fmt.Errorf("%w: space not found", ErrSpaceNotFound)
	}
	return space, err
}

const spaceColumns = `id,title,source,chat_kind,external_chat,default_conversation_id,active_conversation_id,multi_conversation_enabled,created_at,updated_at`

func (store *Store) GetSpace(ctx context.Context, id string) (Space, error) {
	if strings.TrimSpace(id) == "" {
		return Space{}, ErrSpaceNotFound
	}
	return scanSpace(store.db.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1`, id))
}

func (store *Store) ListSpaces(ctx context.Context) ([]Space, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+spaceColumns+`
		FROM fa.spaces ORDER BY CASE WHEN source='local' THEN 0 ELSE 1 END, updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Space, 0, 16)
	for rows.Next() {
		item, err := scanSpace(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) CreateSpace(ctx context.Context, title, source, chatKind, externalChat string) (Space, error) {
	if err := validateSpaceTitle(title); err != nil {
		return Space{}, err
	}
	if err := validateSpaceSource(source, chatKind, externalChat); err != nil {
		return Space{}, err
	}
	now := nowText()
	space := Space{ID: newID(), Title: title, Source: source, ChatKind: chatKind, ExternalChat: externalChat,
		CreatedAt: now, UpdatedAt: now}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Space{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.spaces(`+spaceColumns+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		space.ID, space.Title, space.Source, space.ChatKind, space.ExternalChat, "", "", false, space.CreatedAt, space.UpdatedAt)
	if err != nil {
		return Space{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "space/permission/"+space.ID, string(mustJSON(PermissionSelection{SpaceID: space.ID, Source: "space", Mode: PermissionNone}))); err != nil {
		return Space{}, err
	}
	return space, tx.Commit()
}

func (store *Store) EnsureExternalSpace(ctx context.Context, title, source, chatKind, externalChat string) (Space, bool, error) {
	if source == SpaceSourceLocal {
		return Space{}, false, ErrInvalidSpace
	}
	if err := validateSpaceTitle(title); err != nil {
		return Space{}, false, err
	}
	if err := validateSpaceSource(source, chatKind, externalChat); err != nil {
		return Space{}, false, err
	}
	existing, err := scanSpace(store.db.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE source=$1 AND external_chat=$2`, source, externalChat))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrSpaceNotFound) {
		return Space{}, false, err
	}
	space, err := store.CreateSpace(ctx, title, source, chatKind, externalChat)
	if err != nil {
		if existing, e := scanSpace(store.db.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE source=$1 AND external_chat=$2`, source, externalChat)); e == nil {
			return existing, false, nil
		}
	}
	return space, true, err
}

func (store *Store) RenameSpace(ctx context.Context, id, title string) error {
	if err := validateSpaceTitle(title); err != nil {
		return err
	}
	if _, err := store.GetSpace(ctx, id); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE fa.spaces SET title=$1,updated_at=$2 WHERE id=$3`, title, nowText(), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("%w: %s", ErrSpaceNotFound, id)
	}
	return nil
}

func (store *Store) SetSpaceDefaultConversation(ctx context.Context, spaceID, conversationID string) error {
	return store.setSpaceConversation(ctx, spaceID, conversationID, true)
}

func (store *Store) SetSpaceActiveConversation(ctx context.Context, spaceID, conversationID string) error {
	return store.setSpaceConversation(ctx, spaceID, conversationID, false)
}

func (store *Store) setSpaceConversation(ctx context.Context, spaceID, conversationID string, defaultConversation bool) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1`, spaceID))
	if err != nil {
		return err
	}
	var belongs string
	if err := tx.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1`, conversationID).Scan(&belongs); err != nil {
		return err
	}
	if belongs != spaceID {
		return ErrConversationSpace
	}
	if !defaultConversation && space.Source != "local" && !space.MultiConversation && conversationID != space.DefaultConversationID {
		return errors.New("harness: enable multiple conversations before changing channel routing")
	}
	if defaultConversation {
		_, err = tx.ExecContext(ctx, `UPDATE fa.spaces SET default_conversation_id=$1,active_conversation_id=CASE WHEN source!='local' AND multi_conversation_enabled=false THEN $2 ELSE active_conversation_id END,updated_at=$3 WHERE id=$4`, conversationID, conversationID, nowText(), spaceID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE fa.spaces SET active_conversation_id=$1,updated_at=$2 WHERE id=$3`, conversationID, nowText(), spaceID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) SetSpaceMultiConversation(ctx context.Context, spaceID string, enabled bool) error {
	result, err := store.db.ExecContext(ctx, `UPDATE fa.spaces SET multi_conversation_enabled=$1,active_conversation_id=CASE WHEN $2=false THEN default_conversation_id ELSE active_conversation_id END,updated_at=$3 WHERE id=$4`, enabled, enabled, nowText(), spaceID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrSpaceNotFound
	}
	return nil
}

func (store *Store) MoveConversation(ctx context.Context, conversationID, targetSpaceID string) error {
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sourceSpace string
	if err := tx.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1`, conversationID).Scan(&sourceSpace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("harness: conversation %q not found", conversationID)
		}
		return err
	}
	if _, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1`, targetSpaceID)); err != nil {
		return err
	}
	if sourceSpace == targetSpaceID {
		return tx.Commit()
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate+` `, conversationID).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return ErrActiveRun
	}
	if err := checkConversationCommandsTx(ctx, tx, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.conversations SET space_id=$1,updated_at=$2 WHERE id=$3`, targetSpaceID, nowText(), conversationID); err != nil {
		return err
	}
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT source FROM fa.spaces WHERE id=$1`, targetSpaceID).Scan(&source); err != nil {
		return err
	}
	if source != SpaceSourceLocal {
		if err := publishMovedText(ctx, tx, conversationID, targetSpaceID); err != nil {
			return err
		}
		// Local selections and summaries must never travel implicitly to
		// another publication scope. Original messages/Attempts are retained.
		for _, prefix := range []string{"chat/memory/", capabilityMetaPrefix, summaryMetaPrefix} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, prefix+conversationID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{space}',to_jsonb($1::text),true))::text WHERE key LIKE 'attachment/%' AND (document #>> '{conversation}')=$2`, targetSpaceID, conversationID); err != nil {
		return err
	}
	if err := refreshSpacePointersTx(ctx, tx, sourceSpace); err != nil {
		return err
	}
	if err := refreshSpacePointersTx(ctx, tx, targetSpaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func refreshSpacePointersTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	var currentDefault, currentActive, first string
	if err := tx.QueryRowContext(ctx, `SELECT default_conversation_id,active_conversation_id FROM fa.spaces WHERE id=$1`, spaceID).Scan(&currentDefault, &currentActive); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM fa.conversations WHERE space_id=$1 ORDER BY updated_at DESC,id LIMIT 1`, spaceID).Scan(&first); errors.Is(err, sql.ErrNoRows) {
		first = ""
	} else if err != nil {
		return err
	}
	if currentDefault != "" {
		var belongs string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(space_id,'') FROM fa.conversations WHERE id=$1`, currentDefault).Scan(&belongs); err != nil || belongs != spaceID {
			currentDefault = ""
		}
	}
	if currentActive != "" {
		var belongs string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(space_id,'') FROM fa.conversations WHERE id=$1`, currentActive).Scan(&belongs); err != nil || belongs != spaceID {
			currentActive = ""
		}
	}
	if currentDefault == "" {
		currentDefault = first
	}
	if currentActive == "" {
		currentActive = currentDefault
	}
	var source string
	var multi bool
	if err := tx.QueryRowContext(ctx, `SELECT source,multi_conversation_enabled FROM fa.spaces WHERE id=$1`, spaceID).Scan(&source, &multi); err != nil {
		return err
	}
	if source != "local" && !multi {
		currentActive = currentDefault
	}
	_, err := tx.ExecContext(ctx, `UPDATE fa.spaces SET default_conversation_id=$1,active_conversation_id=$2,updated_at=$3 WHERE id=$4`,
		currentDefault, currentActive, nowText(), spaceID)
	return err
}

func (store *Store) DeleteSpace(ctx context.Context, id string) error {
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	if id == LocalSpaceID {
		return ErrLocalSpace
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM fa.spaces WHERE id=$1`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrSpaceNotFound, id)
		}
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.runs r JOIN fa.conversations c ON c.id=r.conversation_id WHERE c.space_id=$1 AND (r.status IN ('running','waiting_confirmation','stopped','unknown') OR (r.status='limited' AND substr(r.stop_reason,1,14)!='settled_limit:'))`, id).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return ErrSpaceHasActiveRun
	}
	var activeCommands int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta m JOIN fa.conversations c ON c.id=(CASE WHEN m.key LIKE 'command/process/%' THEN m.document ELSE '{}' END #>> '{conversation_id}') WHERE c.space_id=$1 AND m.key LIKE 'command/process/%' AND ((m.document #>> '{state}') IN ('running','stopping') OR ((m.document #>> '{state}')='unknown' AND coalesce(((m.document #>> '{reviewed}')::boolean),false)=false))`, id).Scan(&activeCommands); err != nil {
		return err
	}
	if activeCommands != 0 {
		return ErrCommandBusy
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{space}',to_jsonb($1::text),true))::text WHERE key LIKE 'attachment/%' AND (document #>> '{space}')=$2`, LocalSpaceID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.conversations SET space_id=$1,updated_at=$2 WHERE space_id=$3`, LocalSpaceID, nowText(), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1 OR key LIKE $2`, "space/permission/"+id, "space/people/"+id+"/%"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.spaces WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key LIKE 'telegram/access/%' AND (document #>> '{space_id}')=$1`, id); err != nil {
		return err
	}
	if err := refreshSpacePointersTx(ctx, tx, LocalSpaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func checkConversationCommandsTx(ctx context.Context, tx *sql.Tx, chat string) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'command/process/%' AND (document #>> '{conversation_id}')=$1 AND ((document #>> '{state}') IN ('running','stopping') OR ((document #>> '{state}')='unknown' AND coalesce(((document #>> '{reviewed}')::boolean),false)=false))`, chat).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrCommandBusy
	}
	return nil
}

// A confirmed move publishes the local text the user has previewed. Original
// messages and Attempts remain intact; synthetic command prompts and private
// tool bodies are never made into group background. Receipt keys deduplicate
// repeated moves without a new publication/task entity.
func publishMovedText(ctx context.Context, tx *sql.Tx, chat, spaceID string) error {
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1`, spaceID))
	if err != nil {
		return err
	}
	target, ok := channelTarget(space)
	if !ok {
		return nil
	}

	type item struct {
		id      int64
		message ModelMessage
	}
	var cursor int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT m.id,m.body_json FROM fa.messages m JOIN fa.runs r ON r.id=m.run_id WHERE m.conversation_id=$1 AND m.id>$2 AND COALESCE((r.config_doc #>> '{Permission,entry}'),'local')='local' AND NOT EXISTS(SELECT 1 FROM fa.meta WHERE key='command/direct/'||r.id) ORDER BY m.id LIMIT 32`, chat, cursor)
		if err != nil {
			return err
		}
		var text []item
		count := 0
		for rows.Next() {
			var raw string
			if err := rows.Scan(&cursor, &raw); err != nil {
				rows.Close()
				return err
			}
			count++
			var m ModelMessage
			if json.Unmarshal([]byte(raw), &m) != nil {
				rows.Close()
				return ErrStoreFormat
			}
			if !m.Public && m.Channel == nil && (m.Role == "user" || m.Role == "assistant") && m.Content != "" {
				text = append(text, item{cursor, m})
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, entry := range text {
			key := "channel/publication/" + chat + "/" + strconv.FormatInt(entry.id, 10)
			source := &ChannelMessageSource{ChannelTarget: target, MessageID: "published-" + strconv.FormatInt(entry.id, 10), Purpose: "published"}
			refs := append([]AttachmentRef(nil), entry.message.Attachments...)
			for i := range refs {
				r, err := loadAttachment(ctx, tx, refs[i].ID)
				if err != nil || r.Deleted {
					continue
				}
				refs[i].Public = true
				r.Ref.Public = true
				if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+refs[i].ID); err != nil {
					return err
				}
			}
			public := ModelMessage{Role: entry.message.Role, Content: entry.message.Content, Public: true, Channel: source, Attachments: refs}
			if _, err := tx.ExecContext(ctx, `INSERT INTO fa.messages(conversation_id,run_id,request_key,body_json) VALUES($1,NULL,$2,$3) ON CONFLICT(request_key) WHERE request_key!='' DO NOTHING`, chat, key, string(mustJSON(public))); err != nil {
				return err
			}
		}
		if count < 32 {
			return nil
		}
	}
}
