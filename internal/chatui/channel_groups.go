package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"net/http"
)

func (server *Server) telegramGroupsAPI(w http.ResponseWriter, r *http.Request) bool {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "群设置需要认证管理入口。")
		return true
	}
	target := harness.ChannelTarget{Source: "telegram", ApplicationID: r.URL.Query().Get("bot"), ChatID: r.URL.Query().Get("chat")}
	if r.Method == http.MethodGet {
		state, err := server.tasks.TelegramGroup(r.Context(), target)
		if err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, map[string]any{"state": state})
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return true
	}
	var body struct {
		Target         harness.ChannelTarget `json:"target"`
		ReplyAll       bool                  `json:"reply_all"`
		SaveBackground bool                  `json:"save_background"`
		Revision       *int64                `json:"expected_revision"`
		Operation      string                `json:"operation_id"`
		Action         string                `json:"action"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	if body.Action == "clear_background" {
		if err := server.tasks.ClearTelegramBackground(r.Context(), body.Target); err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, map[string]bool{"cleared": true})
		return true
	}
	if body.Action != "save" || body.Revision == nil {
		server.writeError(w, 400, "revision_required", "先读取当前群设置再保存。")
		return true
	}
	state, err := server.tasks.ApplyTelegramGroup(r.Context(), body.Target, body.ReplyAll, body.SaveBackground, *body.Revision, body.Operation)
	if err != nil {
		server.fail(w, err)
		return true
	}
	server.writeJSON(w, 200, map[string]any{"state": state})
	return true
}
