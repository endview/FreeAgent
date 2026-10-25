package handoff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/endview/freeagent/internal/harness"
)

func (c *Client) Commands(ctx context.Context) ([]harness.CommandRecord, error) {
	var records []harness.CommandRecord
	err := c.request(ctx, http.MethodGet, "/api/attach/commands", nil, &records)
	return records, err
}
func (c *Client) PollCommand(ctx context.Context, id string, cursor int64, limit int) (harness.CommandRecord, error) {
	var record harness.CommandRecord
	err := c.request(ctx, http.MethodGet, "/api/attach/commands?id="+url.QueryEscape(id)+"&cursor="+strconv.FormatInt(cursor, 10), nil, &record)
	return record, err
}
func (c *Client) StopCommand(ctx context.Context, id string) (harness.CommandRecord, error) {
	var record harness.CommandRecord
	err := c.request(ctx, http.MethodPost, "/api/attach/commands", map[string]any{"action": "stop", "id": id}, &record)
	return record, err
}
func (c *Client) StartCommand(ctx context.Context, chat, key string, arguments json.RawMessage) (harness.RunResult, error) {
	var result harness.RunResult
	err := c.request(ctx, http.MethodPost, "/api/attach/commands", map[string]any{"action": "start", "conversation_id": chat, "request_key": key, "arguments": arguments}, &result)
	return result, err
}
