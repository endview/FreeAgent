package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (store *Store) maintainConversationTrash(owner *pgOwner) {
	defer close(owner.cleanupDone)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	last := time.Now().UTC()
	for {
		select {
		case <-owner.ctx.Done():
			return
		case <-owner.stop:
			return
		case <-timer.C:
			now := time.Now().UTC()
			if now.Sub(last) > 2*time.Hour+time.Minute {
				_, _ = store.db.ExecContext(owner.ctx, `INSERT INTO fa.meta(key,value) VALUES('conversation/cleanup-status',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(mustJSON(map[string]any{"at": nowText(), "failed": true, "reason": "clock_jump", "message": "系统时间异常前移，本批自动清理已暂停"})))
				timer.Reset(time.Hour)
				last = now
				continue
			}
			ctx, cancel := context.WithTimeout(owner.ctx, 15*time.Second)
			err := store.expireConversations(ctx, now)
			cancel()
			if !errors.Is(err, context.Canceled) {
				_, _ = store.db.ExecContext(owner.ctx, `INSERT INTO fa.meta(key,value) VALUES('conversation/cleanup-status',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(mustJSON(map[string]any{"at": nowText(), "failed": err != nil})))
			}
			last = now
			timer.Reset(time.Hour)
		}
	}
}

// FinishManagementTasks is explicit user action, bound to a prior exact list.
// It requests cancellation then ends only Runs which have actually quiesced.
func (tasks *TaskService) FinishManagementTasks(ctx context.Context, preview string) error {
	if err := tasks.managementOwner(); err != nil {
		return err
	}
	var raw string
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management/"+preview).Scan(&raw); err != nil {
		return err
	}
	var p ManagementPreview
	if json.Unmarshal([]byte(raw), &p) != nil {
		return ErrStoreFormat
	}
	expiry, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil || time.Now().After(expiry) {
		return ErrManagementStale
	}
	for _, item := range p.Items {
		if item.Workspace != tasks.Config().Workspace {
			return ErrConversationSpace
		}
		current, e := managedConversation(ctx, tasks.core.Store.db, item.ID)
		if e != nil {
			return e
		}
		if len(current.ActiveRuns) != len(item.ActiveRuns) {
			return ErrManagementStale
		}
		for i, id := range current.ActiveRuns {
			if id != item.ActiveRuns[i] {
				return ErrManagementStale
			}
		}
		tasks.CancelConversation(item.ID)
		commands, cmdErr := tasks.core.Store.db.QueryContext(ctx, `SELECT document->>'id' FROM fa.meta WHERE key LIKE 'command/process/%' AND document->>'conversation_id'=$1 AND document->>'state' IN ('running','stopping')`, item.ID)
		if cmdErr != nil {
			return cmdErr
		}
		var commandIDs []string
		for commands.Next() {
			var commandID string
			if cmdErr = commands.Scan(&commandID); cmdErr != nil {
				commands.Close()
				return cmdErr
			}
			commandIDs = append(commandIDs, commandID)
		}
		cmdErr = errors.Join(commands.Err(), commands.Close())
		if cmdErr != nil {
			return cmdErr
		}
		for _, commandID := range commandIDs {
			if _, cmdErr = tasks.StopCommand(ctx, commandID); cmdErr != nil {
				return cmdErr
			}
		}
		rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT id,status FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate, item.ID)
		if err != nil {
			return err
		}
		type pair struct{ id, status string }
		var runs []pair
		for rows.Next() {
			var r pair
			if err = rows.Scan(&r.id, &r.status); err != nil {
				rows.Close()
				return err
			}
			runs = append(runs, r)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, r := range runs {
			if r.status == "running" {
				return ErrActiveRun
			}
			if err = tasks.Abandon(ctx, r.id); err != nil {
				return err
			}
		}
		tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err = lockConversationTx(ctx, tx, item.ID); err == nil {
			err = cancelManagedPending(ctx, tx, item.ID)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
	tasks.core.event(Event{Kind: EventRefresh, Message: "整理前任务处理已完成，请重新预览"})
	return nil
}

func (tasks *TaskService) ManagedChat(ctx context.Context, id, before string) (ChatView, error) {
	if err := tasks.managementOwner(); err != nil {
		return ChatView{}, err
	}
	var state string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT document->>'state' FROM fa.meta WHERE key=$1`, lifecyclePrefix+id).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChatView{}, err
	}
	if state == "purged" {
		return ChatView{}, ErrConversationRetired
	}
	return tasks.Chat(context.WithValue(ctx, managementAccessKey{}, true), id, before)
}

func (tasks *TaskService) ManagedUnresolved(ctx context.Context, id string, after int64) ([]UnresolvedItem, error) {
	if err := tasks.managementOwner(); err != nil {
		return nil, err
	}
	return tasks.Unresolved(context.WithValue(ctx, managementAccessKey{}, true), id, after)
}

func (tasks *TaskService) ManagedDeliveries(ctx context.Context, id, cursor string) ([]DeliveryGroup, error) {
	if err := tasks.managementOwner(); err != nil {
		return nil, err
	}
	return tasks.ChannelDeliveries(context.WithValue(ctx, managementAccessKey{}, true), id, cursor)
}

func (tasks *TaskService) ManagedResolveUnresolved(ctx context.Context, chat, id, revision, action string) error {
	if err := tasks.managementOwner(); err != nil {
		return err
	}
	return tasks.ResolveUnresolved(context.WithValue(ctx, managementAccessKey{}, true), chat, id, revision, action, "", "interactive_owner")
}
