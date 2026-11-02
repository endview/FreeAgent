package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const ControlSetAttachmentPolicy = "attachments.set_policy"
const ControlDeleteAttachment = "attachments.delete"

type attachmentDeleteArguments struct {
	Conversation string `json:"conversation_id"`
	ID           string `json:"id"`
	SHA256       string `json:"sha256"`
}

func (service *ControlService) buildAttachmentDelete(ctx context.Context, record *controlRecord) error {
	var args attachmentDeleteArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	r, err := loadAttachment(ctx, service.core.Store.db, args.ID)
	if err != nil || r.Deleted || r.Conversation != args.Conversation || r.Ref.SHA256 != args.SHA256 {
		return ErrAttachment
	}
	resource := "attachment:" + args.ID
	var messages int
	if err := service.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id=? AND EXISTS (SELECT 1 FROM json_each(body_json,'$.Attachments') WHERE json_extract(value,'$.id')=?)`, args.Conversation, args.ID).Scan(&messages); err != nil {
		return err
	}
	record.Operation.ConversationID = args.Conversation
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: mustJSON(r)}
	record.After = map[string]json.RawMessage{resource: mustJSON(map[string]any{"deleted": true})}
	record.Operation.Preview = fmt.Sprintf("Permanently delete attachment %s (%d bytes, SHA-256 %s). %d message references will display unavailable; message text remains. This cannot be undone except from a backup.", r.Ref.Name, r.Ref.Bytes, r.Ref.SHA256, messages)
	return nil
}
func (service *ControlService) applyAttachmentDelete(ctx context.Context, record *controlRecord) error {
	var args attachmentDeleteArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	store := service.core.Store
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	r, err := loadAttachment(ctx, store.db, args.ID)
	if err != nil || r.Deleted || string(mustJSON(r)) != string(record.Before["attachment:"+args.ID]) {
		return ErrControlStaleRevision
	}
	var active int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE conversation_id=? AND status IN ('running','waiting_confirmation','stopped','unknown','limited')`, r.Conversation).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrActiveRun
	}
	root, err := attachmentRoot(store.dataDir, false)
	if err != nil {
		return err
	}
	defer root.Close()
	// Revoke visibility first. A failed OS removal cannot leak bytes via a
	// still-valid reference, and physical quota accounting includes remnants.
	r.Deleted = true
	if _, err := store.db.ExecContext(ctx, `UPDATE meta SET value=? WHERE key=?`, string(mustJSON(r)), attachmentPrefix+args.ID); err != nil {
		return err
	}
	_ = root.Chmod(args.ID, 0600)
	if err := root.Remove(args.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: reference revoked; file removal failed, inspect local storage")
	}
	return nil
}

type AttachmentInfo struct {
	Ref       AttachmentRef `json:"ref"`
	Committed bool          `json:"committed"`
	Deleted   bool          `json:"deleted"`
}

func (tasks *TaskService) Attachments(ctx context.Context, chat string) ([]AttachmentInfo, error) {
	conversation, err := tasks.attachmentConversation(ctx, chat)
	if err != nil {
		return nil, err
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'attachment/%' AND json_extract(value,'$.conversation')=? ORDER BY key LIMIT 1001`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AttachmentInfo{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r attachmentRecord
		if json.Unmarshal([]byte(raw), &r) != nil {
			return nil, ErrAttachment
		}
		if r.Space == conversation.SpaceID && (r.Committed || r.Principal == attachmentIdentity(ctx)) {
			result = append(result, AttachmentInfo{r.Ref, r.Committed, r.Deleted})
		}
	}
	if len(result) > 1000 {
		return nil, errors.New("harness: attachment list exceeds 1000 entries; use a narrower conversation")
	}
	return result, rows.Err()
}
