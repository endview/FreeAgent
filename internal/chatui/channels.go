package chatui

import (
	"encoding/json"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (server *Server) SetAppChannels(lark, qq *harness.AppChannelRuntime) {
	server.mu.Lock()
	server.lark, server.qq = lark, qq
	server.channelConfig = server.tasks.Config().Channels
	server.mu.Unlock()
}
func (server *Server) channelStatus(source string) any {
	server.mu.Lock()
	telegram, lark, qq := server.telegram, server.lark, server.qq
	server.mu.Unlock()
	switch source {
	case "telegram":
		if telegram != nil {
			return telegram.Status()
		}
		return harness.TelegramChannelStatus{Enabled: server.tasks.Config().Channels.Telegram.Enabled, Key: server.tasks.Config().Channels.Telegram.Key}
	case "lark":
		if lark != nil {
			return lark.Status()
		}
		return harness.AppChannelStatus{Source: source, Config: server.tasks.Config().Channels.Lark}
	case "qq":
		if qq != nil {
			return qq.Status()
		}
		return harness.AppChannelStatus{Source: source, Config: server.tasks.Config().Channels.QQ}
	}
	return map[string]any{"telegram": server.channelStatus("telegram"), "lark": server.channelStatus("lark"), "qq": server.channelStatus("qq"), "config": server.tasks.Config().Channels}
}
func (server *Server) reconfigureChannels() {
	server.mu.Lock()
	telegram, lark, qq := server.telegram, server.lark, server.qq
	server.mu.Unlock()
	if telegram != nil {
		_ = telegram.Reconfigure(server.ctx)
	}
	for _, r := range []*harness.AppChannelRuntime{lark, qq} {
		if r != nil {
			_ = r.Reconfigure(server.ctx)
		}
	}
}
func (server *Server) channelsAPI(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/attach/")
	path = strings.TrimPrefix(path, "/api/")
	if path != "channels" && !strings.HasPrefix(path, "channels/") {
		return false
	}
	source := strings.TrimPrefix(path, "channels/")
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
		status := server.channelStatus(source)
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
		config := server.tasks.Config().Channels
		var err error
		if source == "" {
			if !server.readBody(w, r, &config) {
				return true
			}
		} else if source == "telegram" {
			if !server.readBody(w, r, &config.Telegram) {
				return true
			}
		} else if source == "lark" {
			if !server.readBody(w, r, &config.Lark) {
				return true
			}
		} else {
			if !server.readBody(w, r, &config.QQ) {
				return true
			}
		}
		_, err = server.applyDirectControl(r.Context(), "", "", harness.ControlSetChannelPolicy, config)
		if err != nil {
			server.fail(w, err)
			return true
		}
		server.writeJSON(w, 200, server.channelStatus(source))
		return true
	}
	if r.Method == http.MethodPost && source != "" {
		server.mu.Lock()
		telegram, lark, qq := server.telegram, server.lark, server.qq
		server.mu.Unlock()
		var pair func() (string, time.Time, error)
		switch source {
		case "telegram":
			_ = telegram
			server.writeError(w, 410, "pairing_removed", "Telegram 已改用 /start 申请与管理员批准，请打开接入申请列表。")
			return true
		case "lark":
			if lark != nil {
				pair = lark.PairCode
			}
		case "qq":
			if qq != nil {
				pair = qq.PairCode
			}
		}
		if pair == nil {
			server.writeError(w, 409, "channel_unavailable", "当前 Owner 未装配此渠道。")
			return true
		}
		code, expires, err := pair()

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
	telegram := server.telegram
	server.mu.Unlock()
	if r.Method == http.MethodGet {
		bot := ""
		if telegram != nil {
			bot = strconv.FormatInt(telegram.Status().BotID, 10)
		}
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
