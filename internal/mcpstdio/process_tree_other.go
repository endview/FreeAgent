//go:build !unix && !windows

package mcpstdio

import (
	"errors"
	"os/exec"
)

type managedProcessTree interface {
	terminate()
	kill() error
	close() error
}

func configureManagedCommand(*exec.Cmd) {}

func attachManagedProcessTree(*exec.Cmd) (managedProcessTree, error) {
	return nil, errors.New("managed process trees are unsupported on this platform")
}
