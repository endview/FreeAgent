//go:build windows

package moduleartifactstore

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type artifactRootFileLockV1 struct {
	file       *os.File
	overlapped windows.Overlapped
}

func tryLockArtifactRootFileV1(file *os.File) (*artifactRootFileLockV1, bool, error) {
	if file == nil {
		return nil, false, errors.New("module artifact store: root lease file is invalid")
	}
	lock := &artifactRootFileLockV1{file: file}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, errors.New("module artifact store: acquire root lease failed")
	}
	return lock, false, nil
}

func (lock *artifactRootFileLockV1) releaseV1() error {
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
	if unlockErr != nil || closeErr != nil {
		return errors.New("module artifact store: release root lease failed")
	}
	return nil
}
