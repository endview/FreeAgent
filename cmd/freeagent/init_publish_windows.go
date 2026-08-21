//go:build windows

package main

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// Windows can report ERROR_ACCESS_DENIED while another process briefly
	// holds a file below a directory being renamed. These six waits bound that
	// transient window to 630ms without changing the no-replace operation.
	initPublishWindowsRetryCount        = 6
	initPublishWindowsInitialRetryDelay = 10 * time.Millisecond
)

type initPublishWindowsMoveFileEx func(*uint16, *uint16, uint32) error
type initPublishWindowsSleep func(time.Duration)

func publishInitNoReplace(source, destination string) error {
	return publishInitNoReplaceWindows(
		source,
		destination,
		windows.MoveFileEx,
		time.Sleep,
	)
}

func publishInitNoReplaceWindows(
	source string,
	destination string,
	moveFileEx initPublishWindowsMoveFileEx,
	sleep initPublishWindowsSleep,
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
			return fmt.Errorf("composition: target already exists: %s", destination)
		}
		if retry >= initPublishWindowsRetryCount ||
			(!errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
				!errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		sleep(initPublishWindowsInitialRetryDelay << retry)
	}
}

// MoveFileEx(MOVEFILE_WRITE_THROUGH) is the strongest available Windows
// publication fence; Windows has no portable directory fsync operation.
func syncInitDirectory(string) error { return nil }
