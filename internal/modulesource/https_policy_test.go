package modulesource

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCanonicalHTTPSOriginAndIndexURL(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{
		"https://example.test",
		"https://example.test:8443",
		"https://[2606:4700:4700::1111]",
	} {
		if got, err := parseCanonicalHTTPSOrigin(origin); err != nil || got != origin {
			t.Fatalf("canonical origin %q got=%q err=%v", origin, got, err)
		}
	}
	for _, rawURL := range []string{
		"https://example.test/index.json",
		"https://example.test:8443/modules/index.json",
		"https://example.test/modules/%E6%A8%A1%E5%9D%97.json",
		"https://[2606:4700:4700::1111]/index.json",
	} {
		parsed, origin, err := parseCanonicalHTTPSIndexURL(rawURL)
		if err != nil || parsed.String() != rawURL || origin == "" {
			t.Fatalf("canonical URL %q parsed=%v origin=%q err=%v",
				rawURL, parsed, origin, err)
		}
	}
	for _, invalid := range []string{
		"http://example.test/index.json",
		"https://Example.test/index.json",
		"https://example.test./index.json",
		"https://" + "user" + "@" + "example.test/index.json",
		"https://example.test:443/index.json",
		"https://example.test",
		"https://example.test//index.json",
		"https://example.test/a/../index.json",
		"https://example.test/%2e%2e/index.json",
		"https://example.test/%69ndex.json",
		"https://example.test/index%2fchild.json",
		"https://example.test/index.json?revision=1",
		"https://example.test/index.json#fragment",
		"https://example.test/index.json ",
		"https://例子.invalid/index.json",
		"https://[fe80::1%25zone]/index.json",
	} {
		if _, _, err := parseCanonicalHTTPSIndexURL(invalid); err == nil {
			t.Fatalf("non-canonical HTTPS index URL accepted: %q", invalid)
		}
	}
	for _, invalid := range []string{
		"https://example.test/",
		"https://example.test/index.json",
		"https://example.test:443",
		"https://Example.test",
	} {
		if _, err := parseCanonicalHTTPSOrigin(invalid); err == nil {
			t.Fatalf("non-canonical HTTPS allowlist origin accepted: %q", invalid)
		}
	}
}

func TestNewProviderFreezesExactHTTPSAllowlist(t *testing.T) {
	t.Parallel()
	values := []string{"https://b.example.test", "https://a.example.test"}
	provider, err := newProvider(Config{
		HTTPSIndexEnabled:    true,
		HTTPSOriginAllowlist: values,
	}, httpsDependencies{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) { return nil, nil },
		dialContext:  func(context.Context, string, string) (net.Conn, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	values[0] = "https://mutated.example.test"
	if _, exists := provider.httpsOrigins["https://b.example.test"]; !exists {
		t.Fatal("provider aliases caller allowlist")
	}

	invalid := []Config{
		{HTTPSOriginAllowlist: []string{"https://a.example.test"}},
		{HTTPSIndexEnabled: true},
		{HTTPSIndexEnabled: true, HTTPSOriginAllowlist: []string{
			"https://a.example.test", "https://a.example.test",
		}},
		{HTTPSIndexEnabled: true, HTTPSOriginAllowlist: []string{
			"https://a.example.test/index.json",
		}},
		{HTTPSIndexEnabled: true, HTTPSOriginAllowlist: []string{
			"https://a.example.test",
		}, Timeout: time.Microsecond},
	}
	for index, config := range invalid {
		if _, err := newProvider(config, httpsDependencies{
			lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) { return nil, nil },
			dialContext:  func(context.Context, string, string) (net.Conn, error) { return nil, nil },
		}); err == nil || FailureCodeOf(err) != FailureSourceInputInvalid {
			t.Fatalf("invalid config %d accepted or wrong code: %v", index, err)
		}
	}
}

func TestBoundedPublicDialerRejectsMixedSpecialAndExcessDNS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		addresses []net.IPAddr
	}{
		{name: "mixed private", addresses: []net.IPAddr{
			{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")},
		}},
		{name: "special", addresses: []net.IPAddr{{IP: net.ParseIP("192.31.196.1")}}},
		{name: "documentation", addresses: []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}}},
		{name: "zone", addresses: []net.IPAddr{{IP: net.ParseIP("2606:4700:4700::1111"), Zone: "eth0"}}},
		{name: "too many", addresses: repeatedIPAnswers(maximumDNSAnswers + 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var dialed atomic.Int32
			dialer := &boundedPublicDialer{
				host: "source.example.test", port: "443",
				lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
					return test.addresses, nil
				},
				dialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed.Add(1)
					return nil, nil
				},
			}
			if _, err := dialer.DialContext(
				context.Background(), "tcp", "source.example.test:443",
			); err == nil {
				t.Fatal("unsafe DNS answer accepted")
			}
			if dialed.Load() != 0 {
				t.Fatal("unsafe DNS answer reached network dial")
			}
		})
	}
}

