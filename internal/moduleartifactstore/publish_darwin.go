//go:build darwin

package moduleartifactstore

import (
	"errors"

	"golang.org/x/sys/unix"
)

func publishNoReplaceV1(source, destination string) error {
	err := unix.RenamexNp(source, destination, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		return ErrTargetExists
	}
	return err
}
