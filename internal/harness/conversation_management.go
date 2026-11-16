package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const lifecyclePrefix = "conversation/lifecycle/"
const managementActivitySQL = `greatest(c.created_at,coalesce((SELECT max(r.updated_at) FROM fa.runs r WHERE r.conversation_id=c.id),''))`
const managementEntrySQL = `coalesce((SELECT coalesce(nullif(r.config_doc#>>'{Permission,entry}',''),'local') FROM fa.runs r WHERE r.conversation_id=c.id ORDER BY r.seq DESC LIMIT 1),'unrecorded')`

var ErrConversationRetired = errors.New("harness: conversation is in trash or permanently deleted")
var ErrManagementStale = errors.New("harness: conversation management preview changed; review again")

type ConversationLifecycle struct {
	State         string `json:"state"`
	Generation    string `json:"generation"`
	TrashedAt     string `json:"trashed_at,omitempty"`
	PurgeAfter    string `json:"purge_after,omitempty"`
	OriginalSpace string `json:"original_space,omitempty"`
	OriginalTitle string `json:"original_title,omitempty"`
	Operation     string `json:"operation,omitempty"`
}
type ManagementQuery struct {
	View    string `json:"view,omitempty"`
	SpaceID string `json:"space_id,omitempty"`
	Query   string `json:"q,omitempty"`
	Before  string `json:"before,omitempty"`
	Entry   string `json:"entry,omitempty"`
	Filter  string `json:"filter,omitempty"`
	Sort    string `json:"sort,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type ManagedConversation struct {
	Conversation
	Lifecycle        *ConversationLifecycle `json:"lifecycle,omitempty"`
	Runs             int64                  `json:"runs"`
	Messages         int64                  `json:"messages"`
	AttachmentBytes  int64                  `json:"attachment_bytes"`
	Attachments      int64                  `json:"attachments"`
	CleanupBytes     int64                  `json:"cleanup_bytes"`
	CleanupFiles     int64                  `json:"cleanup_files"`
	RetainedAttempts int64                  `json:"retained_attempts"`
	RetainedBytes    int64                  `json:"retained_bytes"`
	DeliveryRecords  int64                  `json:"delivery_records"`
	Unresolved       int64                  `json:"unresolved"`
	LatestEntry      string                 `json:"latest_entry"`
	ActivityAt       string                 `json:"activity_at"`
	Blockers         []string               `json:"blockers"`
	ActiveRuns       []string               `json:"active_runs"`
	Revision         string                 `json:"revision"`
}
type ManagementPage struct {
	Items          []ManagedConversation   `json:"items"`
	Total          int                     `json:"total"`
	TrashTotal     int                     `json:"trash_total"`
	NextCursor     string                  `json:"next_cursor,omitempty"`
	OriginalSpaces []ManagementSpaceOption `json:"original_spaces,omitempty"`
	CleanupStatus  json.RawMessage         `json:"cleanup_status,omitempty"`
}
type ManagementSpaceOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type ManagementCommand struct {
	Action             string   `json:"action"`
	IDs                []string `json:"ids,omitempty"`
	SpaceID            string   `json:"space_id,omitempty"`
	Title              string   `json:"title,omitempty"`
	ExpectedTitle      string   `json:"expected_title,omitempty"`
	Replacement        string   `json:"replacement,omitempty"` // empty requires explicit "new" when routed
	CancelPending      bool     `json:"cancel_pending,omitempty"`
	TrashConversations *bool    `json:"trash_conversations,omitempty"`
}
type ManagementPreview struct {
	ID             string                `json:"id"`
	Command        ManagementCommand     `json:"command"`
	Items          []ManagedConversation `json:"items"`
	ExpiresAt      string                `json:"expires_at"`
	SpaceRevision  string                `json:"space_revision,omitempty"`
	TargetRevision string                `json:"target_revision,omitempty"`
	Warning        string                `json:"warning"`
}
type ManagementResult struct {
	ID    string                 `json:"id"`
	Items []ManagementItemResult `json:"items"`
}
type ManagementItemResult struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Replacement string `json:"replacement,omitempty"`
}
type managementReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type managementAccessKey struct{}

// The second statement reads lifecycle after the row lock has been acquired,
// including when an earlier retirement transaction held that lock.
func lockLiveConversationTx(ctx context.Context, tx *sql.Tx, id string) error {
	if err := lockConversationTx(ctx, tx, id); err != nil {
		return err
	}
	life, err := readLifecycle(ctx, tx, id)
	if err != nil {
		return err
	}
	if life != nil && (life.State == "trashed" || life.State == "purged") {
		return ErrConversationRetired
	}
	return nil
}

func readLifecycle(ctx context.Context, r managementReader, id string) (*ConversationLifecycle, error) {
	var raw string
	err := r.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, lifecyclePrefix+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var life ConversationLifecycle
	if json.Unmarshal([]byte(raw), &life) != nil {
		return nil, ErrStoreFormat
	}
	return &life, nil
}
func (store *Store) checkLiveConversation(ctx context.Context, id string) error {
	if yes, _ := ctx.Value(managementAccessKey{}).(bool); yes {
		return nil
	}
	life, err := readLifecycle(ctx, store.db, id)
	if err != nil {
		return err
	}
	if life != nil && (life.State == "trashed" || life.State == "purged") {
		return ErrConversationRetired
	}
	return nil
}
func (tasks *TaskService) managementOwner() error {
	if tasks == nil || tasks.core == nil || tasks.core.permission != nil && tasks.core.scopedHistory() {
		return ErrPermissionDenied
	}
	return nil
}

// managedConversation is computed from authority records; no second history is stored.
func managedConversation(ctx context.Context, r managementReader, id string) (ManagedConversation, error) {
	var item ManagedConversation
	item.Blockers = []string{}
	var pin string
	err := r.QueryRowContext(ctx, `SELECT c.id,c.title,c.workspace,c.space_id,c.created_at,c.updated_at,coalesce(p.value,''),coalesce(m.value,'') FROM fa.conversations c LEFT JOIN fa.meta p ON p.key='chat/pin/'||c.id LEFT JOIN fa.meta m ON m.key='chat/model/'||c.id WHERE c.id=$1`, id).Scan(&item.ID, &item.Title, &item.Workspace, &item.SpaceID, &item.CreatedAt, &item.UpdatedAt, &pin, &item.ModelID)
	if err != nil {
		return item, err
	}
	item.Pinned = pin != ""
	item.PinnedAt = pin
	item.Lifecycle, err = readLifecycle(ctx, r, id)
	if err != nil {
		return item, err
	}
	var active, commands, queued, deliveries, dispatches int
	err = r.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM fa.runs WHERE conversation_id=$1),
 (SELECT count(*) FROM fa.messages WHERE conversation_id=$1),
 (SELECT count(*) FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate+`),
 (SELECT count(*) FROM fa.meta WHERE key LIKE 'command/process/%' AND document->>'conversation_id'=$1 AND (document->>'state' IN ('running','stopping') OR document->>'state'='unknown' AND coalesce((document->>'reviewed')::boolean,false)=false)),
 (SELECT count(*) FROM fa.meta WHERE starts_with(key,'input/'||$1||'/') AND document->>'status'='pending'),
 (SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 AND document->>'state' IN ('pending','paused','scheduled','held','reviewing','dispatching')),
 (SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 AND document->>'state'='dispatching'),
 (SELECT count(*) FROM fa.meta WHERE key LIKE 'attempt/unresolved/%' AND document->>'conversation_id'=$1 AND coalesce(document->>'resolution','')=''),
 (SELECT count(*) FROM fa.meta WHERE key LIKE 'attachment/%' AND document->>'conversation'=$1 AND coalesce((document->>'deleted')::boolean,false)=false),
 (SELECT coalesce(sum((document#>>'{ref,bytes}')::bigint),0) FROM fa.meta WHERE key LIKE 'attachment/%' AND document->>'conversation'=$1 AND coalesce((document->>'deleted')::boolean,false)=false)`, id).Scan(&item.Runs, &item.Messages, &active, &commands, &queued, &deliveries, &dispatches, &item.Unresolved, &item.Attachments, &item.AttachmentBytes)
	if err != nil {
		return item, err
	}
	if err = r.QueryRowContext(ctx, `SELECT count(*),coalesce(sum((a.document#>>'{ref,bytes}')::bigint),0) FROM fa.meta a JOIN fa.meta f ON f.key='conversation/file-cleanup/'||(a.document#>>'{ref,id}') WHERE a.key LIKE 'attachment/%' AND a.document->>'conversation'=$1`, id).Scan(&item.CleanupFiles, &item.CleanupBytes); err != nil {
		return item, err
	}
	if err = r.QueryRowContext(ctx, `WITH evidence AS (SELECT a.* FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id WHERE r.conversation_id=$1 AND (a.status IN ('unknown','dispatched') OR EXISTS(SELECT 1 FROM fa.meta u WHERE u.key='attempt/unresolved/'||a.id))) SELECT
 (SELECT count(*) FROM evidence),
 (SELECT coalesce(sum(octet_length(request_json)+octet_length(response_json)),0) FROM evidence)+(SELECT coalesce(sum(octet_length(r.config_json)+octet_length(r.prompt)+octet_length(r.output)),0) FROM fa.runs r WHERE r.id IN (SELECT run_id FROM evidence)),
 (SELECT count(*) FROM fa.meta m WHERE m.key LIKE 'channel/operation/%' AND m.document->>'conversation_id'=$1)`, id).Scan(&item.RetainedAttempts, &item.RetainedBytes, &item.DeliveryRecords); err != nil {
		return item, err
	}
	if active > 0 {
		item.Blockers = append(item.Blockers, "unfinished_run")
	}
	var activeJSON string
	if err = r.QueryRowContext(ctx, `SELECT coalesce(jsonb_agg(id ORDER BY id),'[]')::text FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate, id).Scan(&activeJSON); err != nil {
		return item, err
	}
	if json.Unmarshal([]byte(activeJSON), &item.ActiveRuns) != nil {
		return item, ErrStoreFormat
	}
	if commands > 0 {
		item.Blockers = append(item.Blockers, "background_command")
	}
	if queued > 0 {
		item.Blockers = append(item.Blockers, "queued_input")
	}
	if deliveries > 0 {
		item.Blockers = append(item.Blockers, "pending_delivery")
	}
	if dispatches > 0 {
		item.Blockers = append(item.Blockers, "dispatching")
	}
	var routed bool
	if err = r.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.spaces WHERE source<>'local' AND (active_conversation_id=$1 OR default_conversation_id=$1))`, id).Scan(&routed); err != nil {
		return item, err
	}
	if routed {
		item.Blockers = append(item.Blockers, "channel_route")
	}
	if err = r.QueryRowContext(ctx, `SELECT `+managementActivitySQL+`,`+managementEntrySQL+` FROM fa.conversations c WHERE c.id=$1`, id).Scan(&item.ActivityAt, &item.LatestEntry); err != nil {
		return item, err
	}
	// Bind relevant records, not unrelated conversation activity. JSON aggregation
	// order is explicit so the digest is stable across query plans.
	var raw string
	err = r.QueryRowContext(ctx, `SELECT jsonb_build_array(
 (SELECT to_jsonb(c) FROM fa.conversations c WHERE id=$1),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(r.id,r.status,r.updated_at,r.steps) ORDER BY r.id),'[]') FROM fa.runs r WHERE conversation_id=$1),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(a.id,a.status,encode(sha256(convert_to(a.response_json,'UTF8')),'hex')) ORDER BY a.id),'[]') FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id WHERE r.conversation_id=$1 AND a.status IN ('pending','dispatched','unknown')),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(key,value) ORDER BY key),'[]') FROM fa.meta WHERE key=$2 OR key IN ('chat/pin/'||$1,'chat/model/'||$1,'chat/capabilities/'||$1) OR starts_with(key,'input/'||$1||'/') OR key LIKE 'attachment/%' AND document->>'conversation'=$1 OR key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 OR key LIKE 'command/process/%' AND document->>'conversation_id'=$1),
 (SELECT jsonb_build_array(s.id,s.source,s.chat_kind,s.external_chat,s.source<>'local' AND s.active_conversation_id=$1,s.source<>'local' AND s.default_conversation_id=$1,s.multi_conversation_enabled) FROM fa.spaces s WHERE id=(SELECT space_id FROM fa.conversations WHERE id=$1)),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(key,value) ORDER BY key),'[]') FROM fa.meta WHERE key='space/permission/'||(SELECT space_id FROM fa.conversations WHERE id=$1) OR key LIKE 'telegram/access/%' AND document->>'space_id'=(SELECT space_id FROM fa.conversations WHERE id=$1))
 )::text`, id, lifecyclePrefix+id).Scan(&raw)
	item.Revision = contentHash([]byte(raw))
	return item, err
}

func (tasks *TaskService) ManageConversations(ctx context.Context, q ManagementQuery) (ManagementPage, error) {
	page := ManagementPage{Items: []ManagedConversation{}}
	if err := tasks.managementOwner(); err != nil {
		return page, err
	}
	if q.Limit == 0 {
		q.Limit = 60
	}
	if q.Limit < 1 || q.Limit > 500 || len(q.Query) > 256 {
		return page, ErrRequestKey
	}
	if q.View == "" {
		q.View = "active"
	}
	if q.View != "active" && q.View != "trash" && q.View != "retained" {
		return page, ErrRequestKey
	}
	var maintenance string
	if e := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='conversation/cleanup-status'`).Scan(&maintenance); e == nil {
		page.CleanupStatus = json.RawMessage(maintenance)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return page, e
	}
	if q.Before != "" {
		parsed, e := time.Parse(time.RFC3339Nano, q.Before)
		if e != nil {
			return page, ErrRequestKey
		}
		q.Before = parsed.UTC().Format(time.RFC3339Nano)
	}
	if q.View == "trash" {
		rows, e := tasks.core.Store.db.QueryContext(ctx, `SELECT DISTINCT l.document->>'original_space',l.document->>'original_title' FROM fa.meta l JOIN fa.conversations c ON l.key='conversation/lifecycle/'||c.id WHERE c.workspace=$1 AND l.document->>'state'='trashed' ORDER BY 1,2 LIMIT 500`, tasks.Config().Workspace)
		if e != nil {
			return page, e
		}
		for rows.Next() {
			var option ManagementSpaceOption
			if e = rows.Scan(&option.ID, &option.Title); e != nil {
				rows.Close()
				return page, e
			}
			page.OriginalSpaces = append(page.OriginalSpaces, option)
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return page, e
		}
	}
	switch q.Entry {
	case "", "local", "telegram", "lark", "qq", "unrecorded":
	default:
		return page, ErrRequestKey
	}
	where := ` FROM fa.conversations c LEFT JOIN fa.meta l ON l.key='conversation/lifecycle/'||c.id WHERE c.workspace=$1 AND ($2='' OR CASE WHEN $3='active' THEN c.space_id ELSE l.document->>'original_space' END=$2) AND CASE $3 WHEN 'trash' THEN l.document->>'state'='trashed' WHEN 'retained' THEN l.document->>'state'='purged' ELSE coalesce(l.document->>'state','active')='active' END AND ($4='' OR strpos(lower(c.title),lower($4))>0) AND ($5='' OR ` + managementActivitySQL + `<$5) AND ($6='' OR ` + managementEntrySQL + `=$6)`
	if q.View == "retained" {
		where += ` AND (EXISTS(SELECT 1 FROM fa.runs r WHERE r.conversation_id=c.id) OR EXISTS(SELECT 1 FROM fa.meta e WHERE (e.key LIKE 'channel/operation/%' AND e.document->>'conversation_id'=c.id) OR (e.key LIKE 'conversation/file-cleanup/%' AND e.document->>'conversation'=c.id)))`
	}
	switch q.Filter {
	case "", "all":
	case "pinned":
		where += ` AND EXISTS(SELECT 1 FROM fa.meta p WHERE p.key='chat/pin/'||c.id AND p.value<>'')`
	case "unpinned":
		where += ` AND NOT EXISTS(SELECT 1 FROM fa.meta p WHERE p.key='chat/pin/'||c.id AND p.value<>'')`
	case "empty":
		where += ` AND NOT EXISTS(SELECT 1 FROM fa.messages m WHERE m.conversation_id=c.id)`
	case "branch":
		where += ` AND EXISTS(SELECT 1 FROM fa.meta p WHERE p.key='conversation/branch/'||c.id)`
	case "attachments":
		where += ` AND EXISTS(SELECT 1 FROM fa.meta a WHERE a.key LIKE 'attachment/%' AND a.document->>'conversation'=c.id AND coalesce((a.document->>'deleted')::boolean,false)=false)`
	case "blocked":
		where += ` AND (EXISTS(SELECT 1 FROM fa.runs WHERE conversation_id=c.id AND ` + activeRunPredicate + `) OR EXISTS(SELECT 1 FROM fa.meta p WHERE (starts_with(p.key,'input/'||c.id||'/') AND p.document->>'status'='pending') OR (p.key LIKE 'channel/operation/%' AND p.document->>'conversation_id'=c.id AND p.document->>'state' IN ('pending','paused','scheduled','held','reviewing','dispatching')) OR (p.key LIKE 'command/process/%' AND p.document->>'conversation_id'=c.id AND (p.document->>'state' IN ('running','stopping') OR p.document->>'state'='unknown' AND coalesce((p.document->>'reviewed')::boolean,false)=false))))`
	default:
		return page, ErrRequestKey
	}
	order := `CASE WHEN $3='trash' THEN coalesce(l.document->>'trashed_at','') ELSE ` + managementActivitySQL + ` END DESC,c.id DESC`
	switch q.Sort {
	case "bytes":
		order = `(SELECT coalesce(sum((a.document#>>'{ref,bytes}')::bigint),0) FROM fa.meta a WHERE a.key LIKE 'attachment/%' AND a.document->>'conversation'=c.id AND coalesce((a.document->>'deleted')::boolean,false)=false) DESC,c.id DESC`
	case "runs":
		order = `(SELECT count(*) FROM fa.runs WHERE conversation_id=c.id) DESC,c.id DESC`
	case "", "recent":
	default:
		return page, ErrRequestKey
	}
	args := []any{tasks.Config().Workspace, q.SpaceID, q.View, q.Query, q.Before, q.Entry}
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `WITH candidates AS (SELECT c.id,row_number() OVER(ORDER BY `+order+`) AS pos`+where+`) SELECT id FROM candidates WHERE pos>coalesce((SELECT pos FROM candidates WHERE id=$7),0) ORDER BY pos LIMIT $8`, append(args, q.Cursor, q.Limit+1)...)
	if err != nil {
		return page, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return page, err
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return page, err
	}
	if len(ids) > q.Limit {
		page.NextCursor = ids[q.Limit-1]
		ids = ids[:q.Limit]
	}
	for _, id := range ids {
		item, e := managedConversation(ctx, tasks.core.Store.db, id)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, item)
	}
	_ = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta l JOIN fa.conversations c ON c.id=substr(l.key,length($1)+1) WHERE starts_with(l.key,$1) AND l.document->>'state'='trashed' AND c.workspace=$2`, lifecyclePrefix, tasks.Config().Workspace).Scan(&page.TrashTotal)
	return page, nil
}

func (tasks *TaskService) PreviewConversationManagement(ctx context.Context, c ManagementCommand) (ManagementPreview, error) {
	p := ManagementPreview{ID: newID(), Command: c, Items: []ManagedConversation{}, ExpiresAt: time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano), Warning: "移到回收站保留 7 天。工作目录、独立 Memory 与扩展数据分别管理。执行证据与渠道去重按需保留，删除不授权重做。"}
	if err := tasks.managementOwner(); err != nil {
		return p, err
	}
	switch c.Action {
	case "trash", "restore", "purge", "move", "rename", "pin", "unpin", "delete_space":
	default:
		return p, ErrRequestKey
	}
	if c.Action == "delete_space" {
		if c.SpaceID == LocalSpaceID || c.TrashConversations == nil {
			return p, ErrInvalidSpace
		}
		rows, e := tasks.core.Store.db.QueryContext(ctx, `SELECT id FROM fa.conversations WHERE space_id=$1 ORDER BY id LIMIT 5001`, c.SpaceID)
		if e != nil {
			return p, e
		}
		p.Command.IDs = nil
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return p, e
			}
			p.Command.IDs = append(p.Command.IDs, id)
		}
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			return p, e
		}
		var raw string
		if e = tasks.core.Store.db.QueryRowContext(ctx, `SELECT row_to_json(s)::text FROM fa.spaces s WHERE id=$1`, c.SpaceID).Scan(&raw); e != nil {
			return p, e
		}
		p.SpaceRevision = contentHash([]byte(raw))
		p.Warning += " 删除空间立即解除该渠道授权；空间设置不在回收站中恢复。"
	}
	if len(p.Command.IDs) > 500 && c.Action != "delete_space" || len(p.Command.IDs) > 5000 {
		return p, errors.New("harness: each batch supports 500 conversations; deleting a space supports up to 5000 conversation records")
	}
	if c.Action != "delete_space" && len(c.IDs) == 0 {
		return p, ErrRequestKey
	}
	seen := map[string]bool{}
	for _, id := range p.Command.IDs {
		if seen[id] {
			return p, ErrRequestKey
		}
		seen[id] = true
		item, e := managedConversation(ctx, tasks.core.Store.db, id)
		if e != nil {
			return p, e
		}
		if item.Workspace != tasks.Config().Workspace {
			return p, ErrConversationSpace
		}
		if c.Action == "rename" && c.ExpectedTitle != "" && item.Title != c.ExpectedTitle {
			return p, ErrManagementStale
		}
		p.Items = append(p.Items, item)
	}
	if c.Action == "move" || c.Action == "restore" && c.SpaceID != "" {
		s, e := tasks.core.Store.GetSpace(ctx, c.SpaceID)
		if e != nil {
			return p, e
		}
		if s.Source != SpaceSourceLocal {
			p.Warning += " 目标是渠道空间：确认后符合受众规则的历史正文和附件可用于该渠道后续回答。非 Owner 私聊空间撤销原私人能力选择并保存为空，不回退全局默认。"
		}
		p.TargetRevision, e = managementSpaceRevision(ctx, tasks.core.Store.db, c.SpaceID)
		if e != nil {
			return p, e
		}
	}
	raw := mustJSON(p)
	_, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "conversation/management/"+p.ID, string(raw))
	return p, err
}

func (tasks *TaskService) ApplyConversationManagement(ctx context.Context, id string) (ManagementResult, error) {
	result := ManagementResult{ID: id, Items: []ManagementItemResult{}}
	if err := tasks.managementOwner(); err != nil {
		return result, err
	}
	var raw string
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management-result/"+id).Scan(&raw); err == nil {
		err = json.Unmarshal([]byte(raw), &result)
		return result, err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management/"+id).Scan(&raw); err != nil {
		return result, err
	}
	var p ManagementPreview
	if json.Unmarshal([]byte(raw), &p) != nil {
		return result, ErrStoreFormat
	}
	expires, e := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if e != nil || time.Now().After(expires) {
		return result, ErrManagementStale
	}
	if p.Command.Action == "delete_space" {
		return tasks.applyManagedSpaceDeletion(ctx, p)
	}
	for _, item := range p.Items {
		entry := ManagementItemResult{ID: item.ID, Status: "completed"}
		// Each item has a durable receipt: retry after a lost HTTP response never
		// applies an old trash request to a conversation which was restored later.
		key := "conversation/management-item/" + id + "/" + item.ID
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw); err == nil {
			if json.Unmarshal([]byte(raw), &entry) != nil {
				return result, ErrStoreFormat
			}
			result.Items = append(result.Items, entry)
			continue
		}
		replacement, err := tasks.applyManagedItem(ctx, p, item, key)
		if errors.Is(err, ErrUnknownResult) {
			return result, err
		}
		if err != nil {
			entry.Status = "skipped"
			entry.Message = err.Error()
		} else {
			entry.Replacement = replacement
			entry.Message = "操作已完成；执行证据按需保留"
			var saved string
			if e := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&saved); e == nil {
				_ = json.Unmarshal([]byte(saved), &entry)
			}
		}
		result.Items = append(result.Items, entry)
	}
	_, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, "conversation/management-result/"+id, string(mustJSON(result)))
	tasks.core.event(Event{Kind: EventRefresh, Message: "会话管理状态已更新"})
	return result, err
}

func (tasks *TaskService) ConversationManagementResult(ctx context.Context, id string) (ManagementResult, error) {
	var r ManagementResult
	if err := tasks.managementOwner(); err != nil {
		return r, err
	}
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management-result/"+id).Scan(&raw)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(raw), &r)
	return r, err
}

func (tasks *TaskService) applyManagedItem(ctx context.Context, p ManagementPreview, item ManagedConversation, receipt string) (string, error) {
	s := tasks.core.Store
	if item.Workspace != tasks.Config().Workspace {
		return "", ErrManagementStale
	}
	// Serialize with branch/upload/backup and execution. TryLock does not hold
	// this operation open behind an unbounded model or command request.
	if p.Command.Action != "rename" && p.Command.Action != "pin" && p.Command.Action != "unpin" {
		release, leaseErr := s.runMu.acquire(item.ID, false)
		if leaseErr != nil {
			return "", leaseErr
		}
		defer release()
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = lockConversationTx(ctx, tx, item.ID); err != nil {
		return "", err
	}
	var priorRaw string
	if e := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, receipt).Scan(&priorRaw); e == nil {
		var prior ManagementItemResult
		if json.Unmarshal([]byte(priorRaw), &prior) != nil {
			return "", ErrStoreFormat
		}
		return prior.Replacement, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	current, err := managedConversation(ctx, tx, item.ID)
	if err != nil {
		return "", err
	}
	metadata := p.Command.Action == "rename" || p.Command.Action == "pin" || p.Command.Action == "unpin"
	if (!metadata && current.Revision != item.Revision) || (metadata && (current.Title != item.Title || current.Pinned != item.Pinned || current.SpaceID != item.SpaceID || (current.Lifecycle != nil && current.Lifecycle.State != "active"))) {
		return "", ErrManagementStale
	}
	if p.TargetRevision != "" {
		revision, e := managementSpaceRevision(ctx, tx, p.Command.SpaceID)
		if e != nil {
			return "", e
		}
		if revision != p.TargetRevision {
			return "", ErrManagementStale
		}
	}
	replacement, err := tasks.mutateManagedItem(ctx, tx, p, current)
	if err != nil {
		return "", err
	}
	entry := ManagementItemResult{ID: item.ID, Status: "completed", Message: "操作已完成；执行证据按需保留", Replacement: replacement}
	if p.Command.Action == "restore" && item.Pinned {
		var pinned bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, "chat/pin/"+item.ID).Scan(&pinned); err != nil {
			return "", err
		}
		if !pinned {
			entry.Message = "会话已恢复；置顶名额已满，未恢复置顶。"
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, receipt, string(mustJSON(entry))); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", errors.Join(ErrUnknownResult, fmt.Errorf("harness: management commit unconfirmed; inspect operation %s: %w", p.ID, err))
	}
	if p.Command.Action == "purge" {
		_ = s.cleanupRetiredFiles(ctx)
	}
	return replacement, nil
}

func saveLifecycle(ctx context.Context, tx *sql.Tx, id string, l ConversationLifecycle) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, lifecyclePrefix+id, string(mustJSON(l)))
	return err
}

func managementSpaceRevision(ctx context.Context, r managementReader, id string) (string, error) {
	var raw string
	err := r.QueryRowContext(ctx, `SELECT jsonb_build_array(s.id,s.title,s.source,s.chat_kind,s.external_chat,(SELECT document FROM fa.meta WHERE key='space/permission/'||s.id),(SELECT coalesce(jsonb_agg(document ORDER BY key),'[]') FROM fa.meta WHERE key LIKE 'telegram/access/%' AND document->>'space_id'=s.id))::text FROM fa.spaces s WHERE s.id=$1`, id).Scan(&raw)
	return contentHash([]byte(raw)), err
}

func (tasks *TaskService) mutateManagedItem(ctx context.Context, tx *sql.Tx, p ManagementPreview, item ManagedConversation) (string, error) {
	c := p.Command
	retired := item.Lifecycle != nil && (item.Lifecycle.State == "trashed" || item.Lifecycle.State == "purged")
	if c.Action == "restore" || c.Action == "purge" {
		if item.Lifecycle == nil || item.Lifecycle.State != "trashed" {
			return "", ErrConversationRetired
		}
	} else if retired {
		return "", ErrConversationRetired
	}
	if c.Action == "move" && c.SpaceID == item.SpaceID {
		return "", ErrConversationSpace
	}
	switch c.Action {
	case "rename":
		title := strings.TrimSpace(c.Title)
		if title == "" || len([]rune(title)) > 80 || strings.ContainsAny(title, "\r\n") || hasControl(title) {
			return "", ErrInvalidConversationTitle
		}
		_, err := tx.ExecContext(ctx, `UPDATE fa.conversations SET title=$1 WHERE id=$2`, title, item.ID)
		return "", err
	case "pin", "unpin":
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('conversation/pin-limit',0))`); err != nil {
			return "", err
		}
		if c.Action == "unpin" {
			_, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, "chat/pin/"+item.ID)
			return "", err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta p JOIN fa.conversations c ON p.key='chat/pin/'||c.id WHERE p.value<>'' AND fa.conversation_live(c.id)`).Scan(&n); err != nil {
			return "", err
		}
		if !item.Pinned && n >= 50 {
			return "", ErrPinnedLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, "chat/pin/"+item.ID, nowText())
		return "", err
	}
	if c.Action == "restore" {
		target := c.SpaceID
		if target == "" {
			target = item.Lifecycle.OriginalSpace
		}
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT source FROM fa.spaces WHERE id=$1`, target).Scan(&source); errors.Is(err, sql.ErrNoRows) {
			target = LocalSpaceID
			source = SpaceSourceLocal
		} else if err != nil {
			return "", err
		}
		life := ConversationLifecycle{State: "active", Generation: newID(), Operation: p.ID}
		if source != SpaceSourceLocal {
			if err := protectMovedCapabilities(ctx, tx, item.ID, target); err != nil {
				return "", err
			}
		}
		if err := saveLifecycle(ctx, tx, item.ID, life); err != nil {
			return "", err
		}
		if item.Pinned {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('conversation/pin-limit',0))`); err != nil {
				return "", err
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta p JOIN fa.conversations c ON p.key='chat/pin/'||c.id WHERE p.value<>'' AND fa.conversation_live(c.id)`).Scan(&count); err != nil {
				return "", err
			}
			if count > 50 {
				if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, "chat/pin/"+item.ID); err != nil {
					return "", err
				}
			}
		}
		if target != item.Lifecycle.OriginalSpace && source != SpaceSourceLocal {
			if err := publishMovedText(ctx, tx, item.ID, target); err != nil {
				return "", err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE starts_with(key,$1) OR key=$2`, summaryMetaPrefix+item.ID+"/", "chat/memory/"+item.ID); err != nil {
				return "", err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fa.conversations SET space_id=$1 WHERE id=$2`, target, item.ID); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{space}',to_jsonb($1::text))::text WHERE key LIKE 'attachment/%' AND document->>'conversation'=$2`, target, item.ID); err != nil {
			return "", err
		}
		return "", nil
	}
	if c.Action == "purge" {
		return "", tasks.purgeConversationTx(ctx, tx, item, p.ID)
	}
	for _, b := range item.Blockers {
		if b == "unfinished_run" {
			return "", ErrActiveRun
		}
		if b == "background_command" {
			return "", ErrCommandBusy
		}
		if b == "dispatching" {
			return "", errors.New("harness: delivery is being dispatched; wait for its receipt")
		}
		if (b == "queued_input" || b == "pending_delivery") && !c.CancelPending {
			return "", errors.New("harness: confirm cancellation of queued input and unsent deliveries")
		}
	}
	var replacement string
	for _, b := range item.Blockers {
		if b == "channel_route" {
			var err error
			replacement, err = tasks.replaceManagedRoute(ctx, tx, item, c.Replacement, p.ID)
			if err != nil {
				return "", err
			}
		}
	}
	if c.Action == "move" {
		if c.CancelPending {
			if err := cancelManagedPending(ctx, tx, item.ID); err != nil {
				return "", err
			}
		}
		target, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1 FOR UPDATE`, c.SpaceID))
		if err != nil {
			return "", err
		}
		if target.Source != SpaceSourceLocal {
			if err = publishMovedText(ctx, tx, item.ID, target.ID); err != nil {
				return "", err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE starts_with(key,$1) OR key=$2`, summaryMetaPrefix+item.ID, "chat/memory/"+item.ID); err != nil {
				return "", err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.conversations SET space_id=$1 WHERE id=$2`, target.ID, item.ID); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{space}',to_jsonb($1::text))::text WHERE key LIKE 'attachment/%' AND document->>'conversation'=$2`, target.ID, item.ID); err != nil {
			return "", err
		}
		return replacement, refreshSpacePointersTx(ctx, tx, item.SpaceID)
	}
	if c.Action != "trash" {
		return "", ErrRequestKey
	}
	if err := cancelManagedPending(ctx, tx, item.ID); err != nil {
		return "", err
	}
	var title string
	if err := tx.QueryRowContext(ctx, `SELECT title FROM fa.spaces WHERE id=$1`, item.SpaceID).Scan(&title); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	life := ConversationLifecycle{State: "trashed", Generation: newID(), TrashedAt: now.Format(time.RFC3339Nano), PurgeAfter: now.Add(7 * 24 * time.Hour).Format(time.RFC3339Nano), OriginalSpace: item.SpaceID, OriginalTitle: title, Operation: p.ID}
	if err := saveLifecycle(ctx, tx, item.ID, life); err != nil {
		return "", err
	}
	return replacement, refreshSpacePointersTx(ctx, tx, item.SpaceID)
}

