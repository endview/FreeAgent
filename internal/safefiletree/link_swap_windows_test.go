//go:build windows

package safefiletree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestWalkPathRejectsCaseMismatchedComponentBeforeRead(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "ExactDirectory"))
	mustWriteFile(t, filepath.Join(root, "ExactDirectory", "ExactFile.txt"), "secret")

	reads := 0
	err := WalkPath(
		context.Background(),
		root,
		"ExactDirectory/exactfile.txt",
		func(_ context.Context, entry Entry) error {
			if entry.Reader == nil {
				return nil
			}
			reads++
			_, readErr := io.ReadAll(entry.Reader)
			return readErr
		},
	)
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("case-mismatched WalkPath error = %v", err)
	}
	if reads != 0 {
		t.Fatalf("case-mismatched WalkPath performed %d file reads, want 0", reads)
	}
}

func replaceDirectoryWithSameRootLink(path, target string) (func(), error) {
	moved := path + ".before-swap"
	pathExists := true
	if err := os.Rename(path, moved); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("move directory before replacement: %w", err)
		}
		pathExists = false
	}
	output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		if pathExists {
			_ = os.Rename(moved, path)
		}
		return nil, fmt.Errorf("create same-root junction: %w: %s", err, output)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = os.Remove(path)
			if pathExists {
				_ = os.Rename(moved, path)
			}
		})
	}, nil
}
