package moduleconformance

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDependencyClosureHasNoRuntimeStoreHostOrNetworkClient(t *testing.T) {
	t.Parallel()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	command := exec.Command(goBinary, "list", "-deps", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	const projectPrefix = "github.com/endview/freeagent/"
	allowed := map[string]struct{}{
		"github.com/endview/freeagent/internal/moduleconformance": {},
		"github.com/endview/freeagent/internal/safefiletree":      {},
		"github.com/endview/freeagent/sdk/moduleapi":              {},
	}
	forbidden := map[string]struct{}{
		"database/sql":        {},
		"database/sql/driver": {},
		"net/http":            {},
		"net/rpc":             {},
		"net/rpc/jsonrpc":     {},
		"os/exec":             {},
		"plugin":              {},
	}
	for _, dependency := range strings.Fields(string(output)) {
		if _, blocked := forbidden[dependency]; blocked {
			t.Fatalf("offline verifier imports forbidden dependency %q", dependency)
		}
		if !strings.HasPrefix(dependency, projectPrefix) {
			continue
		}
		if _, ok := allowed[dependency]; !ok {
			t.Fatalf("offline verifier imports unexpected project package %q", dependency)
		}
	}

	moduleCommand := exec.Command(
		goBinary,
		"list",
		"-deps",
		"-f",
		"{{if .Module}}{{.ImportPath}}|{{.Module.Path}}{{end}}",
		".",
	)
	moduleOutput, err := moduleCommand.Output()
	if err != nil {
		t.Fatalf("go list external dependency modules: %v", err)
	}
	allowedModules := map[string]struct{}{
		"github.com/endview/freeagent": {},
		"golang.org/x/sys":             {},
		"golang.org/x/text":            {},
	}
	for _, line := range strings.Split(string(moduleOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			t.Fatalf("unexpected module dependency line %q", line)
		}
		if _, ok := allowedModules[parts[1]]; !ok {
			t.Fatalf("offline verifier imports unapproved module %q through %q", parts[1], parts[0])
		}
	}

	for _, packagePath := range []string{
		".",
		"github.com/endview/freeagent/sdk/moduleapi",
	} {
		direct := exec.Command(
			goBinary,
			"list",
			"-f",
			"{{join .Imports \"\\n\"}}",
			packagePath,
		)
		directOutput, err := direct.Output()
		if err != nil {
			t.Fatalf("go list direct imports for %s: %v", packagePath, err)
		}
		// net/netip and net/url are syntax-only parsers used by the public
		// REMOTE binding wire validator. They do not perform DNS resolution,
		// open sockets, or provide an HTTP/RPC client. Keep every other net
		// import forbidden so the offline verifier cannot acquire a network
		// execution dependency through moduleapi.
		allowedNetworkParsers := map[string]struct{}{
			"net/netip": {},
			"net/url":   {},
		}
		for _, imported := range strings.Fields(string(directOutput)) {
			_, allowedNetworkParser := allowedNetworkParsers[imported]
			if !allowedNetworkParser &&
				(imported == "net" || strings.HasPrefix(imported, "net/")) ||
				imported == "os/exec" || imported == "plugin" ||
				strings.HasPrefix(imported, "database/sql") {
				t.Fatalf("offline verifier package %s directly imports %q", packagePath, imported)
			}
		}
	}
}
