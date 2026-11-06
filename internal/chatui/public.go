package chatui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
	"github.com/endview/freeagent/internal/publicweb"
)

type publicRequestKey struct{}
type publicRequest struct {
	Session *publicweb.Session
	IP      string
}

func publicRequestFrom(ctx context.Context) (publicRequest, bool) {
	v, ok := ctx.Value(publicRequestKey{}).(publicRequest)
	return v, ok && v.Session != nil
}

// PublicServer accepts only connections marked by the restricted Unix socket
// listener. It never forwards a local capability or exposes attach discovery.
type PublicServer struct {
	local   *Server
	auth    *publicweb.Auth
	origin  string
	host    string
	socket  *publicweb.SocketServer
	started harness.PublicWebConfig
}

func NewPublicServer(local *Server) (*PublicServer, error) {
	c := local.tasks.Config().PublicWeb.Effective()
	if !c.Enabled || c.Validate() != nil {
		return nil, errors.New("chat: valid enabled public Web configuration is required")
	}
	u, _ := url.Parse(c.Origin)
	auth, err := publicweb.NewAuth(local.ctx, local.tasks.DataDir(), func() harness.PublicWebConfig { return local.tasks.Config().PublicWeb }, local.tasks)
	if err != nil {
		return nil, err
	}
	p := &PublicServer{local: local, auth: auth, origin: c.Origin, host: u.Host, started: c}
	local.mu.Lock()
	local.public = p
	local.mu.Unlock()
	return p, nil
}
func (p *PublicServer) Close()                { p.auth.Close() }
func (p *PublicServer) Done() <-chan struct{} { return p.auth.Done() }
func (p *PublicServer) SetSocket(socket *publicweb.SocketServer) {
	p.local.mu.Lock()
	p.socket = socket
	p.local.mu.Unlock()
	go func() {
		select {
		case <-socket.Done():
			p.auth.Close()
		case <-p.Done():
		}
	}()
}

