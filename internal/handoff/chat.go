package handoff

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/endview/freeagent/internal/harness"
)

// ChatCommand transports explicit terminal menu actions. Execution and durable
// validation remain in TaskService; this is not another task model.
type ChatCommand struct {
	Action string          `json:"action"`
	Chat   string          `json:"chat,omitempty"`
	Run    string          `json:"run,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

func (client *Client) ChatCommand(ctx context.Context, action, chat, run string, value, result any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return client.request(ctx, http.MethodPost, "/api/attach/chat-command", ChatCommand{action, chat, run, raw}, result)
}

func (client *Client) ContinueReviewed(ctx context.Context, chat, run string, suggestion *harness.CollaborationSuggestion, approved bool) error {
	return client.request(ctx, http.MethodPost, "/api/attach/continue", struct {
		Chat       string                           `json:"conversation_id"`
		Run        string                           `json:"run_id"`
		Suggestion *harness.CollaborationSuggestion `json:"suggestion,omitempty"`
		Approved   bool                             `json:"approved"`
	}{chat, run, suggestion, approved}, nil)
}

func (client *Client) CancelRequest(ctx context.Context, chat, binding string) error {
	return client.request(ctx, http.MethodPost, "/api/attach/cancel", map[string]string{
		"conversation_id": chat, "binding": binding,
	}, nil)
}
