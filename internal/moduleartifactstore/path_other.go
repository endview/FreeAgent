//go:build !windows

package moduleartifactstore

import "os"

func pathIsReparsePointV1(os.FileInfo) bool { return false }
