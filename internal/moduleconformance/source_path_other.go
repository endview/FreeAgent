//go:build !windows

package moduleconformance

import "os"

func sourcePathIsReparsePoint(os.FileInfo) bool {
	return false
}
