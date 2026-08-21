package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
)

// withCmdClosedFileTamperV1 models an offline rewrite while the process-owned
// SQLite guards are unavailable. It restores the exact trigger definitions and
// connection constraints before returning so callers continue to exercise the
// production semantic reader or reopen gate against an authentic schema.
func withCmdClosedFileTamperV1(
	t *testing.T,
	databasePath string,
	triggers []string,
	mutation func(context.Context, *sql.Conn) error,
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = database.Close()
		}
	}()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	withCmdClosedFileDatabaseTamperV1(t, database, triggers, mutation)
	if err := database.Close(); err != nil {
		t.Fatalf("close closed-file tamper database: %v", err)
	}
	closed = true
}

func withCmdClosedFileDatabaseTamperV1(
	t *testing.T,
	database *sql.DB,
	triggers []string,
	mutation func(context.Context, *sql.Conn) error,
) {
	t.Helper()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	ctx := context.Background()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	definitions := make([]string, len(triggers))
	for index, trigger := range triggers {
		if err := connection.QueryRowContext(
			ctx,
			`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`,
			trigger,
		).Scan(&definitions[index]); err != nil {
			t.Fatalf("load closed-file trigger %s: %v", trigger, err)
		}
	}
	var foreignKeys, ignoreCheckConstraints int
	if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRowContext(ctx, `PRAGMA ignore_check_constraints`).Scan(&ignoreCheckConstraints); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range triggers {
		if _, err := connection.ExecContext(ctx, `DROP TRIGGER `+trigger); err != nil {
			t.Fatalf("drop closed-file trigger %s: %v", trigger, err)
		}
	}

	mutationErr := mutation(ctx, connection)
	var restoreErr error
	for index := len(definitions) - 1; index >= 0; index-- {
		if _, err := connection.ExecContext(ctx, definitions[index]); err != nil && restoreErr == nil {
			restoreErr = err
		}
	}
	restoreCheckConstraints := `PRAGMA ignore_check_constraints=OFF`
	if ignoreCheckConstraints == 1 {
		restoreCheckConstraints = `PRAGMA ignore_check_constraints=ON`
	}
	if _, err := connection.ExecContext(ctx, restoreCheckConstraints); err != nil && restoreErr == nil {
		restoreErr = err
	}
	restoreForeignKeys := `PRAGMA foreign_keys=OFF`
	if foreignKeys == 1 {
		restoreForeignKeys = `PRAGMA foreign_keys=ON`
	}
	if _, err := connection.ExecContext(ctx, restoreForeignKeys); err != nil && restoreErr == nil {
		restoreErr = err
	}
	for index, trigger := range triggers {
		var restored string
		if err := connection.QueryRowContext(
			ctx,
			`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`,
			trigger,
		).Scan(&restored); err != nil && restoreErr == nil {
			restoreErr = err
		} else if err == nil && restored != definitions[index] && restoreErr == nil {
			restoreErr = fmt.Errorf("trigger %s definition changed", trigger)
		}
	}
	if err := errors.Join(mutationErr, restoreErr); err != nil {
		t.Fatalf("closed-file tamper: %v", err)
	}
}

func execCmdClosedFileTamperV1(
	t *testing.T,
	databasePath string,
	triggers []string,
	statement string,
	arguments ...any,
) sql.Result {
	t.Helper()
	var result sql.Result
	withCmdClosedFileTamperV1(
		t,
		databasePath,
		triggers,
		func(ctx context.Context, connection *sql.Conn) error {
			var err error
			result, err = connection.ExecContext(ctx, statement, arguments...)
			return err
		},
	)
	return result
}

func execCmdClosedFileDatabaseTamperV1(
	t *testing.T,
	database *sql.DB,
	triggers []string,
	statement string,
	arguments ...any,
) sql.Result {
	t.Helper()
	var result sql.Result
	withCmdClosedFileDatabaseTamperV1(
		t,
		database,
		triggers,
		func(ctx context.Context, connection *sql.Conn) error {
			var err error
			result, err = connection.ExecContext(ctx, statement, arguments...)
			return err
		},
	)
	return result
}

func loadCmdRawPublishedBasisV1(
	t *testing.T,
	databasePath string,
) controlcontract.PublishedBasis {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	basis := controlcontract.PublishedBasis{TenantID: defaultTenantID}
	if err := database.QueryRowContext(context.Background(), `
		SELECT current.pointer_revision,
			current.snapshot_id, control.revision, control.digest,
			current.catalog_generation_id, catalog.generation, catalog.digest
		FROM control_current AS current
		JOIN control_snapshots AS control ON control.snapshot_id=current.snapshot_id
		JOIN runtime_catalog_generations AS catalog
			ON catalog.generation_id=current.catalog_generation_id
		WHERE current.tenant_id=?
	`, defaultTenantID).Scan(
		&basis.PointerRevision,
		&basis.Control.SnapshotID,
		&basis.Control.Revision,
		&basis.Control.Digest,
		&basis.Catalog.GenerationID,
		&basis.Catalog.Generation,
		&basis.Catalog.Digest,
	); err != nil {
		t.Fatal(err)
	}
	return basis
}
