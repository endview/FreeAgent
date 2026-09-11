//go:build windows

package mcpstdio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandTransportStartsLongWindowsPath(t *testing.T) {
	root := t.TempDir()
	dir := root
	for len(dir) <= 260 {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	t.Cleanup(func() { _ = os.RemoveAll(extendedPath(dir)) })
	if err := os.MkdirAll(extendedPath(dir), 0o700); err != nil {
		t.Fatal(err)
	}

	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executableBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "long-helper.exe")
	if err := os.WriteFile(
		extendedPath(executable),
		executableBytes,
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if len(executable) <= 260 {
		t.Fatalf("test executable path is not long: %q", executable)
	}

	transport, err := newCommandTransport(
		executable,
		helperArguments("graceful"),
		dir,
		[]string{helperGORACEEnvironment()},
		1024,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() on long path: %v", err)
	}
	concrete := conn.(*commandConnection)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() on long path: %v", err)
	}
	if concrete.cmd.ProcessState == nil {
		t.Fatal("long-path subprocess was not reaped")
	}
}
