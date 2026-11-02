package channels

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type QQAPI struct {
	api, auth   appHTTP
	app, secret string
	mu          sync.Mutex
	token       string
	expiry      time.Time
}

func NewQQAPI(base, auth, app, secret string, client *http.Client) (*QQAPI, error) {
	if base == "" {
		base = "https://api.sgroup.qq.com"
	}
	if auth == "" {
		auth = "https://bots.qq.com"
	}
	h, err := newAppHTTP(base, "qq", []string{"api.sgroup.qq.com", "sandbox.api.sgroup.qq.com"}, client)
	if err != nil {
		return nil, err
	}
	a, err := newAppHTTP(auth, "qq", []string{"bots.qq.com"}, client)
	if err != nil {
		return nil, err
	}
	if !validID(app) || len(secret) < 1 || len(secret) > 4096 {
		return nil, errors.New("qq: app ID and secret required")
	}
	return &QQAPI{api: h, auth: a, app: app, secret: secret}, nil
}
func (a *QQAPI) AccessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Now().Before(a.expiry) {
		return a.token, nil
	}
	var r struct {
		Token  string          `json:"access_token"`
		Expire json.RawMessage `json:"expires_in"`
	}
	err := a.auth.request(ctx, "/app/getAppAccessToken", "", map[string]string{"appId": a.app, "clientSecret": a.secret}, &r, false)
	if err != nil {
		return "", err
	}
	seconds, err := strconv.Atoi(strings.Trim(string(r.Expire), `"`))
	if err != nil || seconds < 1 || r.Token == "" {
		return "", errors.New("qq: invalid token response")
	}
	a.token = r.Token
	a.expiry = time.Now().Add(time.Duration(max(seconds-60, 1)) * time.Second)
	return a.token, nil
}
func (a *QQAPI) Send(ctx context.Context, kind, target, reply, text string, seq int) (string, error) {
	if !validID(target) || !validID(reply) || text == "" || len(text) > 16<<10 || seq < 1 || seq > 5 {
		return "", errors.New("qq: bounded passive reply required")
	}
	token, err := a.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	path := "/v2/users/"
	if kind == "group" {
		path = "/v2/groups/"
	} else if kind != "private" {
		return "", errors.New("qq: unsupported chat type")
	}
	var out struct {
		ID string `json:"id"`
	}
	err = a.api.request(ctx, path+url.PathEscape(target)+"/messages", "QQBot "+token, map[string]any{"content": text, "msg_type": 0, "msg_id": reply, "msg_seq": seq}, &out, true)
	if err != nil {
		return "", err
	}
	if !validID(out.ID) {
		return "", &APIError{Platform: "qq", Status: 200, Uncertain: true}
	}
	return out.ID, nil
}

type QQEvent struct {
	App, Chat, Person, Message, Kind, Text string
	CreatedAt                              time.Time
}

func DecodeQQEvent(raw []byte, app string) (QQEvent, bool) {
	var w struct {
		Type string `json:"t"`
		D    struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Time    string `json:"timestamp"`
			Group   string `json:"group_openid"`
			Author  struct {
				User   string `json:"user_openid"`
				Member string `json:"member_openid"`
			} `json:"author"`
		} `json:"d"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &w) != nil || !validID(app) || !validID(w.D.ID) || len(w.D.Content) > 32<<10 {
		return QQEvent{}, false
	}
	e := QQEvent{App: app, Message: w.D.ID, Text: strings.TrimSpace(w.D.Content)}
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, w.D.Time)
	switch w.Type {
	case "C2C_MESSAGE_CREATE":
		if !validID(w.D.Author.User) {
			return e, false
		}
		e.Kind = "private"
		e.Chat = "c2c:" + w.D.Author.User
		e.Person = "qq:" + app + ":c2c:" + w.D.Author.User
	case "GROUP_AT_MESSAGE_CREATE":
		if !validID(w.D.Group) || !validID(w.D.Author.Member) {
			return e, false
		}
		if strings.HasPrefix(e.Text, "<@!") {
			if end := strings.IndexByte(e.Text, '>'); end > 3 && end < 96 {
				e.Text = strings.TrimSpace(e.Text[end+1:])
			}
		}
		e.Kind = "group"
		e.Chat = "group:" + w.D.Group
		e.Person = "qq:" + app + ":group:" + w.D.Group + ":" + w.D.Author.Member
	default:
		return e, false
	}
	return e, e.Text != ""
}

// QQ officially uses authenticated HTTP callbacks. The listener is separate
// from the interactive loopback API and must be exposed by a user-run proxy.
type QQWebhook struct {
	Secret string
	Handle func(context.Context, []byte) error
	Now    func() time.Time
}

func qqPrivate(secret string) ed25519.PrivateKey {
	seed := []byte(secret)
	if len(seed) == 0 {
		return nil
	}
	for len(seed) < 32 {
		seed = append(seed, []byte(secret)...)
	}
	return ed25519.NewKeyFromSeed(seed[:32])
}
func QQSignature(secret, timestamp string, body []byte) string {
	key := qqPrivate(secret)
	if key == nil {
		return ""
	}
	return hex.EncodeToString(ed25519.Sign(key, append([]byte(timestamp), body...)))
}
func (w QQWebhook) ServeHTTP(out http.ResponseWriter, req *http.Request) {
	out.Header().Set("Content-Type", "application/json")
	out.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost || req.URL.Path != "/qq/events" {
		http.NotFound(out, req)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		out.WriteHeader(413)
		return
	}
	timestamp := req.Header.Get("X-Signature-Timestamp")
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	if err != nil || now.Unix()-ts > 300 || ts-now.Unix() > 60 {
		out.WriteHeader(401)
		return
	}
	signature, err := hex.DecodeString(req.Header.Get("X-Signature-Ed25519"))
	key := qqPrivate(w.Secret)
	if err != nil || key == nil || !ed25519.Verify(key.Public().(ed25519.PublicKey), append([]byte(timestamp), body...), signature) {
		out.WriteHeader(401)
		return
	}
	var wire struct {
		Op int             `json:"op"`
		D  json.RawMessage `json:"d"`
	}
	if json.Unmarshal(body, &wire) != nil {
		out.WriteHeader(400)
		return
	}
	switch wire.Op {
	case 13:
		var d struct {
			Token string `json:"plain_token"`
			TS    string `json:"event_ts"`
		}
		if json.Unmarshal(wire.D, &d) != nil || d.Token == "" || len(d.Token) > 1024 || len(d.TS) > 32 {
			out.WriteHeader(400)
			return
		}
		json.NewEncoder(out).Encode(map[string]string{"plain_token": d.Token, "signature": QQSignature(w.Secret, d.TS, []byte(d.Token))})
	case 1:
		var seq uint32
		if json.Unmarshal(wire.D, &seq) != nil {
			out.WriteHeader(400)
			return
		}
		json.NewEncoder(out).Encode(map[string]any{"op": 11, "d": seq})
	case 0:
		result := 0
		if w.Handle == nil || w.Handle(req.Context(), body) != nil {
			result = 1
		}
		json.NewEncoder(out).Encode(map[string]int{"op": 12, "d": result})
	default:
		out.WriteHeader(400)
	}
}
