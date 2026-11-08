package handoff

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"net/url"
)

func (c *Client) ChannelDeliveries(ctx context.Context, chat string, cursor ...string) ([]harness.DeliveryGroup, error) {
	var result []harness.DeliveryGroup
	path := "/api/attach/deliveries?conversation_id=" + url.QueryEscape(chat)
	if len(cursor) > 0 {
		path += "&cursor=" + url.QueryEscape(cursor[0])
	}
	err := c.request(ctx, http.MethodGet, path, nil, &result)
	return result, err
}
func (c *Client) ControlDelivery(ctx context.Context, chat string, command harness.DeliveryCommand) error {
	return c.request(ctx, http.MethodPost, "/api/attach/deliveries", map[string]any{"conversation_id": chat, "command": command}, nil)
}

func (c *Client) WakeRecords(ctx context.Context, chat string) ([]harness.WakeOpportunity, error) {
	var result []harness.WakeOpportunity
	err := c.request(ctx, http.MethodGet, "/api/attach/deliveries?scheduled=true&conversation_id="+url.QueryEscape(chat), nil, &result)
	return result, err
}
