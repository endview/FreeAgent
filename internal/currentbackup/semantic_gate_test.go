package currentbackup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyCurrentStoreSemanticClosureRejectsChannelIdentityDrift(
	t *testing.T,
) {
	fixture := newChannelBackupFixture(t)
	if err := VerifyCurrentStoreSemanticClosure(
		context.Background(),
		fixture.databasePath,
	); err != nil {
		t.Fatalf("verify intact Store: %v", err)
	}
	database, err := sql.Open("sqlite", fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := database.Exec(`
		UPDATE channel_ingress_receipts
		SET provider_event_id_digest=?
		WHERE disposition='REJECTED'
	`, strings.Repeat("f", 64))
	closeErr := database.Close()
	if err := errors.Join(updateErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(
		context.Background(),
		fixture.databasePath,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("semantic drift error=%v, want ErrIntegrity", err)
	}
}

func TestBackupSemanticGateRejectsHistoricalOverviewResourceSnapshotTamper(
	t *testing.T,
) {
	fixture := newChannelBackupFixture(t)
	copyPath := filepath.Join(t.TempDir(), "observation-tampered.sqlite")
	copyTestFile(t, fixture.databasePath, copyPath)
	database, err := sql.Open("sqlite", sqliteFileURI(copyPath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	var triggerSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema
		WHERE type='trigger' AND name='overview_resource_snapshots_reject_update'`).Scan(
		&triggerSQL,
	); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TRIGGER overview_resource_snapshots_reject_update`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE overview_resource_snapshots
		SET canonical_json=X'7B7D' WHERE resource_kind='CHANNEL_SEND'
		AND observation_sequence=1`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(triggerSQL); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrentStoreSemanticClosure(
		context.Background(), copyPath,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("VerifyCurrentStoreSemanticClosure error=%v, want ErrIntegrity", err)
	}
	if _, err := CreateBundle(
		context.Background(), copyPath, fixture.artifactRoot,
		filepath.Join(t.TempDir(), "rejected.bundle"), "observation-semantic-test/v1",
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("CreateBundle error=%v, want ErrIntegrity", err)
	}
}
