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
	"github.com/endview/freeagent/internal/publicweb"
)

// localOwner serves the same authenticated surface for both web and terminal
// owners. The caller retains the runtime lock until Close has drained requests.
type localOwner struct {
	channels      *channelProcess
	publicSocket  *publicweb.SocketServer
	publicHandler *chatui.PublicServer
	address       string
	client        *handoff.Client
	handler       *chatui.Server
	server        *http.Server
	done          chan struct{}
	serveErr      error
	once          sync.Once
	result        error
	publish       handoff.Published
}

func startLocalOwner(ctx context.Context, tasks *harness.TaskService, data string, port int, oneShot bool) (*localOwner, error) {
	return startLocalOwnerMode(ctx, tasks, data, port, oneShot, "interactive")
}

func startLocalOwnerMode(ctx context.Context, tasks *harness.TaskService, data string, port int, oneShot bool, mode string) (*localOwner, error) {
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
		Generation: attachSecret[:16], PID: os.Getpid(), OwnerKind: mode}
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
	var publicHandler *chatui.PublicServer
	var publicSocket *publicweb.SocketServer
	if tasks.Config().PublicWeb.Effective().Enabled {
		publicHandler, err = chatui.NewPublicServer(handler)
		if err == nil {
			publicSocket, err = publicweb.StartSocket(tasks.Config().PublicWeb.Effective(), publicHandler, publicHandler.Done())
			if err == nil {
				publicHandler.SetSocket(publicSocket)
			}
		}
		if err != nil {
			if publicHandler != nil {
				publicHandler.Close()
			}
			handler.Close()
			published.Close()
			return nil, err
		}
	}
	defer func() {
		if !ready {
			if publicSocket != nil {
				_ = publicSocket.Close()
			}
			if publicHandler != nil {
				publicHandler.Close()
			}
		}
	}()
	if err := handler.ConfigureChannelBridge(); err != nil {
		handler.Close()
		published.Close()
		return nil, err
	}
	handler.StartInputQueues()
	client, err := handoff.NewInteractiveClient(published.Info, chatSecret)
	if err != nil {
		handler.Close()
		published.Close()
		return nil, err
	}
	owner := &localOwner{address: origin + "/#key=" + chatSecret, client: client, handler: handler,
		publicHandler: publicHandler, publicSocket: publicSocket,
		server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second},
		done:   make(chan struct{}), publish: published}
	go func() {
		owner.serveErr = owner.server.Serve(listener)
		close(owner.done)
	}()
	if mode != "service" {
		owner.channels = startChannelProcess(ctx, data)
	}
	ready = true
	return owner, nil
}

func (owner *localOwner) Close() error {
	owner.once.Do(func() {
		if owner.publicSocket != nil {
			owner.result = errors.Join(owner.result, owner.publicSocket.Close())
		}
		if owner.publicHandler != nil {
			owner.publicHandler.Close()
		}
		owner.handler.Close()
		owner.channels.Close()
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
		owner.result = errors.Join(owner.result, err, serveErr, owner.publish.Close())
	})
	return owner.result
}
