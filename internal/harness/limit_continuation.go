package harness

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const settledLimitPrefix = "settled_limit:"
const activeRunPredicate = `(status IN ('running','waiting_confirmation','stopped','unknown') OR (status='limited' AND substr(stop_reason,1,14)!='settled_limit:'))`
const activeRunIndex = `CREATE UNIQUE INDEX one_active_run ON runs(conversation_id) WHERE ` + activeRunPredicate
const unsettledRunIndex = `CREATE UNIQUE INDEX one_active_run ON runs(conversation_id) WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`
const limitContinuationPrompt = "Continue only the unfinished work from the preceding limited request. Use the saved conversation and confirmed results. Do not repeat completed tools or replay UNKNOWN operations. Report what remains unfinished."

func isExplicitContinuation(prompt string) bool {
	switch strings.TrimSpace(prompt) {
	case "继续", "/continue", "continue", "Continue":
		return true
	}
	return false
}

// Only the occupancy index changes. No table, history, status, permission or
// usage record is migrated; a settled limit remains status=limited in SQLite.
func (store *Store) ensureActiveRunIndex(ctx context.Context) error {
	var existing string
	if err := store.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name='one_active_run'`).Scan(&existing); err != nil {
		return err
	}
	if existing == activeRunIndex {
		return nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DROP INDEX one_active_run`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, activeRunIndex); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) settleLimited(ctx context.Context, run Run) (bool, error) {
	if run.Status != "limited" || run.LimitSettled {
		return run.LimitSettled, nil
	}
	if !strings.Contains(run.StopReason, ErrStepLimit.Error()) && !strings.Contains(run.StopReason, ErrTimeLimit.Error()) && !strings.Contains(run.StopReason, "output") {
		return false, nil
	}
	if parent, err := store.controlledParent(ctx, run.ID); err != nil || parent != "" {
		return false, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var unsettled int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE run_id=? AND status IN ('pending','dispatched','unknown')`, run.ID).Scan(&unsettled); err != nil {
		return false, err
	}
	if unsettled != 0 {
		return false, nil
	}
	if err := checkConversationCommandsTx(ctx, tx, run.ConversationID); err != nil {
		if errors.Is(err, ErrCommandBusy) {
			return false, nil
		}
		return false, err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE runs SET stop_reason=?,updated_at=? WHERE id=? AND status='limited' AND stop_reason=? AND active_since=''`, settledLimitPrefix+run.StopReason, nowText(), run.ID, run.StopReason)
	if err != nil {
		return false, err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (tasks *TaskService) startLimitSuccessor(ctx context.Context, core *Service, run Run, options RunOptions) (RunResult, error) {
	if !run.LimitSettled {
		return RunResult{}, ErrActiveRun
	}
	if parent, err := core.Store.controlledParent(ctx, run.ID); err != nil || parent != "" {
		return RunResult{}, errors.Join(ErrControlledRun, err)
	}
	snapshot, err := decodeRunSnapshot(run.ConfigJSON)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.CommandRulesVersion != 1 {
		return RunResult{}, ErrConfigChanged
	}
	if snapshot.Permission == nil {
		return RunResult{}, ErrPermissionDenied
	}
	previous := snapshot.Permission
	if options.channel != nil && (snapshot.Channel == nil || options.channel.ChannelTarget != snapshot.Channel.ChannelTarget || options.personID != previous.PersonID) {
		return RunResult{}, ErrPermissionDenied
	}
	chat, err := core.Store.GetConversation(ctx, run.ConversationID)
	if err != nil {
		return RunResult{}, err
	}
	if previous.SpaceID != "" && previous.SpaceID != chat.SpaceID {
		return RunResult{}, ErrPermissionRevoked
	}
	if options.SpaceID != "" && options.SpaceID != chat.SpaceID {
		return RunResult{}, ErrConversationSpace
	}
	if err := NewTaskService(core).checkTelegramRun(ctx, run); err != nil {
		return RunResult{}, err
	}
	if err := core.resolvePermission(ctx, run.ConversationID, "", false); err != nil {
		return RunResult{}, err
	}
	if previous.PersonID != "" {
		if err := core.resolveRequestPermission(ctx, run.ConversationID, previous.PersonID, previous.Entry); err != nil {
			return RunResult{}, err
		}
	}
	current := *core.permission
	selection := *previous
	selection.Mode = lowerPermission(previous.Mode, current.Mode)
	if options.Permission != "" {
		if err := options.Permission.Validate(); err != nil {
			return RunResult{}, err
		}
		selection.Mode = lowerPermission(selection.Mode, options.Permission)
	}
	selection.SpaceLimit, selection.PersonLimit = current.SpaceLimit, current.PersonLimit
	if current.RemoteLimit != "" {
		selection.RemoteLimit = current.RemoteLimit
		if previous.RemoteLimit != "" {
			selection.RemoteLimit = lowerPermission(previous.RemoteLimit, current.RemoteLimit)
		}
		selection.Mode = lowerPermission(selection.Mode, current.RemoteLimit)
	}
	core.permission = &selection
	refs, err := tasks.resolveAttachments(ctx, run.ConversationID, options.Attachments)
	if err != nil {
		return RunResult{}, err
	}
	core.inputAttachments = refs
	capabilities, err := core.Store.selectedCapabilities(ctx, run.ConversationID, core.Config)
	if err != nil {
		return RunResult{}, err
	}
	if options.Capabilities != nil {
		capabilities = options.Capabilities.normalize()
		if err := capabilities.validate(core.Config); err != nil {
			return RunResult{}, err
		}
	}
	core.capabilities = &capabilities
	if options.channel != nil {
		if err := core.bindChannel(ctx, run.ConversationID, options.channel); err != nil {
			return RunResult{}, err
		}
	} else if snapshot.Channel != nil {
		copy := *snapshot.Channel
		copy.InputMessageID = 0
		copy.RequestKey = ""
		if err := core.Store.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM messages WHERE conversation_id=?`, run.ConversationID).Scan(&copy.HistoryThrough); err != nil {
			return RunResult{}, err
		}
		core.channel = &copy
	}
	core.continuationOf = run.ID
	core.selection = snapshot.Bindings[len(snapshot.Bindings)-1].ID
	if chat.ModelID != "" {
		core.selection = chat.ModelID
	}
	if options.ModelID != "" {
		core.selection = options.ModelID
	}
	key := options.continuationKey
	if key == "" {
		key = "limited-next:" + run.ID
	}
	return core.Run(ctx, run.ConversationID, key, limitContinuationPrompt)
}

func successorTx(ctx context.Context, tx *sql.Tx, parentID, chat string) (string, error) {
	var parentChat, status, reason string
	if err := tx.QueryRowContext(ctx, `SELECT conversation_id,status,stop_reason FROM runs WHERE id=?`, parentID).Scan(&parentChat, &status, &reason); err != nil {
		return "", err
	}
	if parentChat != chat || status != "limited" || !strings.HasPrefix(reason, settledLimitPrefix) {
		return "", ErrActiveRun
	}
	var unsettled int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE run_id=? AND status IN ('pending','dispatched','unknown')`, parentID).Scan(&unsettled); err != nil {
		return "", err
	}
	if unsettled != 0 {
		return "", ErrUnknownResult
	}
	if err := checkConversationCommandsTx(ctx, tx, chat); err != nil {
		return "", err
	}
	var successor string
	err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "run/limit-next/"+parentID).Scan(&successor)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return successor, err
}
