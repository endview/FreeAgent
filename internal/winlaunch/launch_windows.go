//go:build windows

package winlaunch

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	waitObject0                             = 0
	waitTimeout                             = 258
	handleFlagInherit                       = 0x00000001
	capabilityInternetClientSID             = "S-1-15-3-1"
	accessAllowedACEType                    = 0
	accessDeniedACEType                     = 1
)

// SandboxSupported reports whether this build can launch AppContainer workers.
func SandboxSupported() bool { return true }

type sidAndAttributes struct {
	SID        *windows.SID
	Attributes uint32
}

type securityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *sidAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

type profile struct {
	name    string
	sid     *windows.SID
	owned   bool
	restore []func() error
}

var (
	userenv                   = windows.NewLazySystemDLL("userenv.dll")
	createAppContainerProfile = userenv.NewProc("CreateAppContainerProfile")
	deriveAppContainerSID     = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	deleteAppContainerProfile = userenv.NewProc("DeleteAppContainerProfile")
)

// keptGrants records the grants left in place for each named profile during
// this process. A workspace ACL is propagated once per owner session instead
// of on every command; ReleaseKept removes it when the owner shuts down.
var keptGrants = struct {
	sync.Mutex
	profiles map[string]map[string]Grant
	applied  int
}{profiles: map[string]map[string]Grant{}}

type windowsProcess struct {
	mu sync.Mutex

	process windows.Handle
	thread  windows.Handle
	pid     uint32
	job     windows.Handle
	profile *profile

	stdin  *os.File
	stdout *os.File
	stderr *os.File

	waitDone chan struct{}
	waitExit Exit
	waitErr  error
	closed   bool
}

