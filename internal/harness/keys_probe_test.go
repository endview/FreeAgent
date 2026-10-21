package harness

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const probeTestSecret = "FA-probe-fixture-key-0001"

func probeServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/models" || request.Header.Get("Accept") != "application/json" {
			t.Errorf("probe sent %s %s accept=%q", request.Method, request.URL.Path, request.Header.Get("Accept"))
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestProbeModelsCategories(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		status   int
		body     string
		category string
	}{
		{http.StatusOK, `{"object":"list"}`, "not_models"},
		{http.StatusOK, `not json`, "not_models"},
		{http.StatusUnauthorized, `{"error":"bad key"}`, "auth"},
		{http.StatusForbidden, `{"error":"forbidden"}`, "auth"},
		{http.StatusNotFound, `{"error":"no models route"}`, "not_found"},
		{http.StatusTooManyRequests, `{"error":"slow down"}`, "rate_limited"},
		{http.StatusInternalServerError, `{"error":"broken"}`, "server"},
		{http.StatusServiceUnavailable, `{"error":"busy"}`, "server"},
		{http.StatusTeapot, `{"error":"teapot"}`, "unexpected"},
	} {
		server := probeServer(t, test.status, test.body)
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, probeTestSecret)), nil)
		if err != nil || result.Category != test.category || result.Status != test.status || len(result.Models) != 0 {
			t.Errorf("%d %s: result=%+v err=%v", test.status, test.body, result, err)
		}
	}

	t.Run("ok", func(t *testing.T) {
		var authorization []string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			authorization = append(authorization, request.Header.Get("Authorization"))
			_, _ = writer.Write([]byte(`{"data":[{"id":"zeta"},{"id":"alpha"},{"id":"zeta"},{"id":" "}]}`))
		}))
		defer server.Close()
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, probeTestSecret)), nil)
		if err != nil || result.Category != "ok" || result.Status != http.StatusOK || !slices.Equal(result.Models, []string{"alpha", "zeta"}) {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if _, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, "")), nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(authorization, []string{"Bearer " + probeTestSecret, ""}) {
			t.Fatalf("authorization headers=%q", authorization)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := ProbeModels(cancelled, mustResolved(NewResolvedKey(server.URL, "")), nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled probe error=%v", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		var targetHits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, target.URL+"/models", http.StatusFound)
		}))
		defer server.Close()
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, probeTestSecret)), nil)
		if err != nil || result.Category != "redirect" || result.Status != http.StatusFound || targetHits.Load() != 0 {
			t.Fatalf("result=%+v err=%v target hits=%d", result, err, targetHits.Load())
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			select {
			case <-request.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
		defer server.Close()
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, "")), &http.Client{Timeout: 100 * time.Millisecond})
		if err != nil || result.Category != "timeout" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("tls", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("untrusted certificate reached the handler")
		}))
		defer server.Close()
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, probeTestSecret)), nil)
		if err != nil || result.Category != "tls" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("network", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		result, err := ProbeModels(ctx, mustResolved(NewResolvedKey("http://"+address, "")), nil)
		if err != nil || result.Category != "network" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("unnormalized base", func(t *testing.T) {
		for _, base := range []string{"", "https://Example.com/", "http://example.com"} {
			if result, err := ProbeModels(ctx, ResolvedKey{BaseURL: base}, nil); err == nil {
				t.Errorf("probe accepted %q: %+v", base, result)
			}
		}
	})
}

func TestProbeRedactsKey(t *testing.T) {
	ctx := context.Background()
	long := "x" + strings.Repeat("中", 200)
	for name, body := range map[string]string{
		"echoed key": `{"error":"invalid key ` + probeTestSecret + "\nsecond line\"} " + long,
		// Truncating before redaction would leave "FA-probe" at the cut.
		"key at cut": strings.Repeat("a", 292) + probeTestSecret,
	} {
		t.Run(name, func(t *testing.T) {
			server := probeServer(t, http.StatusUnauthorized, body)
			result, err := ProbeModels(ctx, mustResolved(NewResolvedKey(server.URL, probeTestSecret)), nil)
			if err != nil || result.Category != "auth" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			detail := result.Detail
			if strings.Contains(detail, probeTestSecret[:8]) || !strings.Contains(detail, "[redac") ||
				len(detail) > probeDetailBytes || !utf8.ValidString(detail) || strings.ContainsAny(detail, "\r\n") {
				t.Fatalf("detail not redacted, bounded, and single-line: %q", detail)
			}
		})
	}
}
