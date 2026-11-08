package chatui

import (
	"context"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"strings"
)

func (server *Server) sourceAnalysisAPI(w http.ResponseWriter, r *http.Request) {
	if !server.isInteractiveAttachRequest(r) {
		server.writeError(w, 403, "user_interaction_required", "分析需要已认证用户明确选择方法和来源。")
		return
	}
	if r.Method == http.MethodGet {
		preview, err := server.tasks.PreviewSourceAnalysis(r.Context(), r.URL.Query().Get("conversation_id"), harness.SourceAnalysisSelection{Skill: r.URL.Query().Get("skill"), ModelID: r.URL.Query().Get("model_id"), RunIDs: strings.Split(r.URL.Query().Get("run_ids"), ",")})
		if err != nil {
			server.fail(w, err)
		} else {
			server.writeJSON(w, 200, preview)
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		Chat      string                          `json:"conversation_id"`
		Key       string                          `json:"request_key"`
		Selection harness.SourceAnalysisSelection `json:"selection"`
		Digest    string                          `json:"digest"`
		RunID     string                          `json:"run_id"`
		Title     string                          `json:"title"`
		Body      string                          `json:"body"`
	}
	if !server.readBody(w, r, &body) {
		return
	}
	if body.Key == "" || len(body.Key) > 128 {
		server.writeError(w, 400, "input", "缺少稳定的请求标识。")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/memory") {
		result, err := server.tasks.SaveAnalysisMemory(r.Context(), body.Chat, body.RunID, body.Key, body.Title, body.Body)
		if err != nil {
			server.fail(w, err)
			return
		}
		agent, err := server.tasks.AgentIdentity(r.Context(), body.Chat)
		if err != nil {
			server.fail(w, err)
			return
		}
		server.writeJSON(w, 200, map[string]any{"receipt": result, "scope": harness.MemoryScope{Kind: "private", Owner: agent, Project: server.tasks.Config().Workspace}})
		return
	}
	preview, err := server.tasks.PreviewSourceAnalysis(r.Context(), body.Chat, body.Selection)
	if err != nil {
		server.fail(w, err)
		return
	}
	if preview.Digest != body.Digest {
		server.fail(w, harness.ErrApprovalChanged)
		return
	}
	server.startBound(w, body.Chat, body.Key, func(ctx context.Context) (harness.RunResult, error) {
		return server.tasks.AnalyzeSources(ctx, body.Chat, body.Key, body.Selection, body.Digest)
	})
}
