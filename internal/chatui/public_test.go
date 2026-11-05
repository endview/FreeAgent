package chatui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

type simulatedProxy struct{ net.Conn }

func (simulatedProxy) LocalAddr() net.Addr { return &net.UnixAddr{Name: "fixture", Net: "unix"} }

type publicFixture struct {
	ui           *testUI
	server       *PublicServer
	cookie, csrf string
	detect       *http.Cookie
}

func newPublicFixture(t *testing.T, model harness.ModelClient) *publicFixture {
	t.Helper()
	root := t.TempDir()
	data, work := filepath.Join(root, "data"), filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	c := harness.DefaultConfig(work)
	c.Models = map[string]harness.ModelConfig{"echo": {Provider: "echo"}, "second": {Provider: "echo"}}
	c.DefaultModel = "echo"
	c.PublicWeb = harness.DefaultPublicWebConfig()
	c.PublicWeb.Enabled = true
	c.PublicWeb.Origin = "https://agent.example.test"
	ui := setupUIWithConfig(t, model, data, work, c)
	if err := publicweb.SetPassword(data, []byte("public fixture only password")); err != nil {
		t.Fatal(err)
	}
	p, err := NewPublicServer(ui.server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return &publicFixture{ui: ui, server: p}
}
func (f *publicFixture) request(t *testing.T, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, f.server.origin+path, body)
	r.Header.Set("X-FreeAgent-Client-IP", "192.0.2.11")
	r.Header.Set("X-FreeAgent-Proxy-Proto", "https")
	r.Header.Set("Origin", f.server.origin)
	r.Header.Set("User-Agent", "fixture")
	if body != nil && !strings.HasPrefix(path, "/api/attachments?") {
		r.Header.Set("Content-Type", "application/json")
	}
	if f.cookie != "" {
		r.AddCookie(&http.Cookie{Name: publicweb.SessionCookie, Value: f.cookie})
	}
	if f.csrf != "" {
		r.Header.Set("X-FreeAgent-CSRF", f.csrf)
	}
	if f.detect != nil {
		r.AddCookie(f.detect)
	}
	for k, v := range headers {
		if k == "Host" {
			r.Host = v
		} else {
			r.Header.Set(k, v)
		}
	}
	r = r.WithContext(publicweb.ProxyConnectionContext(r.Context(), simulatedProxy{}))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}
func (f *publicFixture) login(t *testing.T) {
	t.Helper()
	w := f.request(t, "GET", "/api/public/session", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == publicweb.DetectionCookie {
			f.detect = c
		}
	}
	w = f.request(t, "POST", "/api/public/login", strings.NewReader(`{"password":"public fixture only password"}`), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == publicweb.SessionCookie {
			f.cookie = c.Value
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Domain != "" || c.Path != "/" {
				t.Fatal("unsafe session cookie", c)
			}
		}
	}
	var info struct{ CSRF string }
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	f.csrf = info.CSRF
	f.detect = nil
}

