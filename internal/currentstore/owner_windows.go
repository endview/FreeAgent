//go:build windows

package currentstore

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

type windowsProcessFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireProcessFileLock(path string) (processFileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open owner lock: %w", err)
	}
	lock := &windowsProcessFileLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if err == nil {
		return lock, nil
	}
	_ = file.Close()
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, fmt.Errorf("%w: %s", ErrOwnerActive, path)
	}
	return nil, fmt.Errorf("lock owner file: %w", err)
}

func (lock *windowsProcessFileLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(
		windows.Handle(lock.file.Fd()),
		0,
		1,
		0,
		&lock.overlapped,
	)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func validateDatabaseLinkSafety(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("currentstore: open database identity: %w", err)
	}
	defer file.Close()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&information,
	); err != nil {
		return fmt.Errorf("currentstore: inspect database identity: %w", err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("currentstore: database reparse points are not supported")
	}
	if information.NumberOfLinks != 1 {
		return errors.New(
			"currentstore: database hard-link aliases are not supported",
		)
	}
	return nil
}

func ownerPathKey(path string) string {
	return strings.ToLower(path)
}

func publishFileNoReplace(source, target string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if err := windows.MoveFile(sourcePointer, targetPointer); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
