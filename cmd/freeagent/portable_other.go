//go:build !windows

package main

import (
	"errors"
)

func showDoubleClickHint([]string) (bool, error) { return false, nil }

func keepLaunchErrorVisible() {}

func openBrowser(string) error {
	return errors.New("automatic browser opening is only available on Windows")
}