func (tasks *TaskService) replaceManagedRoute(ctx context.Context, tx *sql.Tx, item ManagedConversation, replacement, operation string) (string, error) {
	space, err := scanSpace(tx.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM fa.spaces WHERE id=$1 FOR UPDATE`, item.SpaceID))
	if err != nil {
		return "", err
	}
	if replacement == "" {
		return "", errors.New("harness: choose a replacement conversation or explicitly select new")
	}
	if replacement == "new" {
		replacement = newID()
		now := nowText()
		if _, err = tx.ExecContext(ctx, `INSERT INTO fa.conversations(id,title,workspace,space_id,created_at,updated_at) VALUES($1,'新对话',$2,$3,$4,$4)`, replacement, item.Workspace, space.ID, now); err != nil {
			return "", err
		}
		for _, prefix := range []string{"chat/model/", capabilityMetaPrefix, "chat/reasoning/"} {
			if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT $1,value FROM fa.meta WHERE key=$2 ON CONFLICT DO NOTHING`, prefix+replacement, prefix+item.ID); err != nil {
				return "", err
			}
		}
	} else {
		var belongs string
		if err = tx.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1 AND fa.conversation_live(id)`, replacement).Scan(&belongs); err != nil {
			return "", err
		}
		if belongs != space.ID || replacement == item.ID {
			return "", ErrConversationSpace
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.spaces SET default_conversation_id=CASE WHEN default_conversation_id=$1 THEN $2 ELSE default_conversation_id END,active_conversation_id=CASE WHEN active_conversation_id=$1 THEN $2 ELSE active_conversation_id END WHERE id=$3`, item.ID, replacement, space.ID)
	return replacement, err
}

