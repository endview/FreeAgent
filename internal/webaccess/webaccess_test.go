package webaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestAdaptersSendOnlyQueryAndParseSources(t *testing.T) {
	fixtures := map[string]string{
		"deepseek":   `{"results":[{"title":"来源","url":"https://example.org/a","summary":"正文摘要","date":"2026-11-17"}]}`,
		"tavily":     `{"results":[{"title":"来源","url":"https://example.org/a","content":"正文摘要"}]}`,
		"brave":      `{"web":{"results":[{"title":"来源","url":"https://example.org/a","description":"正文摘要"}]}}`,
		"bocha":      `{"code":200,"data":{"webPages":{"value":[{"name":"来源","url":"https://example.org/a","summary":"正文摘要"}]}}}`,
		"searxng":    `{"results":[{"title":"来源","url":"https://example.org/a","content":"正文摘要"}]}`,
		"exa":        `{"results":[{"title":"来源","url":"https://example.org/a","text":"正文摘要"}]}`,
		"perplexity": `{"results":[{"title":"来源","url":"https://example.org/a","snippet":"正文摘要"}]}`,
		"jina":       `{"code":200,"data":[{"title":"来源","url":"https://example.org/a","description":"正文摘要"}]}`,
		"zhipu":      `{"search_result":[{"title":"来源","link":"https://example.org/a","content":"正文摘要","publish_date":"2026-11-17"}]}`,
		"serper":     `{"organic":[{"title":"来源","link":"https://example.org/a","snippet":"正文摘要"}]}`,
		"serpapi":    `{"organic_results":[{"title":"来源","link":"https://example.org/a","snippet":"正文摘要"}]}`,
	}
	for _, p := range Providers() {
		t.Run(p.ID, func(t *testing.T) {
			requests := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Cookie") != "" {
					t.Error("cookie leaked")
				}
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), "messages") || strings.Contains(string(raw), "history") {
					t.Error("private context sent")
				}
				if p.ID == "brave" && r.Header.Get("X-Subscription-Token") != "fixture-secret" {
					t.Error("Brave auth")
				}
				if p.ID == "exa" && r.Header.Get("x-api-key") != "fixture-secret" {
					t.Error("Exa auth")
				}
				if r.Method == "GET" && r.URL.Query().Get("q") == "" {
					t.Error("missing query")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, fixtures[p.ID])
			}))
			defer s.Close()
			c := Client{BaseURL: s.URL, Secret: "fixture-secret", Provider: p.ID, Config: DefaultConfig()}
			result, err := c.Search(t.Context(), "中文 query", 2)
			if err != nil || len(result.Results) != 1 || result.Results[0].URL != "https://example.org/a" || result.Results[0].Snippet == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if requests != 1 {
				t.Fatalf("unexpected request count %d", requests)
			}
		})
	}
}

func TestSearchRejectsRedirectAndDoesNotLeakKeyOrRetry(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	c := Client{Provider: "serpapi", BaseURL: s.URL, Secret: "fixture-secret", Config: DefaultConfig()}
	_, err := c.Search(t.Context(), "one", 1)
	if err == nil || strings.Contains(err.Error(), c.Secret) || targetCalls != 0 {
		t.Fatalf("redirect leaked/retried: %v calls=%d", err, targetCalls)
	}
	s.Close()
	_, err = c.Search(t.Context(), "two", 1)
	if err == nil || strings.Contains(err.Error(), c.Secret) || strings.Contains(err.Error(), "api_key") {
		t.Fatalf("request URL leaked: %v", err)
	}
}

func TestSearchEmptyResultsAndProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		body string
		fail bool
	}{{`{"results":[]}`, false}, {`{}`, true}, {`{"results":null}`, true}, {`{"error":{"message":"secret"}}`, true}, {`not-json`, true}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
		c := Client{Provider: "deepseek", BaseURL: s.URL, Secret: "secret", Config: DefaultConfig()}
		result, e := c.Search(t.Context(), "query", 1)
		s.Close()
		if (e != nil) != tc.fail {
			t.Fatalf("%s: %+v %v", tc.body, result, e)
		}
	}
}

func TestPublicDestinationsAndDNSGuard(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1"} {
		if PublicAddress(netip.MustParseAddr(raw)) {
			t.Error("permitted " + raw)
		}
	}
	if !PublicAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Error("public destination denied")
	}
	client := NewHTTPClient(time.Second, false)
	_, err := client.Get("http://127.0.0.1:9/")
	if err == nil || !strings.Contains(err.Error(), "destination denied") {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPageExtractsTextAndRejectsEncodedOrBinaryBodies(t *testing.T) {
	for _, tc := range []struct {
		mime, encoding, body string
		fail                 bool
	}{
		{"text/html; charset=utf-8", "", `<title>资料 &amp; 来源</title><script>steal()</script><style>private</style><p>中文正文</p><p>第二段 &lt;safe&gt;</p>`, false},
		{"application/octet-stream", "", "binary", true},
		{"text/plain", "gzip", "compressed", true},
		{"text/plain", "", strings.Repeat("x", 2<<20+1), true},
	} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("credentials on page")
			}
			h := http.Header{"Content-Type": []string{tc.mime}}
			if tc.encoding != "" {
				h.Set("Content-Encoding", tc.encoding)
			}
			return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})}
		page, e := fetchWithClient(t.Context(), "https://example.org/a#heading", DefaultConfig(), client)
		if (e != nil) != tc.fail {
			t.Fatalf("%s err=%v", tc.mime, e)
		}
		if !tc.fail && (strings.Contains(page.Content, "steal") || strings.Contains(page.Content, "private") || !strings.Contains(page.Content, "中文正文") || strings.Contains(page.URL, "#")) {
			t.Fatalf("bad page %+v", page)
		}
	}
}

func TestOutputBudgetAndCancellation(t *testing.T) {
	config := DefaultConfig()
	config.MaxOutputBytes = 1024
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []Result{{Title: "very long", URL: "https://example.org/a", Snippet: strings.Repeat("中文", 3000)}}})
	}))
	defer s.Close()
	c := Client{Provider: "deepseek", BaseURL: s.URL, Secret: "secret", Config: config}
	result, e := c.Search(t.Context(), "query", 1)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(result)
	if len(raw) > 1024 || !result.Truncated {
		t.Fatalf("unbounded %d %+v", len(raw), result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = c.Search(ctx, "query", 1)
	if e == nil {
		t.Fatal("cancelled query sent")
	}
	var status *HTTPError
	if errors.As(e, &status) {
		t.Fatal("cancelled request reported HTTP status")
	}
}
