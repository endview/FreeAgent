//go:build !windows

package harness

import (
	"errors"
	"os/exec"
)

func configureSandboxedCommand(*exec.Cmd, NetworkPolicy) (func(), error) {
	return nil, errors.Join(ErrSandboxUnsupported, errors.New("no Windows sandbox backend is available on this host"))
}
