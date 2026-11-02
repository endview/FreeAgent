//go:build !windows

package harness

import "os"

func publishConfigFile(source, target string) error { return os.Rename(source, target) }
