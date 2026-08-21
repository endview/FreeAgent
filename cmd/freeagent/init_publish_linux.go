//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func publishInitNoReplace(source, destination string) error {
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_NOREPLACE,
	); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("composition: target already exists: %s", destination)
		}
		return err
	}
	return nil
}

func syncInitDirectory(path string) (returnErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, directory.Close()) }()
	return directory.Sync()
}
