//go:build windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// disableEcho turns off echo on a console stdin and returns the function that
// restores the original mode. Anything other than a console is
// errNotConsole.
func disableEcho(stdin io.Reader) (func(), error) {
	file, ok := stdin.(*os.File)
	if !ok {
		return nil, errNotConsole
	}
	handle := windows.Handle(file.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return nil, errNotConsole
	}
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }, nil
}
