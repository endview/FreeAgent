package chatui

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) managementAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/conversation-management" {
		return false
	}
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "需要 Owner 的交互管理入口。")
		return true
	}
	if r.Method == "GET" {
		q := r.URL.Query()
		if q.Get("attachments") == "1" {
			result, err := server.tasks.ManagedAttachments(r.Context(), q.Get("space_id"), q.Get("cursor"))
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, result)
			}
			return true
		}
		if q.Get("status") != "" {
			ids := strings.Split(q.Get("status"), ",")
			if len(ids) > 100 {
				server.writeError(w, 400, "input", "Too many conversation IDs.")
				return true
			}
			out := map[string]any{}
			for _, id := range ids {
				life, e := server.tasks.ConversationLifecycleStatus(r.Context(), id)
				if e == nil {
					out[id] = life
				}
			}
			server.writeJSON(w, 200, out)
			return true
		}
		if q.Get("result") != "" {
			result, e := server.tasks.ConversationManagementResult(r.Context(), q.Get("result"))
			if e != nil {
				server.fail(w, e)
			} else {
				server.writeJSON(w, 200, result)
			}
			return true
		}
		if id := q.Get("inspect"); id != "" {
			result, err := server.tasks.ManagedChat(r.Context(), id, q.Get("before"))
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, result)
			}
			return true
		}
		if id := q.Get("evidence"); id != "" {
			after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
			result, err := server.tasks.ManagedUnresolved(r.Context(), id, after)
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, result)
			}
			return true
		}
		if id := q.Get("deliveries"); id != "" {
			result, err := server.tasks.ManagedDeliveries(r.Context(), id, q.Get("cursor"))
			if err != nil {
				server.fail(w, err)
			} else {
				server.writeJSON(w, 200, result)
			}
			return true
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, err := server.tasks.ManageConversations(r.Context(), harness.ManagementQuery{View: q.Get("view"), SpaceID: q.Get("space_id"), Query: q.Get("q"), Before: q.Get("before"), Entry: q.Get("entry"), Filter: q.Get("filter"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: limit})
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, result)
		}
		return true
	}
	if r.Method != "POST" {
		server.writeError(w, 405, "method", "Unsupported method.")
		return true
	}
	var body struct {
		Operation    string                    `json:"operation"`
		ID           string                    `json:"id"`
		Command      harness.ManagementCommand `json:"command"`
		Conversation string                    `json:"conversation_id"`
		Item         string                    `json:"item"`
		Revision     string                    `json:"revision"`
		Resolution   string                    `json:"resolution"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	switch body.Operation {
	case "resolve":
		if err := server.tasks.ManagedResolveUnresolved(r.Context(), body.Conversation, body.Item, body.Revision, body.Resolution); err != nil {
			server.fail(w, err)
		} else {
			server.notify(harness.EventRefresh, body.Conversation, "未确认记录已保存")
			server.writeJSON(w, 200, map[string]bool{"saved": true})
		}
	case "preview":
		result, err := server.tasks.PreviewConversationManagement(r.Context(), body.Command)
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, result)
		}
	case "execute":
		result, err := server.applyDirectControl(r.Context(), "", "", harness.ControlManageConversations, map[string]any{"preview_id": body.ID, "command": harness.ManagementCommand{}})
		if err != nil {
			server.fail(w, err)
		} else {
			server.notify(harness.EventRefresh, "", "会话管理状态已更新")
			server.writeJSON(w, 200, json.RawMessage(result.Data))
		}
	case "finish":
		err := server.tasks.FinishManagementTasks(r.Context(), body.ID)
		if err != nil {
			server.fail(w, err)
		} else {
			server.notify(harness.EventRefresh, "", "任务已结束，请更新预览")
			server.writeJSON(w, 200, map[string]bool{"finished": true})
		}
	default:
		server.writeError(w, 400, "input", "Unknown management operation.")
	}
	return true
}
