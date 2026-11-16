package harness

import (
	"context"
	"database/sql"
)

// A restricted audience cannot inherit private optional methods. Keep an
// explicit empty selection, so exclusions never become global defaults.
func protectMovedCapabilities(ctx context.Context, tx *sql.Tx, chat, spaceID string) error {
	var ownerPrivate bool
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT (document->>'owner_private')::boolean FROM fa.meta WHERE key=$1),false)`, "space/permission/"+spaceID).Scan(&ownerPrivate); err != nil {
		return err
	}
	if ownerPrivate {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'{}') ON CONFLICT(key) DO UPDATE SET value='{}'`, capabilityMetaPrefix+chat)
	return err
}
