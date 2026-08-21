//go:build darwin

package currentbackup

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func publishNoReplace(source, destination string) error {
	if err := unix.RenamexNp(source, destination, unix.RENAME_EXCL); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("%w: %s", ErrTargetExists, destination)
		}
		return err
	}
	return nil
}
