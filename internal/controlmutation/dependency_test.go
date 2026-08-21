package controlmutation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionDependencyBoundaryV1(t *testing.T) {
	t.Parallel()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate dependency test")
	}
	directory := filepath.Dir(current)
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/controlapicontract":    {},
		"github.com/endview/freeagent/internal/controlconfirmation":   {},
		"github.com/endview/freeagent/internal/controlsession":        {},
		"github.com/endview/freeagent/internal/currentstore":          {},
		"github.com/endview/freeagent/internal/moduleapplyplan":       {},
		"github.com/endview/freeagent/internal/moduledisablecontract": {},
		"github.com/endview/freeagent/sdk/moduleapi":                  {},
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(
			token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "database/sql" || path == "net" || path == "net/http" ||
				path == "os" || path == "os/exec" || path == "path/filepath" {
				t.Errorf("production file %s imports forbidden dependency %s", name, path)
			}
			if strings.HasPrefix(path, "github.com/endview/freeagent/") {
				if _, allowed := allowedProject[path]; !allowed {
					t.Errorf("production file %s imports unapproved project package %s", name, path)
				}
			}
		}
	}
}
