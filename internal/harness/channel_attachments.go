package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *channelSession) receiveFiles(ctx context.Context, e channelEvent, input *ChannelReceived) error {
	if s.source == SpaceSourceTelegram {
		if _, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person); err != nil {
			return err
		}
	}
	if s.download == nil || len(e.Media) > 8 {
		return errors.New("channel: bounded file download is unavailable")
	}
	ctx = WithAttachmentIdentity(ctx, "channel:"+e.Person)
	var refs []AttachmentRef
	var total int64
	policy := s.tasks.Config().Attachments.effective()
	for i, media := range e.Media {
		if s.source == SpaceSourceTelegram {
			if _, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person); err != nil {
				return err
			}
		}
		if media.Bytes > policy.MaxInputBytes {
			return errors.New("channel: attachment exceeds input limit")
		}
		raw, err := s.download(ctx, e, media, policy.MaxInputBytes)
		if err != nil {
			return err
		}
		total += int64(len(raw))
		if total > policy.MaxBatchBytes {
			return errors.New("channel: input batch exceeds its limit")
		}
		ref, err := s.tasks.UploadAttachmentRequest(ctx, input.ConversationID, media.Name, fmt.Sprintf("channel-%s-%d", input.origin.RequestKey, i), bytes.NewReader(raw))
		if err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	store := s.tasks.core.Store
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2 AND run_id IS NULL`, input.MessageID, input.ConversationID).Scan(&raw); err != nil {
		return err
	}
	var body ModelMessage
	if json.Unmarshal([]byte(raw), &body) != nil || body.Channel == nil || body.Channel.requestKey() != input.origin.RequestKey {
		return ErrAttachment
	}
	if err := bindAttachments(ctx, tx, input.ConversationID, "", refs); err != nil {
		return err
	}
	body.Attachments = refs
	if _, err := tx.ExecContext(ctx, `UPDATE fa.messages SET body_json=$1 WHERE id=$2 AND conversation_id=$3`, string(mustJSON(body)), input.MessageID, input.ConversationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, ref := range refs {
		input.attachments = append(input.attachments, ref.ID)
	}
	return nil
}

func (s *channelSession) deliverFiles(ctx context.Context, run Run, origin ChannelRunOrigin) error {
	if err := s.tasks.checkTelegramRun(ctx, run); err != nil {
		return err
	}
	infos, err := s.tasks.Attachments(ctx, run.ConversationID)
	if err != nil {
		return err
	}
	var result error
	for _, info := range infos {
		r, err := loadAttachment(ctx, s.tasks.core.Store.db, info.Ref.ID)
		if err != nil {
			return err
		}
		if r.Deleted || r.Ref.Source != "file_export" || r.Request != run.ID || r.ExportSource != "" && !r.ExportSelected {
			continue
		}
		result = errors.Join(result, s.sendFileReceipt(ctx, run.ConversationID, run.ID, origin.ChannelTarget, s.replyMessage(ctx, run), r.Ref))
	}
	return result
}
func (s *channelSession) sendFileReceipt(ctx context.Context, chat, binding string, target ChannelTarget, reply string, ref AttachmentRef) error {
	if target.Source == SpaceSourceTelegram {
		if err := s.withTelegramFileAccess(ctx, target, binding, func() error { return nil }); err != nil {
			return err
		}
	}
	space, err := s.tasks.ChannelSpace(ctx, target)
	if err != nil {
		return err
	}
	conversation, err := s.tasks.core.Store.GetConversation(ctx, chat)
	if err != nil || conversation.SpaceID != space.ID {
		return ErrConversationSpace
	}
	key := "channel/file-send/" + binding + "/" + contentHash(mustJSON([]any{chat, binding, target, ref.ID, ref.SHA256}))
	s.tasks.core.Store.attachmentMu.Lock()
	record, loadErr := loadAttachment(ctx, s.tasks.core.Store.db, ref.ID)
	if loadErr != nil || record.Deleted || record.ExportSource != "" && !record.ExportSelected {
		s.tasks.core.Store.attachmentMu.Unlock()
		return loadErr
	}
	claim, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, string(mustJSON(map[string]any{"state": "dispatching", "attachment": ref, "target": target, "replay": false})))
	s.tasks.core.Store.attachmentMu.Unlock()
	if err != nil {
		return err
	}
	count, err := claim.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	_, raw, readErr := s.tasks.Attachment(ctx, chat, ref.ID)
	message := ""
	sendErr := readErr
	if readErr == nil {
		if s.sendFile == nil {
			sendErr = &CallError{Certain: true, Message: "channel: file sending unavailable"}
		} else {
			sendErr = s.withTelegramFileAccess(ctx, target, binding, func() error {
				select {
				case s.sendGate <- struct{}{}:
					if delay := time.Until(s.nextSend); delay > 0 {
						if !channelDelay(ctx, min(delay, 4*time.Second)) {
							sendErr = ctx.Err()
						}
					}
					if sendErr == nil {
						message, sendErr = s.sendFile(ctx, target, reply, ref, raw)
					}
					s.nextSend = time.Now().Add(time.Second)
					<-s.sendGate
				case <-ctx.Done():
					sendErr = &CallError{Certain: true, Message: "channel: file send cancelled before dispatch"}
				}
				return sendErr
			})
		}
	}
	state := "sent"
	if isChannelQueued(sendErr) {
		state, sendErr = "queued", nil
	} else if sendErr != nil || message == "" {
		state = "unknown"
		var transport interface{ DispatchCertain() bool }
		var call *CallError
		if readErr != nil || errors.As(sendErr, &transport) && transport.DispatchCertain() || errors.As(sendErr, &call) && call.Certain {
			state = "failed"
		}
	}
	persist, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_, saveErr := s.tasks.core.Store.db.ExecContext(persist, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND (document #>> '{state}')='dispatching'`, string(mustJSON(map[string]any{"state": state, "attachment": ref, "target": target, "message_id": message, "replay": false})), key)
	if state == "sent" {
		store := s.tasks.core.Store
		store.attachmentMu.Lock()
		defer store.attachmentMu.Unlock()
		tx, err := store.db.BeginTx(persist, nil)
		if err != nil {
			return errors.Join(sendErr, saveErr, err)
		}
		defer tx.Rollback()
		r, err := loadAttachment(persist, tx, ref.ID)
		if err != nil {
			return errors.Join(sendErr, saveErr, err)
		}
		r.Ref.Public = true
		ref.Public = true
		if _, err := tx.ExecContext(persist, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(r)), attachmentPrefix+ref.ID); err != nil {
			return errors.Join(sendErr, saveErr, err)
		}
		body := ModelMessage{Role: "assistant", Content: "[Delivered file: " + ref.Name + "]", Public: true, Attachments: []AttachmentRef{ref}, Channel: &ChannelMessageSource{ChannelTarget: target, MessageID: message, Purpose: "delivery"}}
		if _, err := tx.ExecContext(persist, `INSERT INTO fa.messages(conversation_id,run_id,request_key,body_json) VALUES($1,NULL,$2,$3) ON CONFLICT(request_key) WHERE request_key!='' DO NOTHING`, chat, key, string(mustJSON(body))); err != nil {
			return errors.Join(sendErr, saveErr, err)
		}
		saveErr = errors.Join(saveErr, tx.Commit())
	}
	return errors.Join(sendErr, saveErr)
}
func (s *channelSession) saveReplyFile(ctx context.Context, e channelEvent, chat string) error {
	if e.Target.Source == SpaceSourceTelegram {
		grant, err := s.tasks.CheckTelegramAccess(ctx, e.Target, e.Person)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: e.Target, Person: e.Person, Revision: grant.Revision})
	}
	// Only actual public reply text may be exported from a channel, including
	// None. A private Run output is never substituted for a delivered message.
	query := `SELECT body_json FROM fa.messages WHERE conversation_id=$1 AND (body_json #>> '{Role}')='assistant' AND ((body_json #>> '{Public}')::boolean)=true AND coalesce((body_json #>> '{Channel,purpose}'),'')!='command'`
	args := []any{chat}
	if e.ReplyTo != "" {
		query += ` AND (body_json #>> '{Channel,message_id}')=$2`
		args = append(args, e.ReplyTo)
	}
	query += ` ORDER BY id DESC LIMIT 1`
	var raw string
	if err := s.tasks.core.Store.db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return s.plain(ctx, e, "没有可另存的公开回复；请回复选中的消息后发送 /save-reply。")
	}
	var body ModelMessage
	if json.Unmarshal([]byte(raw), &body) != nil || body.Channel == nil || body.Channel.ChannelTarget != e.Target {
		return ErrAttachment
	}
	key := "channel/reply-file/" + contentHash(mustJSON([]string{chat, e.Message}))
	claim, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, `{"state":"creating","replay":false}`)
	if err != nil {
		return err
	}
	count, _ := claim.RowsAffected()
	if count == 0 {
		return nil
	}
	ref, err := s.tasks.createAttachment(ctx, chat, "reply.md", strings.NewReader(body.Content), "reply", e.Message, true)
	if err != nil {
		return err
	}
	return s.sendFileReceipt(ctx, chat, key, e.Target, e.Message, ref)
}
