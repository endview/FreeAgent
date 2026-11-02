package publicweb

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

type proxyContextKey struct{}

func TrustedProxy(ctx context.Context) bool { v, _ := ctx.Value(proxyContextKey{}).(bool); return v }
func ProxyConnectionContext(ctx context.Context, conn net.Conn) context.Context {
	if conn.LocalAddr().Network() == "unix" {
		return context.WithValue(ctx, proxyContextKey{}, true)
	}
	return ctx
}

type SocketServer struct {
	server *http.Server
	done   chan struct{}
	once   sync.Once
	err    error
}

func (s *SocketServer) Done() <-chan struct{} { return s.done }
func (s *SocketServer) Active() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func StartSocket(config harness.PublicWebConfig, handler http.Handler, revoked <-chan struct{}) (*SocketServer, error) {
	listener, err := listenRestricted(config)
	if err != nil {
		return nil, err
	}
	s := &SocketServer{server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ConnContext: ProxyConnectionContext}, done: make(chan struct{})}
	go func() { s.err = s.server.Serve(listener); close(s.done) }()
	go func() {
		select {
		case <-revoked:
			_ = s.server.Close()
		case <-s.done:
		}
	}()
	return s, nil
}
func (s *SocketServer) Close() error {
	s.once.Do(func() {
		_ = s.server.Close()
		<-s.done
		if errors.Is(s.err, http.ErrServerClosed) {
			s.err = nil
		}
	})
	return s.err
}
