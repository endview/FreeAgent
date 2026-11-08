package handoff

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) PreviewSourceAnalysis(ctx context.Context, chat string, selection harness.SourceAnalysisSelection) (harness.SourceAnalysisPreview, error) {
	var result harness.SourceAnalysisPreview
	err := c.request(ctx, http.MethodGet, "/api/attach/analyze?conversation_id="+url.QueryEscape(chat)+"&run_ids="+url.QueryEscape(strings.Join(selection.RunIDs, ","))+"&skill="+url.QueryEscape(selection.Skill)+"&model_id="+url.QueryEscape(selection.ModelID), nil, &result)
	return result, err
}
func (c *Client) AnalyzeSources(ctx context.Context, chat, key string, selection harness.SourceAnalysisSelection, digest string) error {
	return c.request(ctx, http.MethodPost, "/api/attach/analyze", map[string]any{"conversation_id": chat, "request_key": key, "selection": selection, "digest": digest}, nil)
}
