package chatui

import (
	"net/http"
	"os"

	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) serverPolicyAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/server" {
		return false
	}
	if r.Method == http.MethodPut {
		var config harness.ServerConfig
		if !server.readBody(w, r, &config) {
			return true
		}
		if _, err := server.applyDirectControl(r.Context(), "", "", harness.ControlSetServerPolicy, config); err != nil {
			server.fail(w, err)
			return true
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	server.writeJSON(w, http.StatusOK, map[string]any{"policy": server.tasks.Config().Server, "mode": server.ownerInfo.OwnerKind, "origin": server.origin, "uid": os.Geteuid(), "root_host": os.Geteuid() == 0, "sandbox": harness.CheckSandbox(r.Context()), "restart_required": r.Method == http.MethodPut})
	return true
}
