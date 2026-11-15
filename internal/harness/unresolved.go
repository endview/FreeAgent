package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const unresolvedPrefix = "attempt/unresolved/"

func inheritUnresolved(ctx context.Context, tx *sql.Tx, source, target string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT $1||(u.document->>'id'),jsonb_build_object('original',u.document->>'id')::text FROM fa.meta u WHERE starts_with(u.key,$2) AND (u.document->>'conversation_id'=$3 OR EXISTS(SELECT 1 FROM fa.meta i WHERE starts_with(i.key,$4) AND i.document->>'original'=u.document->>'id')) ON CONFLICT DO NOTHING`, "unresolved/inherited/"+target+"/", unresolvedPrefix, source, "unresolved/inherited/"+source+"/")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) SELECT $1,coalesce(max((u.document->>'number')::bigint),0)::text FROM fa.meta u WHERE starts_with(u.key,$2) AND EXISTS(SELECT 1 FROM fa.meta i WHERE starts_with(i.key,$3) AND i.document->>'original'=u.document->>'id') ON CONFLICT DO NOTHING`, "unresolved/counter/"+target, unresolvedPrefix, "unresolved/inherited/"+target+"/")
	return err
}

func onlyUnknownFailure(err error) bool {
	if err == ErrUnknownResult {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !onlyUnknownFailure(e) {
				return false
			}
		}
		return true
	}
	return false
}

// This record never replaces the original Attempt or a transport receipt.
type UnresolvedItem struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	Number         int64  `json:"number"`
	Tool           string `json:"tool"`
	Target         string `json:"target,omitempty"`
	Fingerprint    string `json:"fingerprint"`
	Source         string `json:"source"`
	Handoff        bool   `json:"handoff"`
	Resolution     string `json:"resolution,omitempty"`
	Note           string `json:"note,omitempty"`
	ModelNote      string `json:"model_note,omitempty"`
	Actor          string `json:"actor,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	Revision       string `json:"revision"`
}

func unresolvedFingerprint(run Run, call ToolCall) string {
	var snap runSnapshot
	_ = json.Unmarshal([]byte(run.ConfigJSON), &snap)
	var args any
	_ = json.Unmarshal(call.Arguments, &args)
	var backend any
	if strings.HasPrefix(call.Name, "mcp_") {
		for _, definition := range snap.Tools {
			if definition.Name == call.Name {
				for id, c := range snap.Config.MCPServers {
					if strings.HasPrefix(definition.Description, id+"/") {
						backend = c
						break
					}
				}
			}
		}
	}
	// Definition aliases bind MCP server/tool identity. Config binds the actual
	// backend, workspace, environment and account, never the model's call ID.
	return contentHash(mustJSON([]any{call.Name, args, snap.Config.Workspace,
		backend, snap.ExecutionProfile.WorkspaceRoot, snap.CommandPolicy.Execution, snap.ChannelTargetForUnresolved()}))
}

func (snap runSnapshot) ChannelTargetForUnresolved() any {
	if snap.Channel == nil {
		return nil
	}
	return snap.Channel.ChannelTarget
}

func unresolvedText(item UnresolvedItem) string {
	if item.Resolution == "not_dispatched" {
		return fmt.Sprintf("原判 UNKNOWN（#%d），已核实未执行；原记录保留。", item.Number)
	}
	return fmt.Sprintf("结果未知（#%d）：%s。动作可能已执行，不是成功证据，不要自动重复。依赖它的步骤先只读核查；不能核查时继续不依赖它的部分。原始证据已保留。", item.Number, item.Tool)
}

func putUnresolved(ctx context.Context, tx *sql.Tx, run Run, attempt Attempt, call ToolCall, source string) (UnresolvedItem, error) {
	var item UnresolvedItem
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, unresolvedPrefix+attempt.ID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &item)
		return item, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	counter := "unresolved/counter/" + run.ConversationID
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, counter); err != nil {
		return item, err
	}
	var n int64
	err = tx.QueryRowContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'1') ON CONFLICT(key) DO UPDATE SET value=(fa.meta.value::bigint+1)::text RETURNING value::bigint`, counter).Scan(&n)
	if err != nil {
		return item, err
	}
	item = UnresolvedItem{ID: attempt.ID, ConversationID: run.ConversationID, RunID: run.ID, Number: n, Tool: call.Name, Target: truncateUTF8Bytes(activityTarget(call), 256), Fingerprint: unresolvedFingerprint(run, call), Source: source, Handoff: true, UpdatedAt: nowText()}
	item.Revision = contentHash(mustJSON(item))
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, unresolvedPrefix+item.ID, string(mustJSON(item)))
	return item, err
}

