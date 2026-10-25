//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package winlaunch

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func (*directProcessTree) wait(context.Context) error { return ErrUnsupported }

type directProcessTree struct{ process *exec.Cmd }

func configureProcessCommand(*exec.Cmd) {}

func attachProcessTree(command *exec.Cmd) (processTree, error) {
	if command == nil || command.Process == nil {
		return nil, errors.New("process launcher: process is not started")
	}
	return &directProcessTree{process: command}, nil
}

func (tree *directProcessTree) terminate() {
	if tree != nil && tree.process != nil && tree.process.Process != nil {
		_ = tree.process.Process.Signal(os.Interrupt)
	}
}

func (tree *directProcessTree) kill() error {
	if tree == nil || tree.process == nil || tree.process.Process == nil {
		return nil
	}
	return tree.process.Process.Kill()
}
