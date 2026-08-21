package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareClosedCurrentStoreForPublication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	initialized, err := InitFreshCurrentStore(ctx, path)
	if err != nil {
		t.Fatalf("InitFreshCurrentStore: %v", err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore: %v", err)
	}
	body := []byte(`{"publication":true}`)
	digest, err := ComputeContentDigest(ContentConfig, "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutContent(ctx, ContentInput{
		Digest:         digest,
		Kind:           ContentConfig,
		MediaType:      "application/json",
		CanonicalBytes: body,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("PutContent: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	prepared, err := PrepareClosedCurrentStoreForPublication(ctx, path)
	if err != nil {
		t.Fatalf("PrepareClosedCurrentStoreForPublication: %v", err)
	}
	if prepared.StoreInstanceID != initialized.StoreInstanceID {
		t.Fatalf(
			"Store instance changed: got %q want %q",
			prepared.StoreInstanceID,
			initialized.StoreInstanceID,
		)
	}
	if err := rejectSQLiteSidecars(path); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", sqliteURI(path, "ro", nil))
	if err != nil {
		t.Fatal(err)
	}
	var journalMode string
	queryErr := database.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "delete") {
		t.Fatalf("journal_mode=%q, want delete", journalMode)
	}

	reopened, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatalf("reopen prepared Store: %v", err)
	}
	record, getErr := reopened.GetContent(ctx, digest)
	closeErr = reopened.Close()
	if err := errors.Join(getErr, closeErr); err != nil {
		t.Fatalf("read prepared Store: %v", err)
	}
	if string(record.CanonicalBytes) != string(body) {
		t.Fatalf("content=%s, want %s", record.CanonicalBytes, body)
	}
}

func TestPrepareClosedCurrentStoreForPublicationRejectsActiveOwner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := PrepareClosedCurrentStoreForPublication(ctx, path); !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("active owner error=%v, want ErrOwnerActive", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active-owner rejection changed Store: %v", err)
	}
}
