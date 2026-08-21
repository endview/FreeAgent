//go:build !windows

package moduleartifactingress

import "testing"

func privateIngressArtifactTempDirV1(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
