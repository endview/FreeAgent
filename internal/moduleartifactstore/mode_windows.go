//go:build windows

package moduleartifactstore

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/endview/freeagent/internal/safefiletree"
	"golang.org/x/sys/windows"
)

func artifactRootPrivateV1(path string, current os.FileInfo) bool {
	return current != nil && current.IsDir() && artifactPathPrivateV1(path, current)
}

func artifactPathPrivateV1(path string, current os.FileInfo) bool {
	return artifactPathSecurityV1(path, current, false)
}

func artifactAncestryPathTrustedV1(path string, current os.FileInfo) bool {
	return artifactPathSecurityV1(path, current, true)
}

func artifactPathSecurityV1(path string, current os.FileInfo, ancestry bool) bool {
	if current == nil {
		return false
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return false
	}
	file := os.NewFile(uintptr(handle), "")
	if file == nil {
		windows.CloseHandle(handle)
		return false
	}
	defer file.Close()
	if !artifactOpenedSecurityV1(file, current, ancestry) {
		return false
	}
	afterPath, pathErr := os.Lstat(path)
	afterOpened, openedErr := file.Stat()
	return pathErr == nil && openedErr == nil && os.SameFile(current, afterPath) &&
		os.SameFile(current, afterOpened) && afterPath.IsDir() == current.IsDir() &&
		afterPath.Mode()&os.ModeSymlink == 0 && !pathIsReparsePointV1(afterPath)
}

func artifactOpenedPathPrivateV1(file *os.File, current os.FileInfo) bool {
	return artifactOpenedSecurityV1(file, current, false)
}

func artifactOpenedSecurityV1(file *os.File, current os.FileInfo, ancestry bool) bool {
	if file == nil || current == nil {
		return false
	}
	opened, err := file.Stat()
	if err != nil || opened.IsDir() != current.IsDir() || opened.Mode()&os.ModeSymlink != 0 ||
		pathIsReparsePointV1(opened) || !os.SameFile(current, opened) {
		return false
	}
	return artifactOpenedHandleSecurityV1(windows.Handle(file.Fd()), ancestry)
}

