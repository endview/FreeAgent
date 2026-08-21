package currentstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrTargetExists = errors.New("currentstore: target already exists")
	ErrOwnerActive  = errors.New("currentstore: database already has an active owner")
)

// IdentityError reports a fail-closed Current Store identity mismatch.
type IdentityError struct {
	Field    string
	Expected string
	Actual   string
}

func (failure *IdentityError) Error() string {
	if failure == nil {
		return "currentstore: identity mismatch"
	}
	return fmt.Sprintf(
		"currentstore: %s mismatch (expected %s, got %s)",
		failure.Field,
		failure.Expected,
		failure.Actual,
	)
}

// Verification is a read-only identity result, not an authority grant.
type Verification struct {
	Path              string
	StoreInstanceID   string
	SchemaIdentity    string
	SchemaVersion     int
	SchemaFingerprint string
	GeneratorID       string
}

// Store is the sole writable FAC1 owner. It deliberately exposes no raw SQL
// handle; all production writes are added as narrow Current Store methods.
type Store struct {
	path  string
	db    *sql.DB
	owner *ownerLease

	closeMu sync.Mutex
	closed  bool
}

// Path returns the canonical physical database path.
func (store *Store) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Close releases the writer before releasing the process ownership fence. It
// is idempotent.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.closeMu.Lock()
	defer store.closeMu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	return errors.Join(store.db.Close(), store.owner.release())
}

// InitFreshCurrentStore creates FAC1 only at a caller-selected path that does
// not exist. It builds and verifies a DELETE-journal temporary database in the
// same directory, then publishes it atomically without replacement.
func InitFreshCurrentStore(
	ctx context.Context,
	path string,
) (Verification, error) {
	target, err := newTargetPath(path)
	if err != nil {
		return Verification{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return Verification{}, fmt.Errorf("%w: %s", ErrTargetExists, target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Verification{}, fmt.Errorf("currentstore: inspect target: %w", err)
	}

	parent := filepath.Dir(target)
	info, err := os.Stat(parent)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: inspect target directory: %w",
			err,
		)
	}
	if !info.IsDir() {
		return Verification{}, errors.New(
			"currentstore: target parent is not a directory",
		)
	}

	temporary, err := os.CreateTemp(parent, "."+filepath.Base(target)+".init-*")
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: create initialization file: %w",
			err,
		)
	}
	tempPath := temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = os.Remove(tempPath)
		return Verification{}, fmt.Errorf(
			"currentstore: secure initialization file: %w",
			err,
		)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(tempPath)
		return Verification{}, fmt.Errorf(
			"currentstore: close initialization file: %w",
			err,
		)
	}
	published := false
	defer func() {
		if !published {
			removeInitializationFiles(tempPath)
		}
	}()

	if err := initializeTemporaryStore(ctx, tempPath); err != nil {
		return Verification{}, err
	}
	temporaryVerification, err := VerifyCurrentStoreReadOnly(ctx, tempPath)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: verify initialized temporary Store: %w",
			err,
		)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, statErr := os.Lstat(tempPath + suffix); statErr == nil {
			return Verification{}, fmt.Errorf(
				"currentstore: initialization left SQLite sidecar %s",
				suffix,
			)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return Verification{}, fmt.Errorf(
				"currentstore: inspect initialization sidecar: %w",
				statErr,
			)
		}
	}
	if err := publishFileNoReplace(tempPath, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Verification{}, fmt.Errorf("%w: %s", ErrTargetExists, target)
		}
		return Verification{}, fmt.Errorf(
			"currentstore: publish initialized Store: %w",
			err,
		)
	}
	published = true

	finalVerification, err := VerifyCurrentStoreReadOnly(ctx, target)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: verify published Store: %w",
			err,
		)
	}
	if finalVerification.StoreInstanceID != temporaryVerification.StoreInstanceID {
		return Verification{}, errors.New(
			"currentstore: published Store identity changed",
		)
	}
	return finalVerification, nil
}

