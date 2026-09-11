//go:build !windows

package mcpstdio

import "testing"

func TestNonWindowsLaunchDirIsDirect(t *testing.T) {
	const path = `/very/long/working/directory`
	dir, direct := launchDir(path)
	if !direct || dir != path {
		t.Fatalf("launchDir() = %q, %t; want %q, true", dir, direct, path)
	}
}

func TestNonWindowsLongDirMappingIsUnavailable(t *testing.T) {
	_, cleanup, err := mapLongDir(`/very/long/working/directory`)
	if err == nil {
		t.Fatal("mapLongDir() error = nil, want error")
	}
	if cleanup != nil {
		cleanup()
		t.Fatal("mapLongDir() cleanup = non-nil, want nil")
	}
}
