//go:build windows

package harness

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

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
