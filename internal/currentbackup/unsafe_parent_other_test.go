//go:build !windows

package currentbackup

import (
	"os"
	"testing"
)

func unsafeBackupParentV1(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	return path
}
