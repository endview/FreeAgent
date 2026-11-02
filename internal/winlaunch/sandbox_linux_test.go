//go:build linux

package winlaunch

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requireLinuxSandbox(t *testing.T) {
	t.Helper()
	if err := SandboxReason(t.Context()); err != nil {
		t.Skipf("NOT_SUPPORTED: %v", err)
	}
}

func sandboxFixture(t *testing.T) (Spec, string, string) {
	t.Helper()
	requireLinuxSandbox(t)
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	data := filepath.Join(base, "private")
	for _, path := range []string{workspace, data, filepath.Join(workspace, ".git"), filepath.Join(workspace, ".freeagent")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(data, "keys.json")
	if err := os.WriteFile(key, []byte("PRIVATE-TEST-CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Command: "/usr/bin/dash", Dir: workspace, Mode: ModeBubblewrap, Network: NetworkOff,
		Grants: []Grant{{Root: workspace, Access: AccessWrite}}, PrivatePaths: []string{data, key},
		ProtectedPaths: []ProtectedPath{{Path: filepath.Join(workspace, ".git"), Mode: ProtectReadOnly}, {Path: filepath.Join(workspace, ".freeagent"), Mode: ProtectReadOnly}}}
	return spec, workspace, key
}

func sandboxOutput(t *testing.T, spec Spec) (string, Exit) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	process, err := Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	_ = process.Stdin().Close()
	output := make(chan []byte, 1)
	go func() { raw, _ := io.ReadAll(process.Stdout()); output <- raw }()
	diagnostic := make(chan []byte, 1)
	go func() { raw, _ := io.ReadAll(process.Stderr()); diagnostic <- raw }()
	exit, err := process.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.WaitTree(ctx); err != nil {
		t.Fatal(err)
	}
	out, stderr := string(<-output), string(<-diagnostic)
	if exit.Code != 0 {
		t.Logf("sandbox stderr: %s", stderr)
	}
	return out, exit
}

func TestLinuxSandboxWorkspaceAndProtectedMetadata(t *testing.T) {
	spec, workspace, key := sandboxFixture(t)
	if err := os.Symlink(key, filepath.Join(workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	spec.Env = []string{"FA_API_KEY=DO-NOT-INHERIT", "SSH_AUTH_SOCK=/tmp/agent", "LD_PRELOAD=/tmp/untrusted.so"}
	visibleKey := filepath.Join(workspace, "keys.json")
	if err := os.WriteFile(visibleKey, []byte("PRIVATE-WORKSPACE-KEY"), 0600); err != nil {
		t.Fatal(err)
	}
	spec.PrivatePaths = append(spec.PrivatePaths, visibleKey)
	spec.Args = []string{"-c", `set -eu
printf safe > ordinary.txt
if cat '` + key + `' 2>/dev/null; then exit 21; fi
if cat alias 2>/dev/null; then exit 22; fi
if cat keys.json 2>/dev/null; then exit 27; fi
if unshare -Ur /bin/true 2>/dev/null; then exit 28; fi
if (printf bad > .git/commondir) 2>/dev/null; then exit 23; fi
if (printf bad > .freeagent/config.json) 2>/dev/null; then exit 24; fi
if [ -d /run ] || [ -n "${FA_API_KEY-}${SSH_AUTH_SOCK-}${LD_PRELOAD-}" ]; then exit 25; fi
if [ -e /proc/1/root` + key + ` ]; then exit 26; fi
printf namespace-ok`}
	output, exit := sandboxOutput(t, spec)
	if exit.Code != 0 || output != "namespace-ok" {
		t.Fatalf("exit=%d output=%q", exit.Code, output)
	}
	if raw, err := os.ReadFile(filepath.Join(workspace, "ordinary.txt")); err != nil || string(raw) != "safe" {
		t.Fatal("authorized workspace write did not persist")
	}
	for _, path := range []string{".git/commondir", ".freeagent/config.json"} {
		if _, err := os.Stat(filepath.Join(workspace, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("protected metadata changed: %s", path)
		}
	}
}

func TestLinuxSandboxReadOnlyAndNetworkOff(t *testing.T) {
	spec, workspace, _ := sandboxFixture(t)
	spec.Grants[0].Access = AccessRead
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("NOT_RUN: Python 3 is not installed")
	}
	spec.Command, err = filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	spec.Args = []string{"-c", `import os,socket
try:
 open('denied.txt','w').write('bad')
 raise AssertionError('read-only mount was writable')
except OSError: pass
for host,port in [('127.0.0.1',` + strconv.Itoa(port) + `),('1.1.1.1',443)]:
 s=socket.socket();s.settimeout(.3)
 try:
  s.connect((host,port));raise AssertionError('network escaped')
 except OSError:pass
 finally:s.close()
print('network-off')`}
	output, exit := sandboxOutput(t, spec)
	if exit.Code != 0 || strings.TrimSpace(output) != "network-off" {
		t.Fatalf("network test %q exit=%d", output, exit.Code)
	}
	if _, err := os.Stat(filepath.Join(workspace, "denied.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only write escaped")
	}
	spec.Network = NetworkInternet
	if p, err := Start(t.Context(), spec); !errors.Is(err, ErrUnsupported) {
		if p != nil {
			p.Close()
		}
		t.Fatalf("internet silently fell back: %v", err)
	}
}

func TestLinuxSandboxProtectedHardlinkAndOrdinaryProjectHardlink(t *testing.T) {
	spec, workspace, key := sandboxFixture(t)
	alias := filepath.Join(workspace, "key-copy")
	if err := os.Link(key, alias); err != nil {
		t.Fatal(err)
	}
	spec.Args = []string{"-c", "printf escaped > must-not-run"}
	if p, err := Start(t.Context(), spec); err == nil {
		p.Close()
		t.Fatal("protected hardlink did not prevent dispatch")
	}
	if _, err := os.Stat(filepath.Join(workspace, "must-not-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("payload ran after failed preflight")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(workspace), "pnpm-store-file")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(workspace, "ordinary-link")); err != nil {
		t.Fatal(err)
	}
	spec.Args = []string{"-c", "printf shared > ordinary-link"}
	_, exit := sandboxOutput(t, spec)
	if exit.Code != 0 {
		t.Fatal("ordinary project hardlink was rejected")
	}
	raw, _ := os.ReadFile(outside)
	if string(raw) != "shared" {
		t.Fatal("hardlink behavior differs from documented shared inode")
	}
}

func TestLinuxSandboxPrivateDirectoryHardlinksRefuseDispatch(t *testing.T) {
	for _, relative := range []string{"public-admin.json", "attachments/blobs/input", "blocked/deep/personal.txt"} {
		t.Run(relative, func(t *testing.T) {
			spec, workspace, _ := sandboxFixture(t)
			private := filepath.Join(spec.PrivatePaths[0], relative)
			if strings.HasPrefix(relative, "blocked/") {
				blocked := filepath.Join(filepath.Dir(workspace), "blocked")
				spec.PrivatePaths = append(spec.PrivatePaths, blocked)
				private = filepath.Join(blocked, strings.TrimPrefix(relative, "blocked/"))
			}
			if err := os.MkdirAll(filepath.Dir(private), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(private, []byte("private fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(private, filepath.Join(workspace, "alias")); err != nil {
				t.Fatal(err)
			}
			spec.Args = []string{"-c", "cat alias; printf escaped > must-not-run"}
			if process, err := Start(t.Context(), spec); err == nil {
				_ = process.Kill()
				_ = process.Close()
				t.Fatal("private-directory hardlink did not prevent dispatch")
			}
			if _, err := os.Stat(filepath.Join(workspace, "must-not-run")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("payload ran after private-directory preflight failed")
			}
		})
	}
}

func TestLinuxSandboxKillsSetsidDescendants(t *testing.T) {
	spec, workspace, _ := sandboxFixture(t)
	spec.Args = []string{"-c", `setsid /bin/sh -c 'while true; do date +%s%N > heartbeat; sleep .05; done' & wait`}
	process, err := Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	process.Stdin().Close()
	go io.Copy(io.Discard, process.Stdout())
	go io.Copy(io.Discard, process.Stderr())
	heartbeat := filepath.Join(workspace, "heartbeat")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if raw, _ := os.ReadFile(heartbeat); len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sandbox child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := process.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := process.WaitTree(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(heartbeat)
	time.Sleep(180 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if string(before) != string(after) {
		t.Fatal("setsid descendant survived namespace cleanup")
	}
}

func TestLinuxSandboxOwnerHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--bwrap-owner-helper" {
		return
	}
	work := os.Args[len(os.Args)-1]
	p, err := Start(context.Background(), Spec{Command: "/usr/bin/dash", Args: []string{"-c", `setsid /bin/sh -c 'while true; do date +%s%N > heartbeat; sleep .05; done' & wait`}, Dir: work, Mode: ModeBubblewrap, Network: NetworkOff, Grants: []Grant{{Root: work, Access: AccessWrite}}})
	if err != nil {
		os.Exit(23)
	}
	p.Stdin().Close()
	select {}
}

func TestLinuxSandboxOwnerDeathKillsNamespace(t *testing.T) {
	requireLinuxSandbox(t)
	workspace := t.TempDir()
	binary, _ := os.Executable()
	owner := exec.Command(binary, "-test.run=^TestLinuxSandboxOwnerHelper$", "--", "--bwrap-owner-helper", workspace)
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer owner.Process.Kill()
	heartbeat := filepath.Join(workspace, "heartbeat")
	deadline := time.Now().Add(4 * time.Second)
	for {
		if raw, _ := os.ReadFile(heartbeat); len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start namespace child")
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = owner.Process.Kill()
	_ = owner.Wait()
	time.Sleep(150 * time.Millisecond)
	before, _ := os.ReadFile(heartbeat)
	time.Sleep(200 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if string(before) != string(after) {
		t.Fatal("Owner SIGKILL left namespace descendants executing")
	}
}
