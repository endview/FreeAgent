//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/winlaunch"
	"golang.org/x/sys/windows"
)

const (
	engineFirstLineTimeout = 15 * time.Second
	engineShutdownTimeout  = 10 * time.Second
)

var engineFirstLineWait = engineFirstLineTimeout

type engineOptions struct {
	Executable  string
	EnginePath  string
	EngineArgs  []string
	DataDir     string
	Port        int
	OpenBrowser bool
	LogDir      string
}

type engineProcess struct {
	process *winlaunch.Process
	path    string
	stdin   io.WriteCloser
	logFile *os.File

	mu         sync.Mutex
	closed     bool
	waitOnce   sync.Once
	waitDone   chan error
	finishOnce sync.Once
}

func engineExecutable(exe string) (string, error) {
	exe, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("locate desktop executable: %w", err)
	}
	root, portable, err := apppaths.PackageRootOf(filepath.Dir(exe))
	if err != nil {
		return "", err
	}
	if !portable {
		root = filepath.Dir(exe)
	}
	candidate := filepath.Join(root, "bin", "freeagent.exe")
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("FreeAgent engine was not found at %s: %w", candidate, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("FreeAgent engine is not a file: %s", candidate)
	}
	return candidate, nil
}

func runCLI(ctx context.Context, executable string, args []string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var stdout, stderr bytes.Buffer
	command.Stdout = newBoundedLogWriter(&stdout, engineLogLimit)
	command.Stderr = newBoundedLogWriter(&stderr, engineLogLimit)
	err := command.Run()
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), -1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.String(), stderr.String(), exit.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), 0, err
}

func startEngine(ctx context.Context, options engineOptions) (*engineProcess, engineLaunch, error) {
	var err error
	if options.Executable == "" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, engineLaunch{}, executableErr
		}
		options.Executable = executable
	}
	enginePath := options.EnginePath
	if enginePath == "" {
		enginePath, err = engineExecutable(options.Executable)
		if err != nil {
			return nil, engineLaunch{}, err
		}
	}
	if err := ensureWritableDirectory(options.LogDir); err != nil {
		return nil, engineLaunch{}, fmt.Errorf("desktop log directory: %w", err)
	}
	logPath := filepath.Join(options.LogDir, "engine.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, engineLaunch{}, fmt.Errorf("desktop engine log: %w", err)
	}
	fail := func(cause error) (*engineProcess, engineLaunch, error) {
		_ = logFile.Close()
		detail := lastLogLines(logPath, 20)
		if detail == "" {
			return nil, engineLaunch{}, fmt.Errorf("%w; see %s", cause, logPath)
		}
		return nil, engineLaunch{}, fmt.Errorf("%w; last engine output:\n%s; see %s", cause, detail, logPath)
	}

	args := options.EngineArgs
	if len(args) == 0 {
		args = []string{"web", "--data-dir", options.DataDir, "--port", strconv.Itoa(options.Port), "--exit-on-stdin-close"}
		if options.OpenBrowser {
			args = append(args, "--open-browser")
		}
	}
	worker, err := winlaunch.Start(ctx, winlaunch.Spec{
		Command: enginePath, Args: args, Env: os.Environ(), Mode: winlaunch.ModeHost,
	})
	if err != nil {
		return fail(fmt.Errorf("start engine: %w", err))
	}
	process := &engineProcess{process: worker, path: enginePath, stdin: worker.Stdin(), logFile: logFile, waitDone: make(chan error, 1)}
	stdout, stderr := worker.Stdout(), worker.Stderr()

	logWriter := newBoundedLogWriter(logFile, engineLogLimit)
	go func() {
		_, _ = io.Copy(logWriter, stderr)
	}()
	firstLine := make(chan string, 1)
	firstErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, readErr := reader.ReadString('\n')
		if line != "" {
			firstLine <- line
		} else {
			firstErr <- readErr
			return
		}
		_, _ = io.Copy(logWriter, reader)
	}()

	timer := time.NewTimer(engineFirstLineWait)
	defer timer.Stop()
	select {
	case line := <-firstLine:
		launch, parseErr := parseEngineFirstLine(line)
		if parseErr != nil {
			_ = process.closeWithTimeout(2 * time.Second)
			return fail(parseErr)
		}
		return process, launch, nil
	case err := <-firstErr:
		_ = process.closeWithTimeout(2 * time.Second)
		if err == nil {
			err = errors.New("engine exited without a launch address")
		}
		return fail(err)
	case <-timer.C:
		_ = process.closeWithTimeout(2 * time.Second)
		return fail(errors.New("timed out waiting for engine launch address"))
	case <-ctx.Done():
		_ = process.closeWithTimeout(2 * time.Second)
		return fail(ctx.Err())
	}
}

func (p *engineProcess) closeWithTimeout(timeout time.Duration) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	_ = p.stdin.Close()
	done := p.wait()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		p.finish()
		return normalizeProcessExit(err)
	case <-timer.C:
		_ = p.process.Kill()
		err := <-done
		p.finish()
		if err == nil {
			return context.DeadlineExceeded
		}
		return normalizeProcessExit(err)
	}
}

func (p *engineProcess) finish() {
	p.finishOnce.Do(func() {
		_ = p.process.Close()
		if p.logFile != nil {
			_ = p.logFile.Close()
			p.logFile = nil
		}
	})
}

func (p *engineProcess) wait() <-chan error {
	p.waitOnce.Do(func() {
		go func() {
			_, err := p.process.Wait(context.Background())
			p.waitDone <- err
		}()
	})
	return p.waitDone
}

func normalizeProcessExit(err error) error {
	return err
}

func (p *engineProcess) logPath() string {
	if p.logFile == nil {
		return ""
	}
	return p.logFile.Name()
}

func lastLogLines(path string, count int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
