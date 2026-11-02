package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"net/http/httptest"
	"testing"
)

func TestLocalTunnelHostAndOrigin(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for _, spec := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:18765", "http://127.0.0.1:18765", 200},
		{"[::1]:18765", "http://[::1]:18765", 200},
		{"127.0.0.1:18765", "http://127.0.0.1:8765", 403},
		{"localhost:18765", "http://localhost:18765", 403},
		{"attacker.example:18765", "http://attacker.example:18765", 403},
		{"192.0.2.2:18765", "http://192.0.2.2:18765", 403},
	} {
		r := httptest.NewRequest("GET", "http://"+spec.host+"/api/environment", nil)
		r.Host = spec.host
		r.Header.Set("Origin", spec.origin)
		r.Header.Set("X-FreeAgent-Key", ui.server.token)
		w := httptest.NewRecorder()
		ui.server.ServeHTTP(w, r)
		if w.Code != spec.want {
			t.Fatalf("host=%s origin=%s: %d %s", spec.host, spec.origin, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", ui.http.URL+"/api/environment", nil)
	r.Header.Set("Cookie", "freeagent-chat="+ui.server.token)
	w := httptest.NewRecorder()
	ui.server.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("cookie still authenticates: %d", w.Code)
	}
}
