package channelplugin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
)

func runTelegram(parent context.Context, c *Client, key channelwire.Credential, path string) (result error) {
	api, err := channels.NewTelegramAPI(key.BaseURL, key.Secret, nil)
	key.Secret = ""
	if err != nil {
		return err
	}
	bot, err := api.Me(parent)
	if err != nil {
		return errors.New("channel: Telegram identity check failed")
	}
	if messageID(bot.ID) != c.Bootstrap.Connection.ApplicationID {
		return errors.New("channel: Telegram credential belongs to a different Bot")
	}
	if err := c.RegisterCapabilities(parent); err != nil {
		return err
	}
	if err := c.Observe(parent, channelwire.Observation{Stage: "connected", Count: 1, CanReadAll: &bot.CanReadGroupMessages}); err != nil {
		return err
	}
	position, err := loadCursor(path, c.Bootstrap)
	if err != nil {
		return err
	}
	var cutoff int64
	if cutover := c.Bootstrap.Connection.DropPendingBefore; cutover != "" {
		at, err := time.Parse(time.RFC3339Nano, cutover)
		if err != nil {
			return errors.New("channel: invalid cutover time")
		}
		cutoff = at.Unix()
		if position.Cutover != cutover {
			position.Cutover, position.DropState = cutover, "dispatched"
			if err := saveCursor(path, position); err != nil {
				return err
			}
			if err := api.DropPendingUpdates(parent); err != nil {
				return errors.New("channel: cutover purge UNKNOWN; verify before resuming, it will not be automatically repeated")
			}
			position.DropState = "finished"
			if err := saveCursor(path, position); err != nil {
				return err
			}
		} else if position.DropState != "finished" {
			return errors.New("channel: previous cutover purge UNKNOWN; requires explicit verification")
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	activityDone := make(chan struct{})
	go func() {
		defer close(activityDone)
		runTelegramActivities(ctx, c, api)
	}()
	defer func() { cancel(); <-activityDone }()
	outboxDone := make(chan error, 1)
	var transportFailure error
	go func() {
		transportFailure = transportOutbox(ctx, c, func(ctx context.Context, op channelwire.Operation) channelwire.Receipt {
			return telegramSend(ctx, api, op)
		})
		outboxDone <- transportFailure
		cancel()
	}()
	defer func() {
		cancel()
		<-outboxDone
		if parent.Err() == nil && transportFailure != nil {
			result = transportFailure
		}
	}()
	for ctx.Err() == nil {
		select {
		case err := <-outboxDone:
			outboxDone <- err
			return err
		default:
		}
		updates, err := api.Poll(ctx, position.Last+1, 20)
		if err != nil {
			_ = c.Observe(ctx, channelwire.Observation{Stage: "poll_failed", Count: 1})
			if !wait(ctx, time.Second) {
				break
			}
			continue
		}
		if err := c.Observe(ctx, channelwire.Observation{Stage: "poll_ok", Count: len(updates)}); err != nil {
			return err
		}
		for _, u := range updates {
			if u.ID <= position.Last {
				continue
			}
			e, ok := channels.TelegramEventFromUpdate(u, bot)
			if e.ThreadID == "invalid-topic" {
				ok = false
			}
			if ok && e.Lifecycle != nil && e.Lifecycle.Kind == "membership" && (e.Lifecycle.Status == "member" || e.Lifecycle.Status == "restricted") {
				allowed, readErr := api.GroupCanSend(ctx, e.ChatID)
				if readErr != nil {
					return errors.New("channel: current group speaking permission could not be verified")
				}
				e.Lifecycle.CanSend = e.Lifecycle.CanSend && allowed
			}
			target := channelwire.Target{}
			if e.ChatID != 0 {
				// Group chat IDs are signed; message IDs are positive only.
				target = channelwire.Target{Source: "telegram", ApplicationID: e.ApplicationID, ChatID: strconv.FormatInt(e.ChatID, 10), ThreadID: e.ThreadID}
			}
			if err := c.Observe(ctx, channelwire.Observation{Target: target, Stage: "received", Count: 1}); err != nil {
				return err
			}
			filtered := "filtered"
			if cutoff > 0 && e.Date <= cutoff {
				ok = false
				filtered = "backlog_ignored"
			}
			if ok {
				words := strings.Fields(e.Text)
				if len(words) > 0 {
					parts := strings.SplitN(words[0], "@", 2)
					if strings.HasPrefix(parts[0], "/") && len(parts) == 2 && !strings.EqualFold(parts[1], bot.Username) {
						ok = false
						filtered = "other_bot_command"
					}
				}
			}
			if ok {
				kind := "group"
				if e.ChatKind == "private" {
					kind = "private"
				}
				event := channelwire.Event{ID: strconv.FormatInt(u.ID, 10), Target: target, Person: e.PersonID, Name: e.DisplayName, Message: strconv.FormatInt(e.MessageID, 10), Kind: kind, Text: e.Text, Trigger: e.Triggered, Callback: e.CallbackData, CallbackMessage: strconv.FormatInt(e.MessageID, 10), Media: e.Media, ReplyTo: e.ReplyTo, Lifecycle: e.Lifecycle}
				event.Sticker, event.Reaction = e.Sticker, e.Reaction
				var acceptance channelwire.Acceptance
				for {
					acceptance, err = c.InboundAccepted(ctx, event)
					if err == nil {
						break
					}
					if errors.Is(err, ErrCoreUnavailable) {
						return err
					}
					if !wait(ctx, time.Second) {
						return ctx.Err()
					}
				}
				if e.CallbackID != "" {
					feedback := acceptance.Feedback
					if feedback == "" || len([]rune(feedback)) > 180 {
						feedback = "已收到。"
					}
					_ = api.AnswerCallback(ctx, e.CallbackID, feedback)
				}
			} else {
				if err := c.Observe(ctx, channelwire.Observation{Target: target, Stage: filtered, Count: 1}); err != nil {
					return err
				}
			}
			// Only Core's durable acceptance permits this platform cursor to move.
			position.Last = u.ID
			if err := saveCursor(path, position); err != nil {
				return errors.New("channel: cannot persist pull progress")
			}
		}
	}
	return ctx.Err()
}

func telegramSend(ctx context.Context, api *channels.TelegramAPI, op channelwire.Operation) channelwire.Receipt {
	chat, err := strconv.ParseInt(op.Target.ChatID, 10, 64)
	if err != nil {
		return channelwire.Receipt{State: "failed"}
	}
	reply, _ := strconv.ParseInt(op.Reply, 10, 64)
	switch op.Kind {
	case "sticker":
		if op.Sticker == nil {
			return channelwire.Receipt{State: "failed"}
		}
		id, err := api.SendSticker(ctx, chat, reply, op.Sticker.FileID, op.Target.ThreadID)
		r := receiptForError(err)
		r.MessageID = messageID(id)
		return r
	case "reaction":
		message, err := strconv.ParseInt(op.Reply, 10, 64)
		if err != nil {
			return channelwire.Receipt{State: "failed"}
		}
		r := receiptForError(api.SetReaction(ctx, chat, message, op.Reaction))
		r.MessageID = op.Reply
		return r
	case "sticker_set":
		page, err := api.StickerSet(ctx, op.Name, op.PageOffset, op.PageSize)
		r := receiptForError(err)
		if err == nil {
			r.State = "downloaded"
			r.Data, _ = json.Marshal(page)
		}
		return r
	case "text":
		var keyboard *channels.TelegramKeyboard
		if len(op.Buttons) != 0 {
			var row []channels.TelegramButton
			for _, b := range op.Buttons {
				row = append(row, channels.TelegramButton{Text: b.Text, Data: b.Token})
			}
			keyboard = &channels.TelegramKeyboard{Rows: [][]channels.TelegramButton{row}}
		}
		id, fallback, err := api.SendFormatted(ctx, chat, reply, op.Text, op.Entities, keyboard, op.Target.ThreadID)
		r := receiptForError(err)
		r.PlainTextFallback = fallback
		r.MessageID = messageID(id)
		return r
	case "file":
		id, err := api.SendDocument(ctx, chat, reply, op.Name, op.Data, op.Target.ThreadID)
		r := receiptForError(err)
		r.MessageID = messageID(id)
		return r
	case "download":
		if op.Media == nil {
			return channelwire.Receipt{State: "failed"}
		}
		var data []byte
		var err error
		if op.Media.PreviewFormat != "" {
			data, err = api.DownloadStickerPreview(ctx, *op.Media, op.Limit)
		} else {
			data, err = api.DownloadMedia(ctx, *op.Media, op.Limit)
		}
		r := receiptForError(err)
		if err == nil {
			r.State = "downloaded"
			r.Data = data
		}
		return r
	default:
		return channelwire.Receipt{State: "failed"}
	}
}