// Handoff is deliberately an allowlist. Uncontrolled processes, local commit
// uncertainty and unclassified executors retain the existing blocking path.
func canHandoffTool(call ToolCall, prepared PreparedTool, result ToolResult) bool {
	if prepared.Effect != EffectModify || prepared.executeTransaction != nil || prepared.controlProposal {
		return false
	}
	if strings.HasPrefix(call.Name, "mcp_") || prepared.handoffBusiness {
		return true
	}
	if call.Name == "file_create" || call.Name == "file_edit" {
		for _, r := range result.Receipts {
			if r.Kind == "file" {
				return true
			}
		}
	}
	if call.Name == "command_run" {
		for _, r := range result.Receipts {
			if r.Kind == "command" && r.Cleanup == "confirmed" {
				return true
			}
		}
	}
	return false
}

// Caller holds the normal per-conversation execution slot; the transaction
// atomically closes the protocol and defers the rest of the write batch.
func (service *Service) handoffTool(ctx context.Context, run Run, attempt Attempt, call ToolCall, definitions []ToolDefinition) error {
	tx, err := service.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockExecutionTx(ctx, tx, run.ID); err != nil {
		return err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM fa.attempts WHERE id=$1 AND run_id=$2`, attempt.ID, run.ID).Scan(&status); err != nil {
		return err
	}
	if status != "unknown" {
		return ErrUnknownResult
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts a WHERE a.run_id=$1 AND a.id<>$2 AND a.status='dispatched' AND NOT `+resolvedAttemptSQL, run.ID, attempt.ID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrUnknownResult
	}
	item, err := putUnresolved(ctx, tx, run, attempt, call, "tool_response_unknown")
	if err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.messages WHERE run_id=$1 AND body_json->>'ToolCallID'=$2)`, run.ID, call.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		var result ToolResult
		_ = json.Unmarshal([]byte(attempt.ResponseJSON), &result)
		result.Content = unresolvedText(item) + "\n" + truncateUTF8Bytes(result.Content, 1024)
		result.IsError = true
		if err = insertMessage(ctx, tx, run, toolMessage(call, result)); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,request_json FROM fa.attempts WHERE run_id=$1 AND status='pending' AND kind='tool' AND seq>(SELECT seq FROM fa.attempts WHERE id=$2) ORDER BY seq`, run.ID, attempt.ID)
	if err != nil {
		return err
	}
	type pending struct {
		id   string
		call ToolCall
	}
	var rest []pending
	for rows.Next() {
		var p pending
		var raw string
		if err = rows.Scan(&p.id, &raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(raw), &p.call) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		rest = append(rest, p)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	reads := map[string]bool{}
	for _, d := range definitions {
		reads[d.Name] = d.Effect == EffectRead
	}
	for _, p := range rest {
		if reads[p.call.Name] {
			continue
		}
		result := ToolResult{Content: fmt.Sprintf("本次未执行：请先查看结果未知 #%d，再重新安排此动作。", item.Number), IsError: true, Outcome: &ToolOutcome{Status: "not_dispatched", CompletionKnown: true}}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished',response_json=$1 WHERE id=$2 AND status='pending'`, string(mustJSON(result)), p.id); err != nil {
			return err
		}
		if err = insertMessage(ctx, tx, run, toolMessage(p.call, result)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.runs SET status='running',stop_reason='',updated_at=$1 WHERE id=$2 AND status='unknown'`, nowText(), run.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		// A missing commit acknowledgement is not permission to continue.
		// Read the exact atomic handoff back before any next dispatch.
		readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ok, readErr := service.Store.handoffRecorded(readCtx, run.ID, attempt.ID, call.ID)
		if !ok || readErr != nil {
			return errors.Join(ErrUnknownResult, err, readErr)
		}
	}
	service.event(Event{Kind: EventUnresolved, RunID: run.ID, ConversationID: run.ConversationID, Message: unresolvedText(item)})
	return nil
}

func (store *Store) handoffRecorded(ctx context.Context, run, attempt, call string) (bool, error) {
	var ok bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta u WHERE u.key=$1 AND u.document->>'run_id'=$2 AND u.document->>'handoff'='true' AND EXISTS(SELECT 1 FROM fa.messages WHERE run_id=$2 AND body_json->>'ToolCallID'=$3))`, unresolvedPrefix+attempt, run, call).Scan(&ok)
	return ok, err
}

