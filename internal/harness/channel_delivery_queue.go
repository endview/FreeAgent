package harness

import (
	"context"
	"errors"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

type channelQueued struct{ operation string }

func (e *channelQueued) Error() string { return "channel: delivery durably queued" }
func isChannelQueued(err error) bool   { var queued *channelQueued; return errors.As(err, &queued) }

type channelDeliveryKey struct{}
type channelDeliveryInfo struct {
	Claim, Run, Chat          string
	Maintenance               bool
	Group, Format             string
	Sequence, Total           int
	LogicalPart, PlatformPart int
	MinGapMS                  int
	Entities                  []channelwire.TextEntity
}

func (s *channelSession) callbackKey(token string) string {
	return "channel/approval/" + s.source + "/" + s.application + "/" + token
}
func (s *channelSession) saveCallback(token string, item channelApproval) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, s.callbackKey(token), string(mustJSON(item)))
	return err
}

func (b *ChannelBridge) recordDelivered(ctx context.Context, op channelwire.Operation) error {
	if err := b.projectWakeReceipt(ctx, op); err != nil {
		return err
	}
	if (op.Kind == "sticker" || op.Kind == "reaction") && op.RunID != "" {
		text := "[Bot reaction updated: " + op.Reaction + "]"
		if op.Kind == "sticker" && op.Sticker != nil {
			text = "[Delivered sticker: " + op.Sticker.Emoji + "; type=" + op.Sticker.Format + "; set=" + op.Sticker.SetName + "]"
		}
		if err := b.tasks.RecordChannelDelivery(ctx, op.RunID, op.MessageID+"-"+op.ID, text, "sent"); err != nil {
			return err
		}
	}
	if op.ClaimKey != "" {
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(jsonb_set(document,'{state}','"sent"'::jsonb,true),'{message_id}',to_jsonb($1::text),true))::text WHERE key=$2`, op.MessageID, op.ClaimKey); err != nil {
			return err
		}
	}
	if op.Kind == "text" && op.RunID != "" {
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{state}','"sent"'::jsonb,true))::text WHERE key=$1`, "channel/delivery/"+op.RunID+"/"+op.ID); err != nil {
			return err
		}
		if err := b.tasks.RecordChannelDelivery(ctx, op.RunID, op.MessageID, op.Text, "sent", op.Maintenance); err != nil {
			return err
		}
	}
	for _, button := range op.Buttons {
		key := "channel/approval/" + op.Target.Source + "/" + op.Target.ApplicationID + "/" + button.Token
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(jsonb_set(document,'{Message}',to_jsonb($1::text),true))::text WHERE key=$2`, op.MessageID, key); err != nil {
			return err
		}
	}
	if op.Kind == "file" {
		store := b.tasks.core.Store
		store.attachmentMu.Lock()
		defer store.attachmentMu.Unlock()
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		record, err := loadAttachment(ctx, tx, op.AttachmentID)
		if err != nil {
			return err
		}
		record.Ref.Public = true
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(record)), attachmentPrefix+op.AttachmentID); err != nil {
			return err
		}
		body := ModelMessage{Role: "assistant", Content: "[Delivered file: " + record.Ref.Name + "]", Public: true, Attachments: []AttachmentRef{record.Ref}, Channel: &ChannelMessageSource{ChannelTarget: ChannelTarget(op.Target), MessageID: op.MessageID, Purpose: "delivery"}}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.messages(conversation_id,run_id,request_key,body_json) VALUES($1,NULL,$2,$3) ON CONFLICT(request_key) WHERE request_key!='' DO NOTHING`, op.ConversationID, "channel-file-delivery/"+op.ID, string(mustJSON(body))); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}
