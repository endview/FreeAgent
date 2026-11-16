package webaccess

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in; credentials only come from child-process environment. The
// result file contains source URLs and measurements, never credentials/body
// dumps. Unit fixtures do not substitute for these account-level checks.
func TestSearchAPIsLive(t *testing.T) {
	if os.Getenv("FREEAGENT_SEARCH_ACCEPTANCE") != "1" {
		t.Skip("live search acceptance not selected")
	}
	type check struct {
		Provider   string   `json:"provider"`
		Status     string   `json:"status"`
		Query      string   `json:"query"`
		Count      int      `json:"count"`
		URLs       []string `json:"urls,omitempty"`
		DurationMS int64    `json:"duration_ms"`
		Bytes      int      `json:"normalized_bytes,omitempty"`
		Error      string   `json:"error,omitempty"`
	}
	var checks []check
	for _, provider := range []string{"deepseek", "tavily", "bocha", "brave"} {
		if selected := os.Getenv("FREEAGENT_SEARCH_PROVIDER"); selected != "" && selected != provider {
			continue
		}
		t.Run(provider, func(t *testing.T) {
			secret := os.Getenv("FA_SEARCH_" + strings.ToUpper(provider) + "_KEY")
			if secret == "" {
				t.Fatal("credential missing; no request sent")
			}
			base := ""
			for _, p := range Providers() {
				if p.ID == provider {
					base = p.BaseURL
				}
			}
			config := DefaultConfig()
			config.TimeoutSeconds = 45
			client := Client{Provider: provider, BaseURL: base, Secret: secret, Config: config}
			if os.Getenv("FREEAGENT_SEARCH_TRACE") == "1" {
				client.HTTP = NewHTTPClient(time.Duration(config.TimeoutSeconds)*time.Second, true)
				transport := client.HTTP.Transport
				client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					trace := &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { t.Logf("connection remote=%s", i.Conn.RemoteAddr()) }, TLSHandshakeDone: func(_ tls.ConnectionState, e error) {
						if e != nil {
							t.Logf("TLS: %s", strings.ReplaceAll(e.Error(), secret, "[redacted]"))
						}
					}}
					res, e := transport.RoundTrip(r.WithContext(httptrace.WithClientTrace(r.Context(), trace)))
					if e != nil {
						t.Logf("transport: %T %s", e, strings.ReplaceAll(e.Error(), secret, "[redacted]"))
					}
					return res, e
				})
			}
			for _, query := range []string{"Go official downloads site:go.dev", "中国科学院 官方网站"} {
				started := time.Now()
				result, err := client.Search(t.Context(), query, 3)
				entry := check{Provider: provider, Query: query, DurationMS: time.Since(started).Milliseconds(), Count: len(result.Results), Status: "PASS"}
				if err != nil {
					entry.Status = "FAIL"
					entry.Error = strings.ReplaceAll(err.Error(), secret, "[redacted]")
					checks = append(checks, entry)
					t.Errorf("%s: %s", provider, entry.Error)
					return // no retry or second charged query after failure
				}
				for _, r := range result.Results {
					entry.URLs = append(entry.URLs, r.URL)
				}
				raw, _ := json.Marshal(result)
				entry.Bytes = len(raw)
				if len(result.Results) == 0 || len(raw) > config.MaxOutputBytes || strings.Contains(string(raw), secret) {
					entry.Status = "FAIL"
					entry.Error = "empty, oversized or credential-bearing result"
					t.Error(entry.Error)
				}
				checks = append(checks, entry)
				t.Logf("%s: %d sources, %d normalized UTF-8 bytes, %d ms", provider, entry.Count, entry.Bytes, entry.DurationMS)
			}
		})
	}
	if path := os.Getenv("FREEAGENT_SEARCH_ACCEPTANCE_REPORT"); path != "" {
		raw, _ := json.MarshalIndent(checks, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal("cannot save sanitized acceptance report")
		}
	}
}
