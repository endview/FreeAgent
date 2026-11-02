package channels

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LarkAPI struct {
	api         appHTTP
	app, secret string
	mu          sync.Mutex
	token       string
	expiry      time.Time
}

func NewLarkAPI(base, app, secret string, client *http.Client) (*LarkAPI, error) {
	if base == "" {
		base = "https://open.feishu.cn"
	}
	h, err := newAppHTTP(base, "lark", []string{"open.feishu.cn", "open.larksuite.com"}, client)
	if err != nil {
		return nil, err
	}
	if !validID(app) || len(secret) < 1 || len(secret) > 4096 {
		return nil, errors.New("lark: app ID and secret required")
	}
	return &LarkAPI{api: h, app: app, secret: secret}, nil
}
func (a *LarkAPI) AccessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Now().Before(a.expiry) {
		return a.token, nil
	}
	var r struct {
		Token  string `json:"tenant_access_token"`
		Expire int    `json:"expire"`
	}
	err := a.api.request(ctx, "/open-apis/auth/v3/tenant_access_token/internal", "", map[string]string{"app_id": a.app, "app_secret": a.secret}, &r, false)
	if err != nil {
		return "", err
	}
	if r.Token == "" || r.Expire < 1 {
		return "", errors.New("lark: invalid token response")
	}
	a.token = r.Token
	a.expiry = time.Now().Add(time.Duration(max(r.Expire-60, 1)) * time.Second)
	return a.token, nil
}
func (a *LarkAPI) BotOpenID(ctx context.Context) (string, error) {
	token, err := a.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	var out struct {
		Bot struct {
			Open string `json:"open_id"`
		} `json:"bot"`
	}
	err = a.api.requestMethod(ctx, http.MethodGet, "/open-apis/bot/v3/info", "Bearer "+token, nil, &out, false)
	if err != nil {
		return "", err
	}
	if !validID(out.Bot.Open) {
		return "", errors.New("lark: missing bot identity")
	}
	return out.Bot.Open, nil
}

type LarkButton struct{ Text, Token string }

func (a *LarkAPI) Send(ctx context.Context, chat, reply, text string, buttons []LarkButton) (string, error) {
	if !validID(chat) || text == "" || len(text) > 20<<10 {
		return "", errors.New("lark: invalid text/target")
	}
	token, err := a.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	msgType := "text"
	content := any(map[string]string{"text": text})
	if len(buttons) > 0 {
		msgType = "interactive"
		actions := make([]any, 0, len(buttons))
		for _, b := range buttons {
			actions = append(actions, map[string]any{"tag": "button", "text": map[string]string{"tag": "plain_text", "content": b.Text}, "type": "default", "value": map[string]string{"freeagent_token": b.Token}})
		}
		content = map[string]any{"elements": []any{map[string]any{"tag": "div", "text": map[string]string{"tag": "plain_text", "content": text}}, map[string]any{"tag": "action", "actions": actions}}}
	}
	raw, _ := json.Marshal(content)
	input := map[string]any{"receive_id": chat, "msg_type": msgType, "content": string(raw)}
	path := "/open-apis/im/v1/messages?receive_id_type=chat_id"
	if reply != "" {
		if !validID(reply) {
			return "", errors.New("lark: invalid reply ID")
		}
		path = "/open-apis/im/v1/messages/" + url.PathEscape(reply) + "/reply"
		delete(input, "receive_id")
	}
	var out struct {
		Data struct {
			Message string `json:"message_id"`
		} `json:"data"`
	}
	if err := a.api.request(ctx, path, "Bearer "+token, input, &out, true); err != nil {
		return "", err
	}
	if !validID(out.Data.Message) {
		return "", &APIError{Platform: "lark", Status: 200, Uncertain: true}
	}
	return out.Data.Message, nil
}

// Event is decoded only from the authenticated app websocket; user text never
// supplies its own principal. Tenant/app scope remains in all external IDs.
type LarkEvent struct {
	Media                                                                              []MediaRef
	App, Tenant, Chat, Person, Message, Kind, Text, Callback, CallbackMessage, ReplyTo string
	Trigger                                                                            bool
	CreatedAt                                                                          time.Time
}

