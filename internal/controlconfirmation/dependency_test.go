package controlconfirmation

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionDirectDependenciesStayNarrowV1(t *testing.T) {
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
	const projectPrefix = "github.com/endview/freeagent/"
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/controlapicontract": {},
		"github.com/endview/freeagent/internal/controlsession":     {},
		"github.com/endview/freeagent/sdk/moduleapi":               {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if strings.HasPrefix(dependency, projectPrefix) {
			if _, allowed := allowedProject[dependency]; !allowed {
				t.Fatalf("unapproved project dependency %q", dependency)
			}
		}
		for _, forbidden := range []string{
			"database/sql", "net", "net/http", "os", "path/filepath",
		} {
			if dependency == forbidden {
				t.Fatalf("forbidden production dependency %q", dependency)
			}
		}
	}
}
