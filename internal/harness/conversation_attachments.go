package harness

import (
	"context"
	"encoding/json"
	"errors"
)

type ManagedAttachment struct {
	Ref            AttachmentRef `json:"ref"`
	ConversationID string        `json:"conversation_id"`
	Title          string        `json:"title"`
	References     int           `json:"references"`
	Deleted        bool          `json:"deleted"`
}
type ManagedAttachmentPage struct {
	Items      []ManagedAttachment `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// Only the interactive management surface may read a trashed attachment.
// The normal read path keeps its lifecycle and audience checks.
func (tasks *TaskService) ManagedAttachment(ctx context.Context, chat, id string) (AttachmentRef, []byte, error) {
	if err := tasks.managementOwner(); err != nil {
		return AttachmentRef{}, nil, err
	}
	life, err := readLifecycle(ctx, tasks.core.Store.db, chat)
	if err != nil {
		return AttachmentRef{}, nil, err
	}
	if life == nil || life.State != "trashed" {
		return AttachmentRef{}, nil, ErrConversationRetired
	}
	return tasks.Attachment(context.WithValue(ctx, managementAccessKey{}, true), chat, id)
}

func (tasks *TaskService) ManagedAttachments(ctx context.Context, space, cursor string) (ManagedAttachmentPage, error) {
	page := ManagedAttachmentPage{Items: []ManagedAttachment{}}
	if err := tasks.managementOwner(); err != nil {
		return page, err
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT a.value,c.title,(SELECT count(*) FROM fa.messages m WHERE m.conversation_id=c.id AND EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(m.body_json->'Attachments','[]')) x WHERE x->>'id'=a.document#>>'{ref,id}')) FROM fa.meta a JOIN fa.conversations c ON c.id=a.document->>'conversation' WHERE a.key LIKE 'attachment/%' AND c.workspace=$1 AND ($2='' OR c.space_id=$2) AND fa.conversation_live(c.id) AND a.key>$3 ORDER BY a.key LIMIT 101`, tasks.Config().Workspace, space, cursor)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var item ManagedAttachment
		if err = rows.Scan(&raw, &item.Title, &item.References); err != nil {
			return page, err
		}
		var a attachmentRecord
		if json.Unmarshal([]byte(raw), &a) != nil {
			return page, ErrAttachment
		}
		item.Ref = a.Ref
		item.ConversationID = a.Conversation
		item.Deleted = a.Deleted
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > 100 {
		page.Items = page.Items[:100]
		page.NextCursor = attachmentPrefix + page.Items[99].Ref.ID
	}
	return page, nil
}

// Managed status is also used by clients returning from sleep to retire stale
// local drafts without interpreting a network failure as a deleted record.
func (tasks *TaskService) ConversationLifecycleStatus(ctx context.Context, id string) (*ConversationLifecycle, error) {
	if err := tasks.managementOwner(); err != nil {
		return nil, err
	}
	life, err := readLifecycle(ctx, tasks.core.Store.db, id)
	if err != nil {
		return nil, err
	}
	if life == nil {
		return nil, errors.New("harness: conversation has no lifecycle change")
	}
	return life, nil
}
