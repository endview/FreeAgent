package localchat

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanonicalAuthorityAcceptsLiteralLoopbackListeners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		address  net.Addr
		expected string
	}{
		{
			name: "IPv4 loopback",
			address: &net.TCPAddr{
				IP:   net.IPv4(127, 0, 0, 1),
				Port: 43127,
			},
			expected: "127.0.0.1:43127",
		},
		{
			name: "IPv6 loopback",
			address: &net.TCPAddr{
				IP:   net.IPv6loopback,
				Port: 43128,
			},
			expected: "[::1]:43128",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authority, err := CanonicalAuthority(test.address)
			if err != nil || authority != test.expected {
				t.Fatalf("CanonicalAuthority() = %q, %v; want %q", authority, err, test.expected)
			}
		})
	}

	for name, address := range map[string]net.Addr{
		"public IPv4": &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 43127},
		"wildcard":    &net.TCPAddr{IP: net.IPv4zero, Port: 43127},
		"zero port":   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
		"non-TCP":     stringAuthority("127.0.0.1:43127"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if authority, err := CanonicalAuthority(address); err == nil || authority != "" {
				t.Fatalf("CanonicalAuthority() accepted %v as %q", address, authority)
			}
		})
	}
}

func TestAuthorityGuardRequiresExactHostAndOrigin(t *testing.T) {
	t.Parallel()

	const authority = "127.0.0.1:43127"
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	guard, err := NewAuthorityGuard(next, authority)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		host       string
		origin     string
		wantStatus int
		wantNext   bool
	}{
		{name: "native client without Origin", host: authority, wantStatus: http.StatusOK, wantNext: true},
		{name: "exact browser Origin", host: authority, origin: "http://" + authority, wantStatus: http.StatusOK, wantNext: true},
		{name: "wrong Host", host: "rebind.test:43127", wantStatus: http.StatusForbidden},
		{name: "case-different Host", host: "127.0.0.1:43127 ", wantStatus: http.StatusForbidden},
		{name: "wrong Origin", host: authority, origin: "http://rebind.test:43127", wantStatus: http.StatusForbidden},
		{name: "null Origin", host: authority, origin: "null", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			guard.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
	request.Host = authority
	request.Header.Add("Origin", "http://"+authority)
	request.Header.Add("Origin", "http://rebind.test:43127")
	response := httptest.NewRecorder()
	guard.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("duplicate Origin status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestNewAuthorityGuardRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if _, err := NewAuthorityGuard(nil, "127.0.0.1:43127"); err == nil {
		t.Fatal("NewAuthorityGuard accepted nil handler")
	}
	for _, authority := range []string{
		"localhost:43127",
		"127.0.0.1:0",
		"127.0.0.1:043127",
		"127.0.0.1:65536",
		"127.0.0.1",
		"[::1%zone]:43127",
	} {
		if _, err := NewAuthorityGuard(next, authority); err == nil {
			t.Fatalf("NewAuthorityGuard accepted %q", authority)
		}
	}
}

type stringAuthority string

func (authority stringAuthority) Network() string { return "tcp" }
func (authority stringAuthority) String() string  { return string(authority) }
