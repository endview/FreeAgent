//go:build windows

package harness

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type processFileLock struct {
	file *os.File
	lock windows.Overlapped
}

func openWorkspaceFile(root *os.Root, path string) (*os.File, error) {
	return root.Open(path)
}

func acquireProcessFileLock(path string) (processFileLock, error) {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return processFileLock{}, errors.New("harness: unsafe owner lock")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return processFileLock{}, err
	}
	result := processFileLock{file: file}
	if err := checkRegularHandle(file); err != nil {
		_ = file.Close()
		return processFileLock{}, err
	}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &result.lock); err != nil {
		_ = file.Close()
		return processFileLock{}, ErrOwnerActive
	}
	return result, nil
}

func (lock *processFileLock) release() error {
	if lock.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.lock)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func checkRegularHandle(file *os.File) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("harness: only regular files are supported")
	}
	var attrs windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &attrs); err != nil {
		return err
	}
	if attrs.NumberOfLinks != 1 || attrs.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("harness: linked files are not supported")
	}
	return nil
}

func rejectReparsePoint(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var attrs windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &attrs); err != nil {
		return err
	}
	if attrs.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("harness: reparse point")
	}
	return nil
}

func verifyRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("harness: unsafe file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return checkRegularHandle(file)
}

func publishNoReplace(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}

func publishDirectoryNoReplace(source, target string) error {
	return publishNoReplace(source, target)
}
