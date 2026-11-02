//go:build !linux

package harness

func validatePlatformNetwork(Config, string) error { return nil }
