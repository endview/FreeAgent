package chatui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/endview/freeagent/internal/harness"
)

type spaceView struct {
	harness.Space
	Permission   harness.PermissionMode      `json:"permission"`
	OwnerPrivate bool                        `json:"owner_private"`
	Selection    harness.PermissionSelection `json:"selection"`
}

func (server *Server) spacesAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/spaces" && path != "/api/attach/spaces" {
		return false
	}
	if r.Method == http.MethodGet {
		spaces, err := server.tasks.Spaces(r.Context())
		if err != nil {
			server.fail(w, err)
			return true
		}
		result := make([]spaceView, 0, len(spaces))
		for _, space := range spaces {
			p, err := server.tasks.SpacePermission(r.Context(), space.ID)
			if err != nil {
				server.fail(w, err)
				return true
			}
			result = append(result, spaceView{space, p.Mode, p.OwnerPrivate, p})
		}
		server.writeJSON(w, http.StatusOK, result)
		return true
	}
	if r.Method != http.MethodPost {
		server.writeError(w, http.StatusMethodNotAllowed, "method", "Unsupported method.")
		return true
	}
	if strings.HasPrefix(path, "/api/attach/") && !server.isInteractiveAttachRequest(r) {
		server.writeError(w, http.StatusForbidden, "user_interaction_required", "Space changes require the interactive owner credential.")
		return true
	}
	var body struct {
		Kind      string          `json:"kind"`
		Arguments json.RawMessage `json:"arguments"`
		Confirmed bool            `json:"confirmed"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	allowed := false
	for _, entry := range harness.ControlOperationCatalog() {
		if entry.Kind == body.Kind && (strings.HasPrefix(body.Kind, "space.") || body.Kind == harness.ControlPersonPermission) {
			allowed = true
		}
	}
	if !allowed {
		server.writeError(w, http.StatusBadRequest, "input", "Unknown space operation.")
		return true
	}
	var args harness.SpaceControlArguments
	if json.Unmarshal(body.Arguments, &args) != nil {
		server.writeError(w, http.StatusBadRequest, "input", "Invalid arguments.")
		return true
	}
	if body.Kind == harness.ControlDeleteSpace || body.Kind == harness.ControlMoveConversation || args.Mode == harness.PermissionFull || args.Selection != nil && args.Selection.Mode == harness.PermissionFull {
		if !body.Confirmed {
			server.permissionFailure(w, harness.ErrFullAccessConfirmation)
			return true
		}
	}
	result, err := server.applyDirectControl(r.Context(), "", "", body.Kind, args)
	if err != nil {
		server.fail(w, err)
		return true
	}
	server.writeJSON(w, http.StatusOK, result)
	return true
}
