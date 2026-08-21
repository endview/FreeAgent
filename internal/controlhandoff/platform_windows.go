//go:build windows

package controlhandoff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platformFileIdentityV1 struct {
	volumeSerial uint32
	indexHigh    uint32
	indexLow     uint32
	size         uint32
}

const inheritedACEFlagV1 = 0x10

const (
	windowsElevationTypeDefaultV1 uint32 = 1
	windowsElevationTypeFullV1    uint32 = 2
	windowsElevationTypeLimitedV1 uint32 = 3
	maximumWindowsGroupsV1               = 4096
)

type windowsPrivilegeEvidenceV1 struct {
	elevationVerified          bool
	elevated                   bool
	elevationTypeVerified      bool
	elevationType              uint32
	administratorGroupVerified bool
	administratorGroupPresent  bool
	userVerified               bool
}

func verifyNonElevatedPlatformV1() error {
	return decideWindowsPrivilegeV1(
		collectWindowsPrivilegeEvidenceV1(windows.GetCurrentProcessToken()),
	)
}

func collectWindowsPrivilegeEvidenceV1(
	token windows.Token,
) windowsPrivilegeEvidenceV1 {
	elevation, elevationVerified := queryWindowsTokenUint32V1(
		token,
		windows.TokenElevation,
	)
	elevationType, elevationTypeVerified := queryWindowsTokenUint32V1(
		token,
		windows.TokenElevationType,
	)
	administrators, administratorSIDErr := windows.CreateWellKnownSid(
		windows.WinBuiltinAdministratorsSid,
	)
	groups, groupsErr := token.GetTokenGroups()
	administratorGroupVerified := false
	administratorGroupPresent := false
	if administratorSIDErr == nil && groupsErr == nil {
		administratorGroupVerified, administratorGroupPresent =
			inspectWindowsAdministratorGroupV1(groups, administrators)
	}
	user, userErr := token.GetTokenUser()
	userVerified := userErr == nil && user != nil && user.User.Sid != nil &&
		user.User.Sid.IsValid()
	return windowsPrivilegeEvidenceV1{
		elevationVerified:          elevationVerified,
		elevated:                   elevation != 0,
		elevationTypeVerified:      elevationTypeVerified,
		elevationType:              elevationType,
		administratorGroupVerified: administratorGroupVerified,
		administratorGroupPresent:  administratorGroupPresent,
		userVerified:               userVerified,
	}
}

func queryWindowsTokenUint32V1(
	token windows.Token,
	informationClass uint32,
) (uint32, bool) {
	var value uint32
	var returned uint32
	err := windows.GetTokenInformation(
		token,
		informationClass,
		(*byte)(unsafe.Pointer(&value)),
		uint32(unsafe.Sizeof(value)),
		&returned,
	)
	return value, err == nil && returned == uint32(unsafe.Sizeof(value))
}

func inspectWindowsAdministratorGroupV1(
	groups *windows.Tokengroups,
	administrators *windows.SID,
) (verified bool, present bool) {
	if groups == nil || administrators == nil || !administrators.IsValid() ||
		groups.GroupCount > maximumWindowsGroupsV1 {
		return false, false
	}
	for _, group := range groups.AllGroups() {
		if group.Sid == nil || !group.Sid.IsValid() ||
			group.Attributes & ^uint32(windows.SE_GROUP_VALID_ATTRIBUTES) != 0 {
			return false, false
		}
		// Presence is decisive regardless of attributes. In particular, a
		// filtered UAC administrator token carries this SID with
		// SE_GROUP_USE_FOR_DENY_ONLY, for which Token.IsMember reports false.
		if group.Sid.Equals(administrators) {
			present = true
		}
	}
	return true, present
}

func decideWindowsPrivilegeV1(evidence windowsPrivilegeEvidenceV1) error {
	if !evidence.elevationVerified || evidence.elevated ||
		!evidence.elevationTypeVerified ||
		!evidence.administratorGroupVerified ||
		evidence.administratorGroupPresent || !evidence.userVerified {
		return ErrElevatedProcess
	}
	// Only a default token with no Administrators SID is demonstrably an
	// ordinary user token. Full and Limited are the two halves of a split
	// administrator token; unknown future values fail closed.
	if evidence.elevationType != windowsElevationTypeDefaultV1 {
		return ErrElevatedProcess
	}
	return nil
}

