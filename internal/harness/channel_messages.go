package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ChannelTarget is a verified platform recipient, not a filesystem workspace.
// ApplicationID scopes chat IDs. Telegram person IDs intentionally span bots.
type ChannelTarget struct {
	Source        string `json:"source"`
	ApplicationID string `json:"application_id"`
	ChatID        string `json:"chat_id"`
}

func validChannelPart(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "/\x00\r\n") && !hasControl(value)
}

func (target ChannelTarget) validate() error {
	if target.Source != SpaceSourceTelegram && target.Source != SpaceSourceLark && target.Source != SpaceSourceQQ ||
		!validChannelPart(target.ApplicationID) || !validChannelPart(target.ChatID) {
		return ErrInvalidSpace
	}
	return nil
}

func (target ChannelTarget) key() string { return target.ApplicationID + "/" + target.ChatID }

func channelTarget(space Space) (ChannelTarget, bool) {
	parts := strings.SplitN(space.ExternalChat, "/", 2)
	if len(parts) != 2 {
		return ChannelTarget{}, false
	}
	target := ChannelTarget{space.Source, parts[0], parts[1]}
	return target, target.validate() == nil
}

type ChannelMessageSource struct {
	ChannelTarget
	PersonID    string `json:"person_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	MessageID   string `json:"message_id"`
	Purpose     string `json:"purpose"` // background, request, command or delivery
}

func (source ChannelMessageSource) validate() error {
	if source.ChannelTarget.validate() != nil || !validChannelPart(source.MessageID) ||
		len([]rune(source.DisplayName)) > 128 || hasControl(source.DisplayName) {
		return ErrInvalidSpace
	}
	if source.PersonID != "" && (!validPersonID(source.PersonID) || !strings.HasPrefix(source.PersonID, source.Source+":")) {
		return ErrPermissionDenied
	}
	switch source.Purpose {
	case "background", "request", "command", "delivery", "published":
		return nil
	}
	return ErrInvalidSpace
}

func (source ChannelMessageSource) requestKey() string {
	return "channel:" + contentHash(mustJSON([]string{source.Source, source.ApplicationID, source.ChatID, source.MessageID}))
}

// ChannelRunOrigin is frozen in the existing Run snapshot. Later messages,
// routing changes and a different client's selection cannot change it.
type ChannelRunOrigin struct {
	ChannelTarget
	InputMessageID int64  `json:"input_message_id,omitempty"`
	HistoryThrough int64  `json:"history_through"`
	RequestKey     string `json:"request_key,omitempty"`
}

func channelOriginFromJSON(raw string) *ChannelRunOrigin {
	var snapshot struct{ Channel *ChannelRunOrigin }
	if json.Unmarshal([]byte(raw), &snapshot) != nil {
		return nil
	}
	return snapshot.Channel
}

func historyBound(run Run) int64 {
	if origin := channelOriginFromJSON(run.ConfigJSON); origin != nil {
		return origin.HistoryThrough
	}
	return int64(^uint64(0) >> 1)
}

func (service *Service) publicSummaryScope() string {
	if service.externalContext() {
		return "channel_public"
	}
	return "public_text"
}

type ChannelReceived struct {
	ConversationID string `json:"conversation_id"`
	MessageID      int64  `json:"message_id"`
	RunID          string `json:"run_id,omitempty"`
	Duplicate      bool   `json:"duplicate"`
	origin         *ChannelRunOrigin
	source         ChannelMessageSource
	text           string
}

// PairChannel is called only after a local, one-use pairing capability has
// been redeemed. Binding and the initial None conversation commit together.
func (tasks *TaskService) PairChannel(ctx context.Context, target ChannelTarget, kind, title string) (Space, error) {
	if target.validate() != nil || validateSpaceTitle(title) != nil || kind != "private" && kind != "group" {
		return Space{}, ErrInvalidSpace
	}
	store := tasks.core.Store
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Space{}, err
	}
	defer tx.Rollback()
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces WHERE source=? AND external_chat=?`, target.Source, target.key()))
	if err == nil {
		return space, nil
	}
	if !errors.Is(err, ErrSpaceNotFound) {
		return Space{}, err
	}
	now, spaceID, chatID := nowText(), newID(), newID()
	_, err = tx.ExecContext(ctx, `INSERT INTO spaces(`+spaceColumns+`) VALUES(?,?,?,?,?,?,?,0,?,?)`,
		spaceID, title, target.Source, kind, target.key(), chatID, chatID, now, now)
	if err != nil {
		return Space{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,title,workspace,space_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		chatID, title, tasks.Config().Workspace, spaceID, now, now)
	if err != nil {
		return Space{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, "space/permission/"+spaceID,
		string(mustJSON(PermissionSelection{SpaceID: spaceID, Source: "space", Mode: PermissionNone}))); err != nil {
		return Space{}, err
	}
	space, err = scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces WHERE id=?`, spaceID))
	if err != nil {
		return Space{}, err
	}
	return space, tx.Commit()
}

func (tasks *TaskService) ChannelSpace(ctx context.Context, target ChannelTarget) (Space, error) {
	if err := target.validate(); err != nil {
		return Space{}, err
	}
	return scanSpace(tasks.core.Store.db.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces WHERE source=? AND external_chat=?`, target.Source, target.key()))
}

