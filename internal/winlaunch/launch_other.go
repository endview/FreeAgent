//go:build !windows

package winlaunch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// SandboxSupported reports whether this build can launch AppContainer workers.
func SandboxSupported() bool { return false }

// ReleaseKept is a no-op: kept grants exist only for Windows AppContainers.
func ReleaseKept(string) error { return nil }

func Start(ctx context.Context, spec Spec) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("process launcher: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.Mode == ModeAppContainer {
		return nil, ErrUnsupported
	}
	if spec.Command == "" || !filepath.IsAbs(spec.Command) {
		return nil, errors.New("process launcher: command must be an absolute path")
	}
	command := exec.Command(spec.Command, spec.Args...)
	command.Dir = spec.Dir
	if spec.Env != nil {
		command.Env = append([]string(nil), spec.Env...)
	}
	configureProcessCommand(command)
	// Own the pipes: the StdoutPipe helpers are closed by cmd.Wait as soon as
	// the child exits, which races with readers that have not drained the
	// child's last output.
	var files []*os.File
	closeFiles := func() {
		for _, file := range files {
			_ = file.Close()
		}
	}
	pipe := func() (*os.File, *os.File, error) {
		read, write, err := os.Pipe()
		if err == nil {
			files = append(files, read, write)
		}
		return read, write, err
	}
	childStdin, stdin, err := pipe()
	if err != nil {
		return nil, err
	}
	stdout, childStdout, err := pipe()
	if err != nil {
		closeFiles()
		return nil, err
	}
	stderr, childStderr, err := pipe()
	if err != nil {
		closeFiles()
		return nil, err
	}
	command.Stdin, command.Stdout, command.Stderr = childStdin, childStdout, childStderr
	if err := command.Start(); err != nil {
		closeFiles()
		return nil, err
	}
	// The child holds its own copies of these ends.
	_ = childStdin.Close()
	_ = childStdout.Close()
	_ = childStderr.Close()
	process := &otherProcess{cmd: command, stdin: stdin, stdout: stdout, stderr: stderr,
		waitDone: make(chan struct{})}
	process.tree, err = attachProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	go process.waitBackground()
	return &Process{stdin: stdin, stdout: stdout, stderr: stderr,
		wait: process.wait, kill: process.kill, term: process.terminate, close: process.close}, nil
}

type otherProcess struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	tree     processTree
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	waitDone chan struct{}
	exit     Exit
	waitErr  error
	closed   bool
}

type processTree interface {
	terminate()
	kill() error
}

func (process *otherProcess) waitBackground() {
	err := process.cmd.Wait()
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			process.waitErr = err
		}
	}
	if process.cmd.ProcessState != nil {
		process.exit.Code = process.cmd.ProcessState.ExitCode()
	}
	close(process.waitDone)
}

func (process *otherProcess) wait(ctx context.Context) (Exit, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-process.waitDone:
		return process.exit, process.waitErr
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	}
}

func (process *otherProcess) terminate() {
	if process != nil && process.tree != nil {
		process.tree.terminate()
	}
}

func (process *otherProcess) kill() error {
	if process == nil {
		return nil
	}
	// Descendants in the process group can outlive the direct child, so the
	// tree is killed even after Wait returned.
	if process.tree != nil {
		if err := process.tree.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	select {
	case <-process.waitDone:
		return nil
	default:
	}
	return process.cmd.Process.Kill()
}

func (process *otherProcess) close() error {
	if process == nil {
		return nil
	}
	process.mu.Lock()
	if process.closed {
		process.mu.Unlock()
		return nil
	}
	process.closed = true
	process.mu.Unlock()
	result := process.kill()
	for _, pipe := range []io.Closer{process.stdin, process.stdout, process.stderr} {
		if err := pipe.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}
