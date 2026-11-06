package chatui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

// Check actual send sites as well as the sample contract. A new string literal
// must not silently bypass EventKinds and the frontend's explicit policy.
func TestFrontendEventSendSiteCoverage(t *testing.T) {
	set := token.NewFileSet()
	constants, err := parser.ParseFile(set, "../harness/event_kinds.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, kind := range harness.EventKinds() {
		if registered[kind] {
			t.Fatalf("duplicate event kind %q", kind)
		}
		registered[kind] = true
	}
	known := map[string]bool{}
	ast.Inspect(constants, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Values) != 1 {
			return true
		}
		literal, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		kind := strings.Trim(literal.Value, "\"")
		if !registered[kind] {
			t.Errorf("event constant %s is absent from EventKinds", spec.Names[0])
		}
		known[spec.Names[0].Name] = true
		return true
	})
	isKind := func(expr ast.Expr) bool {
		if selector, ok := expr.(*ast.SelectorExpr); ok {
			return known[selector.Sel.Name]
		}
		if name, ok := expr.(*ast.Ident); ok {
			return known[name.Name]
		}
		return false
	}
	for _, dir := range []string{".", "../harness"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					name := ""
					switch typ := value.Type.(type) {
					case *ast.Ident:
						name = typ.Name
					case *ast.SelectorExpr:
						name = typ.Sel.Name
					}
					if name != "Event" {
						return true
					}
					for _, element := range value.Elts {
						pair, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := pair.Key.(*ast.Ident)
						if !ok || key.Name != "Kind" {
							continue
						}
						// The three forwarding helpers are checked at their call sites.
						forward, ok := pair.Value.(*ast.Ident)
						if !isKind(pair.Value) && !(ok && forward.Name == "kind") {
							t.Errorf("%s: event must use a registered constant", set.Position(pair.Pos()))
						}
					}
				case *ast.CallExpr:
					name := ""
					switch callee := value.Fun.(type) {
					case *ast.Ident:
						name = callee.Name
					case *ast.SelectorExpr:
						name = callee.Sel.Name
					}
					if name == "modelEvent" || name == "toolEvent" || name == "notify" || name == "controlFrame" {
						if len(value.Args) > 0 && !isKind(value.Args[0]) {
							t.Errorf("%s: %s must receive a registered event constant", set.Position(value.Pos()), name)
						}
					}
				}
				return true
			})
		}
	}
}