func cancelManagedPending(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "conversation/delivery-revoked/"+id, string(mustJSON(map[string]any{"at": nowText()}))); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT 'channel/run-cancelled/'||id,'true' FROM fa.runs WHERE conversation_id=$1 ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{state}','"rejected"')::text WHERE key LIKE 'channel/inbound/%' AND document->>'conversation_id'=$1 AND document->>'state' IN ('pending','scheduled')`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{status}','"cancelled"')::text WHERE starts_with(key,'input/'||$1||'/') AND document->>'status'='pending'`, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{state}','"cancelled"')::text WHERE key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 AND document->>'state' IN ('pending','paused','scheduled','reviewing','held')`, id)
	return err
}

func (store *Store) conversationDeliveryRevoked(ctx context.Context, id string) bool {
	var yes bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, "conversation/delivery-revoked/"+id).Scan(&yes)
	return err != nil || yes
}

// Keep the real UNKNOWN and transport records. A purged skeleton is not a
// restorable chat and is never eligible for execution or model history.
func (tasks *TaskService) purgeConversationTx(ctx context.Context, tx *sql.Tx, item ManagedConversation, operation string) error {
	if err := cancelManagedPending(ctx, tx, item.ID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key LIKE 'attachment/%' AND document->>'conversation'=$1`, item.ID)
	if err != nil {
		return err
	}
	type entry struct{ key, raw string }
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.key, &e.raw); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, e := range entries {
		var a attachmentRecord
		if json.Unmarshal([]byte(e.raw), &a) != nil {
			return ErrAttachment
		}
		a.Deleted = true
		if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(a)), e.key); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, "conversation/file-cleanup/"+a.Ref.ID, string(mustJSON(map[string]any{"id": a.Ref.ID, "conversation": item.ID}))); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT 'channel/retired-input/'||encode(sha256(convert_to(request_key,'UTF8')),'hex'),jsonb_build_object('conversation_id',conversation_id,'message_id',id)::text FROM fa.messages WHERE conversation_id=$1 AND request_key<>'' ON CONFLICT DO NOTHING`, item.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fa.messages WHERE conversation_id=$1`, item.ID); err != nil {
		return err
	}
	// Transport ledgers retain their own claims/receipts. Normal Run bodies
	// must not survive solely because a terminal receipt mentions their ID.
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document-'message')::text WHERE key LIKE 'channel/receipt/%' AND document->>'conversation_id'=$1`, item.ID); err != nil {
		return err
	}
	if err = discardConversationCaches(ctx, tx, item.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fa.attempts a WHERE a.run_id IN(SELECT r.id FROM fa.runs r WHERE r.conversation_id=$1) AND a.status NOT IN ('unknown','dispatched') AND NOT EXISTS(SELECT 1 FROM fa.meta m WHERE m.key='attempt/unresolved/'||a.id)`, item.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fa.runs r WHERE r.conversation_id=$1 AND NOT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=r.id) AND NOT EXISTS(SELECT 1 FROM fa.meta m WHERE m.key LIKE 'attempt/unresolved/%' AND m.document->>'run_id'=r.id)`, item.ID); err != nil {
		return err
	}
	// Inbound intents keep identity/fingerprint/terminal state, without Event text.
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=(document-'event')::text WHERE key LIKE 'channel/inbound/%' AND document->>'conversation_id'=$1 AND document->>'state'<>'pending'`, item.ID); err != nil {
		return err
	}
	life := *item.Lifecycle
	life.State = "purged"
	life.Operation = operation
	if err = saveLifecycle(ctx, tx, item.ID, life); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.conversations SET title='已删除会话' WHERE id=$1`, item.ID)
	return err
}

