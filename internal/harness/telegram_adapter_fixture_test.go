package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/channels"
	"strconv"
	"strings"
	"sync"
)

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

func (r *TelegramRuntime) NotifyAccess(ctx context.Context, grant TelegramAccess) error {
	return r.session.NotifyAccess(ctx, grant)
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
