//go:build windows

package controlhandoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivilegeDecisionFailsClosedV1(t *testing.T) {
	tests := []struct {
		name     string
		evidence windowsPrivilegeEvidenceV1
		wantErr  bool
	}{
		{
			name: "ordinary",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeDefaultV1,
				administratorGroupVerified: true,
				userVerified:               true,
			},
		},
		{
			name: "elevated flag",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevated:                   true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeFullV1,
				administratorGroupVerified: true,
				administratorGroupPresent:  true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "filtered administrator SID is deny-only",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeLimitedV1,
				administratorGroupVerified: true,
				administratorGroupPresent:  true,
				administratorGroupDenyOnly: true,
				userVerified:               true,
			},
		},
		{
			name: "limited token even if group evidence omits administrator",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeLimitedV1,
				administratorGroupVerified: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "limited token with enabled administrator SID",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeLimitedV1,
				administratorGroupVerified: true,
				administratorGroupPresent:  true,
				administratorGroupEnabled:  true,
				administratorGroupDenyOnly: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "limited token with administrator SID not marked deny-only",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeLimitedV1,
				administratorGroupVerified: true,
				administratorGroupPresent:  true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "default token with administrator SID",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeDefaultV1,
				administratorGroupVerified: true,
				administratorGroupPresent:  true,
				administratorGroupDenyOnly: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "full token even if elevation flag is inconsistent",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeFullV1,
				administratorGroupVerified: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "unknown elevation type",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              99,
				administratorGroupVerified: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "elevation unknown",
			evidence: windowsPrivilegeEvidenceV1{
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeDefaultV1,
				administratorGroupVerified: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "elevation type unknown",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationType:              windowsElevationTypeDefaultV1,
				administratorGroupVerified: true,
				userVerified:               true,
			},
			wantErr: true,
		},
		{
			name: "groups unknown",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:     true,
				elevationTypeVerified: true,
				elevationType:         windowsElevationTypeDefaultV1,
				userVerified:          true,
			},
			wantErr: true,
		},
		{
			name: "user unknown",
			evidence: windowsPrivilegeEvidenceV1{
				elevationVerified:          true,
				elevationTypeVerified:      true,
				elevationType:              windowsElevationTypeDefaultV1,
				administratorGroupVerified: true,
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := decideWindowsPrivilegeV1(test.evidence)
			if errors.Is(err, ErrElevatedProcess) != test.wantErr {
				t.Fatalf("privilege decision error = %v", err)
			}
		})
	}
}

func TestWindowsAdministratorGroupInspectionIncludesDenyOnlyV1(t *testing.T) {
	administrators, err := windows.CreateWellKnownSid(
		windows.WinBuiltinAdministratorsSid,
	)
	if err != nil {
		t.Fatal(err)
	}
	groups := &windows.Tokengroups{GroupCount: 1}
	groups.Groups[0] = windows.SIDAndAttributes{
		Sid:        administrators,
		Attributes: windows.SE_GROUP_USE_FOR_DENY_ONLY,
	}
	denyOnly := inspectWindowsAdministratorGroupV1(
		groups,
		administrators,
	)
	if !denyOnly.verified || !denyOnly.present || denyOnly.enabled || !denyOnly.denyOnly {
		t.Fatalf("deny-only administrator group = %+v", denyOnly)
	}

	groups.Groups[0].Attributes = windows.SE_GROUP_ENABLED
	enabled := inspectWindowsAdministratorGroupV1(groups, administrators)
	if !enabled.verified || !enabled.present || !enabled.enabled || enabled.denyOnly {
		t.Fatalf("enabled administrator group = %+v", enabled)
	}

	groups.Groups[0].Sid = nil
	invalid := inspectWindowsAdministratorGroupV1(groups, administrators)
	if invalid.verified || invalid.present {
		t.Fatalf("invalid group evidence = %+v", invalid)
	}
}

