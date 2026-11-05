package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/channels"
	"strconv"
	"strings"
)

func (r *TelegramRuntime) downloadMedia(ctx context.Context, e channelEvent, ref channels.MediaRef, limit int64) ([]byte, error) {
	r.mu.Lock()
	api, bot := r.api, r.bot
	r.mu.Unlock()
	if api == nil || strconv.FormatInt(bot.ID, 10) != e.Target.ApplicationID {
		return nil, ErrPermissionDenied
	}
	return api.DownloadMedia(ctx, ref, limit)
}
func (r *TelegramRuntime) sendMedia(ctx context.Context, target ChannelTarget, reply string, ref AttachmentRef, raw []byte) (string, error) {
	r.mu.Lock()
	api, bot := r.api, r.bot
	r.mu.Unlock()
	if api == nil || strconv.FormatInt(bot.ID, 10) != target.ApplicationID {
		return "", &CallError{Certain: true, Message: "telegram: file target connection changed"}
	}
	chat, err := strconv.ParseInt(target.ChatID, 10, 64)
	if err != nil {
		return "", err
	}
	replyID, _ := strconv.ParseInt(reply, 10, 64)
	id, err := api.SendDocument(ctx, chat, replyID, ref.Name, raw)
	if id <= 0 {
		return "", err
	}
	return strconv.FormatInt(id, 10), err
}
func (r *AppChannelRuntime) downloadMedia(ctx context.Context, e channelEvent, ref channels.MediaRef, limit int64) ([]byte, error) {
	r.mu.Lock()
	api, app := r.lark, r.app
	r.mu.Unlock()
	if r.source != SpaceSourceLark {
		return nil, errors.New("QQ file input is not supported by this transport")
	}
	if api == nil || !strings.HasPrefix(e.Target.ApplicationID, app+":") {
		return nil, ErrPermissionDenied
	}
	return api.DownloadMedia(ctx, e.Message, ref, limit)
}
func (r *AppChannelRuntime) sendMedia(ctx context.Context, target ChannelTarget, reply string, ref AttachmentRef, raw []byte) (string, error) {
	r.mu.Lock()
	api, app := r.lark, r.app
	r.mu.Unlock()
	if r.source != SpaceSourceLark {
		return "", &CallError{Certain: true, Message: "QQ C2C/group arbitrary file upload is not supported; no public file URL is created"}
	}
	if api == nil || !strings.HasPrefix(target.ApplicationID, app+":") {
		return "", &CallError{Certain: true, Message: "lark: file target connection changed"}
	}
	return api.SendDocument(ctx, target.ChatID, reply, ref.Name, raw)
}
