package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

type HistoryQuery struct {
	Before int64 `json:"before,omitempty"`
	Limit  int   `json:"limit,omitempty"`
}
type HistoryItem struct {
	ID        int64  `json:"id"`
	RunID     string `json:"run_id,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}
type HistoryPage struct {
	Items   []HistoryItem `json:"items"`
	HasMore bool          `json:"has_more"`
	Before  int64         `json:"before,omitempty"`
}

func (store *Store) HistoryPage(ctx context.Context, chat string, query HistoryQuery) (HistoryPage, error) {
	page := HistoryPage{Items: []HistoryItem{}}
	if query.Limit == 0 {
		query.Limit = 40
	}
	if query.Before < 0 || query.Limit < 1 || query.Limit > 100 {
		return page, errors.New("harness: history requires a nonnegative cursor and 1..100 items")
	}
	if _, err := store.GetConversation(ctx, chat); err != nil {
		return page, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id,coalesce(run_id,''),coalesce(body_json->>'Role',''),substr(coalesce(body_json->>'Content',''),1,16384),length(coalesce(body_json->>'Content',''))>16384
	 FROM fa.messages WHERE conversation_id=$1 AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT $3`, chat, query.Before, query.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.RunID, &item.Role, &item.Content, &item.Truncated); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > query.Limit {
		page.HasMore = true
		page.Items = page.Items[:query.Limit]
	}
	slices.Reverse(page.Items)
	if page.HasMore && len(page.Items) > 0 {
		page.Before = page.Items[0].ID
	}
	return page, nil
}
func (tasks *TaskService) HistoryPage(ctx context.Context, chat string, query HistoryQuery) (HistoryPage, error) {
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return HistoryPage{}, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return HistoryPage{}, ErrConversationSpace
	}
	return tasks.core.Store.HistoryPage(ctx, chat, query)
}

// ExportHistory streams one consistent snapshot to an explicitly selected
// destination. It contains user content; it is never a diagnostic export.
func (store *Store) ExportHistory(ctx context.Context, chat string, writer io.Writer) (int, error) {
	if _, err := store.GetConversation(ctx, chat); err != nil {
		return 0, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,coalesce(run_id,''),body_json FROM fa.messages WHERE conversation_id=$1 ORDER BY id`, chat)
	if err != nil {
		return 0, err
	}
	count := 0
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	for rows.Next() {
		var item Message
		var raw []byte
		if err := rows.Scan(&item.ID, &item.RunID, &raw); err != nil {
			rows.Close()
			return count, err
		}
		if err := json.Unmarshal(raw, &item.ModelMessage); err != nil {
			rows.Close()
			return count, err
		}
		if err := encoder.Encode(item); err != nil {
			rows.Close()
			return count, err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return count, err
	}
	return count, tx.Commit()
}
