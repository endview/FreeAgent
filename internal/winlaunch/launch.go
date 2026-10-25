// Package winlaunch owns the local child-process boundary used by FreeAgent.
// The Windows implementation is deliberately small so every child launch can
// share the same ordering and handle rules.
package winlaunch

import (
	"context"
	"errors"
	"io"
)

var ErrUnsupported = errors.New("process launcher is unsupported on this host")

type Mode string

const (
	ModeHost         Mode = "host_explicit"
	ModeAppContainer Mode = "appcontainer"
)

type NetworkMode string

const (
	NetworkOff      NetworkMode = "off"
	NetworkInternet NetworkMode = "internet"
)

type AccessMode string

const (
	AccessRead  AccessMode = "read"
	AccessWrite AccessMode = "write"
)

type Grant struct {
	Root   string
	Access AccessMode
	// Keep leaves the grant in place after the process exits, so a later
	// launch with the same AppContainerName reuses it instead of propagating
	// the ACL through the whole tree again. ReleaseKept removes it.
	Keep bool
}

// ProtectMode selects how a protected path is exposed to the AppContainer.
type ProtectMode string

const (
	// ProtectReadOnly lets the container read the subtree but not change it.
	ProtectReadOnly ProtectMode = "read_only"
	// ProtectGitWrite lets the container read and write inside the subtree
	// while the entry itself cannot be deleted, renamed or replaced.
	ProtectGitWrite ProtectMode = "git_write"
)

// ProtectedPath overrides one subtree's access for a single launch. The
// override is applied with a protected ACL so a writable workspace grant
// cannot inherit into it, and the previous ACL is restored on close.
type ProtectedPath struct {
	Path string
	Mode ProtectMode
	// Create makes a missing entry for the launch, a directory when Dir is
	// set and an empty file otherwise. A missing entry without Create is
	// skipped.
	Create bool
	Dir    bool
}

type Spec struct {
	Command        string
	Args           []string
	Dir            string
	Env            []string
	Mode           Mode
	Network        NetworkMode
	Grants         []Grant
	ProtectedPaths []ProtectedPath

	// AppContainerName is optional. An empty name creates a per-launch profile.
	// A non-empty name is owned by the caller and is required for kept grants;
	// each launch leaves exactly its own kept grants on that profile.
	AppContainerName string
	ProfileLabel     string
}

type Exit struct {
	Code int
}

type Process struct {
	pid      int
	identity string
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser

	wait     func(context.Context) (Exit, error)
	kill     func() error
	term     func()
	close    func() error
	treeWait func(context.Context) error
}

func (p *Process) PID() int {
	if p == nil {
		return 0
	}
	return p.pid
}
func (p *Process) Identity() string {
	if p == nil {
		return ""
	}
	return p.identity
}

// WaitTree verifies the owned containment boundary after termination. A root
// exit alone is insufficient evidence that descendants have stopped.
func (p *Process) WaitTree(ctx context.Context) error {
	if p == nil || p.treeWait == nil {
		return ErrUnsupported
	}
	return p.treeWait(ctx)
}

func (p *Process) Stdin() io.WriteCloser {
	if p == nil {
		return nil
	}
	return p.stdin
}

func (p *Process) Stdout() io.ReadCloser {
	if p == nil {
		return nil
	}
	return p.stdout
}

func (p *Process) Stderr() io.ReadCloser {
	if p == nil {
		return nil
	}
	return p.stderr
}

func (p *Process) Wait(ctx context.Context) (Exit, error) {
	if p == nil || p.wait == nil {
		return Exit{}, ErrUnsupported
	}
	return p.wait(ctx)
}

func (p *Process) Kill() error {
	if p == nil || p.kill == nil {
		return nil
	}
	return p.kill()
}

func (p *Process) Terminate() {
	if p != nil && p.term != nil {
		p.term()
	}
}

func (p *Process) Close() error {
	if p == nil || p.close == nil {
		return nil
	}
	return p.close()
}
