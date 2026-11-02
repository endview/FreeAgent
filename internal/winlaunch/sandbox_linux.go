//go:build linux

package winlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var linuxProbe struct {
	sync.Mutex
	until time.Time
	stamp time.Time
	err   error
}

func trustedBubblewrap() (string, error) {
	const path = "/usr/bin/bwrap"
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: install the optional Debian bubblewrap package", ErrUnsupported)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return "", fmt.Errorf("%w: bwrap must be a root-owned non-setuid system executable", ErrUnsupported)
	}
	if size, _ := unix.Getxattr(path, "security.capability", nil); size > 0 {
		return "", fmt.Errorf("%w: privileged bwrap is not supported", ErrUnsupported)
	}
	return path, nil
}

func SandboxSupported() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return SandboxReason(ctx) == nil
}

func SandboxReason(ctx context.Context) error {
	path, err := trustedBubblewrap()
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	linuxProbe.Lock()
	defer linuxProbe.Unlock()
	if time.Now().Before(linuxProbe.until) && info.ModTime() == linuxProbe.stamp {
		return linuxProbe.err
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	process, err := startSandbox(probe, Spec{Command: "/usr/bin/true", Dir: "/tmp", Mode: ModeBubblewrap, Network: NetworkOff})
	if err == nil {
		_ = process.Stdin().Close()
		go io.Copy(io.Discard, process.Stdout())
		go io.Copy(io.Discard, process.Stderr())
		var exit Exit
		exit, err = process.Wait(probe)
		if err == nil && exit.Code != 0 {
			err = errors.New("bwrap namespace probe exited unsuccessfully")
		}
		if err == nil {
			err = process.WaitTree(probe)
		}
		err = errors.Join(err, process.Close())
	}
	if err != nil {
		err = fmt.Errorf("%w: Linux user/PID/mount/network namespace probe failed: %v", ErrUnsupported, err)
	}
	linuxProbe.until, linuxProbe.stamp, linuxProbe.err = time.Now().Add(2*time.Second), info.ModTime(), err
	return err
}

func startSandbox(ctx context.Context, spec Spec) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("process launcher: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bwrap, err := trustedBubblewrap()
	if err != nil {
		return nil, err
	}
	if spec.Network != NetworkOff {
		return nil, fmt.Errorf("%w: Linux sandbox supports network-off only; explicit Full uses host execution", ErrUnsupported)
	}
	denied, err := os.CreateTemp("", "freeagent-bwrap-denied-*")
	if err != nil {
		return nil, err
	}
	deniedPath := denied.Name()
	keepDenied := false
	defer func() {
		if !keepDenied {
			_ = os.Remove(deniedPath)
		}
	}()
	if err := denied.Chmod(0); err != nil {
		denied.Close()
		return nil, err
	}
	if err := denied.Close(); err != nil {
		return nil, err
	}
	args, err := bubblewrapArgs(spec, deniedPath)
	if err != nil {
		return nil, err
	}
	infoRead, infoWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer infoRead.Close()
	defer infoWrite.Close()
	blockRead, blockWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer blockRead.Close()
	defer blockWrite.Close()
	args = append(args, "--info-fd", "3", "--block-fd", "4", "--", spec.Command)
	args = append(args, spec.Args...)
	commandSpec := Spec{Command: bwrap, Args: args, Dir: "/", Mode: ModeHost, Env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}}
	var namespace *linuxNamespaceTree
	process, err := startOther(ctx, commandSpec, []*os.File{infoWrite, blockRead}, func(tree processTree, cmd *exec.Cmd) (processTree, error) {
		_ = infoWrite.Close()
		_ = blockRead.Close()
		_ = infoRead.SetReadDeadline(time.Now().Add(3 * time.Second))
		var status struct {
			PID int `json:"child-pid"`
		}
		if err := json.NewDecoder(io.LimitReader(infoRead, 4096)).Decode(&status); err != nil || status.PID <= 0 {
			return nil, fmt.Errorf("%w: bwrap did not establish a tracked PID namespace", ErrUnsupported)
		}
		own, _ := os.Readlink("/proc/self/ns/pid")
		child, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", status.PID))
		if err != nil || child == own {
			return nil, fmt.Errorf("%w: separate PID namespace was not observed", ErrUnsupported)
		}
		pidfd, err := unix.PidfdOpen(status.PID, 0)
		if err != nil {
			return nil, fmt.Errorf("%w: kernel pidfd tracking unavailable", ErrUnsupported)
		}
		namespace = &linuxNamespaceTree{parent: tree, fd: pidfd}
		if _, err := blockWrite.Write([]byte{1}); err != nil {
			namespace.close()
			return nil, err
		}
		return namespace, nil
	})
	if err != nil {
		if namespace != nil {
			namespace.close()
		}
		return nil, err
	}
	originalClose := process.close
	process.close = func() error {
		err := originalClose()
		removeErr := os.Remove(deniedPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return errors.Join(err, removeErr)
	}
	keepDenied = true
	return process, nil
}

// Begin with an empty mount tree. No user HOME, host /run, sockets, arbitrary
// /etc or host root is mounted. Authorized project hardlinks remain shared.
func bubblewrapArgs(spec Spec, deniedPath string) ([]string, error) {
	if !filepath.IsAbs(spec.Command) || !filepath.IsAbs(spec.Dir) {
		return nil, errors.New("sandbox: absolute command and working directory required")
	}
	real, err := filepath.EvalSymlinks(spec.Command)
	if err != nil || real != spec.Command {
		return nil, errors.New("sandbox: command must be the resolved executable")
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--disable-userns", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv"}
	for _, root := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
		info, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, private := range spec.PrivatePaths {
			if pathUnder(root, private) {
				return nil, errors.New("sandbox: system installation overlaps private application state")
			}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(root)
			if err != nil {
				return nil, err
			}
			args = append(args, "--symlink", target, root)
		} else {
			args = append(args, "--ro-bind", root, root)
		}
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/home/freeagent", "--dir", "/etc")
	for _, path := range []string{"/etc/ld.so.cache", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			args = append(args, "--ro-bind", path, path)
		}
	}
	for _, grant := range spec.Grants {
		if grant.Access != AccessRead && grant.Access != AccessWrite {
			return nil, errors.New("sandbox: invalid mount access")
		}
		root, err := filepath.EvalSymlinks(grant.Root)
		if err != nil || root != filepath.Clean(grant.Root) || root == "/" {
			return nil, errors.New("sandbox: grant must be an existing canonical directory")
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, errors.New("sandbox: grant is not a directory")
		}
		for _, private := range spec.PrivatePaths {
			if root == private || pathUnder(private, root) {
				return nil, errors.New("sandbox: grant is private application state")
			}
			if grant.Access == AccessRead && pathUnder(root, private) {
				return nil, errors.New("sandbox: installation root contains private application state")
			}
		}
		flag := "--ro-bind"
		if grant.Access == AccessWrite {
			flag = "--bind"
		}
		args = append(args, flag, root, root)
	}
	for _, protected := range spec.ProtectedPaths {
		if protected.Mode != ProtectReadOnly {
			return nil, errors.New("sandbox: invalid protected mode")
		}
		info, err := os.Lstat(protected.Path)
		if errors.Is(err, os.ErrNotExist) && protected.Create {
			if protected.Dir {
				err = os.Mkdir(protected.Path, 0700)
			} else {
				var file *os.File
				file, err = os.OpenFile(protected.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if file != nil {
					file.Close()
				}
			}
			if err != nil {
				return nil, err
			}
			info, err = os.Lstat(protected.Path)
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("sandbox: protected mount cannot be a symbolic link")
		}
		args = append(args, "--ro-bind", protected.Path, protected.Path)
	}
	for _, path := range spec.PrivatePaths {
		if path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("sandbox: private state is a symbolic link")
		}
		files := []string{path}
		if info.IsDir() {
			files = nil
			for _, name := range []string{"config.json", "state.sqlite", "state.sqlite-wal", "state.sqlite-shm", "owner.lock", "engine.json", "attach.secret", "chat.secret", "control-journal.jsonl", "public.credentials.json"} {
				files = append(files, filepath.Join(path, name))
			}
		}
		for _, file := range files {
			if stat, err := os.Lstat(file); err == nil {
				raw, ok := stat.Sys().(*syscall.Stat_t)
				if !ok || stat.Mode()&os.ModeSymlink != 0 || stat.Mode().IsRegular() && raw.Nlink != 1 {
					return nil, errors.New("sandbox: protected state has a link; remove the alias before execution")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		visible := false
		for _, grant := range spec.Grants {
			visible = visible || pathUnder(grant.Root, path)
		}
		if visible {
			if info.IsDir() {
				args = append(args, "--tmpfs", path, "--remount-ro", path)
			} else {
				args = append(args, "--ro-bind", deniedPath, path)
			}
		}
	}
	// Explicit whitelist. Host variables (including agents, credentials, loader
	// settings and service-private values) never enter the sandbox.
	env := map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/home/freeagent", "TMPDIR": "/tmp", "LANG": "C.UTF-8", "GOCACHE": "/tmp/go-cache", "GOPATH": "/home/freeagent/go", "NPM_CONFIG_CACHE": "/tmp/npm-cache", "PYTHONPYCACHEPREFIX": "/tmp/python-cache"}
	for _, entry := range spec.Env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && (name == "LANG" || name == "LC_ALL" || name == "TERM") {
			env[name] = value
		}
	}
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TERM", "GOCACHE", "GOPATH", "NPM_CONFIG_CACHE", "PYTHONPYCACHEPREFIX"} {
		if value, ok := env[name]; ok {
			args = append(args, "--setenv", name, value)
		}
	}
	args = append(args, "--chdir", spec.Dir)
	return args, nil
}

func pathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// A pidfd pins the namespace's init process, so cleanup never signals a reused
// numeric PID. Its exit proves the kernel has killed the PID namespace.
type linuxNamespaceTree struct {
	parent processTree
	fd     int
	mu     sync.Mutex
	closed bool
}

func (tree *linuxNamespaceTree) signal(signal syscall.Signal) error {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.closed {
		return nil
	}
	err := unix.PidfdSendSignal(tree.fd, unix.Signal(signal), nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
func (tree *linuxNamespaceTree) terminate() { _ = tree.signal(syscall.SIGTERM) }
func (tree *linuxNamespaceTree) kill() error {
	return errors.Join(tree.signal(syscall.SIGKILL), tree.parent.kill())
}
func (tree *linuxNamespaceTree) wait(ctx context.Context) error {
	for {
		tree.mu.Lock()
		if tree.closed {
			tree.mu.Unlock()
			return errors.New("sandbox: namespace observation was closed")
		}
		poll := []unix.PollFd{{Fd: int32(tree.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 20)
		tree.mu.Unlock()
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if n > 0 {
			return tree.parent.wait(ctx)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
func (tree *linuxNamespaceTree) close() error {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.closed {
		return nil
	}
	tree.closed = true
	return unix.Close(tree.fd)
}
