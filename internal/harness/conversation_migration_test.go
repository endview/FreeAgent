package harness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/postgres"
)

func TestConversationSchemaUpgradeBacksUpLegacyAndRejectsLegacyWriter(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, core)
	data := core.Store.dataDir
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	c, release, err := postgres.AcquireOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	a, err := maintenanceConnection(data, c)
	if err != nil {
		release()
		t.Fatal(err)
	}
	a.Database = c.Database
	db, err := postgres.SQL(a, "", false)
	if err != nil {
		release()
		t.Fatal(err)
	}
	for _, query := range []string{`DROP FUNCTION fa.guard_live_meta() CASCADE`, `DROP FUNCTION fa.guard_space_route() CASCADE`, `DROP FUNCTION fa.guard_live_record() CASCADE`, `DROP FUNCTION fa.conversation_live(text) CASCADE`, `UPDATE fa.meta SET value='1' WHERE key='schema/version'`} {
		if _, err = db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key='schema/source'`, contentHash([]byte(postgresSchema))); err != nil {
		t.Fatal(err)
	}
	identity, err := schemaIdentity(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key='schema/identity'`, identity); err != nil {
		t.Fatal(err)
	}
	if err = verifyPostgres(t.Context(), db, c.CoreID); !errors.Is(err, ErrStoreFormat) {
		t.Fatal("new writer accepted legacy", err)
	}
	if err = verifyPostgresFormat(t.Context(), db, c.CoreID, true); err != nil {
		t.Fatal(err)
	}
	db.Close()
	release()
	backup := filepath.Join(t.TempDir(), "pre-upgrade")
	if err = UpgradeConversationStore(t.Context(), data, backup); err != nil {
		t.Fatal(err)
	}
	if err = VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err = UpgradeConversationStore(t.Context(), data, backup); err != nil {
		t.Fatal("idempotent upgrade", err)
	}
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.GetConversation(t.Context(), chat); err != nil {
		t.Fatal("migration lost chat", err)
	}
	if legacy := os.Getenv("FREEAGENT_TEST_LEGACY_ENGINE"); legacy != "" {
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
		output, oldErr := exec.CommandContext(t.Context(), legacy, "run", "--data-dir", data, "--session", chat, "must not execute against schema 2").CombinedOutput()
		if oldErr == nil || !strings.Contains(strings.ToLower(string(output)), "format") {
			t.Fatal("old engine did not reject upgraded storage", string(output), oldErr)
		}
		fresh, openErr := OpenStoreReadOnly(t.Context(), data)
		if openErr != nil {
			t.Fatal(openErr)
		}
		var count int
		openErr = fresh.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.runs WHERE conversation_id=$1`, chat).Scan(&count)
		fresh.Close()
		if openErr != nil || count != 0 {
			t.Fatal("old engine wrote a Run", count, openErr)
		}
		t.Log("released dev.8 executable rejected schema 2 before execution")
	}
	restore := filepath.Join(t.TempDir(), "restored-legacy")
	if err = Restore(t.Context(), backup, restore); err != nil {
		t.Fatal("restore pre-upgrade backup", err)
	}
	old, err := OpenStoreReadOnly(t.Context(), restore)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
}
