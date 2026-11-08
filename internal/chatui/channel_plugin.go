package chatui

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/endview/freeagent/internal/channelwire"
)

// This route accepts only a connection-scoped plugin token. GUI, attach and
// public-web credentials confer no plugin identity, and vice versa.
func (server *Server) channelPluginAPI(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/api/channel-plugin/v1/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	if _, public := publicRequestFrom(r.Context()); public {
		http.NotFound(w, r)
		return true
	}
	server.mu.Lock()
	bridge := server.channelBridge
	server.mu.Unlock()
	if bridge == nil {
		http.NotFound(w, r)
		return true
	}
	id, err := bridge.Authenticate(r.Header.Get("X-FreeAgent-Core"), r.Header.Get("X-FreeAgent-Generation"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		server.writeError(w, 403, "channel_scope", "渠道凭据或 Core 代次无效。")
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	read := func(value any) bool {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
			server.writeError(w, 400, "input", "Invalid bounded channel request.")
			return false
		}
		return true
	}
	switch {
	case path == "capabilities" && r.Method == http.MethodPost:
		var capabilities channelwire.Capabilities
		if !read(&capabilities) {
			return true
		}
		if err := bridge.RegisterCapabilities(id, capabilities); err != nil {
			server.writeError(w, 409, "capabilities", "Channel capabilities or contract do not match.")
		} else {
			server.writeJSON(w, 200, map[string]bool{"registered": true})
		}
	case path == "credential" && r.Method == http.MethodGet:
		credential, err := bridge.Credential(id)
		if err != nil {
			server.writeError(w, 403, "credential", "Channel credential unavailable.")
		} else {
			server.writeJSON(w, 200, credential)
		}
	case path == "outbox" && r.Method == http.MethodGet:
		ops, err := bridge.Outbox(r.Context(), id)
		if err != nil {
			server.writeError(w, 409, "outbox", "Channel delivery is not available.")
		} else {
			server.writeJSON(w, 200, ops)
		}
	case path == "activities" && r.Method == http.MethodGet:
		activities, err := bridge.Activities(r.Context(), id)
		if err != nil {
			server.writeError(w, 409, "activity", "Channel activity is not available.")
		} else {
			server.writeJSON(w, 200, activities)
		}
	case path == "inbound" && r.Method == http.MethodPost:
		leave, admitted := server.enterClientRequest(r)
		if !admitted {
			server.writeError(w, 503, "owner_stopping", "Owner 正在停止，未接收输入。")
			return true
		}
		defer leave()
		var event channelwire.Event
		if !read(&event) {
			return true
		}
		if err := bridge.Inbound(r.Context(), id, event); err != nil {
			server.writeError(w, 409, "inbound", "Input was not accepted; retain the platform cursor.")
		} else {
			server.writeJSON(w, 202, channelwire.Acceptance{Received: true, Disposition: bridge.InputReceipt(r.Context(), id, event.ID)})
		}
	case path == "receipt" && r.Method == http.MethodPost:
		var receipt channelwire.Receipt
		if !read(&receipt) {
			return true
		}
		if err := bridge.Receipt(r.Context(), id, receipt); err != nil {
			server.writeError(w, 409, "receipt", "Delivery receipt did not match this connection.")
		} else {
			server.writeJSON(w, 200, map[string]bool{"recorded": true})
		}
	default:
		http.NotFound(w, r)
	}
	return true
}
