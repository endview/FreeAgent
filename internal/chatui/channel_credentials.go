package chatui

import (
	"context"
	"net/http"
	"strings"
	"time"

	tgpolicy "github.com/endview/freeagent/internal/connectors/telegram"
	"github.com/endview/freeagent/internal/harness"
)

func (server *Server) channelCredentials(w http.ResponseWriter, r *http.Request) {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "渠道凭据需要本机用户交互。")
		return
	}
	if r.URL.Path == "/api/channels/credentials/test" {
		server.testTelegramCredential(w, r)
		return
	}
	_, remote := publicRequestFrom(r.Context())
	if r.Method == http.MethodGet {
		control, err := server.tasks.Control()
		if err != nil {
			server.fail(w, err)
			return
		}
		views, revision, err := control.CredentialSettingsSnapshot(r.Context(), "channels")
		format := func(profiles any) any {
			if r.URL.Query().Get("revision") == "1" {
				return map[string]any{"profiles": profiles, "revision": revision}
			}
			return profiles
		}
		if err != nil {
			server.fail(w, err)
		} else if remote {
			references := make([]map[string]string, 0, len(views))
			for _, view := range views {
				references = append(references, map[string]string{"id": view.ID, "name": view.Name, "protocol": view.Protocol})
			}
			server.writeJSON(w, 200, format(references))
		} else {
			server.writeJSON(w, 200, format(views))
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		ExpectedRevision uint64 `json:"expected_revision,omitempty"`
		ID               string `json:"id"`
		Remove           bool   `json:"remove"`
		Name             string `json:"name"`
		Protocol         string `json:"protocol"`
		BaseURL          string `json:"base_url"`
		Source           string `json:"source"`
		EnvName          string `json:"env_name"`
		Secret           string `json:"secret"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	secret := []byte(body.Secret)
	body.Secret = ""
	defer clear(secret)
	if remote {
		if body.ID != "" {
			current, err := server.tasks.ChannelKeys().Find(body.ID)
			if err != nil || current.Protocol != harness.KeyProtocolTelegram || current.Source != harness.KeySourceSaved || strings.TrimRight(current.BaseURL, "/") != tgpolicy.APIOrigin {
				server.fail(w, harness.ErrPermissionDenied)
				return
			}
		}
		if !body.Remove && (body.Protocol != harness.KeyProtocolTelegram || body.Source != harness.KeySourceSaved ||
			strings.TrimRight(body.BaseURL, "/") != tgpolicy.APIOrigin || body.EnvName != "" || len(secret) == 0) {
			server.writeError(w, 400, "telegram_credential", "公网仅接受保存到 Telegram 官方端点的 Bot Token。")
			return
		}
		if !body.Remove {
			bot, err := tgpolicy.TokenBotID(string(secret))
			if err != nil {
				server.writeError(w, 400, "telegram_credential", "Telegram Bot Token 格式无效。")
				return
			}
			if body.ID != "" {
				current, _ := server.tasks.ChannelKeys().Find(body.ID)
				body.ID = current.ID
				for _, connection := range server.tasks.Config().Channels.Connections {
					if connection.Credential == body.ID && connection.ApplicationID != bot {
						server.writeError(w, 409, "telegram_bot_mismatch", "新 Token 对应另一个 Bot。现有连接和凭据未修改。")
						return
					}
				}
			}
		}
	}
	result, err := server.applyDirectKeyControl(r.Context(), harness.KeyControlRequest{Namespace: "channels", Ref: body.ID, Remove: body.Remove,
		ExpectedRevision: body.ExpectedRevision, Input: harness.KeyInput{Name: body.Name, Protocol: body.Protocol, BaseURL: body.BaseURL, Source: body.Source, EnvName: body.EnvName, Secret: secret}})
	if err != nil {
		server.fail(w, err)
		return
	}
	server.writeJSON(w, 200, result)
}

// Check the newly entered Token against the connector's official endpoint.
func (server *Server) testTelegramCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	release, err := harness.BeginPublicSettingsDecision(ctx, true)
	if err != nil {
		server.fail(w, err)
		return
	}
	defer release()
	bot, err := tgpolicy.ProbeBot(ctx, body.Token, nil)
	if err != nil {
		server.writeError(w, 400, "telegram_probe", "无法核实 Telegram Bot 身份，未保存凭据。")
		return
	}
	server.writeJSON(w, 200, bot)
}
