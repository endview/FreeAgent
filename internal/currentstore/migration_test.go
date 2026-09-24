package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaVersionRegistryFreezesVersionOne(t *testing.T) {
	version, ok := KnownSchemaVersion(1)
	if !ok || version.UserVersion != 1 ||
		version.SchemaFingerprint != "9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561" {
		t.Fatalf("version 1 = %+v, known=%v", version, ok)
	}
	current, ok := KnownSchemaVersion(2)
	if !ok || current.UserVersion != UserVersion || current.SchemaFingerprint != ExpectedSchemaFingerprint {
		t.Fatalf("version 2 = %+v, known=%v", current, ok)
	}
	if _, ok := KnownSchemaVersion(0); ok {
		t.Fatal("blank schema version is registered")
	}
	if _, ok := KnownSchemaVersion(UserVersion + 1); ok {
		t.Fatal("future schema version is registered")
	}
	migration, err := Migration0001()
	if err != nil {
		t.Fatal(err)
	}
	if len(migration) == 0 {
		t.Fatal("0001 migration is empty")
	}
	migration, err = Migration0002()
	if err != nil || len(migration) == 0 {
		t.Fatalf("0002 migration = %d bytes, error=%v", len(migration), err)
	}
}

func TestInspectKnownSchemaVersionRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.sqlite")
	if _, err := InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=99`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = InspectKnownSchemaVersionReadOnly(context.Background(), path)
	if !errors.Is(err, ErrUnknownSchemaVersion) {
		t.Fatalf("InspectKnownSchemaVersionReadOnly() error = %v", err)
	}
}

func TestInspectKnownSchemaVersionRejectsMetadataDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata-drift.sqlite")
	if _, err := InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE store_meta SET schema_fingerprint=?`,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = InspectKnownSchemaVersionReadOnly(context.Background(), path)
	var identityErr *IdentityError
	if !errors.As(err, &identityErr) || identityErr.Field != "schema_fingerprint" {
		t.Fatalf("InspectKnownSchemaVersionReadOnly() error = %v", err)
	}
}

func TestOfflineMigrationIsExplicitAndFenced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	var retained *OfflineLease
	err := WithOfflineLease(
		context.Background(),
		path,
		func(lease *OfflineLease) error {
			retained = lease
			if _, err := OpenExistingCurrentStore(context.Background(), path); !errors.Is(err, ErrOwnerActive) {
				t.Fatalf("overlapping OpenExistingCurrentStore() error = %v", err)
			}
			result, err := MigrateOfflineCurrentStore(context.Background(), lease)
			if err != nil {
				return err
			}
			if result.FromVersion != UserVersion || result.ToVersion != UserVersion ||
				len(result.AppliedVersions) != 0 {
				t.Fatalf("migration result = %+v", result)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Path(); err == nil {
		t.Fatal("retained offline lease remained active")
	}
	store, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".freeagent.owner.lock"); err != nil {
		t.Fatalf("persistent owner lock missing: %v", err)
	}
}

func TestOfflineMigrationUpgradesFAC2V1ToServerOwnedReviewV2(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fac2-v1.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := Migration0001()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO store_meta(
			singleton,store_instance_id,schema_identity,schema_version,
			schema_fingerprint,generator_id,created_at
		) VALUES(1,?,?,?,?,?,?)`,
		"migration-test-store", SchemaIdentity, 1,
		"9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561",
		GeneratorID, int64(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA application_id=1178682162; PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if version, err := InspectKnownSchemaVersionReadOnly(ctx, path); err != nil || version.UserVersion != 1 {
		t.Fatalf("inspect FAC2 v1 = %+v, error=%v", version, err)
	}
	resultErr := WithOfflineLease(ctx, path, func(lease *OfflineLease) error {
		result, err := MigrateOfflineCurrentStore(ctx, lease)
		if err != nil {
			return err
		}
		if result.FromVersion != 1 || result.ToVersion != 2 ||
			len(result.AppliedVersions) != 1 || result.AppliedVersions[0] != 2 ||
			result.Verification.SchemaFingerprint != ExpectedSchemaFingerprint {
			t.Fatalf("migration result = %+v", result)
		}
		return nil
	})
	if resultErr != nil {
		t.Fatal(resultErr)
	}
	verification, err := VerifyCurrentStoreReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if verification.SchemaVersion != 2 || verification.SchemaFingerprint != ExpectedSchemaFingerprint {
		t.Fatalf("upgraded verification = %+v", verification)
	}
}
