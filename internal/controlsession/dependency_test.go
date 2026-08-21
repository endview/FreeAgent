package controlsession

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionDirectDependenciesStayNarrow(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(
		goBinary,
		"list",
		"-f",
		`{{join .Imports "\n"}}`,
		".",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list direct imports: %v", err)
	}
	forbidden := map[string]struct{}{
		"database/sql":  {},
		"net":           {},
		"net/http":      {},
		"os":            {},
		"path/filepath": {},
		"github.com/endview/freeagent/internal/currentstore": {},
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/controlapicontract": {},
		"github.com/endview/freeagent/sdk/moduleapi":               {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if _, denied := forbidden[dependency]; denied {
			t.Fatalf("controlsession imports forbidden dependency %q", dependency)
		}
		if strings.HasPrefix(dependency, projectPrefix) {
			if _, allowed := allowedProject[dependency]; !allowed {
				t.Fatalf("controlsession imports unapproved project dependency %q", dependency)
			}
		}
	}
}