func Start(ctx context.Context, spec Spec) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("process launcher: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSpec(spec); err != nil {
		return nil, fmt.Errorf("process launcher: validate spec: %w", err)
	}

	var p *profile
	var err error
	if spec.Mode == ModeAppContainer {
		p, err = newProfile(spec)
		if err != nil {
			return nil, fmt.Errorf("process launcher: create profile: %w", err)
		}
		if err := applyGrants(p, spec.Grants); err != nil {
			cleanupProfile(p)
			return nil, fmt.Errorf("process launcher: apply grants: %w", err)
		}
		if err := applyProtectedPaths(p, spec.ProtectedPaths); err != nil {
			cleanupProfile(p)
			return nil, fmt.Errorf("process launcher: protect paths: %w", err)
		}
	}

	stdinRead, stdinWrite, err := createPipePair()
	if err != nil {
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stdin pipe: %w", err)
	}
	stdoutRead, stdoutWrite, err := createPipePair()
	if err != nil {
		closeFiles(stdinRead, stdinWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stdout pipe: %w", err)
	}
	stderrRead, stderrWrite, err := createPipePair()
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stderr pipe: %w", err)
	}

	// The child ends are inherited; parent ends must never cross the process
	// boundary. The handle list below is still passed explicitly.
	if err := clearInherit(stdinWrite); err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stdin inheritance: %w", err)
	}
	if err := clearInherit(stdoutRead); err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stdout inheritance: %w", err)
	}
	if err := clearInherit(stderrRead); err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: stderr inheritance: %w", err)
	}

	attr, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: attribute list: %w", err)
	}
	defer attr.Delete()
	handles := []windows.Handle{windows.Handle(stdinRead.Fd()), windows.Handle(stdoutWrite.Fd()), windows.Handle(stderrWrite.Fd())}
	if err := attr.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: handle list: %w", err)
	}

	var capabilitySID *windows.SID
	var capabilityAttrs sidAndAttributes
	var security securityCapabilities
	if p != nil {
		if spec.Network == NetworkInternet {
			capabilitySID, err = sidFromString(capabilityInternetClientSID)
			if err != nil {
				closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
				cleanupProfile(p)
				return nil, fmt.Errorf("process launcher: internet capability: %w", err)
			}
			defer windows.LocalFree(windows.Handle(unsafe.Pointer(capabilitySID)))
			capabilityAttrs = sidAndAttributes{SID: capabilitySID, Attributes: 0x00000004}
			security.Capabilities = &capabilityAttrs
			security.CapabilityCount = 1
		}
		security.AppContainerSID = p.sid
		if err := attr.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&security), unsafe.Sizeof(security)); err != nil {
			closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
			cleanupProfile(p)
			return nil, fmt.Errorf("process launcher: security capabilities: %w", err)
		}
	}

	startup := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  windows.Handle(stdinRead.Fd()),
			StdOutput: windows.Handle(stdoutWrite.Fd()),
			StdErr:    windows.Handle(stderrWrite.Fd()),
		},
		ProcThreadAttributeList: attr.List(),
	}
	application, err := windows.UTF16PtrFromString(spec.Command)
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: command: %w", err)
	}
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{spec.Command}, spec.Args...)))
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: arguments: %w", err)
	}
	var directory *uint16
	if spec.Dir != "" {
		directory, err = windows.UTF16PtrFromString(filepath.Clean(spec.Dir))
		if err != nil {
			closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
			cleanupProfile(p)
			return nil, fmt.Errorf("process launcher: directory: %w", err)
		}
	}
	env, err := environmentBlock(spec.Env)
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, err
	}
	var environment *uint16
	if len(env) != 0 {
		environment = &env[0]
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: create job: %w", err)
	}
	closeJob := func() { _ = windows.CloseHandle(job) }
	jobInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	jobInfo.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		closeJob()
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: configure job: %w", err)
	}

	creationFlags := uint32(windows.CREATE_SUSPENDED | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if spec.Mode == ModeHost {
		creationFlags |= windows.CREATE_NO_WINDOW
	}
	var info windows.ProcessInformation
	err = windows.CreateProcess(application, &commandLine[0], nil, nil, true, creationFlags, environment, directory, &startup.StartupInfo, &info)
	if err != nil {
		closeJob()
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: CreateProcessW: %w", err)
	}
	// The process is still suspended. Assigning before ResumeThread is the
	// ordering that prevents a child from escaping the Job during startup.
	processHandle := info.Process
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = windows.TerminateProcess(processHandle, 1)
		_ = windows.CloseHandle(processHandle)
		closeJob()
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: assign job before resume: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.CloseHandle(info.Thread)
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(processHandle)
		closeJob()
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		cleanupProfile(p)
		return nil, fmt.Errorf("process launcher: resume: %w", err)
	}
	_ = windows.CloseHandle(info.Thread)

	// The parent closes the child ends immediately after the process is live.
	closeFiles(stdinRead, stdoutWrite, stderrWrite)
	wp := &windowsProcess{process: processHandle, pid: info.ProcessId, job: job, profile: p, stdin: stdinWrite, stdout: stdoutRead, stderr: stderrRead, waitDone: make(chan struct{})}
	go wp.waitBackground()
	return &Process{
		pid: int(wp.pid), treeWait: wp.waitTree,
		stdin: wp.stdin, stdout: wp.stdout, stderr: wp.stderr,
		wait: wp.wait, kill: wp.kill, term: wp.terminate, close: wp.close,
	}, nil
}

func validateSpec(spec Spec) error {
	if spec.Command == "" || !filepath.IsAbs(spec.Command) {
		return errors.New("process launcher: command must be an absolute path")
	}
	if spec.Mode != ModeHost && spec.Mode != ModeAppContainer {
		return fmt.Errorf("process launcher: unsupported mode %q", spec.Mode)
	}
	if spec.Network != "" && spec.Network != NetworkOff && spec.Network != NetworkInternet {
		return fmt.Errorf("process launcher: unsupported network mode %q", spec.Network)
	}
	if spec.Mode == ModeHost && spec.Network == NetworkInternet {
		return errors.New("process launcher: host mode does not use an AppContainer capability")
	}
	for _, value := range spec.Env {
		if _, ok := environmentKey(value); !ok {
			return errors.New("process launcher: environment must contain KEY=VALUE entries")
		}
	}
	return nil
}

// environmentKey returns the name of a KEY=VALUE entry. Windows keeps
// per-drive working directories in hidden entries such as "=C:=C:\work",
// so a leading '=' belongs to the name and the separator is the first '='
// after it. Processes started by Explorer or cmd.exe always carry these.
func environmentKey(entry string) (string, bool) {
	if entry == "" || strings.IndexByte(entry, 0) >= 0 {
		return "", false
	}
	index := strings.IndexByte(entry[1:], '=') + 1
	if index <= 0 || entry[:index] == "=" {
		return "", false
	}
	return entry[:index], true
}

