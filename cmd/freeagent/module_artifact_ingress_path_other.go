//go:build !windows

package main

import (
	"os"
	"syscall"
)

func moduleArtifactIngressPathSpecialV1(os.FileInfo) bool { return false }

func moduleArtifactIngressSingleLinkFileV1(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
