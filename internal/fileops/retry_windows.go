package fileops

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func retrySharing(action func() error) error {
	for attempt := 0; ; attempt++ {
		err := action()
		if err == nil || attempt == 9 || !errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
			!errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
