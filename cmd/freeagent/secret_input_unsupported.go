//go:build !windows && !linux

package main

import "io"

// Hidden input is implemented for the supported Windows and Linux builds.
// Elsewhere --key-stdin or --key-env provides the key.
func disableEcho(io.Reader) (func(), error) { return nil, errNotConsole }
