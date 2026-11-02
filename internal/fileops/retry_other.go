//go:build !windows

package fileops

func retrySharing(action func() error) error { return action() }
