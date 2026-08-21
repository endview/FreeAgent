package moduledisabledryrun

import (
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestProductionImportsStayReadOnlyAndNeutralV1(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	allowedDirect := map[string]struct{}{
		"bytes": {}, "context": {}, "errors": {}, "fmt": {},
		"math": {}, "reflect": {}, "strings": {},
		"github.com/endview/freeagent/internal/controlcontract": {},
		"github.com/endview/freeagent/internal/moduleapplyplan": {},
		"github.com/endview/freeagent/sdk/moduleapi":            {},
	}
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) < 3 ||
			entry.Name()[len(entry.Name())-3:] != ".go" ||
			len(entry.Name()) >= 8 && entry.Name()[len(entry.Name())-8:] == "_test.go" {
			continue
		}
		parsed, parseErr := parser.ParseFile(
			token.NewFileSet(),
			entry.Name(),
			nil,
			parser.ImportsOnly,
		)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", entry.Name(), parseErr)
		}
		for _, imported := range parsed.Imports {
			path := imported.Path.Value[1 : len(imported.Path.Value)-1]
			if _, allowed := allowedDirect[path]; !allowed {
				t.Fatalf("production file %s directly imports unapproved package %q", entry.Name(), path)
			}
		}
	}
}
