package currentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestInitVerifyOpenAndCloseCurrentStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	verification, err := InitFreshCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Path != path ||
		verification.SchemaFingerprint != ExpectedSchemaFingerprint ||
		verification.SchemaIdentity != SchemaIdentity ||
		verification.StoreInstanceID == "" {
		t.Fatalf("verification = %+v", verification)
	}
	store, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Path() != path {
		t.Fatalf("Store path = %q, want %q", store.Path(), path)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitNeverOverwritesAndConcurrentInitPublishesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	var wait sync.WaitGroup
	wait.Add(2)
	errorsSeen := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			defer wait.Done()
			_, err := InitFreshCurrentStore(context.Background(), path)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	successes, existsFailures := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrTargetExists):
			existsFailures++
		default:
			t.Fatalf("unexpected concurrent init error: %v", err)
		}
	}
	if successes != 1 || existsFailures != 1 {
		t.Fatalf(
			"concurrent init successes=%d exists failures=%d",
			successes,
			existsFailures,
		)
	}
	if _, err := VerifyCurrentStoreReadOnly(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("existing Store init error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected init modified existing Store")
	}
}

func TestOpenExistingNeverInitializesMissingEmptyOrForeignStore(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.db")
	if _, err := OpenExistingCurrentStore(
		context.Background(),
		missing,
	); err == nil {
		t.Fatal("OpenExisting initialized a missing path")
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path was created: %v", err)
	}

	empty := filepath.Join(directory, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingCurrentStore(
		context.Background(),
		empty,
	); err == nil {
		t.Fatal("OpenExisting accepted an empty file")
	}
	info, err := os.Stat(empty)
	if err != nil || info.Size() != 0 {
		t.Fatalf("empty file was mutated: info=%v err=%v", info, err)
	}

	foreign := filepath.Join(directory, "foreign.db")
	db, err := sql.Open("sqlite", sqliteURI(foreign, "rwc", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		PRAGMA application_id=1179796805;
		PRAGMA user_version=11;
		CREATE TABLE legacy_free(id TEXT PRIMARY KEY);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingCurrentStore(
		context.Background(),
		foreign,
	); err == nil {
		t.Fatal("OpenExisting accepted a foreign FREE Store")
	}
	after, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("foreign Store was mutated during rejection")
	}
}

func TestVerifyReadOnlyDoesNotChangeCleanDatabaseOrCreateSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly.db")
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := VerifyCurrentStoreReadOnly(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterDigest := sha256.Sum256(after)
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if beforeDigest != afterDigest ||
		beforeInfo.Size() != afterInfo.Size() ||
		!beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("read-only verification changed the database file")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only verification created %s: %v", suffix, err)
		}
	}
}

func TestVerifyRejectsRuntimeSchemaDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteURI(path, "rw", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX drift_index ON runs(tenant_id)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCurrentStoreReadOnly(
		context.Background(),
		path,
	); err == nil {
		t.Fatal("schema drift was accepted")
	}
}

func TestOnlyOneWritableOwnerCanOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.db")
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	first, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := OpenExistingCurrentStore(
		context.Background(),
		path,
	); !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("second owner error = %v", err)
	}
}

func TestWritableOwnerFenceIsCrossProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cross-process-owner.db")
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	owner, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestCurrentStoreOwnerHelperProcess$",
	)
	command.Env = append(
		os.Environ(),
		"FREEAGENT_CURRENTSTORE_OWNER_HELPER="+path,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("cross-process owner helper: %v\n%s", err, output)
	}
}

func TestCurrentStoreOwnerHelperProcess(t *testing.T) {
	path := os.Getenv("FREEAGENT_CURRENTSTORE_OWNER_HELPER")
	if path == "" {
		return
	}
	store, err := OpenExistingCurrentStore(context.Background(), path)
	if store != nil {
		_ = store.Close()
	}
	if !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("cross-process competing owner error = %v", err)
	}
}

func TestDatabaseHardLinkAliasIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hardlink.db")
	if _, err := InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatal(err)
	}
	alias := path + ".alias"
	if err := os.Link(path, alias); err != nil {
		t.Skipf("filesystem does not support hardlink test: %v", err)
	}
	if _, err := VerifyCurrentStoreReadOnly(
		context.Background(),
		path,
	); err == nil {
		t.Fatal("database with a hard-link alias was accepted")
	}
}
