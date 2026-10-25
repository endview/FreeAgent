package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

func (server *Server) commandSessionsAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/attach/commands" {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		if id := r.URL.Query().Get("id"); id != "" {
			cursor, err := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
			if r.URL.Query().Get("cursor") == "" {
				cursor, err = 0, nil
			}
			if err != nil {
				server.fail(w, err)
				return true
			}
			record, err := server.tasks.PollCommand(r.Context(), id, cursor, 16<<10)
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, record)
			}
		} else {
			records, err := server.tasks.Commands(r.Context())
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, records)
			}
		}
	case http.MethodPost:
		if !server.isInteractiveAttachRequest(r) {
			server.writeError(w, 403, "user_interaction_required", "Command changes require the interactive owner credential.")
			return true
		}
		var body struct {
			Action    string          `json:"action"`
			ID        string          `json:"id"`
			Chat      string          `json:"conversation_id"`
			Key       string          `json:"request_key"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if !server.readBody(w, r, &body) {
			return true
		}
		if body.Action == "stop" {
			record, err := server.tasks.StopCommand(r.Context(), body.ID)
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, record)
			}
			return true
		}
		if body.Action != "start" || body.Chat == "" || body.Key == "" || len(body.Key) > 128 {
			server.writeError(w, 400, "input", "Choose start or stop with an exact command binding.")
			return true
		}
		server.executeAttachedOperationBound(w, body.Chat, body.Key, func(ctx context.Context) (any, error) {
			return server.tasks.StartCommand(ctx, body.Chat, body.Key, body.Arguments)
		})
	default:
		server.writeError(w, 405, "method", "Unsupported command operation.")
	}
	return true
}
