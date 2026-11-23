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

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/webaccess"

	"github.com/endview/freeagent/internal/publicweb"
)

// Opt-in real browser acceptance. Unlike a mocked API transport this exercises
// the actual public route table, password, cookies and CSRF over isolated TLS.
func TestPublicSearchRealBrowser(t *testing.T) {
	runPublicBrowser(t, "browser-public-search.cjs", nil, nil)
}

func TestPublicAdminSettingsRealBrowser(t *testing.T) {
	runPublicBrowser(t, "browser-public-admin-settings.cjs", nil, func(f *publicFixture) {
		ctx := t.Context()
		tasks := f.ui.tasks
		key, err := tasks.ChannelKeys().Add(harness.KeyInput{Name: "Isolated fixture Bot", Protocol: harness.KeyProtocolTelegram,
			BaseURL: "https://api.telegram.org", Source: harness.KeySourceSaved, Secret: []byte("1:fixture-credential-before-rotation")})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tasks.ConfigureRuntimePolicy(ctx, "", func(c *harness.Config) error {
			c.Channels.Connections = []channelwire.Connection{{ID: "fixture", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true}}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		bridge, err := harness.NewChannelBridge(tasks, f.ui.server.origin, "isolated-browser-fixture", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bridge.Close() })
		control, err := tasks.Control()
		if err != nil {
			t.Fatal(err)
		}
		target := harness.ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "7"}
		grant := harness.TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}}
		if _, err := control.ApplyDirectControlChange(ctx, harness.ControlTelegramAccess, grant, "", "cli"); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.ReceiveChannelText(ctx, harness.ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "verify", Purpose: "request"}, "Fixture identity"); err != nil {
			t.Fatal(err)
		}
	})
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
