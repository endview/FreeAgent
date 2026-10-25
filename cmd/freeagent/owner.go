package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/chatui"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

// localOwner serves the same authenticated surface for both web and terminal
// owners. The caller retains the runtime lock until Close has drained requests.
type localOwner struct {
	address  string
	client   *handoff.Client
	handler  *chatui.Server
	server   *http.Server
	done     chan struct{}
	serveErr error
	once     sync.Once
	result   error
	publish  handoff.Published
}

func startLocalOwner(ctx context.Context, tasks *harness.TaskService, data string, port int, oneShot bool) (*localOwner, error) {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			_ = listener.Close()
		}
	}()
	attachSecret, err := handoff.NewSecret()
	if err != nil {
		return nil, err
	}
	chatSecret, err := handoff.NewSecret()
	if err != nil {
		return nil, err
	}
	origin := "http://" + listener.Addr().String()
	info := handoff.EngineInfo{Product: "FreeAgent", Origin: origin, Workspace: tasks.Config().Workspace,
		Generation: attachSecret[:16], PID: os.Getpid(), OwnerKind: "interactive"}
	if oneShot {
		info.OwnerKind = "one_shot"
		info.ExpiresAt = time.Now().UTC().Add(oneShotLease).Format(time.RFC3339Nano)
	}
	published, err := handoff.Publish(data, info, attachSecret, chatSecret)
	if err != nil {
		return nil, err
	}
	handler, err := chatui.NewServerWithAttach(ctx, tasks, origin, chatSecret, attachSecret)
	if err != nil {
		published.Close()
		return nil, err
	}
	handler.SetGeneration(published.Info.Generation)
	handler.SetOwnerInfo(published.Info)
	client, err := handoff.NewInteractiveClient(published.Info, chatSecret)
	if err != nil {
		handler.Close()
		published.Close()
		return nil, err
	}
	owner := &localOwner{address: origin + "/#key=" + chatSecret, client: client, handler: handler,
		server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second},
		done:   make(chan struct{}), publish: published}
	go func() {
		owner.serveErr = owner.server.Serve(listener)
		close(owner.done)
	}()
	ready = true
	return owner, nil
}

func (owner *localOwner) Close() error {
	owner.once.Do(func() {
		owner.handler.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := owner.server.Shutdown(ctx)
		if err != nil {
			_ = owner.server.Close()
		}
		<-owner.done
		serveErr := owner.serveErr
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		owner.publish.Close()
		owner.result = errors.Join(err, serveErr)
	})
	return owner.result
}
