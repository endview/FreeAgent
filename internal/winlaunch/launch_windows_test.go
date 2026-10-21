//go:build windows

package winlaunch

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAppContainerHelper(t *testing.T) {
	for index, arg := range os.Args {
		if arg == "--appcontainer-probe-helper" {
			for _, operation := range os.Args[index+1:] {
				label, _, _ := strings.Cut(operation, "|")
				if err := probeOperation(operation); err != nil {
					fmt.Printf("%s=fail:%v\n", label, err)
				} else {
					fmt.Printf("%s=ok\n", label)
				}
			}
			return
		}
		if arg != "--appcontainer-helper" || len(os.Args) < index+3 {
			if arg == "--appcontainer-loopback-helper" && len(os.Args) >= index+2 {
				connection, err := net.DialTimeout("tcp4", os.Args[index+1], 500*time.Millisecond)
				if connection != nil {
					_ = connection.Close()
				}
				fmt.Printf("connect=%v", err)
				return
			}
			if arg != "--appcontainer-isolation-helper" || len(os.Args) < index+3 {
				continue
			}
			ownMarker, otherMarker := os.Args[index+1], os.Args[index+2]
			_, ownErr := os.Create(ownMarker)
			otherFile, otherErr := os.Create(otherMarker)
			if otherFile != nil {
				_ = otherFile.Close()
			}
			fmt.Printf("own=%v cross=%v", ownErr, otherErr)
			return
		}
		var value uint32
		length := uint32(0)
		token := windows.GetCurrentProcessToken()
		if err := windows.GetTokenInformation(token, 29, (*byte)(unsafe.Pointer(&value)), 4, &length); err != nil {
			fmt.Print("token-error:", err)
			return
		}
		_, _ = os.Create(os.Args[index+1])
		secret, readErr := os.ReadFile(os.Args[index+2])
		protectedErr := error(nil)
		if len(os.Args) >= index+4 {
			protectedFile := filepath.Join(os.Args[index+3], "blocked.txt")
			file, err := os.Create(protectedFile)
			protectedErr = err
			if file != nil {
				_ = file.Close()
			}
		}
		fmt.Printf("appcontainer=%d secret=%s read=%v protected=%v", value, secret, readErr, protectedErr)
		return
	}
}

