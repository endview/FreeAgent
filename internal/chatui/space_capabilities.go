package chatui

import (
	"net/http"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) spaceCapabilitiesAPI(w http.ResponseWriter, r *http.Request) bool {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "空间默认能力需要认证管理入口。")
		return true
	}
	if r.Method == http.MethodGet {
		control, err := server.tasks.Control()
		if err != nil {
			server.fail(w, err)
			return true
		}
		state, revisions, err := control.SpaceCapabilitySnapshot(r.Context(), r.URL.Query().Get("space"))
		if err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, map[string]any{"state": state, "catalog": server.tasks.CapabilityCatalog(), "expected_revisions": revisions})
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return true
	}
	var body struct {
		SpaceID   string                      `json:"space_id"`
		Selection harness.CapabilitySelection `json:"selection"`
		Expected  map[string]uint64           `json:"expected_revisions"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	if len(body.Expected) != 1 {
		server.writeError(w, 400, "revision_required", "先读取空间默认能力再保存。")
		return true
	}
	result, err := server.applyDirectControl(r.Context(), "", "", harness.ControlSpaceCapabilities, harness.SpaceControlArguments{SpaceID: body.SpaceID, Capabilities: &body.Selection}, body.Expected)
	if err != nil {
		server.fail(w, err)
		return true
	}
	server.writeJSON(w, 200, result)
	return true
}
