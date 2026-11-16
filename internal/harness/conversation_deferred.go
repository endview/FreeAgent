package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const deferredManagementPrefix = "conversation/management-deferred/"

type deferredConversationManagement struct {
	Operation    string `json:"operation"`
	Preview      string `json:"preview"`
	Run          string `json:"run"`
	Attempt      string `json:"attempt"`
	Conversation string `json:"conversation"`
	Stamp        string `json:"stamp"`
	State        string `json:"state"`
	Message      string `json:"message,omitempty"`
}

// Ignore only the proposing Run's expected protocol/settlement writes. New
// input, another Run, permissions, routing, attachments or delivery invalidate it.
func managementSelfStamp(ctx context.Context, r managementReader, chat, run string) (string, error) {
	var raw string
	err := r.QueryRowContext(ctx, `SELECT jsonb_build_array(
 (SELECT to_jsonb(c)-'last_message_seq'-'updated_at' FROM fa.conversations c WHERE id=$1),
 (SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY id),'[]') FROM fa.runs x WHERE conversation_id=$1 AND id<>$2),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(id,body_json) ORDER BY id),'[]') FROM fa.messages WHERE conversation_id=$1 AND run_id IS DISTINCT FROM $2),
 (SELECT coalesce(jsonb_agg(jsonb_build_array(key,value) ORDER BY key),'[]') FROM fa.meta WHERE key='conversation/lifecycle/'||$1 OR key='chat/capabilities/'||$1 OR key='chat/model/'||$1 OR key='chat/pin/'||$1 OR starts_with(key,'input/'||$1||'/') OR key LIKE 'attachment/%' AND document->>'conversation'=$1 OR key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1),
 (SELECT to_jsonb(s) FROM fa.spaces s WHERE id=(SELECT space_id FROM fa.conversations WHERE id=$1)),
 (SELECT value FROM fa.meta WHERE key='space/permission/'||(SELECT space_id FROM fa.conversations WHERE id=$1)))::text`, chat, run).Scan(&raw)
	return contentHash([]byte(raw)), err
}

func (tasks *TaskService) completeDeferredManagement(ctx context.Context, runID string) (returnErr error) {
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, deferredManagementPrefix+runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var d deferredConversationManagement
	if json.Unmarshal([]byte(raw), &d) != nil {
		return ErrStoreFormat
	}
	if d.State == "completed" || d.State == "needs_review" {
		return nil
	}
	defer func() {
		if !errors.Is(returnErr, ErrManagementStale) {
			return
		}
		d.State, d.Message = "needs_review", "会话或授权在确认后发生变化，整理未继续；请重新预览。"
		if _, e := tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(d)), deferredManagementPrefix+d.Run); e != nil {
			returnErr = fmt.Errorf("harness: persist deferred management review state: %w", e)
			return
		}
		_, e := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, "conversation/management-result/"+d.Preview, string(mustJSON(ManagementResult{ID: d.Preview, Items: []ManagementItemResult{{ID: d.Conversation, Status: "skipped", Message: d.Message}}})))
		if e != nil {
			returnErr = fmt.Errorf("harness: persist deferred management result: %w", e)
		}
	}()
	if result, e := tasks.ConversationManagementResult(ctx, d.Preview); e == nil {
		done := len(result.Items) > 0
		for _, i := range result.Items {
			done = done && i.Status == "completed"
		}
		if done {
			d.State = "completed"
			_, e = tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(d)), deferredManagementPrefix+d.Run)
			return e
		}
	}
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	control.mu.Lock()
	op, exists := control.operations[d.Operation]
	control.mu.Unlock()
	if !exists || op.Operation.Status != ControlApplied {
		return nil
	}
	run, err := tasks.core.Store.GetRun(ctx, d.Run)
	if err != nil {
		return err
	}
	attempt, err := tasks.core.Store.GetAttempt(ctx, d.Attempt)
	if err != nil {
		return err
	}
	if attempt.RunID != run.ID || run.ConversationID != d.Conversation {
		return ErrPermissionDenied
	}
	if attempt.Status == "pending" {
		var safe bool
		if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM fa.attempts WHERE run_id=$1 AND id<>$2 AND status IN ('pending','dispatched','unknown'))`, run.ID, attempt.ID).Scan(&safe); err != nil {
			return err
		}
		if !safe {
			return ErrActiveRun
		}
		if err = tasks.core.Store.settleControlProposal(ctx, run, attempt, ToolResult{Content: "user confirmed conversation management; finish this Run before managing its history"}, true, "conversation_management_confirmed"); err != nil {
			return err
		}
	}
	stamp, err := managementSelfStamp(ctx, tasks.core.Store.db, d.Conversation, d.Run)
	if err != nil {
		return err
	}
	if stamp != d.Stamp {
		return ErrManagementStale
	}
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management/"+d.Preview).Scan(&raw); err != nil {
		return err
	}
	var p ManagementPreview
	if json.Unmarshal([]byte(raw), &p) != nil {
		return ErrStoreFormat
	}
	for i := range p.Items {
		if p.Items[i].ID == d.Conversation {
			item, e := managedConversation(ctx, tasks.core.Store.db, d.Conversation)
			if e != nil {
				return e
			}
			p.Items[i] = item
		}
	}
	if _, err = tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(p)), "conversation/management/"+d.Preview); err != nil {
		return err
	}
	result, err := tasks.ApplyConversationManagement(ctx, d.Preview)
	if err != nil {
		return err
	}
	for _, i := range result.Items {
		if i.Status != "completed" {
			return ErrManagementStale
		}
	}
	d.State = "completed"
	_, err = tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(d)), deferredManagementPrefix+d.Run)
	return err
}

func (tasks *TaskService) recoverDeferredManagement(ctx context.Context) error {
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT document->>'run' FROM fa.meta WHERE key LIKE 'conversation/management-deferred/%' AND document->>'state'='waiting_settlement' ORDER BY key LIMIT 32`)
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
	for _, id := range ids {
		if err = tasks.completeDeferredManagement(ctx, id); err != nil && !errors.Is(err, ErrManagementStale) && !errors.Is(err, ErrActiveRun) {
			return err
		}
	}
	return nil
}
