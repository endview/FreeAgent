//go:build !windows

package postgres

import (
	"os"
	"os/exec"
)

func hide(cmd *exec.Cmd) {}
func protect(path string, dir bool) error {
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
