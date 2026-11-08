package chatui

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
)

func (server *Server) deliveriesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("scheduled") == "true" {
			records, err := server.tasks.WakeRecords(r.Context(), r.URL.Query().Get("conversation_id"))
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, records)
			}
			return
		}
		items, err := server.tasks.ChannelDeliveries(r.Context(), r.URL.Query().Get("conversation_id"), r.URL.Query().Get("cursor"))
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, items)
		}
		return
	}
	if r.Method != http.MethodPost || !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "投递调整需要已认证用户操作。")
		return
	}
	var body struct {
		Chat    string                  `json:"conversation_id"`
		Command harness.DeliveryCommand `json:"command"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	if body.Command.Action == "preview" {
		allowed := false
		for _, rule := range server.tasks.Config().Delivery.Rules {
			allowed = allowed || rule.ID == body.Command.Rule && rule.ConversationID == body.Chat
		}
		if !allowed {
			server.writeError(w, 403, "scope", "目标对话不匹配。")
			return
		}
		o, err := server.tasks.PreviewScheduledGeneration(r.Context(), body.Command.Rule, body.Command.Key)
		if err != nil {
			server.fail(w, err)
			return
		}
		if o.Rule.ConversationID != body.Chat {
			server.writeError(w, 403, "scope", "目标对话不匹配。")
			return
		}
		err = server.dispatchBound(body.Chat, "scheduled-preview", func(ctx context.Context) (harness.RunResult, error) { return server.tasks.EvaluateWake(ctx, o.ID) })
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 202, o)
		}
		return
	}
	if err := server.tasks.ControlDelivery(r.Context(), body.Chat, body.Command); err != nil {
		server.fail(w, err)
		return
	}
	server.writeJSON(w, 200, map[string]bool{"ok": true})
}