func newProfile(spec Spec) (*profile, error) {
	name := spec.AppContainerName
	owned := false
	if name == "" {
		name = fmt.Sprintf("FreeAgentRun%d", time.Now().UnixNano())
		owned = true
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	displayPtr, err := windows.UTF16PtrFromString("FreeAgent sandbox worker")
	if err != nil {
		return nil, err
	}
	descriptionPtr, err := windows.UTF16PtrFromString("FreeAgent bounded command worker")
	if err != nil {
		return nil, err
	}
	var profileCapabilities *sidAndAttributes
	var profileCapabilitySID *windows.SID
	if spec.Network == NetworkInternet {
		profileCapabilitySID, err = sidFromString(capabilityInternetClientSID)
		if err != nil {
			return nil, fmt.Errorf("process launcher: profile internet capability: %w", err)
		}
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(profileCapabilitySID)))
		profileCapabilities = &sidAndAttributes{SID: profileCapabilitySID, Attributes: 0x00000004}
	}
	var sid *windows.SID
	result, _, callErr := createAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(displayPtr)),
		uintptr(unsafe.Pointer(descriptionPtr)),
		uintptr(unsafe.Pointer(profileCapabilities)), boolUintptr(profileCapabilities != nil), uintptr(unsafe.Pointer(&sid)),
	)
	if result != 0 {
		result, _, callErr = deriveAppContainerSID.Call(uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(&sid)))
		if result != 0 {
			return nil, fmt.Errorf("process launcher: AppContainer profile: HRESULT 0x%08x (%v)", uint32(result), callErr)
		}
	}
	if sid == nil {
		return nil, errors.New("process launcher: AppContainer profile returned no SID")
	}
	return &profile{name: name, sid: sid, owned: owned}, nil
}

func cleanupProfile(p *profile) {
	if p == nil {
		return
	}
	for i := len(p.restore) - 1; i >= 0; i-- {
		_ = p.restore[i]()
	}
	if p.sid != nil {
		_ = windows.FreeSid(p.sid)
	}
	if p.owned {
		name, err := windows.UTF16PtrFromString(p.name)
		if err == nil {
			_, _, _ = deleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
		}
	}
}

func applyGrants(p *profile, grants []Grant) error {
	if p == nil {
		return errors.New("process launcher: grants require an AppContainer profile")
	}
	seen := map[string]bool{}
	var keep, temporary []Grant
	for _, grant := range grants {
		root := filepath.Clean(grant.Root)
		key := strings.ToLower(root)
		if root == "" || !filepath.IsAbs(root) || seen[key] {
			return fmt.Errorf("process launcher: invalid or duplicate grant root %q", grant.Root)
		}
		seen[key] = true
		if grant.Access != AccessRead && grant.Access != AccessWrite {
			return fmt.Errorf("process launcher: invalid grant access %q", grant.Access)
		}
		// Links inside the root are fine: ACL propagation does not follow
		// them, so their targets keep their own ACL. The root itself must not
		// be a link, or the grant would land on a directory nobody checked.
		if err := rejectReparsePoint(root); err != nil {
			return fmt.Errorf("process launcher: grant root %s: %w", root, err)
		}
		grant.Root = root
		if grant.Keep {
			if p.owned {
				return errors.New("process launcher: kept grants require an AppContainerName")
			}
			keep = append(keep, grant)
		} else {
			temporary = append(temporary, grant)
		}
	}
	if !p.owned {
		if err := syncKept(p, keep); err != nil {
			return err
		}
	}
	for _, grant := range temporary {
		revoke, err := grantPath(grant.Root, grant.Access, p.sid)
		if err != nil {
			return fmt.Errorf("process launcher: grant %s: %w", grant.Root, err)
		}
		p.restore = append(p.restore, revoke)
	}
	return nil
}

