package mcpstdio

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// memoryConnection is an in-process mcp.Connection for RPC client tests.
type memoryConnection struct {
	incoming chan jsonrpc.Message
	written  chan jsonrpc.Message
	closed   chan struct{}
	once     sync.Once
}

func newMemoryConnection() *memoryConnection {
	return &memoryConnection{incoming: make(chan jsonrpc.Message, 256),
		written: make(chan jsonrpc.Message, 256), closed: make(chan struct{})}
}

func (connection *memoryConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case message := <-connection.incoming:
		return message, nil
	case <-connection.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (connection *memoryConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	select {
	case connection.written <- message:
		return nil
	case <-connection.closed:
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (connection *memoryConnection) Close() error {
	connection.once.Do(func() { close(connection.closed) })
	return nil
}

func (*memoryConnection) SessionID() string { return "" }

// An extension can send requests faster than the Host answers them. Each one
// used to start its own goroutine with no limit.
func TestRPCClientBoundsConcurrentHostRequests(t *testing.T) {
	const total = 64
	release := make(chan struct{})
	var active, peak atomic.Int64
	handler := func(ctx context.Context, _ *jsonrpc.Request) (any, error) {
		current := active.Add(1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		defer active.Add(-1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return map[string]any{"ok": true}, nil
	}
	connection := newMemoryConnection()
	client := newRPCClientWithConnection(connection, handler)
	defer client.Close()
	for index := range total {
		id, err := jsonrpc.MakeID(fmt.Sprintf("host-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		connection.incoming <- &jsonrpc.Request{ID: id, Method: "model.generate"}
	}
	// Notifications never get a reply, even when they are dropped.
	connection.incoming <- &jsonrpc.Request{Method: "activity.report"}

	rejected := 0
	deadline := time.After(5 * time.Second)
	for active.Load()+int64(rejected) < total {
		select {
		case message := <-connection.written:
			response, ok := message.(*jsonrpc.Response)
			if !ok || response.Error == nil {
				t.Fatalf("unexpected early message %#v", message)
			}
			rejected++
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("active=%d rejected=%d", active.Load(), rejected)
		}
	}
	if got := peak.Load(); got > maxConcurrentHostRequests {
		t.Fatalf("peak concurrent host handlers=%d, limit=%d", got, maxConcurrentHostRequests)
	}
	if rejected != total-maxConcurrentHostRequests {
		t.Fatalf("rejected=%d, want %d", rejected, total-maxConcurrentHostRequests)
	}
	close(release)
	for range maxConcurrentHostRequests {
		select {
		case message := <-connection.written:
			if response, ok := message.(*jsonrpc.Response); !ok || response.Error != nil {
				t.Fatalf("admitted request reply=%#v", message)
			}
		case <-deadline:
			t.Fatal("admitted requests did not finish")
		}
	}
	// Released slots accept new requests again.
	id, _ := jsonrpc.MakeID("host-after")
	connection.incoming <- &jsonrpc.Request{ID: id, Method: "model.generate"}
	select {
	case message := <-connection.written:
		if response, ok := message.(*jsonrpc.Response); !ok || response.Error != nil {
			t.Fatalf("request after release=%#v", message)
		}
	case <-deadline:
		t.Fatal("request after release was not handled")
	}
}
