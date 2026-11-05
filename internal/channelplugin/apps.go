package channelplugin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
)

func runLark(parent context.Context, c *Client, key channelwire.Credential) (result error) {
	parts := strings.SplitN(c.Bootstrap.Connection.ApplicationID, ":", 2)
	if len(parts) != 2 {
		return errors.New("channel: Lark requires an explicit app:tenant binding")
	}
	api, err := channels.NewLarkAPI(key.BaseURL, parts[0], key.Secret, nil)
	key.Secret = ""
	if err != nil {
		return err
	}
	bot, err := api.BotOpenID(parent)
	if err != nil {
		return errors.New("channel: Lark identity unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	var transportFailure error
	go func() {
		err := transportOutbox(ctx, c, func(ctx context.Context, op channelwire.Operation) channelwire.Receipt {
			var id string
			var err error
			switch op.Kind {
			case "text":
				var buttons []channels.LarkButton
				for _, b := range op.Buttons {
					buttons = append(buttons, channels.LarkButton{Text: b.Text, Token: b.Token})
				}
				id, err = api.Send(ctx, op.Target.ChatID, op.Reply, op.Text, buttons)
			case "file":
				id, err = api.SendDocument(ctx, op.Target.ChatID, op.Reply, op.Name, op.Data)
			case "download":
				if op.Media == nil {
					return channelwire.Receipt{State: "failed"}
				}
				data, err := api.DownloadMedia(ctx, op.Reply, *op.Media, op.Limit)
				r := receiptForError(err)
				if err == nil {
					r.State = "downloaded"
					r.Data = data
				}
				return r
			default:
				return channelwire.Receipt{State: "failed"}
			}
			r := receiptForError(err)
			r.MessageID = id
			return r
		})
		transportFailure = err
		done <- err
		cancel()
	}()
	defer func() {
		cancel()
		<-done
		if parent.Err() == nil && transportFailure != nil {
			result = transportFailure
		}
	}()
	for ctx.Err() == nil {
		err = api.Listen(ctx, func(raw []byte) (any, error) {
			e, ok := channels.DecodeLarkEvent(raw, parts[0], bot)
			if !ok {
				return map[string]string{"status": "ignored"}, nil
			}
			if e.Tenant != parts[1] {
				return nil, errors.New("channel: Lark tenant does not match this connection")
			}
			event := channelwire.Event{ID: e.Message + "/" + e.Callback, Target: channelwire.Target{Source: "lark", ApplicationID: e.App + ":" + e.Tenant, ChatID: e.Chat}, Person: e.Person, Message: e.Message, Kind: e.Kind, Text: e.Text, Trigger: e.Trigger, Callback: e.Callback, CallbackMessage: e.CallbackMessage, Media: e.Media, ReplyTo: e.ReplyTo}
			// ID must remain one opaque part in the Core protocol.
			event.ID = e.Message
			if e.Callback != "" {
				event.ID = e.Callback
			}
			if err := c.Inbound(ctx, event); err != nil {
				return nil, err
			}
			return map[string]string{"status": "ok"}, nil
		})
		if errors.Is(err, ErrCoreUnavailable) {
			return err
		}
		if !wait(ctx, time.Second) {
			break
		}
	}
	return ctx.Err()
}

func runQQ(parent context.Context, c *Client, key channelwire.Credential) (result error) {
	host, _, err := net.SplitHostPort(c.Bootstrap.Connection.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("channel: QQ requires an explicit numeric loopback listener")
	}
	api, err := channels.NewQQAPI(key.BaseURL, "", c.Bootstrap.Connection.ApplicationID, key.Secret, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type window struct {
		at  time.Time
		seq int
	}
	var mu sync.Mutex
	windows := map[string]window{}
	done := make(chan error, 1)
	var transportFailure error
	go func() {
		err := transportOutbox(ctx, c, func(ctx context.Context, op channelwire.Operation) channelwire.Receipt {
			if op.Kind != "text" {
				return channelwire.Receipt{State: "failed"}
			}
			parts := strings.SplitN(op.Target.ChatID, ":", 2)
			if len(parts) != 2 {
				return channelwire.Receipt{State: "failed"}
			}
			mu.Lock()
			w, ok := windows[op.Target.ChatID+"/"+op.Reply]
			if !ok || time.Since(w.at) > 120*time.Second || w.seq >= 5 {
				mu.Unlock()
				return channelwire.Receipt{State: "failed"}
			}
			w.seq++
			windows[op.Target.ChatID+"/"+op.Reply] = w
			mu.Unlock()
			kind := "private"
			if parts[0] == "group" {
				kind = "group"
			}
			id, err := api.Send(ctx, kind, parts[1], op.Reply, op.Text, w.seq)
			r := receiptForError(err)
			r.MessageID = id
			return r
		})
		transportFailure = err
		done <- err
		cancel()
	}()
	defer func() {
		cancel()
		<-done
		if parent.Err() == nil && transportFailure != nil {
			result = transportFailure
		}
	}()
	webhook := channels.QQWebhook{Secret: key.Secret, Handle: func(_ context.Context, raw []byte) error {
		e, ok := channels.DecodeQQEvent(raw, c.Bootstrap.Connection.ApplicationID)
		if !ok {
			return nil
		}
		mu.Lock()
		for id, w := range windows {
			if time.Since(w.at) > 120*time.Second {
				delete(windows, id)
			}
		}
		if len(windows) >= 512 {
			mu.Unlock()
			return errors.New("channel: passive reply capacity reached")
		}
		if _, ok := windows[e.Chat+"/"+e.Message]; !ok {
			windows[e.Chat+"/"+e.Message] = window{at: time.Now()}
		}
		mu.Unlock()
		return c.Inbound(ctx, channelwire.Event{ID: e.Message, Target: channelwire.Target{Source: "qq", ApplicationID: e.App, ChatID: e.Chat}, Person: e.Person, Message: e.Message, Kind: e.Kind, Text: e.Text, Trigger: true})
	}}
	key.Secret = ""
	listener, err := net.Listen("tcp", c.Bootstrap.Connection.Listen)
	if err != nil {
		return errors.New("channel: QQ loopback port unavailable")
	}
	server := &http.Server{Handler: webhook, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return errors.New("channel: QQ listener stopped")
}
