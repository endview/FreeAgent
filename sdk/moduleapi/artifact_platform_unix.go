//go:build !windows

package moduleapi

import (
	"errors"
	"fmt"
	"os"
	"reflect"
)

func rejectArtifactPlatformSpecial(os.FileInfo) error {
	return nil
}

func rejectArtifactFilesystemBoundary(root, entry os.FileInfo) error {
	rootDevice, err := artifactFileInfoUintField(root, "Dev", "device")
	if err != nil {
		return err
	}
	entryDevice, err := artifactFileInfoUintField(entry, "Dev", "device")
	if err != nil {
		return err
	}
	if entryDevice != rootDevice {
		return errors.New("filesystem boundaries are not allowed")
	}
	return nil
}

func artifactFileInfoUintField(
	info os.FileInfo,
	fieldName string,
	label string,
) (uint64, error) {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, errors.New("file metadata is unavailable")
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, fmt.Errorf("file metadata has no %s", label)
	}
	field := value.FieldByName(fieldName)
	if !field.IsValid() {
		return 0, fmt.Errorf("file metadata has no %s", label)
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64:
		return field.Uint(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64:
		count := field.Int()
		if count < 0 {
			return 0, fmt.Errorf("file metadata has a negative %s", label)
		}
		return uint64(count), nil
	default:
		return 0, fmt.Errorf("file metadata %s has an unknown type", label)
	}
}
