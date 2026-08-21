//go:build windows

package moduleartifactstore

import (
	"errors"

	"golang.org/x/sys/windows"
)

func publishNoReplaceV1(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(sourcePointer, destinationPointer, windows.MOVEFILE_WRITE_THROUGH)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return ErrTargetExists
	}
	return err
}

func syncDirectoryV1(string) error { return nil }
