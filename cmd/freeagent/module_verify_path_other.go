//go:build !windows

package main

import "os"

func moduleVerifyPathIsReparsePoint(os.FileInfo) bool {
	return false
}
