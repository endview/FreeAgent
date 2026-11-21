package channelplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestBootstrapProtocolMismatchExplainsRequiredPair(t *testing.T) {
	b := channelwire.Bootstrap{Version: 3, CoreID: "core", Generation: "generation", Origin: "http://127.0.0.1:9090", Token: strings.Repeat("a", 64), Connection: channelwire.Connection{ID: "fixture", Source: "telegram", ApplicationID: "1"}}
	p := filepath.Join(t.TempDir(), "bootstrap.json")
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBootstrap(p); err == nil || !strings.Contains(err.Error(), "version 3; require 4") {
		t.Fatal(err)
	}
	b.Version = 4
	raw, _ = json.Marshal(b)
	_ = os.WriteFile(p, raw, 0600)
	if _, err := ReadBootstrap(p); err != nil {
		t.Fatal(err)
	}
}
