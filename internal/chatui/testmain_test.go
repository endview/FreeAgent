package chatui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/apppaths"
)

// TestMain points the default keys file at a temporary directory so no Chat
// UI test can read or write the user's real keys.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "freeagent-chatui-keys-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv(apppaths.KeysFileEnv, filepath.Join(dir, "keys.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
