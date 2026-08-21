//go:build !windows

package moduleartifactstore

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type artifactRootFileLockV1 struct {
	file *os.File
}

func tryLockArtifactRootFileV1(file *os.File) (*artifactRootFileLockV1, bool, error) {
	if file == nil {
		return nil, false, errors.New("module artifact store: root lease file is invalid")
	}
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, errors.New("module artifact store: acquire root lease failed")
	}
	return &artifactRootFileLockV1{file: file}, false, nil
}

func (lock *artifactRootFileLockV1) releaseV1() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil || closeErr != nil {
		return errors.New("module artifact store: release root lease failed")
	}
	return nil
}
