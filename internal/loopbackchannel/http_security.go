package loopbackchannel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRequestTimeout = 5 * time.Second
	maximumRequestTimeout = 30 * time.Second
	maximumInboundBytes   = 64 << 10
	maximumResponseBytes  = 64 << 10
)

var (
	errInvalidLoopbackEndpoint = errors.New("loopbackchannel: invalid loopback endpoint")
	errResponseTooLarge        = errors.New("loopbackchannel: response exceeds bound")
)

// parseLoopbackEndpoint deliberately accepts only literal IPv4 or IPv6
// loopback addresses. Hostnames (including localhost), userinfo, query
// parameters and encoded paths are rejected so DNS, proxy and URL
// normalization cannot expand the configured network authority.
func parseLoopbackEndpoint(raw string) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return nil, fmt.Errorf("%w: endpoint is empty or contains surrounding whitespace", errInvalidLoopbackEndpoint)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed URL", errInvalidLoopbackEndpoint)
	}
	if parsed.Scheme != "http" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.Host == "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.RawFragment != "" {
		return nil, fmt.Errorf("%w: only plain HTTP authority and path are allowed", errInvalidLoopbackEndpoint)
	}
	if parsed.RawPath != "" || parsed.Path == "" || parsed.Path[0] != '/' ||
		path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.Path, "//") {
		return nil, fmt.Errorf("%w: path must be absolute and canonical", errInvalidLoopbackEndpoint)
	}
	host := parsed.Hostname()
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: IPv6 zones are forbidden", errInvalidLoopbackEndpoint)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%w: host must be a literal loopback address", errInvalidLoopbackEndpoint)
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%w: invalid port", errInvalidLoopbackEndpoint)
		}
	}
	return parsed, nil
}

func newLoopbackHTTPClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	if timeout > maximumRequestTimeout {
		return nil, fmt.Errorf("loopbackchannel: request timeout exceeds %s", maximumRequestTimeout)
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         loopbackDialContext(dialer),
		DisableKeepAlives:   true,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        0,
		IdleConnTimeout:     0,
		TLSHandshakeTimeout: timeout,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func loopbackDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if ctx == nil {
			return nil, errors.New("loopbackchannel: dial context is nil")
		}
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("loopbackchannel: unsupported network %q", network)
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("loopbackchannel: malformed dial address: %w", err)
		}
		if strings.Contains(host, "%") {
			return nil, errors.New("loopbackchannel: IPv6 zones are forbidden")
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("loopbackchannel: dial target is not a literal loopback address")
		}
		return dialer.DialContext(ctx, network, address)
	}
}

func readBoundedAndClose(body io.ReadCloser, maximum int64) ([]byte, error) {
	if body == nil {
		return nil, errors.New("loopbackchannel: response body is nil")
	}
	defer body.Close()
	if maximum <= 0 {
		return nil, errors.New("loopbackchannel: invalid response bound")
	}
	value, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, errResponseTooLarge
	}
	return value, nil
}
