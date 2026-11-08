package channels

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/channelwire"
	_ "golang.org/x/image/webp"
)

type TelegramSticker struct {
	ID        string             `json:"file_id"`
	UniqueID  string             `json:"file_unique_id"`
	Width     int                `json:"width"`
	Height    int                `json:"height"`
	Animated  bool               `json:"is_animated"`
	Video     bool               `json:"is_video"`
	Emoji     string             `json:"emoji"`
	Set       string             `json:"set_name"`
	Bytes     int64              `json:"file_size"`
	Thumbnail *TelegramThumbnail `json:"thumbnail"`
}
type TelegramThumbnail struct {
	ID     string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int64  `json:"file_size"`
}

func (s TelegramSticker) Wire() (channelwire.Sticker, error) {
	format := "webp"
	if s.Animated {
		format = "tgs"
	}
	if s.Video {
		format = "webm"
	}
	result := channelwire.Sticker{FileID: s.ID, UniqueID: s.UniqueID, Width: s.Width, Height: s.Height, Format: format, Emoji: s.Emoji, SetName: s.Set, Bytes: s.Bytes}
	if !validID(s.ID) || !validID(s.UniqueID) || s.Width < 1 || s.Height < 1 || s.Width > 4096 || s.Height > 4096 || s.Bytes < 0 || s.Bytes > 16<<20 || len(s.Emoji) > 128 || len(s.Set) > 128 || s.Animated && s.Video {
		return result, errors.New("telegram: invalid sticker metadata")
	}
	if s.Thumbnail != nil && validID(s.Thumbnail.ID) && s.Thumbnail.Width > 0 && s.Thumbnail.Width <= 1024 && s.Thumbnail.Height > 0 && s.Thumbnail.Height <= 1024 && s.Thumbnail.Bytes >= 0 && s.Thumbnail.Bytes <= 1<<20 {
		result.Thumbnail = &channelwire.MediaRef{ID: s.Thumbnail.ID, Name: "sticker-preview.png", Bytes: s.Thumbnail.Bytes, Image: true, PreviewFormat: "image"}
	}
	if format == "webp" {
		result.Preview = &channelwire.MediaRef{ID: s.ID, Name: "preview.png", Bytes: s.Bytes, Image: true, PreviewFormat: "webp"}
	} else {
		result.Preview = result.Thumbnail
	}
	return result, nil
}

func StickerSetName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if strings.Contains(name, "://") {
		u, err := url.Parse(name)
		if err != nil || u.Scheme != "https" || u.Host != "t.me" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/addstickers/") {
			return "", errors.New("telegram: use a sticker set name or https://t.me/addstickers/name")
		}
		name = strings.TrimPrefix(u.Path, "/addstickers/")
	}
	if len(name) < 1 || len(name) > 128 {
		return "", errors.New("telegram: invalid sticker set name")
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "", errors.New("telegram: invalid sticker set name")
		}
	}
	return name, nil
}

func (api *TelegramAPI) StickerSet(ctx context.Context, name string, offset, limit int) (channelwire.StickerPage, error) {
	name, err := StickerSetName(name)
	if err != nil {
		return channelwire.StickerPage{}, err
	}
	if offset < 0 || offset > 10000 || limit < 1 || limit > 24 {
		return channelwire.StickerPage{}, errors.New("telegram: invalid sticker page")
	}
	var result struct {
		Name     string            `json:"name"`
		Title    string            `json:"title"`
		Stickers []TelegramSticker `json:"stickers"`
	}
	if err := api.call(ctx, "getStickerSet", map[string]string{"name": name}, &result, false); err != nil {
		return channelwire.StickerPage{}, err
	}
	if !strings.EqualFold(result.Name, name) || len(result.Title) > 512 || len(result.Stickers) > 10000 {
		return channelwire.StickerPage{}, errors.New("telegram: invalid sticker set response")
	}
	page := channelwire.StickerPage{Name: result.Name, Title: result.Title, Total: len(result.Stickers), Offset: offset, Stickers: []channelwire.Sticker{}}
	for _, s := range result.Stickers[min(offset, len(result.Stickers)):min(offset+limit, len(result.Stickers))] {
		wire, err := s.Wire()
		if err != nil {
			return page, err
		}
		if wire.SetName == "" {
			wire.SetName = result.Name
		} else if !strings.EqualFold(wire.SetName, result.Name) {
			return page, errors.New("telegram: sticker belongs to a different set")
		}
		page.Stickers = append(page.Stickers, wire)
	}
	return page, nil
}

