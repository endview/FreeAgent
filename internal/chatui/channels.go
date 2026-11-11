package chatui

import (
	"encoding/json"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"strings"
)

func (server *Server) ConfigureChannelBridge() error {
	config := server.tasks.Config().Channels
	enabled := false
	for _, c := range config.Connections {
		enabled = enabled || c.Enabled
	}
	server.mu.Lock()
	bridge := server.channelBridge
	generation := server.generation
	server.channelConfig = config
	server.mu.Unlock()
	if !enabled {
		if bridge != nil {
			_ = bridge.Close()
			server.mu.Lock()
			server.channelBridge = nil
			server.mu.Unlock()
		}
		return nil
	}
	if bridge != nil {
		return bridge.Configure()
	}
	next, err := harness.NewChannelBridge(server.tasks, server.origin, generation, server.DispatchChannel)
	if err != nil {
		return err
	}
	server.mu.Lock()
	server.channelBridge = next
	server.mu.Unlock()
	return nil
}
func (server *Server) channelStatus(source string) any {
	server.mu.Lock()
	bridge := server.channelBridge
	server.mu.Unlock()
	if bridge != nil {
		return bridge.Status(source)
	}
	return map[string]any{"connections": []any{}, "config": server.tasks.Config().Channels, "enabled": false, "running": false}
}
func (server *Server) channelSettings(source string) (any, error) {
	raw, err := json.Marshal(server.channelStatus(source))
	if err != nil {
		return nil, err
	}
	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	config, revisions, err := server.settingsSnapshot()
	if err != nil {
		return nil, err
	}
	status["config"] = config.Channels
	status["expected_revisions"] = settingsRevision(revisions, "config.channels")
	return status, nil
}
func (server *Server) reconfigureChannels() {
	if err := server.ConfigureChannelBridge(); err != nil {
		server.notify(harness.EventChannelError, "", "渠道进程配置未生效，请检查账号与凭据。")
	}
}
func (server *Server) channelsAPI(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/attach/")
	path = strings.TrimPrefix(path, "/api/")
	if path != "channels" && !strings.HasPrefix(path, "channels/") {
		return false
	}
	source := strings.TrimPrefix(path, "channels/")
	if source == "credentials" {
		server.channelCredentials(w, r)
		return true
	}
	if source == "telegram/access" {
		return server.telegramAccessAPI(w, r)
	}
	if path == "channels" {
		source = ""
	}
	if source != "" && source != "telegram" && source != "lark" && source != "qq" {
		http.NotFound(w, r)
		return true
	}
	if r.Method == http.MethodGet {
		status, err := server.channelSettings(source)
		if err != nil {
			server.fail(w, err)
			return true
		}
		if !server.isInteractiveAttachRequest(r) {
			// An observer can inspect connection state, never redeem a local
			// pairing capability on a different transport.
			raw, _ := json.Marshal(status)
			var visible any
			_ = json.Unmarshal(raw, &visible)
			redactPairing(visible)
			status = visible
		}
		server.writeJSON(w, 200, status)
		return true
	}
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "渠道配置和配对需要本机用户交互凭据。")
		return true
	}
	if r.Method == http.MethodPatch {
		var body struct {
			harness.ChannelsConfig
			ExpectedRevisions map[string]uint64 `json:"expected_revisions,omitempty"`
		}
		var err error
		if source == "" {
			if !server.readBody(w, r, &body) {
				return true
			}
		} else {
			server.writeError(w, 400, "connection_required", "请通过 connections 配置明确的账号连接。")
			return true
		}

		_, err = server.applyDirectControl(r.Context(), "", "", harness.ControlSetChannelPolicy, body.ChannelsConfig, body.ExpectedRevisions)
		if err != nil {
			server.fail(w, err)
			return true
		}
		status, err := server.channelSettings(source)
		if err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, status)
		return true
	}
	if r.Method == http.MethodPost && source != "" {
		if source == "telegram" {
			server.writeError(w, 410, "pairing_removed", "Telegram 使用 /start 申请与管理员批准。")
			return true
		}
		server.mu.Lock()
		bridge := server.channelBridge
		server.mu.Unlock()
		if bridge == nil {
			server.writeError(w, 409, "channel_unavailable", "当前渠道未启用。")
			return true
		}
		code, expires, err := bridge.PairCode(source, r.URL.Query().Get("connection"))
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, map[string]any{"code": code, "expires_at": expires})
		}
		return true
	}
	w.WriteHeader(405)
	return true
}

func redactPairing(value any) {
	if object, ok := value.(map[string]any); ok {
		delete(object, "pair_code")
		delete(object, "pair_expires_at")
		for _, child := range object {
			redactPairing(child)
		}
	}
}

func (server *Server) telegramAccessAPI(w http.ResponseWriter, r *http.Request) bool {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "Telegram 接入管理需要已认证的管理员交互。")
		return true
	}
	server.mu.Lock()
	telegram := server.channelBridge
	server.mu.Unlock()
	if r.Method == http.MethodGet {
		bot := ""
		grants, err := server.tasks.TelegramAccessList(r.Context(), bot)
		if err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, map[string]any{"requests": server.tasks.TelegramAccessRequests(), "access": grants})
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return true
	}
	var body struct {
		harness.TelegramAccessChange
		Code string `json:"code,omitempty"`
	}
	if !server.readBody(w, r, &body) {
		return true
	}
	if body.Code != "" {
		request, err := server.tasks.TelegramRequest(body.Code)
		if err != nil {
			server.fail(w, err)
			return true
		}
		body.RequestID = request.ID
		body.Target = request.Target
		body.Kind = request.Kind
	}
	result, err := server.applyDirectControl(r.Context(), "", "", harness.ControlTelegramAccess, body.TelegramAccessChange)
	if err != nil {
		server.fail(w, err)
		return true
	}
	var grant harness.TelegramAccess
	_ = json.Unmarshal(result.Data, &grant)
	notice := "not_requested"
	if telegram != nil && body.Enabled {
		notice = "sent"
		if err := telegram.NotifyAccess(r.Context(), grant); err != nil {
			notice = "failed_or_unknown; access remains committed; no automatic retry"
		}
	}
	server.writeJSON(w, 200, map[string]any{"applied": result.Applied, "access": grant, "notification": notice})
	return true
}

func (server *Server) reconfigureChannelsIfChanged() {
	next := server.tasks.Config().Channels
	server.mu.Lock()
	old, _ := json.Marshal(server.channelConfig)
	raw, _ := json.Marshal(next)
	changed := string(old) != string(raw)
	server.channelConfig = next
	server.mu.Unlock()
	if changed {
		server.reconfigureChannels()
	}
}
