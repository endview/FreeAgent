package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/channels"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AppChannelRuntime is a concrete optional app connection, with no independent
// task scheduler, permission store or plugin lifecycle.
type AppChannelRuntime struct {
	tasks     *TaskService
	source    string
	session   *channelSession
	lifecycle sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	running   bool
	lastErr   string
	lark      *channels.LarkAPI
	qq        *channels.QQAPI
	bot       string
	app       string
	replies   map[string]*qqReply
}
type qqReply struct {
	at  time.Time
	seq int
}
type AppChannelStatus struct {
	Source        string           `json:"source"`
	Config        AppChannelConfig `json:"config"`
	Running       bool             `json:"running"`
	LastError     string           `json:"last_error,omitempty"`
	PairCode      string           `json:"pair_code,omitempty"`
	PairExpiresAt time.Time        `json:"pair_expires_at,omitempty"`
	Capabilities  []string         `json:"capabilities"`
	Limitations   []string         `json:"limitations"`
}

func NewLarkRuntime(tasks *TaskService) *AppChannelRuntime {
	return newAppChannelRuntime(tasks, SpaceSourceLark)
}
func NewQQRuntime(tasks *TaskService) *AppChannelRuntime {
	return newAppChannelRuntime(tasks, SpaceSourceQQ)
}
func newAppChannelRuntime(tasks *TaskService, source string) *AppChannelRuntime {
	r := &AppChannelRuntime{tasks: tasks, source: source, replies: map[string]*qqReply{}}
	r.session = newChannelSession(tasks, source, r.send)
	r.session.buttons = source == SpaceSourceLark
	return r
}
func (r *AppChannelRuntime) config() AppChannelConfig {
	c := r.tasks.Config().Channels
	if r.source == SpaceSourceLark {
		return c.Lark
	}
	return c.QQ
}
func (r *AppChannelRuntime) SetDispatch(dispatch ChannelDispatch) { r.session.setDispatch(dispatch) }
func (r *AppChannelRuntime) Deliver(ctx context.Context, result RunResult) error {
	return r.session.Deliver(ctx, result)
}
func (r *AppChannelRuntime) PairCode() (string, time.Time, error) { return r.session.PairCode() }
func (r *AppChannelRuntime) Status() AppChannelStatus {
	r.mu.Lock()
	out := AppChannelStatus{Source: r.source, Config: r.config(), Running: r.running, LastError: r.lastErr}
	r.mu.Unlock()
	r.session.mu.Lock()
	if time.Now().Before(r.session.pair.Expires) && !r.session.pair.Used {
		out.PairCode = r.session.pair.Code
		out.PairExpiresAt = r.session.pair.Expires
	}
	r.session.mu.Unlock()
	out.Capabilities = []string{"private_text", "group_received_text", "scoped_people", "local_handoff", "bounded_final_reply"}
	if r.source == SpaceSourceLark {
		out.Capabilities = append(out.Capabilities, "app_websocket", "approval_cards")
		out.Limitations = []string{"仅使用实际收到的文字；群背景取决于应用事件权限", "附件未支持；真实应用验收未运行"}
	} else {
		out.Capabilities = append(out.Capabilities, "signed_webhook", "group_mentions")
		out.Limitations = []string{"仅官方 C2C 和群 @；频道、普通群消息、编辑与审批按钮未支持", "需要用户提供公网 HTTPS 反向代理到本机回调；不提供托管", "仅保守的 120 秒、最多 5 次被动回复；超出限制保留结果并用 /status 查询", "真实应用权限、回复窗口与额度未验收"}
	}
	return out
}
func (r *AppChannelRuntime) setError(err error) {
	r.mu.Lock()
	r.lastErr = safeError(err)
	r.mu.Unlock()
}
func (r *AppChannelRuntime) Start(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	c := r.config()
	if !c.Enabled {
		return nil
	}
	if err := r.tasks.Config().Channels.validate(); err != nil {
		r.setError(err)
		return err
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if err := validateChannelKeys(r.tasks.Keys(), r.tasks.Config().Channels); err != nil {
		r.setError(err)
		return err
	}
	key, err := r.tasks.Keys().Resolve(c.Key)
	if err != nil {
		r.setError(err)
		return err
	}
	var lark *channels.LarkAPI
	var qq *channels.QQAPI
	var listener net.Listener
	if r.source == SpaceSourceLark {
		lark, err = channels.NewLarkAPI(key.BaseURL, c.AppID, key.secret, nil)
	} else {
		auth := ""
		if strings.HasPrefix(key.BaseURL, "http://") {
			auth = key.BaseURL
		}
		qq, err = channels.NewQQAPI(key.BaseURL, auth, c.AppID, key.secret, nil)
		if err == nil {
			listener, err = net.Listen("tcp", c.Listen)
		}
	}
	if err != nil {
		r.setError(err)
		return err
	}
	lifetime, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.mu.Lock()
	r.cancel = cancel
	r.done = done
	r.app = c.AppID
	r.lark = lark
	r.qq = qq
	r.lastErr = ""
	r.replies = map[string]*qqReply{}
	r.mu.Unlock()
	r.session.reset(c.AppID)
	if lark != nil {
		go r.larkLoop(lifetime, done, lark, c.AppID)
	} else {
		go r.qqLoop(lifetime, done, listener, qq, c.AppID, key.secret)
	}
	return nil
}
func (r *AppChannelRuntime) Stop() error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			return errors.New("channel: connection did not stop")
		}
	}
	r.mu.Lock()
	r.cancel = nil
	r.done = nil
	r.running = false
	r.lark = nil
	r.qq = nil
	r.mu.Unlock()
	r.session.reset("")
	return nil
}
func (r *AppChannelRuntime) Reconfigure(ctx context.Context) error {
	if err := r.Stop(); err != nil {
		return err
	}
	return r.Start(ctx)
}
func (r *AppChannelRuntime) larkLoop(ctx context.Context, done chan struct{}, api *channels.LarkAPI, app string) {
	defer close(done)
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	for ctx.Err() == nil {
		cutoff := time.Now().Add(-time.Second)
		bot, err := api.BotOpenID(ctx)
		if err == nil {
			r.mu.Lock()
			r.bot = bot
			r.running = true
			r.mu.Unlock()
			err = api.Listen(ctx, func(raw []byte) (any, error) {
				event, ok := channels.DecodeLarkEvent(raw, app, bot)
				if !ok {
					return nil, nil
				}
				if event.CreatedAt.IsZero() || event.CreatedAt.Before(cutoff) {
					return nil, nil
				}
				return nil, r.HandleLark(ctx, event)
			})
		}
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		r.setError(err)
		r.session.reset(app)
		if !channelDelay(ctx, time.Second) {
			return
		}
	}
}
func (r *AppChannelRuntime) HandleLark(ctx context.Context, e channels.LarkEvent) error {
	target := ChannelTarget{Source: SpaceSourceLark, ApplicationID: e.App + ":" + e.Tenant, ChatID: e.Chat}
	// Transport app binding and tenant scoping are checked separately; target
	// IDs remain distinct even if two tenants use identical chat strings.
	r.session.mu.Lock()
	app := r.session.application
	r.session.mu.Unlock()
	if app != "" && app != e.App {
		return ErrPermissionDenied
	}
	return r.session.handle(ctx, channelEvent{Target: target, Person: e.Person, Message: e.Message, Kind: e.Kind, Text: e.Text, Trigger: e.Trigger, Callback: e.Callback, CallbackMessage: e.CallbackMessage})
}
func (r *AppChannelRuntime) qqLoop(ctx context.Context, done chan struct{}, listener net.Listener, api *channels.QQAPI, app, secret string) {
	defer close(done)
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	cutoff := time.Now().Add(-time.Second)
	slots := make(chan struct{}, 8)
	webhook := channels.QQWebhook{Secret: secret, Handle: func(_ context.Context, raw []byte) error {
		e, ok := channels.DecodeQQEvent(raw, app)
		if !ok {
			return nil
		}
		if e.CreatedAt.IsZero() || e.CreatedAt.Before(cutoff) {
			return nil
		}
		return r.HandleQQ(ctx, e)
	}}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			webhook.ServeHTTP(w, req)
		default:
			w.WriteHeader(429)
		}
	})}
	end := make(chan struct{})
	defer close(end)
	go func() {
		select {
		case <-ctx.Done():
			server.Close()
		case <-end:
		}
	}()
	r.mu.Lock()
	r.running = true
	r.mu.Unlock()
	err := server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		r.setError(errors.New("qq: local webhook listener failed"))
	}
}
func (r *AppChannelRuntime) HandleQQ(ctx context.Context, e channels.QQEvent) error {
	r.mu.Lock()
	for key, value := range r.replies {
		if time.Since(value.at) > 120*time.Second {
			delete(r.replies, key)
		}
	}
	key := e.Chat + "/" + e.Message
	if r.replies[key] == nil && len(r.replies) < 512 {
		r.replies[key] = &qqReply{at: time.Now()}
	}
	r.mu.Unlock()
	return r.session.handle(ctx, channelEvent{Target: ChannelTarget{Source: SpaceSourceQQ, ApplicationID: e.App, ChatID: e.Chat}, Person: e.Person, Message: e.Message, Kind: e.Kind, Text: e.Text, Trigger: true})
}
func (r *AppChannelRuntime) send(ctx context.Context, target ChannelTarget, reply, text string, buttons []channelButton) (string, error) {
	r.mu.Lock()
	lark, qq, app := r.lark, r.qq, r.app
	r.mu.Unlock()
	if app != "" && !(target.ApplicationID == app || r.source == SpaceSourceLark && strings.HasPrefix(target.ApplicationID, app+":")) {
		return "", &CallError{Certain: true, Message: "channel: target belongs to another application connection"}
	}
	if r.source == SpaceSourceLark {
		if lark == nil {
			return "", errors.New("lark: transport unavailable")
		}
		parts := strings.SplitN(target.ApplicationID, ":", 2)
		if len(parts) != 2 {
			return "", ErrInvalidSpace
		}
		converted := make([]channels.LarkButton, 0, len(buttons))
		for _, b := range buttons {
			converted = append(converted, channels.LarkButton{Text: b.Text, Token: b.Token})
		}
		return lark.Send(ctx, target.ChatID, reply, text, converted)
	}
	if qq == nil {
		return "", errors.New("qq: transport unavailable")
	}
	parts := strings.SplitN(target.ChatID, ":", 2)
	if len(parts) != 2 {
		return "", ErrInvalidSpace
	}
	kind := "private"
	if parts[0] == "group" {
		kind = "group"
	}
	r.mu.Lock()
	window := r.replies[target.ChatID+"/"+reply]
	if window == nil || time.Since(window.at) > 120*time.Second || window.seq >= 5 {
		r.mu.Unlock()
		return "", &CallError{Certain: true, Message: "qq: passive reply window unavailable; use /status to retrieve saved result"}
	}
	window.seq++
	seq := window.seq
	r.mu.Unlock()
	return qq.Send(ctx, kind, parts[1], reply, text, seq)
}
