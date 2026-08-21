//go:build unix

package mcpstdio

import (
	"errors"
	"os/exec"
	"syscall"
)

type managedProcessTree interface {
	terminate()
	kill() error
	close() error
}

type unixProcessGroup struct {
	pid int
}

func configureManagedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachManagedProcessTree(cmd *exec.Cmd) (managedProcessTree, error) {
	return &unixProcessGroup{pid: cmd.Process.Pid}, nil
}

func (group *unixProcessGroup) terminate() {
	if group == nil || group.pid <= 0 {
		return
	}
	_ = syscall.Kill(-group.pid, syscall.SIGTERM)
}

func (group *unixProcessGroup) kill() error {
	if group == nil || group.pid <= 0 {
		return nil
	}
	err := syscall.Kill(-group.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (*unixProcessGroup) close() error { return nil }
