package currentstore

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

type sqliteContentionTestError struct {
	code int
}

func (failure *sqliteContentionTestError) Error() string { return "sqlite test error" }
func (failure *sqliteContentionTestError) Code() int     { return failure.code }

func TestClassifySQLiteOwnerContentionUsesPrimaryResultCode(t *testing.T) {
	t.Parallel()
	for _, code := range []int{
		sqlite3.SQLITE_BUSY,
		sqlite3.SQLITE_BUSY_RECOVERY,
		sqlite3.SQLITE_BUSY_SNAPSHOT,
		sqlite3.SQLITE_LOCKED,
		sqlite3.SQLITE_LOCKED_SHAREDCACHE,
	} {
		code := code
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Parallel()
			cause := &sqliteContentionTestError{code: code}
			wrapped := errors.Join(errors.New("verification boundary"), cause)
			windowsUnsafePath := string([]byte{'D', ':', '\\'}) + `store\current.sqlite`
			got := classifySQLiteOwnerContention(windowsUnsafePath, wrapped)
			if !errors.Is(got, ErrOwnerActive) || !errors.Is(got, cause) {
				t.Fatalf("code=%d classification=%v", code, got)
			}
		})
	}
}

func TestClassifySQLiteOwnerContentionDoesNotMaskOtherFailures(t *testing.T) {
	t.Parallel()
	for _, code := range []int{
		sqlite3.SQLITE_PERM,
		sqlite3.SQLITE_CORRUPT,
		sqlite3.SQLITE_IOERR,
		sqlite3.SQLITE_CONSTRAINT,
		sqlite3.SQLITE_NOTADB,
	} {
		cause := &sqliteContentionTestError{code: code}
		if got := classifySQLiteOwnerContention("current.sqlite", cause); got != cause ||
			errors.Is(got, ErrOwnerActive) {
			t.Fatalf("code=%d was misclassified as owner contention: %v", code, got)
		}
	}
	for _, cause := range []error{
		os.ErrNotExist,
		os.ErrPermission,
		context.Canceled,
		&IdentityError{Field: "application_id", Expected: "1", Actual: "2"},
	} {
		if got := classifySQLiteOwnerContention("current.sqlite", cause); got != cause ||
			errors.Is(got, ErrOwnerActive) {
			t.Fatalf("non-SQLite failure was misclassified: %v", got)
		}
	}
}

func TestPublishedBasisBoundaryClassifiesSQLiteContention(t *testing.T) {
	store := &Store{path: "current.sqlite"}
	for _, code := range []int{
		sqlite3.SQLITE_BUSY,
		sqlite3.SQLITE_BUSY_RECOVERY,
		sqlite3.SQLITE_LOCKED,
	} {
		cause := &sqliteContentionTestError{code: code}
		got := store.classifyPublishedBasisContentionV1(cause)
		if !errors.Is(got, ErrOwnerActive) || !errors.Is(got, cause) {
			t.Fatalf("code=%d PublishedBasis classification=%v", code, got)
		}
	}
	cause := &sqliteContentionTestError{code: sqlite3.SQLITE_CORRUPT}
	if got := store.classifyPublishedBasisContentionV1(cause); got != cause {
		t.Fatalf("non-contention PublishedBasis failure changed: %v", got)
	}
}
