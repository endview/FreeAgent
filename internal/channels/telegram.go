// Package channels contains optional transport adapters. It owns no Agent,
// task, permission, persistence or execution state.
package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const telegramBodyLimit = 1 << 20

type TelegramAPI struct {
	base  string
	token string
	http  *http.Client
}

// TelegramError never formats the token-bearing request URL or the upstream
// description. Sending uncertainty is delivery uncertainty, not a reason to
// repeat the model or its tool execution.
type TelegramError struct {
	Status     int
	RetryAfter time.Duration
	Uncertain  bool
}

func (e *TelegramError) Error() string {
	return fmt.Sprintf("telegram: API failure status=%d delivery_unknown=%t", e.Status, e.Uncertain)
}

func NewTelegramAPI(base, token string, client *http.Client) (*TelegramAPI, error) {
	if base == "" {
		base = "https://api.telegram.org"
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("telegram: invalid API origin")
	}
	local := net.ParseIP(u.Hostname())
	if !(u.Scheme == "https" && u.Host == "api.telegram.org" || u.Scheme == "http" && local != nil && local.IsLoopback()) {
		return nil, errors.New("telegram: official HTTPS origin or an explicit loopback fixture is required")
	}
	if len(token) < 8 || len(token) > 256 {
		return nil, errors.New("telegram: invalid token")
	}
	for _, r := range token {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ':' || r == '_' || r == '-') {
			return nil, errors.New("telegram: invalid token")
		}
	}
	var copy http.Client
	if client != nil {
		copy = *client
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copy.Timeout = 65 * time.Second
	return &TelegramAPI{base: strings.TrimRight(base, "/"), token: token, http: &copy}, nil
}

func (api *TelegramAPI) call(ctx context.Context, method string, input, result any, delivery bool) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return errors.New("telegram: invalid request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, api.base+"/bot"+api.token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return &TelegramError{Uncertain: false}
	}
	request.Header.Set("Content-Type", "application/json")
	if ctx.Err() != nil {
		return &TelegramError{Uncertain: false}
	}
	response, err := api.http.Do(request)
	if err != nil {
		return &TelegramError{Uncertain: delivery}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, telegramBodyLimit+1))
	if err != nil || len(body) > telegramBodyLimit {
		return &TelegramError{Status: response.StatusCode, Uncertain: delivery}
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		ErrorCode  int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return &TelegramError{Status: response.StatusCode, Uncertain: delivery}
	}
	if !envelope.OK || response.StatusCode != http.StatusOK {
		status := response.StatusCode
		if envelope.ErrorCode != 0 {
			status = envelope.ErrorCode
		}
		return &TelegramError{Status: status, RetryAfter: time.Duration(min(max(envelope.Parameters.RetryAfter, 0), 3600)) * time.Second, Uncertain: delivery && status >= 500}
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return &TelegramError{Status: response.StatusCode, Uncertain: delivery}
	}
	return nil
}

type TelegramUser struct {
	ID                   int64  `json:"id"`
	IsBot                bool   `json:"is_bot"`
	Username             string `json:"username"`
	FirstName            string `json:"first_name"`
	LastName             string `json:"last_name"`
	CanReadGroupMessages bool   `json:"can_read_all_group_messages"`
}
type TelegramChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}
type TelegramEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}
type TelegramMessage struct {
	Document        *TelegramDocument `json:"document"`
	Photo           []TelegramPhoto   `json:"photo"`
	Caption         string            `json:"caption"`
	CaptionEntities []TelegramEntity  `json:"caption_entities"`
	ID              int64             `json:"message_id"`
	Date            int64             `json:"date"`
	Chat            TelegramChat      `json:"chat"`
	From            *TelegramUser     `json:"from"`
	SenderChat      *TelegramChat     `json:"sender_chat"`
	Text            string            `json:"text"`
	Entities        []TelegramEntity  `json:"entities"`
	ReplyTo         *TelegramMessage  `json:"reply_to_message"`
}
type TelegramCallback struct {
	ID      string           `json:"id"`
	From    TelegramUser     `json:"from"`
	Message *TelegramMessage `json:"message"`
	Data    string           `json:"data"`
}
type TelegramUpdate struct {
	ID       int64             `json:"update_id"`
	Message  *TelegramMessage  `json:"message"`
	Callback *TelegramCallback `json:"callback_query"`
}
type TelegramButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type TelegramKeyboard struct {
	Rows [][]TelegramButton `json:"inline_keyboard"`
}

