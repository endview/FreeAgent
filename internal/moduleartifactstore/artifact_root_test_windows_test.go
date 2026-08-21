//go:build windows

package moduleartifactstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
	"golang.org/x/sys/windows"
)

func privateArtifactTempDirV1(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp(home, ".freeagent-artifactstore-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	setArtifactRootDACLForTestV1(t, parent, 0)
	path := filepath.Join(parent, "artifacts")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	setArtifactRootDACLForTestV1(t, path, 0)
	return path
}

func TestArtifactRootPrivateV1AcceptsReadOnlyUntrustedAndRejectsWritableUntrusted(t *testing.T) {
	root := privateArtifactTempDirV1(t)
	setArtifactRootDACLForTestV1(t, root, windows.GENERIC_READ)
	if _, err := SelectArtifactRootV1(root); err != nil {
		t.Fatalf("read-only BUILTIN\\Users ACE rejected: %v", err)
	}

	setArtifactRootDACLForTestV1(t, root, windows.GENERIC_WRITE)
	if _, err := SelectArtifactRootV1(root); err == nil {
		t.Fatal("writable BUILTIN\\Users ACE unexpectedly accepted")
	}
}

func TestArtifactRootAncestryRejectsWritableGrandparent(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	grandparent, err := os.MkdirTemp(home, ".freeagent-unsafe-ancestor-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(grandparent) })
	setArtifactRootDACLForTestV1(t, grandparent, windows.ACCESS_MASK(0x40)) // FILE_DELETE_CHILD
	parent := filepath.Join(grandparent, "state")
	root := filepath.Join(parent, "artifacts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	setArtifactRootDACLForTestV1(t, parent, 0)
	setArtifactRootDACLForTestV1(t, root, 0)
	if _, err := SelectArtifactRootV1(root); err == nil {
		t.Fatal("artifact root below writable grandparent unexpectedly accepted")
	}
}

func TestPublishV1RejectsWritableUntrustedChildACL(t *testing.T) {
	sourceRoot, artifactRoot := t.TempDir(), privateArtifactTempDirV1(t)
	_, digest, size := writeArtifactFixtureV1(t, sourceRoot, "packages/child-acl")
	selection, err := SelectSourcePackageV1(sourceRoot, artifactRoot, "packages/child-acl")
	if err != nil {
		t.Fatal(err)
	}
	request := PublishRequestV1{
		Source: selection, ArtifactDigest: digest, ArtifactSizeBytes: size,
		MaxPackageBytes: moduleapi.MaxModuleSourcePackageBytesV1,
	}
	if _, err := PublishV1(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(artifactRoot, digest, "content", "entry.txt")
	setArtifactRootDACLForTestV1(t, payload, windows.GENERIC_WRITE)
	if _, err := PublishV1(context.Background(), request); err == nil {
		t.Fatal("payload writable by BUILTIN\\Users unexpectedly reused")
	}
}

func setArtifactRootDACLForTestV1(t *testing.T, path string, untrustedAccess windows.ACCESS_MASK) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		t.Fatalf("current user SID: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, 4)
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
	if untrustedAccess != 0 {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: untrustedAccess,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(users),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		t.Fatalf("set artifact ACL: %v", err)
	}
}
