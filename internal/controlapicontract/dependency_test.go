package controlapicontract_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionContractDirectDependenciesStayPure(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(
		goBinary,
		"list",
		"-f",
		"{{join .Imports \"\\n\"}}",
		".",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list direct imports: %v", err)
	}
	const projectPrefix = "github.com/endview/freeagent/"
	const allowedProject = "github.com/endview/freeagent/sdk/moduleapi"
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "net/http" || dependency == "database/sql" ||
			dependency == "crypto/rand" || dependency == "time" ||
			dependency == "os" || dependency == "path/filepath" ||
			dependency == "github.com/endview/freeagent/internal/currentstore" {
			t.Fatalf("pure Control API contract directly imports %q", dependency)
		}
		if strings.HasPrefix(dependency, projectPrefix) &&
			dependency != allowedProject {
			t.Fatalf("pure Control API contract imports unapproved project package %q", dependency)
		}
	}
}
