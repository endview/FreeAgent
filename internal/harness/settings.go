package harness

import (
	"context"
	"errors"
	"path/filepath"
)

// ProtectedPaths is a read-only projection for settings UIs. The runtime
// still derives protection independently for every file operation; clients
// cannot submit this list as authority.
func (tasks *TaskService) ProtectedPaths() []string {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return nil
	}
	paths := []string{filepath.Clean(tasks.DataDir())}
	if keys := tasks.Keys(); keys != nil && keys.Path() != "" {
		paths = append(paths, filepath.Clean(keys.Path()))
	}
	return uniqueAbsoluteRoots(paths)
}

func validateUserBlockedPaths(dataDir string, paths []string) error {
	data, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		resolved, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return err
		}
		if protectedPath(data, resolved) || within(data, resolved) {
			return ErrProtectedPath
		}
	}
	return nil
}

// ValidateFileAccessSettings applies the one rule that cannot be inferred
// from the generic Config validator: the selected-root scope needs a root,
// while all_non_protected intentionally does not.
func ValidateFileAccessSettings(ctx context.Context, dataDir string, scope FileScope, roots, blocked []string) error {
	if ctx == nil {
		return errors.New("harness: settings context is required")
	}
	scope = canonicalFileScope(scope)
	if err := scope.Validate(); err != nil {
		return err
	}
	if scope == FileScopeWorkspacePlusAllowedRoots && len(roots) == 0 {
		return ErrComputerRootsRequired
	}
	if err := validateUserBlockedPaths(dataDir, blocked); err != nil {
		return err
	}
	return nil
}
