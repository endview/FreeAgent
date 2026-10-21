package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopStateLoad(t *testing.T) {
	t.Run("missing uses defaults", func(t *testing.T) {
		state, err := loadDesktopState(filepath.Join(t.TempDir(), "desktop.json"))
		if err != nil {
			t.Fatal(err)
		}
		if state != defaultDesktopState() {
			t.Fatalf("state=%+v defaults=%+v", state, defaultDesktopState())
		}
	})
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"malformed", "{"},
		{"missing fields", `{}`},
		{"oversized coordinate", `{"version":1,"x":2147483647,"width":1200,"height":800}`},
		{"wrong version", `{"version":2,"width":1200,"height":800}`},
		{"too small", `{"version":1,"width":559,"height":480}`},
		{"negative port", `{"version":1,"width":1200,"height":800,"last_port":-1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "desktop.json")
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			state, err := loadDesktopState(path)
			if err == nil || state != defaultDesktopState() {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
	t.Run("valid content is retained", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "desktop.json")
		want := desktopState{Version: 1, X: -20, Y: 30, Width: 900, Height: 700, Maximized: true, LastPort: 51234}
		raw, _ := json.Marshal(want)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := loadDesktopState(path)
		if err != nil || got != want {
			t.Fatalf("got=%+v err=%v want=%+v", got, err, want)
		}
	})
}

func TestDefaultWindowRectScalesForDPIAndFitsWorkArea(t *testing.T) {
	for _, test := range []struct {
		name                       string
		dpi, workWidth, workHeight int
		wantX, wantY, wantW, wantH int
	}{
		{"100 percent", 96, 1920, 1040, 360, 120, 1200, 800},
		{"150 percent", 144, 2560, 1528, 380, 164, 1800, 1200},
		{"200 percent clamps to work area", 192, 2560, 1400, 80, 0, 2400, 1400},
		{"unknown dpi uses 96", 0, 1920, 1040, 360, 120, 1200, 800},
	} {
		t.Run(test.name, func(t *testing.T) {
			x, y, width, height := defaultWindowRect(test.dpi, test.workWidth, test.workHeight)
			if x != test.wantX || y != test.wantY || width != test.wantW || height != test.wantH {
				t.Fatalf("got=%d,%d %dx%d want=%d,%d %dx%d", x, y, width, height,
					test.wantX, test.wantY, test.wantW, test.wantH)
			}
		})
	}
	if got := scaleForDPI(minWindowWidth, 144); got != 840 {
		t.Fatalf("minimum width at 150%% = %d", got)
	}
}

func TestDesktopStateSaveFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "desktop.json")
	state := desktopState{Version: 1, X: 120, Y: 80, Width: 1200, Height: 800, Maximized: false, LastPort: 51234}
	if err := saveDesktopState(path, state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 7 || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "workspace") {
		t.Fatalf("unexpected desktop state: %s", raw)
	}
	for _, name := range []string{"version", "x", "y", "width", "height", "maximized", "last_port"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("field %q missing from %s", name, raw)
		}
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".desktop-state-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary state files remain: %v %v", files, err)
	}
	state.LastPort = 43123
	if err := saveDesktopState(path, state); err != nil {
		t.Fatal(err)
	}
	if got, err := loadDesktopState(path); err != nil || got != state {
		t.Fatalf("replacement failed: %v %v", got, err)
	}
}