func validatePlatformPathV1(path string, _ bool) error {
	volume := filepath.VolumeName(path)
	if volume == "" || strings.HasPrefix(path, `\\`) ||
		strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) ||
		len(volume) != 2 || volume[1] != ':' ||
		strings.Contains(path[len(volume):], ":") {
		return ErrInvalidInput
	}
	return nil
}

func ensurePrivateRuntimeDirectoryPlatformV1(path string) (bool, error) {
	if err := verifyFixedLocalPathWindowsV1(filepath.Dir(path)); err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return false, verifyPrivateDirectoryPlatformV1(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, ErrUnsafeParent
	}
	parent := filepath.Dir(path)
	if err := verifyOrdinaryLocalDirectoryWindowsV1(parent, false); err != nil {
		return false, err
	}
	security, err := currentUserOnlySecurityWindowsV1()
	if err != nil {
		return false, ErrSecurityVerification
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, ErrInvalidInput
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: security,
	}
	if err := windows.CreateDirectory(pointer, attributes); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return false, ErrTargetExists
		}
		return false, ErrUnsafeParent
	}
	if err := verifyPrivateDirectoryPlatformV1(path); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}

func verifyPrivateDirectoryPlatformV1(path string) error {
	return verifyOrdinaryLocalDirectoryWindowsV1(path, true)
}

func verifyOrdinaryLocalDirectoryWindowsV1(path string, private bool) error {
	if err := verifyFixedLocalPathWindowsV1(path); err != nil {
		return err
	}
	if err := stableExistingPathWindowsV1(path); err != nil {
		return ErrUnsafeParent
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ErrUnsafeParent
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return ErrUnsafeParent
	}
	defer windows.CloseHandle(handle)
	if err := verifyLocalDiskHandleWindowsV1(handle); err != nil {
		return err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafeParent
	}
	if private {
		if err := verifyCurrentUserOnlyHandleWindowsV1(handle); err != nil {
			return ErrUnsafeParent
		}
	}
	return nil
}

func writeExclusivePrivateFilePlatformV1(
	path string,
	canonical []byte,
) (identity platformFileIdentityV1, returnErr error) {
	return writeExclusivePrivateFileWithCloseWindowsV1(
		path,
		canonical,
		func(file *os.File) error { return file.Close() },
	)
}

