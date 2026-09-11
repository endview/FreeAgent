//go:build windows

package mcpstdio

import (
	"errors"
	"fmt"
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
	return extendedPath(clean)
}

func launchDir(path string) (string, bool) {
	clean := filepath.Clean(path)
	if len(clean) < windowsMaxPath {
		return clean, true
	}
	if short, ok := windowsShortPath(extendedPath(clean)); ok {
		if len(short) < windowsMaxPath {
			return short, true
		}
	}
	return extendedPath(clean), false
}

func mapLongDir(path string) (string, func(), error) {
	available, err := windows.GetLogicalDrives()
	if err != nil {
		return "", nil, fmt.Errorf("enumerate DOS devices: %w", err)
	}
	for index := 0; index < 26; index++ {
		if available&(1<<uint(index)) != 0 {
			continue
		}
		letter := rune('A' + index)
		device := string(letter) + ":"
		devicePtr, err := windows.UTF16PtrFromString(device)
		if err != nil {
			return "", nil, fmt.Errorf("encode DOS device name: %w", err)
		}
		targetPtr, err := windows.UTF16PtrFromString(extendedPath(path))
		if err != nil {
			return "", nil, fmt.Errorf("encode DOS device target: %w", err)
		}
		if err := windows.DefineDosDevice(0, devicePtr, targetPtr); err != nil {
			continue
		}
		cleanup := func() {
			_ = windows.DefineDosDevice(windows.DDD_REMOVE_DEFINITION, devicePtr, nil)
		}
		return string(letter) + `:\`, cleanup, nil
	}
	return "", nil, errors.New("no free DOS drive letters")
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
