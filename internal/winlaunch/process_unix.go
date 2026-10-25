//go:build !windows && (aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package winlaunch

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

type unixProcessTree struct{ pid int }

func configureProcessCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (tree *unixProcessTree) wait(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Kill(-tree.pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
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
