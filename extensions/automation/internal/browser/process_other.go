//go:build linux

package browser

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
func systemBrowserLocations() []string {
	return []string{"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome"}
}
func bindProcessTree(cmd *exec.Cmd) (func(), error) {
	// A separate minimal guardian owns only process cleanup. EOF also occurs
	// when the extension is killed, so grandchildren cannot outlive its pipe.
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	guard := exec.Command("/bin/sh", "-c", `while IFS= read -r line; do :; done; exec /bin/kill -KILL -- -"$1"`, "freeagent-browser-lifetime", strconv.Itoa(cmd.Process.Pid))
	guard.Stdin = reader
	guard.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = guard.Start(); err != nil {
		reader.Close()
		writer.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil, err
	}
	reader.Close()
	done := make(chan struct{})
	go func() { _ = guard.Wait(); close(done) }()
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); writer.Close(); <-done }, nil
}