func TestWindowsCurrentTokenEvidenceIsReadableV1(t *testing.T) {
	evidence := collectWindowsPrivilegeEvidenceV1(windows.GetCurrentProcessToken())
	if !evidence.elevationVerified || !evidence.elevationTypeVerified ||
		!evidence.administratorGroupVerified || !evidence.userVerified {
		t.Fatalf("current token evidence is incomplete: %+v", evidence)
	}
	if evidence.elevationType < windowsElevationTypeDefaultV1 ||
		evidence.elevationType > windowsElevationTypeLimitedV1 {
		t.Fatalf("current token elevation type = %d", evidence.elevationType)
	}
	if evidence.elevationType == windowsElevationTypeLimitedV1 &&
		(!evidence.administratorGroupPresent || evidence.administratorGroupEnabled ||
			!evidence.administratorGroupDenyOnly) {
		t.Fatalf("inconsistent limited-token evidence: %+v", evidence)
	}
}

func TestWindowsPrivateDirectoryACLRejectsOtherPrincipalV1(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	directory := testPrivateRuntimeDirectoryV1(t)
	privateSecurity, err := currentUserOnlySecurityWindowsV1()
	if err != nil {
		t.Fatal(err)
	}
	privateDACL := mustWindowsDACLV1(t, privateSecurity)
	pointer, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		restoreErr := windows.SetSecurityInfo(
			handle,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|
				windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil,
			nil,
			privateDACL,
			nil,
		)
		_ = windows.CloseHandle(handle)
		if restoreErr != nil {
			t.Errorf("restore private DACL: %v", restoreErr)
		}
	}()
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(world),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDirectoryPlatformV1(directory); !errors.Is(err, ErrUnsafeParent) {
		t.Fatalf("other-principal ACL error = %v", err)
	}
}

func TestWindowsPrivateFileRejectsHardLinkV1(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	directory := testPrivateRuntimeDirectoryV1(t)
	path := filepath.Join(directory, "one")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	security, err := currentUserOnlySecurityWindowsV1()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		mustWindowsOwnerV1(t, security),
		nil,
		mustWindowsDACLV1(t, security),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "two")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	if _, err := inspectPrivateFileWindowsV1(path, 1); !errors.Is(err, ErrSecurityVerification) {
		t.Fatalf("hardlinked leaf error = %v", err)
	}
}

func mustWindowsOwnerV1(
	t *testing.T,
	descriptor *windows.SECURITY_DESCRIPTOR,
) *windows.SID {
	t.Helper()
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func mustWindowsDACLV1(
	t *testing.T,
	descriptor *windows.SECURITY_DESCRIPTOR,
) *windows.ACL {
	t.Helper()
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	return dacl
}

func TestDeleteOnCloseStructureSizeV1(t *testing.T) {
	value := struct{ DeleteFile bool }{DeleteFile: true}
	if unsafe.Sizeof(value) == 0 {
		t.Fatal("zero FILE_DISPOSITION_INFO stand-in")
	}
}

func TestWindowsDriveTypeGateAcceptsOnlyFixedV1(t *testing.T) {
	for _, driveType := range []uint32{
		windows.DRIVE_UNKNOWN,
		windows.DRIVE_NO_ROOT_DIR,
		windows.DRIVE_REMOVABLE,
		windows.DRIVE_REMOTE,
		windows.DRIVE_CDROM,
		windows.DRIVE_RAMDISK,
	} {
		if isFixedLocalDriveTypeWindowsV1(driveType) {
			t.Fatalf("non-fixed drive type %d was accepted", driveType)
		}
	}
	if !isFixedLocalDriveTypeWindowsV1(windows.DRIVE_FIXED) {
		t.Fatal("fixed drive was rejected")
	}
}

func TestWindowsCloseFailureRemovesExactCreatedLeafV1(t *testing.T) {
	if err := VerifyNonElevatedV1(); err != nil {
		t.Skipf("current test process is intentionally ineligible: %v", err)
	}
	directory := testPrivateRuntimeDirectoryV1(t)
	path := filepath.Join(directory, "close-failure.json")
	_, err := writeExclusivePrivateFileWithCloseWindowsV1(
		path,
		[]byte(`{"bounded":"canonical"}`),
		func(file *os.File) error {
			closeErr := file.Close()
			if closeErr != nil {
				return closeErr
			}
			return errors.New("injected close report")
		},
	)
	if !errors.Is(err, ErrSecurityVerification) {
		t.Fatalf("injected close error = %v", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("close failure left capability leaf: %v", statErr)
	}
}
