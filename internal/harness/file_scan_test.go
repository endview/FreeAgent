package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceScansRefreshContentAndRespectBoundaries(t *testing.T) {
	workspace := t.TempDir()
	data, blocked := filepath.Join(workspace, "private"), filepath.Join(workspace, "blocked")
	for _, path := range []string{data, blocked, filepath.Join(workspace, ".git")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.txt", "first\nneedle-one\n")
	for _, name := range []string{"private/credential.txt", "blocked/secret.txt", ".git/config"} {
		write(name, "needle-private-do-not-expose")
	}
	files, err := NewFileTools(workspace, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	files = files.withBlockedPaths([]string{blocked})
	commands := &CommandTools{Files: files}
	before, _ := commands.observe(t.Context())
	if len(before) != 1 || before["visible.txt"] != contentHash([]byte("first\nneedle-one\n")) {
		t.Fatalf("observation included protected data or lost content: %v", before)
	}
	search := func(want string) {
		t.Helper()
		result, err := files.search(t.Context(), ".", "needle")
		var output struct {
			Matches []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Text string `json:"text"`
			} `json:"matches"`
		}
		if err != nil || json.Unmarshal([]byte(result.Content), &output) != nil || len(output.Matches) != 1 ||
			output.Matches[0].Path != "visible.txt" || output.Matches[0].Line != 2 || output.Matches[0].Text != want {
			t.Fatalf("search=%+v err=%v", result, err)
		}
		if strings.Contains(result.Content, "private-do-not-expose") {
			t.Fatal("search exposed protected content")
		}
	}
	search("needle-one")
	write("visible.txt", "first\nneedle-two\n") // Same size: metadata cannot stand in for a fresh read.
	after, _ := commands.observe(t.Context())
	if after["visible.txt"] == before["visible.txt"] || after["visible.txt"] != contentHash([]byte("first\nneedle-two\n")) {
		t.Fatal("a repeated scan reused stale content")
	}
	search("needle-two")
	if err := os.Link(filepath.Join(blocked, "secret.txt"), filepath.Join(workspace, "alias.txt")); err == nil {
		observed, complete := commands.observe(t.Context())
		if complete || observed["alias.txt"] != "" {
			t.Fatal("hard-linked content was accepted as observed text")
		}
		search("needle-two")
	} else {
		t.Logf("hardlink case not run on this filesystem: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if observed, complete := commands.observe(ctx); complete || len(observed) != 0 {
		t.Fatal("cancelled observation returned a complete scan")
	}
	if _, err := files.search(ctx, ".", "needle"); err == nil {
		t.Fatal("cancelled search succeeded")
	}
}
