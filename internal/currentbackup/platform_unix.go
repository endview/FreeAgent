//go:build !windows

package currentbackup

import (
	"fmt"
	"os"
	"reflect"

	"golang.org/x/sys/unix"
)

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
