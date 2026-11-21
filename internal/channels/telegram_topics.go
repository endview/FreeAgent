package channels

import (
	"errors"
	"strconv"
)

func telegramTopic(m *TelegramMessage) string {
	if m.Chat.Type != "supergroup" || !m.Chat.Forum {
		return ""
	}
	if !m.Topic || m.ThreadID == 1 {
		return "general"
	}
	if m.ThreadID > 1 {
		return strconv.FormatInt(m.ThreadID, 10)
	}
	return "invalid-topic"
}

func telegramThread(input map[string]any, thread []string) error {
	if len(thread) == 0 || thread[0] == "" || thread[0] == "general" {
		return nil
	}
	id, err := strconv.ParseInt(thread[0], 10, 64)
	if err != nil || id <= 1 || strconv.FormatInt(id, 10) != thread[0] {
		return errors.New("telegram: invalid topic")
	}
	input["message_thread_id"] = id
	return nil
}
