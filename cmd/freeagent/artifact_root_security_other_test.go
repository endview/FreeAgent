//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func artifactRootBelowUnsafeAncestorV1(t *testing.T) string {
	t.Helper()
	grandparent := t.TempDir()
	if err := os.Chmod(grandparent, 0o777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(grandparent, "state", "artifacts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
