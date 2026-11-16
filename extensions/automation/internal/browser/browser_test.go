package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent-automation/internal/extwire"
)

func TestProxyScopeAndRebindingTargets(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "::ffff:127.0.0.1"} {
		if publicIP(net.ParseIP(s)) {
			t.Fatal("private address allowed", s)
		}
	}
	if !publicIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public address denied")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("allowed")) }))
	defer target.Close()
	for _, allowed := range []bool{false, true} {
		origins := []string{}
		if allowed {
			origins = []string{target.URL}
		}
		g, err := newGuard(origins)
		if err != nil {
			t.Fatal(err)
		}
		defer g.transport.CloseIdleConnections()
		proxy := httptest.NewServer(g)
		defer proxy.Close()
		u, _ := url.Parse(proxy.URL)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}, Timeout: time.Second}
		response, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if (response.StatusCode == 200) != allowed {
			t.Fatal("origin boundary changed", response.StatusCode, allowed)
		}
	}
}

func TestBrowserOwnerExitHelper(t *testing.T) {
	if os.Getenv("FREEAGENT_BROWSER_OWNER_HELPER") != "1" {
		t.Skip("subprocess helper only")
	}
	e := &Extension{}
	_ = e.Initialize(extwire.Init{DataDir: os.Getenv("FREEAGENT_BROWSER_HELPER_DATA")})
	scope := extwire.Scope{CoreID: "helper", SpaceID: "space", ConversationID: "chat", RunID: "run", Local: true}
	s, err := e.start(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(s.cmd.Process.Pid)
	_ = os.Stdout.Sync()
	select {}
}
func TestBrowserOwnerCrashCleansProcessTree(t *testing.T) {
	if os.Getenv("FREEAGENT_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("explicit real process acceptance only")
	}
	binary, _ := os.Executable()
	helper := exec.Command(binary, "-test.run=^TestBrowserOwnerExitHelper$")
	helper.Env = append(os.Environ(), "FREEAGENT_BROWSER_OWNER_HELPER=1", "FREEAGENT_BROWSER_HELPER_DATA="+t.TempDir())
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("helper failed to start browser")
	}
	pid, err := strconv.Atoi(scanner.Text())
	if err != nil {
		t.Fatal("helper did not return browser identity", scanner.Text())
	}
	_ = helper.Process.Kill()
	_ = helper.Wait()
	for i := 0; i < 50; i++ {
		if !testProcessAlive(pid) {
			t.Log("PASS: extension owner crash closed browser process")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("browser survived abrupt extension owner exit")
}
func TestBrowserLiveIsolatedWorkflow(t *testing.T) {
	if os.Getenv("FREEAGENT_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("explicit real browser test only")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download" {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Disposition", `attachment; filename="test.txt"`)
			_, _ = w.Write([]byte("DOWNLOAD_OK"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<title>FreeAgent browser fixture</title><h1>Browser acceptance</h1><label>Test name<input aria-label="Test name" id="name"></label><button onclick="document.querySelector('#result').textContent='Saved '+document.querySelector('#name').value">Apply test</button><p id="result">waiting</p><a href="/download">Download</a>`))
	}))
	defer server.Close()
	headless := true
	e := &Extension{}
	if err := e.Initialize(extwire.Init{DataDir: t.TempDir(), Settings: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	e.config.Headless = &headless
	e.config.Origins = []string{server.URL}
	defer e.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	scope := extwire.Scope{CoreID: "test", SpaceID: "space", ConversationID: "chat", RunID: "run", Local: true, Vision: true}
	call := func(tool string, a args) extwire.Result {
		t.Helper()
		raw, _ := json.Marshal(a)
		r, err := e.Call(ctx, extwire.Call{Name: tool, Invocation: "test", Scope: scope, Arguments: raw})
		if err != nil || r.IsError {
			t.Fatalf("%s/%s failed: %s %v", tool, a.Action, r.Content, err)
		}
		return r
	}
	opened := call("act", args{Action: "open", URL: server.URL})
	var open struct{ Tab string }
	_ = json.Unmarshal([]byte(opened.Content), &open)
	var view struct {
		Revision string
		Text     string
		Elements []struct{ Ref, Tag, Label string }
	}
	for i := 0; i < 50; i++ {
		r := call("observe", args{Action: "observe", Tab: open.Tab})
		_ = json.Unmarshal([]byte(r.Content), &view)
		if len(view.Elements) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	input := ""
	for _, el := range view.Elements {
		if el.Tag == "input" {
			input = el.Ref
		}
	}
	if input == "" {
		t.Fatal("real browser did not observe input")
	}
	call("act", args{Action: "type", Tab: open.Tab, Revision: view.Revision, Ref: input, Text: "浏览器实测"})
	stale, _ := json.Marshal(args{Action: "click", Tab: open.Tab, Revision: view.Revision, Ref: input})
	r, err := e.Call(ctx, extwire.Call{Name: "act", Scope: scope, Arguments: stale})
	if err != nil || !r.IsError {
		t.Fatal("stale ref accepted")
	}
	_ = json.Unmarshal([]byte(call("observe", args{Action: "observe", Tab: open.Tab}).Content), &view)
	button := ""
	for _, el := range view.Elements {
		if el.Tag == "button" {
			button = el.Ref
		}
	}
	call("act", args{Action: "click", Tab: open.Tab, Revision: view.Revision, Ref: button})
	_ = json.Unmarshal([]byte(call("observe", args{Action: "observe", Tab: open.Tab}).Content), &view)
	if !strings.Contains(view.Text, "Saved 浏览器实测") {
		t.Fatal("real page result missing", view.Text)
	}
	image := call("observe", args{Action: "screenshot", Tab: open.Tab})
	if len(image.Artifacts) != 1 || !image.Artifacts[0].Image {
		t.Fatal("screenshot missing")
	}
	_ = json.Unmarshal([]byte(call("observe", args{Action: "observe", Tab: open.Tab}).Content), &view)
	link := ""
	for _, el := range view.Elements {
		if el.Tag == "a" {
			link = el.Ref
		}
	}
	call("act", args{Action: "click", Tab: open.Tab, Revision: view.Revision, Ref: link})
	var download string
	for i := 0; i < 40; i++ {
		raw := call("observe", args{Action: "downloads"})
		var files map[string]string
		_ = json.Unmarshal([]byte(raw.Content), &files)
		for id := range files {
			s := e.sessions[scopeKey(scope)]
			s.mu.Lock()
			ready := s.downloadStates[id] == "completed"
			s.mu.Unlock()
			if ready {
				download = id
			}
		}
		if download != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if download == "" {
		t.Fatal("download did not complete")
	}
	artifact := call("observe", args{Action: "download", Download: download})
	if len(artifact.Artifacts) != 1 {
		t.Fatal("download not returned as artifact")
	}
	other := scope
	other.ConversationID = "other"
	statusArgs, _ := json.Marshal(args{Action: "status"})
	isolated, err := e.Call(ctx, extwire.Call{Name: "observe", Scope: other, Arguments: statusArgs})
	if err != nil || !strings.Contains(isolated.Content, "not_started") {
		t.Fatal("session scope leaked", err)
	}
	call("act", args{Action: "takeover", Tab: open.Tab})
	raw, _ := json.Marshal(args{Action: "open", URL: server.URL})
	blocked, _ := e.Call(ctx, extwire.Call{Name: "act", Scope: scope, Arguments: raw})
	if !blocked.IsError {
		t.Fatal("takeover did not pause")
	}
	call("act", args{Action: "resume"})
	call("act", args{Action: "stop"})
	t.Log("PASS: isolated real Chromium, DOM input/click/result, stale rejection, screenshot, download artifact, scope isolation, pause/resume/stop")
}
