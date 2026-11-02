package harness

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

// SandboxStatus answers one question for doctor: can command_run start an
// AppContainer worker on this machine right now.
type SandboxStatus struct {
	Backend string `json:"backend"`
	Network string `json:"network"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// CheckSandbox launches cmd.exe in a throwaway AppContainer with no grants
// and no network. It touches no workspace and leaves no profile behind.
func CheckSandbox(ctx context.Context) SandboxStatus {
	if runtime.GOOS == "linux" {
		if err := winlaunch.SandboxReason(ctx); err != nil {
			return SandboxStatus{Status: "unsupported", Backend: winlaunch.SandboxBackend(), Network: "off only", Detail: safeError(err)}
		}
		return SandboxStatus{Status: "ready", Backend: winlaunch.SandboxBackend(), Network: "off only", Detail: "user/PID/mount/network namespaces and pidfd cleanup verified; project hardlinks share host content"}
	}
	if !winlaunch.SandboxSupported() {
		return SandboxStatus{Status: "unsupported", Backend: winlaunch.SandboxBackend(), Detail: "no command sandbox is implemented on this platform"}
	}
	system := os.Getenv("SystemRoot")
	if system == "" {
		return SandboxStatus{Status: "failed", Detail: "SystemRoot is not set"}
	}
	system32 := filepath.Join(system, "System32")
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	process, err := winlaunch.Start(ctx, winlaunch.Spec{Command: filepath.Join(system32, "cmd.exe"),
		Args: []string{"/d", "/c", "exit 0"}, Dir: system32, Mode: winlaunch.ModeAppContainer, Network: winlaunch.NetworkOff})
	if err != nil {
		return SandboxStatus{Status: "failed", Detail: safeError(err)}
	}
	defer process.Close()
	_ = process.Stdin().Close()
	go func() { _, _ = io.Copy(io.Discard, process.Stdout()) }()
	go func() { _, _ = io.Copy(io.Discard, process.Stderr()) }()
	exit, err := process.Wait(ctx)
	if err != nil {
		return SandboxStatus{Status: "failed", Detail: safeError(err)}
	}
	if exit.Code != 0 {
		return SandboxStatus{Status: "failed", Detail: "sandbox worker exited with a non-zero code"}
	}
	return SandboxStatus{Status: "ready", Backend: winlaunch.SandboxBackend(), Network: "off/internet", Detail: "AppContainer worker started and exited"}
}
