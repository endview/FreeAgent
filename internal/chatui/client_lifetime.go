package chatui

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (server *Server) enterClientRequest(r *http.Request) (func(), bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.stopping && r.Method != http.MethodGet {
		return nil, false
	}
	server.clientRequests++
	id := r.Header.Get("X-FreeAgent-Client")
	if len(id) > 0 && len(id) <= 128 && !strings.ContainsAny(id, "\r\n\x00") {
		if server.clients == nil {
			server.clients = map[string]time.Time{}
		}
		now := time.Now()
		for key, last := range server.clients {
			if now.Sub(last) > 45*time.Second {
				delete(server.clients, key)
			}
		}
		if len(server.clients) < 1024 {
			server.clients[id] = now
		}
	}
	return func() { server.mu.Lock(); server.clientRequests--; server.mu.Unlock() }, true
}

// ArmIdleShutdown follows a detached desktop. Server deployments never call
// it. The final decision shares the admission mutex with GUI/CLI/channel work.
func (server *Server) ArmIdleShutdown() {
	server.idleOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-server.ctx.Done():
					return
				case <-ticker.C:
					if server.tryIdleShutdown(time.Now()) {
						return
					}
				}
			}
		}()
	})
}
func (server *Server) tryIdleShutdown(now time.Time) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.stopping || server.clientRequests > 0 || len(server.listeners) > 0 || len(server.running) > 0 {
		return false
	}
	for id, last := range server.clients {
		if now.Sub(last) > 45*time.Second {
			delete(server.clients, id)
		} else {
			return false
		}
	}
	if server.tasks.HasCommands() {
		return false
	}
	ctx, cancel := context.WithTimeout(server.ctx, 2*time.Second)
	defer cancel()
	work, err := server.tasks.KeepOwnerAlive(ctx)
	if err != nil || work {
		return false
	}
	server.stopping = true
	server.requestStopLocked()
	return true
}

func (server *Server) ForgetClient(id string) {
	server.mu.Lock()
	delete(server.clients, id)
	server.mu.Unlock()
}

// Database ownership loss is an execution failure, never a client detach.
// Stop admission, cancel current operations and ask the owning process to exit.
func (server *Server) watchDatabaseOwner() {
	select {
	case <-server.ctx.Done():
		return
	case <-server.tasks.OwnerDone():
	}
	if server.tasks.OwnerError() == nil {
		return
	}
	server.mu.Lock()
	server.stopping = true
	for _, execution := range server.running {
		execution.cancel()
	}
	server.requestStopLocked()
	server.mu.Unlock()
}
