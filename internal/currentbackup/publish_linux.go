//go:build linux

package currentbackup

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func publishNoReplace(source, destination string) error {
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_NOREPLACE,
	); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("%w: %s", ErrTargetExists, destination)
		}
		return err
	}
	return nil
}
