//go:build windows

package mcpstdio

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

const windowsExtendedPathPrefix = `\\?\`
const windowsExtendedUNCPathPrefix = `\\?\UNC\`
const windowsMaxPath = 260

func extendedPath(path string) string {
	if path == "" {
		return path
	}
	clean := filepath.Clean(path)
	if len(clean) >= len(windowsExtendedPathPrefix) &&
		clean[:len(windowsExtendedPathPrefix)] == windowsExtendedPathPrefix ||
		len(clean) >= len(windowsExtendedUNCPathPrefix) &&
			clean[:len(windowsExtendedUNCPathPrefix)] == windowsExtendedUNCPathPrefix {
		return clean
	}
	if clean == `\\` || len(clean) < 2 ||
		clean[0] != '\\' || clean[1] != '\\' {
		return windowsExtendedPathPrefix + clean
	}
	return windowsExtendedUNCPathPrefix + clean[2:]
}

func launchPath(path string) string {
	clean := filepath.Clean(path)
	if len(clean) < windowsMaxPath {
		return clean
	}
	if short, ok := windowsShortPath(extendedPath(clean)); ok {
		return short
	}
	return extendedPath(clean)
}

func launchDirPath(path string) string {
	clean := filepath.Clean(path)
	if len(clean) < windowsMaxPath {
		return clean
	}
	if short, ok := windowsShortPath(extendedPath(clean)); ok {
		return short
	}
	return clean
}

func windowsShortPath(path string) (string, bool) {
	longPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	needed, err := windows.GetShortPathName(longPath, nil, 0)
	if err != nil || needed == 0 {
		return "", false
	}
	shortPath := make([]uint16, needed)
	returned, err := windows.GetShortPathName(longPath, &shortPath[0], uint32(len(shortPath)))
	if err != nil || returned == 0 || returned >= uint32(len(shortPath)) {
		return "", false
	}
	short := windows.UTF16ToString(shortPath[:returned])
	return short, short != ""
}
