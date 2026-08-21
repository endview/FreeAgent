//go:build windows

package currentbackup

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

type offlineFence struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireOfflineFence(databasePath string) (*offlineFence, error) {
	path := databasePath + ".freeagent.owner.lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: open owner fence: %w", err)
	}
	fence := &offlineFence{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&fence.overlapped,
	)
	if err == nil {
		return fence, nil
	}
	_ = file.Close()
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, fmt.Errorf("%w: %s", ErrSourceActive, databasePath)
	}
	return nil, fmt.Errorf("currentbackup: lock owner fence: %w", err)
}

func (fence *offlineFence) close() error {
	if fence == nil || fence.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(
		windows.Handle(fence.file.Fd()),
		0,
		1,
		0,
		&fence.overlapped,
	)
	closeErr := fence.file.Close()
	fence.file = nil
	return errors.Join(unlockErr, closeErr)
}

func openedFileLinkCount(file *os.File) (uint64, error) {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&information,
	); err != nil {
		return 0, fmt.Errorf("currentbackup: inspect file identity: %w", err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, fmt.Errorf("%w: reparse-point file is forbidden", ErrIntegrity)
	}
	return uint64(information.NumberOfLinks), nil
}

func rejectSafeTreePlatformSpecial(info os.FileInfo) error {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return fmt.Errorf("%w: Windows file metadata is unavailable", ErrIntegrity)
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: reparse-point path is forbidden", ErrIntegrity)
	}
	return nil
}

func rejectSafeTreeFilesystemBoundary(os.FileInfo, os.FileInfo) error {
	// Windows mount points are reparse points and are rejected above.
	return nil
}

func publishNoReplace(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(
		sourcePointer,
		destinationPointer,
		windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return fmt.Errorf("%w: %s", ErrTargetExists, destination)
		}
		return err
	}
	return nil
}

// Windows does not expose a portable directory fsync. MoveFileEx with
// MOVEFILE_WRITE_THROUGH supplies the strongest available publication fence.
func syncDirectory(string) error { return nil }
