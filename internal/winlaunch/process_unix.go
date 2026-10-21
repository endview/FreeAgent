//go:build !windows && (aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package winlaunch

import (
	"errors"
	"os/exec"
	"syscall"
)

type unixProcessTree struct{ pid int }

func configureProcessCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(command *exec.Cmd) (processTree, error) {
	if command == nil || command.Process == nil {
		return nil, errors.New("process launcher: process is not started")
	}
	return &unixProcessTree{pid: command.Process.Pid}, nil
}

func (tree *unixProcessTree) terminate() {
	if tree == nil || tree.pid <= 0 {
		return
	}
	_ = syscall.Kill(-tree.pid, syscall.SIGTERM)
}

func (tree *unixProcessTree) kill() error {
	if tree == nil || tree.pid <= 0 {
		return nil
	}
	err := syscall.Kill(-tree.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