// PrepareClosedCurrentStoreForPublication turns a closed, verified FAC1 Store
// into one self-contained SQLite file for atomic publication. It takes the same
// physical owner fence as normal startup, checkpoints WAL, switches the staged
// file to DELETE journal mode, syncs it, and verifies it again. It never seeds,
// migrates, repairs, or starts Runtime work.
func PrepareClosedCurrentStoreForPublication(
	ctx context.Context,
	path string,
) (verification Verification, returnErr error) {
	if ctx == nil {
		return Verification{}, errors.New(
			"currentstore: publication context is nil",
		)
	}
	verification, err := VerifyCurrentStoreReadOnly(ctx, path)
	if err != nil {
		return Verification{}, err
	}
	owner, err := acquireOwner(verification.Path)
	if err != nil {
		return Verification{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, owner.release()) }()
	verification, err = VerifyCurrentStoreReadOnly(ctx, verification.Path)
	if err != nil {
		return Verification{}, err
	}
	if err := consolidateCurrentStoreForPublication(ctx, verification.Path); err != nil {
		return Verification{}, err
	}
	if err := rejectSQLiteSidecars(verification.Path); err != nil {
		return Verification{}, err
	}
	file, err := os.OpenFile(verification.Path, os.O_RDWR, 0)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: open publication file for sync: %w",
			err,
		)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: sync publication file: %w",
			err,
		)
	}
	finalVerification, err := VerifyCurrentStoreReadOnly(ctx, verification.Path)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: verify prepared publication file: %w",
			err,
		)
	}
	if err := rejectSQLiteSidecars(verification.Path); err != nil {
		return Verification{}, err
	}
	if finalVerification.StoreInstanceID != verification.StoreInstanceID {
		return Verification{}, errors.New(
			"currentstore: Store identity changed during publication preparation",
		)
	}
	return finalVerification, nil
}

func consolidateCurrentStoreForPublication(
	ctx context.Context,
	path string,
) (returnErr error) {
	db, err := sql.Open(
		"sqlite",
		sqliteURI(path, "rw", []string{
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
			"synchronous(FULL)",
		}),
	)
	if err != nil {
		return fmt.Errorf("currentstore: open publication database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("currentstore: acquire publication connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, connection.Close()) }()
	var busy, remaining, checkpointed int
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	).Scan(&busy, &remaining, &checkpointed); err != nil {
		return fmt.Errorf("currentstore: checkpoint publication database: %w", err)
	}
	if busy != 0 || remaining != 0 {
		return fmt.Errorf(
			"currentstore: publication checkpoint incomplete (busy=%d remaining=%d checkpointed=%d)",
			busy,
			remaining,
			checkpointed,
		)
	}
	var journalMode string
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA journal_mode=DELETE`,
	).Scan(&journalMode); err != nil {
		return fmt.Errorf("currentstore: set publication journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "delete") {
		return fmt.Errorf(
			"currentstore: publication journal is %q, want DELETE",
			journalMode,
		)
	}
	return nil
}

func rejectSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf(
				"currentstore: publication file retained SQLite sidecar %s",
				suffix,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(
				"currentstore: inspect publication sidecar: %w",
				err,
			)
		}
	}
	return nil
}

func initializeTemporaryStore(ctx context.Context, path string) (resultErr error) {
	db, err := sql.Open("sqlite", sqliteURI(path, "rwc", nil))
	if err != nil {
		return fmt.Errorf("currentstore: open initialization database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() {
		resultErr = errors.Join(resultErr, db.Close())
	}()

	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("currentstore: acquire initialization connection: %w", err)
	}
	defer connection.Close()

	var journalMode string
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA journal_mode=DELETE`,
	).Scan(&journalMode); err != nil {
		return fmt.Errorf("currentstore: set initialization journal: %w", err)
	}
	if !strings.EqualFold(journalMode, "delete") {
		return fmt.Errorf(
			"currentstore: initialization journal is %q, want delete",
			journalMode,
		)
	}
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA trusted_schema=OFF",
		"PRAGMA synchronous=FULL",
	} {
		if _, err := connection.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("currentstore: apply %s: %w", pragma, err)
		}
	}
	if err := verifyBlankSQLite(ctx, connection); err != nil {
		return err
	}

	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("currentstore: begin initialization: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	migration, err := Migration0001()
	if err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, string(migration)); err != nil {
		return fmt.Errorf("currentstore: apply 0001_current.sql: %w", err)
	}
	fingerprint, err := DatabaseSchemaFingerprint(ctx, connection)
	if err != nil {
		return err
	}
	if fingerprint != ExpectedSchemaFingerprint {
		return &IdentityError{
			Field:    "schema fingerprint during initialization",
			Expected: ExpectedSchemaFingerprint,
			Actual:   fingerprint,
		}
	}
	instanceID, err := randomStoreInstanceID()
	if err != nil {
		return err
	}
	if _, err := connection.ExecContext(
		ctx,
		`INSERT INTO store_meta(
			singleton, store_instance_id, schema_identity, schema_version,
			schema_fingerprint, generator_id, created_at
		) VALUES(1, ?, ?, ?, ?, ?, ?)`,
		instanceID,
		SchemaIdentity,
		UserVersion,
		ExpectedSchemaFingerprint,
		GeneratorID,
		nowUnixMicro(),
	); err != nil {
		return fmt.Errorf("currentstore: write store_meta: %w", err)
	}
	for _, pragma := range []string{
		fmt.Sprintf("PRAGMA application_id=%d", ApplicationID),
		fmt.Sprintf("PRAGMA user_version=%d", UserVersion),
	} {
		if _, err := connection.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("currentstore: apply %s: %w", pragma, err)
		}
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("currentstore: commit initialization: %w", err)
	}
	committed = true
	return nil
}

