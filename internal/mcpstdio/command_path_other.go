//go:build !windows

package mcpstdio

func launchPath(path string) string { return path }

func launchDir(path string) (string, bool) { return path, true }
