package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEngineFirstLine(t *testing.T) {
	valid := "http://127.0.0.1:43127/#key=" + strings.Repeat("a", 64)
	launch, err := parseEngineFirstLine(valid)
	if err != nil || launch.Origin != "http://127.0.0.1:43127" || launch.Port != 43127 {
		t.Fatalf("valid launch line rejected: %#v %v", launch, err)
	}
	for _, line := range []string{
		"https://127.0.0.1:43127/#key=" + strings.Repeat("a", 64),
		"http://localhost:43127/#key=" + strings.Repeat("a", 64),
		"http://127.0.0.1:43127/#key=" + strings.Repeat("a", 31),
		"http://127.0.0.1:43127/",
		"http://127.0.0.1:43127/#key=" + strings.Repeat("a", 64) + "\nextra",
	} {
		if _, err := parseEngineFirstLine(line); err == nil {
			t.Fatalf("invalid launch line accepted: %q", line)
		}
	}
}

func TestInstanceName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := instanceName(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := instanceName(dir + `\`)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first, `Local\FreeAgent-`) || len(first) != len(`Local\FreeAgent-`)+16 {
		t.Fatalf("unstable instance name: %q %q", first, second)
	}
}

func TestExternalURLAllowed(t *testing.T) {
	for _, test := range []struct {
		raw string
		ok  bool
	}{
		{"http://example.com/a", true},
		{"https://example.com/a", true},
		{"mailto:user@example.com", true},
		{"file:///tmp/a", false},
		{"javascript:alert(1)", false},
		{"/relative", false},
	} {
		if got := externalURLAllowed(test.raw); got != test.ok {
			t.Errorf("externalURLAllowed(%q)=%v, want %v", test.raw, got, test.ok)
		}
	}
}

func TestDarkFromRegistry(t *testing.T) {
	if !darkFromRegistry(0, nil) {
		t.Fatal("zero AppsUseLightTheme value should select dark mode")
	}
	if darkFromRegistry(1, nil) {
		t.Fatal("nonzero AppsUseLightTheme value should select light mode")
	}
	if darkFromRegistry(0, os.ErrNotExist) {
		t.Fatal("registry read errors should select light mode")
	}
}

func TestEngineLogCap(t *testing.T) {
	var buffer bytes.Buffer
	writer := newBoundedLogWriter(&buffer, 8)
	if _, err := writer.Write([]byte("1234567890")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("extra")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buffer.String(), "12345678") || strings.Count(buffer.String(), "engine log truncated") != 1 {
		t.Fatalf("unexpected capped log: %q", buffer.String())
	}
}

func TestPickPort(t *testing.T) {
	if got := pickPort(4123, func(port int) bool { return port == 4123 }); got != 4123 {
		t.Fatalf("available previous port=%d", got)
	}
	if got := pickPort(4123, func(int) bool { return false }); got != 0 {
		t.Fatalf("occupied previous port=%d", got)
	}
	if got := pickPort(0, func(int) bool { return true }); got != 0 {
		t.Fatalf("zero previous port=%d", got)
	}
}

func TestNeedsInit(t *testing.T) {
	data := t.TempDir()
	need, err := needsInit(data)
	if err != nil || !need {
		t.Fatalf("missing config: need=%v err=%v", need, err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	need, err = needsInit(data)
	if err != nil || need {
		t.Fatalf("existing config: need=%v err=%v", need, err)
	}
}

func TestCLIArgs(t *testing.T) {
	workspace := `C:\Users\test\FreeAgent & notes!`
	data := `C:\Users\test\AppData\Harness-v5`
	if got, want := initCLIArgs(workspace, data), []string{"init", "--workspace", workspace, "--data-dir", data}; !sameStrings(got, want) {
		t.Fatalf("init args=%#v want=%#v", got, want)
	}
	if got, want := workspaceCLIArgs(workspace, data), []string{"config", "workspace", "--path", workspace, "--data-dir", data}; !sameStrings(got, want) {
		t.Fatalf("workspace args=%#v want=%#v", got, want)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
