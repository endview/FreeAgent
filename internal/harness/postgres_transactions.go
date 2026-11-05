package harness

import (
	"context"
	"database/sql"
	"errors"
)

func lockConversationTx(ctx context.Context, tx *sql.Tx, chat string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM fa.conversations WHERE id=$1 FOR UPDATE`, chat).Scan(&id)
}

// Ordinary writes lock conversation then Run. A collaboration first locks its
// shared budget overlay, consistently with its edit/cancel/accounting paths.
// None of these locks crosses a model, command, or network operation.
func lockExecutionTx(ctx context.Context, tx *sql.Tx, run string) error {
	var parent string
	err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "collaboration-run/"+run).Scan(&parent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if parent != "" {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1 FOR UPDATE`, "collaboration/"+parent).Scan(&raw); err != nil {
			return err
		}
	}
	var chat string
	if err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM fa.runs WHERE id=$1`, run).Scan(&chat); err != nil {
		return err
	}
	if err := lockConversationTx(ctx, tx, chat); err != nil {
		return err
	}
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM fa.runs WHERE id=$1 FOR UPDATE`, run).Scan(&id)
}
func loadCollaborationForUpdate(ctx context.Context, tx *sql.Tx, parent string) (collaborationRecord, error) {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT key FROM fa.meta WHERE key=$1 FOR UPDATE`, "collaboration/"+parent).Scan(&id); err != nil {
		return collaborationRecord{}, err
	}
	return loadCollaboration(ctx, tx, parent)
}
