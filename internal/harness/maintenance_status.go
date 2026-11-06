package harness

import (
	"context"
	"database/sql"
	"errors"
)

// MaintenanceStatus projects existing execution facts without claiming an
// Owner, resolving Keys, or returning prompts, results, command lines or paths.
type MaintenanceStatus struct {
	ActiveRuns           int `json:"active_runs"`
	WaitingConfirmations int `json:"waiting_confirmations"`
	UnknownRuns          int `json:"unknown_runs"`
	StoppedRuns          int `json:"stopped_runs"`
	UnknownAttempts      int `json:"unknown_attempts"`
	ActiveCommands       int `json:"active_commands"`
	RecentFailedRuns     int `json:"recent_failed_runs"`
	RecentRuns           int `json:"recent_runs"`
}

// MaintenanceStatus is a consistent, bounded read of existing records. A zero
// count is an observation, not an update lock: dispatch must be stopped by the
// normal update procedure before replacing executable files.
func (store *Store) MaintenanceStatus(ctx context.Context) (MaintenanceStatus, error) {
	var result MaintenanceStatus
	if store == nil || ctx == nil {
		return result, errors.New("harness: store and context are required")
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT
	 count(*) FILTER (WHERE status='running'),
	 count(*) FILTER (WHERE status='waiting_confirmation'),
	 count(*) FILTER (WHERE status='unknown'),
	 count(*) FILTER (WHERE status='stopped') FROM fa.runs`).Scan(
		&result.ActiveRuns, &result.WaitingConfirmations, &result.UnknownRuns, &result.StoppedRuns)
	if err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts WHERE status='unknown'`).Scan(&result.UnknownAttempts); err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key LIKE 'command/process/%' AND document->>'state' IN ('running','stopping')`).Scan(&result.ActiveCommands); err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER (WHERE status='failed') FROM
	 (SELECT status FROM fa.runs ORDER BY updated_at DESC,id DESC LIMIT 100) recent`).Scan(&result.RecentRuns, &result.RecentFailedRuns); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
