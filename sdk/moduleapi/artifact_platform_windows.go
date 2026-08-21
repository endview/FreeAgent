//go:build windows

package moduleapi

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func rejectArtifactPlatformSpecial(info os.FileInfo) error {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse points are not allowed")
	}
	return nil
}

func rejectArtifactFilesystemBoundary(os.FileInfo, os.FileInfo) error {
	// Windows mount points are reparse points and are rejected by
	// rejectArtifactPlatformSpecial before traversal can cross them.
	return nil
}
