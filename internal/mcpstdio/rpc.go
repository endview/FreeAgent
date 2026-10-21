package mcpstdio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrRPCClosed = errors.New("mcp stdio: rpc client closed")

const (
	// maxConcurrentHostRequests bounds the extension-initiated requests that
	// are handled at the same time. Further requests are answered at once
	// with an error instead of starting another handler goroutine.
	maxConcurrentHostRequests = 16
	// A rejection is written from the read loop, so the loop follows the
	// extension's pace. An extension that stops reading is disconnected.
	rejectWriteTimeout = 5 * time.Second
	codeHostBusy       = -32000
)

// RPCRequestHandler handles requests initiated by a local extension. Returning
// an error sends a JSON-RPC error response; notifications never receive one.
type RPCRequestHandler func(context.Context, *jsonrpc.Request) (any, error)

// RPCClient is a small bidirectional JSON-RPC client for the extension
// protocol. It deliberately owns no protocol-specific methods or persistence.
type RPCClient struct {
	connection mcp.Connection
	handler    RPCRequestHandler
	next       atomic.Uint64
	slots      chan struct{}

	mu       sync.Mutex
	pending  map[string]chan rpcResult
	done     chan struct{}
	once     sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
	closeErr error
}

type rpcResult struct {
	response *jsonrpc.Response
	err      error
}

func NewBidirectionalClient(ctx context.Context, command string, args []string, dir string, env []string, handler RPCRequestHandler) (*RPCClient, error) {
	transport, err := NewBidirectionalTransport(command, args, dir, env)
	if err != nil {
		return nil, err
	}
	return newRPCClient(ctx, transport, handler)
}

func NewBidirectionalClientWithLog(ctx context.Context, command string, args []string, dir string, env []string, logPath string, redact func(string) string, handler RPCRequestHandler) (*RPCClient, error) {
	transport, err := NewBidirectionalTransportWithLog(command, args, dir, env, logPath, redact)
	if err != nil {
		return nil, err
	}
	return newRPCClient(ctx, transport, handler)
}

func newRPCClient(ctx context.Context, transport *commandTransport, handler RPCRequestHandler) (*RPCClient, error) {
	if ctx == nil {
		_ = transport.Close()
		return nil, errors.New("mcp stdio: nil rpc context")
	}
	connection, err := transport.Connect(ctx)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	return newRPCClientWithConnection(connection, handler), nil
}

func newRPCClientWithConnection(connection mcp.Connection, handler RPCRequestHandler) *RPCClient {
	lifetime, cancel := context.WithCancel(context.Background())
	client := &RPCClient{connection: connection, handler: handler,
		slots:   make(chan struct{}, maxConcurrentHostRequests),
		pending: map[string]chan rpcResult{}, done: make(chan struct{}), ctx: lifetime, cancel: cancel}
	go client.readLoop()
	return client
}

func (client *RPCClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if client == nil || method == "" {
		return nil, errors.New("mcp stdio: invalid rpc call")
	}
	if ctx == nil {
		return nil, errors.New("mcp stdio: nil rpc call context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idNumber := client.next.Add(1)
	id, err := jsonrpc.MakeID(fmt.Sprintf("freeagent-%d", idNumber))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	wait := make(chan rpcResult, 1)
	key := rpcIDKey(id)
	client.mu.Lock()
	select {
	case <-client.done:
		client.mu.Unlock()
		return nil, ErrRPCClosed
	default:
	}
	client.pending[key] = wait
	client.mu.Unlock()
	request := &jsonrpc.Request{ID: id, Method: method, Params: raw}
	if err := client.connection.Write(ctx, request); err != nil {
		client.removePending(key)
		return nil, err
	}
	select {
	case result := <-wait:
		if result.err != nil {
			return nil, result.err
		}
		if result.response == nil {
			return nil, ErrRPCClosed
		}
		if result.response.Error != nil {
			return nil, result.response.Error
		}
		return result.response.Result, nil
	case <-ctx.Done():
		client.removePending(key)
		return nil, ctx.Err()
	case <-client.done:
		client.removePending(key)
		return nil, ErrRPCClosed
	}
}

func (client *RPCClient) Notify(ctx context.Context, method string, params any) error {
	if client == nil || method == "" {
		return errors.New("mcp stdio: invalid rpc notification")
	}
	if ctx == nil {
		return errors.New("mcp stdio: nil rpc notification context")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	select {
	case <-client.done:
		return ErrRPCClosed
	default:
	}
	return client.connection.Write(ctx, &jsonrpc.Request{Method: method, Params: raw})
}

func (client *RPCClient) Close() error {
	if client == nil {
		return nil
	}
	client.fail(ErrRPCClosed)
	return client.closeErr
}

func (client *RPCClient) Closed() bool {
	select {
	case <-client.done:
		return true
	default:
		return false
	}
}

func (client *RPCClient) readLoop() {
	for {
		message, err := client.connection.Read(context.Background())
		if err != nil {
			client.fail(err)
			return
		}
		switch value := message.(type) {
		case *jsonrpc.Response:
			key := rpcIDKey(value.ID)
			client.mu.Lock()
			wait := client.pending[key]
			if wait != nil {
				delete(client.pending, key)
			}
			client.mu.Unlock()
			if wait != nil {
				wait <- rpcResult{response: value}
			}
		case *jsonrpc.Request:
			select {
			case client.slots <- struct{}{}:
				go func() {
					defer func() { <-client.slots }()
					client.handleRequest(value)
				}()
			default:
				client.rejectBusy(value)
			}
		default:
			client.fail(errors.New("mcp stdio: unsupported rpc message"))
			return
		}
	}
}

func (client *RPCClient) handleRequest(request *jsonrpc.Request) {
	if request == nil {
		return
	}
	var result any
	var err error
	if client.handler == nil {
		err = &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"}
	} else {
		result, err = client.handler(client.ctx, request)
	}
	if !request.ID.IsValid() {
		return
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil && err == nil {
		err = marshalErr
	}
	if err != nil {
		raw = nil
	}
	response := &jsonrpc.Response{ID: request.ID, Result: raw, Error: err}
	_ = client.connection.Write(client.ctx, response)
}

// rejectBusy answers a request that found every handler slot in use. A
// notification has no reply and is dropped.
func (client *RPCClient) rejectBusy(request *jsonrpc.Request) {
	if request == nil || !request.ID.IsValid() {
		return
	}
	ctx, cancel := context.WithTimeout(client.ctx, rejectWriteTimeout)
	defer cancel()
	_ = client.connection.Write(ctx, &jsonrpc.Response{ID: request.ID,
		Error: &jsonrpc.Error{Code: codeHostBusy, Message: "too many concurrent host requests"}})
}

func (client *RPCClient) fail(err error) {
	client.once.Do(func() {
		close(client.done)
		client.cancel()
		client.mu.Lock()
		for key, wait := range client.pending {
			delete(client.pending, key)
			wait <- rpcResult{err: err}
		}
		client.mu.Unlock()
		client.closeErr = client.connection.Close()
	})
}

func (client *RPCClient) removePending(key string) {
	client.mu.Lock()
	delete(client.pending, key)
	client.mu.Unlock()
}

func rpcIDKey(id jsonrpc.ID) string {
	return fmt.Sprintf("%T:%v", id.Raw(), id.Raw())
}
