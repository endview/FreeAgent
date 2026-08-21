//go:build !windows && !linux && !darwin

package main

import "errors"

func publishInitNoReplace(_, _ string) error {
	return errors.New("composition: this OS has no atomic no-replace publication primitive")
}

func syncInitDirectory(string) error { return nil }