func writeExclusivePrivateFileWithCloseWindowsV1(
	path string,
	canonical []byte,
	closeFile func(*os.File) error,
) (identity platformFileIdentityV1, returnErr error) {
	if closeFile == nil {
		return identity, ErrSecurityVerification
	}
	if err := verifyFixedLocalPathWindowsV1(filepath.Dir(path)); err != nil {
		return identity, err
	}
	if _, err := os.Lstat(path); err == nil {
		return identity, ErrTargetExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return identity, ErrSecurityVerification
	}
	parent := filepath.Dir(path)
	if err := verifyPrivateDirectoryPlatformV1(parent); err != nil {
		return identity, err
	}
	security, err := currentUserOnlySecurityWindowsV1()
	if err != nil {
		return identity, ErrSecurityVerification
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return identity, ErrInvalidInput
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: security,
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|
			windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|
			windows.FILE_FLAG_WRITE_THROUGH,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return identity, ErrTargetExists
		}
		return identity, ErrSecurityVerification
	}
	file := os.NewFile(uintptr(handle), "")
	if file == nil {
		_ = windows.CloseHandle(handle)
		return identity, ErrSecurityVerification
	}
	committed := false
	fileOpen := true
	identityKnown := false
	cleanupIdentity := platformFileIdentityV1{}
	defer func() {
		if !committed && fileOpen {
			if current, err := inspectPrivateFileHandleWindowsV1(handle); err == nil &&
				identityKnown && sameWindowsFileObjectV1(current, cleanupIdentity) {
				cleanupIdentity = current
			}
			// Marking by handle avoids a pathname race. Failure is not the
			// only cleanup path: after close, exact object identity is checked
			// again before any pathname removal.
			_ = setDeleteDispositionWindowsV1(handle, true)
		}
		if fileOpen {
			if err := closeFile(file); err != nil && returnErr == nil {
				_ = windows.CloseHandle(handle)
				returnErr = ErrSecurityVerification
			}
			fileOpen = false
		}
		if !committed && identityKnown {
			if err := removeExactCreatedFileWindowsV1(
				path,
				cleanupIdentity,
			); err != nil && returnErr == nil {
				returnErr = ErrSecurityVerification
			}
		}
	}()
	// Establish an identity for the empty, CREATE_NEW leaf before any
	// capability bytes are written. Every later error can therefore either
	// rely on delete-on-close or remove this exact identity after close.
	emptyIdentity, err := verifyPrivateFileHandleWindowsV1(handle, 0)
	if err != nil {
		return platformFileIdentityV1{}, err
	}
	identity = emptyIdentity
	cleanupIdentity = emptyIdentity
	identityKnown = true
	written, err := file.Write(canonical)
	if err != nil || written != len(canonical) || file.Sync() != nil {
		return identity, ErrSecurityVerification
	}
	identity, err = verifyPrivateFileHandleWindowsV1(handle, uint32(len(canonical)))
	if err != nil {
		return platformFileIdentityV1{}, err
	}
	if identity.volumeSerial != emptyIdentity.volumeSerial ||
		identity.indexHigh != emptyIdentity.indexHigh ||
		identity.indexLow != emptyIdentity.indexLow {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	cleanupIdentity = identity
	if err := verifyPrivateDirectoryPlatformV1(parent); err != nil {
		return platformFileIdentityV1{}, err
	}
	pathIdentity, err := inspectPrivateFileWindowsV1(path, uint32(len(canonical)))
	if err != nil || pathIdentity != identity {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	if err := closeFile(file); err != nil {
		_ = windows.CloseHandle(handle)
		fileOpen = false
		return identity, ErrSecurityVerification
	}
	fileOpen = false
	pathIdentity, err = inspectPrivateFileWindowsV1(path, uint32(len(canonical)))
	if err != nil || pathIdentity != identity {
		return identity, ErrIdentityChanged
	}
	committed = true
	return identity, nil
}

func inspectPrivateFileWindowsV1(
	path string,
	size uint32,
) (platformFileIdentityV1, error) {
	if err := verifyFixedLocalPathWindowsV1(filepath.Dir(path)); err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	if err := stableExistingPathWindowsV1(path); err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	defer windows.CloseHandle(handle)
	return verifyPrivateFileHandleWindowsV1(handle, size)
}

func verifyPrivateFileHandleWindowsV1(
	handle windows.Handle,
	size uint32,
) (platformFileIdentityV1, error) {
	identity, err := inspectPrivateFileHandleWindowsV1(handle)
	if err != nil || identity.size != size {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	return identity, nil
}

func inspectPrivateFileHandleWindowsV1(
	handle windows.Handle,
) (platformFileIdentityV1, error) {
	if err := verifyLocalDiskHandleWindowsV1(handle); err != nil {
		return platformFileIdentityV1{}, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		information.NumberOfLinks != 1 || information.FileSizeHigh != 0 {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	if err := verifyCurrentUserOnlyHandleWindowsV1(handle); err != nil {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	return platformFileIdentityV1{
		volumeSerial: information.VolumeSerialNumber,
		indexHigh:    information.FileIndexHigh,
		indexLow:     information.FileIndexLow,
		size:         information.FileSizeLow,
	}, nil
}

func removeExactPrivateFilePlatformV1(
	path string,
	identity platformFileIdentityV1,
) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrIdentityChanged
	}
	current, err := inspectPrivateFileWindowsV1(path, identity.size)
	if err != nil || current != identity {
		return ErrIdentityChanged
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrSecurityVerification
	}
	return nil
}

func removeExactCreatedFileWindowsV1(
	path string,
	identity platformFileIdentityV1,
) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrIdentityChanged
	}
	current, err := inspectPrivateFileWindowsV1AnySizeV1(path)
	if err != nil || !sameWindowsFileObjectV1(current, identity) {
		return ErrIdentityChanged
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrSecurityVerification
	}
	return nil
}

func inspectPrivateFileWindowsV1AnySizeV1(
	path string,
) (platformFileIdentityV1, error) {
	if err := verifyFixedLocalPathWindowsV1(filepath.Dir(path)); err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	if err := stableExistingPathWindowsV1(path); err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	defer windows.CloseHandle(handle)
	return inspectPrivateFileHandleWindowsV1(handle)
}

func sameWindowsFileObjectV1(left, right platformFileIdentityV1) bool {
	return left.volumeSerial == right.volumeSerial &&
		left.indexHigh == right.indexHigh && left.indexLow == right.indexLow
}

func removeEmptyDirectoryPlatformV1(path string) {
	if verifyPrivateDirectoryPlatformV1(path) == nil {
		_ = os.Remove(path)
	}
}

func stableExistingPathWindowsV1(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeParent
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
		return ErrUnsafeParent
	}
	return nil
}

