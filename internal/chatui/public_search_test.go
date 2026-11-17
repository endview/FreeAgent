package chatui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/webaccess"
)

func TestPublicSearchSettingsLoadSaveAndProbe(t *testing.T) {
	f := newPublicFixture(t, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/settings/web-search"},
		{"PATCH", "/api/settings/web-search"},
		{"POST", "/api/settings/web-search/test"},
	} {
		if w := f.request(t, route.method, route.path, nil, nil); w.Code != 401 {
			t.Fatalf("search route bypassed login: %s %s %d", route.method, route.path, w.Code)
		}
	}
	f.login(t)
	w := f.request(t, "GET", "/api/settings/web-search", nil, nil)
	var first struct {
		Config    webaccess.Config     `json:"config"`
		Legacy    bool                 `json:"legacy"`
		Providers []webaccess.Provider `json:"providers"`
		Revisions map[string]uint64    `json:"expected_revisions"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || !first.Legacy || len(first.Providers) == 0 {
		t.Fatalf("public settings did not load: %d %s", w.Code, w.Body.String())
	}
	var calls atomic.Int32
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "FreeAgent official documentation" {
			t.Errorf("unexpected probe: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Fixture","url":"https://example.org/source","content":"Synthetic search result"}]}`))
	}))
	defer search.Close()
	w = f.request(t, "POST", "/api/keys", strings.NewReader(`{"name":"Public search fixture","protocol":"search","base_url":"`+search.URL+`","source":"none"}`), nil)
	var key struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &key) != nil || key.ID == "" {
		t.Fatalf("public search Key: %d %s", w.Code, w.Body.String())
	}
	policy := webaccess.DefaultConfig()
	policy.Mode, policy.DefaultConnection = "api", "public-search"
	policy.Connections = map[string]webaccess.Connection{"public-search": {Provider: "searxng", Key: key.ID}}
	body := mustJSONTest(map[string]any{"config": policy, "expected_revisions": first.Revisions})
	for _, route := range []struct{ method, path string }{
		{"PATCH", "/api/settings/web-search"},
		{"POST", "/api/settings/web-search/test"},
	} {
		w := f.request(t, route.method, route.path, bytes.NewReader(body), map[string]string{"X-FreeAgent-CSRF": ""})
		if w.Code != 403 {
			t.Fatalf("search mutation bypassed CSRF: %s %d", route.path, w.Code)
		}
	}
	w = f.request(t, "PATCH", "/api/settings/web-search", bytes.NewReader(body), nil)
	if w.Code != 200 {
		t.Fatalf("public search save: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("saving a search connection performed a probe")
	}
	w = f.request(t, "GET", "/api/settings/web-search", nil, nil)
	var saved struct{ Config webaccess.Config }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Config.DefaultConnection != "public-search" || saved.Config.Mode != "api" {
		t.Fatalf("public search readback: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, "PATCH", "/api/settings/web-search", bytes.NewReader(body), nil)
	if w.Code != 409 {
		t.Fatalf("public stale draft overwrote settings: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/settings/web-search/test", strings.NewReader(`{"connection":"public-search"}`), nil)
	if w.Code != 200 || calls.Load() != 1 || !strings.Contains(w.Body.String(), `"result_count":1`) {
		t.Fatalf("public fixed-query probe: %d %s, calls=%d", w.Code, w.Body.String(), calls.Load())
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/settings/web-search"},
		{"GET", "/api/settings/web-search/test"},
		{"PATCH", "/api/settings/web-search/test"},
		{"POST", "/api/settings/web-search/private"},
	} {
		if w := f.request(t, route.method, route.path, nil, nil); w.Code != 404 {
			t.Fatalf("unexpected search route exposed: %s %s %d", route.method, route.path, w.Code)
		}
	}
}
