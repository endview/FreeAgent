package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type HTTPTools struct {
	Config HTTPConfig
}

var (
	ErrHTTPOriginsRequired = errors.New("harness: enabled HTTP requires at least one allowed origin")
	ErrInvalidHTTPOrigin   = errors.New("harness: HTTP origin is invalid")
)

func (config HTTPConfig) Validate() error {
	if config.Enabled && len(config.AllowedOrigins) == 0 {
		return ErrHTTPOriginsRequired
	}
	if config.Enabled && (config.TimeoutSecs < 1 || config.TimeoutSecs > 60 ||
		config.MaxOutput < 1024 || config.MaxOutput > 64<<10) {
		return errors.New("harness: enabled HTTP requires allowed origins, timeout 1-60s and output 1024-65536 bytes")
	}
	if len(config.AllowedOrigins) > 16 {
		return errors.New("harness: at most 16 HTTP origins")
	}
	seen := map[string]bool{}
	for _, origin := range config.AllowedOrigins {
		target, err := parseHTTPURL(origin)
		if err != nil || target.Path != "" || target.RawQuery != "" || target.ForceQuery ||
			origin != httpOrigin(target) || seen[origin] {
			return fmt.Errorf("%w: origins must be unique canonical scheme://host[:port], without path, query or credentials", ErrInvalidHTTPOrigin)
		}
		seen[origin] = true
	}
	return nil
}

func (tools *HTTPTools) Definitions() []ToolDefinition {
	if !tools.Config.Enabled {
		return nil
	}
	origins, _ := json.Marshal(tools.Config.AllowedOrigins)
	return []ToolDefinition{{Name: "http_get", Effect: EffectModify,
		Description: "Fetch bounded UTF-8 text with an explicitly approved GET. No credentials, redirects, proxy or automatic retry. GET may have external effects. Allowed origins: " + string(origins),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","maxLength":4096}},"required":["url"],"additionalProperties":false}`)}}
}

func parseHTTPURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n\t #\\") {
		return nil, errors.New("HTTP URL is invalid or exceeds the limit")
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || target.User != nil || target.Opaque != "" ||
		(target.Scheme != "https" && target.Scheme != "http") {
		return nil, errors.New("HTTP URL must be absolute HTTP(S), without credentials or fragments")
	}
	host := target.Hostname()
	if host == "" || strings.Contains(host, "%") {
		return nil, errors.New("HTTP host is invalid")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !addr.IsLoopback() && !publicHTTPAddress(addr) {
			return nil, errors.New("HTTP target address is not public or explicitly selected loopback")
		}
	} else {
		if len(host) > 253 || strings.HasSuffix(host, ".") {
			return nil, errors.New("HTTP host requires a bounded canonical ASCII name")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, errors.New("HTTP host is invalid")
			}
			for _, char := range label {
				if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-') {
					return nil, errors.New("HTTP host requires an ASCII name")
				}
			}
		}
	}
	if port := target.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
			return nil, errors.New("HTTP port is invalid")
		}
	} else if strings.HasSuffix(target.Host, ":") {
		return nil, errors.New("HTTP port is empty")
	}
	if target.Scheme == "http" && !loopbackHTTPHost(host) {
		return nil, errors.New("plain HTTP is restricted to explicitly selected loopback origins")
	}
	return target, nil
}

func httpOrigin(target *url.URL) string {
	return target.Scheme + "://" + strings.ToLower(target.Host)
}

func loopbackHTTPHost(host string) bool {
	addr, err := netip.ParseAddr(host)
	return strings.EqualFold(host, "localhost") || err == nil && addr.Unmap().IsLoopback()
}

// Fail closed for private, special-purpose and non-global destinations. This
// deliberately excludes some otherwise routable special-purpose ranges.
var deniedHTTPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicHTTPAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		(addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr)) {
		return false
	}
	for _, prefix := range deniedHTTPPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func validateHTTPAddresses(host string, addresses []netip.Addr) error {
	if len(addresses) == 0 {
		return errors.New("HTTP host has no addresses")
	}
	local := loopbackHTTPHost(host)
	for _, addr := range addresses {
		if local && !addr.Unmap().IsLoopback() || !local && !publicHTTPAddress(addr) {
			return errors.New("HTTP host resolved outside its permitted address scope")
		}
	}
	return nil
}

func (tools *HTTPTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if !tools.Config.Enabled || call.Name != "http_get" {
		return PreparedTool{}, errors.New("HTTP tools are disabled")
	}
	if err := ctx.Err(); err != nil {
		return PreparedTool{}, err
	}
	if err := tools.Config.Validate(); err != nil {
		return PreparedTool{}, err
	}
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	target, err := parseHTTPURL(args.URL)
	if err != nil {
		return PreparedTool{}, err
	}
	allowed := false
	for _, origin := range tools.Config.AllowedOrigins {
		allowed = allowed || origin == httpOrigin(target)
	}
	if !allowed {
		return PreparedTool{}, errors.New("HTTP origin is not explicitly allowed")
	}
	config := tools.Config
	receipt := ToolReceipt{Kind: "http", URL: target.String(), Status: "not_dispatched"}
	execute := func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
		return executeHTTP(ctx, target, config, receipt, record)
	}
	return PreparedTool{Effect: EffectModify,
		Items: []ApprovalItem{{Kind: "http", Target: target.String(),
			Fields: map[string]string{
				"url":          target.String(),
				"timeout_s":    strconv.Itoa(config.TimeoutSecs),
				"output_bytes": strconv.Itoa(config.MaxOutput),
			}}},
		Preview: fmt.Sprintf("GET %s\nTimeout: %ds\nOutput: at most %d bytes of UTF-8 text\nNo credentials, proxy, redirects or automatic retries. GET may have external effects. Localhost is NOT a sandbox.",
			target.String(), config.TimeoutSecs, config.MaxOutput),
		ExecuteRecorded: execute,
		Execute: func(ctx context.Context) (ToolResult, error) {
			return execute(ctx, func(ToolResult) error { return nil })
		},
		Reject: func() ToolResult {
			return ToolResult{IsError: true, Content: "user declined the HTTP request; do not repeat it", Receipts: []ToolReceipt{receipt}}
		},
	}, nil
}

