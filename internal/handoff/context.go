package handoff

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func (client *Client) ContextState(ctx context.Context, chat string) (harness.ContextState, error) {
	var state harness.ContextState
	err := client.request(ctx, http.MethodGet, "/api/attach/context?conversation_id="+url.QueryEscape(chat), nil, &state)
	return state, err
}

func (client *Client) ManageContext(ctx context.Context, chat string, command harness.ContextCommand) (harness.ContextResult, error) {
	key, err := NewSecret()
	if err != nil {
		return harness.ContextResult{}, err
	}
	copyClient := *client
	if client.HTTP != nil {
		copyHTTP := *client.HTTP
		copyClient.HTTP = &copyHTTP
	} else {
		copyClient.HTTP = &http.Client{}
	}
	copyClient.HTTP.Timeout = time.Hour
	stop := context.AfterFunc(ctx, func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.CancelRequest(cancelCtx, chat, key)
	})
	defer stop()
	var result harness.ContextResult
	err = copyClient.request(ctx, http.MethodPost, "/api/attach/context", map[string]any{
		"conversation_id": chat, "command": command, "request_key": key,
	}, &result)
	return result, err
}
