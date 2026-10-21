//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
	webview2 "github.com/jchv/go-webview2"
)

func TestEngineExecutable(t *testing.T) {
	for _, test := range []struct {
		name     string
		manifest string
		engine   string
		wantErr  string
	}{
		{"portable", `{"data_format":"Harness-v5"}`, "file", ""},
		{"installed", "", "file", ""},
		{"missing engine", `{"data_format":"Harness-v5"}`, "", "engine was not found"},
		{"directory instead of engine", `{"data_format":"Harness-v5"}`, "directory", "not a file"},
		{"invalid manifest", `{`, "file", "manifest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "package (spaces) & !")
			if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.manifest != "" {
				if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(test.manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := filepath.Join(root, "bin", "freeagent.exe")
			if test.engine == "file" {
				if err := os.WriteFile(want, []byte("engine fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if test.engine == "directory" {
				if err := os.Mkdir(want, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			got, err := engineExecutable(filepath.Join(root, "FreeAgent.exe"))
			if test.wantErr != "" {
				if err == nil || got != "" || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("path=%q err=%v", got, err)
				}
				return
			}
			if err != nil || got != want || !filepath.IsAbs(got) {
				t.Fatalf("path=%q want=%q err=%v", got, want, err)
			}
		})
	}
	t.Run("working directory cannot supply engine", func(t *testing.T) {
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, "bin", "freeagent.exe"), []byte("unrelated"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		_, err := engineExecutable(filepath.Join(t.TempDir(), "FreeAgent.exe"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected missing package engine, got %v", err)
		}
	})
}

// Keep UI callbacks queued while exercising the real workspace CLI and restart.
type queuedWorkspaceView struct {
	webview2.WebView
	callbacks []func()
}

func (v *queuedWorkspaceView) Dispatch(callback func()) {
	v.callbacks = append(v.callbacks, callback)
}

func TestDesktopCLIWorkflow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "package (spaces) & !")
	cli := filepath.Join(root, "bin", "freeagent.exe")
	if err := os.MkdirAll(filepath.Dir(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go.exe"),
		"build", "-buildvcs=false", "-o", cli, "../freeagent")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	desktop := filepath.Join(root, "FreeAgent.exe")
	if err := os.WriteFile(desktop, []byte("must never be executed as a CLI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"data_format":"Harness-v5"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	enginePath, err := engineExecutable(desktop)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data", apppaths.DataFormat)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code, err := runCLI(t.Context(), enginePath, initCLIArgs(workspace, data)); err != nil || code != 0 {
		t.Fatalf("init: code=%d err=%v stderr=%s", code, err, stderr)
	}
	config, err := readConfigShow(t.Context(), enginePath, data)
	if err != nil || config.Workspace != workspace {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	options := engineOptions{Executable: desktop, EnginePath: enginePath, DataDir: data, LogDir: filepath.Join(root, "data")}
	engine, launch, err := startEngineWithRetry(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.closeWithTimeout(engineShutdownTimeout) }()
	var mu sync.Mutex
	var closing atomic.Bool
	next := filepath.Join(root, "next workspace & !")
	if err := os.Mkdir(next, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{"switch succeeds", next, next},
		{"missing workspace retains config and restarts", filepath.Join(root, "missing"), next},
	} {
		t.Run(test.name, func(t *testing.T) {
			queued := &queuedWorkspaceView{}
			view := &desktopWebView{view: queued, workspace: workspace}
			old := engine
			changeWorkspace(t.Context(), enginePath, data, options.LogDir, view, test.path,
				&mu, &engine, &launch, &closing, workspace)
			if engine == old || len(queued.callbacks) != 1 {
				t.Fatal("workspace change did not restart the engine and queue a UI update")
			}
			if _, err := old.process.Wait(context.Background()); err != nil || engine.path != cli {
				t.Fatal("workspace change did not stop the old engine or launched the wrong executable")
			}
			_ = engine.closeWithTimeout(engineShutdownTimeout)
			config, err := readConfigShow(t.Context(), enginePath, data)
			if err != nil || config.Workspace != test.want {
				t.Fatalf("workspace=%q want=%q err=%v", config.Workspace, test.want, err)
			}
			workspace = test.want
		})
	}
}
