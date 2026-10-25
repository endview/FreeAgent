//go:build !windows

package harness

import "path/filepath"

func canonicalCommandPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func resolveCommandTarget(program string, args []string) (commandTarget, error) {
	return commandTarget{program: program, args: args}, nil
}
