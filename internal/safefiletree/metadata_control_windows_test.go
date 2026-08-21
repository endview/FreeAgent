//go:build windows

package safefiletree

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func changeTestPermissionMetadata(descriptor uintptr) error {
	return setTestPermissionMetadata(descriptor, windows.GENERIC_ALL)
}

func changeTestPermissionMetadataAfterChangeTick(descriptor uintptr) error {
	before, err := testPermissionMetadataChangeTime(descriptor)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := setTestPermissionMetadata(descriptor, windows.GENERIC_READ); err != nil {
			return err
		}
		if err := setTestPermissionMetadata(descriptor, windows.GENERIC_ALL); err != nil {
			return err
		}
		after, err := testPermissionMetadataChangeTime(descriptor)
		if err != nil {
			return err
		}
		if after != before {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("permission metadata change time did not advance")
		}
		time.Sleep(time.Millisecond)
	}
}

func setTestPermissionMetadata(descriptor uintptr, permissions windows.ACCESS_MASK) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(
		windows.Handle(descriptor), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

func testPermissionMetadataChangeTime(descriptor uintptr) (int64, error) {
	var basic windowsFileBasicInfo
	err := windows.GetFileInformationByHandleEx(
		windows.Handle(descriptor),
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)),
		uint32(unsafe.Sizeof(basic)),
	)
	return basic.ChangeTime, err
}

func changeTestContent(descriptor uintptr) error {
	if _, err := windows.SetFilePointer(
		windows.Handle(descriptor), 0, nil, windows.FILE_BEGIN,
	); err != nil {
		return err
	}
	var written uint32
	return windows.WriteFile(
		windows.Handle(descriptor), []byte("after!"), &written, nil,
	)
}

func changeTestSize(descriptor uintptr) error {
	if _, err := windows.SetFilePointer(
		windows.Handle(descriptor), 1, nil, windows.FILE_BEGIN,
	); err != nil {
		return err
	}
	return windows.SetEndOfFile(windows.Handle(descriptor))
}

func changeTestModificationTime(descriptor uintptr) error {
	stamp := windows.NsecToFiletime(time.Now().Add(time.Hour).UnixNano())
	return windows.SetFileTime(windows.Handle(descriptor), nil, nil, &stamp)
}
