//go:build !linux

package main

import "errors"

func guardCLIIdentity([]string) error { return nil }
func validateServiceMode() error      { return errors.New("web --service is supported on Linux only") }
