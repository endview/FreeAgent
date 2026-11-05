package harness

import (
	"database/sql"
	"errors"
	"github.com/endview/freeagent/internal/postgres"
	"os"
	"path/filepath"
	"testing"
)

func installPGFault(t *testing.T, store *Store, name, table, event, condition string) error {
	t.Helper()
	admin := testPGAdmin(t, store.DataDir())
	when := ""
	if condition != "" {
		when = " WHEN (" + condition + ")"
	}
	_, err := admin.ExecContext(t.Context(), `CREATE FUNCTION fa.`+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture persistence failure'; END $$; CREATE TRIGGER `+name+` BEFORE `+event+` ON fa.`+table+` FOR EACH ROW`+when+` EXECUTE FUNCTION fa.`+name+`()`)
	return err
}
func removePGFault(t *testing.T, store *Store, name, table string) error {
	t.Helper()
	_, err := testPGAdmin(t, store.DataDir()).ExecContext(t.Context(), `DROP TRIGGER `+name+` ON fa.`+table+`; DROP FUNCTION fa.`+name+`()`)
	return err
}
func testPGAdmin(t *testing.T, data string) *sql.DB {
	t.Helper()
	c, err := postgres.Read(filepath.Join(data, postgres.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv(postgres.ServiceFileEnv)
	if c.Managed {
		path = filepath.Join(data, "postgres-maintenance.json")
	}
	admin, err := postgres.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	admin.Database = c.Database
	db, err := postgres.SQL(admin, "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestStoreRejectsUnexpectedOccupancyIndexWithoutWriting(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	data := core.Store.DataDir()
	admin := testPGAdmin(t, data)
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(t.Context(), `DROP INDEX fa.one_active_run; CREATE UNIQUE INDEX one_active_run ON fa.runs(conversation_id) WHERE status='running'`); err != nil {
		t.Fatal(err)
	}
	before, err := schemaIdentity(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, readonly := range []bool{false, true} {
		var store *Store
		if readonly {
			store, err = OpenStoreReadOnly(t.Context(), data)
		} else {
			store, err = OpenStore(t.Context(), data)
		}
		if !errors.Is(err, ErrStoreFormat) {
			if store != nil {
				store.Close()
			}
			t.Fatal("changed schema accepted", err)
		}
	}
	after, err := schemaIdentity(t.Context(), admin)
	if err != nil || after != before {
		t.Fatal("unrecognized schema modified", err)
	}
}
