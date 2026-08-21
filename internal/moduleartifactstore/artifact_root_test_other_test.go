//go:build !windows

package moduleartifactstore

import "testing"

func privateArtifactTempDirV1(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
