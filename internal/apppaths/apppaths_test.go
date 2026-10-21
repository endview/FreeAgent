package apppaths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, directory, body string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppPathsPortableDetection(t *testing.T) {
	current := `{"version":"v7.0.0-dev.1","data_format":"Harness-v5","files":[]}`
	t.Run("package root", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, current)
		got, portable, err := portableRoot(root)
		if err != nil || !portable || got != root {
			t.Fatalf("root=%q portable=%v err=%v", got, portable, err)
		}
	})
	t.Run("bin below package root", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, current)
		bin := filepath.Join(root, "bin")
		if err := os.Mkdir(bin, 0o700); err != nil {
			t.Fatal(err)
		}
		got, portable, err := portableRoot(bin)
		if err != nil || !portable || got != root {
			t.Fatalf("root=%q portable=%v err=%v", got, portable, err)
		}
	})
	t.Run("not portable", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "bin")
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		got, portable, err := portableRoot(directory)
		if err != nil || portable || got != "" {
			t.Fatalf("root=%q portable=%v err=%v", got, portable, err)
		}
		other := filepath.Join(t.TempDir(), "tools")
		writeManifest(t, filepath.Dir(other), current)
		if err := os.Mkdir(other, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, portable, err := portableRoot(other); err != nil || portable {
			t.Fatalf("only a bin directory may use its parent manifest: portable=%v err=%v", portable, err)
		}
	})
	for name, body := range map[string]string{
		"broken JSON":     `{"data_format":`,
		"old data format": `{"data_format":"Harness-v` + `3"}`,
		"missing format":  `{"version":"v5.5.0-dev.1"}`,
		"trailing data":   `{"data_format":"Harness-v5"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeManifest(t, root, body)
			if _, _, err := portableRoot(root); !errors.Is(err, ErrManifest) {
				t.Fatalf("expected ErrManifest, got %v", err)
			}
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, _, err := portableRoot(bin); !errors.Is(err, ErrManifest) {
				t.Fatalf("expected ErrManifest from bin, got %v", err)
			}
		})
	}
	t.Run("manifest directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "manifest.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := portableRoot(root); !errors.Is(err, ErrManifest) {
			t.Fatalf("expected ErrManifest, got %v", err)
		}
	})
}

func TestAppPathsUserLocations(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "keys.json")
	t.Setenv(KeysFileEnv, keys)
	if got, err := KeysFile(); err != nil || got != keys {
		t.Fatalf("keys override=%q err=%v", got, err)
	}
	t.Setenv(KeysFileEnv, "")
	config, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user configuration directory:", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory:", err)
	}
	// The test binary is never inside a portable package.
	for name, check := range map[string]struct {
		call func() (string, error)
		want string
	}{
		"data":    {DataDir, filepath.Join(config, "FreeAgent", DataFormat)},
		"keys":    {KeysFile, filepath.Join(config, "FreeAgent", "keys.json")},
		"desktop": {DesktopState, filepath.Join(config, "FreeAgent", "desktop.json")},
		"webview": {WebView2Dir, filepath.Join(cache, "FreeAgent", "webview2")},
		"logs":    {LogDir, filepath.Join(cache, "FreeAgent")},
	} {
		got, err := check.call()
		if err != nil || got != check.want {
			t.Fatalf("%s=%q err=%v, want %q", name, got, err, check.want)
		}
	}
	workspace, err := DefaultWorkspace()
	if err != nil || filepath.Base(workspace) != "FreeAgent" || !filepath.IsAbs(workspace) {
		t.Fatalf("workspace=%q err=%v", workspace, err)
	}
}