// syncKept leaves exactly the wanted kept grants on a named profile. Grants
// already applied in this process are reused; others are revoked or applied.
func syncKept(p *profile, wanted []Grant) error {
	keptGrants.Lock()
	defer keptGrants.Unlock()
	name := strings.ToLower(p.name)
	current := keptGrants.profiles[name]
	if current == nil {
		current = map[string]Grant{}
		keptGrants.profiles[name] = current
	}
	next := map[string]Grant{}
	for _, grant := range wanted {
		next[strings.ToLower(grant.Root)] = grant
	}
	for key, grant := range current {
		if want, ok := next[key]; ok && want.Access == grant.Access {
			continue
		}
		if err := revokeSID(grant.Root, p.sid); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("process launcher: release grant %s: %w", grant.Root, err)
		}
		delete(current, key)
	}
	for key, grant := range next {
		if _, ok := current[key]; ok {
			continue
		}
		if _, err := grantPath(grant.Root, grant.Access, p.sid); err != nil {
			_ = revokeSID(grant.Root, p.sid)
			return fmt.Errorf("process launcher: grant %s: %w", grant.Root, err)
		}
		current[key] = grant
		keptGrants.applied++
	}
	return nil
}

// ReleaseKept revokes every grant kept for the named profile and deletes the
// profile. The owner that launched the profile calls it when it shuts down.
func ReleaseKept(name string) error {
	keptGrants.Lock()
	defer keptGrants.Unlock()
	key := strings.ToLower(name)
	grants := keptGrants.profiles[key]
	delete(keptGrants.profiles, key)
	if len(grants) == 0 {
		return nil
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var sid *windows.SID
	if result, _, callErr := deriveAppContainerSID.Call(uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(&sid))); result != 0 || sid == nil {
		return fmt.Errorf("process launcher: AppContainer SID: HRESULT 0x%08x (%v)", uint32(result), callErr)
	}
	defer windows.FreeSid(sid)
	var errs []error
	for _, grant := range grants {
		if err := revokeSID(grant.Root, sid); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("release grant %s: %w", grant.Root, err))
		}
	}
	_, _, _ = deleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr)))
	return errors.Join(errs...)
}

func rejectReparsePoint(path string) error {
	if info, err := os.Lstat(path); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &attributes); err != nil {
		return err
	}
	if attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse point")
	}
	return nil
}

// grantPath adds inheritable allow ACEs for the container to an existing
// DACL. The returned function removes only those ACEs again.
func grantPath(path string, access AccessMode, sid *windows.SID) (func() error, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("grant root must be a directory")
	}
	descriptor, oldDACL, err := readDACL(path)
	if err != nil {
		return nil, err
	}
	permissions := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	if access == AccessWrite {
		permissions |= windows.FILE_GENERIC_WRITE
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
		Trustee:           sidTrustee(sid),
	}}
	if access == AccessWrite {
		// DELETE is inherited by ordinary children. FILE_DELETE_CHILD is
		// deliberately absent; it would defeat protected child deny ACEs.
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.DELETE,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.INHERIT_ONLY_ACE,
			Trustee:           sidTrustee(sid),
		})
	}
	newDACL, err := windows.ACLFromEntries(entries, oldDACL)
	if err != nil {
		return nil, err
	}
	if err := setDACL(path, descriptor, newDACL); err != nil {
		return nil, err
	}
	return func() error { return revokeSID(path, sid) }, nil
}

func applyProtectedPaths(p *profile, paths []ProtectedPath) error {
	seen := map[string]bool{}
	for _, protected := range paths {
		path := filepath.Clean(protected.Path)
		if path == "" || !filepath.IsAbs(path) || seen[strings.ToLower(path)] {
			return fmt.Errorf("invalid or duplicate protected path %q", protected.Path)
		}
		seen[strings.ToLower(path)] = true
		switch protected.Mode {
		case ProtectReadOnly, ProtectGitWrite:
		default:
			return fmt.Errorf("invalid protect mode %q for %s", protected.Mode, path)
		}
		created := false
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if !protected.Create {
				continue
			}
			if protected.Dir {
				err = os.Mkdir(path, 0o700)
			} else {
				var file *os.File
				if file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
					err = file.Close()
				}
			}
			if err != nil {
				return fmt.Errorf("create protected placeholder %s: %w", path, err)
			}
			created = true
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		// A linked protected path would move the override onto its target.
		if err := rejectReparsePoint(path); err != nil {
			return fmt.Errorf("protected path %s: %w", path, err)
		}
		restore, err := protectPath(path, protected.Mode, info.IsDir(), p.sid)
		if err != nil {
			if created {
				_ = os.Remove(path)
			}
			return fmt.Errorf("protect %s: %w", path, err)
		}
		p.restore = append(p.restore, func() error {
			err := restore()
			if created {
				err = errors.Join(err, os.Remove(path))
			}
			return err
		})
	}
	return nil
}

