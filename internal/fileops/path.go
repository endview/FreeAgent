package fileops

import (
	"errors"
	"os"
	"path/filepath"
)

// CanonicalPath resolves existing aliases, including Windows short names, and
// preserves a nonexistent suffix. It does not create or modify anything.
func CanonicalPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty filesystem path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			suffix, err := filepath.Rel(current, absolute)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(current) == current {
			return "", err
		}
	}
}