func (store *Store) cleanupRetiredFiles(ctx context.Context) error {
	var after string
	if err := store.db.QueryRowContext(ctx, `SELECT document->>'after' FROM fa.meta WHERE key='conversation/file-cleanup-cursor'`).Scan(&after); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT key,document->>'id' FROM fa.meta WHERE key LIKE 'conversation/file-cleanup/%' AND key>$1 ORDER BY key LIMIT 64`, after)
	if err != nil {
		return err
	}
	var entries [][2]string
	for rows.Next() {
		var e [2]string
		if err = rows.Scan(&e[0], &e[1]); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(entries) == 0 {
		_, err = store.db.ExecContext(ctx, `DELETE FROM fa.meta WHERE key='conversation/file-cleanup-cursor'`)
		return err
	}
	root, err := attachmentRoot(store.dataDir, false)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, e := range entries {
		if !attachmentID(e[1]) {
			return ErrAttachment
		}
		_ = root.Chmod(e[1], 0600)
		if err = root.Remove(e[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if _, err = store.db.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, e[0]); err != nil {
			return err
		}
	}
	after = ""
	if len(entries) == 64 {
		after = entries[len(entries)-1][0]
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES('conversation/file-cleanup-cursor',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(mustJSON(map[string]string{"after": after})))
	return err
}

func (store *Store) expireConversations(ctx context.Context, now time.Time) error {
	rows, err := store.db.QueryContext(ctx, `SELECT c.id FROM fa.conversations c JOIN fa.meta l ON l.key=$1||c.id WHERE l.document->>'state'='trashed' AND l.document->>'purge_after'<=$2 ORDER BY l.document->>'purge_after' LIMIT 32`, lifecyclePrefix, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(ids) == 0 {
		var pendingFiles bool
		if err = store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key LIKE 'conversation/file-cleanup/%')`).Scan(&pendingFiles); err != nil {
			return err
		}
		// Most sweeps have no work. Do not briefly exclude foreground work
		// merely to discover that both the trash and file retry queue are empty.
		if !pendingFiles {
			return nil
		}
	}
	if !store.runMu.TryLock() {
		return nil
	}
	defer store.runMu.Unlock()
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	tasks := NewTaskService(NewService(store, Config{}, Adapters{}))
	for _, id := range ids {
		tx, e := store.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		if e = lockConversationTx(ctx, tx, id); e == nil {
			var item ManagedConversation
			item, e = managedConversation(ctx, tx, id)
			if e == nil && item.Lifecycle != nil && item.Lifecycle.State == "trashed" && item.Lifecycle.PurgeAfter <= now.UTC().Format(time.RFC3339Nano) {
				e = tasks.purgeConversationTx(ctx, tx, item, "auto-7d")
			}
		}
		if e == nil {
			e = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if e != nil {
			return e
		}
	}
	return store.cleanupRetiredFiles(ctx)
}

