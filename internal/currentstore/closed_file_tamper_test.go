package currentstore

import (
	"context"
	"testing"
)

// execClosedFileTamperV1 models storage drift while the process-owned SQLite
// guards are unavailable. It restores the exact trigger definitions before it
// returns so the subsequent assertion exercises the semantic reader or reopen
// gate against an otherwise authentic schema.
func execClosedFileTamperV1(
	t *testing.T,
	store *Store,
	triggers []string,
	statement string,
	args ...any,
) {
	t.Helper()
	ctx := context.Background()
	connection, err := store.db.Conn(ctx)
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

	_, mutationErr := connection.ExecContext(ctx, statement, args...)
	restoreErr := error(nil)
	for index := len(definitions) - 1; index >= 0; index-- {
		if _, err := connection.ExecContext(ctx, definitions[index]); err != nil && restoreErr == nil {
			restoreErr = err
		}
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`); err != nil && restoreErr == nil {
		restoreErr = err
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil && restoreErr == nil {
		restoreErr = err
	}
	if restoreErr != nil {
		t.Fatalf("restore closed-file tamper guards: %v", restoreErr)
	}
	if mutationErr != nil {
		t.Fatalf("apply closed-file tamper: %v", mutationErr)
	}
}