func DecodeLarkEvent(raw []byte, app, bot string) (LarkEvent, bool) {
	var wire struct {
		Header struct {
			App    string `json:"app_id"`
			Tenant string `json:"tenant_key"`
			Type   string `json:"event_type"`
			Time   string `json:"create_time"`
		} `json:"header"`
		Event struct {
			Sender struct {
				Type string `json:"sender_type"`
				ID   struct {
					Open string `json:"open_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				ID       string `json:"message_id"`
				Chat     string `json:"chat_id"`
				Kind     string `json:"chat_type"`
				Type     string `json:"message_type"`
				Content  string `json:"content"`
				Time     string `json:"create_time"`
				Parent   string `json:"parent_id"`
				Mentions []struct {
					Key string `json:"key"`
					ID  struct {
						Open string `json:"open_id"`
					} `json:"id"`
				} `json:"mentions"`
			} `json:"message"`
			Operator struct {
				Open string `json:"open_id"`
			} `json:"operator"`
			Action struct {
				Value map[string]string `json:"value"`
			} `json:"action"`
			Context struct {
				Chat    string `json:"open_chat_id"`
				Message string `json:"open_message_id"`
			} `json:"context"`
		} `json:"event"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &wire) != nil || wire.Header.App != app || !validID(wire.Header.Tenant) {
		return LarkEvent{}, false
	}
	e := LarkEvent{App: app, Tenant: wire.Header.Tenant}
	if ts, err := strconv.ParseInt(wire.Header.Time, 10, 64); err == nil {
		e.CreatedAt = time.UnixMilli(ts)
	}
	switch wire.Header.Type {
	case "im.message.receive_v1":
		m := wire.Event.Message
		if (m.Type != "text" && m.Type != "file" && m.Type != "image") || wire.Event.Sender.Type != "user" || !validID(wire.Event.Sender.ID.Open) {
			return e, false
		}
		var content struct {
			Text  string `json:"text"`
			File  string `json:"file_key"`
			Image string `json:"image_key"`
			Name  string `json:"file_name"`
		}
		if json.Unmarshal([]byte(m.Content), &content) != nil || len(content.Text) > 32<<10 {
			return e, false
		}
		if m.Type == "file" {
			if !validID(content.File) {
				return e, false
			}
			e.Media = []MediaRef{{ID: content.File, Name: content.Name}}
			content.Text = "[附件]"
		} else if m.Type == "image" {
			if !validID(content.Image) {
				return e, false
			}
			e.Media = []MediaRef{{ID: content.Image, Name: "image.png", Image: true}}
			content.Text = "[图片附件]"
		}
		if content.Text == "" {
			return e, false
		}
		e.Chat, e.Message, e.Text, e.ReplyTo = m.Chat, m.ID, content.Text, m.Parent
		e.Person = "lark:" + app + ":" + e.Tenant + ":open:" + wire.Event.Sender.ID.Open
		if m.Kind == "p2p" {
			e.Kind = "private"
			e.Trigger = true
		} else if m.Kind == "group" {
			e.Kind = "group"
			for _, mention := range m.Mentions {
				if bot != "" && mention.ID.Open == bot {
					e.Trigger = true
					e.Text = strings.ReplaceAll(e.Text, mention.Key, "")
				}
			}
			if strings.HasPrefix(strings.TrimSpace(e.Text), "/") {
				e.Trigger = true
			}
		} else {
			return e, false
		}
		if ts, err := strconv.ParseInt(m.Time, 10, 64); err == nil {
			e.CreatedAt = time.UnixMilli(ts)
		}
	case "card.action.trigger":
		e.Chat, e.Message = wire.Event.Context.Chat, wire.Event.Context.Message
		e.CallbackMessage = e.Message
		e.Callback = wire.Event.Action.Value["freeagent_token"]
		if !validID(wire.Event.Operator.Open) || e.Callback == "" {
			return e, false
		}
		e.Person = "lark:" + app + ":" + e.Tenant + ":open:" + wire.Event.Operator.Open
	default:
		return e, false
	}
	return e, validID(e.Chat) && validID(e.Message)
}

// Listen performs one connection. Its caller chooses bounded reconnect delays.
func (a *LarkAPI) Listen(ctx context.Context, receive func([]byte) (any, error)) error {
	var boot struct {
		Data struct {
			URL    string `json:"URL"`
			Config struct {
				Ping int `json:"PingInterval"`
			} `json:"ClientConfig"`
		} `json:"data"`
	}
	if err := a.api.request(ctx, "/callback/ws/endpoint", "", map[string]string{"AppID": a.app, "AppSecret": a.secret}, &boot, false); err != nil {
		return err
	}
	endpoint, err := url.Parse(boot.Data.URL)
	if err != nil || endpoint.User != nil {
		return errors.New("lark: invalid websocket endpoint")
	}
	origin, _ := url.Parse(a.api.base)
	ip := net.ParseIP(endpoint.Hostname())
	fixture := ip != nil && ip.IsLoopback() && origin.Scheme == "http" && endpoint.Scheme == "ws"
	if !fixture && !(endpoint.Scheme == "wss" && (strings.HasSuffix(endpoint.Hostname(), ".feishu.cn") || strings.HasSuffix(endpoint.Hostname(), ".larksuite.com"))) {
		return errors.New("lark: websocket endpoint outside official domains")
	}
	socket, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return errors.New("lark: websocket connection failed")
	}
	defer socket.Close()
	socket.SetReadLimit(1 << 20)
	service, _ := strconv.Atoi(endpoint.Query().Get("service_id"))
	ping := time.Duration(min(max(boot.Data.Config.Ping, 5), 300)) * time.Second
	var writeMu sync.Mutex
	write := func(f LarkFrame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		socket.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return socket.WriteMessage(websocket.BinaryMessage, f.Marshal())
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(ping)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				socket.Close()
				return
			case <-ticker.C:
				if write(LarkFrame{Service: uint64(service), Headers: map[string]string{"type": "ping"}}) != nil {
					socket.Close()
					return
				}
			}
		}
	}()
	assembler := NewLarkAssembler()
	for ctx.Err() == nil {
		socket.SetReadDeadline(time.Now().Add(2*ping + 5*time.Second))
		kind, raw, err := socket.ReadMessage()
		if err != nil {
			return errors.New("lark: websocket disconnected")
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		frame, err := DecodeLarkFrame(raw)
		if err != nil {
			return err
		}
		if frame.Method == 0 {
			continue
		}
		if frame.Method != 1 || frame.Headers["type"] != "event" && frame.Headers["type"] != "card" {
			continue
		}
		payload, complete, err := assembler.Add(frame)
		if err != nil {
			return err
		}
		if !complete {
			continue
		}
		result, handleErr := receive(payload)
		code := 200
		if handleErr != nil {
			code = 500
		}
		var data []byte
		if result != nil {
			data, _ = json.Marshal(result)
		}
		frame.Payload, _ = json.Marshal(map[string]any{"code": code, "data": data, "headers": map[string]string{}})
		frame.Headers["biz_rt"] = "0"
		if err := write(frame); err != nil {
			return errors.New("lark: websocket ack failed")
		}
	}
	return ctx.Err()
}
