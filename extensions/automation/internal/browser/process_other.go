//go:build linux

package browser

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
func systemBrowserLocations() []string {
	return []string{"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome"}
}
func bindProcessTree(cmd *exec.Cmd) (func(), error) {
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }, nil
}