// Scope filtering happens before rows, counts or numbers leave the Core.
// The first release exposes side-effect details only to the Owner full view.
func (tasks *TaskService) Unresolved(ctx context.Context, chat string, after int64) ([]UnresolvedItem, error) {
	return tasks.unresolved(ctx, chat, after, false)
}

func (tasks *TaskService) unresolved(ctx context.Context, chat string, after int64, openOnly bool) ([]UnresolvedItem, error) {
	if chat == "" {
		return []UnresolvedItem{}, nil // non-conversation probes have no operation list
	}
	core, err := tasks.contextReader(ctx, chat)
	if err != nil {
		return nil, err
	}
	if core.scopedHistory() {
		return []UnresolvedItem{}, nil
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT u.value FROM fa.meta u WHERE starts_with(u.key,$1) AND (u.document->>'conversation_id'=$2 OR EXISTS(SELECT 1 FROM fa.meta i WHERE starts_with(i.key,$4) AND i.document->>'original'=u.document->>'id')) AND (u.document->>'number')::bigint>$3 AND (NOT $5::boolean OR coalesce(u.document->>'resolution','')='') ORDER BY (u.document->>'number')::bigint LIMIT 50`, unresolvedPrefix, chat, after, "unresolved/inherited/"+chat+"/", openOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UnresolvedItem{}
	for rows.Next() {
		var raw string
		var item UnresolvedItem
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &item) != nil {
			return nil, ErrStoreFormat
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (tasks *TaskService) ResolveUnresolved(ctx context.Context, chat, id, revision, action, note, actor string) error {
	core, err := tasks.contextReader(ctx, chat)
	if err != nil {
		return err
	}
	if core.scopedHistory() {
		return ErrPermissionDenied
	}
	if action != "ignored" && action != "user_confirmed_applied" && action != "user_confirmed_not_applied" && action != "model_note" {
		return ErrRequestKey
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	var item UnresolvedItem
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, unresolvedPrefix+id).Scan(&raw); err != nil {
		return err
	}
	if json.Unmarshal([]byte(raw), &item) != nil {
		return ErrPermissionDenied
	}
	if item.ConversationID != chat {
		var inherited bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1 AND document->>'original'=$2)`, "unresolved/inherited/"+chat+"/"+id, id).Scan(&inherited); err != nil {
			return err
		}
		if !inherited {
			return ErrPermissionDenied
		}
	}
	if action != "model_note" && item.Resolution == action && item.Actor == actor && item.Note == truncateUTF8Bytes(note, 1024) {
		return nil
	}
	if item.Revision != revision {
		return ErrApprovalChanged
	}
	if action != "model_note" {
		item.Resolution = action
		item.Note = truncateUTF8Bytes(note, 1024)
		item.Actor = actor
	} else {
		item.ModelNote = truncateUTF8Bytes(note, 1024)
	}
	item.UpdatedAt = nowText()
	item.Revision = ""
	item.Revision = contentHash(mustJSON(item))
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, "attempt/unresolved-history/"+id+"/"+contentHash([]byte(raw)), raw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(item)), unresolvedPrefix+id, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func (service *Service) unresolvedReport(ctx context.Context, run Run) (string, error) {
	items, err := NewTaskService(service).unresolved(ctx, run.ConversationID, 0, true)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, item := range items {
		if item.Resolution == "" {
			lines = append(lines, fmt.Sprintf("#%d %s：结果未确认，原操作未重放。", item.Number, item.Tool))
			if len(lines) == 10 {
				break
			}
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	more := ""
	if len(items) > 10 {
		more = "\n更多未决事项可在详情分页查看。"
	}
	return "未确认事项\n" + strings.Join(lines, "\n") + more + "\n可在未决详情中核查或处理；忽略不会授权重发。", nil
}

func (service *Service) replayUnresolved(ctx context.Context, run Run, call ToolCall) (bool, error) {
	var exists bool
	err := service.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE starts_with(key,$1) AND document->>'fingerprint'=$2 AND coalesce(document->>'resolution','') NOT IN ('not_dispatched','user_confirmed_not_applied','evidence_not_applied'))`, unresolvedPrefix, unresolvedFingerprint(run, call)).Scan(&exists)
	return exists, err
}
