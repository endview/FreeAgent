package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

var ErrUnknownSchemaVersion = errors.New("currentstore: unknown schema version")

// MigrationResult describes an explicit offline migration. AppliedVersions is
// empty when the Store is already current.
type MigrationResult struct {
	FromVersion     int
	ToVersion       int
	AppliedVersions []int
	Verification    Verification
}

type migrationStep struct {
	fromVersion int
	toVersion   int
	name        string
	loadSQL     func() ([]byte, error)
}

// migrationSteps contains forward-only transitions between released Stores.
// Version 1 is the immutable bootstrap and therefore has no predecessor step.
var migrationSteps = map[int]migrationStep{
	1: {
		fromVersion: 1,
		toVersion:   2,
		name:        "0002_server_owned_review.sql",
		loadSQL:     Migration0002,
	},
}

// InspectKnownSchemaVersionReadOnly identifies a released FAC2 schema without
// mutating it. It rejects blank, foreign, future, and unregistered versions.
func InspectKnownSchemaVersionReadOnly(
	ctx context.Context,
	path string,
) (SchemaVersion, error) {
	canonicalPath, err := existingDatabasePath(path)
	if err != nil {
		return SchemaVersion{}, err
	}
	db, err := sql.Open("sqlite", sqliteURI(canonicalPath, "ro", []string{
		"query_only(1)",
		"trusted_schema(0)",
	}))
	if err != nil {
		return SchemaVersion{}, fmt.Errorf("currentstore: open schema inspector: %w", err)
	}
	defer db.Close()
	var applicationID, userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return SchemaVersion{}, fmt.Errorf("currentstore: inspect application_id: %w", err)
	}
	if applicationID != ApplicationID {
		return SchemaVersion{}, &IdentityError{
			Field: "application_id", Expected: strconv.Itoa(ApplicationID),
			Actual: strconv.Itoa(applicationID),
		}
	}
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return SchemaVersion{}, fmt.Errorf("currentstore: inspect user_version: %w", err)
	}
	version, known := KnownSchemaVersion(userVersion)
	if !known {
		return SchemaVersion{}, fmt.Errorf(
			"%w: %d",
			ErrUnknownSchemaVersion,
			userVersion,
		)
	}
	fingerprint, err := DatabaseSchemaFingerprint(ctx, db)
	if err != nil {
		return SchemaVersion{}, err
	}
	if fingerprint != version.SchemaFingerprint {
		return SchemaVersion{}, &IdentityError{
			Field: "runtime schema fingerprint", Expected: version.SchemaFingerprint,
			Actual: fingerprint,
		}
	}
	var (
		metaCount       int
		storeInstanceID string
		schemaIdentity  string
		schemaVersion   int
		metaFingerprint string
		generatorID     string
	)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM store_meta`).Scan(&metaCount); err != nil {
		return SchemaVersion{}, fmt.Errorf("currentstore: inspect store_meta: %w", err)
	}
	if metaCount != 1 {
		return SchemaVersion{}, &IdentityError{
			Field: "store_meta row count", Expected: "1", Actual: strconv.Itoa(metaCount),
		}
	}
	if err := db.QueryRowContext(ctx, `
		SELECT store_instance_id, schema_identity, schema_version,
		       schema_fingerprint, generator_id
		FROM store_meta
		WHERE singleton=1
	`).Scan(
		&storeInstanceID,
		&schemaIdentity,
		&schemaVersion,
		&metaFingerprint,
		&generatorID,
	); err != nil {
		return SchemaVersion{}, fmt.Errorf("currentstore: inspect Store identity: %w", err)
	}
	for field, pair := range map[string][2]string{
		"store_instance_id":  {"non-empty", storeInstanceID},
		"schema_identity":    {SchemaIdentity, schemaIdentity},
		"schema_version":     {strconv.Itoa(userVersion), strconv.Itoa(schemaVersion)},
		"schema_fingerprint": {version.SchemaFingerprint, metaFingerprint},
		"generator_id":       {GeneratorID, generatorID},
	} {
		if (field == "store_instance_id" && pair[1] == "") ||
			(field != "store_instance_id" && pair[0] != pair[1]) {
			return SchemaVersion{}, &IdentityError{
				Field: field, Expected: pair[0], Actual: pair[1],
			}
		}
	}
	return version, nil
}

// MigrateOfflineCurrentStore applies every registered forward migration while
// an OfflineLease is held. It never creates a backup itself; orchestration must
// complete and verify that mandatory backup before calling this function.
func MigrateOfflineCurrentStore(
	ctx context.Context,
	lease *OfflineLease,
) (MigrationResult, error) {
	path, err := requireOfflineLease(lease)
	if err != nil {
		return MigrationResult{}, err
	}
	from, err := InspectKnownSchemaVersionReadOnly(ctx, path)
	if err != nil {
		return MigrationResult{}, err
	}
	result := MigrationResult{
		FromVersion:     from.UserVersion,
		ToVersion:       from.UserVersion,
		AppliedVersions: make([]int, 0),
	}
	if from.UserVersion == UserVersion {
		verification, err := VerifyCurrentStoreReadOnly(ctx, path)
		if err != nil {
			return MigrationResult{}, err
		}
		result.Verification = verification
		return result, nil
	}
	if from.UserVersion > UserVersion {
		return MigrationResult{}, fmt.Errorf(
			"%w: %d is newer than supported %d",
			ErrUnknownSchemaVersion,
			from.UserVersion,
			UserVersion,
		)
	}

	db, err := sql.Open("sqlite", sqliteURI(path, "rw", []string{
		"foreign_keys(1)",
		"trusted_schema(0)",
		"busy_timeout(0)",
		"synchronous(FULL)",
	}))
	if err != nil {
		return MigrationResult{}, fmt.Errorf("currentstore: open migration writer: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("currentstore: acquire migration writer: %w", err)
	}
	defer connection.Close()

	for result.ToVersion < UserVersion {
		step, ok := migrationSteps[result.ToVersion]
		if !ok || step.fromVersion != result.ToVersion ||
			step.toVersion != result.ToVersion+1 || step.loadSQL == nil {
			return MigrationResult{}, fmt.Errorf(
				"currentstore: no migration from schema version %d",
				result.ToVersion,
			)
		}
		if err := applyMigrationStep(ctx, connection, step); err != nil {
			return MigrationResult{}, err
		}
		result.ToVersion = step.toVersion
		result.AppliedVersions = append(result.AppliedVersions, step.toVersion)
	}
	verification, err := VerifyCurrentStoreReadOnly(ctx, path)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("currentstore: verify migrated Store: %w", err)
	}
	result.Verification = verification
	return result, nil
}

func applyMigrationStep(
	ctx context.Context,
	connection *sql.Conn,
	step migrationStep,
) error {
	script, err := step.loadSQL()
	if err != nil {
		return fmt.Errorf("currentstore: load migration %s: %w", step.name, err)
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("currentstore: begin migration %s: %w", step.name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if _, err := connection.ExecContext(ctx, string(script)); err != nil {
		return fmt.Errorf("currentstore: apply migration %s: %w", step.name, err)
	}
	expected, known := KnownSchemaVersion(step.toVersion)
	if !known {
		return fmt.Errorf("currentstore: migration %s targets an unknown schema", step.name)
	}
	actual, err := DatabaseSchemaFingerprint(ctx, connection)
	if err != nil {
		return err
	}
	if actual != expected.SchemaFingerprint {
		return &IdentityError{
			Field: "migrated schema fingerprint", Expected: expected.SchemaFingerprint,
			Actual: actual,
		}
	}
	if _, err := connection.ExecContext(ctx, `
		UPDATE store_meta
		SET schema_version=?, schema_fingerprint=?
		WHERE singleton=1
	`, step.toVersion, expected.SchemaFingerprint); err != nil {
		return fmt.Errorf("currentstore: update migration identity: %w", err)
	}
	if _, err := connection.ExecContext(
		ctx,
		fmt.Sprintf("PRAGMA user_version=%d", step.toVersion),
	); err != nil {
		return fmt.Errorf("currentstore: update user_version: %w", err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("currentstore: commit migration %s: %w", step.name, err)
	}
	committed = true
	return nil
}
