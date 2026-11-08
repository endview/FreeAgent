package handoff

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
)

func (c *Client) ChannelStickerCommand(ctx context.Context, chat string, command harness.StickerCommand) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.request(ctx, http.MethodPost, "/api/attach/stickers", map[string]any{"conversation_id": chat, "command": command}, &result)
	return result, err
}