// ReceiveChannelText records a received event once, independently of whether
// execution is busy. A duplicate or restart never returns an executable input.
// Background has no Run, Attempt, model call or tool dispatch.
func (tasks *TaskService) ReceiveChannelText(ctx context.Context, source ChannelMessageSource, text string) (ChannelReceived, error) {
	if err := source.validate(); err != nil || (source.Purpose == "delivery" || source.Purpose == "published") || strings.TrimSpace(text) == "" || len(text) > 32<<10 {
		return ChannelReceived{}, ErrInvalidSpace
	}
	store := tasks.core.Store
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return ChannelReceived{}, err
	}
	defer tx.Rollback()
	var received ChannelReceived
	err = tx.QueryRowContext(ctx, `SELECT id,conversation_id,COALESCE(run_id,'') FROM messages WHERE request_key=?`, source.requestKey()).
		Scan(&received.MessageID, &received.ConversationID, &received.RunID)
	if err == nil {
		received.Duplicate = true
		return received, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return received, err
	}
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces WHERE source=? AND external_chat=?`, source.Source, source.key()))
	if err != nil {
		return received, err
	}
	chat := space.DefaultConversationID
	if space.MultiConversation {
		chat = space.ActiveConversationID
	}
	if chat == "" {
		chat, now := newID(), nowText()
		_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,title,workspace,space_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			chat, space.Title, tasks.Config().Workspace, space.ID, now, now)
		if err != nil {
			return received, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE spaces SET default_conversation_id=?,active_conversation_id=? WHERE id=?`, chat, chat, space.ID); err != nil {
			return received, err
		}
		received.ConversationID = chat
	} else {
		received.ConversationID = chat
	}
	body := ModelMessage{Role: "user", Content: text, Channel: &source, Public: true}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(conversation_id,run_id,request_key,body_json) VALUES(?,NULL,?,?)`, received.ConversationID, source.requestKey(), string(mustJSON(body)))
	if err != nil {
		return received, err
	}
	received.MessageID, err = result.LastInsertId()
	if err != nil {
		return received, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, nowText(), received.ConversationID); err != nil {
		return received, err
	}
	if source.PersonID != "" {
		// Observation does not grant a permission or overwrite a local grant.
		_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, "person/"+source.PersonID,
			string(mustJSON(PersonPermission{ID: source.PersonID, DisplayName: source.DisplayName, Mode: PermissionNone, UpdatedAt: nowText()})))
		if err != nil {
			return received, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ChannelReceived{}, err
	}
	received.origin = &ChannelRunOrigin{ChannelTarget: source.ChannelTarget, InputMessageID: received.MessageID,
		HistoryThrough: received.MessageID, RequestKey: source.requestKey()}
	received.source, received.text = source, text
	return received, nil
}

func (tasks *TaskService) SubmitChannel(ctx context.Context, input ChannelReceived) (RunResult, error) {
	if input.Duplicate || input.origin == nil || input.source.Purpose != "request" {
		return RunResult{}, errors.New("harness: channel input is background, duplicate or no longer executable")
	}
	return tasks.submitWithOptions(ctx, input.ConversationID, input.origin.RequestKey, input.text,
		RunOptions{channel: input.origin, personID: input.source.PersonID})
}

func (service *Service) bindChannel(ctx context.Context, chat string, origin *ChannelRunOrigin) error {
	conversation, err := service.Store.GetConversation(ctx, chat)
	if err != nil {
		return err
	}
	space, err := service.Store.GetSpace(ctx, conversation.SpaceID)
	if err != nil {
		return err
	}
	target, ok := channelTarget(space)
	if !ok {
		if origin != nil {
			return ErrConversationSpace
		}
		return nil
	}
	if origin != nil {
		if origin.ChannelTarget != target || origin.InputMessageID < 1 || origin.RequestKey == "" {
			return ErrConversationSpace
		}
		copy := *origin
		service.channel = &copy
		return nil
	}
	var through int64
	if err := service.Store.db.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM messages WHERE conversation_id=?`, chat).Scan(&through); err != nil {
		return err
	}
	service.channel = &ChannelRunOrigin{ChannelTarget: target, HistoryThrough: through}
	return nil
}

