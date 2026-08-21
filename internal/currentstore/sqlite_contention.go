package currentstore

import (
	"errors"
	"fmt"

	sqlite3 "modernc.org/sqlite/lib"
)

type sqliteCodeError interface {
	error
	Code() int
}

// classifySQLiteOwnerContention gives every Current Store open/verification
// path one stable owner-busy classification. SQLite extended result codes keep
// their primary result in the low byte, so BUSY_RECOVERY (261) and every other
// BUSY/LOCKED extension are covered without string matching. Corruption,
// permissions, missing files, cancellation and identity failures are returned
// unchanged.
func classifySQLiteOwnerContention(path string, err error) error {
	if err == nil || errors.Is(err, ErrOwnerActive) {
		return err
	}
	var coded sqliteCodeError
	if !errors.As(err, &coded) {
		return err
	}
	primary := coded.Code() & 0xff
	if primary != sqlite3.SQLITE_BUSY && primary != sqlite3.SQLITE_LOCKED {
		return err
	}
	return fmt.Errorf("%w: %s: %w", ErrOwnerActive, path, err)
}
