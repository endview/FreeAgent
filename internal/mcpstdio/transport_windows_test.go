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

func TestLongPathLaunchFallbackUsesExtendedPaths(t *testing.T) {
	longPath := filepath.Join(
		`C:\`,
		strings.Repeat(`a\`, 160),
		"helper.exe",
	)
	if len(longPath) <= windowsMaxPath {
		t.Fatalf("test path is not long: %q", longPath)
	}
	if got := launchPath(longPath); !strings.HasPrefix(got, windowsExtendedPathPrefix) {
		t.Fatalf("launchPath() = %q, want extended path fallback", got)
	}
	dir, direct := launchDir(longPath)
	if direct {
		t.Fatalf("launchDir() direct = true, want mapped working directory fallback")
	}
	if !strings.HasPrefix(dir, windowsExtendedPathPrefix) {
		t.Fatalf("launchDir() = %q, want extended path fallback", dir)
	}
}

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

func TestCommandTransportStartsVeryLongWindowsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < 60; i++ {
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
	executable := filepath.Join(dir, "very-long-helper.exe")
	if err := os.WriteFile(
		extendedPath(executable),
		executableBytes,
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	_, direct := launchDir(dir)
	if direct {
		t.Fatal("very long working directory unexpectedly selected a direct path")
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("Connect() on very long working directory: %v", err)
	}
	after, err := os.Getwd()
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if before != after {
		_ = conn.Close()
		t.Fatalf("working directory changed: before=%q after=%q", before, after)
	}
	concrete := conn.(*commandConnection)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() on very long working directory: %v", err)
	}
	if concrete.cmd.ProcessState == nil {
		t.Fatal("very-long-directory subprocess was not reaped")
	}
}
