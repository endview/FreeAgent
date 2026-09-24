package runtimefacts

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedRuntimeFactsAreCurrent(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	wantJSON, wantMarkdown, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{
		filepath.Join(root, "docs", "generated", "runtime-facts.json"): wantJSON,
		filepath.Join(root, "docs", "generated", "runtime-facts.md"):   wantMarkdown,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated fact %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("generated fact %s is stale; run go generate ./...", path)
		}
	}
}
