//go:build linux

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// disableEcho clears ECHO on a terminal stdin and returns the function that
// restores the saved settings. Anything other than a terminal is
// errNotConsole.
func disableEcho(stdin io.Reader) (func(), error) {
	file, ok := stdin.(*os.File)
	if !ok {
		return nil, errNotConsole
	}
	fd := int(file.Fd())
	settings, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, errNotConsole
	}
	saved := *settings
	settings.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, settings); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, &saved) }, nil
}
