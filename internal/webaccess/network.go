package webaccess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var ErrDestinationDenied = errors.New("web: private or special destination denied (check DNS/VPN fake-IP mode)")

func PublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr) {
		return false
	}
	for _, prefix := range deniedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// NewHTTPClient pins DNS results at dial time. privateEndpoint is only for the
// exact configured search endpoint; it is never used by page fetches. There is
// no ambient proxy, cookie jar, automatic retry, or credential-bearing redirect.
func NewHTTPClient(timeout time.Duration, privateEndpoint bool) *http.Client {
	t := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		MaxResponseHeaderBytes: 16384, TLSHandshakeTimeout: timeout}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("web: invalid destination")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("web: destination lookup failed")
		}
		for _, ip := range ips {
			if !privateEndpoint && !PublicAddress(ip) {
				return nil, ErrDestinationDenied
			}
		}
		return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: t, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