func TestPublicProxyOriginAuthenticationCSRFAndRouteBoundary(t *testing.T) {
	f := newPublicFixture(t, nil)
	for _, path := range []string{"/api/environment", "/api/chats", "/api/events", "/api/attachments"} {
		w := f.request(t, "GET", path, nil, map[string]string{"X-FreeAgent-Key": f.ui.server.token})
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	f.login(t)
	for _, h := range []map[string]string{{"Host": "evil.example.test"}, {"Origin": "https://evil.example.test"}, {"X-FreeAgent-Proxy-Proto": "http"}, {"X-FreeAgent-Client-IP": "1.2.3.4, 5.6.7.8"}} {
		if w := f.request(t, "GET", "/api/environment", nil, h); w.Code != 403 {
			t.Fatal(h, w.Code)
		}
	}
	for _, path := range []string{"/api/attach/state", "/api/attach/backup", "/api/remote/state", "/api/public-password", "/api/public-recovery", "/api/mcp/private/logs"} {
		if w := f.request(t, "GET", path, nil, nil); w.Code != 404 {
			t.Fatal("private route exposed", path, w.Code)
		}
	}
	w := f.request(t, "POST", "/api/chats", strings.NewReader(`{"title":"remote"}`), map[string]string{"X-FreeAgent-CSRF": ""})
	if w.Code != 403 {
		t.Fatal("missing CSRF", w.Code)
	}
	w = f.request(t, "GET", "/api/environment", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), f.ui.server.token) || strings.Contains(w.Body.String(), f.ui.server.attachToken) {
		t.Fatal("public environment leaked capability", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", f.server.origin+"/api/public/session", nil)
	r.Header.Set("X-FreeAgent-Client-IP", "192.0.2.11")
	r.Header.Set("X-FreeAgent-Proxy-Proto", "https")
	w = httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded headers trusted without Unix connection")
	}
}

func TestPublicDatabaseStatusRequiresLoginAndDoesNotExposeConnection(t *testing.T) {
	f := newPublicFixture(t, nil)
	if w := f.request(t, "GET", "/api/settings/database", nil, nil); w.Code != 401 {
		t.Fatal("database status bypassed login", w.Code)
	}
	f.login(t)
	w := f.request(t, "GET", "/api/settings/database", nil, nil)
	var status harness.DatabaseStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Backend != "PostgreSQL" || status.CoreID == "" || status.PoolLimit != 4 {
		t.Fatal("authenticated PG status unavailable", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(f.ui.tasks.DataDir(), "postgres.json"))
	if err != nil {
		t.Fatal(err)
	}
	var connection struct {
		Password string `json:"password"`
		User     string `json:"user"`
	}
	if err := json.Unmarshal(raw, &connection); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{connection.Password, connection.User, f.ui.server.token} {
		if secret != "" && strings.Contains(w.Body.String(), secret) {
			t.Fatal("database status exposed private connection/capability")
		}
	}
	if w := f.request(t, "PUT", "/api/settings/database", strings.NewReader(`{}`), nil); w.Code != 404 {
		t.Fatal("read-only status allowed mutation", w.Code)
	}
	if w := f.request(t, "POST", "/api/client/detach", nil, map[string]string{"X-FreeAgent-Client": "public-closing"}); w.Code != 200 {
		t.Fatal("public normal-close notification failed", w.Code)
	}
}

func TestPublicAttachmentOwnershipAndAsynchronousEntryPermission(t *testing.T) {
	requests := make(chan harness.ModelRequest, 4)
	f := newPublicFixture(t, fixtureModel(func(_ context.Context, r harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		requests <- r
		return harness.ModelResponse{Content: "done"}, nil
	}))
	f.login(t)
	if err := f.ui.tasks.SetSpacePermission(t.Context(), harness.LocalSpaceID, harness.PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	c := f.ui.tasks.Config().PublicWeb
	c.MaxPermission = harness.PermissionNone
	w := f.request(t, "PUT", "/api/public-settings", bytes.NewReader(mustJSONTest(c)), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	raise := c
	raise.MaxPermission = harness.PermissionFull
	if response := f.request(t, "PUT", "/api/public-settings", bytes.NewReader(mustJSONTest(raise)), nil); response.Code != 409 || !strings.Contains(response.Body.String(), "local_confirmation_required") {
		t.Fatal("remote raise did not explain local confirmation", response.Code, response.Body.String())
	}
	chat := f.ui.create(t)
	q := url.Values{"conversation_id": {chat}, "name": {"说明.md"}, "request_key": {"upload"}}
	w = f.request(t, "POST", "/api/attachments?"+q.Encode(), strings.NewReader("user supplied text"), nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var ref harness.AttachmentRef
	if err := json.Unmarshal(w.Body.Bytes(), &ref); err != nil {
		t.Fatal(err)
	}
	other := &publicFixture{ui: f.ui, server: f.server}
	other.login(t)
	if w := other.request(t, "GET", "/api/attachments/"+ref.ID+"?conversation_id="+chat, nil, nil); w.Code != 404 {
		t.Fatal("another session read a staged upload", w.Code)
	}
	w = f.request(t, "POST", "/api/chats/"+chat+"/submit", bytes.NewReader(mustJSONTest(map[string]any{"prompt": "read", "request_key": "input", "attachments": []string{ref.ID}})), nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.ui.server.wg.Wait()
	run, err := f.ui.tasks.Latest(t.Context(), chat)
	if err != nil || run.Status != "completed" || run.Permission == nil || run.Permission.RemoteLimit != harness.PermissionNone || run.Permission.Mode != harness.PermissionNone {
		t.Fatalf("lost asynchronous public entry %+v %v", run, err)
	}
	select {
	case r := <-requests:
		if len(r.Tools) != 0 || !strings.Contains(string(mustJSONTest(r)), "user supplied text") {
			t.Fatal("entry input or None boundary changed")
		}
	case <-time.After(time.Second):
		t.Fatal("model request absent")
	}
	w = f.request(t, "GET", "/api/attachments/"+ref.ID+"?conversation_id="+chat, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Code, w.Header())
	}
	w = f.request(t, "POST", "/api/public/logout", strings.NewReader(`{}`), nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = f.request(t, "GET", "/api/attachments/"+ref.ID+"?conversation_id="+chat, nil, nil); w.Code != 401 {
		t.Fatal("logout retained download", w.Code)
	}
	if run, err := f.ui.tasks.Latest(t.Context(), chat); err != nil || run.Status != "completed" {
		t.Fatal("logout changed execution", err)
	}
}
func mustJSONTest(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestPublicGUIModelAndPermissionControlsUseSharedCore(t *testing.T) {
	f := newPublicFixture(t, nil)
	f.login(t)
	chat := f.ui.create(t)
	w := f.request(t, "POST", "/api/chats/"+chat+"/model", strings.NewReader(`{"model":"second"}`), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/chats/"+chat+"/permission", strings.NewReader(`{"mode":"none","confirmed":false}`), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	view, err := f.ui.tasks.Chat(t.Context(), chat, "")
	if err != nil || view.ModelID != "second" || view.Permission.Mode != harness.PermissionNone {
		t.Fatalf("public controls diverged from local core: %+v %v", view, err)
	}
	w = f.request(t, "GET", "/api/remote/context?conversation_id="+chat, nil, nil)
	if w.Code != 200 {
		t.Fatal("context alias", w.Code, w.Body.String())
	}
}

func TestPublicLogoutClosesSSEWithoutCancelingAcceptedTask(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	f := newPublicFixture(t, fixtureModel(func(ctx context.Context, _ harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		close(started)
		select {
		case <-ctx.Done():
			return harness.ModelResponse{}, ctx.Err()
		case <-finish:
			return harness.ModelResponse{Content: "task completed after logout"}, nil
		}
	}))
	f.login(t)
	chat := f.ui.create(t)
	w := f.request(t, "POST", "/api/chats/"+chat+"/submit", strings.NewReader(`{"prompt":"work","request_key":"one"}`), nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = f.server.host
		r.Header.Set("X-FreeAgent-Client-IP", "192.0.2.11")
		r.Header.Set("X-FreeAgent-Proxy-Proto", "https")
		r = r.WithContext(publicweb.ProxyConnectionContext(r.Context(), simulatedProxy{}))
		f.server.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	r, _ := http.NewRequestWithContext(t.Context(), "GET", httpServer.URL+"/api/events", nil)
	r.AddCookie(&http.Cookie{Name: publicweb.SessionCookie, Value: f.cookie})
	r.Header.Set("User-Agent", "fixture")
	response, err := httpServer.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || !strings.Contains(line, "reconnect") {
		t.Fatal(line, err)
	}
	w = f.request(t, "POST", "/api/public/logout", strings.NewReader(`{}`), nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	closed := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); closed <- err }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("logout left the SSE reader open")
	}
	close(finish)
	f.ui.server.wg.Wait()
	run, err := f.ui.tasks.Latest(t.Context(), chat)
	if err != nil || run.Status != "completed" || !strings.Contains(run.Output, "after logout") {
		t.Fatalf("logout canceled an accepted task: %+v %v", run, err)
	}
}
