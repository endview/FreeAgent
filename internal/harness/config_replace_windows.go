package harness

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// A concurrent config reader may briefly deny destination replacement on
// Windows. Keep the complete old file and retry only transient sharing/access
// errors; never remove the destination or retry indefinitely.
func publishConfigFile(source, target string) error {
	for attempt := 0; ; attempt++ {
		err := os.Rename(source, target)
		if err == nil || attempt == 9 || !errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
			!errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
