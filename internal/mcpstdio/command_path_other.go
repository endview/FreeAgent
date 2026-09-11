//go:build !windows

package mcpstdio

func launchPath(path string) string { return path }

func launchDirPath(path string) string { return path }
