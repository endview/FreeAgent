package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
)

func TestPortableBinDetection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "FreeAgent package")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"data_format":"`+apppaths.DataFormat+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, portable, err := apppaths.PackageRootOf(bin)
	if err != nil || !portable || got != root {
		t.Fatalf("root=%q portable=%v err=%v", got, portable, err)
	}
	hint := doubleClickHint(got)
	if !strings.Contains(hint, filepath.Join(root, "FreeAgent.exe")) || !strings.Contains(hint, "Press Enter") {
		t.Fatalf("hint=%q", hint)
	}
	if _, portable, err := apppaths.PackageRootOf(t.TempDir()); err != nil || portable {
		t.Fatalf("plain directory treated as a package: portable=%v err=%v", portable, err)
	}
	// The test binary itself is not in a package, so a launch would chat.
	if _, portable, err := apppaths.PackageRoot(); err != nil || portable {
		t.Fatalf("test binary treated as a package: portable=%v err=%v", portable, err)
	}
}
