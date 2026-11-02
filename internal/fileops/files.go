// Package fileops retains normal filesystem semantics while tolerating brief
// Windows sharing conflicts. It never removes a destination before rename.
package fileops

import "os"

func Rename(source, target string) error {
	return retrySharing(func() error { return os.Rename(source, target) })
}

func Remove(path string) error {
	return retrySharing(func() error { return os.Remove(path) })
}
