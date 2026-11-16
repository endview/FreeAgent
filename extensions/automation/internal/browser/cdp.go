package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type cdpReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type cdp struct {
	ws      *websocket.Conn
	next    atomic.Int64
	write   sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan cdpReply
	done    chan struct{}
	event   func(string, json.RawMessage)
}

func connect(ctx context.Context, address string, event func(string, json.RawMessage)) (*cdp, error) {
	ws, _, e := websocket.DefaultDialer.DialContext(ctx, address, nil)
	if e != nil {
		return nil, e
	}
	c := &cdp{ws: ws, pending: map[int64]chan cdpReply{}, done: make(chan struct{}), event: event}
	ws.SetReadLimit(12 << 20)
	go c.read()
	return c, nil
}
func (c *cdp) read() {
	defer close(c.done)
	for {
		_, raw, e := c.ws.ReadMessage()
		if e != nil {
			return
		}
		var message struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			cdpReply
		}
		if json.Unmarshal(raw, &message) != nil {
			return
		}
		if message.ID == 0 {
			if c.event != nil {
				c.event(message.Method, message.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[message.ID]
		delete(c.pending, message.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- message.cdpReply
		}
	}
}
func (c *cdp) call(ctx context.Context, session, method string, params any, out any) error {
	id := c.next.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	v := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		v["sessionId"] = session
	}
	c.write.Lock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	e := c.ws.WriteJSON(v)
	c.write.Unlock()
	if e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("browser connection closed")
	case r := <-ch:
		if r.Error != nil {
			return fmt.Errorf("browser: %s", r.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	}
}
func (c *cdp) close() { _ = c.ws.Close(); <-c.done }
func (c *cdp) evaluate(ctx context.Context, session, expression string, out any) error {
	var v struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if e := c.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &v); e != nil {
		return e
	}
	if len(v.Exception) > 0 {
		return errors.New("page observation unavailable; observe again")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(v.Result.Value, out)
}
