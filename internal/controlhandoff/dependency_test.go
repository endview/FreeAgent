package controlhandoff

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionDependencyBoundaryV1(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	allowedProject := map[string]bool{
		"github.com/endview/freeagent/internal/controlsession": true,
		"github.com/endview/freeagent/sdk/moduleapi":           true,
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(
			token.NewFileSet(),
			filepath.Clean(name),
			nil,
			parser.ImportsOnly,
		)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(path, "github.com/endview/freeagent/") &&
				!allowedProject[path] {
				t.Fatalf("production file %s imports forbidden project package %s", name, path)
			}
			for _, forbidden := range []string{
				"database/sql",
				"net/http",
				"modernc.org/sqlite",
			} {
				if path == forbidden {
					t.Fatalf("production file %s imports forbidden package %s", name, path)
				}
			}
		}
	}
}
