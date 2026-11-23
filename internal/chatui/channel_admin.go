package chatui

import (
	"net/http"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) telegramAdminAPI(w http.ResponseWriter, r *http.Request) bool {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "管理绑定需要认证管理入口。")
		return true
	}
	if r.Method == http.MethodGet {
		type bot struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			harness.TelegramAdminView
		}
		result := []bot{}
		for _, connection := range server.tasks.Config().Channels.Connections {
			if connection.Source != harness.SpaceSourceTelegram || !connection.Enabled {
				continue
			}
			state, err := server.tasks.TelegramAdminBinding(r.Context(), connection.ApplicationID)
			if err != nil {
				server.fail(w, err)
				return true
			}
			result = append(result, bot{connection.ApplicationID, connection.ID, state})
		}
		server.writeJSON(w, 200, result)
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	var body struct {
		Bot      string `json:"bot"`
		Action   string `json:"action"`
		User     string `json:"user,omitempty"`
		Revision *int64 `json:"expected_revision"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	if body.Revision == nil {
		server.writeError(w, 400, "revision_required", "先读取当前管理绑定。")
		return true
	}
	if err := server.tasks.ApplyTelegramAdminBinding(r.Context(), body.Bot, body.Action, body.User, *body.Revision); err != nil {
		server.fail(w, err)
		return true
	}
	server.writeJSON(w, 200, map[string]bool{"saved": true})
	return true
}
