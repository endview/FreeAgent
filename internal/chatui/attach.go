package chatui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) isAttachRequest(request *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(request.Header.Get("X-FreeAgent-Attach")), []byte(server.attachToken)) == 1
}

func (server *Server) isInteractiveAttachRequest(request *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(request.Header.Get("X-FreeAgent-Key")), []byte(server.token)) == 1
}

func (server *Server) attachAPI(writer http.ResponseWriter, request *http.Request) bool {
	if !strings.HasPrefix(request.URL.Path, "/api/attach/") ||
		(!server.isAttachRequest(request) && !server.isInteractiveAttachRequest(request)) {
		return false
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/attach/")
	if server.commandSessionsAPI(writer, request) {
		return true
	}
	switch {
	case path == "context" && request.Method == http.MethodGet:
		state, err := server.tasks.ContextState(request.Context(), request.URL.Query().Get("conversation_id"))
		if err != nil {
			server.fail(writer, err)
		} else {
			server.writeJSON(writer, http.StatusOK, state)
		}
		return true
	case path == "context" && request.Method == http.MethodPost:
		if !server.isInteractiveAttachRequest(request) {
			server.writeError(writer, http.StatusForbidden, "user_interaction_required", "Context changes require the interactive owner credential.")
			return true
		}
		var body struct {
			Chat    string                 `json:"conversation_id"`
			Command harness.ContextCommand `json:"command"`
			Key     string                 `json:"request_key"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.Chat == "" || body.Key == "" || len(body.Key) > 128 {
			server.writeError(writer, http.StatusBadRequest, "input", "Conversation and request key are required.")
			return true
		}
		server.executeAttachedOperationBound(writer, body.Chat, body.Key, func(ctx context.Context) (any, error) {
			return server.tasks.ManageContext(ctx, body.Chat, body.Command)
		})
		return true
	case path == "chat-command" && request.Method == http.MethodPost:
		server.chatCommand(writer, request)
		return true
	case path == "collaboration" && request.Method == http.MethodPost:
		if !server.isInteractiveAttachRequest(request) {
			server.writeError(writer, http.StatusForbidden, "user_interaction_required", "协作控制需要当前用户界面的交互确认。")
			return true
		}
		var command harness.CollaborationCommand
		if !server.readBody(writer, request, &command) {
			return true
		}
		if command.Action == "continue" || command.Action == "confirm" {
			server.executeAttachedOperation(writer, command.Parent, func(ctx context.Context) (any, error) {
				return harness.ExecuteCollaborationCommand(ctx, server.tasks, command)
			})
			return true
		}
		result, err := harness.ExecuteCollaborationCommand(request.Context(), server.tasks, command)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, result)
		return true
	case path == "memory" && request.Method == http.MethodPost:
		if !server.isInteractiveAttachRequest(request) {
			server.writeError(writer, http.StatusForbidden, "user_interaction_required", "memory 管理需要当前用户界面的交互确认。")
			return true
		}
		var command harness.MemoryCommand
		if !server.readBody(writer, request, &command) {
			return true
		}
		result, err := harness.ExecuteMemoryCommand(request.Context(), server.tasks, command)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, result)
		return true
	case path == "state" && request.Method == http.MethodGet:
		control, err := server.tasks.Control()
		if err != nil {
			server.fail(writer, err)
			return true
		}
		state, err := control.ReadControlStateForModel(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.mu.Lock()
		active, binding, generation, seq, stopping, owner := server.active, server.activeBinding, server.generation, server.eventSeq, server.stopping, server.ownerInfo
		server.mu.Unlock()
		conversations, err := server.tasks.Conversations(request.Context())
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"protocol": "freeagent-attach-v1", "data_format": "freeagent-harness-v7",
			"generation": generation, "owner_kind": owner.OwnerKind, "pid": owner.PID, "expires_at": owner.ExpiresAt,
			"stopping": stopping, "last_seq": seq, "active_chat": active, "active_binding": binding, "active_commands": server.tasks.HasCommands(), "control": state, "conversations": conversations})
		return true
	case path == "events" && request.Method == http.MethodGet:
		return server.attachEvents(writer, request)
	case path == "conversation" && request.Method == http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		conversation, err := server.tasks.NewConversation(request.Context(), body.Title)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusCreated, conversation)
		return true
	case path == "status" && request.Method == http.MethodPost:
		var body struct {
			ConversationID string `json:"conversation_id"`
			RequestKey     string `json:"request_key"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.ConversationID == "" || body.RequestKey == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "conversation_id 和 request_key 均不能为空。")
			return true
		}
		run, found, err := server.tasks.RequestStatus(request.Context(), body.ConversationID, body.RequestKey)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"found": found, "request_key": body.RequestKey, "run": run})
		return true
	case path == "chat" && request.Method == http.MethodPost:
		var body struct {
			ConversationID string `json:"conversation_id"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.ConversationID == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "conversation_id 不能为空。")
			return true
		}
		view, err := server.tasks.Chat(request.Context(), body.ConversationID, "")
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, view)
		return true
	case path == "inspect" && request.Method == http.MethodPost:
		var body struct {
			RunID string `json:"run_id"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.RunID == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "run_id 不能为空。")
			return true
		}
		run, attempts, evidence, err := server.tasks.Inspect(request.Context(), body.RunID)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, map[string]any{"run": run, "attempts": attempts, "evidence": evidence})
		return true
	case path == "submit" && request.Method == http.MethodPost:
		var body struct {
			ConversationID string             `json:"conversation_id"`
			Prompt         string             `json:"prompt"`
			RequestKey     string             `json:"request_key"`
			Options        harness.RunOptions `json:"options"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if strings.TrimSpace(body.ConversationID) == "" || strings.TrimSpace(body.Prompt) == "" || strings.TrimSpace(body.RequestKey) == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "conversation_id、prompt 和 request_key 均不能为空。")
			return true
		}
		server.startBound(writer, body.ConversationID, body.RequestKey, func(ctx context.Context) (harness.RunResult, error) {
			options := body.Options
			if options.Collaboration == nil {
				off := harness.DefaultCollaborationSettings()
				off.Mode = "off"
				options.Collaboration = &off
			}
			return server.tasks.SubmitWithOptions(ctx, body.ConversationID, body.RequestKey, body.Prompt, options)
		})
		return true
	case path == "preview" && request.Method == http.MethodPost:
		var body struct {
			RunID string `json:"run_id"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		confirmation, err := server.tasks.Preview(request.Context(), body.RunID)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		server.writeJSON(writer, http.StatusOK, confirmation)
		return true
	case path == "confirm" && request.Method == http.MethodPost:
		var body struct {
			Confirmation harness.Confirmation `json:"confirmation"`
			Approved     bool                 `json:"approved"`
			Code         string               `json:"code,omitempty"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.Confirmation.ControlOperationID != "" && !server.isInteractiveAttachRequest(request) {
			server.writeError(writer, http.StatusForbidden, "user_interaction_required", "控制面提案必须在当前用户界面中确认。")
			return true
		}
		run, attempts, _, err := server.tasks.Inspect(request.Context(), body.Confirmation.RunID)
		if err != nil {
			server.fail(writer, err)
			return true
		}
		if !server.isInteractiveAttachRequest(request) {
			for _, attempt := range attempts {
				if attempt.Kind == "tool" && attempt.Status == "pending" {
					var call harness.ToolCall
					if json.Unmarshal([]byte(attempt.RequestJSON), &call) == nil && call.Name == "context_manage" {
						server.writeError(writer, http.StatusForbidden, "user_interaction_required", "Context changes require the interactive owner credential.")
						return true
					}
				}
			}
		}
		server.startBound(writer, run.ConversationID, run.ID, func(ctx context.Context) (harness.RunResult, error) {
			if body.Confirmation.ControlOperationID != "" {
				return server.confirmControlProposal(ctx, run.ConversationID, run.ID, body.Confirmation, body.Code, body.Approved)
			}
			return server.tasks.Confirm(ctx, body.Confirmation.RunID, body.Confirmation, body.Approved)
		})
		return true
	case path == "cancel" && request.Method == http.MethodPost:
		var body struct {
			ConversationID string `json:"conversation_id"`
			Binding        string `json:"binding,omitempty"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		server.mu.Lock()
		requested := body.ConversationID != "" && server.active == body.ConversationID && server.activeCancel != nil &&
			(body.Binding == "" || body.Binding == server.activeBinding)
		if requested {
			server.activeCancel()
		}
		server.mu.Unlock()
		server.writeJSON(writer, http.StatusOK, map[string]bool{"requested": requested})
		return true
	case path == "continue" && request.Method == http.MethodPost:
		var body struct {
			ConversationID string                           `json:"conversation_id"`
			RunID          string                           `json:"run_id"`
			Suggestion     *harness.CollaborationSuggestion `json:"suggestion,omitempty"`
			Approved       bool                             `json:"approved"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		if body.RunID == "" || body.ConversationID == "" {
			server.writeError(writer, http.StatusBadRequest, "input", "run_id 不能为空。")
			return true
		}
		if run, _, _, err := server.tasks.Inspect(request.Context(), body.RunID); err != nil {
			server.fail(writer, err)
			return true
		} else if run.ConversationID != body.ConversationID {
			server.writeError(writer, http.StatusConflict, "conversation_mismatch", "run 不属于请求的会话。")
			return true
		}
		if body.Suggestion != nil && !server.isInteractiveAttachRequest(request) {
			server.writeError(writer, http.StatusForbidden, "user_interaction_required", "Review requires the interactive owner credential.")
			return true
		}
		server.startBound(writer, body.ConversationID, body.RunID, func(ctx context.Context) (harness.RunResult, error) {
			options := harness.RunOptions{}
			if body.Suggestion != nil {
				options.Review = func(context.Context, harness.CollaborationSuggestion) (harness.CollaborationSuggestion, bool, error) {
					return *body.Suggestion, body.Approved, nil
				}
			}
			return server.tasks.ContinueWithOptions(ctx, body.RunID, options)
		})
		return true
	case path == "handoff" && request.Method == http.MethodPost:
		var body struct {
			CancelActive bool `json:"cancel_active"`
		}
		if !server.readBody(writer, request, &body) {
			return true
		}
		server.mu.Lock()
		if server.closed {
			server.mu.Unlock()
			server.writeError(writer, http.StatusConflict, "owner_closed", "owner 已经关闭。")
			return true
		}
		if server.stopping {
			server.mu.Unlock()
			server.writeJSON(writer, http.StatusAccepted, map[string]any{"accepted": true, "already_stopping": true})
			return true
		}
		active := server.active
		if (active != "" || server.tasks.HasCommands()) && !body.CancelActive {
			server.mu.Unlock()
			server.writeError(writer, http.StatusConflict, "active_run", "存在运行中的任务；请明确请求取消后再交接。")
			return true
		}
		cancel := server.activeCancel
		server.stopping = true
		server.mu.Unlock()
		if err := server.tasks.CloseCommands("owner_handoff"); err != nil {
			server.mu.Lock()
			server.stopping = false
			server.mu.Unlock()
			server.fail(writer, err)
			return true
		}
		if active == "" {
			server.mu.Lock()
			server.requestStopLocked()
			server.mu.Unlock()
		}
		if cancel != nil {
			cancel()
			server.tasks.Cancel()
		}
		server.writeJSON(writer, http.StatusAccepted, map[string]any{"accepted": true, "active": active, "cancel_requested": active != ""})
		return true
	default:
		http.NotFound(writer, request)
		return true
	}
}

func (server *Server) attachEvents(writer http.ResponseWriter, request *http.Request) bool {
	after := uint64(0)
	if raw := request.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			server.writeError(writer, http.StatusBadRequest, "cursor", "after 必须是有效的事件序号。")
			return true
		}
		after = parsed
	}
	requestedGeneration := request.URL.Query().Get("generation")
	server.mu.Lock()
	generation, next := server.generation, server.eventSeq
	if requestedGeneration != "" && requestedGeneration != generation {
		server.mu.Unlock()
		server.writeError(writer, http.StatusConflict, "generation_changed", "owner generation 已变化，请重新读取 state。")
		return true
	}
	if len(server.eventLog) != 0 && after+1 < server.eventLog[0].Seq {
		server.mu.Unlock()
		server.writeError(writer, http.StatusConflict, "cursor_expired", "事件游标已超出保留范围，请重新读取 state。")
		return true
	}
	events := make([]harness.Event, 0)
	for _, event := range server.eventLog {
		if event.Seq > after {
			events = append(events, event)
		}
	}
	active, binding := server.active, server.activeBinding
	server.mu.Unlock()
	server.writeJSON(writer, http.StatusOK, map[string]any{"generation": generation, "next_seq": next, "events": events,
		"active_chat": active, "active_binding": binding})
	return true
}
