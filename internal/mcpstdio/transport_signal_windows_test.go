//go:build windows

package mcpstdio

import (
	"os"
	"os/signal"
)

func ignoreTerminationForTest() {
	signal.Ignore(os.Interrupt)
}
