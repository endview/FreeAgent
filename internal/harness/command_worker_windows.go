//go:build windows

package harness

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const disableMaxPrivilege = 0x1

var createRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// configureSandboxedCommand applies a restricted primary token. The process
// tree is still placed in the existing Job Object by the command runner. This
// backend is deliberately described as limited: it is not an AppContainer and
// does not claim complete filesystem or network isolation.
func configureSandboxedCommand(cmd *exec.Cmd, network NetworkPolicy) (func(), error) {
	if cmd == nil {
		return nil, errors.New("harness: sandbox worker received a nil command")
	}
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ASSIGN_PRIMARY |
		windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID)
	var current windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), access, &current)
	if err != nil {
		return nil, fmt.Errorf("%w: open process token: %v", ErrSandboxUnsupported, err)
	}
	defer current.Close()
	var restricted windows.Token
	result, _, callErr := createRestrictedToken.Call(
		uintptr(current),
		uintptr(disableMaxPrivilege),
		0, 0,
		0, 0,
		0, 0,
		uintptr(unsafe.Pointer(&restricted)),
	)
	if result == 0 || restricted == 0 {
		if callErr == nil {
			callErr = syscall.EINVAL
		}
		return nil, fmt.Errorf("%w: CreateRestrictedToken: %v", ErrSandboxUnsupported, callErr)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(restricted)
	// os/exec's handle list contains only stdin/stdout/stderr. Keep those
	// handles available while preventing unrelated parent handles from leaking.
	cmd.SysProcAttr.NoInheritHandles = false
	// Restricted-token mode does not provide a complete network namespace. A
	// future AppContainer backend will enforce off/allowlist policies; until
	// then fail closed rather than implying network enforcement.
	if network.Mode != NetworkHostInherited {
		_ = restricted.Close()
		return nil, fmt.Errorf("%w: network isolation requires an AppContainer backend", ErrSandboxUnsupported)
	}
	return func() { _ = restricted.Close() }, nil
}
