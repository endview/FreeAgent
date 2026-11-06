package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/postgres"
)

type backupIdentity struct {
	CoreID string `json:"core_id"`
	Schema int    `json:"schema"`
}

func (store *Store) Backup(ctx context.Context, target string) error {
	return store.backupSnapshot(ctx, target, nil)
}
func (store *Store) backupSnapshot(ctx context.Context, target string, check func(context.Context, *sql.Tx) error) error {
	// Attachment retirement and snapshot copying exclude one another. Task
	// dispatch/model calls and normal database writes remain available.
	store.attachmentMu.Lock()
	defer store.attachmentMu.Unlock()
	target, err := canonicalDir(target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: backup target must not exist")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".harness-backup-*")
	if err != nil {
		return err
	}
	defer removeStaging(stage)
	config, err := backupConfig(store.dataDir)
	if err != nil {
		return err
	}
	if err := copyRegularFile(filepath.Join(store.dataDir, "config.json"), filepath.Join(stage, "config.json")); err != nil {
		return err
	}
	if err := copyInstructions(store.dataDir, stage, config.Instructions); err != nil {
		return err
	}
	c, err := postgres.Read(filepath.Join(store.dataDir, postgres.ConfigFile))
	if err != nil {
		return err
	}
	backupCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	tx, err := store.db.DB.BeginTx(backupCtx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(backupCtx, `SET LOCAL idle_in_transaction_session_timeout='0'`); err != nil {
		return err
	}
	var snapshot string
	if err := tx.QueryRowContext(backupCtx, `SELECT pg_export_snapshot()`).Scan(&snapshot); err != nil {
		return err
	}
	if check != nil {
		if err := check(backupCtx, tx); err != nil {
			return err
		}
	}
	refs, err := snapshotAttachmentRefs(backupCtx, tx)
	if err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(stage, "attachment-refs.json"), mustJSON(refs)); err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(stage, "identity.json"), mustJSON(backupIdentity{c.CoreID, storeSchemaVersion})); err != nil {
		return err
	}
	cmd, err := postgres.Command(backupCtx, c, "pg_dump", "--format=custom", "--schema=fa", "--no-owner", "--no-privileges", "--snapshot="+snapshot, "--file="+filepath.Join(stage, "state.pgdump"))
	if err != nil {
		return err
	}
	// Keep utility stderr private; the API receives a safe actionable message.
	log, err := os.CreateTemp("", "freeagent-pg-dump-*")
	if err != nil {
		return err
	}
	defer os.Remove(log.Name())
	defer log.Close()
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("harness: PostgreSQL snapshot failed (%w); verify pg_dump and database availability", err)
	}
	if err := copyAttachmentRefs(store.dataDir, stage, refs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	manifest, err := manifestFor(stage)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(stage, "backup.json"), append(raw, '\n')); err != nil {
		return err
	}
	if err := syncFile(filepath.Join(stage, "state.pgdump")); err != nil {
		return err
	}
	return publishDirectory(stage, target)
}

func Restore(ctx context.Context, source, target string) (returnErr error) {
	source, err := canonicalDir(source)
	if err != nil {
		return err
	}
	target, err = canonicalDir(target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("harness: restore target must be a new directory")
	}
	if err := VerifyBackup(ctx, source); err != nil {
		return err
	}
	identityRaw, err := os.ReadFile(filepath.Join(source, "identity.json"))
	if err != nil {
		return err
	}
	var identity backupIdentity
	if json.Unmarshal(identityRaw, &identity) != nil || !memoryID(identity.CoreID) || identity.Schema != storeSchemaVersion {
		return ErrStoreFormat
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".harness-restore-*")
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, removeStaging(stage)) }()
	c, admin, release, err := postgres.Bootstrap(ctx, stage)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, admin.Close(), release()) }()
	adminConfig, err := maintenanceConnection(stage, c)
	if err != nil {
		return err
	}
	adminConfig.Database = c.Database
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION pg_trgm WITH SCHEMA public`); err != nil {
		return err
	}
	cmd, err := postgres.Command(ctx, adminConfig, "pg_restore", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname="+c.Database, filepath.Join(source, "state.pgdump"))
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("harness: PostgreSQL restore failed: %w", err)
	}
	if _, err := admin.ExecContext(ctx, `GRANT USAGE ON SCHEMA fa TO `+c.User+`; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA fa TO `+c.User+`; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA fa TO `+c.User); err != nil {
		return err
	}
	c.CoreID = identity.CoreID
	if c.Managed {
		c.Bin = ""
	}
	if err := os.Remove(filepath.Join(stage, postgres.ConfigFile)); err != nil {
		return err
	}
	if err := postgres.Write(filepath.Join(stage, postgres.ConfigFile), c); err != nil {
		return err
	}
	if err := verifyPostgres(ctx, admin, c.CoreID); err != nil {
		return err
	}
	if err := copyRegularFile(filepath.Join(source, "config.json"), filepath.Join(stage, "config.json")); err != nil {
		return err
	}
	config, err := backupConfig(stage)
	if err != nil {
		return err
	}
	if err := copyInstructions(source, stage, config.Instructions); err != nil {
		return err
	}
	refs, err := readSnapshotAttachmentRefs(source)
	if err != nil {
		return err
	}
	actualRefs, err := snapshotAttachmentRefs(ctx, admin)
	if err != nil {
		return err
	}
	if string(mustJSON(actualRefs)) != string(mustJSON(refs)) {
		return errors.New("harness: restored database and attachment snapshot references differ")
	}
	if err := copyAttachmentRefs(source, stage, refs); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stage, "backups"), 0700); err != nil {
		return err
	}
	// A managed cluster is stopped before its directory changes location.
	if err := admin.Close(); err != nil {
		return err
	}
	if err := release(); err != nil {
		return err
	}
	return publishDirectory(stage, target)
}

func maintenanceConnection(data string, c postgres.Connection) (postgres.Connection, error) {
	if !c.Managed {
		return postgres.Read(os.Getenv(postgres.ServiceFileEnv))
	}
	return postgres.Read(filepath.Join(data, "postgres-maintenance.json"))
}
