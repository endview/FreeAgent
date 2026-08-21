//go:build !windows

package moduleapi

import (
	"os"
	"testing"
	"time"
)

func TestRejectArtifactFilesystemBoundary(t *testing.T) {
	t.Parallel()

	root := artifactDeviceTestInfo{device: 11}
	if err := rejectArtifactFilesystemBoundary(root, artifactDeviceTestInfo{device: 11}); err != nil {
		t.Fatalf("same-device entry rejected: %v", err)
	}
	if err := rejectArtifactFilesystemBoundary(root, artifactDeviceTestInfo{device: 12}); err == nil {
		t.Fatal("different-device entry accepted")
	}
}

type artifactDeviceTestInfo struct {
	device uint64
}

func (info artifactDeviceTestInfo) Name() string       { return "device-test" }
func (info artifactDeviceTestInfo) Size() int64        { return 0 }
func (info artifactDeviceTestInfo) Mode() os.FileMode  { return 0 }
func (info artifactDeviceTestInfo) ModTime() time.Time { return time.Time{} }
func (info artifactDeviceTestInfo) IsDir() bool        { return false }
func (info artifactDeviceTestInfo) Sys() any {
	return &struct{ Dev uint64 }{Dev: info.device}
}
