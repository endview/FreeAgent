//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEngineFirstLineTimeout(t *testing.T) {
	previous := engineFirstLineWait
	engineFirstLineWait = 100 * time.Millisecond
	defer func() { engineFirstLineWait = previous }()
	engine, _, err := startEngine(context.Background(), engineOptions{
		EnginePath: os.Args[0],
		EngineArgs: []string{"-test.run=^TestEngineHelper$"},
		DataDir:    t.TempDir(),
		LogDir:     t.TempDir(),
	})
	if err == nil || engine != nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected first-line timeout, engine=%v err=%v", engine, err)
	}
}

func TestEngineJobKillsChild(t *testing.T) {
	if os.Getenv("FREEAGENT_DESKTOP_ENGINE_HELPER") == "job" {
		fmt.Println("http://127.0.0.1:43127/#key=" + strings.Repeat("a", 64))
		time.Sleep(10 * time.Second)
		return
	}
	t.Setenv("FREEAGENT_DESKTOP_ENGINE_HELPER", "job")
	engine, _, err := startEngine(context.Background(), engineOptions{
		EnginePath: os.Args[0],
		EngineArgs: []string{"-test.run=^TestEngineHelper$"},
		DataDir:    t.TempDir(),
		LogDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.process.Kill(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := engine.process.Wait(context.Background()); done <- err }()
	select {
	case <-done:
		engine.finish()
		return
	case <-time.After(5 * time.Second):
	}
	_ = engine.closeWithTimeout(time.Second)
	t.Fatal("launcher did not terminate helper process")
}

func TestEngineHelper(t *testing.T) {
	if os.Getenv("FREEAGENT_DESKTOP_ENGINE_HELPER") == "job" {
		fmt.Println("http://127.0.0.1:43127/#key=" + strings.Repeat("a", 64))
		time.Sleep(10 * time.Second)
		return
	}
	time.Sleep(10 * time.Second)
}

// Explorer gives the desktop hidden "=C:=..." entries; startEngine forwards
// os.Environ and must still launch the engine.
func TestStartEngineWithHiddenDriveEnvironment(t *testing.T) {
	t.Setenv("=C:", `C:\Windows`)
	hidden := false
	for _, entry := range os.Environ() {
		hidden = hidden || strings.HasPrefix(entry, "=C:=")
	}
	if !hidden {
		t.Fatal("test process did not expose the hidden drive entry through os.Environ")
	}
	t.Setenv("FREEAGENT_DESKTOP_ENGINE_HELPER", "job")
	engine, launch, err := startEngine(context.Background(), engineOptions{
		EnginePath: os.Args[0],
		EngineArgs: []string{"-test.run=^TestEngineHelper$"},
		DataDir:    t.TempDir(),
		LogDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("engine launch with hidden drive environment failed: %v", err)
	}
	if launch.Port != 43127 {
		t.Fatalf("unexpected engine launch: %+v", launch)
	}
	_ = engine.process.Kill()
	_ = engine.closeWithTimeout(5 * time.Second)
}

func TestRunCLIPathArgs(t *testing.T) {
	t.Setenv("FREEAGENT_DESKTOP_CLI_HELPER", "1")
	want := []string{`C:\a b & c!\workspace`, `C:\a b & c!\data`}
	args := append([]string{"-test.run=^TestCLIHelper$", "--"}, want...)
	stdout, stderr, code, err := runCLI(t.Context(), os.Args[0], args)
	var got []string
	if err != nil || code != 7 || stderr != "fixture stderr\n" ||
		json.Unmarshal([]byte(stdout), &got) != nil || !sameStrings(got, want) {
		t.Fatalf("stdout=%q stderr=%q code=%d err=%v", stdout, stderr, code, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, code, err := runCLI(ctx, os.Args[0], args); err == nil || code != -1 {
		t.Fatalf("cancelled CLI code=%d err=%v", code, err)
	}
}

func TestCLIHelper(t *testing.T) {
	if os.Getenv("FREEAGENT_DESKTOP_CLI_HELPER") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[index+1:])
			fmt.Fprintln(os.Stderr, "fixture stderr")
			os.Exit(7)
		}
	}
	os.Exit(8)
}
