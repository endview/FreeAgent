//go:build windows

package apppaths

import "golang.org/x/sys/windows"

// The Documents folder may be redirected; the Shell knows the real location.
func workspaceBase() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Documents, windows.KF_FLAG_DEFAULT)
}