// protectPath replaces the entry's DACL with a protected one. An AppContainer
// can use an access only when a package or capability ACE grants it, so the
// protected ACL, which does not inherit the workspace's write grant, is what
// enforces the mode; the user, SYSTEM and Administrators keep full control.
func protectPath(path string, mode ProtectMode, dir bool, sid *windows.SID) (func() error, error) {
	_, oldDACL, err := readDACL(path)
	if err != nil {
		return nil, err
	}
	inherit := uint32(windows.NO_INHERITANCE)
	if dir {
		inherit = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	read := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	var entries []windows.EXPLICIT_ACCESS
	switch mode {
	case ProtectReadOnly:
		entries = append(entries, windows.EXPLICIT_ACCESS{AccessPermissions: read, AccessMode: windows.GRANT_ACCESS, Inheritance: inherit, Trustee: sidTrustee(sid)})
	case ProtectGitWrite:
		// The entry itself is readable and writable but carries no DELETE,
		// so it cannot be removed, renamed or replaced. Children inherit
		// write and DELETE; no ACE grants FILE_DELETE_CHILD.
		entries = append(entries, windows.EXPLICIT_ACCESS{AccessPermissions: read | windows.FILE_GENERIC_WRITE, AccessMode: windows.GRANT_ACCESS, Inheritance: inherit, Trustee: sidTrustee(sid)})
		if dir {
			entries = append(entries, windows.EXPLICIT_ACCESS{AccessPermissions: windows.DELETE, AccessMode: windows.GRANT_ACCESS,
				Inheritance: windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.INHERIT_ONLY_ACE, Trustee: sidTrustee(sid)})
		}
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	for _, raw := range []string{"S-1-5-18", "S-1-5-32-544"} {
		owner, err := windows.StringToSid(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: inherit, Trustee: sidTrustee(owner)})
	}
	entries = append(entries, windows.EXPLICIT_ACCESS{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: inherit, Trustee: sidTrustee(user.User.Sid)})
	newDACL, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return nil, err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, newDACL, nil); err != nil {
		return nil, err
	}
	// The saved DACL keeps its explicit ACEs; restoring it unprotected lets
	// the entry inherit from its parent again.
	return func() error {
		return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, oldDACL, nil)
	}, nil
}

func sidTrustee(sid *windows.SID) windows.TRUSTEE {
	return windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(sid)}
}

// readDACL refuses a missing DACL: building on it would replace "everyone
// may access" with an ACL that locks out the user.
func readDACL(path string) (*windows.SECURITY_DESCRIPTOR, *windows.ACL, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, nil, err
	}
	dacl, _, err := descriptor.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || (err == nil && dacl == nil) {
		return nil, nil, errors.New("path has no access control list")
	}
	if err != nil {
		return nil, nil, err
	}
	return descriptor, dacl, nil
}

// setDACL writes a DACL and keeps the object's inheritance setting, so a
// grant never cuts a workspace off from its parent's ACL.
func setDACL(path string, descriptor *windows.SECURITY_DESCRIPTOR, dacl *windows.ACL) error {
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control, _, err := descriptor.Control(); err == nil && control&windows.SE_DACL_PROTECTED != 0 {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, dacl, nil)
}

// revokeSID removes the explicit ACEs for one container SID. Unlike restoring
// a saved DACL, it keeps any change the user made in the meantime.
func revokeSID(path string, sid *windows.SID) error {
	descriptor, dacl, err := readDACL(path)
	if err != nil {
		return err
	}
	filtered, removed, err := withoutExplicitSID(dacl, sid)
	if err != nil || !removed {
		return err
	}
	return setDACL(path, descriptor, filtered)
}

func withoutExplicitSID(acl *windows.ACL, sid *windows.SID) (*windows.ACL, bool, error) {
	const headerSize = 8
	kept := make([][]byte, 0, acl.AceCount)
	size := headerSize
	removed := false
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return nil, false, err
		}
		explicit := ace.Header.AceFlags&windows.INHERITED_ACE == 0
		simple := ace.Header.AceType == accessAllowedACEType || ace.Header.AceType == accessDeniedACEType
		if explicit && simple && (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			removed = true
			continue
		}
		kept = append(kept, unsafe.Slice((*byte)(unsafe.Pointer(ace)), ace.Header.AceSize))
		size += int(ace.Header.AceSize)
	}
	if !removed {
		return acl, false, nil
	}
	buffer := make([]byte, size)
	buffer[0] = *(*byte)(unsafe.Pointer(acl)) // ACL revision
	binary.LittleEndian.PutUint16(buffer[2:], uint16(size))
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(kept)))
	offset := headerSize
	for _, ace := range kept {
		offset += copy(buffer[offset:], ace)
	}
	return (*windows.ACL)(unsafe.Pointer(&buffer[0])), true, nil
}

