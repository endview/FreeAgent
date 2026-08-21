package controlcursor

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
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	allowedStandard := map[string]struct{}{
		"bytes":           {},
		"crypto/hmac":     {},
		"crypto/rand":     {},
		"crypto/sha256":   {},
		"encoding/base64": {},
		"encoding/binary": {},
		"encoding/json":   {},
		"errors":          {},
		"io":              {},
		"strings":         {},
		"sync":            {},
		"unicode":         {},
		"unicode/utf8":    {},
	}
	allowedProject := map[string]struct{}{
		"github.com/endview/freeagent/internal/controlapp": {},
		"github.com/endview/freeagent/sdk/moduleapi":       {},
	}
	const projectPrefix = "github.com/endview/freeagent/"
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(
			token.NewFileSet(),
			filepath.Join(directory, name),
			nil,
			parser.ImportsOnly,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(path, projectPrefix) {
				if _, allowed := allowedProject[path]; !allowed {
					t.Errorf(
						"production file %s imports unapproved project package %s",
						name,
						path,
					)
				}
				continue
			}
			if _, allowed := allowedStandard[path]; !allowed {
				t.Errorf(
					"production file %s imports unapproved standard package %s",
					name,
					path,
				)
			}
		}
	}
}
