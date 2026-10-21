package harness

import (
	"testing"

	"github.com/endview/freeagent/internal/winlaunch"
)

func TestCheckSandboxReportsTheHost(t *testing.T) {
	status := CheckSandbox(t.Context())
	want := "unsupported"
	if winlaunch.SandboxSupported() {
		want = "ready"
	}
	if status.Status != want {
		t.Fatalf("sandbox check = %+v, want %s", status, want)
	}
}
