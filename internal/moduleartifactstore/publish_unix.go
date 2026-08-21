//go:build !windows

package moduleartifactstore

import (
	"errors"
	"os"
)

func syncDirectoryV1(path string) (returnErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, directory.Close()) }()
	return directory.Sync()
}
