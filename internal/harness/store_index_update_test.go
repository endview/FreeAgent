package harness

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const initialOccupancyIndex = `CREATE UNIQUE INDEX one_active_run ON runs(conversation_id) WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`

func replaceOccupancyIndex(t *testing.T, data, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(data, "state.sqlite"), "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `DROP INDEX one_active_run`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func occupancyIndex(t *testing.T, store *Store) string {
	t.Helper()
	var statement string
	if err := store.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_schema WHERE name='one_active_run'`).Scan(&statement); err != nil {
		t.Fatal(err)
	}
	return statement
}

func TestStoreOccupancyIndexUpdatePreservesCurrentFormatHistory(t *testing.T) {
	service, _, original, _ := limitedFixture(t)
	data := service.Store.DataDir()
	if _, err := service.Store.db.ExecContext(t.Context(), `UPDATE runs SET stop_reason=?,config_json=json_remove(config_json,'$.command_rules_version') WHERE id=?`, ErrStepLimit.Error(), original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.db.ExecContext(t.Context(), `UPDATE attempts SET status='pending',response_json='' WHERE run_id=? AND kind='tool'`, original.ID); err != nil {
		t.Fatal(err)
	}
	wantRun, err := service.Store.GetRun(t.Context(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantAttempts, err := service.Store.ListAttempts(t.Context(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	replaceOccupancyIndex(t, data, initialOccupancyIndex)
	path := filepath.Join(data, "state.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := OpenStoreReadOnly(t.Context(), data)
	if err != nil {
		t.Fatalf("same-format data rejected before index update: %v", err)
	}
	if occupancyIndex(t, viewer) != initialOccupancyIndex {
		t.Fatal("read-only verification modified the index")
	}
	if err := viewer.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || contentHash(before) != contentHash(after) {
		t.Fatal("read-only verification wrote to the store", err)
	}
	for range 2 {
		store, err := OpenStore(t.Context(), data)
		if err != nil {
			t.Fatalf("current-format Owner failed to open: %v", err)
		}
		if occupancyIndex(t, store) != activeRunIndex {
			t.Fatal("Owner did not apply the current occupancy index")
		}
		gotRun, err := store.GetRun(t.Context(), original.ID)
		if err != nil || !reflect.DeepEqual(gotRun, wantRun) {
			t.Fatal("index update changed the limited Run or its frozen snapshot", err)
		}
		gotAttempts, err := store.ListAttempts(t.Context(), original.ID)
		if err != nil || !reflect.DeepEqual(gotAttempts, wantAttempts) {
			t.Fatal("index update dispatched or changed the pending tool", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreRejectsUnexpectedOccupancyIndexWithoutWriting(t *testing.T) {
	service, _, _, _ := limitedFixture(t)
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	replaceOccupancyIndex(t, data, `CREATE UNIQUE INDEX one_active_run ON runs(conversation_id) WHERE status='running'`)
	path := filepath.Join(data, "state.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(t.Context(), data); !errors.Is(err, ErrStoreFormat) {
		if store != nil {
			store.Close()
		}
		t.Fatal("unexpected schema was accepted", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || contentHash(before) != contentHash(after) {
		t.Fatal("unrecognized schema was modified before rejection", err)
	}
}
