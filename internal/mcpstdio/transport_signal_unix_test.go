//go:build unix

package mcpstdio

import (
	"os/signal"
	"syscall"
)

func ignoreTerminationForTest() {
	signal.Ignore(syscall.SIGTERM)
}
