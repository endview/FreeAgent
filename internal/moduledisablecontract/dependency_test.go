package moduledisablecontract

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestProductionDependencyBoundaryV1(t *testing.T) {
	allowedDirect := map[string]struct{}{
		"bytes":         {},
		"encoding/json": {},
		"errors":        {},
		"io":            {},
		"math":          {},
		"strings":       {},
		"unicode":       {},
		"unicode/utf8":  {},
		"github.com/endview/freeagent/internal/controlapicontract": {},
		"github.com/endview/freeagent/internal/moduleapplyplan":    {},
		"github.com/endview/freeagent/sdk/moduleapi":               {},
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if _, allowed := allowedDirect[path]; !allowed {
				t.Fatalf("production file %s imports unapproved package %s", name, path)
			}
		}
	}
}
