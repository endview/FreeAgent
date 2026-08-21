//go:build !windows

package modulesource

import "os"

func localPathIsReparsePoint(os.FileInfo) bool { return false }
