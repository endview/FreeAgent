package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/endview/freeagent/internal/runtimefacts"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if flag.NArg() != 0 {
		fatalf("unexpected arguments: %v", flag.Args())
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fatalf("resolve repository root: %v", err)
	}
	jsonBytes, markdownBytes, err := runtimefacts.Generate(context.Background(), absRoot)
	if err != nil {
		fatalf("generate runtime facts: %v", err)
	}
	outputDir := filepath.Join(absRoot, "docs", "generated")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	for path, content := range map[string][]byte{
		filepath.Join(outputDir, "runtime-facts.json"): jsonBytes,
		filepath.Join(outputDir, "runtime-facts.md"):   markdownBytes,
	} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			fatalf("write %s: %v", path, err)
		}
	}
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "genfacts: "+format+"\n", arguments...)
	os.Exit(1)
}