func verifyFixedLocalPathWindowsV1(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ErrUnsafeParent
	}
	volumePath := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(
		pointer,
		&volumePath[0],
		uint32(len(volumePath)),
	); err != nil {
		return ErrUnsafeParent
	}
	root := windows.UTF16ToString(volumePath)
	rootPointer, err := windows.UTF16PtrFromString(root)
	if err != nil || !isFixedLocalDriveTypeWindowsV1(
		windows.GetDriveType(rootPointer),
	) {
		return ErrUnsafeParent
	}
	return nil
}

func isFixedLocalDriveTypeWindowsV1(driveType uint32) bool {
	return driveType == windows.DRIVE_FIXED
}

func setDeleteDispositionWindowsV1(
	handle windows.Handle,
	deleteFile bool,
) error {
	deleteValue := struct{ DeleteFile bool }{DeleteFile: deleteFile}
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&deleteValue)),
		uint32(unsafe.Sizeof(deleteValue)),
	)
}

func verifyLocalDiskHandleWindowsV1(handle windows.Handle) error {
	typeValue, err := windows.GetFileType(handle)
	if err != nil || typeValue != windows.FILE_TYPE_DISK {
		return ErrUnsafeParent
	}
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(
		handle,
		nil,
		0,
		nil,
		nil,
		&flags,
		nil,
		0,
	); err != nil || flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return ErrUnsafeParent
	}
	return nil
}

func currentUserSIDWindowsV1() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil ||
		!user.User.Sid.IsValid() {
		return nil, ErrSecurityVerification
	}
	return user.User.Sid.Copy()
}

func currentUserOnlySecurityWindowsV1() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUserSIDWindowsV1()
	if err != nil {
		return nil, err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}, nil)
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	if err := descriptor.SetOwner(user, false); err != nil {
		return nil, err
	}
	if err := descriptor.SetDACL(acl, true, false); err != nil {
		return nil, err
	}
	if err := descriptor.SetControl(
		windows.SE_DACL_PROTECTED,
		windows.SE_DACL_PROTECTED,
	); err != nil {
		return nil, err
	}
	return descriptor.ToSelfRelative()
}

func verifyCurrentUserOnlyHandleWindowsV1(handle windows.Handle) error {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil {
		return ErrSecurityVerification
	}
	owner, defaulted, err := descriptor.Owner()
	if err != nil || defaulted || owner == nil {
		return ErrSecurityVerification
	}
	user, err := currentUserSIDWindowsV1()
	if err != nil || !owner.Equals(user) {
		return ErrSecurityVerification
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrSecurityVerification
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil || defaulted || dacl == nil || dacl.AceCount == 0 {
		return ErrSecurityVerification
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil ||
			ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&inheritedACEFlagV1 != 0 {
			return ErrSecurityVerification
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid == nil || !sid.IsValid() || !sid.Equals(user) || ace.Mask == 0 {
			return ErrSecurityVerification
		}
	}
	return nil
}
