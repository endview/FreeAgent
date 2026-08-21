//go:build !windows

package currentbackup

import (
	"os"
	"testing"
	"time"
)

func TestRejectSafeTreeFilesystemBoundary(t *testing.T) {
	t.Parallel()

	root := safeTreeDeviceTestInfo{device: 21}
	if err := rejectSafeTreeFilesystemBoundary(root, safeTreeDeviceTestInfo{device: 21}); err != nil {
		t.Fatalf("same-device entry rejected: %v", err)
	}
	if err := rejectSafeTreeFilesystemBoundary(root, safeTreeDeviceTestInfo{device: 22}); err == nil {
		t.Fatal("different-device entry accepted")
	}
}

type safeTreeDeviceTestInfo struct {
	device uint64
}

func (info safeTreeDeviceTestInfo) Name() string       { return "device-test" }
func (info safeTreeDeviceTestInfo) Size() int64        { return 0 }
func (info safeTreeDeviceTestInfo) Mode() os.FileMode  { return 0 }
func (info safeTreeDeviceTestInfo) ModTime() time.Time { return time.Time{} }
func (info safeTreeDeviceTestInfo) IsDir() bool        { return false }
func (info safeTreeDeviceTestInfo) Sys() any {
	return &struct{ Dev uint64 }{Dev: info.device}
}
