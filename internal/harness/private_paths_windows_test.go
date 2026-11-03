//go:build windows

package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func shortPathForTest(t *testing.T, path string) string {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	short := windows.UTF16ToString(buffer[:length])
	if samePath(short, path) {
		t.Skip("filesystem did not allocate a distinct 8.3 name")
	}
	return short
}

func TestSandboxedCommandPrivateBrowserAcrossShortParent(t *testing.T) {
	testPrivateBrowserSandbox(t, func(path string) string {
		return shortPathForTest(t, path)
	})
}

func TestAbsentPrivateStateRejectsCreationViaShortParent(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	files, err := NewFileTools(shortPathForTest(t, cache), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	_, err = files.Prepare(t.Context(), makeCall("new", "file_create", map[string]string{"path": "FreeAgent", "content": "blocked"}))
	if !errors.Is(err, ErrProtectedPath) {
		t.Fatalf("absent private directory was not protected: %v", err)
	}
}

func TestBlockedPathRejectsShortNameAlias(t *testing.T) {
	workspace := t.TempDir()
	blocked := filepath.Join(workspace, "sensitive-directory")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "note.txt"), []byte("blocked body"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Base(shortPathForTest(t, blocked)), "note.txt")
	files, err := NewFileTools(workspace, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	_, err = files.PrepareWithPolicy(t.Context(), makeCall("blocked", "file_read", map[string]string{"path": alias}), FileAccessPolicy{BlockedPaths: []string{blocked}})
	if !errors.Is(err, ErrPathBlocked) {
		t.Fatalf("blocked short-name alias was not refused: %v", err)
	}
}

func TestPrivateBrowserProfileRejectsShortNameAlias(t *testing.T) {
	cache, secret := privateWebViewFixture(t)
	private := filepath.Join(cache, "FreeAgent")
	name, err := windows.UTF16PtrFromString(private)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	short := filepath.Base(windows.UTF16ToString(buffer[:length]))
	if samePath(short, filepath.Base(private)) {
		t.Skip("filesystem did not allocate a distinct 8.3 name")
	}
	files, err := NewFileTools(cache, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	relative, _ := filepath.Rel(private, secret)
	_, err = files.Prepare(t.Context(), makeCall("alias", "file_read", map[string]string{"path": filepath.Join(short, relative)}))
	if err == nil {
		t.Fatal("a short-name alias exposed the private browser profile")
	}
}