func sidFromString(raw string) (*windows.SID, error) {
	ptr, err := windows.UTF16PtrFromString(raw)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	if err := windows.ConvertStringSidToSid(ptr, &sid); err != nil {
		return nil, err
	}
	return sid, nil
}

func createPipePair() (*os.File, *os.File, error) {
	var readHandle, writeHandle windows.Handle
	security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&readHandle, &writeHandle, security, 0); err != nil {
		return nil, nil, err
	}
	read := os.NewFile(uintptr(readHandle), "freeagent-child-read")
	write := os.NewFile(uintptr(writeHandle), "freeagent-child-write")
	if read == nil || write == nil {
		closeFiles(read, write)
		return nil, nil, errors.New("process launcher: create pipe files")
	}
	return read, write, nil
}

func clearInherit(file *os.File) error {
	return windows.SetHandleInformation(windows.Handle(file.Fd()), handleFlagInherit, 0)
}

func closeFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}

func environmentBlock(values []string) ([]uint16, error) {
	if len(values) == 0 {
		return nil, nil
	}
	type entry struct{ key, value string }
	block := make([]entry, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key, ok := environmentKey(value)
		if !ok {
			return nil, errors.New("process launcher: environment must contain KEY=VALUE entries")
		}
		// Windows environment names are case-insensitive.
		folded := strings.ToUpper(key)
		if seen[folded] {
			return nil, fmt.Errorf("process launcher: duplicate environment key %q", key)
		}
		seen[folded] = true
		block = append(block, entry{key: folded, value: value})
	}
	// CreateProcess expects the block sorted by name without regard to case.
	sort.Slice(block, func(i, j int) bool { return block[i].key < block[j].key })
	joined := ""
	for _, item := range block {
		joined += item.value + "\x00"
	}
	joined += "\x00"
	block16 := utf16.Encode([]rune(joined))
	return append(block16, 0), nil
}

func boolUintptr(value bool) uintptr {
	if value {
		return 1
	}
	return 0
}

func (p *windowsProcess) wait(ctx context.Context) (Exit, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.waitDone:
		return p.waitExit, p.waitErr
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	}
}

func (p *windowsProcess) waitBackground() {
	result, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
	if err == nil && result == waitObject0 {
		var code uint32
		err = windows.GetExitCodeProcess(p.process, &code)
		if err == nil {
			p.waitExit = Exit{Code: int(int32(code))}
		}
	} else if err == nil {
		err = fmt.Errorf("process launcher: unexpected wait result %d", result)
	}
	p.waitErr = err
	close(p.waitDone)
}

func (p *windowsProcess) terminate() {
	if p == nil || p.process == 0 {
		return
	}
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, p.pid)
}

func (p *windowsProcess) kill() error {
	if p == nil {
		return nil
	}
	if p.job != 0 {
		return windows.TerminateJobObject(p.job, 1)
	}
	if p.process != 0 {
		return windows.TerminateProcess(p.process, 1)
	}
	return nil
}

func (p *windowsProcess) close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	var errs []error
	if p.stdin != nil {
		if err := p.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if p.stdout != nil {
		if err := p.stdout.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if p.stderr != nil {
		if err := p.stderr.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if p.job != 0 {
		errs = append(errs, windows.CloseHandle(p.job))
	}
	if p.process != 0 {
		errs = append(errs, windows.CloseHandle(p.process))
	}
	cleanupProfile(p.profile)
	return errors.Join(errs...)
}

func (p *windowsProcess) waitTree(ctx context.Context) error {
	if p.job == 0 {
		return ErrUnsupported
	}
	// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION is a fixed Windows ABI layout.
	var accounting struct {
		User, Kernel, PeriodUser, PeriodKernel int64
		Faults, Total, Active, Terminated      uint32
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return err
		}
		if accounting.Active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