func (api *TelegramAPI) Me(ctx context.Context) (TelegramUser, error) {
	var user TelegramUser
	err := api.call(ctx, "getMe", struct{}{}, &user, false)
	if err == nil && (user.ID <= 0 || !user.IsBot || user.Username == "") {
		err = errors.New("telegram: incomplete bot identity")
	}
	return user, err
}
func (api *TelegramAPI) Poll(ctx context.Context, offset int64, waitSeconds int) ([]TelegramUpdate, error) {
	if waitSeconds < 0 || waitSeconds > 50 {
		return nil, errors.New("telegram: invalid long-poll timeout")
	}
	var updates []TelegramUpdate
	err := api.call(ctx, "getUpdates", struct {
		Offset  int64    `json:"offset"`
		Limit   int      `json:"limit"`
		Timeout int      `json:"timeout"`
		Allowed []string `json:"allowed_updates"`
	}{offset, 100, waitSeconds, []string{"message", "callback_query"}}, &updates, false)
	if err == nil && len(updates) > 100 {
		return nil, errors.New("telegram: update batch exceeds bound")
	}
	return updates, err
}
func validTelegramText(text string, maxUnits int) bool {
	if !utf8.ValidString(text) {
		return false
	}
	units := 0
	for _, r := range text {
		units += utf16.RuneLen(r)
	}
	return units > 0 && units <= maxUnits
}
func validateKeyboard(keyboard *TelegramKeyboard) error {
	if keyboard == nil {
		return nil
	}
	if len(keyboard.Rows) > 8 {
		return errors.New("telegram: keyboard exceeds bound")
	}
	for _, row := range keyboard.Rows {
		if len(row) > 8 {
			return errors.New("telegram: keyboard row exceeds bound")
		}
		for _, b := range row {
			if !validTelegramText(b.Text, 64) || len(b.Data) < 1 || len(b.Data) > 64 {
				return errors.New("telegram: invalid callback button")
			}
		}
	}
	return nil
}
func (api *TelegramAPI) Send(ctx context.Context, chatID, replyTo int64, text string, keyboard *TelegramKeyboard) (int64, error) {
	if chatID == 0 || !validTelegramText(text, 4096) {
		return 0, errors.New("telegram: invalid text or target")
	}
	if err := validateKeyboard(keyboard); err != nil {
		return 0, err
	}
	input := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if replyTo > 0 {
		input["reply_parameters"] = map[string]any{"message_id": replyTo}
	}
	if keyboard != nil {
		input["reply_markup"] = keyboard
	}
	var result TelegramMessage
	err := api.call(ctx, "sendMessage", input, &result, true)
	if err == nil && (result.ID <= 0 || result.Chat.ID != chatID) {
		err = &TelegramError{Status: http.StatusOK, Uncertain: true}
	}
	return result.ID, err
}
func (api *TelegramAPI) Edit(ctx context.Context, chatID, messageID int64, text string, keyboard *TelegramKeyboard) error {
	if chatID == 0 || messageID <= 0 || !validTelegramText(text, 4096) {
		return errors.New("telegram: invalid edit")
	}
	if err := validateKeyboard(keyboard); err != nil {
		return err
	}
	input := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if keyboard != nil {
		input["reply_markup"] = keyboard
	}
	return api.call(ctx, "editMessageText", input, nil, true)
}
func (api *TelegramAPI) AnswerCallback(ctx context.Context, id, text string) error {
	if id == "" || len(id) > 256 || len(text) > 800 {
		return errors.New("telegram: invalid callback answer")
	}
	return api.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil, true)
}

// SplitTelegramText is bounded by UTF-16 units so non-BMP text never exceeds
// the platform bound. It performs no Markdown/HTML interpretation.
func SplitTelegramText(text string) []string {
	if text == "" {
		return nil
	}
	var result []string
	start, units := 0, 0
	for index, r := range text {
		n := utf16.RuneLen(r)
		if units+n > 4000 {
			result = append(result, text[start:index])
			start = index
			units = 0
		}
		units += n
	}
	return append(result, text[start:])
}

