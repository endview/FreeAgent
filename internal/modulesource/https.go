package modulesource

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maximumDNSAnswers     = 16
	maximumResponseHeader = 16 << 10
	moduleIndexMediaType  = "application/json"
	defaultHTTPSPort      = "443"
)

var (
	errHTTPSResolution = errors.New("HTTPS source resolution failed")
	errHTTPSNotPublic  = errors.New("HTTPS source address is not public")
	errHTTPSDial       = errors.New("HTTPS source dial failed")
)

func (provider *Provider) observeHTTPSIndex(
	ctx context.Context,
	policy moduleapi.ModuleSourcePolicyV1,
	rawURL string,
) ([]byte, error) {
	parsed, origin, err := parseCanonicalHTTPSIndexURL(rawURL)
	if err != nil {
		return nil, observationFailure(FailureSourceInputInvalid, err)
	}
	if _, allowed := provider.httpsOrigins[origin]; !allowed {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index origin is not explicitly allowed"),
		)
	}
	digest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindHTTPSIndexV1,
		[]byte(rawURL),
	)
	if err != nil || digest != policy.OriginDigest {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index URL differs from source policy"),
		)
	}
	if literal, parseErr := netip.ParseAddr(parsed.Hostname()); parseErr == nil &&
		!isPublicHTTPSAddress(literal.Unmap()) {
		return nil, observationFailure(FailureSourceDenied, errHTTPSNotPublic)
	}

	port := parsed.Port()
	if port == "" {
		port = defaultHTTPSPort
	}
	dialer := &boundedPublicDialer{
		host:         parsed.Hostname(),
		port:         port,
		lookupIPAddr: provider.https.lookupIPAddr,
		dialContext:  provider.https.dialContext,
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if provider.https.tlsConfig != nil {
		tlsConfig = provider.https.tlsConfig.Clone()
		if tlsConfig.MinVersion < tls.VersionTLS12 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		if tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS12 {
			return nil, observationFailure(
				FailureInternal,
				errors.New("TLS maximum version is below TLS 1.2"),
			)
		}
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      false,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxIdleConns:           0,
		MaxIdleConnsPerHost:    0,
		IdleConnTimeout:        0,
		TLSHandshakeTimeout:    provider.timeout,
		ResponseHeaderTimeout:  provider.timeout,
		ExpectContinueTimeout:  0,
		MaxResponseHeaderBytes: maximumResponseHeader,
		TLSClientConfig:        tlsConfig,
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, observationFailure(FailureSourceInputInvalid, err)
	}
	// Close plus a fresh no-keepalive Transport freezes this explicit refresh
	// to one non-reusable connection; the request context is the sole overall
	// timeout and cancellation authority (Client.Timeout intentionally stays 0).
	request.Close = true
	request.Header.Set("Accept", moduleIndexMediaType)
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			ctx.Err() != nil {
			return nil, cancelledObservation(firstContextError(ctx, err))
		}
		if errors.Is(err, errHTTPSNotPublic) || errors.Is(err, errHTTPSResolution) {
			return nil, observationFailure(FailureSourceDenied, err)
		}
		return nil, observationFailure(FailureSourceUnavailable, errHTTPSDial)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index response status is not 200"),
		)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index response must not be content encoded"),
		)
	}
	if !validIndexContentType(response.Header.Get("Content-Type")) {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index response MIME type is invalid"),
		)
	}
	if response.ContentLength > int64(policy.MaxIndexBytes) {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index exceeds policy byte limit"),
		)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, int64(policy.MaxIndexBytes)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, cancelledObservation(ctx.Err())
		}
		return nil, observationFailure(FailureSourceUnavailable, err)
	}
	if uint64(len(content)) > policy.MaxIndexBytes {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("HTTPS index exceeds policy byte limit"),
		)
	}
	if ctx.Err() != nil {
		return nil, cancelledObservation(ctx.Err())
	}
	return append([]byte(nil), content...), nil
}

func firstContextError(ctx context.Context, fallback error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fallback
}

func validIndexContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || strings.ToLower(mediaType) != moduleIndexMediaType {
		return false
	}
	for name, value := range parameters {
		if strings.ToLower(name) != "charset" ||
			!strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

type boundedPublicDialer struct {
	host         string
	port         string
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	dialContext  func(context.Context, string, string) (net.Conn, error)
	used         atomic.Bool
}

func (dialer *boundedPublicDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	if dialer == nil || ctx == nil || dialer.lookupIPAddr == nil ||
		dialer.dialContext == nil || !dialer.used.CompareAndSwap(false, true) {
		return nil, errHTTPSResolution
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errHTTPSResolution
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != dialer.host || port != dialer.port ||
		strings.Contains(host, "%") {
		return nil, errHTTPSResolution
	}
	addresses := make([]netip.Addr, 0, 4)
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = append(addresses, literal.Unmap())
	} else {
		resolved, lookupErr := dialer.lookupIPAddr(ctx, host)
		if lookupErr != nil || len(resolved) == 0 ||
			len(resolved) > maximumDNSAnswers {
			return nil, errHTTPSResolution
		}
		for _, candidate := range resolved {
			if candidate.Zone != "" {
				return nil, errHTTPSResolution
			}
			value, ok := netip.AddrFromSlice(candidate.IP)
			if !ok {
				return nil, errHTTPSResolution
			}
			addresses = append(addresses, value.Unmap())
		}
	}
	for _, candidate := range addresses {
		if !isPublicHTTPSAddress(candidate) {
			return nil, errHTTPSNotPublic
		}
	}
	sort.Slice(addresses, func(left, right int) bool {
		return addresses[left].Compare(addresses[right]) < 0
	})
	selected := netip.Addr{}
	for _, candidate := range addresses {
		if network == "tcp4" && !candidate.Is4() ||
			network == "tcp6" && candidate.Is4() {
			continue
		}
		selected = candidate
		break
	}
	if !selected.IsValid() {
		return nil, errHTTPSResolution
	}
	connection, err := dialer.dialContext(
		ctx,
		network,
		net.JoinHostPort(selected.String(), port),
	)
	if err != nil {
		return nil, errHTTPSDial
	}
	return connection, nil
}

var forbiddenHTTPSAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

var publicHTTPSIPv6Prefix = netip.MustParsePrefix("2000::/3")

func isPublicHTTPSAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() ||
		address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	if address.Is6() && !publicHTTPSIPv6Prefix.Contains(address) {
		return false
	}
	for _, prefix := range forbiddenHTTPSAddressPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
