package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/channels"
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
	r.session.download = r.downloadMedia
	r.session.sendFile = r.sendMedia
	return r
}
func (r *AppChannelRuntime) SetDispatch(dispatch ChannelDispatch) { r.session.setDispatch(dispatch) }
func (r *AppChannelRuntime) Deliver(ctx context.Context, result RunResult) error {
	return r.session.Deliver(ctx, result)
}
func (r *AppChannelRuntime) PairCode() (string, time.Time, error) { return r.session.PairCode() }
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
	return r.session.handle(ctx, channelEvent{Target: target, Person: e.Person, Message: e.Message, Kind: e.Kind, Text: e.Text, Trigger: e.Trigger, Callback: e.Callback, CallbackMessage: e.CallbackMessage, Media: e.Media, ReplyTo: e.ReplyTo})
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
