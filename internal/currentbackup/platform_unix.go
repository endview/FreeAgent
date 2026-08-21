//go:build !windows

package currentbackup

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"golang.org/x/sys/unix"
)

type offlineFence struct {
	file *os.File
}

func acquireOfflineFence(databasePath string) (*offlineFence, error) {
	file, err := os.OpenFile(
		databasePath+".freeagent.owner.lock",
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: open owner fence: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrSourceActive, databasePath)
		}
		return nil, fmt.Errorf("currentbackup: lock owner fence: %w", err)
	}
	return &offlineFence{file: file}, nil
}

func (fence *offlineFence) close() error {
	if fence == nil || fence.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(fence.file.Fd()), unix.LOCK_UN)
	closeErr := fence.file.Close()
	fence.file = nil
	return errors.Join(unlockErr, closeErr)
}

func openedFileLinkCount(file *os.File) (uint64, error) {
	var information unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &information); err != nil {
		return 0, fmt.Errorf("currentbackup: inspect file identity: %w", err)
	}
	return uint64(information.Nlink), nil
}

func rejectSafeTreePlatformSpecial(os.FileInfo) error { return nil }

func rejectSafeTreeFilesystemBoundary(root, entry os.FileInfo) error {
	rootDevice, err := safeTreeFileInfoDevice(root)
	if err != nil {
		return err
	}
	entryDevice, err := safeTreeFileInfoDevice(entry)
	if err != nil {
		return err
	}
	if entryDevice != rootDevice {
		return fmt.Errorf("%w: filesystem boundaries are forbidden", ErrIntegrity)
	}
	return nil
}

func safeTreeFileInfoDevice(info os.FileInfo) (uint64, error) {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, fmt.Errorf("%w: file metadata is unavailable", ErrIntegrity)
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, fmt.Errorf("%w: file metadata has no device", ErrIntegrity)
	}
	field := value.FieldByName("Dev")
	if !field.IsValid() {
		return 0, fmt.Errorf("%w: file metadata has no device", ErrIntegrity)
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		device := field.Int()
		if device < 0 {
			return 0, fmt.Errorf("%w: file metadata has a negative device", ErrIntegrity)
		}
		return uint64(device), nil
	default:
		return 0, fmt.Errorf("%w: file metadata device has an unknown type", ErrIntegrity)
	}
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