func TestAppContainerLaunchIdentityGrantAndIsolation(t *testing.T) {
	base := filepath.Join(os.Getenv("USERPROFILE"), "Documents", fmt.Sprintf("FreeAgent-v7-native-%d", time.Now().UnixNano()))
	workspace := filepath.Join(base, "workspace")
	privateRoot := filepath.Join(base, "private")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	secretPath := filepath.Join(privateRoot, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("private-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "created.txt")
	protectedRoot := filepath.Join(workspace, ".freeagent")
	profileName := fmt.Sprintf("FreeAgentTestProtected%d", time.Now().UnixNano())
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	spec := Spec{
		Command: command,
		Args:    []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-helper", marker, secretPath, protectedRoot},
		Dir:     workspace,
		Env: func() []string {
			// Explorer-started parents always carry hidden drive entries.
			values := []string{"=C:=C:\\Windows", "=::=::\\"}
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, "=") && !strings.HasPrefix(entry, "=C:=") && !strings.HasPrefix(entry, "=::=") {
					values = append(values, entry)
				}
			}
			for _, name := range []string{"Path", "SystemRoot", "TEMP", "TMP", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "ALLUSERSPROFILE", "ProgramData", "ProgramFiles", "ProgramFiles(x86)", "CommonProgramFiles", "CommonProgramFiles(x86)", "PUBLIC", "LOCALAPPDATA", "APPDATA", "ComSpec"} {
				if value, ok := os.LookupEnv(name); ok {
					values = append(values, name+"="+value)
				}
			}
			return values
		}(),
		Mode:             ModeAppContainer,
		Network:          NetworkOff,
		Grants:           []Grant{{Root: workspace, Access: AccessWrite}, {Root: filepath.Dir(command), Access: AccessRead}},
		ProtectedPaths:   []ProtectedPath{{Path: protectedRoot, Mode: ProtectReadOnly, Create: true, Dir: true}},
		AppContainerName: profileName,
	}
	namePtr, _ := windows.UTF16PtrFromString(profileName)
	t.Cleanup(func() { _, _, _ = deleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr))) })
	process, err := Start(ctx, spec)
	if err != nil {
		t.Fatalf("AppContainer launch failed: %T %#v %v", err, err, err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	exit, waitErr := process.Wait(ctx)
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if exit.Code != 0 {
		t.Fatalf("sandbox helper failed: exit=%d output=%q", exit.Code, stdout)
	}
	if !strings.Contains(string(stdout), "appcontainer=1") {
		t.Fatalf("child did not report an AppContainer identity: %q", stdout)
	}
	if strings.Contains(string(stdout), "secret=private-data") {
		t.Fatalf("private file unexpectedly readable: %q", stdout)
	}
	if strings.Contains(string(stdout), "protected=<nil>") {
		items, _ := os.ReadDir(protectedRoot)
		t.Fatalf("protected path was writable: %q entries=%v", stdout, items)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("workspace grant did not allow ordinary file creation: %v", err)
	}
}

func TestHostLaunchSmoke(t *testing.T) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		t.Skip("COMSPEC is unavailable")
	}
	process, err := Start(context.Background(), Spec{Command: command, Args: []string{"/d", "/c", "exit 0"}, Mode: ModeHost})
	if err != nil {
		t.Fatalf("host launch failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	if _, err := io.ReadAll(process.Stdout()); err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait(context.Background())
	if err != nil || exit.Code != 0 {
		t.Fatalf("host exit=%+v err=%v", exit, err)
	}
}

func TestHostLaunchFilteredEnvironment(t *testing.T) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		t.Skip("COMSPEC is unavailable")
	}
	process, err := Start(context.Background(), Spec{Command: command, Args: []string{"/d", "/c", "exit 0"}, Env: []string{"PATH=C:\\Windows\\System32", "SystemRoot=C:\\Windows"}, Mode: ModeHost})
	if err != nil {
		t.Fatalf("filtered host launch failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	_, _ = io.ReadAll(process.Stdout())
	if exit, err := process.Wait(context.Background()); err != nil || exit.Code != 0 {
		t.Fatalf("filtered host exit=%+v err=%v", exit, err)
	}
}

func TestEnvironmentKey(t *testing.T) {
	for _, test := range []struct {
		entry string
		key   string
		ok    bool
	}{
		{"PATH=C:\\Windows", "PATH", true},
		{"EMPTY=", "EMPTY", true},
		{"=C:=C:\\work", "=C:", true},
		{"=::=::\\", "=::", true},
		{"=ExitCode=00000000", "=ExitCode", true},
		{"", "", false},
		{"PATH", "", false},
		{"=", "", false},
		{"==value", "", false},
		{"=C:", "", false},
		{"A=b\x00c", "", false},
	} {
		key, ok := environmentKey(test.entry)
		if key != test.key || ok != test.ok {
			t.Errorf("environmentKey(%q) = %q, %v; want %q, %v", test.entry, key, ok, test.key, test.ok)
		}
	}
}

func TestEnvironmentBlockOrderAndDuplicates(t *testing.T) {
	block, err := environmentBlock([]string{"b=2", "=C:=C:\\x", "A=1", "=::=::\\"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(utf16.Decode(block)), "\x00"), "\x00")
	want := []string{"=::=::\\", "=C:=C:\\x", "A=1", "b=2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("block order = %q, want %q", got, want)
	}
	if _, err := environmentBlock([]string{"Path=a", "PATH=b"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("case-insensitive duplicate accepted: %v", err)
	}
	if _, err := environmentBlock([]string{"=C:"}); err == nil {
		t.Fatal("malformed hidden entry accepted")
	}
}

// A parent started from Explorer passes hidden "=C:=..." entries through
// os.Environ; the launcher must accept them and still pass ordinary values.
func TestHostLaunchAcceptsHiddenDriveEnvironment(t *testing.T) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		t.Skip("COMSPEC is unavailable")
	}
	env := []string{"=C:=C:\\Windows", "=::=::\\", "=ExitCode=00000000", "FREEAGENT_LAUNCH_PROBE=visible"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "=") && !strings.HasPrefix(strings.ToUpper(entry), "FREEAGENT_LAUNCH_PROBE=") {
			env = append(env, entry)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := Start(ctx, Spec{Command: command, Args: []string{"/d", "/c", "echo [%FREEAGENT_LAUNCH_PROBE%]"}, Env: env, Mode: ModeHost})
	if err != nil {
		t.Fatalf("host launch with hidden drive entries failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	output, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := process.Wait(ctx); err != nil || exit.Code != 0 {
		t.Fatalf("host exit=%+v err=%v", exit, err)
	}
	if !strings.Contains(string(output), "[visible]") {
		t.Fatalf("child did not receive the ordinary variable: %q", output)
	}
}

// pnpm links packages with junctions. A link inside the grant root keeps
// working when its target is inside the root, and a link that points outside
// exposes nothing: propagation never follows it, so the target keeps its ACL.
func TestJunctionInsideGrantDoesNotExposeTarget(t *testing.T) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		t.Skip("COMSPEC is unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	store := filepath.Join(root, "node_modules", ".pnpm", "lib@1.0.0", "node_modules", "lib")
	for _, directory := range []string{store, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "index.js"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	packageLink := filepath.Join(root, "node_modules", "lib")
	escape := filepath.Join(root, "escape")
	makeJunction(t, command, packageLink, store)
	makeJunction(t, command, escape, outside)
	name := fmt.Sprintf("FreeAgentTestJunction%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteTestProfile(name) })
	output := runProbe(t, executable, root, name, []Grant{{Root: root, Access: AccessWrite}, {Root: filepath.Dir(executable), Access: AccessRead}}, nil,
		"inside|read|"+filepath.Join(packageLink, "index.js"),
		"escape|read|"+filepath.Join(escape, "secret.txt"),
		"plant|write|"+filepath.Join(escape, "planted.txt"))
	expectProbe(t, output, "inside", true)
	expectProbe(t, output, "escape", false)
	expectProbe(t, output, "plant", false)
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Fatal("sandbox wrote through a junction outside the grant")
	}
	sid := testProfileSID(t, name)
	for _, path := range []string{outside, filepath.Join(outside, "secret.txt")} {
		if hasSIDEntry(t, path, sid) {
			t.Fatalf("junction target %s received a container ACE", path)
		}
	}
}

func TestAppContainerInternetCapabilityLaunch(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	privateRoot := filepath.Join(base, "private")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(privateRoot, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("private-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "internet-marker.txt")
	protected := filepath.Join(workspace, ".freeagent")
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	process, err := Start(ctx, Spec{Command: command,
		Args: []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-helper", marker, secretPath, protected},
		Dir:  workspace, Mode: ModeAppContainer, Network: NetworkInternet,
		Grants:           []Grant{{Root: workspace, Access: AccessWrite}, {Root: filepath.Dir(command), Access: AccessRead}},
		AppContainerName: fmt.Sprintf("FreeAgentTestInternet%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("internet-capability launch failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait(ctx)
	if err != nil || exit.Code != 0 {
		t.Fatalf("internet-capability exit=%+v err=%v output=%q", exit, err, stdout)
	}
	if !strings.Contains(string(stdout), "appcontainer=1") {
		t.Fatalf("child did not run in AppContainer: %q", stdout)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("workspace grant failed: %v", err)
	}
}

func makeJunction(t *testing.T, command, link, target string) {
	t.Helper()
	if output, err := exec.Command(command, "/d", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, output)
	}
	// Remove links before the temporary tree so cleanup never walks a target.
	t.Cleanup(func() { _ = os.Remove(link) })
}

func deleteTestProfile(name string) {
	_ = ReleaseKept(name)
	if namePtr, err := windows.UTF16PtrFromString(name); err == nil {
		_, _, _ = deleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr)))
	}
}

func testProfileSID(t *testing.T, name string) *windows.SID {
	t.Helper()
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	var sid *windows.SID
	if result, _, callErr := deriveAppContainerSID.Call(uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(&sid))); result != 0 || sid == nil {
		t.Fatalf("derive SID: 0x%08x %v", uint32(result), callErr)
	}
	copied, err := sid.Copy()
	_ = windows.FreeSid(sid)
	if err != nil {
		t.Fatal(err)
	}
	return copied
}

// hasSIDEntry reports any explicit or inherited ACE for the SID.
func hasSIDEntry(t *testing.T, path string, sid *windows.SID) bool {
	t.Helper()
	_, dacl, err := readDACL(path)
	if err != nil {
		t.Fatalf("read DACL %s: %v", path, err)
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatal(err)
		}
		if (ace.Header.AceType == accessAllowedACEType || ace.Header.AceType == accessDeniedACEType) &&
			(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			return true
		}
	}
	return false
}

// runProbe runs the test binary in an AppContainer. Each operation is
// "label|read|path", "label|write|path", "label|delete|path" or
// "label|rename|from>to"; the child prints "label=ok" or "label=fail:...".
func runProbe(t *testing.T, executable, dir, name string, grants []Grant, protected []ProtectedPath, operations ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-probe-helper"}, operations...)
	process, err := Start(ctx, Spec{Command: executable, Args: args, Dir: dir, Mode: ModeAppContainer, Network: NetworkOff,
		Grants: grants, ProtectedPaths: protected, AppContainerName: name})
	if err != nil {
		t.Fatalf("probe launch failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := process.Wait(ctx); err != nil || exit.Code != 0 {
		t.Fatalf("probe exit=%+v err=%v output=%q", exit, err, stdout)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("probe cleanup: %v", err)
	}
	return string(stdout)
}

func expectProbe(t *testing.T, output, label string, allowed bool) {
	t.Helper()
	ok := strings.Contains(output, label+"=ok\n")
	if !ok && !strings.Contains(output, label+"=fail:") {
		t.Fatalf("probe %s missing from %q", label, output)
	}
	if ok != allowed {
		t.Fatalf("probe %s allowed=%v, want %v; output=%q", label, ok, allowed, output)
	}
}

func probeOperation(operation string) error {
	parts := strings.SplitN(operation, "|", 3)
	if len(parts) != 3 {
		return fmt.Errorf("malformed operation %q", operation)
	}
	switch parts[1] {
	case "read":
		_, err := os.ReadFile(parts[2])
		return err
	case "write":
		return os.WriteFile(parts[2], []byte("probe"), 0o600)
	case "delete":
		return os.Remove(parts[2])
	case "rename":
		from, to, _ := strings.Cut(parts[2], ">")
		return os.Rename(from, to)
	}
	return fmt.Errorf("unknown operation %q", parts[1])
}

func TestKeptGrantIsReusedAndReleased(t *testing.T) {
	command := os.Getenv("COMSPEC")
	if command == "" {
		t.Skip("COMSPEC is unavailable")
	}
	root := filepath.Join(t.TempDir(), "workspace")
	nested := filepath.Join(root, "src")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "main.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A few thousand entries make the saved propagation visible in the log.
	for index := 0; index < 1500; index++ {
		directory := filepath.Join(nested, fmt.Sprintf("pkg-%04d", index))
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "file.go"), []byte("package pkg"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("FreeAgentTestKept%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteTestProfile(name) })
	launch := func(marker string) time.Duration {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		process, err := Start(ctx, Spec{Command: command, Args: []string{"/d", "/c", "echo ok>" + marker}, Dir: root,
			Mode: ModeAppContainer, Network: NetworkOff, Grants: []Grant{{Root: root, Access: AccessWrite, Keep: true}}, AppContainerName: name})
		if err != nil {
			t.Fatalf("kept-grant launch failed: %v", err)
		}
		_ = process.Stdin().Close()
		_, _ = io.ReadAll(process.Stdout())
		if exit, err := process.Wait(ctx); err != nil || exit.Code != 0 {
			t.Fatalf("kept-grant exit=%+v err=%v", exit, err)
		}
		if err := process.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			t.Fatalf("kept grant did not allow a write: %v", err)
		}
		return time.Since(started)
	}
	keptGrants.Lock()
	before := keptGrants.applied
	keptGrants.Unlock()
	first := launch("first.txt")
	second := launch("second.txt")
	keptGrants.Lock()
	applied := keptGrants.applied - before
	keptGrants.Unlock()
	if applied != 1 {
		t.Fatalf("kept grant applied %d times across two launches, want 1", applied)
	}
	sid := testProfileSID(t, name)
	if !hasSIDEntry(t, root, sid) || !hasSIDEntry(t, filepath.Join(nested, "main.go"), sid) {
		t.Fatal("kept grant was removed when the process closed")
	}
	released := time.Now()
	if err := ReleaseKept(name); err != nil {
		t.Fatal(err)
	}
	releaseTime := time.Since(released)
	for _, path := range []string{root, nested, filepath.Join(nested, "main.go"), filepath.Join(root, "first.txt"), filepath.Join(nested, "pkg-1499", "file.go")} {
		if hasSIDEntry(t, path, sid) {
			t.Fatalf("ReleaseKept left a container ACE on %s", path)
		}
	}
	t.Logf("kept grant: entries=3002 first=%s second=%s release=%s", first, second, releaseTime)
}

func TestProtectedGitPathsStayReadable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "workspace")
	gitDir := filepath.Join(root, ".git")
	for _, directory := range []string{filepath.Join(gitDir, "objects"), filepath.Join(gitDir, "hooks")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"HEAD": "ref: refs/heads/main\n", "config": "[core]\n"} {
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("FreeAgentTestGit%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteTestProfile(name) })
	sid := testProfileSID(t, name)
	grants := []Grant{{Root: root, Access: AccessWrite}, {Root: filepath.Dir(executable), Access: AccessRead}}

	readOnly := runProbe(t, executable, root, name, grants, []ProtectedPath{{Path: gitDir, Mode: ProtectReadOnly}},
		"head|read|"+filepath.Join(gitDir, "HEAD"),
		"object|write|"+filepath.Join(gitDir, "objects", "new"),
		"delete|delete|"+filepath.Join(gitDir, "HEAD"),
		"rename|rename|"+gitDir+">"+filepath.Join(root, "moved"),
		"ordinary|write|"+filepath.Join(root, "file.txt"))
	expectProbe(t, readOnly, "head", true)
	expectProbe(t, readOnly, "object", false)
	expectProbe(t, readOnly, "delete", false)
	expectProbe(t, readOnly, "rename", false)
	expectProbe(t, readOnly, "ordinary", true)

	gitWrite := runProbe(t, executable, root, name, grants, []ProtectedPath{
		{Path: gitDir, Mode: ProtectGitWrite},
		{Path: filepath.Join(gitDir, "hooks"), Mode: ProtectReadOnly, Create: true, Dir: true},
		{Path: filepath.Join(gitDir, "config"), Mode: ProtectReadOnly, Create: true},
	},
		"object|write|"+filepath.Join(gitDir, "objects", "new"),
		"hook|write|"+filepath.Join(gitDir, "hooks", "pre-commit"),
		"config|write|"+filepath.Join(gitDir, "config"),
		"configdelete|delete|"+filepath.Join(gitDir, "config"),
		"hooksrename|rename|"+filepath.Join(gitDir, "hooks")+">"+filepath.Join(gitDir, "hooks-old"),
		"rename|rename|"+gitDir+">"+filepath.Join(root, "moved"))
	expectProbe(t, gitWrite, "object", true)
	expectProbe(t, gitWrite, "hook", false)
	expectProbe(t, gitWrite, "config", false)
	expectProbe(t, gitWrite, "configdelete", false)
	expectProbe(t, gitWrite, "hooksrename", false)
	expectProbe(t, gitWrite, "rename", false)

	// Every deny is temporary; nothing for the container stays in .git.
	for _, path := range []string{gitDir, filepath.Join(gitDir, "config"), filepath.Join(gitDir, "hooks"), filepath.Join(gitDir, "HEAD")} {
		if hasSIDEntry(t, path, sid) {
			t.Fatalf("container ACE left on %s after the launch", path)
		}
	}
}

func TestConcurrentAppContainerWorkersCannotCrossWorkspaceGrants(t *testing.T) {
	base := t.TempDir()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type worker struct {
		root, own, other string
		process          *Process
	}
	workers := make([]worker, 2)
	for index := range workers {
		root := filepath.Join(base, fmt.Sprintf("worker-%d", index))
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		workers[index].root = root
		workers[index].own = filepath.Join(root, "own.txt")
		workers[index].other = filepath.Join(base, fmt.Sprintf("worker-%d", 1-index), "cross.txt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for index := range workers {
		process, startErr := Start(ctx, Spec{Command: command,
			Args: []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-isolation-helper", workers[index].own, workers[index].other},
			Dir:  workers[index].root, Mode: ModeAppContainer, Network: NetworkOff,
			Grants:           []Grant{{Root: workers[index].root, Access: AccessWrite}, {Root: filepath.Dir(command), Access: AccessRead}},
			AppContainerName: fmt.Sprintf("FreeAgentTestWorker%d-%d", index, time.Now().UnixNano())})
		if startErr != nil {
			t.Fatal(startErr)
		}
		workers[index].process = process
		defer process.Close()
		_ = process.Stdin().Close()
	}
	for index := range workers {
		stdout, readErr := io.ReadAll(workers[index].process.Stdout())
		if readErr != nil {
			t.Fatal(readErr)
		}
		exit, waitErr := workers[index].process.Wait(ctx)
		if waitErr != nil || exit.Code != 0 || !strings.Contains(string(stdout), "own=<nil>") {
			t.Fatalf("worker %d exit=%+v err=%v output=%q", index, exit, waitErr, stdout)
		}
		if _, err := os.Stat(workers[index].other); err == nil {
			t.Fatalf("worker %d crossed into sibling workspace", index)
		}
	}
}

func TestAppContainerNetworkOffRejectsLoopback(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := Start(ctx, Spec{Command: command,
		Args: []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-loopback-helper", listener.Addr().String()},
		Mode: ModeAppContainer, Network: NetworkOff, Grants: []Grant{{Root: filepath.Dir(command), Access: AccessRead}},
		AppContainerName: fmt.Sprintf("FreeAgentTestLoopback%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stdout), "connect=<nil>") {
		t.Fatalf("AppContainer unexpectedly reached loopback without exemption: %q", stdout)
	}
}

func TestNodeModulesShapedGrantLaunchReportsCost(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	nodeModules := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1000; index++ {
		packageRoot := filepath.Join(nodeModules, fmt.Sprintf("package-%04d", index))
		if err := os.MkdirAll(filepath.Join(packageRoot, "dist"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"fixture"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, "dist", "index.js"), []byte("module.exports = 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	privateRoot := filepath.Join(base, "private")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(privateRoot, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("private-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(nodeModules, "package-0999", "dist", "created.txt")
	protected := filepath.Join(root, ".freeagent")
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	process, err := Start(ctx, Spec{Command: command,
		Args: []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-helper", marker, secretPath, protected},
		Dir:  root, Mode: ModeAppContainer, Network: NetworkOff,
		Grants:         []Grant{{Root: root, Access: AccessWrite}, {Root: filepath.Dir(command), Access: AccessRead}},
		ProtectedPaths: []ProtectedPath{{Path: protected, Mode: ProtectReadOnly, Create: true, Dir: true}}, AppContainerName: fmt.Sprintf("FreeAgentTestNodeModules%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("node_modules-shaped grant launch failed: %v", err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	exit, waitErr := process.Wait(ctx)
	if waitErr != nil || exit.Code != 0 {
		t.Fatalf("node_modules-shaped worker exit=%+v err=%v output=%q", exit, waitErr, stdout)
	}
	if !strings.Contains(string(stdout), "appcontainer=1") {
		t.Fatalf("node_modules-shaped worker did not run in AppContainer: %q", stdout)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("inherited workspace grant did not reach a nested package: %v", err)
	}
	t.Logf("node_modules-shaped grant launch: directories=%d files=%d elapsed=%s", 2001, 2000, time.Since(started))
}

func TestAppContainerInternetStillRejectsLoopback(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := Start(ctx, Spec{Command: command,
		Args: []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-loopback-helper", listener.Addr().String()},
		Mode: ModeAppContainer, Network: NetworkInternet, Grants: []Grant{{Root: filepath.Dir(command), Access: AccessRead}},
		AppContainerName: fmt.Sprintf("FreeAgentTestInternetLoopback%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	stdout, err := io.ReadAll(process.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stdout), "connect=<nil>") {
		t.Fatalf("AppContainer unexpectedly reached loopback with internet capability: %q", stdout)
	}
}
