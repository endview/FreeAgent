package harness

import (
	"path/filepath"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/fileops"
)

// Browser session storage can contain the local launch capability. It stays
// private even when a user grants a parent directory or overrides --data-dir.
func privateApplicationPaths(dataDir string) ([]string, error) {
	paths, err := apppaths.PrivateStateDirs()
	if err != nil {
		return nil, err
	}
	keys, err := apppaths.KeysFile()
	if err != nil {
		return nil, err
	}
	return appendPrivatePaths(nil, append(paths, dataDir, keys)...), nil
}

func appendPrivatePaths(paths []string, more ...string) []string {
	for _, path := range more {
		if resolved, err := fileops.CanonicalPath(path); err == nil {
			path = resolved
		}
		if path == "" || insidePrivatePath(paths, path) {
			continue
		}
		kept := paths[:0]
		for _, previous := range paths {
			if previous != "" && !within(path, previous) {
				kept = append(kept, previous)
			}
		}
		paths = append(kept, filepath.Clean(path))
	}
	return paths
}

func insidePrivatePath(paths []string, target string) bool {
	for _, path := range paths {
		if path != "" && within(path, target) {
			return true
		}
	}
	return false
}

func (tools *FileTools) isPrivatePath(target string) bool {
	if insidePrivatePath(tools.private, target) {
		return true
	}
	if !tools.privateDescendants {
		return false
	}
	// Windows 8.3 names can reach the same directory under another spelling.
	// Only broad roots containing private state need this additional lookup.
	resolved, err := fileops.CanonicalPath(target)
	return err == nil && insidePrivatePath(tools.private, resolved)
}
