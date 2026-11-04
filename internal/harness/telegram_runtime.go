package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/endview/freeagent/internal/channels"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	telegramPairTTL      = 10 * time.Minute
	telegramPairAttempts = 5
	telegramCallbackTTL  = 10 * time.Minute
)

type telegramPair struct {
	CodeHash [32]byte
	Code     string
	Expires  time.Time
	Attempts int
	Used     bool
}
type TelegramChannelStatus struct {
	Key          string `json:"key,omitempty"`
	Enabled      bool   `json:"enabled"`
	Running      bool   `json:"running"`
	BotID        int64  `json:"bot_id,omitempty"`
	BotUsername  string `json:"bot_username,omitempty"`
	LastUpdateID int64  `json:"last_update_id,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}
type TelegramRuntime struct {
	tasks     *TaskService
	lifecycle sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	api       *channels.TelegramAPI
	bot       channels.TelegramUser
	running   bool
	lastID    int64
	lastErr   string
	session   *channelSession
}

func NewTelegramRuntime(tasks *TaskService) *TelegramRuntime {
	r := &TelegramRuntime{tasks: tasks}
	r.session = newChannelSession(tasks, SpaceSourceTelegram, func(ctx context.Context, target ChannelTarget, reply, text string, buttons []channelButton) (string, error) {
		r.mu.Lock()
		api, bot := r.api, r.bot
		r.mu.Unlock()
		if strconv.FormatInt(bot.ID, 10) != target.ApplicationID {
			return "", &CallError{Certain: true, Message: "telegram: recipient belongs to another Bot connection"}
		}
		if api == nil {
			return "", errors.New("telegram: transport unavailable")
		}
		chat, err := strconv.ParseInt(target.ChatID, 10, 64)
		if err != nil {
			return "", ErrInvalidSpace
		}
		replyID, _ := strconv.ParseInt(reply, 10, 64)
		var keyboard *channels.TelegramKeyboard
		if len(buttons) > 0 {
			row := make([]channels.TelegramButton, 0, len(buttons))
			for _, b := range buttons {
				row = append(row, channels.TelegramButton{Text: b.Text, Data: b.Token})
			}
			keyboard = &channels.TelegramKeyboard{Rows: [][]channels.TelegramButton{row}}
		}
		id, err := api.Send(ctx, chat, replyID, text, keyboard)
		if id <= 0 {
			return "", err
		}
		return strconv.FormatInt(id, 10), err
	})
	r.session.download = r.downloadMedia
	r.session.sendFile = r.sendMedia
	return r
}
func (r *TelegramRuntime) SetDispatch(d ChannelDispatch) { r.session.setDispatch(d) }
func (r *TelegramRuntime) Deliver(ctx context.Context, result RunResult) error {
	return r.session.Deliver(ctx, result)
}
func (r *TelegramRuntime) Status() TelegramChannelStatus {
	r.mu.Lock()
	v := TelegramChannelStatus{Enabled: r.tasks.Config().Channels.Telegram.Enabled, Key: r.tasks.Config().Channels.Telegram.Key, Running: r.running, BotID: r.bot.ID, BotUsername: r.bot.Username, LastUpdateID: r.lastID, LastError: r.lastErr}
	r.mu.Unlock()
	return v
}
func (r *TelegramRuntime) setError(err error) { r.mu.Lock(); r.lastErr = safeError(err); r.mu.Unlock() }

// Start admits an optional network loop; errors never prevent local UI startup.
func (r *TelegramRuntime) Start(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.tasks == nil || ctx == nil {
		return errors.New("telegram: tasks/context required")
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if !r.tasks.Config().Channels.Telegram.Enabled {
		return nil
	}
	api, err := r.tasks.TelegramAPI(nil)
	if err != nil {
		r.setError(err)
		return err
	}
	loop, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.mu.Lock()
	r.api = api
	r.bot = channels.TelegramUser{}
	r.cancel = cancel
	r.done = done
	r.lastErr = ""
	r.mu.Unlock()
	go r.poll(loop, done, api)
	return nil
}
func (r *TelegramRuntime) Stop() error {
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
		case <-time.After(3 * time.Second):
			return errors.New("telegram: transport did not stop")
		}
	}
	r.mu.Lock()
	r.cancel = nil
	r.done = nil
	r.running = false
	r.api = nil
	r.mu.Unlock()
	r.session.reset("")
	return nil
}
func (r *TelegramRuntime) Reconfigure(ctx context.Context) error {
	if err := r.Stop(); err != nil {
		return err
	}
	return r.Start(ctx)
}
func (r *TelegramRuntime) poll(ctx context.Context, done chan struct{}, api *channels.TelegramAPI) {
	defer close(done)
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	var offset int64
	connected := false
	for ctx.Err() == nil {
		if !connected {
			auth, stop := context.WithTimeout(ctx, 10*time.Second)
			bot, err := api.Me(auth)
			if err == nil {
				var updates []channels.TelegramUpdate
				updates, err = api.Poll(auth, -1, 0)
				for _, u := range updates {
					if u.ID >= offset {
						offset = u.ID + 1
					}
				}
			}
			stop()
			if err != nil {
				r.setError(err)
				if !channelDelay(ctx, time.Second) {
					return
				}
				continue
			}
			r.mu.Lock()
			r.bot = bot
			r.running = true
			r.lastErr = ""
			r.mu.Unlock()
			r.session.reset(strconv.FormatInt(bot.ID, 10))
			connected = true
		}
		updates, err := api.Poll(ctx, offset, 50)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.setError(err)
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
			connected = false
			delay := time.Second
			var e *channels.TelegramError
			if errors.As(err, &e) && e.RetryAfter > delay {
				delay = e.RetryAfter
			}
			if !channelDelay(ctx, delay) {
				return
			}
			continue
		}
		for _, u := range updates {
			if u.ID < offset {
				continue
			}
			offset = u.ID + 1
			r.mu.Lock()
			r.lastID = u.ID
			r.mu.Unlock()
			if err := r.HandleUpdate(ctx, u); err != nil {
				r.setError(err)
			}
		}
	}
}
func channelDelay(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (r *TelegramRuntime) HandleUpdate(ctx context.Context, u channels.TelegramUpdate) error {
	r.mu.Lock()
	bot, api := r.bot, r.api
	r.mu.Unlock()
	e, ok := channels.TelegramEventFromUpdate(u, bot)
	if !ok {
		return nil
	}
	r.tasks.core.Store.telegramIdentity(strconv.FormatInt(bot.ID, 10))
	words := strings.Fields(e.Text)
	if len(words) > 0 {
		parts := strings.SplitN(words[0], "@", 2)
		if strings.HasPrefix(parts[0], "/") && len(parts) == 2 && !strings.EqualFold(parts[1], bot.Username) {
			return nil
		}
	}
	event := channelEvent{Target: ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: e.ApplicationID, ChatID: strconv.FormatInt(e.ChatID, 10)}, Person: e.PersonID, Name: e.DisplayName, Message: strconv.FormatInt(e.MessageID, 10), Kind: e.ChatKind, Text: e.Text, Trigger: e.Triggered, Callback: e.CallbackData, CallbackMessage: strconv.FormatInt(e.MessageID, 10), Media: e.Media, ReplyTo: e.ReplyTo}
	if event.Kind != "private" {
		event.Kind = "group"
	}
	err := r.session.handle(ctx, event)
	if e.CallbackID != "" && api != nil {
		message := "已收到。"
		if err != nil {
			message = "审批无权、已失效或内容已变化。"
		}
		return api.AnswerCallback(ctx, e.CallbackID, message)
	}
	return err
}
func shortDeliveryID(run string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", run, index)))
	return hex.EncodeToString(h[:8])
}
