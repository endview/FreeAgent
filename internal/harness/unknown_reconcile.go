package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Reviewed against control_query.go at the v8.8.0-dev.5 source commit.
// Future tools/descriptions are not eligible merely because they mention reads.
const readDenialProof = "control-read-denial/v8.8.0-dev.5-9f6fa2efe089"
const legacyControlReadDescription = "For FreeAgent settings requests, read this current control state FIRST, then use control_plan_change with its available typed operations. Current runtime facts take precedence over old release files and conversation summaries. Use domain runtime/models/tools/extensions for a focused read. Pass operation for its exact input schema. Extension state is fetched only with extension_id and is bounded. Paths and secrets are redacted."

type UnknownReconciliation struct {
	Digest   string   `json:"digest"`
	Attempts []string `json:"attempts"`
	Applied  bool     `json:"applied"`
}

func (tasks *TaskService) ReconcileReadDenials(ctx context.Context, digest string) (UnknownReconciliation, error) {
	if digest != "" {
		if !tasks.core.Store.runMu.TryLock() {
			return UnknownReconciliation{}, ErrActiveRun
		}
		defer tasks.core.Store.runMu.Unlock()
	}
	return tasks.core.Store.ReconcileReadDenials(ctx, digest)
}

// A positive versioned proof: this read implementation denies before it
// starts extension business code, and the original frozen selection excludes
// the requested extension. No inference from missing logs or current config.
func provableReadDenial(run Run, attempt Attempt) bool {
	var call ToolCall
	var q controlStateQuery
	var snap runSnapshot
	var result ToolResult
	if attempt.Kind != "tool" || attempt.Status != "unknown" || json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil || call.Name != controlReadTool || json.Unmarshal(call.Arguments, &q) != nil || q.ExtensionID == "" || json.Unmarshal([]byte(run.ConfigJSON), &snap) != nil || json.Unmarshal([]byte(attempt.ResponseJSON), &result) != nil {
		return false
	}
	if snap.Capabilities != nil && contains(snap.Capabilities.Extensions, q.ExtensionID) {
		return false
	}
	declared := false
	for _, d := range snap.Tools {
		if d.Name == controlReadTool && d.Effect == EffectRead && d.Description == legacyControlReadDescription {
			declared = true
		}
	}
	return declared && result.Outcome != nil && result.Outcome.Status == "unknown" && len(result.Receipts) == 0 && result.Content == ""
}

