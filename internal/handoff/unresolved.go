package handoff

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"net/url"
	"strconv"
)

func (client *Client) ReconcileReadDenials(ctx context.Context, digest string) (harness.UnknownReconciliation, error) {
	var result harness.UnknownReconciliation
	method := http.MethodGet
	var body any
	if digest != "" {
		method = http.MethodPost
		body = map[string]string{"digest": digest}
	}
	err := client.request(ctx, method, "/api/attach/unknown-reconcile", body, &result)
	return result, err
}

func (client *Client) Unresolved(ctx context.Context, chat string, after int64) ([]harness.UnresolvedItem, error) {
	var result []harness.UnresolvedItem
	err := client.request(ctx, http.MethodGet, "/api/attach/unresolved?conversation_id="+url.QueryEscape(chat)+"&after="+strconv.FormatInt(after, 10), nil, &result)
	return result, err
}

func (client *Client) ResolveUnresolved(ctx context.Context, chat, id, revision, action, note string) error {
	return client.request(ctx, http.MethodPost, "/api/attach/unresolved", map[string]string{"conversation_id": chat, "id": id, "revision": revision, "action": action, "note": note}, nil)
}