func (api *TelegramAPI) SendSticker(ctx context.Context, chat, reply int64, fileID string) (int64, error) {
	if chat == 0 || reply < 0 || !validID(fileID) {
		return 0, errors.New("telegram: invalid sticker target or file")
	}
	input := map[string]any{"chat_id": chat, "sticker": fileID}
	if reply > 0 {
		input["reply_parameters"] = map[string]int64{"message_id": reply}
	}
	var result TelegramMessage
	if err := api.call(ctx, "sendSticker", input, &result, true); err != nil {
		return 0, err
	}
	if result.ID < 1 || result.Chat.ID != chat {
		return 0, &TelegramError{Uncertain: true}
	}
	return result.ID, nil
}

// Bots can use one ordinary reaction. The platform may further restrict a
// chat's available reactions, and its rejection is preserved as a failure.
const TelegramReactions = "👍 👎 ❤ 🔥 🥰 👏 😁 🤔 🤯 😱 🤬 😢 🎉 🤩 🤮 💩 🙏 👌 🕊 🤡 🥱 🥴 😍 🐳 ❤‍🔥 🌚 🌭 💯 🤣 ⚡ 🍌 🏆 💔 🤨 😐 🍓 🍾 💋 🖕 😈 😴 😭 🤓 👻 👨‍💻 👀 🎃 🙈 😇 😨 🤝 ✍ 🤗 🫡 🎅 🎄 ☃ 💅 🤪 🗿 🆒 💘 🙉 🦄 😘 💊 🙊 😎 👾 🤷‍♂ 🤷 🤷‍♀ 😡"

func ValidTelegramReaction(emoji string) bool {
	if emoji == "" {
		return true
	}
	for _, s := range strings.Fields(TelegramReactions) {
		if emoji == s {
			return true
		}
	}
	return false
}
func (api *TelegramAPI) SetReaction(ctx context.Context, chat, message int64, emoji string) error {
	if chat == 0 || message < 1 || !ValidTelegramReaction(emoji) {
		return errors.New("telegram: invalid reaction")
	}
	reactions := []map[string]string{}
	if emoji != "" {
		reactions = append(reactions, map[string]string{"type": "emoji", "emoji": emoji})
	}
	var ok bool
	if err := api.call(ctx, "setMessageReaction", map[string]any{"chat_id": chat, "message_id": message, "reaction": reactions, "is_big": false}, &ok, true); err != nil {
		return err
	}
	if !ok {
		return &TelegramError{Uncertain: true}
	}
	return nil
}

