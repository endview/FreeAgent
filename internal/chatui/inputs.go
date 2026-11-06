package chatui

import (
	"context"
	"net/http"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) inputsAPI(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		state, err := server.tasks.Inputs(request.Context(), request.URL.Query().Get("conversation_id"))
		if err != nil {
			server.fail(writer, err)
		} else {
			server.writeJSON(writer, http.StatusOK, state)
		}
		return
	}
	if request.Method != http.MethodPost || !server.isInteractiveAttachRequest(request) {
		server.writeError(writer, http.StatusForbidden, "user_interaction_required", "输入控制需要已认证用户操作。")
		return
	}
	var body struct {
		Chat      string               `json:"conversation_id"`
		Operation string               `json:"operation"`
		Command   harness.InputCommand `json:"command"`
		Key       string               `json:"key"`
		RunID     string               `json:"run_id"`
	}
	if !server.readBody(writer, request, &body) {
		return
	}
	switch body.Operation {
	case "accept":
		item, err := server.tasks.AcceptInput(server.executionContext(writer), body.Chat, body.Command)
		if err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusAccepted, item)
		server.notify(harness.EventInputChanged, body.Chat, item.Status)
		if body.Command.Action == "queue" {
			server.advanceQueue(body.Chat)
		}
	case "steer":
		item, err := server.tasks.SteerInput(request.Context(), body.Chat, body.Key, body.RunID)
		if err != nil {
			server.fail(writer, err)
		} else {
			server.writeJSON(writer, http.StatusOK, item)
			server.notify(harness.EventInputChanged, body.Chat, item.Status)
		}
	case "cancel":
		item, err := server.tasks.CancelInput(request.Context(), body.Chat, body.Key)
		if err != nil {
			server.fail(writer, err)
		} else {
			server.writeJSON(writer, http.StatusOK, item)
			server.notify(harness.EventInputChanged, body.Chat, item.Status)
		}
	case "pause", "resume":
		if err := server.tasks.PauseQueue(request.Context(), body.Chat, body.Operation == "pause"); err != nil {
			server.fail(writer, err)
			return
		}
		server.writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
		if body.Operation == "resume" {
			server.advanceQueue(body.Chat)
		}
	default:
		server.writeError(writer, http.StatusBadRequest, "input", "请选择 accept、steer、cancel、pause 或 resume。")
	}
}

func (server *Server) advanceQueue(chat string) {
	if server.ctx.Err() != nil {
		return
	}
	state, err := server.tasks.Inputs(server.ctx, chat)
	if err != nil || state.Paused {
		return
	}
	for _, input := range state.Entries {
		if input.Action == "queue" && input.Status == "pending" {
			_ = server.dispatchBound(chat, "queue", func(ctx context.Context) (harness.RunResult, error) { return server.tasks.RunQueued(ctx, chat) })
			return
		}
	}
}

func (server *Server) advanceInputQueues() {
	chats, err := server.tasks.QueuedConversations(server.ctx)
	if err != nil {
		return
	}
	for _, chat := range chats {
		server.advanceQueue(chat)
	}
}

// Startup recovery runs after transports are registered. Stopped or unresolved
// work needs explicit review; only a previously completed boundary may advance.
func (server *Server) StartInputQueues() {
	chats, err := server.tasks.Conversations(server.ctx)
	if err != nil {
		return
	}
	for _, chat := range chats {
		state, err := server.tasks.Inputs(server.ctx, chat.ID)
		if err != nil || state.Paused {
			continue
		}
		pending := false
		for _, input := range state.Entries {
			pending = pending || input.Action == "queue" && input.Status == "pending"
		}
		if !pending {
			continue
		}
		if last, err := server.tasks.Latest(server.ctx, chat.ID); err == nil && last.Status != "completed" {
			_ = server.tasks.PauseQueue(server.ctx, chat.ID, true)
			continue
		}
		server.advanceQueue(chat.ID)
	}
}

func (server *Server) branchAPI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !server.isInteractiveAttachRequest(request) {
		server.writeError(writer, http.StatusForbidden, "user_interaction_required", "分支需要已认证用户操作。")
		return
	}
	var body struct {
		Chat    string                `json:"conversation_id"`
		Command harness.BranchCommand `json:"command"`
	}
	if !server.readBody(writer, request, &body) {
		return
	}
	result, err := server.tasks.BranchConversation(request.Context(), body.Chat, body.Command)
	if err != nil {
		server.fail(writer, err)
	} else {
		server.writeJSON(writer, http.StatusCreated, result)
		server.notify(harness.EventRefresh, body.Chat, "")
	}
}