func publicHeaders(w http.ResponseWriter, host string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	u, _ := url.Parse("https://" + host)
	if _, err := netip.ParseAddr(u.Hostname()); err != nil {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
}
func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func setPublicCookie(w http.ResponseWriter, name, value string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: seconds, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func (p *PublicServer) denied(w http.ResponseWriter, status int) {
	p.local.writeError(w, status, "public_access", "访问未获授权。")
}

// Explicit route shapes are intentionally independent of attachAPI. A new
// local endpoint cannot become public merely by being added to that handler.
func publicRoute(path, method string) (string, bool) {
	if strings.HasPrefix(path, "/api/attach/") {
		return "", false
	}
	if strings.HasPrefix(path, "/api/remote/") {
		name := strings.TrimPrefix(path, "/api/remote/")
		if (name == "context" || name == "inputs" || name == "branch") && (method == "GET" || method == "POST") || name == "commands" && (method == "GET" || method == "POST") || name == "cancel" && method == "POST" {
			return "/api/attach/" + name, true
		}
		return "", false
	}
	allowed := map[string]string{
		"/api/connect": "POST", "/api/client/detach": "POST", "/api/environment": "GET", "/api/events": "GET", "/api/chats": "GET POST", "/api/spaces": "GET POST", "/api/permissions": "GET",
		"/api/public-settings":       "GET PUT",
		"/api/permissions/selection": "PUT", "/api/permissions/files": "PUT", "/api/permissions/profile": "PUT", "/api/commands": "GET PUT", "/api/commands/rules": "PUT",
		"/api/settings": "GET", "/api/settings/permissions": "PATCH", "/api/settings/file-access": "PATCH", "/api/settings/tools": "PATCH", "/api/settings/command-run": "PATCH", "/api/settings/context": "PATCH", "/api/runtime-tools": "GET",
		"/api/settings/database": "GET",
		"/api/settings/prompt":   "GET",
		"/api/server":            "GET PUT", "/api/providers": "PUT", "/api/attachment-policy": "GET PUT", "/api/attachments": "GET POST", "/api/attachments/reply": "POST", "/api/attachments/delete": "POST",
		"/api/keys": "GET POST", "/api/keys/test": "POST", "/api/models/enable": "POST", "/api/models/enable-batch": "POST", "/api/models/default": "POST", "/api/models/vision": "POST", "/api/models/context": "POST",
		"/api/channels": "GET PATCH", "/api/mcp": "GET", "/api/skills": "GET", "/api/extensions": "GET", "/api/commands/catalog": "GET",
		"/api/channels/telegram/access": "GET POST",
		"/api/control/catalog":          "GET", "/api/control/config": "GET", "/api/control/state": "GET", "/api/control/operations": "GET", "/api/control/plan": "POST", "/api/control/confirmation": "GET", "/api/control/confirm": "POST", "/api/control/reject": "POST", "/api/control/revert-plan": "POST",
	}
	if methods, ok := allowed[path]; ok {
		return path, strings.Contains(" "+methods+" ", " "+method+" ")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	if len(parts) == 2 && parts[0] == "chats" && method == "GET" {
		return path, true
	}
	if len(parts) == 3 && parts[0] == "chats" {
		switch parts[2] {
		case "submit", "stop", "continue", "preview", "confirm", "end", "pin", "model", "permission", "capabilities", "title", "exclude-memory", "workflow":
			return path, method == "POST"
		case "memory", "raw":
			return path, method == "GET"
		}
	}
	if len(parts) == 3 && parts[0] == "conversations" {
		switch parts[2] {
		case "permission", "title", "capabilities":
			return path, method == "PATCH"
		}
	}
	if len(parts) == 5 && parts[0] == "conversations" && parts[2] == "requests" && parts[4] == "status" {
		return path, method == "GET"
	}
	if len(parts) == 2 && parts[0] == "attachments" {
		return path, method == "GET"
	}
	if len(parts) == 3 && parts[0] == "keys" {
		switch parts[2] {
		case "update", "delete", "models":
			return path, method == "POST"
		}
	}
	if len(parts) == 3 && parts[0] == "models" && parts[2] == "disable" {
		return path, method == "POST"
	}
	if len(parts) == 2 && parts[0] == "channels" && (parts[1] == "telegram" || parts[1] == "lark" || parts[1] == "qq") {
		return path, method == "GET" || method == "PATCH" || method == "POST"
	}
	return "", false
}

func (p *PublicServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w, p.host)
	c := p.local.tasks.Config().PublicWeb.Effective()
	ip, err := netip.ParseAddr(r.Header.Get("X-FreeAgent-Client-IP"))
	if !publicweb.TrustedProxy(r.Context()) || !c.Enabled || c.Origin != p.origin || r.Host != p.host || r.Header.Get("X-FreeAgent-Proxy-Proto") != "https" || err != nil || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != p.origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		p.denied(w, 403)
		return
	}
	source := ip.Unmap().String()
	token := cookieValue(r, publicweb.SessionCookie)
	s := p.auth.Lookup(token, source, r.UserAgent(), r.Header.Get("X-FreeAgent-Activity") == "1" && r.URL.Path != "/api/events")
	if !p.auth.Allowed(source, s != nil) {
		p.denied(w, 403)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		http.FileServerFS(Assets()).ServeHTTP(w, r)
		return
	}
	if r.Method != "GET" && r.Header.Get("Origin") != p.origin {
		p.denied(w, 403)
		return
	}
	if r.URL.Path == "/api/public/session" && r.Method == "GET" {
		if s == nil {
			var detect string
			if c.Security.CookieCheck {
				detect, err = p.auth.Challenge(source)
				if err != nil {
					p.denied(w, 403)
					return
				}
				setPublicCookie(w, publicweb.DetectionCookie, detect, 120)
			}
			p.local.writeJSON(w, 200, map[string]any{"public": true, "authenticated": false, "cookie_check": c.Security.CookieCheck})
			return
		}
		p.local.writeJSON(w, 200, map[string]any{"public": true, "authenticated": true, "csrf": s.CSRF, "max_permission": c.MaxPermission})
		return
	}
	if r.URL.Path == "/api/public/login" && r.Method == "POST" {
		var body struct {
			Password string `json:"password"`
		}
		if !readPublicBody(w, r, &body) {
			return
		}
		secret := []byte(body.Password)
		body.Password = ""
		defer clear(secret)
		value, session, err := p.auth.Login(r.Context(), source, r.UserAgent(), cookieValue(r, publicweb.DetectionCookie), secret)
		setPublicCookie(w, publicweb.DetectionCookie, "", -1)
		if err != nil {
			p.local.writeError(w, 401, "login_failed", "登录失败，请重新打开登录表单后重试。")
			return
		}
		setPublicCookie(w, publicweb.SessionCookie, value, c.AbsoluteHours*3600)
		p.local.writeJSON(w, 200, map[string]any{"authenticated": true, "csrf": session.CSRF})
		return
	}
	if s == nil {
		p.local.writeError(w, 401, "login_required", "会话已失效，请重新登录。已经开始的任务继续保留。")
		return
	}
	if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-FreeAgent-CSRF")), []byte(s.CSRF)) != 1 {
		p.denied(w, 403)
		return
	}
	if r.URL.Path == "/api/public/logout" && r.Method == "POST" {
		setPublicCookie(w, publicweb.SessionCookie, "", -1)
		p.local.writeJSON(w, 200, map[string]bool{"logged_out": true})
		p.auth.Logout(token)
		return
	}
	if r.URL.Path == "/api/public/security" && r.Method == "GET" {
		p.local.writeJSON(w, 200, p.auth.State())
		return
	}
	if r.URL.Path == "/api/public/password" && r.Method == "POST" {
		var body struct{ Current, Password string }
		if !readPublicBody(w, r, &body) {
			return
		}
		current, next := []byte(body.Current), []byte(body.Password)
		body.Current, body.Password = "", ""
		defer clear(current)
		defer clear(next)
		if err := p.auth.ChangePassword(current, next); err != nil {
			p.local.writeError(w, 400, "password", err.Error())
			return
		}
		p.auth.Refresh()
		p.local.writeJSON(w, 200, map[string]bool{"saved": true, "reauthenticate": true})
		return
	}
	path, allowed := publicRoute(r.URL.Path, r.Method)
	if !allowed {
		http.NotFound(w, r)
		return
	}
	// Admitted mutations settle under the Owner lifetime. Revoking their
	// reader must not interrupt the journal after configuration save.
	parent := r.Context()
	reading := r.Method == "GET" || r.Method == "POST" && r.URL.Path == "/api/attachments"
	if !reading {
		parent = p.local.ctx
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if reading {
		stop := context.AfterFunc(s.Context(), cancel)
		defer stop()
	}
	ctx = context.WithValue(ctx, publicRequestKey{}, publicRequest{s, source})
	ctx = harness.WithPublicEntry(ctx, "public:"+s.ID, source)
	ctx = harness.WithAttachmentIdentity(ctx, "public:"+s.ID)
	r = r.Clone(ctx)
	u := *r.URL
	u.Path = path
	r.URL = &u
	// External copies of these headers are never interpreted as capabilities.
	r.Header = r.Header.Clone()
	r.Header.Del("X-FreeAgent-Key")
	r.Header.Del("X-FreeAgent-Attach")
	if r.URL.Path == "/api/events" || strings.HasPrefix(r.URL.Path, "/api/attachments") {
		if !s.AcquireRead() {
			p.local.writeError(w, 429, "busy", "此会话的并发传输已达上限。")
			return
		}
		defer s.ReleaseRead()
	}
	writer := &publicWriter{ResponseWriter: w, session: s, execution: harness.WithAttachmentIdentity(harness.WithPublicEntry(p.local.ctx, "public:"+s.ID, source), "public:"+s.ID)}
	deadlineStop := context.AfterFunc(s.Context(), func() {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now())
		_ = controller.SetWriteDeadline(time.Now())
	})
	defer deadlineStop()
	p.local.ServeHTTP(writer, r)
}

func readPublicBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		http.Error(w, "Invalid input", 400)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Invalid input", 400)
		return false
	}
	return true
}

type publicWriter struct {
	http.ResponseWriter
	session   *publicweb.Session
	execution context.Context
}

func (w *publicWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *publicWriter) Write(raw []byte) (int, error) {
	select {
	case <-w.session.Context().Done():
		return 0, context.Canceled
	default:
		return w.ResponseWriter.Write(raw)
	}
}
func (w *publicWriter) Flush() {
	select {
	case <-w.session.Context().Done():
		return
	default:
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}
func (w *publicWriter) ExecutionContext() context.Context { return w.execution }
func (server *Server) executionContext(w http.ResponseWriter) context.Context {
	if p, ok := w.(interface{ ExecutionContext() context.Context }); ok {
		return p.ExecutionContext()
	}
	return server.ctx
}

// The local recovery surface is never part of the public allowlist.
func (server *Server) publicSettingsAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/public-settings" && r.URL.Path != "/api/public-recovery" && r.URL.Path != "/api/public-password" {
		return false
	}
	if _, remote := publicRequestFrom(r.Context()); remote && r.URL.Path != "/api/public-settings" {
		server.writeError(w, 403, "local_required", "此操作需要服务器本机入口。")
		return true
	}
	if r.URL.Path == "/api/public-settings" {
		if r.Method == "PUT" {
			var c harness.PublicWebConfig
			if !server.readBody(w, r, &c) {
				return true
			}
			if err := c.Validate(); err != nil {
				server.writeError(w, 400, "public_policy_invalid", err.Error())
				return true
			}
			if _, err := server.applyDirectControl(r.Context(), "", "", harness.ControlSetPublicWeb, c); err != nil {
				server.fail(w, err)
				return true
			}
		} else if r.Method != "GET" {
			w.WriteHeader(405)
			return true
		}
		server.mu.Lock()
		p := server.public
		server.mu.Unlock()
		config := server.tasks.Config().PublicWeb.Effective()
		server.mu.Lock()
		active := p != nil && p.socket != nil && p.socket.Active()
		server.mu.Unlock()
		restart := config.Enabled && (!active || p == nil || config.Origin != p.started.Origin || config.Socket != p.started.Socket || config.SocketGroup != p.started.SocketGroup)
		server.writeJSON(w, 200, map[string]any{"config": config, "credential_ready": publicweb.CredentialReady(server.tasks.DataDir()), "listener_active": active, "restart_required": restart})
		return true
	}
	if r.URL.Path == "/api/public-password" && r.Method == "POST" {
		var body struct {
			Password string `json:"password"`
		}
		if !readPublicBody(w, r, &body) {
			return true
		}
		secret := []byte(body.Password)
		body.Password = ""
		defer clear(secret)
		if err := publicweb.SetPassword(server.tasks.DataDir(), secret); err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, map[string]bool{"saved": true})
		server.mu.Lock()
		p := server.public
		server.mu.Unlock()
		if p != nil {
			p.auth.Refresh()
		}
		return true
	}
	server.mu.Lock()
	p := server.public
	server.mu.Unlock()
	if p == nil {
		server.writeJSON(w, 200, map[string]any{"active": false, "events": []any{}})
		return true
	}
	if r.Method == "POST" {
		p.auth.ResetLocks()
	} else if r.Method != "GET" {
		w.WriteHeader(405)
		return true
	}
	server.writeJSON(w, 200, p.auth.State())
	return true
}
