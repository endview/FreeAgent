package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ConversationQuery struct {
	Workspace string `json:"workspace"`
	SpaceID   string `json:"space_id,omitempty"`
	Query     string `json:"query,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}
type ConversationPage struct {
	Items       []Conversation `json:"items"`
	NextCursor  string         `json:"next_cursor,omitempty"`
	HasMore     bool           `json:"has_more"`
	SearchScope string         `json:"search_scope"`
}
type conversationCursor struct{ Scope, Pin, Updated, ID string }

// ListConversationPage is a bounded metadata query. Its cursor describes a
// position, not a locked snapshot; callers deduplicate IDs after pin changes.
func (store *Store) ListConversationPage(ctx context.Context, query ConversationQuery) (ConversationPage, error) {
	page := ConversationPage{Items: []Conversation{}, SearchScope: "titles"}
	query.Query = strings.TrimSpace(query.Query)
	if query.Limit == 0 {
		query.Limit = 60
	}
	if query.Limit < 1 || query.Limit > 100 || len(query.Query) > 256 || len(query.Cursor) > 4096 {
		return page, errors.New("harness: invalid conversation page size, search or cursor")
	}
	scope := contentHash(mustJSON([]string{query.Workspace, query.SpaceID, query.Query}))
	cursor := conversationCursor{}
	if query.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Scope != scope || cursor.ID == "" || cursor.Updated == "" {
			return page, errors.New("harness: conversation cursor does not match the current filter")
		}
	}
	where := ` WHERE ($1='' OR c.workspace=$1) AND ($2='' OR c.space_id=$2)`
	args := []any{query.Workspace, query.SpaceID}
	if query.Query != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query.Query)+"%")
		where += fmt.Sprintf(` AND c.title ILIKE $%d ESCAPE '\'`, len(args))
	}
	if query.Cursor != "" {
		start := len(args) + 1
		args = append(args, cursor.Pin, cursor.Updated, cursor.ID)
		where += fmt.Sprintf(` AND (coalesce(pin.value,''),c.updated_at,c.id)<($%d,$%d,$%d)`, start, start+1, start+2)
	}
	args = append(args, query.Limit+1)
	rows, err := store.db.QueryContext(ctx, `SELECT c.id,c.title,c.workspace,c.space_id,c.created_at,c.updated_at,coalesce(model.value,''),coalesce(pin.value,'')
     FROM fa.conversations c LEFT JOIN fa.meta pin ON pin.key='chat/pin/'||c.id
     LEFT JOIN fa.meta model ON model.key='chat/model/'||c.id`+where+fmt.Sprintf(` ORDER BY coalesce(pin.value,'') DESC,c.updated_at DESC,c.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Conversation
		if err := rows.Scan(&item.ID, &item.Title, &item.Workspace, &item.SpaceID, &item.CreatedAt, &item.UpdatedAt, &item.ModelID, &item.PinnedAt); err != nil {
			return page, err
		}
		item.Pinned = item.PinnedAt != ""
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > query.Limit {
		page.HasMore = true
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString(mustJSON(conversationCursor{scope, last.PinnedAt, last.UpdatedAt, last.ID}))
	}
	return page, nil
}

func (tasks *TaskService) ConversationPage(ctx context.Context, query ConversationQuery) (ConversationPage, error) {
	query.Workspace = tasks.Config().Workspace
	if query.SpaceID != "" {
		if _, err := tasks.Space(ctx, query.SpaceID); err != nil {
			return ConversationPage{}, err
		}
	}
	return tasks.core.Store.ListConversationPage(ctx, query)
}
