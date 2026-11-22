package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	tgpolicy "github.com/endview/freeagent/internal/connectors/telegram"
	"github.com/endview/freeagent/internal/statemodule"
)

type ChannelConfirmers struct {
	Revision int64    `json:"revision"`
	Members  []string `json:"members"`
}
type confirmerResult struct {
	statemodule.Result
	State ChannelConfirmers `json:"state"`
}

func (tasks *TaskService) confirmerKey(bot string) (string, error) {
	if !telegramNumeric(bot, true) {
		return "", ErrPermissionDenied
	}
	tasks.core.Store.telegramMu.Lock()
	known := tasks.core.Store.telegramBots[bot]
	tasks.core.Store.telegramMu.Unlock()
	if !known {
		return "", ErrPermissionDenied
	}
	return "connector/telegram/" + bot + "/confirmers", nil
}

// These are management methods, never registered as model or channel tools.
// The HTTP/CLI entry must authenticate the owner before calling Apply.
func (tasks *TaskService) TelegramConfirmers(ctx context.Context, bot string) (ChannelConfirmers, error) {
	key, err := tasks.confirmerKey(bot)
	if err != nil {
		return ChannelConfirmers{}, err
	}
	result := ChannelConfirmers{Members: []string{}}
	var raw string
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Revision < 0 {
		return result, ErrStoreFormat
	}
	return result, nil
}

func (tasks *TaskService) PreviewTelegramConfirmers(ctx context.Context, bot string, members []string, revision int64) (statemodule.Preview, error) {
	next, err := tgpolicy.Confirmers(members)
	if err != nil {
		return statemodule.Preview{}, err
	}
	current, err := tasks.TelegramConfirmers(ctx, bot)
	if err != nil {
		return statemodule.Preview{}, err
	}
	if current.Revision != revision {
		return statemodule.Preview{}, ErrControlStaleRevision
	}
	return statemodule.Preview{Revision: revision, Before: mustJSON(current.Members), After: mustJSON(next), Summary: "Telegram settings confirmation people; chat admission and tool permissions are unchanged"}, nil
}

func (tasks *TaskService) ApplyTelegramConfirmers(ctx context.Context, bot string, members []string, revision int64, operation string) (ChannelConfirmers, error) {
	release, err := BeginPublicSettingsDecision(ctx, true)
	if err != nil {
		return ChannelConfirmers{}, err
	}
	defer release()
	key, err := tasks.confirmerKey(bot)
	if err != nil {
		return ChannelConfirmers{}, err
	}
	if !validChannelPart(operation) || revision < 0 {
		return ChannelConfirmers{}, ErrRequestKey
	}
	next, err := tgpolicy.Confirmers(members)
	if err != nil {
		return ChannelConfirmers{}, err
	}
	digest := contentHash(mustJSON([]any{key, revision, next}))
	ledger := key + "/result/" + operation
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return ChannelConfirmers{}, err
	}
	defer tx.Rollback()
	// A single row lock covers this Bot's roster and operation result. The
	// existing Core store remains the sole writer; no platform DB connection.
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, string(mustJSON(ChannelConfirmers{Members: []string{}}))); err != nil {
		return ChannelConfirmers{}, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, key).Scan(&raw); err != nil {
		return ChannelConfirmers{}, err
	}
	var current ChannelConfirmers
	if json.Unmarshal([]byte(raw), &current) != nil {
		return current, ErrStoreFormat
	}
	var oldResult string
	err = tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, ledger).Scan(&oldResult)
	if err == nil {
		var result confirmerResult
		if json.Unmarshal([]byte(oldResult), &result) != nil || result.Digest != digest {
			return current, ErrRequestKey
		}
		return result.State, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return current, err
	}
	if current.Revision != revision {
		return current, ErrControlStaleRevision
	}
	if !slices.Equal(current.Members, next) {
		current = ChannelConfirmers{Revision: revision + 1, Members: next}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(current)), key); err != nil {
		return current, err
	}
	result := confirmerResult{Result: statemodule.Result{Found: true, OperationID: operation, Digest: digest, Revision: current.Revision}, State: current}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, ledger, string(mustJSON(result))); err != nil {
		return current, err
	}
	return current, tx.Commit()
}

func (tasks *TaskService) TelegramConfirmersResult(ctx context.Context, bot, operation string) (statemodule.Result, error) {
	key, err := tasks.confirmerKey(bot)
	if err != nil {
		return statemodule.Result{}, err
	}
	if !validChannelPart(operation) {
		return statemodule.Result{}, ErrRequestKey
	}
	var raw string
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key+"/result/"+operation).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return statemodule.Result{}, nil
	}
	var result statemodule.Result
	if err == nil && json.Unmarshal([]byte(raw), &result) != nil {
		err = ErrStoreFormat
	}
	return result, err
}
