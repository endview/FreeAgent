//go:build !windows

package harness

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type processFileLock struct{ file *os.File }

func openWorkspaceFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}

func acquireProcessFileLock(path string) (processFileLock, error) {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return processFileLock{}, errors.New("harness: unsafe owner lock")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return processFileLock{}, err
	}
	if err := checkRegularHandle(file); err != nil {
		_ = file.Close()
		return processFileLock{}, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return processFileLock{}, ErrOwnerActive
	}
	return processFileLock{file}, nil
}

func (lock *processFileLock) release() error {
	if lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func checkRegularHandle(file *os.File) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("harness: only regular files are supported")
	}
	var attrs unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &attrs); err != nil {
		return err
	}
	if attrs.Nlink != 1 {
		return errors.New("harness: hard-linked files are not supported")
	}
	return nil
}

func rejectReparsePoint(string) error { return nil }

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
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}
