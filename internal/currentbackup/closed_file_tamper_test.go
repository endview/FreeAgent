package currentbackup

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// execClosedFileTamperV1 models an offline database rewrite while the
// process-owned SQLite guards are unavailable. It restores the byte-exact
// trigger definitions and connection constraints before the semantic backup
// gates inspect the closed database.
func execClosedFileTamperV1(
	t *testing.T,
	database *sql.DB,
	triggers []string,
	statement string,
	arguments ...any,
) sql.Result {
	t.Helper()
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
			t.Fatalf("load trigger %s: %v", trigger, err)
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
			t.Fatalf("drop trigger %s: %v", trigger, err)
		}
	}

	result, mutationErr := connection.ExecContext(ctx, statement, arguments...)
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
	if restoreErr != nil {
		t.Fatalf("restore closed-file tamper guards: %v", restoreErr)
	}
	if mutationErr != nil {
		t.Fatalf("apply closed-file tamper: %v", mutationErr)
	}
	return result
}
