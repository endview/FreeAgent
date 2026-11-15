package chatui

import (
	"net/http"
	"strconv"
)

func (server *Server) unresolvedAPI(w http.ResponseWriter, r *http.Request) {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "Interactive Owner access required.")
		return
	}
	if r.Method == http.MethodGet {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		items, err := server.tasks.Unresolved(r.Context(), r.URL.Query().Get("conversation_id"), after)
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, items)
		}
		return
	}
	if r.Method != http.MethodPost {
		server.writeError(w, 405, "method", "Unsupported method.")
		return
	}
	var b struct {
		Chat     string `json:"conversation_id"`
		ID       string `json:"id"`
		Revision string `json:"revision"`
		Action   string `json:"action"`
		Note     string `json:"note"`
	}
	if !server.readBody(w, r, &b) {
		return
	}
	if b.Action == "model_note" {
		server.writeError(w, 400, "action", "Use a user action.")
		return
	}
	if err := server.tasks.ResolveUnresolved(r.Context(), b.Chat, b.ID, b.Revision, b.Action, b.Note, "interactive_owner"); err != nil {
		server.fail(w, err)
	} else {
		server.writeJSON(w, 200, map[string]bool{"saved": true})
	}
}

func (server *Server) reconcileUnknownAPI(w http.ResponseWriter, r *http.Request) {
	if _, public := publicRequestFrom(r.Context()); public || !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "maintenance_only", "Local interactive maintenance access required.")
		return
	}
	digest := ""
	if r.Method == http.MethodPost {
		var b struct {
			Digest string `json:"digest"`
		}
		if !server.readBody(w, r, &b) {
			return
		}
		if b.Digest == "" {
			server.writeError(w, 400, "digest", "Review digest required.")
			return
		}
		digest = b.Digest
	} else if r.Method != http.MethodGet {
		server.writeError(w, 405, "method", "Unsupported method.")
		return
	}
	result, err := server.tasks.ReconcileReadDenials(r.Context(), digest)
	if err != nil {
		server.fail(w, err)
	} else {
		server.writeJSON(w, 200, result)
	}
}
