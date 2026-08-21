package currentbackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	_ "modernc.org/sqlite"
)

func TestInspectSnapshotRejectsInstallationPopulationBeforeMaterialization(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "installation-population.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(databasePath, "rw", "foreign_keys(1)"),
	)
	if err != nil {
		t.Fatal(err)
	}
	manifestRef := strings.Repeat("a", 64)
	artifactDigest := strings.Repeat("b", 64)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes,
			size_bytes, created_at
		) VALUES (?, 'MODULE_MANIFEST', 'application/json', ?, 2, 1)
	`, manifestRef, []byte("{}")); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO module_installations(
			installation_id, module_id, exact_version, manifest_ref,
			artifact_digest, installed_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		_ = database.Close()
		t.Fatal(err)
	}
	for index := 0; index <= maxArtifactCount; index++ {
		if _, err := statement.ExecContext(
			ctx,
			fmt.Sprintf("installation-%03d", index),
			fmt.Sprintf("freeagent.test.population.%03d", index),
			fmt.Sprintf("1.0.%d", index),
			manifestRef,
			artifactDigest,
			index+1,
		); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			_ = database.Close()
			t.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		_ = database.Close()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = inspectSnapshot(ctx, databasePath)
	if !errors.Is(err, ErrIntegrity) ||
		!strings.Contains(err.Error(), "module installation count exceeds 256") {
		t.Fatalf("inspectSnapshot(over-limit installations) error=%v", err)
	}
}
