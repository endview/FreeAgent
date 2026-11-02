//go:build !linux

package publicweb

import (
	"errors"
	"github.com/endview/freeagent/internal/harness"
	"net"
)

func listenRestricted(harness.PublicWebConfig) (net.Listener, error) {
	return nil, errors.New("public Web: the restricted proxy entrance is supported only on Linux")
}
