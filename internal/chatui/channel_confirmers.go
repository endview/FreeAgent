package chatui

import (
	"net/http"

	tgpolicy "github.com/endview/freeagent/internal/connectors/telegram"
	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) telegramConfirmersAPI(w http.ResponseWriter, r *http.Request) bool {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "设置确认名单需要认证管理入口。")
		return true
	}
	if r.Method == http.MethodGet {
		bots := []any{}
		for _, c := range server.tasks.Config().Channels.Connections {
			if c.Source != "telegram" || !c.Enabled {
				continue
			}
			state, err := server.tasks.TelegramConfirmers(r.Context(), c.ApplicationID)
			if err != nil {
				server.fail(w, err)
				return true
			}
			bots = append(bots, map[string]any{"application_id": c.ApplicationID, "name": c.ID, "state": state})
		}
		server.writeJSON(w, 200, map[string]any{"bots": bots})
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return true
	}
	var body struct {
		Bot       string   `json:"bot"`
		Members   []string `json:"members"`
		Revision  *int64   `json:"expected_revision"`
		Operation string   `json:"operation_id"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	if body.Revision == nil {
		server.writeError(w, 400, "revision_required", "先读取当前名单再保存。")
		return true
	}
	if _, err := tgpolicy.Confirmers(body.Members); err != nil {
		server.writeError(w, 400, "invalid_confirmers", "请填写 TG 数字用户 ID；不能使用用户名、昵称或群 ID。")
		return true
	}
	state, err := server.tasks.ApplyTelegramConfirmers(r.Context(), body.Bot, body.Members, *body.Revision, body.Operation)
	if err != nil {
		server.fail(w, err)
		return true
	}
	server.writeJSON(w, 200, struct {
		State harness.ChannelConfirmers `json:"state"`
	}{State: state})
	return true
}
