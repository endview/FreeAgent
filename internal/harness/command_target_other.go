//go:build !windows

package harness

import (
	"os"
	"path/filepath"
)

func canonicalCommandPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func resolveCommandTarget(program string, args []string) (commandTarget, error) {
	resolved, err := filepath.EvalSymlinks(program)
	if err != nil {
		return commandTarget{}, err
	}
	target := commandTarget{program: resolved, args: args}
	if filepath.Base(resolved) == "go" {
		root := filepath.Dir(filepath.Dir(resolved))
		if info, err := os.Stat(filepath.Join(root, "src")); err == nil && info.IsDir() {
			target.root = root
		}
	}
	return target, nil
}
