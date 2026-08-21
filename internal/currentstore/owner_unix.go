//go:build !windows

package currentstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type unixProcessFileLock struct {
	file *os.File
}

func acquireProcessFileLock(path string) (processFileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open owner lock: %w", err)
	}
	if err := unix.Flock(
		int(file.Fd()),
		unix.LOCK_EX|unix.LOCK_NB,
	); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) ||
			errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrOwnerActive, path)
		}
		return nil, fmt.Errorf("lock owner file: %w", err)
	}
	return &unixProcessFileLock{file: file}, nil
}

func (lock *unixProcessFileLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func validateDatabaseLinkSafety(path string) error {
	var information unix.Stat_t
	if err := unix.Stat(path, &information); err != nil {
		return fmt.Errorf("currentstore: inspect database identity: %w", err)
	}
	if uint64(information.Nlink) != 1 {
		return errors.New(
			"currentstore: database hard-link aliases are not supported",
		)
	}
	return nil
}

func ownerPathKey(path string) string {
	return path
}

func publishFileNoReplace(source, target string) error {
	if err := os.Link(source, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ErrExist
		}
		return err
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("remove published source link: %w", err)
	}
	return nil
}
