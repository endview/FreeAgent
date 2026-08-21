package controlhttp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionDirectImportAllowlistV1(t *testing.T) {
	t.Parallel()
	allowed := map[string]struct{}{
		"bytes":           {},
		"context":         {},
		"crypto/rand":     {},
		"crypto/sha256":   {},
		"encoding/base64": {},
		"encoding/json":   {},
		"errors":          {},
		"fmt":             {},
		"io":              {},
		"net":             {},
		"net/http":        {},
		"reflect":         {},
		"strconv":         {},
		"strings":         {},
		"sync":            {},
		"sync/atomic":     {},
		"time":            {},
		"unicode/utf8":    {},
		"github.com/endview/freeagent/internal/controlapicontract": {},
		"github.com/endview/freeagent/internal/controlapipolicy":   {},
		"github.com/endview/freeagent/internal/controlapp":         {},
		"github.com/endview/freeagent/internal/controloverview":    {},
		"github.com/endview/freeagent/internal/controlsession":     {},
		"github.com/endview/freeagent/sdk/moduleapi":               {},
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(
			token.NewFileSet(),
			filepath.Clean(entry.Name()),
			nil,
			parser.ImportsOnly,
		)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, declaration := range parsed.Imports {
			path, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", declaration.Path.Value, err)
			}
			if _, ok := allowed[path]; !ok {
				t.Errorf("production import is outside exact allowlist: %s", path)
			}
			seen[path] = struct{}{}
		}
	}
	for _, forbidden := range []string{
		"database/sql",
		"os",
		"path/filepath",
		"github.com/endview/freeagent/internal/currentstore",
		"github.com/endview/freeagent/cmd/freeagent",
	} {
		if _, ok := seen[forbidden]; ok {
			t.Errorf("forbidden production import present: %s", forbidden)
		}
	}
}
