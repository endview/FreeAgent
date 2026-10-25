//go:build windows

package winlaunch

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestJobOwnerCrashHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--job-owner-crash-helper" {
			continue
		}
		if os.Args[i+1] == "heartbeat" {
			for {
				_ = os.WriteFile(os.Args[i+2], []byte(time.Now().String()), 0600)
				time.Sleep(20 * time.Millisecond)
			}
		}
		program, _ := os.Executable()
		child, err := Start(context.Background(), Spec{Command: program, Args: []string{"-test.run=TestJobOwnerCrashHelper", "--", "--job-owner-crash-helper", "heartbeat", os.Args[i+2]}, Mode: ModeHost, Env: os.Environ()})
		if err != nil {
			fmt.Println(err)
			os.Exit(3)
		}
		fmt.Println(child.PID())
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestWindowsJobKillsTreeWhenOwnerIsKilled(t *testing.T) {
	program, _ := os.Executable()
	marker := filepath.Join(t.TempDir(), "heartbeat.txt")
	owner := exec.Command(program, "-test.run=TestJobOwnerCrashHelper", "--", "--job-owner-crash-helper", "owner", marker)
	output, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	line := make(chan string, 1)
	go func() { text, _ := bufio.NewReader(output).ReadString('\n'); line <- strings.TrimSpace(text) }()
	var pid int
	select {
	case text := <-line:
		pid, err = strconv.Atoi(text)
		if err != nil {
			t.Fatal(text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not start child")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never wrote")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	result, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil || result != waitObject0 {
		t.Fatalf("owned child survived owner kill: wait=%d error=%v", result, err)
	}
	before, _ := os.ReadFile(marker)
	time.Sleep(100 * time.Millisecond)
	after, _ := os.ReadFile(marker)
	if string(before) != string(after) {
		t.Fatal("child still writes after Job termination")
	}
}