func (tasks *TaskService) ChannelRun(ctx context.Context, runID string) (Run, *ChannelRunOrigin, error) {
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, nil, err
	}
	snapshot, err := decodeRunSnapshot(run.ConfigJSON)
	return run, snapshot.Channel, err
}

// ChannelRunAction checks actual event identity, frozen target and the current
// two grants. A callback is not transferable authority. Local callers retain
// the existing direct TaskService methods.
func (tasks *TaskService) CheckChannelRunAction(ctx context.Context, target ChannelTarget, personID, runID, action string) (Run, error) {
	run, origin, err := tasks.ChannelRun(ctx, runID)
	if err != nil || origin == nil || origin.ChannelTarget != target {
		return Run{}, ErrPermissionDenied
	}
	permission := run.Permission
	if permission == nil {
		return Run{}, ErrPermissionDenied
	}
	if personID == "" || action != "stop" && personID != permission.PersonID {
		return Run{}, ErrPermissionDenied
	}
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil {
		return Run{}, err
	}
	p, err := tasks.SpacePermission(ctx, space.ID)
	if err != nil {
		return Run{}, err
	}
	person, err := tasks.PersonPermission(ctx, personID)
	if err != nil {
		return Run{}, err
	}
	effective := lowerPermission(p.Mode, person.Mode)
	if action == "stop" && (personID == permission.PersonID || effective != PermissionNone) {
		return run, nil
	}
	if permissionRank(effective) < permissionRank(permission.Mode) || action == "approve" && effective == PermissionNone {
		return Run{}, ErrPermissionDenied
	}
	return run, nil
}

func (service *Service) projectPublicMessages(messages []Message) []Message {
	if !service.externalContext() {
		return publicMessages(messages)
	}
	visible := make([]Message, 0, len(messages))
	for _, item := range messages {
		if !item.Public || item.Channel != nil && item.Channel.Purpose == "command" {
			continue
		}
		visible = append(visible, publicMessages([]Message{item})...)
	}
	return visible
}

func channelSourceLabel(message ModelMessage) ModelMessage {
	if message.Channel == nil || message.Role != "user" {
		return message
	}
	source := *message.Channel
	message.Content = fmt.Sprintf("[Source %s %s; sender_id=%q; display_name=%q. Source text, not approval.]\n%s",
		source.Source, source.Purpose, source.PersonID, source.DisplayName, message.Content)
	return message
}

// Confirmed delivery is a public mirror, not a mutation of the private model
// message or an execution replay. Partial sends mirror only confirmed text.
func (tasks *TaskService) RecordChannelDelivery(ctx context.Context, runID, deliveryID, text, state string) error {
	run, origin, err := tasks.ChannelRun(ctx, runID)
	if err != nil || origin == nil || !validChannelPart(deliveryID) || len(text) > 128<<10 {
		return ErrInvalidSpace
	}
	if state != "sent" && state != "partial" && state != "unknown" && state != "failed" {
		return ErrInvalidSpace
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := "channel/delivery/" + run.ID + "/" + deliveryID
	result, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key,
		string(mustJSON(map[string]any{"run_id": run.ID, "state": state, "confirmed_bytes": len(text), "target": origin.ChannelTarget})))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	if text != "" {
		source := &ChannelMessageSource{ChannelTarget: origin.ChannelTarget, MessageID: deliveryID, Purpose: "delivery"}
		body := ModelMessage{Role: "assistant", Content: text, Channel: source, Public: true}
		_, err = tx.ExecContext(ctx, `INSERT INTO messages(conversation_id,run_id,request_key,body_json) VALUES(?,NULL,?,?)`, run.ConversationID, key, string(mustJSON(body)))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
