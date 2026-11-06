package chatui

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"sort"
)

type ownerExecution struct {
	ctx     context.Context
	cancel  context.CancelFunc
	binding string
}

func (server *Server) activeChatsLocked() []string {
	ids := make([]string, 0, len(server.running))
	for id := range server.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (server *Server) firstActiveLocked() (string, string) {
	ids := server.activeChatsLocked()
	if len(ids) == 0 {
		return "", ""
	}
	return ids[0], server.running[ids[0]].binding
}
func (server *Server) reserve(chat, binding string) (*ownerExecution, error) {
	return server.reserveContext(server.ctx, chat, binding)
}
func (server *Server) reserveContext(parent context.Context, chat, binding string) (*ownerExecution, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat == "" || server.closed || server.stopping || server.running[chat] != nil || len(server.running) >= harness.MaxForegroundRuns {
		return nil, harness.ErrActiveRun
	}
	ctx, cancel := context.WithCancel(parent)
	item := &ownerExecution{ctx: ctx, cancel: cancel, binding: binding}
	server.running[chat] = item
	server.wg.Add(1)
	return item, nil
}
func (server *Server) release(chat string, item *ownerExecution) {
	item.cancel()
	server.mu.Lock()
	delete(server.running, chat)
	if server.stopping && len(server.running) == 0 {
		server.requestStopLocked()
	}
	server.mu.Unlock()
	server.wg.Done()
	server.notify(harness.EventRefresh, chat, "")
	// A queue accepted in another idle conversation may have been waiting for
	// the same foreground slot. Reuse this Owner dispatch boundary on release.
	server.advanceInputQueues()
}
func (server *Server) cancelConversation(chat, binding string) bool {
	_ = server.tasks.PauseQueue(server.ctx, chat, true)
	server.mu.Lock()
	defer server.mu.Unlock()
	item := server.running[chat]
	if item == nil || binding != "" && binding != item.binding {
		return false
	}
	item.cancel()
	return true
}

// DispatchChannel admits the same bounded operation used by GUI and CLI.
// Receivers remain free to record background messages while a model is busy.
func (server *Server) DispatchChannel(chat string, execute func(context.Context) (harness.RunResult, error)) error {
	return server.dispatchBound(chat, "", execute)
}
func (server *Server) dispatchBound(chat, binding string, execute func(context.Context) (harness.RunResult, error)) error {
	return server.dispatchBoundContext(server.ctx, chat, binding, execute)
}
func (server *Server) dispatchBoundContext(parent context.Context, chat, binding string, execute func(context.Context) (harness.RunResult, error)) error {
	item, err := server.reserveContext(parent, chat, binding)
	if err != nil {
		return err
	}
	go func() {
		defer server.release(chat, item)
		result, err := execute(item.ctx)
		server.deliverResult(result)
		if err != nil {
			code, message := userError(err)
			server.notify(harness.EventRequestError, chat, code+": "+message)
		}
	}()
	return nil
}
func (server *Server) AddChannelDelivery(deliver func(context.Context, harness.RunResult) error) {
	server.mu.Lock()
	server.deliveries = append(server.deliveries, deliver)
	server.mu.Unlock()
}
func (server *Server) deliverResult(result harness.RunResult) {
	if result.Run.ID == "" || result.Duplicate {
		return
	}
	server.mu.Lock()
	deliveries := append([]func(context.Context, harness.RunResult) error(nil), server.deliveries...)
	server.mu.Unlock()
	server.mu.Lock()
	bridge := server.channelBridge
	server.mu.Unlock()
	if bridge != nil {
		_ = bridge.Deliver(server.ctx, result)
	}
	for _, deliver := range deliveries {
		if err := deliver(server.ctx, result); err != nil {
			server.notify(harness.EventChannelDeliveryError, result.Run.ConversationID, "渠道交付未确认；执行不会重放。")
		}
	}
}

func (server *Server) bindingsLocked() map[string]string {
	out := map[string]string{}
	for chat, item := range server.running {
		out[chat] = item.binding
	}
	return out
}
