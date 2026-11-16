package harness

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/endview/freeagent/internal/postgres"
)

// UpgradeConversationStore runs only under the OS maintenance lock and PG
// owner exclusion. The old complete format is verified before taking a backup.
func UpgradeConversationStore(ctx context.Context, data, backup string) (returnErr error) {
	data, err := canonicalDir(data)
	if err != nil {
		return err
	}
	lock, err := acquireProcessFileLock(filepath.Join(data, "owner.lock"))
	if err != nil {
		return err
	}
	defer lock.release()
	c, release, err := postgres.AcquireOwner(ctx, data)
	if err != nil {
		return err
	}
	defer release()
	adminConfig, err := maintenanceConnection(data, c)
	if err != nil {
		return err
	}
	adminConfig.Database = c.Database
	db, err := postgres.SQL(adminConfig, "", false)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var held bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(1178686216,1)`).Scan(&held); err != nil {
		return err
	}
	if !held {
		return ErrOwnerActive
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(1178686216,1)`)
	if err = verifyPostgresFormat(ctx, db, c.CoreID, true); err != nil {
		return err
	}
	var version string
	if err = db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key='schema/version'`).Scan(&version); err != nil {
		return err
	}
	if version == "2" {
		return nil
	}
	if backup == "" {
		return errors.New("harness: upgrade requires a new complete --backup directory")
	}
	var active bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.runs WHERE `+activeRunPredicate+`) OR EXISTS(SELECT 1 FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'state' IN ('pending','dispatching','scheduled','reviewing','held','paused'))`).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrActiveRun
	}
	old := &Store{dataDir: data, db: &pgDatabase{DB: db}}
	if err = old.Backup(ctx, backup); err != nil {
		return err
	}
	if err = VerifyBackup(ctx, backup); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Schema metadata changes are authorized maintenance, not an unreviewed
	// edit to user configuration. Preserve the existing control-journal baseline.
	if _, err = tx.ExecContext(ctx, `ALTER TABLE fa.meta DISABLE TRIGGER z_maintenance_revision`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, conversationLifecycleSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value='2' WHERE key='schema/version'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key='schema/source'`, contentHash([]byte(postgresSchema+conversationLifecycleSchema))); err != nil {
		return err
	}
	// Newly created functions must be callable by the restricted Owner role.
	if _, err = tx.ExecContext(ctx, `GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA fa TO `+c.User); err != nil {
		return err
	}
	// schemaIdentity reads catalog state, so calculate using this transaction.
	identity, err := schemaIdentityReader(ctx, tx)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key='schema/identity'`, identity); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE fa.meta ENABLE TRIGGER z_maintenance_revision`); err != nil {
		return err
	}
	return tx.Commit()
}