func stickerPreview(raw []byte) ([]byte, error) {
	if len(raw) > 8<<20 {
		return nil, errors.New("telegram: preview exceeds byte limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "webp" && format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 4<<20 {
		return nil, errors.New("telegram: invalid or oversized sticker preview")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("telegram: cannot decode sticker preview")
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil || b.Len() > 16<<20 {
		return nil, errors.New("telegram: cannot encode bounded sticker preview")
	}
	return b.Bytes(), nil
}

func (api *TelegramAPI) DownloadStickerPreview(ctx context.Context, ref channelwire.MediaRef, limit int64) ([]byte, error) {
	ref.PreviewFormat = ""
	raw, err := api.DownloadMedia(ctx, ref, min(limit, 8<<20))
	if err != nil {
		return nil, err
	}
	data, err := stickerPreview(raw)
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("telegram: converted preview exceeds limit")
	}
	return data, err
}

type TelegramReactionType struct {
	Type   string `json:"type"`
	Emoji  string `json:"emoji"`
	Custom string `json:"custom_emoji_id"`
}
type TelegramReaction struct {
	Chat    TelegramChat           `json:"chat"`
	Message int64                  `json:"message_id"`
	Date    int64                  `json:"date"`
	User    *TelegramUser          `json:"user"`
	Actor   *TelegramChat          `json:"actor_chat"`
	Old     []TelegramReactionType `json:"old_reaction"`
	New     []TelegramReactionType `json:"new_reaction"`
}

type TelegramReactionCount struct {
	Chat      TelegramChat `json:"chat"`
	Message   int64        `json:"message_id"`
	Date      int64        `json:"date"`
	Reactions []struct {
		Type  TelegramReactionType `json:"type"`
		Total int64                `json:"total_count"`
	} `json:"reactions"`
}

func telegramReactionCountEvent(update TelegramUpdate, bot TelegramUser) (TelegramEvent, bool) {
	r := update.ReactionCount
	if r == nil || r.Message <= 0 || r.Chat.ID == 0 || r.Date <= 0 || len(r.Reactions) > 128 || r.Chat.Type != "private" && r.Chat.Type != "group" && r.Chat.Type != "supergroup" {
		return TelegramEvent{}, false
	}
	counts := []channelwire.ReactionCount{}
	seen := map[string]bool{}
	for _, item := range r.Reactions {
		values := telegramReactionValues([]TelegramReactionType{item.Type})
		if item.Type.Type == "paid" {
			values = []string{"paid"}
		}
		if len(values) != 1 || item.Total < 0 || item.Total > 1<<53 || seen[values[0]] {
			return TelegramEvent{}, false
		}
		seen[values[0]] = true
		counts = append(counts, channelwire.ReactionCount{Value: values[0], Count: item.Total})
	}
	return TelegramEvent{UpdateID: update.ID, ApplicationID: strconv.FormatInt(bot.ID, 10), ChatID: r.Chat.ID, ChatKind: r.Chat.Type, MessageID: r.Message, Date: r.Date, Reaction: &channelwire.Reaction{Message: strconv.FormatInt(r.Message, 10), Date: r.Date, Anonymous: true, CountSnapshot: true, Counts: counts}}, true
}

func telegramReactionValues(items []TelegramReactionType) []string {
	result := []string{}
	for _, item := range items {
		if item.Type == "emoji" && len(item.Emoji) <= 128 {
			result = append(result, item.Emoji)
		} else if item.Type == "custom_emoji" && validID(item.Custom) {
			result = append(result, "custom:"+item.Custom)
		}
	}
	return result
}
func telegramReactionEvent(update TelegramUpdate, bot TelegramUser) (TelegramEvent, bool) {
	r := update.Reaction
	if r == nil || r.Message <= 0 || r.Chat.ID == 0 || r.Date <= 0 || len(r.Old) > 32 || len(r.New) > 32 || r.Chat.Type != "private" && r.Chat.Type != "group" && r.Chat.Type != "supergroup" {
		return TelegramEvent{}, false
	}
	if r.User != nil && r.User.IsBot {
		return TelegramEvent{}, false
	}
	e := TelegramEvent{UpdateID: update.ID, ApplicationID: strconv.FormatInt(bot.ID, 10), ChatID: r.Chat.ID, ChatKind: r.Chat.Type, MessageID: r.Message, Date: r.Date, Reaction: &channelwire.Reaction{Message: strconv.FormatInt(r.Message, 10), Old: telegramReactionValues(r.Old), New: telegramReactionValues(r.New), Date: r.Date, Anonymous: r.User == nil || r.Actor != nil}}
	if r.User != nil && r.User.ID > 0 && r.Actor == nil {
		e.PersonID = "telegram:user:" + strconv.FormatInt(r.User.ID, 10)
	}
	return e, true
}
