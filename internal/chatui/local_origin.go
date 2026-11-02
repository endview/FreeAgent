package chatui

import (
	"net"
	"strconv"
)

// SSH forwarding may change the local port, but may not supply a DNS name
// or change the browser's same-origin boundary.
func validLocalHost(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || (h != "127.0.0.1" && h != "::1") {
		return false
	}
	n, err := strconv.Atoi(p)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == p
}
