package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"net/http"
)

func (server *Server) channelCredentials(w http.ResponseWriter, r *http.Request) {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "渠道凭据需要本机用户交互。")
		return
	}
	if r.Method == http.MethodGet {
		views, err := server.tasks.ChannelKeys().List()
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, views)
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		ID       string `json:"id"`
		Remove   bool   `json:"remove"`
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		Source   string `json:"source"`
		EnvName  string `json:"env_name"`
		Secret   string `json:"secret"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	secret := []byte(body.Secret)
	body.Secret = ""
	defer clear(secret)
	result, err := server.applyDirectKeyControl(r.Context(), harness.KeyControlRequest{Namespace: "channels", Ref: body.ID, Remove: body.Remove, Input: harness.KeyInput{Name: body.Name, Protocol: body.Protocol, BaseURL: body.BaseURL, Source: body.Source, EnvName: body.EnvName, Secret: secret}})
	if err != nil {
		server.fail(w, err)
		return
	}
	server.writeJSON(w, 200, result)
}
