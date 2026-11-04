package handoff

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"net/url"
)

func (c *Client) Inputs(ctx context.Context, chat string) (harness.InputState, error) {
	var state harness.InputState
	err := c.request(ctx, http.MethodGet, "/api/attach/inputs?conversation_id="+url.QueryEscape(chat), nil, &state)
	return state, err
}
func (c *Client) AcceptInput(ctx context.Context, chat string, command harness.InputCommand) (harness.PendingInput, error) {
	var result harness.PendingInput
	err := c.request(ctx, http.MethodPost, "/api/attach/inputs", map[string]any{"conversation_id": chat, "operation": "accept", "command": command}, &result)
	return result, err
}
func (c *Client) CancelInput(ctx context.Context, chat, key string) (harness.PendingInput, error) {
	var result harness.PendingInput
	err := c.request(ctx, http.MethodPost, "/api/attach/inputs", map[string]string{"conversation_id": chat, "operation": "cancel", "key": key}, &result)
	return result, err
}
func (c *Client) SteerInput(ctx context.Context, chat, key, runID string) (harness.PendingInput, error) {
	var result harness.PendingInput
	err := c.request(ctx, http.MethodPost, "/api/attach/inputs", map[string]string{"conversation_id": chat, "operation": "steer", "key": key, "run_id": runID}, &result)
	return result, err
}
func (c *Client) PauseQueue(ctx context.Context, chat string, paused bool) error {
	action := "resume"
	if paused {
		action = "pause"
	}
	return c.request(ctx, http.MethodPost, "/api/attach/inputs", map[string]string{"conversation_id": chat, "operation": action}, nil)
}
func (c *Client) BranchConversation(ctx context.Context, chat string, command harness.BranchCommand) (harness.BranchResult, error) {
	var result harness.BranchResult
	err := c.request(ctx, http.MethodPost, "/api/attach/branch", map[string]any{"conversation_id": chat, "command": command}, &result)
	return result, err
}