func verifyBlankSQLite(
	ctx context.Context,
	connection *sql.Conn,
) error {
	var applicationID, userVersion, objectCount int
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA application_id`,
	).Scan(&applicationID); err != nil {
		return fmt.Errorf("currentstore: read blank application_id: %w", err)
	}
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA user_version`,
	).Scan(&userVersion); err != nil {
		return fmt.Errorf("currentstore: read blank user_version: %w", err)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_schema
		WHERE name NOT GLOB 'sqlite_*'
	`).Scan(&objectCount); err != nil {
		return fmt.Errorf("currentstore: inspect blank schema: %w", err)
	}
	if applicationID != 0 || userVersion != 0 || objectCount != 0 {
		return errors.New("currentstore: initialization file is not blank SQLite")
	}
	return nil
}

// VerifyCurrentStoreReadOnly verifies FAC1 without entering a writable startup
// path, migrating, seeding, repairing or changing journal mode.
func VerifyCurrentStoreReadOnly(
	ctx context.Context,
	path string,
) (Verification, error) {
	return verifyCurrentStoreReadOnly(ctx, path, false)
}

// verifyCurrentStoreReadOnly uses SQLite immutable mode only for callers that
// have already established a self-contained closed source with no sidecars.
// Normal verification must continue to observe an active WAL when present.
func verifyCurrentStoreReadOnly(
	ctx context.Context,
	path string,
	immutable bool,
) (result Verification, returnErr error) {
	defer func() {
		returnErr = classifySQLiteOwnerContention(path, returnErr)
	}()
	canonicalPath, err := existingDatabasePath(path)
	if err != nil {
		return Verification{}, err
	}
	databaseURI := sqliteURI(canonicalPath, "ro", []string{
		"query_only(1)",
		"foreign_keys(1)",
		"trusted_schema(0)",
	})
	if immutable {
		databaseURI = sqliteImmutableURI(canonicalPath, []string{
			"query_only(1)",
			"foreign_keys(1)",
			"trusted_schema(0)",
		})
	}
	db, err := sql.Open(
		"sqlite",
		databaseURI,
	)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: open read-only verifier: %w",
			err,
		)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: acquire verifier connection: %w",
			err,
		)
	}
	defer connection.Close()

	for pragma, expected := range map[string]int{
		"query_only":     1,
		"foreign_keys":   1,
		"trusted_schema": 0,
	} {
		var actual int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA "+pragma,
		).Scan(&actual); err != nil {
			return Verification{}, fmt.Errorf(
				"currentstore: read verifier PRAGMA %s: %w",
				pragma,
				err,
			)
		}
		if actual != expected {
			return Verification{}, &IdentityError{
				Field:    "verifier PRAGMA " + pragma,
				Expected: strconv.Itoa(expected),
				Actual:   strconv.Itoa(actual),
			}
		}
	}
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: begin coherent read-only verification: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	var applicationID, userVersion int
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA application_id`,
	).Scan(&applicationID); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: read application_id: %w",
			err,
		)
	}
	if applicationID != ApplicationID {
		return Verification{}, &IdentityError{
			Field:    "application_id",
			Expected: strconv.Itoa(ApplicationID),
			Actual:   strconv.Itoa(applicationID),
		}
	}
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA user_version`,
	).Scan(&userVersion); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: read user_version: %w",
			err,
		)
	}
	if userVersion != UserVersion {
		return Verification{}, &IdentityError{
			Field:    "user_version",
			Expected: strconv.Itoa(UserVersion),
			Actual:   strconv.Itoa(userVersion),
		}
	}

	var verification Verification
	verification.Path = canonicalPath
	var metaCount int
	if err := connection.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM store_meta`,
	).Scan(&metaCount); err != nil {
		return Verification{}, fmt.Errorf("currentstore: read store_meta: %w", err)
	}
	if metaCount != 1 {
		return Verification{}, &IdentityError{
			Field:    "store_meta row count",
			Expected: "1",
			Actual:   strconv.Itoa(metaCount),
		}
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT store_instance_id, schema_identity, schema_version,
		       schema_fingerprint, generator_id
		FROM store_meta
		WHERE singleton=1
	`).Scan(
		&verification.StoreInstanceID,
		&verification.SchemaIdentity,
		&verification.SchemaVersion,
		&verification.SchemaFingerprint,
		&verification.GeneratorID,
	); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: read store identity: %w",
			err,
		)
	}
	for field, pair := range map[string][2]string{
		"schema_identity": {
			SchemaIdentity,
			verification.SchemaIdentity,
		},
		"schema_version": {
			strconv.Itoa(UserVersion),
			strconv.Itoa(verification.SchemaVersion),
		},
		"schema_fingerprint": {
			ExpectedSchemaFingerprint,
			verification.SchemaFingerprint,
		},
		"generator_id": {
			GeneratorID,
			verification.GeneratorID,
		},
	} {
		if pair[0] != pair[1] {
			return Verification{}, &IdentityError{
				Field: field, Expected: pair[0], Actual: pair[1],
			}
		}
	}
	if strings.TrimSpace(verification.StoreInstanceID) == "" {
		return Verification{}, &IdentityError{
			Field: "store_instance_id", Expected: "non-empty", Actual: "empty",
		}
	}
	runtimeFingerprint, err := DatabaseSchemaFingerprint(ctx, connection)
	if err != nil {
		return Verification{}, err
	}
	if runtimeFingerprint != ExpectedSchemaFingerprint {
		return Verification{}, &IdentityError{
			Field:    "runtime schema fingerprint",
			Expected: ExpectedSchemaFingerprint,
			Actual:   runtimeFingerprint,
		}
	}

	var integrity string
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA integrity_check`,
	).Scan(&integrity); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: run integrity_check: %w",
			err,
		)
	}
	if integrity != "ok" {
		return Verification{}, &IdentityError{
			Field: "integrity_check", Expected: "ok", Actual: integrity,
		}
	}
	rows, err := connection.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: run foreign_key_check: %w",
			err,
		)
	}
	if rows.Next() {
		_ = rows.Close()
		return Verification{}, errors.New(
			"currentstore: foreign_key_check reported a violation",
		)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Verification{}, fmt.Errorf(
			"currentstore: read foreign_key_check: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: close foreign_key_check: %w",
			err,
		)
	}
	if err := VerifyModuleArtifactIngressSemanticClosureV1(ctx, connection); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: verify module Artifact ingress semantic closure: %w",
			err,
		)
	}
	if err := VerifyOverviewObservationSemanticClosureV1(ctx, connection); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: verify Overview observation semantic closure: %w",
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return Verification{}, fmt.Errorf(
			"currentstore: commit coherent read-only verification: %w",
			err,
		)
	}
	committed = true
	return verification, nil
}

