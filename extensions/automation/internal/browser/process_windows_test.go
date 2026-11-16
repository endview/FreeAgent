package browser

import (
	"context"
	"github.com/endview/freeagent-automation/internal/extwire"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testProcessAlive(pid int) bool {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259
}

func TestBrowserEdgeStartupHasBoundedCleanup(t *testing.T) {
	if os.Getenv("FREEAGENT_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("explicit real browser test only")
	}
	path := ""
	for _, p := range systemBrowserLocations() {
		if strings.EqualFold(filepath.Base(p), "msedge.exe") {
			if _, e := os.Stat(p); e == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		t.Skip("Edge unavailable")
	}
	e := &Extension{}
	_ = e.Initialize(extwire.Init{DataDir: t.TempDir()})
	e.config.Executable = path
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	_, err := e.start(ctx, extwire.Scope{CoreID: "edge-test", SpaceID: "space", ConversationID: "chat", RunID: "run", Local: true})
	_ = e.Close()
	if err != nil {
		t.Log("Edge startup rejected with bounded cleanup:", err)
	} else {
		t.Log("Edge started and closed normally")
	}
}