func TestBoundedPublicDialerSortsAndDialsExactlyOnce(t *testing.T) {
	t.Parallel()
	var lookupCount, dialCount atomic.Int32
	var selected string
	clientConnection, serverConnection := net.Pipe()
	defer serverConnection.Close()
	dialer := &boundedPublicDialer{
		host: "source.example.test", port: "8443",
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			lookupCount.Add(1)
			return []net.IPAddr{
				{IP: net.ParseIP("9.9.9.9")},
				{IP: net.ParseIP("8.8.8.8")},
			}, nil
		},
		dialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			dialCount.Add(1)
			selected = network + " " + address
			return clientConnection, nil
		},
	}
	connection, err := dialer.DialContext(
		context.Background(), "tcp", "source.example.test:8443",
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if selected != "tcp 8.8.8.8:8443" || lookupCount.Load() != 1 ||
		dialCount.Load() != 1 {
		t.Fatalf("selected=%q lookups=%d dials=%d",
			selected, lookupCount.Load(), dialCount.Load())
	}
	if _, err := dialer.DialContext(
		context.Background(), "tcp", "source.example.test:8443",
	); err == nil || lookupCount.Load() != 1 || dialCount.Load() != 1 {
		t.Fatalf("dialer replay accepted: err=%v lookups=%d dials=%d",
			err, lookupCount.Load(), dialCount.Load())
	}
}

func TestHTTPSAddressClassificationRejectsSpecialUse(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"0.1.2.3", "10.0.0.1", "100.64.0.1", "127.0.0.1",
		"169.254.1.1", "192.0.2.1", "192.31.196.1", "192.52.193.1",
		"192.175.48.1", "198.18.0.1", "198.51.100.1", "203.0.113.1",
		"224.0.0.1", "240.0.0.1", "::1", "64:ff9b::808:808",
		"2001:db8::1", "2002::1", "3fff::1", "fec0::1",
	} {
		if isPublicHTTPSAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("special-use address classified public: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "9.9.9.9", "2606:4700:4700::1111"} {
		if !isPublicHTTPSAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("ordinary public address rejected: %s", raw)
		}
	}
}

func TestObserveHTTPSResponsePolicyAndByteLimits(t *testing.T) {
	index := testDiscoveryIndex(t)
	noncanonical := append([]byte(nil), index...)
	noncanonical[len(noncanonical)-1] = ' '
	tests := []struct {
		name        string
		status      int
		contentType string
		encoding    string
		body        []byte
		want        FailureCode
	}{
		{name: "wrong status", status: http.StatusNoContent, contentType: moduleIndexMediaType, want: FailureSourceDenied},
		{name: "missing MIME", status: http.StatusOK, body: index, want: FailureSourceDenied},
		{name: "wrong MIME", status: http.StatusOK, contentType: "text/plain", body: index, want: FailureSourceDenied},
		{name: "extra MIME parameter", status: http.StatusOK, contentType: "application/json; profile=x", body: index, want: FailureSourceDenied},
		{name: "encoded", status: http.StatusOK, contentType: moduleIndexMediaType, encoding: "gzip", body: index, want: FailureSourceDenied},
		{name: "oversized", status: http.StatusOK, contentType: moduleIndexMediaType, body: append(append([]byte(nil), index...), 'x'), want: FailureSourceDenied},
		{name: "noncanonical", status: http.StatusOK, contentType: moduleIndexMediaType, body: noncanonical, want: FailureSourceIndexInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					if test.contentType != "" {
						writer.Header().Set("Content-Type", test.contentType)
					}
					if test.encoding != "" {
						writer.Header().Set("Content-Encoding", test.encoding)
					}
					writer.WriteHeader(test.status)
					_, _ = writer.Write(test.body)
				},
			))
			server.StartTLS()
			defer server.Close()
			provider, rawURL, policyCanonical, policyID, _ :=
				testHTTPSProvider(t, server, "/index.json", index)
			_, err := provider.Observe(context.Background(), ObserveRequest{
				SourcePolicyCanonical: policyCanonical,
				SourcePolicyID:        policyID,
				HTTPSIndexURL:         rawURL,
			})
			requireFailureCode(t, err, test.want)
		})
	}
}

