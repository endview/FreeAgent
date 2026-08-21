//go:build linux || darwin

package safefiletree

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWalkRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Walk(context.Background(), root, func(context.Context, Entry) error { return nil })
	if err == nil || !errors.Is(err, ErrUnsafeTree) {
		t.Fatalf("FIFO error = %v", err)
	}
}
