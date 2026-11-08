package channelplugin

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/endview/freeagent/internal/channels"
)

func runTelegramActivities(ctx context.Context, client *Client, api *channels.TelegramAPI) {
	if client.Bootstrap.Connection.DisableTyping {
		return
	}
	next := map[string]time.Time{}
	var failures int
	var logged time.Time
	for ctx.Err() == nil {
		request, cancel := context.WithTimeout(ctx, 2*time.Second)
		activities, err := client.Activities(request)
		cancel()
		if errors.Is(err, ErrCoreUnavailable) {
			return
		}
		now := time.Now()
		for target, until := range next {
			if now.Sub(until) > time.Minute {
				delete(next, target)
			}
		}
		if err == nil && len(activities) <= 128 {
			for _, activity := range activities {
				if activity.Target.Source != "telegram" || activity.Target.ApplicationID != client.Bootstrap.Connection.ApplicationID || activity.Action != "typing" || activity.UntilMS <= now.UnixMilli() || activity.UntilMS > now.Add(12*time.Second).UnixMilli() {
					continue
				}
				chat, err := strconv.ParseInt(activity.Target.ChatID, 10, 64)
				if err != nil || chat == 0 || now.Before(next[activity.Target.ChatID]) || len(next) >= 128 && next[activity.Target.ChatID].IsZero() {
					continue
				}
				call, done := context.WithTimeout(ctx, 2*time.Second)
				err = api.ChatAction(call, chat, "typing")
				done()
				delay := 4 * time.Second
				if err != nil {
					failures++
					var upstream *channels.TelegramError
					if errors.As(err, &upstream) {
						delay = max(delay, upstream.RetryAfter)
					}
					if now.Sub(logged) >= time.Minute {
						log.Printf("telegram: activity requests failed (%d); message delivery continues", failures)
						logged = now
					}
				}
				next[activity.Target.ChatID] = now.Add(delay)
			}
		}
		if !wait(ctx, time.Second) {
			return
		}
	}
}
