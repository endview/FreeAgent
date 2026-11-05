package channelplugin

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func lockConnection(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("channel: connection lock unavailable")
	}
	var lock windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock); err != nil {
		f.Close()
		return nil, errors.New("channel: another process already owns this platform account")
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &lock); _ = f.Close() }, nil
}
