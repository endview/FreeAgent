package chatui

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/publicweb"
)

// Opt-in real browser acceptance. Unlike a mocked API transport this exercises
// the actual public route table, password, cookies and CSRF over isolated TLS.
func TestPublicSearchRealBrowser(t *testing.T) {
	if os.Getenv("FREEAGENT_PUBLIC_SEARCH_BROWSER") != "1" {
		t.Skip("set FREEAGENT_PUBLIC_SEARCH_BROWSER=1 with Playwright installed")
	}
	server := httptest.NewUnstartedServer(nil)
	f := newPublicFixtureAt(t, nil, "https://"+server.Listener.Addr().String())
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
	command := exec.CommandContext(t.Context(), "node", filepath.Join("scripts", "browser-public-search.cjs"))
	command.Env = append(os.Environ(), "FREEAGENT_PUBLIC_SEARCH_ORIGIN="+server.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("public browser acceptance: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
