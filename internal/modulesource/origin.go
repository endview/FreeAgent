package modulesource

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// parseCanonicalHTTPSOrigin accepts one allowlist origin without a path. The
// Source Policy separately binds the complete index URL.
func parseCanonicalHTTPSOrigin(raw string) (string, error) {
	parsed, err := parseCanonicalHTTPSURL(raw, false)
	if err != nil || parsed.Path != "" {
		return "", errors.New("HTTPS allowlist origin is not canonical")
	}
	return raw, nil
}

// parseCanonicalHTTPSIndexURL accepts the exact complete index URL. V1
// deliberately excludes userinfo, query strings, fragments, escaped paths,
// dot segments, Unicode host names, zones, and an explicit default port.
func parseCanonicalHTTPSIndexURL(raw string) (*url.URL, string, error) {
	parsed, err := parseCanonicalHTTPSURL(raw, true)
	if err != nil || parsed.Path == "" {
		return nil, "", errors.New("HTTPS index URL is not canonical")
	}
	origin := "https://" + parsed.Host
	return parsed, origin, nil
}

func parseCanonicalHTTPSURL(raw string, requirePath bool) (*url.URL, error) {
	if raw == "" || len(raw) > moduleapi.MaxModuleSourceOriginBytesV1 ||
		!utf8.ValidString(raw) || raw != strings.TrimSpace(raw) ||
		raw != moduleapi.CanonicalText(raw) {
		return nil, errors.New("HTTPS URL is not canonical UTF-8")
	}
	for _, character := range raw {
		if character < 0x20 || character == 0x7f {
			return nil, errors.New("HTTPS URL contains a control character")
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme != "https" ||
		parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.RawFragment != "" {
		return nil, errors.New("HTTPS URL is not exact HTTPS")
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.Contains(hostname, "%") ||
		strings.HasSuffix(hostname, ".") {
		return nil, errors.New("HTTPS URL host is invalid")
	}

	canonicalHost, err := canonicalHTTPSHost(hostname, parsed.Port())
	if err != nil || parsed.Host != canonicalHost || parsed.Port() == "443" {
		return nil, errors.New("HTTPS URL host is not canonical")
	}
	requestPath := parsed.EscapedPath()
	if requirePath {
		decodedPath := parsed.Path
		if requestPath == "" || requestPath[0] != '/' ||
			strings.Contains(decodedPath, "\\") || path.Clean(decodedPath) != decodedPath ||
			strings.Contains(decodedPath, "//") ||
			canonicalEscapedPath(decodedPath) != requestPath {
			return nil, errors.New("HTTPS URL path is not canonical")
		}
	} else if parsed.Path != "" || parsed.RawPath != "" {
		return nil, errors.New("HTTPS origin must not have a path")
	}
	if parsed.String() != raw {
		return nil, errors.New("HTTPS URL spelling is not canonical")
	}
	return parsed, nil
}

func canonicalEscapedPath(decoded string) string {
	segments := strings.Split(decoded, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	return strings.Join(segments, "/")
}

func canonicalHTTPSHost(hostname, port string) (string, error) {
	if port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != port {
			return "", errors.New("HTTPS origin port is invalid")
		}
	}
	if address, err := netip.ParseAddr(hostname); err == nil {
		address = address.Unmap()
		if address.String() != hostname {
			return "", errors.New("HTTPS literal address is not canonical")
		}
		if port != "" {
			return net.JoinHostPort(hostname, port), nil
		}
		if address.Is6() {
			return "[" + hostname + "]", nil
		}
		return hostname, nil
	}
	if !validASCIIHostname(hostname) || hostname != strings.ToLower(hostname) {
		return "", errors.New("HTTPS DNS name is not canonical")
	}
	if port != "" {
		return net.JoinHostPort(hostname, port), nil
	}
	return hostname, nil
}

func validASCIIHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' ||
			label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character > 0x7f ||
				!((character >= 'a' && character <= 'z') ||
					(character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}
