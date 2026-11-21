package harness

import (
	"context"
	"encoding/json"
	"errors"
)

// Revoked, never-dispatched approvals must not reserve a shared conversation
// forever. This never resolves or abandons an UNKNOWN/dispatched operation.
func (tasks *TaskService) retireChannelWaits(ctx context.Context, target ChannelTarget) error {
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil {
		return err
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT r.id FROM fa.runs r JOIN fa.conversations c ON c.id=r.conversation_id WHERE c.space_id=$1 AND r.status='waiting_confirmation' LIMIT 128`, space.ID)
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
		run, err := tasks.core.Store.GetRun(ctx, id)
		if err != nil {
			return err
		}
		origin := channelOriginFromJSON(run.ConfigJSON)
		if origin == nil || origin.Source != SpaceSourceTelegram || run.Permission == nil || run.Permission.PersonID == "" {
			continue
		}
		grant, checkErr := tasks.CheckTelegramAccess(ctx, origin.ChannelTarget, run.Permission.PersonID)
		if checkErr != nil && !errors.Is(checkErr, ErrPermissionDenied) {
			return checkErr
		}
		if checkErr == nil && grant.Revision == origin.AccessRevision {
			continue
		}
		if err = tasks.core.Store.endUndispatchedWait(ctx, id, "channel_authority_changed"); err != nil {
			if errors.Is(err, ErrUnknownResult) {
				continue
			}
			return err
		}
		control, e := tasks.Control()
		if e != nil {
			continue
		}
		attempts, e := tasks.core.Store.ListAttempts(ctx, id)
		if e != nil {
			return e
		}
		for _, attempt := range attempts {
			if record, ok := control.operationForAttempt(attempt.ID); ok {
				if err = control.withdrawProposal(record.Operation.ID, "channel_authority_changed"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (store *Store) endUndispatchedWait(ctx context.Context, id, reason string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockExecutionTx(ctx, tx, id); err != nil {
		return err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if run.Status != "waiting_confirmation" {
		return nil
	}
	var unsafe bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.attempts WHERE run_id=$1 AND status IN ('unknown','dispatched'))`, id).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return ErrUnknownResult
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,request_json FROM fa.attempts WHERE run_id=$1 AND kind='tool' AND status='pending' ORDER BY seq`, id)
	if err != nil {
		return err
	}
	type pending struct {
		id   string
		call ToolCall
	}
	var calls []pending
	for rows.Next() {
		var item pending
		var raw string
		if err = rows.Scan(&item.id, &raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(raw), &item.call) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		calls = append(calls, item)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, item := range calls {
		result := ToolResult{Content: "Not executed: channel authority or route changed while awaiting confirmation. Obtain a fresh preview before any action.", IsError: true}
		if _, err = tx.ExecContext(ctx, `UPDATE fa.attempts SET status='finished',response_json=$1 WHERE id=$2 AND status='pending'`, string(mustJSON(result)), item.id); err != nil {
			return err
		}
		if err = insertMessage(ctx, tx, run, toolMessage(item.call, result)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.runs SET status='abandoned',stop_reason=$1,updated_at=$2 WHERE id=$3`, reason, nowText(), id); err != nil {
		return err
	}
	return tx.Commit()
}