// A space deletion is one transaction, including all children and route/grant changes.
func (tasks *TaskService) applyManagedSpaceDeletion(ctx context.Context, p ManagementPreview) (ManagementResult, error) {
	result := ManagementResult{ID: p.ID, Items: []ManagementItemResult{}}
	s := tasks.core.Store
	var leases []func()
	defer func() {
		for i := len(leases) - 1; i >= 0; i-- {
			leases[i]()
		}
	}()
	for _, item := range p.Items {
		if item.Workspace != tasks.Config().Workspace {
			return result, ErrManagementStale
		}
		release, e := s.runMu.acquire(item.ID, false)
		if e != nil {
			return result, e
		}
		leases = append(leases, release)
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	s.telegramMu.Lock()
	defer s.telegramMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT row_to_json(s)::text FROM fa.spaces s WHERE id=$1 FOR UPDATE`, p.Command.SpaceID).Scan(&raw); err != nil {
		return result, err
	}
	if contentHash([]byte(raw)) != p.SpaceRevision {
		return result, ErrManagementStale
	}
	var deletedSpace Space
	if json.Unmarshal([]byte(raw), &deletedSpace) != nil {
		return result, ErrStoreFormat
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.conversations WHERE space_id=$1`, p.Command.SpaceID).Scan(&count); err != nil {
		return result, err
	}
	if count != len(p.Items) {
		return result, ErrManagementStale
	}
	for _, item := range p.Items {
		if err = lockConversationTx(ctx, tx, item.ID); err != nil {
			return result, err
		}
		cur, e := managedConversation(ctx, tx, item.ID)
		if e != nil {
			return result, e
		}
		if cur.Revision != item.Revision {
			return result, ErrManagementStale
		}
		for _, b := range cur.Blockers {
			if b == "channel_route" {
				continue
			}
			if b == "queued_input" || b == "pending_delivery" {
				if p.Command.CancelPending {
					continue
				}
			}
			return result, fmt.Errorf("harness: %s requires processing: %s", cur.Title, b)
		}
		if cur.Lifecycle == nil || cur.Lifecycle.State == "active" {
			if p.Command.CancelPending || *p.Command.TrashConversations {
				if err = cancelManagedPending(ctx, tx, cur.ID); err != nil {
					return result, err
				}
			}
			if *p.Command.TrashConversations {
				now := time.Now().UTC()
				life := ConversationLifecycle{State: "trashed", Generation: newID(), TrashedAt: now.Format(time.RFC3339Nano), PurgeAfter: now.Add(168 * time.Hour).Format(time.RFC3339Nano), OriginalSpace: p.Command.SpaceID, OriginalTitle: deletedSpace.Title, Operation: p.ID}
				if err = saveLifecycle(ctx, tx, cur.ID, life); err != nil {
					return result, err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.conversations SET space_id=$1 WHERE id=$2`, LocalSpaceID, cur.ID); err != nil {
			return result, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{space}',to_jsonb($1::text))::text WHERE key LIKE 'attachment/%' AND document->>'conversation'=$2`, LocalSpaceID, cur.ID); err != nil {
			return result, err
		}
		result.Items = append(result.Items, ManagementItemResult{ID: cur.ID, Status: "completed", Message: "空间删除：会话已按确认方式处理"})
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1 OR starts_with(key,$2) OR key LIKE 'telegram/access/%' AND document->>'space_id'=$3`, "space/permission/"+p.Command.SpaceID, "space/people/"+p.Command.SpaceID+"/", p.Command.SpaceID); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM fa.spaces WHERE id=$1`, p.Command.SpaceID); err != nil {
		return result, err
	}
	if err = refreshSpacePointersTx(ctx, tx, LocalSpaceID); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "conversation/management-result/"+p.ID, string(mustJSON(result))); err != nil {
		return result, err
	}
	err = tx.Commit()
	if err == nil {
		tasks.core.event(Event{Kind: EventRefresh, Message: "空间已删除"})
	}
	return result, err
}
