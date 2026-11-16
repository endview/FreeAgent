package handoff

import (
	"context"
	"net/url"

	"github.com/endview/freeagent/internal/harness"
)

func (c *Client) ManageConversations(ctx context.Context, q url.Values) (harness.ManagementPage, error) {
	var out harness.ManagementPage
	err := c.request(ctx, "GET", "/api/conversation-management?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) PreviewConversationManagement(ctx context.Context, cmd harness.ManagementCommand) (harness.ManagementPreview, error) {
	var out harness.ManagementPreview
	err := c.request(ctx, "POST", "/api/conversation-management", map[string]any{"operation": "preview", "command": cmd}, &out)
	return out, err
}
func (c *Client) ApplyConversationManagement(ctx context.Context, id string) (harness.ManagementResult, error) {
	var out harness.ManagementResult
	err := c.request(ctx, "POST", "/api/conversation-management", map[string]any{"operation": "execute", "id": id}, &out)
	return out, err
}
func (c *Client) FinishManagementTasks(ctx context.Context, id string) error {
	return c.request(ctx, "POST", "/api/conversation-management", map[string]any{"operation": "finish", "id": id}, nil)
}
func (c *Client) ConversationManagementResult(ctx context.Context, id string) (harness.ManagementResult, error) {
	var out harness.ManagementResult
	err := c.request(ctx, "GET", "/api/conversation-management?"+url.Values{"result": {id}}.Encode(), nil, &out)
	return out, err
}