type TelegramDocument struct {
	ID    string `json:"file_id"`
	Name  string `json:"file_name"`
	Bytes int64  `json:"file_size"`
}
type TelegramPhoto struct {
	ID    string `json:"file_id"`
	Bytes int64  `json:"file_size"`
}

type TelegramEvent struct {
	Media         []MediaRef
	ReplyTo       string
	UpdateID      int64
	ApplicationID string
	ChatID        int64
	ChatKind      string
	PersonID      string
	DisplayName   string
	MessageID     int64
	Text          string
	Triggered     bool
	Date          int64
	CallbackID    string
	CallbackData  string
}

func TelegramEventFromUpdate(update TelegramUpdate, bot TelegramUser) (TelegramEvent, bool) {
	event := TelegramEvent{UpdateID: update.ID, ApplicationID: strconv.FormatInt(bot.ID, 10)}
	if bot.ID <= 0 || !bot.IsBot || bot.Username == "" {
		return event, false
	}
	if update.Callback != nil {
		c := update.Callback
		if c.Message == nil || c.Message.Date == 0 || c.Message.Chat.ID == 0 || c.From.ID <= 0 || c.From.IsBot || len(c.Data) > 64 {
			return event, false
		}
		if c.Message.Chat.Type != "private" && c.Message.Chat.Type != "group" && c.Message.Chat.Type != "supergroup" {
			return event, false
		}
		event.ChatID, event.ChatKind, event.MessageID = c.Message.Chat.ID, c.Message.Chat.Type, c.Message.ID
		event.PersonID = "telegram:user:" + strconv.FormatInt(c.From.ID, 10)
		event.CallbackID, event.CallbackData = c.ID, c.Data
		return event, true
	}
	m := update.Message
	if m != nil && (m.Document != nil || len(m.Photo) > 0) {
		copy := *m
		if copy.Text == "" {
			copy.Text = copy.Caption
			copy.Entities = copy.CaptionEntities
		}
		if copy.Text == "" {
			copy.Text = "[附件]"
		}
		m = &copy
		if m.Document != nil {
			event.Media = []MediaRef{{ID: m.Document.ID, Name: m.Document.Name, Bytes: m.Document.Bytes}}
		} else {
			photo := m.Photo[len(m.Photo)-1]
			event.Media = []MediaRef{{ID: photo.ID, Name: "photo.jpg", Bytes: photo.Bytes, Image: true}}
		}
	}
	if m == nil || m.Chat.ID == 0 || m.ID <= 0 || m.Text == "" || len(m.Text) > 32<<10 {
		return event, false
	}
	if m.Chat.Type != "private" && m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
		return event, false
	}
	if m.From != nil && m.From.IsBot && m.SenderChat == nil {
		return event, false
	}
	event.ChatID, event.ChatKind, event.MessageID, event.Text, event.Date = m.Chat.ID, m.Chat.Type, m.ID, m.Text, m.Date
	if m.From != nil && m.From.ID > 0 && m.SenderChat == nil {
		event.PersonID = "telegram:user:" + strconv.FormatInt(m.From.ID, 10)
		event.DisplayName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
	}
	if m.ReplyTo != nil {
		event.ReplyTo = strconv.FormatInt(m.ReplyTo.ID, 10)
	}
	event.Triggered = m.Chat.Type == "private"
	if m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.ID == bot.ID {
		event.Triggered = true
	}
	units := utf16.Encode([]rune(m.Text))
	for _, entity := range m.Entities {
		if entity.Offset < 0 || entity.Length < 1 || entity.Offset > len(units) || entity.Length > len(units)-entity.Offset {
			continue
		}
		value := string(utf16.Decode(units[entity.Offset : entity.Offset+entity.Length]))
		if entity.Type == "mention" && strings.EqualFold(value, "@"+bot.Username) {
			event.Triggered = true
		}
		if entity.Type == "bot_command" && entity.Offset == 0 {
			parts := strings.SplitN(value, "@", 2)
			if len(parts) == 1 || strings.EqualFold(parts[1], bot.Username) {
				event.Triggered = true
			}
		}
	}
	return event, true
}
