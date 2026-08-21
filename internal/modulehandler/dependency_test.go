package modulehandler

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDependencyClosureStaysPure(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goBinary, "list", "-deps", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	forbidden := map[string]struct{}{
		"database/sql": {}, "net/http": {}, "os/exec": {}, "plugin": {},
		"github.com/endview/freeagent/internal/currentstore":     {},
		"github.com/endview/freeagent/internal/modulehost":       {},
		"github.com/endview/freeagent/internal/moduleupgrade":    {},
		"github.com/endview/freeagent/internal/mcpstdio":         {},
		"github.com/endview/freeagent/internal/remoteactionhttp": {},
		"github.com/endview/freeagent/internal/wasmaction":       {},
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/modulehandler": {},
		"github.com/endview/freeagent/internal/safefiletree":  {},
		"github.com/endview/freeagent/sdk/moduleapi":          {},
	}
	seenProject := make(map[string]struct{}, len(allowedProject))
	for _, dependency := range strings.Fields(string(output)) {
		if _, found := forbidden[dependency]; found {
			t.Fatalf("pure handler policy depends on forbidden package %q", dependency)
		}
		if !strings.HasPrefix(dependency, projectPrefix) {
			continue
		}
		if _, allowed := allowedProject[dependency]; !allowed {
			t.Fatalf("pure handler policy depends on unapproved project package %q", dependency)
		}
		seenProject[dependency] = struct{}{}
	}
	for dependency := range allowedProject {
		if _, present := seenProject[dependency]; !present {
			t.Fatalf("pure handler policy is missing required project dependency %q", dependency)
		}
	}
}
