package localchat

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const authorityOriginScheme = "http://"

// AuthorityGuard binds the local HTTP surface to the exact authority returned
// by the bound listener. Native clients may omit Origin; browser clients must
// send the single exact loopback origin.
type AuthorityGuard struct {
	next      http.Handler
	authority string
	origin    string
}

func NewAuthorityGuard(next http.Handler, authority string) (*AuthorityGuard, error) {
	if next == nil {
		return nil, errors.New("localchat: guarded handler is required")
	}
	if err := validateBoundAuthority(authority); err != nil {
		return nil, err
	}
	return &AuthorityGuard{
		next:      next,
		authority: authority,
		origin:    authorityOriginScheme + authority,
	}, nil
}

func (guard *AuthorityGuard) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if guard == nil || request == nil || request.Host != guard.authority {
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "local HTTP authority is invalid",
		})
		return
	}
	origins := request.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != guard.origin) {
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "local HTTP origin is invalid",
		})
		return
	}
	guard.next.ServeHTTP(w, request)
}

func CanonicalAuthority(address net.Addr) (string, error) {
	tcpAddress, ok := address.(*net.TCPAddr)
	if !ok || tcpAddress == nil || tcpAddress.IP == nil ||
		tcpAddress.Zone != "" || !tcpAddress.IP.IsLoopback() ||
		tcpAddress.Port < 1 || tcpAddress.Port > 65535 {
		return "", errors.New("localchat: listener did not bind literal loopback IP")
	}
	authority := net.JoinHostPort(
		tcpAddress.IP.String(),
		strconv.Itoa(tcpAddress.Port),
	)
	if err := validateBoundAuthority(authority); err != nil {
		return "", err
	}
	return authority, nil
}

func validateBoundAuthority(authority string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(authority))
	if err != nil || authority != strings.TrimSpace(authority) {
		return fmt.Errorf("localchat: bound authority must be host:port")
	}
	host = strings.Trim(host, "[]")
	if strings.Contains(host, "%") {
		return errors.New("localchat: IPv6 zones are forbidden")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("localchat: bound authority must use literal loopback IP")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 ||
		strconv.Itoa(portNumber) != port {
		return errors.New("localchat: bound authority has an invalid canonical port")
	}
	return nil
}
