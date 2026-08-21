package moduleapplyplan_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestProductionDirectDependenciesStayExact(t *testing.T) {
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
	got := strings.Fields(string(output))
	sort.Strings(got)
	want := []string{
		"bytes",
		"encoding/json",
		"errors",
		"fmt",
		"github.com/endview/freeagent/sdk/moduleapi",
		"io",
		"math",
		"strings",
		"unicode/utf8",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("direct production dependencies differ\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDependencyClosureStaysPureAndExact(t *testing.T) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goBinary, "list", "-deps", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/moduleapplyplan": {},
		"github.com/endview/freeagent/internal/safefiletree":    {},
		"github.com/endview/freeagent/sdk/moduleapi":            {},
	}
	seenProject := make(map[string]struct{}, len(allowedProject))
	for _, dependency := range strings.Fields(string(output)) {
		if !strings.HasPrefix(dependency, projectPrefix) {
			continue
		}
		if _, allowed := allowedProject[dependency]; !allowed {
			t.Fatalf("pure plan contract depends on unapproved project package %q", dependency)
		}
		seenProject[dependency] = struct{}{}
	}
	for dependency := range allowedProject {
		if _, present := seenProject[dependency]; !present {
			t.Fatalf("pure plan contract is missing required project dependency %q", dependency)
		}
	}
}
