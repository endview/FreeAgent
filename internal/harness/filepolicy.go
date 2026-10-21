package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrComputerFilesDisabled = errors.New("harness: computer file access is disabled")
var ErrComputerRootsRequired = errors.New("harness: at least one computer file root is required")
var ErrPathNotAllowed = errors.New("harness: path is outside authorized computer file roots")
var ErrPathBlocked = errors.New("harness: path is blocked by the file policy")
var ErrProtectedPath = errors.New("harness: protected FreeAgent path")
var ErrGenerationConflict = errors.New("harness: runtime policy generation conflict")

func validateComputerRoots(ctx context.Context, dataDir string, roots []string) error {
	if len(roots) > 8 {
		return errors.New("harness: at most 8 computer file roots are allowed")
	}
	data, err := canonicalDir(dataDir)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, raw := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := strings.TrimSpace(raw)
		if path == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("harness: computer file roots must be absolute directories")
		}
		resolved, err := canonicalDir(path)
		if err != nil {
			return errors.New("harness: computer file root does not exist")
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("harness: computer file root must be a directory or regular file")
		}
		if err := rejectReparsePoint(resolved); err != nil {
			return errors.New("harness: computer file root cannot be a reparse point")
		}
		// A parent such as D:\ may contain the Harness data directory. It is
		// allowed as a selection, but every Broker operation rejects the
		// protected subtree below it.
		if within(data, resolved) {
			return ErrProtectedPath
		}
		key := strings.ToLower(filepath.Clean(resolved))
		if seen[key] {
			return errors.New("harness: duplicate computer file root")
		}
		seen[key] = true
	}
	return nil
}

func normalizeComputerRoots(ctx context.Context, dataDir string, roots []string) ([]string, error) {
	if err := validateComputerRoots(ctx, dataDir, roots); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(roots))
	for _, raw := range roots {
		resolved, err := filepath.Abs(filepath.Clean(strings.TrimSpace(raw)))
		if err != nil {
			return nil, err
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.IsDir() && !info.Mode().IsRegular() {
			return nil, errors.New("harness: computer file root does not exist")
		}
		if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
			resolved = evaluated
		}
		result = append(result, filepath.Clean(resolved))
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	kept := result[:0]
	for _, item := range result {
		if len(kept) == 0 || !within(kept[len(kept)-1], item) {
			kept = append(kept, item)
		}
	}
	return kept, nil
}

func normalizeBlockedPaths(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) > 64 {
		return nil, errors.New("harness: at most 64 blocked paths are allowed")
	}
	result := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := strings.TrimSpace(raw)
		if path == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, errors.New("harness: blocked paths must be absolute")
		}
		resolved, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return nil, errors.New("harness: invalid blocked path")
		}
		key := strings.ToLower(filepath.Clean(resolved))
		if seen[key] {
			return nil, errors.New("harness: duplicate blocked path")
		}
		seen[key] = true
		result = append(result, filepath.Clean(resolved))
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result, nil
}

func protectedPath(dataDir, target string) bool {
	data, err := filepath.Abs(dataDir)
	if err != nil {
		return true
	}
	resolved, err := filepath.Abs(target)
	if err != nil {
		return true
	}
	if within(data, resolved) {
		return true
	}
	name := strings.ToLower(filepath.Base(resolved))
	return name == "keys.json" || name == "config.json" || name == "owner.lock" ||
		name == "desktop.json" || strings.HasPrefix(name, "state.sqlite")
}

func validateExternalPath(dataDir string, roots []string, raw string) (string, error) {
	return validateExternalPathWithBlocked(dataDir, roots, nil, raw)
}

func validateExternalPathWithBlocked(dataDir string, roots, blocked []string, raw string) (string, error) {
	if !filepath.IsAbs(raw) {
		return "", errors.New("harness: external file path must be absolute")
	}
	resolved, err := filepath.Abs(filepath.Clean(raw))
	if err != nil || protectedPath(dataDir, resolved) {
		return "", ErrProtectedPath
	}
	for _, denied := range blocked {
		if within(denied, resolved) {
			return "", ErrPathBlocked
		}
	}
	for _, root := range roots {
		if within(root, resolved) {
			return resolved, nil
		}
	}
	return "", ErrPathNotAllowed
}

func sameRootSet(selected, configured []string) bool {
	if len(selected) == 0 {
		return false
	}
	for _, root := range selected {
		found := false
		for _, allowed := range configured {
			if samePath(root, allowed) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
