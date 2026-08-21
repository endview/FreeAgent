//go:build linux || darwin

package safefiletree

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

func changeTestPermissionMetadata(descriptor uintptr) error {
	return unix.Fchmod(int(descriptor), 0o700)
}

func changeTestPermissionMetadataAfterChangeTick(descriptor uintptr) error {
	fd := int(descriptor)
	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := unix.Fchmod(fd, 0o600); err != nil {
			return err
		}
		if err := unix.Fchmod(fd, 0o700); err != nil {
			return err
		}
		var after unix.Stat_t
		if err := unix.Fstat(fd, &after); err != nil {
			return err
		}
		if unixChangeTime(after) != unixChangeTime(before) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("permission metadata change time did not advance")
		}
		time.Sleep(time.Millisecond)
	}
}

func changeTestContent(descriptor uintptr) error {
	_, err := unix.Pwrite(int(descriptor), []byte("after!"), 0)
	return err
}

func changeTestSize(descriptor uintptr) error {
	return unix.Ftruncate(int(descriptor), 1)
}

func changeTestModificationTime(descriptor uintptr) error {
	stamp := unix.NsecToTimeval(time.Now().Add(time.Hour).UnixNano())
	return unix.Futimes(int(descriptor), []unix.Timeval{stamp, stamp})
}