func TestObserveHTTPSRejectsOversizedHeadersAndDoesNotRetryFailure(t *testing.T) {
	index := testDiscoveryIndex(t)
	t.Run("oversized response headers", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(
			func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", moduleIndexMediaType)
				writer.Header().Set("X-Oversized", strings.Repeat("x", maximumResponseHeader+1))
				_, _ = writer.Write(index)
			},
		))
		server.StartTLS()
		defer server.Close()
		provider, rawURL, policy, policyID, counters :=
			testHTTPSProvider(t, server, "/index.json", index)
		_, err := provider.Observe(context.Background(), ObserveRequest{
			SourcePolicyCanonical: policy,
			SourcePolicyID:        policyID,
			HTTPSIndexURL:         rawURL,
		})
		requireFailureCode(t, err, FailureSourceUnavailable)
		if requests.Load() != 1 || counters.lookups.Load() != 1 || counters.dials.Load() != 1 {
			t.Fatalf("header failure retried: requests=%d lookups=%d dials=%d",
				requests.Load(), counters.lookups.Load(), counters.dials.Load())
		}
	})

	t.Run("connection failure after request", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(
			func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				hijacker, ok := writer.(http.Hijacker)
				if !ok {
					t.Error("test response writer cannot hijack")
					return
				}
				connection, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("Hijack() error=%v", err)
					return
				}
				_ = connection.Close()
			},
		))
		server.StartTLS()
		defer server.Close()
		provider, rawURL, policy, policyID, counters :=
			testHTTPSProvider(t, server, "/index.json", index)
		_, err := provider.Observe(context.Background(), ObserveRequest{
			SourcePolicyCanonical: policy,
			SourcePolicyID:        policyID,
			HTTPSIndexURL:         rawURL,
		})
		requireFailureCode(t, err, FailureSourceUnavailable)
		if requests.Load() != 1 || counters.lookups.Load() != 1 || counters.dials.Load() != 1 {
			t.Fatalf("connection failure retried: requests=%d lookups=%d dials=%d",
				requests.Load(), counters.lookups.Load(), counters.dials.Load())
		}
	})
}

func TestObserveHTTPSIgnoresProxyAndHonorsOverallDeadline(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	index := testDiscoveryIndex(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", moduleIndexMediaType)
			_, _ = writer.Write(index)
		},
	))
	server.StartTLS()
	defer server.Close()
	provider, rawURL, policyCanonical, policyID, counters :=
		testHTTPSProvider(t, server, "/index.json", index)
	if _, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		HTTPSIndexURL:         rawURL,
	}); err != nil || counters.dials.Load() != 1 {
		t.Fatalf("explicit no-proxy request failed: err=%v dials=%d", err, counters.dials.Load())
	}

	blockingServer := httptest.NewUnstartedServer(http.HandlerFunc(
		func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		},
	))
	blockingServer.StartTLS()
	defer blockingServer.Close()
	_, port, err := net.SplitHostPort(blockingServer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	timeoutURL := "https://source.example.test:" + port + "/index.json"
	timeoutProvider, err := newProvider(Config{
		HTTPSIndexEnabled: true,
		HTTPSOriginAllowlist: []string{
			"https://source.example.test:" + port,
		},
		Timeout: 25 * time.Millisecond,
	}, httpsDependencies{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		},
		dialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, blockingServer.Listener.Addr().String())
		},
		tlsConfig: &tls.Config{InsecureSkipVerify: true}, // test-only hermetic TLS
	})
	if err != nil {
		t.Fatal(err)
	}
	timeoutPolicy, timeoutPolicyID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindHTTPSIndexV1,
		moduleapi.ModuleSourceNetworkExactHTTPSV1,
		timeoutURL,
		uint64(len(index)),
	)
	_, err = timeoutProvider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: timeoutPolicy,
		SourcePolicyID:        timeoutPolicyID,
		HTTPSIndexURL:         timeoutURL,
	})
	requireFailureCode(t, err, FailureSourceCancelled)
}

func TestObserveHTTPSRejectsUnallowedOrPolicyMismatchedURLBeforeNetwork(t *testing.T) {
	t.Parallel()
	index := testDiscoveryIndex(t)
	allowedURL := "https://allowed.example.test/index.json"
	policy, policyID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindHTTPSIndexV1,
		moduleapi.ModuleSourceNetworkExactHTTPSV1,
		allowedURL,
		uint64(len(index)),
	)
	var lookups, dials atomic.Int32
	provider, err := newProvider(Config{
		HTTPSIndexEnabled: true,
		HTTPSOriginAllowlist: []string{
			"https://allowed.example.test",
			"https://other.example.test",
		},
	}, httpsDependencies{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			lookups.Add(1)
			return nil, nil
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{
		"https://not-allowed.example.test/index.json",
		"https://other.example.test/index.json",
		"https://allowed.example.test/other.json",
		"https://127.0.0.1/index.json",
	} {
		_, err := provider.Observe(context.Background(), ObserveRequest{
			SourcePolicyCanonical: policy,
			SourcePolicyID:        policyID,
			HTTPSIndexURL:         rawURL,
		})
		requireFailureCode(t, err, FailureSourceDenied)
	}
	if lookups.Load() != 0 || dials.Load() != 0 {
		t.Fatalf("denied URL reached network: lookups=%d dials=%d", lookups.Load(), dials.Load())
	}
}

func repeatedIPAnswers(count int) []net.IPAddr {
	output := make([]net.IPAddr, count)
	for index := range output {
		output[index] = net.IPAddr{IP: net.ParseIP("8.8.8.8")}
	}
	return output
}