func (store *Store) ReconcileReadDenials(ctx context.Context, digest string) (UnknownReconciliation, error) {
	report := UnknownReconciliation{Attempts: []string{}}
	if digest != "" {
		if len(digest) != 64 {
			return report, ErrRequestKey
		}
		var raw string
		e := store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "unknown/reconciled/"+digest).Scan(&raw)
		if e == nil {
			if json.Unmarshal([]byte(raw), &report) != nil {
				return report, ErrStoreFormat
			}
			return report, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return report, e
		}
	}
	rows, err := store.db.QueryContext(ctx, `SELECT a.id,a.run_id,a.kind,a.call_id,a.status,a.request_json,a.response_json,a.seq,r.config_json,r.conversation_id,r.status FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id WHERE a.kind='tool' AND a.status='unknown' AND a.request_doc->>'Name'=$1 AND NOT `+resolvedAttemptSQL+` ORDER BY a.id LIMIT 1000`, controlReadTool)
	if err != nil {
		return report, err
	}
	type candidate struct {
		run     Run
		attempt Attempt
	}
	var candidates []candidate
	var proof []string
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.attempt.ID, &c.attempt.RunID, &c.attempt.Kind, &c.attempt.CallID, &c.attempt.Status, &c.attempt.RequestJSON, &c.attempt.ResponseJSON, &c.attempt.Sequence, &c.run.ConfigJSON, &c.run.ConversationID, &c.run.Status); err != nil {
			rows.Close()
			return report, err
		}
		c.run.ID = c.attempt.RunID
		if provableReadDenial(c.run, c.attempt) {
			candidates = append(candidates, c)
			report.Attempts = append(report.Attempts, c.attempt.ID)
			proof = append(proof, contentHash(mustJSON([]any{c.run.ConfigJSON, c.run.ID, c.run.Status, c.attempt})))
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return report, err
	}
	report.Digest = contentHash(mustJSON([]any{readDenialProof, proof}))
	if digest == "" {
		return report, nil
	}
	if digest != report.Digest {
		return report, ErrApprovalChanged
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	for _, c := range candidates {
		e := func() error {
			if e := lockExecutionTx(ctx, tx, c.run.ID); e != nil {
				return e
			}
			var current string
			if e := tx.QueryRowContext(ctx, `SELECT response_json FROM fa.attempts WHERE id=$1 AND status='unknown' FOR UPDATE`, c.attempt.ID).Scan(&current); e != nil {
				return e
			}
			if current != c.attempt.ResponseJSON {
				return ErrApprovalChanged
			}
			var call ToolCall
			_ = json.Unmarshal([]byte(c.attempt.RequestJSON), &call)
			item, e := putUnresolved(ctx, tx, c.run, c.attempt, call, readDenialProof)
			if e != nil {
				return e
			}
			item.Resolution = "not_dispatched"
			item.Actor = "owner_maintenance"
			item.Revision = ""
			item.Revision = contentHash(mustJSON(item))
			if _, e = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(item)), unresolvedPrefix+item.ID); e != nil {
				return e
			}
			var exists bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.messages WHERE run_id=$1 AND body_json->>'ToolCallID'=$2)`, c.run.ID, call.ID).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				if e = insertMessage(ctx, tx, c.run, toolMessage(call, ToolResult{Content: unresolvedText(item), IsError: true})); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, `UPDATE fa.runs r SET status='failed',stop_reason='unknown_reconciled',updated_at=$1 WHERE id=$2 AND status='unknown' AND NOT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=r.id AND a.status IN ('pending','dispatched','unknown') AND NOT `+resolvedAttemptSQL+`)`, nowText(), c.run.ID); e != nil {
				return e
			}
			return nil
		}()
		if e != nil {
			return report, e
		}
	}
	report.Applied = true
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "unknown/reconciled/"+digest, string(mustJSON(report))); err != nil {
		return report, err
	}
	return report, tx.Commit()
}

func (store *Store) recoverHandedOffTools(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `SELECT a.id,a.run_id,a.kind,a.call_id,a.status,a.request_json,a.response_json,a.seq,r.config_json,r.conversation_id FROM fa.attempts a JOIN fa.runs r ON r.id=a.run_id JOIN fa.meta m ON m.key='attempt/dispatch-contract/'||a.id JOIN fa.meta cause ON cause.key='attempt/uncertainty/'||a.id WHERE a.kind='tool' AND a.status='unknown' AND m.document->>'record_message'='true' AND cause.document->>'source'='tool_response' AND NOT `+resolvedAttemptSQL+` ORDER BY r.created_at,a.seq LIMIT 1000`)
	if err != nil {
		return err
	}
	type item struct {
		run Run
		a   Attempt
	}
	var all []item
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.a.ID, &i.a.RunID, &i.a.Kind, &i.a.CallID, &i.a.Status, &i.a.RequestJSON, &i.a.ResponseJSON, &i.a.Sequence, &i.run.ConfigJSON, &i.run.ConversationID); err != nil {
			rows.Close()
			return err
		}
		i.run.ID = i.a.RunID
		all = append(all, i)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, i := range all {
		var contract struct {
			Effect   ToolEffect `json:"effect"`
			Business bool       `json:"business"`
		}
		var raw string
		if err = store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "attempt/dispatch-contract/"+i.a.ID).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal([]byte(raw), &contract) != nil {
			return ErrStoreFormat
		}
		var call ToolCall
		var result ToolResult
		var snap runSnapshot
		if json.Unmarshal([]byte(i.a.RequestJSON), &call) != nil || json.Unmarshal([]byte(i.a.ResponseJSON), &result) != nil || json.Unmarshal([]byte(i.run.ConfigJSON), &snap) != nil {
			continue
		}
		if !canHandoffTool(call, PreparedTool{Effect: contract.Effect, handoffBusiness: contract.Business}, result) {
			continue
		}
		core := &Service{Store: store, Config: snap.Config}
		if err = core.handoffTool(ctx, i.run, i.a, call, nil); err != nil {
			return err
		}
		if _, err = store.db.ExecContext(ctx, `UPDATE fa.runs r SET status='failed',stop_reason='owner_interrupted',updated_at=$1 WHERE id=$2 AND status='running' AND NOT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=r.id AND a.status IN ('pending','dispatched','unknown') AND NOT `+resolvedAttemptSQL+`)`, nowText(), i.run.ID); err != nil {
			return err
		}
	}
	_, err = store.db.ExecContext(ctx, `UPDATE fa.runs r SET status='failed',stop_reason='owner_interrupted',updated_at=$1 WHERE status IN ('stopped','unknown') AND EXISTS(SELECT 1 FROM fa.meta u WHERE starts_with(u.key,$2) AND u.document->>'run_id'=r.id AND u.document->>'handoff'='true') AND NOT EXISTS(SELECT 1 FROM fa.attempts a WHERE a.run_id=r.id AND a.status IN ('pending','dispatched','unknown') AND NOT `+resolvedAttemptSQL+`)`, nowText(), unresolvedPrefix)
	return err
}
