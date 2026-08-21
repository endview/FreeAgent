//go:build windows

package mcpstdio

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type managedProcessTree interface {
	terminate()
	kill() error
	close() error
}

type windowsJobProcessTree struct {
	handle windows.Handle
	pid    uint32
}

func configureManagedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func attachManagedProcessTree(cmd *exec.Cmd) (managedProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (managedProcessTree, error) {
		return nil, errors.Join(cause, windows.CloseHandle(job))
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fail(err)
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return fail(err)
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	closeErr := windows.CloseHandle(process)
	if err := errors.Join(assignErr, closeErr); err != nil {
		return fail(err)
	}
	return &windowsJobProcessTree{handle: job, pid: uint32(cmd.Process.Pid)}, nil
}

func (tree *windowsJobProcessTree) terminate() {
	if tree == nil || tree.pid == 0 {
		return
	}
	// CTRL_BREAK is the only general non-forced termination request available
	// to an arbitrary Windows console process group. It is best-effort because
	// GUI and detached processes may have no console; the bounded Job kill is
	// still authoritative in the following phase.
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, tree.pid)
}

func (tree *windowsJobProcessTree) kill() error {
	if tree == nil || tree.handle == 0 {
		return nil
	}
	return windows.TerminateJobObject(tree.handle, 1)
}

func (tree *windowsJobProcessTree) close() error {
	if tree == nil || tree.handle == 0 {
		return nil
	}
	handle := tree.handle
	tree.handle = 0
	tree.pid = 0
	return windows.CloseHandle(handle)
}
