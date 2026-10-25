//go:build windows

package harness

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

var errExecutableTarget = errors.New("executable_target_unavailable: unsupported or unsafe shim target")

// Only Scoop's native .exe + .shim pair is interpreted. Scripts and arbitrary
// shim chains are never executed to discover a target.
func resolveCommandTarget(program string, args []string) (commandTarget, error) {
	target := commandTarget{program: program, args: args}
	if !strings.EqualFold(filepath.Ext(program), ".exe") {
		return target, errExecutableTarget
	}
	metadata := strings.TrimSuffix(program, filepath.Ext(program)) + ".shim"
	_, err := os.Lstat(metadata)
	if errors.Is(err, os.ErrNotExist) {
		if strings.EqualFold(filepath.Base(filepath.Dir(program)), "shims") {
			return target, errExecutableTarget
		}
		return target, nil
	}
	if err != nil || !strings.EqualFold(filepath.Base(filepath.Dir(program)), "shims") {
		return target, errExecutableTarget
	}
	if verifyRegularFile(metadata) != nil {
		return target, errExecutableTarget
	}
	file, err := os.Open(metadata)
	if err != nil {
		return target, errExecutableTarget
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 32769))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 32768 || bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		return target, errExecutableTarget
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || (key != "path" && key != "args") {
			return target, errExecutableTarget
		}
		if _, duplicate := fields[key]; duplicate {
			return target, errExecutableTarget
		}
		fields[key] = value
	}
	path := fields["path"]
	if len(path) < 3 || path[0] != '"' || path[len(path)-1] != '"' {
		return target, errExecutableTarget
	}
	path = path[1 : len(path)-1]
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\"\r\n") || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return target, errExecutableTarget
	}
	apps := filepath.Join(filepath.Dir(filepath.Dir(program)), "apps")
	relative, err := filepath.Rel(apps, path)
	if err != nil || !within(apps, path) {
		return target, errExecutableTarget
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) < 3 {
		return target, errExecutableTarget
	}
	installation, err := canonicalCommandPath(filepath.Join(apps, parts[0], parts[1]))
	if err != nil {
		return target, errExecutableTarget
	}
	canonicalApps, err := canonicalCommandPath(apps)
	if err != nil || !within(canonicalApps, installation) {
		return target, errExecutableTarget
	}
	resolved, err := canonicalCommandPath(path)
	if err != nil || !within(installation, resolved) || verifyRegularFile(resolved) != nil ||
		strings.EqualFold(filepath.Base(filepath.Dir(resolved)), "shims") {
		return target, errExecutableTarget
	}
	if _, err := os.Lstat(strings.TrimSuffix(resolved, filepath.Ext(resolved)) + ".shim"); !errors.Is(err, os.ErrNotExist) {
		return target, errExecutableTarget
	}
	prefix, err := windows.DecomposeCommandLine("shim " + fields["args"])
	if err != nil || len(prefix) == 0 || len(prefix)-1+len(args) > 128 {
		return target, errExecutableTarget
	}
	return commandTarget{program: resolved, args: append(prefix[1:], args...), root: installation, shim: program}, nil
}

// GetFinalPathNameByHandle resolves directory junctions as well as symlinks.
// filepath.EvalSymlinks can retain a mount-point junction on Windows.
func canonicalCommandPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil || length == 0 || int(length) >= len(buffer) {
		return "", errExecutableTarget
	}
	resolved := windows.UTF16ToString(buffer[:length])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		return "", errExecutableTarget
	}
	resolved = strings.TrimPrefix(resolved, `\\?\`)
	if len(filepath.VolumeName(resolved)) != 2 {
		return "", errExecutableTarget
	}
	return filepath.Clean(resolved), nil
}
