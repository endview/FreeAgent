//go:build linux || darwin

package safefiletree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

func replaceDirectoryWithSameRootLink(path, target string) (func(), error) {
	moved := path + ".before-swap"
	pathExists := true
	if err := os.Rename(path, moved); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("move directory before replacement: %w", err)
		}
		pathExists = false
	}
	relativeTarget, err := filepath.Rel(filepath.Dir(path), target)
	if err != nil {
		if pathExists {
			_ = os.Rename(moved, path)
		}
		return nil, err
	}
	if err := os.Symlink(relativeTarget, path); err != nil {
		if pathExists {
			_ = os.Rename(moved, path)
		}
		return nil, fmt.Errorf("create same-root symlink: %w", err)
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
