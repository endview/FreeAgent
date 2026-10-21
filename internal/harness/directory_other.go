//go:build !windows && !linux

package harness

import "errors"

func publishDirectoryNoReplace(source, target string) error {
	return errors.New("harness: atomic directory publication is currently supported on Windows and Linux only")
}
