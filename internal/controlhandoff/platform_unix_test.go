//go:build linux

package controlhandoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnixDirectoryIdentityComparisonRejectsReopenedParentV1(t *testing.T) {
	one := unixDirectoryIdentityV1{device: 1, inode: 2}
	if !sameUnixDirectoryObjectV1(one, one) {
		t.Fatal("stable parent identity was rejected")
	}
	for _, changed := range []unixDirectoryIdentityV1{
		{device: 9, inode: 2},
		{device: 1, inode: 9},
	} {
		if sameUnixDirectoryObjectV1(one, changed) {
			t.Fatalf("reopened parent identity was accepted: %+v", changed)
		}
	}
}

func TestUnixFilesystemDecisionRejectsRemoteAndUnknownV1(t *testing.T) {
	for _, filesystemType := range []int64{
		unix.EXT4_SUPER_MAGIC,
		unix.XFS_SUPER_MAGIC,
		unix.BTRFS_SUPER_MAGIC,
		unix.TMPFS_MAGIC,
		unix.RAMFS_MAGIC,
		unix.OVERLAYFS_SUPER_MAGIC,
	} {
		if !isTrustedLocalFilesystemTypeUnixV1(filesystemType) {
			t.Fatalf("local filesystem type %x was rejected", filesystemType)
		}
	}
	for _, filesystemType := range []int64{
		unix.NFS_SUPER_MAGIC,
		unix.CIFS_SUPER_MAGIC,
		unix.FUSE_SUPER_MAGIC,
		0,
		-1,
	} {
		if isTrustedLocalFilesystemTypeUnixV1(filesystemType) {
			t.Fatalf("untrusted filesystem type %x was accepted", filesystemType)
		}
	}
}

func TestUnixCloseFailureRemovesExactCreatedLeafV1(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "close-failure.json")
	_, err := writeExclusivePrivateFileWithCloseUnixV1(
		path,
		[]byte(`{"bounded":"canonical"}`),
		func(file *os.File) error {
			closeErr := file.Close()
			if closeErr != nil {
				return closeErr
			}
			return errors.New("injected close report")
		},
	)
	if !errors.Is(err, ErrSecurityVerification) {
		t.Fatalf("injected close error = %v", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("close failure left capability leaf: %v", statErr)
	}
}

func TestUnixNilCloseSeamFailsBeforeCreateV1(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "nil-close.json")
	if _, err := writeExclusivePrivateFileWithCloseUnixV1(
		path,
		[]byte(`{"bounded":"canonical"}`),
		nil,
	); !errors.Is(err, ErrSecurityVerification) {
		t.Fatalf("nil close seam error = %v", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("nil close seam created leaf: %v", statErr)
	}
}