// OpenExistingCurrentStore opens only an already verified FAC1 Store, takes
// the single physical writer fence, verifies again after fencing, and then
// enables the writer journal. It never initializes, migrates, seeds or repairs.
func OpenExistingCurrentStore(
	ctx context.Context,
	path string,
) (result *Store, returnErr error) {
	defer func() {
		returnErr = classifySQLiteOwnerContention(path, returnErr)
	}()
	verification, err := VerifyCurrentStoreReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	owner, err := acquireOwner(verification.Path)
	if err != nil {
		return nil, err
	}
	releaseOnFailure := true
	defer func() {
		if releaseOnFailure {
			_ = owner.release()
		}
	}()
	verification, err = VerifyCurrentStoreReadOnly(ctx, verification.Path)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(
		"sqlite",
		sqliteURI(verification.Path, "rw", []string{
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(5000)",
			"synchronous(FULL)",
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("currentstore: open writer: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	connection, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(
			"currentstore: acquire writer connection: %w",
			err,
		)
	}
	var journalMode string
	if err := connection.QueryRowContext(
		ctx,
		`PRAGMA journal_mode=WAL`,
	).Scan(&journalMode); err != nil {
		_ = connection.Close()
		_ = db.Close()
		return nil, fmt.Errorf("currentstore: enable WAL: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		_ = connection.Close()
		_ = db.Close()
		return nil, fmt.Errorf(
			"currentstore: writer journal is %q, want wal",
			journalMode,
		)
	}
	for pragma, expected := range map[string]int{
		"foreign_keys":   1,
		"trusted_schema": 0,
		"synchronous":    2,
	} {
		var actual int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA "+pragma,
		).Scan(&actual); err != nil || actual != expected {
			_ = connection.Close()
			_ = db.Close()
			if err != nil {
				return nil, fmt.Errorf(
					"currentstore: read writer PRAGMA %s: %w",
					pragma,
					err,
				)
			}
			return nil, &IdentityError{
				Field:    "writer PRAGMA " + pragma,
				Expected: strconv.Itoa(expected),
				Actual:   strconv.Itoa(actual),
			}
		}
	}
	if err := connection.Close(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(
			"currentstore: release writer connection: %w",
			err,
		)
	}
	store := &Store{path: verification.Path, db: db, owner: owner}
	releaseOnFailure = false
	return store, nil
}

func nowUnixMicro() int64 {
	return time.Now().UTC().UnixMicro()
}

func timeFromUnixMicro(value int64) (time.Time, error) {
	if value <= 0 {
		return time.Time{}, fmt.Errorf(
			"currentstore: invalid UTC Unix microsecond timestamp %d",
			value,
		)
	}
	return time.UnixMicro(value).UTC(), nil
}

func sqliteURI(path, mode string, pragmas []string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		// A drive-letter path needs an empty URI authority; otherwise the
		// drive letter is parsed as the authority instead of the path.
		uriPath = "/" + uriPath
	}
	uri := &url.URL{Scheme: "file", Path: uriPath}
	query := uri.Query()
	query.Set("mode", mode)
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func sqliteImmutableURI(path string, pragmas []string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := &url.URL{Scheme: "file", Path: uriPath}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func newTargetPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("currentstore: database path is required")
	}
	if strings.ContainsAny(path, "?#") {
		return "", errors.New(
			"currentstore: database path must be a filesystem path, not a URI",
		)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("currentstore: resolve database path: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func existingDatabasePath(path string) (string, error) {
	absolute, err := newTargetPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("currentstore: inspect database path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("currentstore: database path must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("currentstore: database path is not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf(
			"currentstore: resolve database path links: %w",
			err,
		)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf(
			"currentstore: resolve physical database path: %w",
			err,
		)
	}
	if ownerPathKey(filepath.Clean(resolved)) != ownerPathKey(absolute) {
		return "", errors.New(
			"currentstore: database path traverses a symlink or reparse point",
		)
	}
	if err := validateDatabaseLinkSafety(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func randomStoreInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf(
			"currentstore: generate store instance ID: %w",
			err,
		)
	}
	return hex.EncodeToString(value[:]), nil
}

func removeInitializationFiles(path string) {
	for _, candidate := range []string{
		path,
		path + "-wal",
		path + "-shm",
		path + "-journal",
	} {
		_ = os.Remove(candidate)
	}
}