func artifactOpenedHandleSecurityV1(handle windows.Handle, ancestry bool) bool {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil {
		return false
	}
	owner, defaulted, err := descriptor.Owner()
	if err != nil || defaulted || owner == nil || !owner.IsValid() {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	trustedInstaller, err := windows.StringToSid(
		"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464",
	)
	if err != nil || trustedInstaller == nil {
		return false
	}
	ownerTrusted := owner.Equals(user.User.Sid)
	if ancestry {
		ownerTrusted = ownerTrusted || owner.Equals(system) || owner.Equals(administrators) ||
			owner.Equals(trustedInstaller)
	}
	if !ownerTrusted {
		return false
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil || defaulted || dacl == nil {
		return false
	}
	const untrustedLeafWriteMask = uint32(
		windows.GENERIC_ALL | windows.GENERIC_WRITE |
			windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
			0x40 | windows.FILE_WRITE_ATTRIBUTES |
			windows.FILE_WRITE_EA | windows.FILE_WRITE_DATA |
			windows.FILE_APPEND_DATA,
	)
	const untrustedAncestryTakeoverMask = uint32(
		windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.DELETE |
			windows.WRITE_DAC | windows.WRITE_OWNER | 0x40, // 0x40 is FILE_DELETE_CHILD.
	)
	forbiddenMask := untrustedLeafWriteMask
	if ancestry {
		forbiddenMask = untrustedAncestryTakeoverMask
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil ||
			ace == nil || ace.Mask == 0 {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid == nil || !sid.IsValid() {
			return false
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			trusted := sid.Equals(user.User.Sid) || sid.Equals(system) ||
				sid.Equals(administrators) || (ancestry && sid.Equals(trustedInstaller))
			if !trusted && uint32(ace.Mask)&forbiddenMask != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func artifactWalkedEntryPrivateV1(entry safefiletree.Entry) bool {
	if entry.Info == nil {
		return false
	}
	private := false
	err := entry.Control(func(descriptor uintptr) error {
		private = artifactOpenedHandleSecurityV1(windows.Handle(descriptor), false)
		return nil
	})
	return err == nil && private
}

func artifactWalkedLeaseFilePrivateV1(entry safefiletree.Entry) bool {
	return entry.Kind == safefiletree.KindRegularFile && entry.Info != nil &&
		entry.Info.Size() == 0 && artifactWalkedEntryPrivateV1(entry)
}

func artifactSameFilesystemV1(os.FileInfo, os.FileInfo) bool { return true }

func artifactOpenedSingleLinkV1(file *os.File) bool {
	if file == nil {
		return false
	}
	var information windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information) == nil &&
		information.NumberOfLinks == 1
}

func artifactOpenedLeaseFilePrivateV1(file *os.File, expected os.FileInfo) bool {
	return file != nil && expected != nil && expected.Mode().IsRegular() &&
		artifactOpenedPathPrivateV1(file, expected) && artifactOpenedSingleLinkV1(file)
}

// Every ancestor through the volume root must have a trusted owner and must
// deny namespace-takeover rights to untrusted principals. Checking only the
// immediate state parent would still allow a writable grandparent to replace
// the complete selected tree between verification and use.
func artifactRootAncestryPrivateV1(path string) bool {
	childPath := filepath.Clean(path)
	for {
		parentPath := filepath.Dir(childPath)
		if parentPath == childPath {
			return true
		}
		parent, err := os.Lstat(parentPath)
		if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
			pathIsReparsePointV1(parent) || !artifactAncestryPathTrustedV1(parentPath, parent) {
			return false
		}
		childPath = parentPath
	}
}

func artifactProvisionParentPrivateV1(path string, current os.FileInfo) bool {
	return current != nil && current.IsDir() && artifactPathPrivateV1(path, current)
}

func provisionArtifactPathPrivateV1(path string) error {
	return provisionArtifactDACLPrivateV1(path)
}

func provisionArtifactLeaseFilePrivateV1(path string) error {
	return provisionArtifactDACLPrivateV1(path)
}

func provisionArtifactDACLPrivateV1(path string) error {
	acl, err := privateArtifactDACLCurrentUserV1()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

func privateArtifactDACLCurrentUserV1() (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return nil, windows.ERROR_INVALID_OWNER
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{user.User.Sid, system, administrators} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return nil, err
	}
	return acl, nil
}

func artifactLeasePathKeyV1(path string) string { return strings.ToLower(path) }

func artifactExecutableModeObservableV1() bool { return false }

// Go's portable FileMode cannot express Windows ACLs. Reparse points are
// rejected separately; the server-owned root ACL remains the Windows boundary.
func artifactModeCandidateV1(info os.FileInfo) (bool, bool) {
	return false, info.IsDir() || info.Mode().IsRegular()
}

type artifactInstalledModeNormalizerV1 struct {
	acl *windows.ACL
}

func newArtifactInstalledModeNormalizerV1() (artifactInstalledModeNormalizerV1, error) {
	acl, err := privateArtifactDACLCurrentUserV1()
	if err != nil {
		return artifactInstalledModeNormalizerV1{}, err
	}
	return artifactInstalledModeNormalizerV1{acl: acl}, nil
}

func (normalizer artifactInstalledModeNormalizerV1) normalizeV1(
	entry safefiletree.Entry,
	_ bool,
) error {
	if normalizer.acl == nil || entry.Info == nil ||
		(entry.Kind != safefiletree.KindDirectory &&
			entry.Kind != safefiletree.KindRegularFile) {
		return windows.ERROR_INVALID_PARAMETER
	}
	return entry.ControlMetadata(func(descriptor uintptr) error {
		return windows.SetSecurityInfo(
			windows.Handle(descriptor), windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil, nil, normalizer.acl, nil,
		)
	})
}
