package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"net/http"
)

func (server *Server) stickersAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "贴纸操作需要已认证用户选择。")
		return
	}
	var body struct {
		Chat    string                 `json:"conversation_id"`
		Command harness.StickerCommand `json:"command"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	value, err := server.tasks.ChannelStickerCommand(r.Context(), body.Chat, body.Command)
	if err != nil {
		server.fail(w, err)
	} else {
		server.writeJSON(w, 200, value)
	}
}
