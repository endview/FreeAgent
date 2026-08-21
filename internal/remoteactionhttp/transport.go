package remoteactionhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	productionDialTimeout         = 10 * time.Second
	productionTLSHandshakeTimeout = 10 * time.Second
	productionResponseHeaderLimit = 16 << 10
)

var (
	ErrEndpointNotPublic = errors.New(
		"remoteactionhttp: endpoint is not a public network address",
	)
	ErrEndpointResolution = errors.New(
		"remoteactionhttp: endpoint cannot be resolved before dispatch",
	)
)

type publicNetworkDialer struct {
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	dialContext  func(context.Context, string, string) (net.Conn, error)
}

func newProductionHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   productionDialTimeout,
		KeepAlive: -1,
	}
	policyDialer := &publicNetworkDialer{
		lookupIPAddr: net.DefaultResolver.LookupIPAddr,
		dialContext:  dialer.DialContext,
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            policyDialer.DialContext,
		ForceAttemptHTTP2:      false,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxIdleConns:           0,
		MaxIdleConnsPerHost:    0,
		IdleConnTimeout:        0,
		TLSHandshakeTimeout:    productionTLSHandshakeTimeout,
		ResponseHeaderTimeout:  0,
		ExpectContinueTimeout:  0,
		MaxResponseHeaderBytes: productionResponseHeaderLimit,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		// A non-nil empty TLSNextProto map disables implicit HTTP/2. Together
		// with a non-replayable POST body this keeps the R1 transport surface
		// to one HTTP/1.1 application request.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	return newHTTPClient(transport)
}

func newHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (dialer *publicNetworkDialer) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	if dialer == nil || ctx == nil || dialer.lookupIPAddr == nil ||
		dialer.dialContext == nil {
		return nil, ErrEndpointResolution
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, ErrEndpointResolution
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" || strings.Contains(host, "%") {
		return nil, ErrEndpointResolution
	}
	addresses := make([]netip.Addr, 0, 4)
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = append(addresses, literal.Unmap())
	} else {
		resolved, lookupErr := dialer.lookupIPAddr(ctx, host)
		if lookupErr != nil || len(resolved) == 0 {
			return nil, ErrEndpointResolution
		}
		for _, candidate := range resolved {
			value, ok := netip.AddrFromSlice(candidate.IP)
			if !ok {
				return nil, ErrEndpointResolution
			}
			addresses = append(addresses, value.Unmap())
		}
	}
	for _, candidate := range addresses {
		if !isPublicAddress(candidate) {
			return nil, ErrEndpointNotPublic
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
		return nil, ErrEndpointResolution
	}
	return dialer.dialContext(
		ctx,
		network,
		net.JoinHostPort(selected.String(), port),
	)
}

func rejectNonPublicLiteralEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ErrEndpointNotPublic
	}
	host := parsed.Hostname()
	address, err := netip.ParseAddr(host)
	if err != nil {
		// DNS names are checked by the production DialContext immediately
		// before a connection is opened; this function performs no I/O.
		return nil
	}
	if !isPublicAddress(address.Unmap()) {
		return ErrEndpointNotPublic
	}
	return nil
}

var remoteActionForbiddenAddressPrefixes = []netip.Prefix{
	// IPv4 special-purpose ranges which netip otherwise classifies as
	// global-unicast. Private, loopback, link-local, multicast and
	// unspecified addresses are rejected by the attribute checks below.
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),

	// IPv6 special-purpose ranges are denied explicitly instead of relying
	// on IsGlobalUnicast. Several translation, tunnel and protocol-assignment
	// prefixes are globally reachable according to the IANA registry but are
	// not ordinary public endpoint addresses. In particular, denying both
	// translation prefixes prevents an embedded private IPv4 destination from
	// bypassing the IPv4 policy.
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	// 2001::/23 is the complete IETF Protocol Assignments block, including
	// Teredo, benchmarking, ORCHID/ORCHIDv2 and other protocol anycast ranges.
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	// Site-local addressing was deprecated and removed from the current IANA
	// table, but remains non-public and must not become reachable through an
	// implementation which still routes the historical prefix.
	netip.MustParsePrefix("fec0::/10"),
}

var remoteActionPublicIPv6Prefix = netip.MustParsePrefix("2000::/3")

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() ||
		address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	// IsGlobalUnicast reports most IPv6 addresses outside the IANA global
	// unicast allocation as global too. Accept only 2000::/3 before applying
	// the narrower special-purpose deny list below. This also rejects legacy
	// and translation forms which can embed an otherwise forbidden IPv4
	// destination without being recognized by Addr.Unmap.
	if address.Is6() && !remoteActionPublicIPv6Prefix.Contains(address) {
		return false
	}
	for _, prefix := range remoteActionForbiddenAddressPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func sanitizeTransportError(err error) error {
	if errors.Is(err, ErrEndpointNotPublic) {
		return ErrEndpointNotPublic
	}
	if errors.Is(err, ErrEndpointResolution) {
		return ErrEndpointResolution
	}
	return fmt.Errorf("remoteactionhttp: ambiguous transport failure")
}