func executeHTTP(parent context.Context, target *url.URL, config HTTPConfig, receipt ToolReceipt, record func(ToolResult) error) (ToolResult, error) {
	result := ToolResult{IsError: true, Receipts: []ToolReceipt{receipt}}
	ctx, cancel := context.WithTimeout(parent, time.Duration(config.TimeoutSecs)*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		result.Content = "HTTP request not dispatched: cancelled"
		return result, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", target.Hostname())
	if err != nil || validateHTTPAddresses(target.Hostname(), addresses) != nil {
		result.Content = "HTTP request not dispatched: address resolution failed or destination denied"
		return result, nil
	}
	port := target.Port()
	if port == "" {
		port = "443"
		if target.Scheme == "http" {
			port = "80"
		}
	}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		MaxResponseHeaderBytes: 16384, TLSHandshakeTimeout: time.Duration(config.TimeoutSecs) * time.Second,
		// DNS is checked once then pinned for this single request, not resolved
		// again by the dialer. No fallback request, proxy or reused connection.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(addresses[0].String(), port))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(config.TimeoutSecs) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		result.Content = "HTTP request not dispatched: invalid request"
		return result, nil
	}
	request.Header.Set("Accept", "text/*, application/json, application/xml")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "FreeAgent-Harness/4.0")
	if ctx.Err() != nil {
		result.Content = "HTTP request not dispatched: cancelled before send"
		return result, nil
	}
	result.Receipts[0].Status = "unknown"
	result.Content = "HTTP request intent recorded; response not yet known"
	if err := record(result); err != nil {
		result.Receipts[0].Status = "not_dispatched"
		return result, &CallError{Certain: true, Message: "HTTP request not dispatched: receipt could not be persisted"}
	}
	response, err := client.Do(request)
	if err != nil {
		result.Content = "HTTP request interrupted; outcome UNKNOWN; inspect before repeating"
		result.Receipts[0].Detail = "request may have reached the endpoint; no automatic replay"
		_ = record(result)
		return result, &CallError{Message: result.Content}
	}
	defer response.Body.Close()
	result.Receipts[0].Status = "response_received"
	result.Receipts[0].HTTPStatus = response.StatusCode
	result.Receipts[0].Detail = "headers received; response body not yet complete"
	result.Content = "HTTP response headers received; body not yet complete"
	if err := record(result); err != nil {
		return result, &CallError{Message: "HTTP response received but receipt could not be persisted"}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(config.MaxOutput)+1))
	result.Receipts[0].Status = "completed"
	result.Receipts[0].Detail = ""
	if readErr != nil {
		result.Receipts[0].Truncated = true
		result.Receipts[0].Detail = "response body incomplete; no automatic retry"
	} else if len(body) > config.MaxOutput {
		result.Receipts[0].Truncated = true
		result.Receipts[0].Detail = "response body exceeded output limit"
	}
	body = body[:min(len(body), config.MaxOutput)]
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		body = nil
		result.Receipts[0].Detail = "redirect not followed"
	} else if !httpTextResponse(response.Header.Get("Content-Type")) ||
		(response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
		body = nil
		result.Receipts[0].Detail = "non-text, non-UTF-8 charset or encoded response omitted"
	} else if bytes.ContainsRune(body, 0) || !utf8.Valid(body) {
		// Only a cut at the byte limit may omit a trailing partial character.
		if result.Receipts[0].Truncated && !bytes.ContainsRune(body, 0) {
			for cut := 0; cut < 3 && len(body) > 0 && !utf8.Valid(body); cut++ {
				body = body[:len(body)-1]
			}
		}
		if !utf8.Valid(body) || bytes.ContainsRune(body, 0) {
			body = nil
			result.Receipts[0].Detail = "non-UTF-8 or NUL-containing response omitted"
		}
	}
	result.IsError = response.StatusCode < 200 || response.StatusCode >= 300 ||
		result.Receipts[0].Truncated || result.Receipts[0].Detail != ""
	raw, _ := json.Marshal(map[string]any{"request": result.Receipts[0], "content": string(body)})
	result.Content = string(raw)
	return result, nil
}

func httpTextResponse(contentType string) bool {
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	if charset := strings.ToLower(parameters["charset"]); charset != "" && charset != "utf-8" && charset != "us-ascii" {
		return false
	}
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		mediaType == "application/xml" || strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}
