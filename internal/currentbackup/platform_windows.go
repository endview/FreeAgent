//go:build windows

package currentbackup

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// Windows can report ERROR_ACCESS_DENIED while another process briefly
	// holds a file below a directory being renamed. These six waits bound that
	// transient window to 630ms without changing the no-replace operation.
	currentBackupWindowsPublishRetryCount        = 6
	currentBackupWindowsPublishInitialRetryDelay = 10 * time.Millisecond
)

type currentBackupWindowsMoveFileEx func(*uint16, *uint16, uint32) error
type currentBackupWindowsSleep func(time.Duration)

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
	return publishNoReplaceWindows(
		source,
		destination,
		windows.MoveFileEx,
		time.Sleep,
	)
}

func publishNoReplaceWindows(
	source string,
	destination string,
	moveFileEx currentBackupWindowsMoveFileEx,
	sleep currentBackupWindowsSleep,
) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	for retry := 0; ; retry++ {
		err := moveFileEx(
			sourcePointer,
			destinationPointer,
			windows.MOVEFILE_WRITE_THROUGH,
		)
		if err == nil {
			return nil
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return fmt.Errorf("%w: %s", ErrTargetExists, destination)
		}
		if retry >= currentBackupWindowsPublishRetryCount ||
			(!errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
				!errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		sleep(currentBackupWindowsPublishInitialRetryDelay << retry)
	}
}

// Windows does not expose a portable directory fsync. MoveFileEx with
// MOVEFILE_WRITE_THROUGH supplies the strongest available publication fence.
func syncDirectory(string) error { return nil }
