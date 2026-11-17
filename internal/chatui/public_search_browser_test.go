package chatui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/webaccess"

	"github.com/endview/freeagent/internal/publicweb"
)

// Opt-in real browser acceptance. Unlike a mocked API transport this exercises
// the actual public route table, password, cookies and CSRF over isolated TLS.
func TestPublicSearchRealBrowser(t *testing.T) {
	runPublicBrowser(t, "browser-public-search.cjs", nil, nil)
}

func TestPublicSearchApprovalRealBrowser(t *testing.T) {
	var calls atomic.Int32
	model := fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		n := calls.Add(1)
		if n%2 == 1 {
			arguments := json.RawMessage(`{"path":"approval.txt","content":"approved exactly once"}`)
			if n > 1 {
				arguments = json.RawMessage(`{"path":"not-approved.txt","content":"must not be written"}`)
			}
			return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "approval-file", Name: "file_create", Arguments: arguments}}}, nil
		}
		return harness.ModelResponse{Content: "Approval completed exactly once."}, nil
	})
	runPublicBrowser(t, "browser-public-approval.cjs", model, func(f *publicFixture) {
		_, err := f.ui.tasks.ConfigureRuntimePolicy(t.Context(), "", func(config *harness.Config) error {
			policy := webaccess.DefaultConfig()
			policy.Mode = "auto"
			config.WebSearch, config.Tools.Network = &policy, true
			config.PublicWeb.MaxPermission = harness.PermissionAsk
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := os.Stat(filepath.Join(f.ui.work, "not-approved.txt")); !os.IsNotExist(err) {
				t.Fatal("ending without a preview dispatched the pending write")
			}
		})
	})
	if calls.Load() != 3 {
		t.Fatalf("preview retry or reload replayed model: %d calls", calls.Load())
	}
}

func runPublicBrowser(t *testing.T, script string, model harness.ModelClient, configure func(*publicFixture)) {
	t.Helper()
	if os.Getenv("FREEAGENT_PUBLIC_SEARCH_BROWSER") != "1" {
		t.Skip("set FREEAGENT_PUBLIC_SEARCH_BROWSER=1 with Playwright installed")
	}
	server := httptest.NewUnstartedServer(nil)
	f := newPublicFixtureAt(t, model, "https://"+server.Listener.Addr().String())
	if configure != nil {
		configure(f)
	}
	server.Config.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		return publicweb.ProxyConnectionContext(ctx, simulatedProxy{conn})
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-FreeAgent-Client-IP", "127.0.0.1")
		r.Header.Set("X-FreeAgent-Proxy-Proto", "https")
		f.server.ServeHTTP(w, r)
	})
	server.StartTLS()
	defer server.Close()
	command := exec.CommandContext(t.Context(), "node", filepath.Join("scripts", script))
	command.Env = append(os.Environ(), "FREEAGENT_PUBLIC_SEARCH_ORIGIN="+server.URL)
	if script == "browser-public-approval.cjs" {
		command.Env = append(command.Env, "FREEAGENT_PUBLIC_APPROVAL_CHAT="+f.ui.create(t))
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("public browser acceptance: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
