package browser

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// All browser HTTP(S)/WebSocket traffic uses this proxy. DNS is resolved here,
// then the checked address is dialled directly, including CONNECT tunnels.
type guard struct {
	origins   map[string]bool
	transport *http.Transport
}

func newGuard(origins []string) (*guard, error) {
	g := &guard{origins: map[string]bool{}}
	for _, s := range origins {
		u, e := url.Parse(s)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return nil, errors.New("allowed_origins must contain exact HTTP(S) origins")
		}
		g.origins[u.Scheme+"://"+u.Host] = true
	}
	g.transport = &http.Transport{Proxy: nil, DialContext: g.dial, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	return g, nil
}
func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16"} {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func (g *guard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	privateAllowed := false
	for origin := range g.origins {
		u, _ := url.Parse(origin)
		p := u.Port()
		if p == "" {
			p = "443"
			if u.Scheme == "http" {
				p = "80"
			}
		}
		privateAllowed = privateAllowed || strings.EqualFold(u.Hostname(), host) && p == port
	}
	if len(g.origins) > 0 && !privateAllowed {
		return nil, errors.New("origin outside configured scope")
	}
	if !privateAllowed && port != "80" && port != "443" {
		return nil, errors.New("public browser traffic is limited to HTTP/HTTPS ports")
	}
	ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("DNS unavailable")
	}
	for _, ip := range ips {
		if !privateAllowed && !publicIP(ip) {
			return nil, errors.New("non-public destination denied")
		}
	}
	d := net.Dialer{Timeout: 12 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}
func (g *guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "CONNECT" {
		host, port, e := net.SplitHostPort(r.Host)
		if e != nil || host == "" {
			http.Error(w, "invalid tunnel", 400)
			return
		}
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			http.Error(w, "invalid port", 400)
			return
		}
		remote, e := g.dial(r.Context(), "tcp", r.Host)
		if e != nil {
			http.Error(w, "destination denied", 403)
			return
		}
		client, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		_, e = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if e == nil {
			e = rw.Flush()
		}
		if e != nil {
			client.Close()
			remote.Close()
			return
		}
		go func() {
			defer client.Close()
			defer remote.Close()
			_ = client.SetDeadline(time.Now().Add(10 * time.Minute))
			_ = remote.SetDeadline(time.Now().Add(10 * time.Minute))
			go func() { _, _ = io.Copy(remote, rw) }()
			_, _ = io.Copy(client, remote)
		}()
		return
	}
	if r.URL.Scheme != "http" || r.URL.User != nil {
		http.Error(w, "HTTP origin required", 400)
		return
	}
	if len(g.origins) > 0 && !g.origins["http://"+r.URL.Host] {
		http.Error(w, "origin denied", 403)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		address := r.URL.Host
		if r.URL.Port() == "" {
			address = net.JoinHostPort(r.URL.Hostname(), "80")
		}
		remote, e := g.dial(r.Context(), "tcp", address)
		if e != nil {
			http.Error(w, "destination denied", 403)
			return
		}
		r.Header.Del("Proxy-Authorization")
		if e = r.Write(remote); e != nil {
			remote.Close()
			http.Error(w, "websocket unavailable", 502)
			return
		}
		client, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		go func() {
			defer client.Close()
			defer remote.Close()
			_ = client.SetDeadline(time.Now().Add(10 * time.Minute))
			_ = remote.SetDeadline(time.Now().Add(10 * time.Minute))
			go func() { _, _ = io.Copy(remote, rw) }()
			_, _ = io.Copy(client, remote)
		}()
		return
	}
	r.RequestURI = ""
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")
	response, e := g.transport.RoundTrip(r)
	if e != nil {
		http.Error(w, "destination unavailable or denied", 403)
		return
	}
	defer response.Body.Close()
	for k, v := range response.Header {
		if strings.EqualFold(k, "Connection") {
			continue
		}
		w.Header()[k] = v
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 32<<20))
}
