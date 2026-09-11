//go:build !windows

package mcpstdio

import "errors"

func launchPath(path string) string { return path }

func launchDir(path string) (string, bool) { return path, true }

func mapLongDir(path string) (string, func(), error) {
	return "", nil, errors.New("mcp stdio: DOS-device mapping is only available on Windows")
}
